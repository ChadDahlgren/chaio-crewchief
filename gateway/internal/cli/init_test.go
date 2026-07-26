package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/registry"
)

func TestInitWritesStarterConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CHAIO_CREWCHIEF_HOME", home)

	var out bytes.Buffer
	if code := Init(&out, nil); code != 0 {
		t.Fatalf("Init() = %d, want 0; output: %s", code, out.String())
	}
	path := filepath.Join(home, "models.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("models.yaml not written: %v", err)
	}
	if !strings.Contains(out.String(), path) {
		t.Errorf("output %q does not name the file it wrote", out.String())
	}
}

// A starter file that the registry rejects is worse than none: the user edits
// it, restarts, and gets a parse error they did not cause.
func TestStarterConfigIsValidRegistryInput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CHAIO_CREWCHIEF_HOME", home)
	if code := Init(&bytes.Buffer{}, nil); code != 0 {
		t.Fatalf("Init() = %d, want 0", code)
	}
	if _, err := registry.LoadRegistry(filepath.Join(home, "models.yaml")); err != nil {
		t.Errorf("LoadRegistry(starter) error = %v; the starter file must parse", err)
	}
}

func TestInitRefusesToOverwrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CHAIO_CREWCHIEF_HOME", home)
	path := filepath.Join(home, "models.yaml")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("models: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if code := Init(&out, nil); code == 0 {
		t.Fatal("Init() = 0 over an existing file, want non-zero")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "models: []\n" {
		t.Error("Init overwrote an existing models.yaml without --force")
	}
	if !strings.Contains(out.String(), "--force") {
		t.Errorf("refusal %q does not mention --force", out.String())
	}
}

func TestInitForceOverwrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CHAIO_CREWCHIEF_HOME", home)
	path := filepath.Join(home, "models.yaml")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("models: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if code := Init(&bytes.Buffer{}, []string{"--force"}); code != 0 {
		t.Fatalf("Init(--force) = %d, want 0", code)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) == "models: []\n" {
		t.Error("Init(--force) did not overwrite")
	}
}
