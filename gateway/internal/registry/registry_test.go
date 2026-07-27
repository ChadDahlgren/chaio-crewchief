package registry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/types"
)

func writeYAML(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "models.yaml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadValidWithDefault(t *testing.T) {
	dir := t.TempDir()
	p := writeYAML(t, dir, `
models:
  - name: a
    base_url: http://a
    default: true
  - name: b
    base_url: http://b
`)
	reg, err := LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	def, ok := reg.Default()
	if !ok || def.Name != "a" {
		t.Fatalf("Default() = %+v, %v", def, ok)
	}
	got, ok := reg.Get("b")
	if !ok || got.BaseURL != "http://b" {
		t.Fatalf("Get(b) = %+v, %v", got, ok)
	}
	if len(reg.List()) != 2 {
		t.Fatalf("List() len = %d", len(reg.List()))
	}
}

func TestDuplicateNamesErrors(t *testing.T) {
	dir := t.TempDir()
	p := writeYAML(t, dir, `
models:
  - name: a
    base_url: http://a
  - name: a
    base_url: http://b
`)
	if _, err := LoadRegistry(p); err == nil {
		t.Fatal("expected error for duplicate names")
	}
}

func TestMultipleDefaultsErrors(t *testing.T) {
	dir := t.TempDir()
	p := writeYAML(t, dir, `
models:
  - name: a
    base_url: http://a
    default: true
  - name: b
    base_url: http://b
    default: true
`)
	if _, err := LoadRegistry(p); err == nil {
		t.Fatal("expected error for multiple defaults")
	}
}

func TestDefaultsApplied(t *testing.T) {
	dir := t.TempDir()
	p := writeYAML(t, dir, `
models:
  - name: a
    base_url: http://a
`)
	reg, err := LoadRegistry(p)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	got, _ := reg.Get("a")
	if got.Temperature != 0.3 {
		t.Fatalf("Temperature = %v, want 0.3", got.Temperature)
	}
	if got.TimeoutSec != 120 {
		t.Fatalf("TimeoutSec = %v, want 120", got.TimeoutSec)
	}
}

func TestEmptyBaseURLErrors(t *testing.T) {
	dir := t.TempDir()
	p := writeYAML(t, dir, `
models:
  - name: a
    base_url: ""
`)
	if _, err := LoadRegistry(p); err == nil {
		t.Fatal("expected error for empty base_url")
	}
}

func TestLoadRegistryMissingFileIsDistinguishable(t *testing.T) {
	_, err := LoadRegistry(filepath.Join(t.TempDir(), "absent.yaml"))
	if err == nil {
		t.Fatal("LoadRegistry(absent) = nil error, want ErrNotFound")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false; err = %v", err)
	}
}

func TestLoadRegistryMalformedIsNotNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.yaml")
	if err := os.WriteFile(path, []byte("models: [oh no: :"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadRegistry(path)
	if err == nil {
		t.Fatal("LoadRegistry(malformed) = nil error, want an error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("malformed YAML reported as ErrNotFound; it must stay a loud failure")
	}
}

func TestWatchDetectsChange(t *testing.T) {
	dir := t.TempDir()
	p := writeYAML(t, dir, `
models:
  - name: a
    base_url: http://a
`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	changed := make(chan types.Registry, 1)
	go Watch(ctx, p, func(r types.Registry) {
		changed <- r
	})

	// give the watcher a moment to start, ensure mtime will differ
	time.Sleep(50 * time.Millisecond)
	writeYAML(t, dir, `
models:
  - name: a
    base_url: http://changed
`)

	select {
	case r := <-changed:
		got, ok := r.Get("a")
		if !ok || got.BaseURL != "http://changed" {
			t.Fatalf("Watch delivered stale registry: %+v, %v", got, ok)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Watch did not detect file change within 5s")
	}
}
