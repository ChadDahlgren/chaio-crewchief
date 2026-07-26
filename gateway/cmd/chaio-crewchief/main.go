// Command chaio-crewchief runs the model-delegation gateway.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/archive"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/cli"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/engine"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/mcpserve"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/provider"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/rates"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/registry"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/routing"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/server"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/store"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/types"
)

// liveRegistry lets a hot-reloaded registry.Registry be swapped underneath
// the long-lived Engine/server without either holding a stale reference.
type liveRegistry struct {
	current atomic.Value // types.Registry
}

func newLiveRegistry(initial types.Registry) *liveRegistry {
	l := &liveRegistry{}
	l.current.Store(initial)
	return l
}

func (l *liveRegistry) set(r types.Registry) { l.current.Store(r) }
func (l *liveRegistry) Get(name string) (types.Preset, bool) {
	return l.current.Load().(types.Registry).Get(name)
}
func (l *liveRegistry) Default() (types.Preset, bool) {
	return l.current.Load().(types.Registry).Default()
}
func (l *liveRegistry) List() []types.Preset { return l.current.Load().(types.Registry).List() }

// liveRates lets a hot-reloaded rates.Table be swapped underneath the
// long-lived Engine/server, same pattern as liveRegistry.
type liveRates struct {
	current atomic.Value // rates.Table
}

func newLiveRates(initial rates.Table) *liveRates {
	l := &liveRates{}
	l.current.Store(initial)
	return l
}

func (l *liveRates) set(t rates.Table) { l.current.Store(t) }
func (l *liveRates) Price(model string, promptTokens, outputTokens int) float64 {
	return l.current.Load().(rates.Table).Price(model, promptTokens, outputTokens)
}
func (l *liveRates) Counterfactual(promptTokens, outputTokens int) float64 {
	return l.current.Load().(rates.Table).Counterfactual(promptTokens, outputTokens)
}

// serviceName is the single place the display/log name lives, so the
// service can be renamed without hunting through the codebase.
const serviceName = "chaio-crewchief"

// version is overridden at build time:
//
//	go build -ldflags "-X main.version=v0.4.0"
//
// It stays "dev" for local builds so a bug report from an untagged binary is
// obvious rather than misleadingly precise.
var version = "dev"

// defaultAddr binds loopback only. Crew Chief has no authentication and its
// process environment holds provider API keys, so anyone who can reach the
// port can spend the operator's money. Exposing it to a network is a
// deliberate choice the operator makes with --addr, never a default.
const defaultAddr = "127.0.0.1:8181"

func main() {
	// Subcommands: `chaio-crewchief serve` (default when omitted), `chaio-crewchief mcp`,
	// `chaio-crewchief doctor`, `chaio-crewchief usage`. Bare `chaio-crewchief [flags]` still serves,
	// so existing systemd units keep working.
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "mcp":
			if err := mcpserve.Run(context.Background(), version); err != nil {
				log.Fatalf("mcp: %v", err)
			}
			return
		case "doctor":
			os.Exit(cli.Doctor(os.Stdout, os.Args[2:]))
		case "usage":
			os.Exit(cli.Usage(os.Stdout, os.Args[2:]))
		case "version", "--version", "-v":
			fmt.Printf("%s %s\n", serviceName, version)
			return
		case "serve":
			os.Args = append(os.Args[:1], os.Args[2:]...)
		}
	}
	serve()
}

// isLoopback reports whether addr binds only the local host. A bare-port or
// wildcard address (":8181", "0.0.0.0:8181") accepts remote connections.
func isLoopback(addr string) bool {
	host := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func serve() {
	dbPath := flag.String("db", "./"+serviceName+".db", "sqlite database path")
	modelsPath := flag.String("models", "./models.yaml", "registry YAML path")
	ratesPath := flag.String("rates", "./rates.yaml", "cost rates YAML path")
	routingPath := flag.String("routing", "./routing.yaml", "lang-to-model routing YAML path (optional)")
	archivePath := flag.String("archive", "./archive", "archive root directory")
	addr := flag.String("addr", defaultAddr, "listen address; use :8181 to accept connections from other hosts (no auth — trusted networks only)")
	flag.Parse()

	initialReg, err := registry.LoadRegistry(*modelsPath)
	if err != nil {
		log.Fatalf("load registry: %v", err)
	}
	reg := newLiveRegistry(initialReg)

	initialRates, err := rates.Load(*ratesPath)
	if err != nil {
		log.Fatalf("load rates: %v", err)
	}
	if _, statErr := os.Stat(*ratesPath); statErr != nil {
		log.Printf("warning: rates file %s not found, all attempts price at $0 (local)", *ratesPath)
	}
	rt := newLiveRates(initialRates)

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	arch, err := archive.New(*archivePath)
	if err != nil {
		log.Fatalf("open archiver: %v", err)
	}

	router, err := routing.Load(*routingPath)
	if err != nil {
		log.Fatalf("load routing table: %v", err)
	}

	prov := provider.New()
	eng := engine.NewWithRouter(reg, router, prov, st, arch, rt)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := registry.Watch(ctx, *modelsPath, func(newReg types.Registry) {
			reg.set(newReg)
			log.Printf("registry reloaded from %s", *modelsPath)
		}); err != nil {
			log.Printf("registry watch stopped: %v", err)
		}
	}()
	go func() {
		if err := rates.Watch(ctx, *ratesPath, func(newRates rates.Table) {
			rt.set(newRates)
			log.Printf("rates reloaded from %s", *ratesPath)
		}); err != nil {
			log.Printf("rates watch stopped: %v", err)
		}
	}()

	if !isLoopback(*addr) {
		log.Printf("WARNING: listening on %s, which is reachable from other hosts. "+
			"%s has no authentication and holds provider API keys — anyone who can reach "+
			"this port can spend them. Use a trusted network only.", *addr, serviceName)
	}

	handler := server.New(eng, st, reg, arch, rt)
	srv := &http.Server{Addr: *addr, Handler: handler}

	go func() {
		log.Printf("%s listening on %s (models=%s db=%s archive=%s)", serviceName, *addr, *modelsPath, *dbPath, *archivePath)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh
	log.Println("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	cancel()
}
