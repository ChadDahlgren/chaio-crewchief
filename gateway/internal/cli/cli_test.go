package cli

import (
	"strings"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	got := ParseEnvFile("A=1\n# comment\n\nB=\"two words\"\nA=3\nbad line\nC='x'\n  D = spaced  \n")
	want := map[string]string{"A": "3", "B": "two words", "C": "x", "D": "spaced"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestCheckPresets(t *testing.T) {
	checks := CheckPresets([]presetInfo{
		{Name: "m1", APIKeyEnv: "K1", ProviderClass: "cloud"},
		{Name: "m2", APIKeyEnv: "", ProviderClass: "weird"},
		{Name: "m3", APIKeyEnv: "K3", ProviderClass: "local"},
	}, map[string]string{"K3": "secret", "K1": ""})

	if len(checks[0].Issues) != 1 || checks[0].Issues[0] != "missing key: K1" {
		t.Errorf("m1 issues = %v", checks[0].Issues)
	}
	if len(checks[1].Issues) != 1 || checks[1].Issues[0] != "unknown provider_class: weird" {
		t.Errorf("m2 issues = %v", checks[1].Issues)
	}
	if len(checks[2].Issues) != 0 {
		t.Errorf("m3 issues = %v", checks[2].Issues)
	}
}

func TestSummarize(t *testing.T) {
	h := healthResp{Status: "ok"}
	h.Models = []struct {
		Name    string `json:"name"`
		Healthy bool   `json:"healthy"`
	}{{"m1", true}, {"m2", false}, {"m3", true}}
	checks := []PresetCheck{
		{Name: "m1", Issues: []string{"missing key: K1"}},
		{Name: "m2", Issues: []string{}},
		{Name: "m3", Issues: []string{}},
	}
	out := Summarize(h, checks)
	lines := strings.Split(out, "\n")
	if lines[0] != "gateway: ok" {
		t.Errorf("line0 = %q", lines[0])
	}
	if !strings.Contains(out, "m1: UNHEALTHY; missing key: K1") {
		t.Errorf("m1 line wrong in:\n%s", out)
	}
	if !strings.Contains(out, "m2: UNHEALTHY") || !strings.Contains(out, "m3: OK") {
		t.Errorf("m2/m3 lines wrong in:\n%s", out)
	}
	if lines[len(lines)-1] != "1 of 3 models ready" {
		t.Errorf("last = %q", lines[len(lines)-1])
	}
}

func TestAggregate(t *testing.T) {
	rows := []statRow{
		{Model: "a", Outcome: "delivered", Count: 8, CostUSD: 0.03},
		{Model: "a", Outcome: "failed", Count: 2},
		{Model: "b", Outcome: "failed", Count: 1},
	}
	agg := Aggregate(rows)
	if len(agg) != 2 || agg[0].Model != "a" {
		t.Fatalf("agg = %+v", agg)
	}
	a := agg[0]
	if a.Attempts != 10 || a.Delivered != 8 || a.Failed != 2 {
		t.Errorf("a = %+v", a)
	}
	if a.CostUSD < 0.029 || a.CostUSD > 0.031 {
		t.Errorf("cost = %f", a.CostUSD)
	}
}

func TestRenderUsageShape(t *testing.T) {
	var s statsResp
	s.Rows = []statRow{{Model: "m", Outcome: "delivered", Count: 1, CostUSD: 0.5}}
	s.Totals.Attempts = 1
	s.Totals.PromptTokens = 1234567
	s.Totals.CostUSD = 0.5
	s.Totals.CounterfactualUSD = 10
	s.Totals.SavingsPct = 0.95
	out := RenderUsage(s)
	for _, want := range []string{"CREW CHIEF USAGE", "1,234,567", "$0.50", "$9.50 (95.0%)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
