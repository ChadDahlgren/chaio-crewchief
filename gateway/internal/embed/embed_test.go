package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/chome"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/ownership"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/registry"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/store"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/types"
)

func startIn(t *testing.T, home string) *Instance {
	t.Helper()
	inst, err := Start(context.Background(), Config{Paths: chome.ResolveIn(home)})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { inst.Close() })
	return inst
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s error = %v", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(b)
}

func TestStartWithNoConfigServesHealth(t *testing.T) {
	inst := startIn(t, t.TempDir())

	if !strings.HasPrefix(inst.BaseURL, "http://127.0.0.1:") {
		t.Errorf("BaseURL = %q, want a loopback URL", inst.BaseURL)
	}
	code, _ := get(t, inst.BaseURL+"/health")
	if code != http.StatusOK {
		t.Errorf("GET /health = %d, want 200", code)
	}
}

// GET /models is server.New's `json.NewEncoder(w).Encode(reg.List())`, and
// emptyRegistry.List() returns a nil []types.Preset — so with no models.yaml
// the body is the literal JSON encoding of that: "null". Assert the exact
// shape rather than tolerating either "null" or "[]", since a test that
// accepts both shapes verifies neither.
func TestStartWithNoConfigHasEmptyRoster(t *testing.T) {
	inst := startIn(t, t.TempDir())
	code, body := get(t, inst.BaseURL+"/models")
	if code != http.StatusOK {
		t.Fatalf("GET /models = %d, want 200", code)
	}
	if got := strings.TrimSpace(body); got != "null" {
		t.Errorf("GET /models body = %q, want %q", got, "null")
	}
	var models []types.Preset
	if err := json.Unmarshal([]byte(body), &models); err != nil {
		t.Fatalf("unmarshal /models body: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("GET /models returned %d presets with no models.yaml, want 0", len(models))
	}
}

func TestStartWithNoConfigReportsModelsNotConfigured(t *testing.T) {
	inst := startIn(t, t.TempDir())
	if inst.ModelsConfigured {
		t.Error("ModelsConfigured = true with no models.yaml, want false")
	}
}

func TestStartWithValidConfigReportsModelsConfigured(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "models.yaml"), []byte(`
models:
  - name: a
    base_url: http://127.0.0.1:1
    default: true
`), 0o600); err != nil {
		t.Fatal(err)
	}
	inst := startIn(t, home)
	if !inst.ModelsConfigured {
		t.Error("ModelsConfigured = false with a valid models.yaml, want true")
	}
	// Prove the fixture actually loaded, rather than merely proving
	// ModelsConfigured is hardcoded true: the preset written above must show
	// up in the served roster.
	_, body := get(t, inst.BaseURL+"/models")
	if !strings.Contains(body, "\"name\":\"a\"") {
		t.Errorf("GET /models = %s, want it to contain the configured preset %q", body, "a")
	}
}

func TestStartCreatesHomeAndArchive(t *testing.T) {
	home := filepath.Join(t.TempDir(), "nested", "home")
	startIn(t, home)

	for _, p := range []string{home, filepath.Join(home, "archive"), filepath.Join(home, "locks")} {
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			t.Errorf("%s not created as a directory: err = %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "models.yaml")); !os.IsNotExist(err) {
		t.Error("Start created a models.yaml; it must never write config")
	}
}

func TestStartFailsOnMalformedRegistry(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "models.yaml"), []byte("models: [oh no: :"), 0o600); err != nil {
		t.Fatal(err)
	}
	inst, err := Start(context.Background(), Config{Paths: chome.ResolveIn(home)})
	if err == nil {
		inst.Close()
		t.Fatal("Start() with malformed models.yaml = nil error, want failure")
	}
	// Pin the reason, not just that Start failed: a bad path, an unopenable
	// store, or a listener failure would also make err non-nil, and none of
	// those proves the invariant under test — that a malformed config is
	// never mistaken for a missing one.
	if errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("Start() error = %v, want anything but ErrNotFound for a malformed file", err)
	}
	if !errors.Is(err, registry.ErrInvalidYAML) {
		t.Fatalf("Start() error = %v, want it to wrap registry.ErrInvalidYAML", err)
	}
}

func TestCloseReleasesPortAndIsIdempotent(t *testing.T) {
	inst := startIn(t, t.TempDir())
	url := inst.BaseURL
	if err := inst.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := http.Get(url + "/health"); err == nil {
		t.Error("server still answering after Close()")
	}
	if err := inst.Close(); err != nil {
		t.Errorf("second Close() error = %v, want nil", err)
	}
}

func TestCloseIsSafeForConcurrentCallers(t *testing.T) {
	inst := startIn(t, t.TempDir())

	const n = 8
	errs := make(chan error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			errs <- inst.Close()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Close() error = %v, want nil", err)
		}
	}
}

func TestEachStartGetsItsOwnPort(t *testing.T) {
	a := startIn(t, t.TempDir())
	b := startIn(t, t.TempDir())
	if a.BaseURL == b.BaseURL {
		t.Errorf("both instances bound %s; ports must be kernel-assigned", a.BaseURL)
	}

	// A hardcoded incrementing counter would also make the URLs differ.
	// Prove both are real, independently live listeners with non-zero ports.
	for _, inst := range []*Instance{a, b} {
		u, err := url.Parse(inst.BaseURL)
		if err != nil {
			t.Fatalf("parse %q: %v", inst.BaseURL, err)
		}
		if u.Port() == "" || u.Port() == "0" {
			t.Errorf("BaseURL = %q, want a non-zero kernel-assigned port", inst.BaseURL)
		}
		code, _ := get(t, inst.BaseURL+"/health")
		if code != http.StatusOK {
			t.Errorf("GET %s/health = %d, want 200", inst.BaseURL, code)
		}
	}
}

// The same guard has to cover a hand-written models.yaml someone emptied, which
// is not a file `init` would ever produce.
func TestStartWithEmptyRosterReportsModelsNotConfigured(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "models.yaml"), []byte("models: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inst := startIn(t, home)
	if inst.ModelsConfigured {
		t.Error("ModelsConfigured = true with an empty roster, want false")
	}
}

// Embedded mode never warned that pricing was off, so a new user got a ledger
// where every attempt cost $0.00 and `usage` reported "savings: $0.00 (0.0%)"
// with nothing anywhere saying why. `serve` has warned about this all along.
func TestStartWarnsWhenRatesFileMissing(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	home := t.TempDir()
	startIn(t, home)

	if !strings.Contains(buf.String(), "all attempts price at $0") {
		t.Errorf("Start() logged %q, want a warning that the rates file is missing", buf.String())
	}
}

func TestStartDoesNotWarnWhenRatesFilePresent(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "rates.yaml"), []byte("models: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	startIn(t, home)

	if strings.Contains(buf.String(), "all attempts price at $0") {
		t.Errorf("Start() warned about rates despite the file existing: %q", buf.String())
	}
}

// A dead owner's rows must be reaped even when this process drew that owner's
// pid — the realistic case being a reboot, which reallocates low pids while
// lock files survive on disk. Start reaps before it acquires for exactly this
// reason: acquiring first leaves this process holding the dead owner's lock
// file, so OwnerAlive reports the dead owner alive and its rows stay `running`
// forever, unfixable by any later run.
func TestStartReapsRowsOwnedByThisProcessesReusedPID(t *testing.T) {
	home := t.TempDir()
	paths := chome.ResolveIn(home)

	st, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	// The dead owner: same host, and the same pid this test process now has.
	st.AssumeOwnership(os.Getpid(), ownership.Host())
	ctx := context.Background()
	if err := st.RecordRequest(ctx, "stranded", types.DelegateRequest{Task: "t"}, types.StatusRunning); err != nil {
		t.Fatalf("RecordRequest() error = %v", err)
	}
	st.Close()

	// It exited without cleaning up: the lock directory exists, its lock file
	// does not. Nothing here is holding pid's lock when Start runs.
	if err := ownership.EnsureDir(paths.Locks); err != nil {
		t.Fatal(err)
	}

	startIn(t, home)

	st2, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer st2.Close()
	rec, ok, err := st2.GetRequest(ctx, "stranded")
	if err != nil || !ok {
		t.Fatalf("GetRequest() ok=%v err=%v", ok, err)
	}
	if rec.Status != types.StatusFailed {
		t.Errorf("stranded request status = %q, want %q — a reused pid stranded it",
			rec.Status, types.StatusFailed)
	}
}
