package rates

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRatesFile(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "rates.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestLoadAndPrice(t *testing.T) {
	path := writeRatesFile(t, `
models:
  gpt-oss-120b:
    input_per_mtok: 0.0
    output_per_mtok: 0.0
  bedrock-qwen:
    input_per_mtok: 1.0
    output_per_mtok: 2.0
counterfactual:
  input_per_mtok: 3.0
  output_per_mtok: 15.0
`)
	tbl, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := tbl.Price("gpt-oss-120b", 1_000_000, 1_000_000); got != 0 {
		t.Fatalf("local price = %v, want 0", got)
	}

	got := tbl.Price("bedrock-qwen", 1_000_000, 500_000)
	want := 1.0 + 1.0 // 1M input @ $1/Mtok + 0.5M output @ $2/Mtok
	if got != want {
		t.Fatalf("price = %v, want %v", got, want)
	}
}

func TestPriceMissingModelIsZero(t *testing.T) {
	path := writeRatesFile(t, `
models:
  known-model:
    input_per_mtok: 5.0
    output_per_mtok: 5.0
`)
	tbl, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := tbl.Price("unknown-model", 1_000_000, 1_000_000); got != 0 {
		t.Fatalf("price for unknown model = %v, want 0", got)
	}
}

func TestCounterfactual(t *testing.T) {
	path := writeRatesFile(t, `
models: {}
counterfactual:
  input_per_mtok: 3.0
  output_per_mtok: 15.0
`)
	tbl, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := tbl.Counterfactual(2_000_000, 1_000_000)
	want := 2*3.0 + 1*15.0
	if got != want {
		t.Fatalf("counterfactual = %v, want %v", got, want)
	}
}

func TestLoadMissingFileIsAllZero(t *testing.T) {
	tbl, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := tbl.Price("anything", 1_000_000, 1_000_000); got != 0 {
		t.Fatalf("price = %v, want 0", got)
	}
	if got := tbl.Counterfactual(1_000_000, 1_000_000); got != 0 {
		t.Fatalf("counterfactual = %v, want 0", got)
	}
}
