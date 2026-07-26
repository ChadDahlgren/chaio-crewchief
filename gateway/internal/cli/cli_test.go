package cli

import (
	"math"
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
	s.Totals.CounterfactualConfigured = true
	out := RenderUsage(s)
	for _, want := range []string{"CREW CHIEF USAGE", "1,234,567", "$0.50", "savings:  $9.50 (95.0%)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestMoneySign(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{0, "$0.00"},
		{3.75, "$3.75"},
		{0.5, "$0.5000"},
		{-3.75, "-$3.75"},
		{-0.5, "-$0.5000"},
		{math.NaN(), "n/a"},
		{math.Inf(1), "n/a"},
		{math.Inf(-1), "n/a"},
	} {
		if got := money(tc.in); got != tc.want {
			t.Errorf("money(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The savings number is the product; each of these is a shape it must not
// misreport. "n/a" with a reason is a real answer, "0.0%" beside a negative
// dollar figure is two contradictory claims.
func TestSavingsLine(t *testing.T) {
	mk := func(attempts int, cost, cf float64, configured bool, pct float64) statsResp {
		var s statsResp
		s.Totals.Attempts = attempts
		s.Totals.CostUSD = cost
		s.Totals.CounterfactualUSD = cf
		s.Totals.CounterfactualConfigured = configured
		s.Totals.SavingsPct = pct
		return s
	}
	tests := []struct {
		name    string
		s       statsResp
		want    string
		absent  []string
		present []string
	}{
		{
			name:   "no rates table, ledger has real spend",
			s:      mk(12, 3.75, 0, false, 0),
			want:   "savings:  n/a — no rates.yaml, so there is no frontier price to compare against\n",
			absent: []string{"%", "0.0"},
		},
		{
			name:   "zero attempts",
			s:      mk(0, 0, 0, true, 0),
			want:   "savings:  n/a — no attempts recorded yet\n",
			absent: []string{"%"},
		},
		{
			name: "normal savings",
			s:    mk(3, 0.10, 5, true, 0.98),
			want: "savings:  $4.90 (98.0%)\n",
		},
		{
			name:    "cost above counterfactual is overspend, not savings",
			s:       mk(3, 10, 1, true, -9),
			absent:  []string{"savings", "%"},
			present: []string{"overspend: $9.00", "10.0x"},
		},
		{
			name:    "partial rates: tiny counterfactual reads as a ratio, not -3749900%",
			s:       mk(9, 3.75, 0.0001, true, -37499),
			absent:  []string{"%"},
			present: []string{"overspend:"},
		},
		{
			name:   "counterfactual priced to zero with attempts present",
			s:      mk(9, 3.75, 0, true, 0),
			want:   "savings:  n/a — the frontier counterfactual priced to $0.00, so there is nothing to compare against\n",
			absent: []string{"%"},
		},
		{
			name:   "non-finite totals never reach the user as $NaN",
			s:      mk(2, math.NaN(), 5, true, math.NaN()),
			want:   "savings:  n/a — the ledger totals are not a usable number\n",
			absent: []string{"NaN", "%"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := savingsLine(tt.s)
			if tt.want != "" && got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
			for _, a := range tt.absent {
				if strings.Contains(got, a) {
					t.Errorf("line must not contain %q: %q", a, got)
				}
			}
			for _, p := range tt.present {
				if !strings.Contains(got, p) {
					t.Errorf("line must contain %q: %q", p, got)
				}
			}
		})
	}
}
