package embed

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/chome"
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

func TestEachStartGetsItsOwnPort(t *testing.T) {
	a := startIn(t, t.TempDir())
	b := startIn(t, t.TempDir())
	if a.BaseURL == b.BaseURL {
		t.Errorf("both instances bound %s; ports must be kernel-assigned", a.BaseURL)
	}
}
