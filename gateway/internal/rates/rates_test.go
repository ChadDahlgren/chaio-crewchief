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

	if got := tbl.Price("gpt-oss-120b", Usage{PromptTokens: 1_000_000, OutputTokens: 1_000_000}); got != 0 {
		t.Fatalf("local price = %v, want 0", got)
	}

	got := tbl.Price("bedrock-qwen", Usage{PromptTokens: 1_000_000, OutputTokens: 500_000})
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
	if got := tbl.Price("unknown-model", Usage{PromptTokens: 1_000_000, OutputTokens: 1_000_000}); got != 0 {
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
	got := tbl.Counterfactual(Usage{PromptTokens: 2_000_000, OutputTokens: 1_000_000})
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
	if got := tbl.Price("anything", Usage{PromptTokens: 1_000_000, OutputTokens: 1_000_000}); got != 0 {
		t.Fatalf("price = %v, want 0", got)
	}
	if got := tbl.Counterfactual(Usage{PromptTokens: 1_000_000, OutputTokens: 1_000_000}); got != 0 {
		t.Fatalf("counterfactual = %v, want 0", got)
	}
}

// A missing rates.yaml and a rates.yaml with no counterfactual block both mean
// there is no frontier price to compare a local run against. Counterfactual()
// returns 0 for both, which is indistinguishable from tokens that genuinely
// priced to nothing — this is the signal that tells them apart, and `usage`
// reports "n/a" rather than "0.0% savings" on the strength of it.
func TestHasCounterfactual(t *testing.T) {
	dir := t.TempDir()

	missing, err := Load(filepath.Join(dir, "absent.yaml"))
	if err != nil {
		t.Fatalf("Load(missing) error = %v", err)
	}
	if missing.HasCounterfactual() {
		t.Error("missing rates.yaml reports a counterfactual")
	}

	noCF := filepath.Join(dir, "nocf.yaml")
	if err := os.WriteFile(noCF, []byte("models:\n  m:\n    input_per_mtok: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tbl, err := Load(noCF)
	if err != nil {
		t.Fatalf("Load(noCF) error = %v", err)
	}
	if tbl.HasCounterfactual() {
		t.Error("rates.yaml without a counterfactual block reports one")
	}

	withCF := filepath.Join(dir, "cf.yaml")
	if err := os.WriteFile(withCF, []byte("counterfactual:\n  input_per_mtok: 3\n  output_per_mtok: 15\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tbl, err = Load(withCF)
	if err != nil {
		t.Fatalf("Load(withCF) error = %v", err)
	}
	if !tbl.HasCounterfactual() {
		t.Error("configured counterfactual not reported")
	}
}
