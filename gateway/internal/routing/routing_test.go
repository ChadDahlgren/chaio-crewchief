package routing

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "routing.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolveHit(t *testing.T) {
	p := writeTemp(t, "routes:\n  typescript: cf-glm-5.2\n  python: local-coder\n")
	tbl, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := tbl.Resolve("typescript")
	if !ok || got != "cf-glm-5.2" {
		t.Fatalf("Resolve(typescript) = %q, %v", got, ok)
	}
}

func TestResolveMiss(t *testing.T) {
	p := writeTemp(t, "routes:\n  python: local-coder\n")
	tbl, _ := Load(p)
	if _, ok := tbl.Resolve("cobol"); ok {
		t.Fatal("expected no match for unlisted lang")
	}
}

func TestResolveEmptyLangNeverMatches(t *testing.T) {
	p := writeTemp(t, "routes:\n  \"\": local-coder\n")
	tbl, _ := Load(p)
	if _, ok := tbl.Resolve(""); ok {
		t.Fatal("empty lang should never resolve, even if YAML has a blank key")
	}
}

func TestLoadMissingFileIsEmptyTableNotError(t *testing.T) {
	tbl, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing routing.yaml should not error: %v", err)
	}
	if _, ok := tbl.Resolve("python"); ok {
		t.Fatal("empty table should resolve nothing")
	}
}

func TestLoadInvalidYAMLErrors(t *testing.T) {
	p := writeTemp(t, "routes: [this, is, not, a, map]\n")
	if _, err := Load(p); err == nil {
		t.Fatal("expected error for malformed routing.yaml")
	}
}

func TestCaseNormalization(t *testing.T) {
	p := writeTemp(t, "routes:\n  TypeScript: cf-glm-5.2\n")
	tbl, _ := Load(p)
	got, ok := tbl.Resolve("typescript")
	if !ok || got != "cf-glm-5.2" {
		t.Fatalf("case-insensitive lookup failed: %q, %v", got, ok)
	}
}
