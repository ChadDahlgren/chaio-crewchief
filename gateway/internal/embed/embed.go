// Package embed runs Crew Chief inside the calling process.
//
// `chaio-crewchief mcp` was a pure HTTP proxy, so the plugin registered fine
// and then failed every tool call unless the user happened to be running
// `serve` somewhere. Nothing in the install path said so. Embedded mode is the
// default answer: the same components, wired the same way, served on an
// ephemeral loopback port that dies with the process.
//
// It reuses internal/server rather than calling the engine directly so that
// both modes share one set of handlers. Tool behavior is then identical by
// construction instead of by discipline — and the /stats and /history
// aggregation, which is where a divergence would quietly make `usage` and
// `crewchief_stats` disagree about money, exists exactly once.
//
// The cost is a real listener. It binds 127.0.0.1 on a kernel-assigned port
// with no authentication, so any local process can reach it while it is up.
// That is documented in README and SECURITY rather than hidden.
package embed

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/archive"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/chome"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/engine"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/ownership"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/provider"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/rates"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/registry"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/routing"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/server"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/store"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/types"
)

// Config selects where the embedded instance keeps its files.
type Config struct {
	Paths chome.Paths
}

// Instance is a running embedded gateway.
type Instance struct {
	// BaseURL is the loopback address of the in-process server.
	BaseURL string
	// Owner is this process's ownership lock, held for the instance's life.
	Owner *ownership.Owner
	// ModelsConfigured reports whether a registry was loaded. False means the
	// server is running but delegation will refuse with guidance.
	ModelsConfigured bool

	srv      *http.Server
	st       *store.SQLite
	closeOne sync.Once
	closeErr error
}

// emptyRegistry stands in for a missing models.yaml. A fresh install has none,
// and failing to start would surface to Claude Code as an opaque "server
// failed to start" with nothing actionable in it. Starting empty lets health,
// models, and delegate each answer with the path to fix.
type emptyRegistry struct{}

func (emptyRegistry) Get(string) (types.Preset, bool) { return types.Preset{}, false }
func (emptyRegistry) Default() (types.Preset, bool)   { return types.Preset{}, false }
func (emptyRegistry) List() []types.Preset            { return nil }

// Start wires the components and serves them on a loopback port.
//
// It fails on exactly two things: a store it cannot open, and a config file
// that exists but is malformed. A missing config is not an error; a malformed
// one must never be read as "nothing configured", because that sends the user
// hunting in the wrong place for a YAML typo.
//
// ctx bounds only the startup orphan-reaping pass; it does not control the
// server's lifetime. Cancelling it after Start returns has no effect on the
// running server — Close is the only shutdown path.
func Start(ctx context.Context, cfg Config) (*Instance, error) {
	p := cfg.Paths
	if err := os.MkdirAll(p.Home, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", p.Home, err)
	}

	reg, configured, err := loadRegistry(p.Models)
	if err != nil {
		return nil, err
	}

	rt, err := rates.Load(p.Rates)
	if err != nil {
		return nil, fmt.Errorf("load rates %s: %w", p.Rates, err)
	}
	// `serve` has warned about this since it shipped; embedded mode did not,
	// and embedded mode is the path a new user actually takes. Without rates
	// there is no frontier price, so `usage` can only report "savings: n/a".
	// On a fresh ledger new attempts also price at $0; on a ledger that
	// accumulated costs under a rates file that has since gone missing the
	// recorded spend stays put, because cost_usd is priced per attempt at
	// write time and never recomputed — so that n/a sits above a real number.
	// Not fatal: a purely local roster is a legitimate way to run, and the
	// ledger is still correct about tokens.
	if _, statErr := os.Stat(p.Rates); statErr != nil {
		log.Printf("warning: rates file %s not found, all attempts price at $0 (local)", p.Rates)
	}

	router, err := routing.Load(p.Routing)
	if err != nil {
		return nil, fmt.Errorf("load routing %s: %w", p.Routing, err)
	}

	st, err := store.Open(p.DB)
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", p.DB, err)
	}

	// Reap before acquiring, not after. Lock files outlive their processes and
	// pids get reused, so if a dead owner had the pid we are about to draw,
	// acquiring first would leave us holding its lock file — its abandoned rows
	// would read as live and stay `running` forever. Creating the directory is
	// Acquire's job normally; doing it here keeps OwnerAlive able to answer at
	// all, since a missing lockDir means "unsure, assume alive" for every pid.
	if err := ownership.EnsureDir(p.Locks); err != nil {
		log.Printf("warning: %v; orphan reaping may be skipped this run", err)
	}
	if n, err := st.ReapOrphans(ctx, p.Locks, ownership.Host()); err != nil {
		log.Printf("warning: reaping orphaned requests failed: %v", err)
	} else if n > 0 {
		log.Printf("failed %d orphaned request(s) left by exited processes", n)
	}

	owner, err := ownership.Acquire(p.Locks)
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("acquire ownership lock: %w", err)
	}
	// Every request this instance records is stamped with this owner, so a
	// later run can tell work we abandoned from work still in flight.
	st.AssumeOwnership(owner.PID(), ownership.Host())

	arch, err := archive.New(p.Archive)
	if err != nil {
		owner.Release()
		st.Close()
		return nil, fmt.Errorf("open archive %s: %w", p.Archive, err)
	}

	eng := engine.NewWithRouter(reg, router, provider.New(), st, arch, rt)
	handler := server.New(eng, st, reg, arch, rt)

	// Port 0: the kernel picks a free port, so concurrent sessions never
	// collide and nothing has to be configured.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		owner.Release()
		st.Close()
		return nil, fmt.Errorf("listen on loopback: %w", err)
	}

	srv := &http.Server{Handler: handler}
	inst := &Instance{
		BaseURL:          "http://" + ln.Addr().String(),
		Owner:            owner,
		ModelsConfigured: configured,
		srv:              srv,
		st:               st,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("embedded server stopped: %v", err)
		}
	}()
	return inst, nil
}

// loadRegistry returns the registry and whether one was actually configured.
//
// "Configured" means a roster with something in it, not merely a file that
// parsed. `init` deliberately writes `models: []` with every preset commented
// out, and that parses fine — so keying off the absence of an error made
// running the documented setup step strictly worse than doing nothing: with no
// config at all delegation named the file to create, and after `init` the same
// call returned a bare 500. An emptied hand-written models.yaml had the same
// problem. Counting presets covers both.
func loadRegistry(path string) (types.Registry, bool, error) {
	reg, err := registry.LoadRegistry(path)
	if err == nil {
		return reg, len(reg.List()) > 0, nil
	}
	if errors.Is(err, registry.ErrNotFound) {
		return emptyRegistry{}, false, nil
	}
	return nil, false, fmt.Errorf("load registry %s: %w", path, err)
}

// Close shuts the server, releases the ownership lock, and closes the store.
// It is safe to call more than once, and safe to call concurrently: a
// sync.Once guarantees the shutdown sequence runs exactly once even if, say,
// a defer races a signal handler, and every caller — first or not — gets the
// same outcome back rather than a bare nil on the second call.
func (i *Instance) Close() error {
	if i == nil {
		return nil
	}
	i.closeOne.Do(func() {
		var firstErr error
		if err := i.srv.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			firstErr = err
		}
		if err := i.Owner.Release(); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := i.st.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		i.closeErr = firstErr
	})
	return i.closeErr
}
