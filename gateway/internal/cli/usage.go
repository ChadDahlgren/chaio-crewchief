package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/gwurl"
)

type statRow struct {
	Model     string  `json:"model"`
	Outcome   string  `json:"outcome"`
	Count     int     `json:"count"`
	AvgWallMS float64 `json:"avg_wall_ms"`
	AvgTokens float64 `json:"avg_tokens"`
	CostUSD   float64 `json:"cost_usd"`
}

type statsResp struct {
	Rows   []statRow `json:"rows"`
	Totals struct {
		Attempts          int     `json:"attempts"`
		PromptTokens      int64   `json:"prompt_tokens"`
		OutputTokens      int64   `json:"output_tokens"`
		CostUSD           float64 `json:"cost_usd"`
		CounterfactualUSD float64 `json:"counterfactual_usd"`
		SavingsPct        float64 `json:"savings_pct"`
	} `json:"totals"`
}

type modelUsage struct {
	Model     string
	Attempts  int
	Delivered int // a response came back; Crew Chief does not judge its quality
	Failed    int // mechanical failure: no response, timeout, transport/API error
	CostUSD   float64
}

// Aggregate folds outcome rows into per-model usage lines.
func Aggregate(rows []statRow) []modelUsage {
	byModel := map[string]*modelUsage{}
	for _, r := range rows {
		m, ok := byModel[r.Model]
		if !ok {
			m = &modelUsage{Model: r.Model}
			byModel[r.Model] = m
		}
		m.Attempts += r.Count
		m.CostUSD += r.CostUSD
		if r.Outcome == "failed" {
			m.Failed += r.Count
		} else {
			m.Delivered += r.Count
		}
	}
	out := make([]modelUsage, 0, len(byModel))
	for _, m := range byModel {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Attempts > out[j].Attempts })
	return out
}

// RenderUsage produces the /usage-style efficiency report. "delivered" means
// a response came back, not that it was judged correct — Crew Chief doesn't
// grade output; the calling brain does.
func RenderUsage(s statsResp) string {
	var b strings.Builder
	b.WriteString("CREW CHIEF USAGE — efficiency report\n\n")
	fmt.Fprintf(&b, "%-24s %9s %11s %8s %12s\n", "model", "attempts", "delivered", "failed", "cost")
	for _, m := range Aggregate(s.Rows) {
		fmt.Fprintf(&b, "%-24s %9d %11d %8d %12s\n",
			m.Model, m.Attempts, m.Delivered, m.Failed, money(m.CostUSD))
	}
	t := s.Totals
	b.WriteString("\n")
	fmt.Fprintf(&b, "attempts: %d   tokens: %s in / %s out\n", t.Attempts, thousands(t.PromptTokens), thousands(t.OutputTokens))
	fmt.Fprintf(&b, "spend:    %s\n", money(t.CostUSD))
	fmt.Fprintf(&b, "frontier counterfactual: %s\n", money(t.CounterfactualUSD))
	fmt.Fprintf(&b, "savings:  %s (%.1f%%)\n", money(t.CounterfactualUSD-t.CostUSD), t.SavingsPct*100)
	return b.String()
}

func money(v float64) string {
	if v >= 1 || v == 0 {
		return fmt.Sprintf("$%.2f", v)
	}
	return fmt.Sprintf("$%.4f", v)
}

func thousands(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	return s + "," + strings.Join(parts, ",")
}

// Usage fetches /stats and prints the efficiency report. Returns exit code.
func Usage(w io.Writer, args []string) int {
	base := gwurl.URLFromEnv()
	var s statsResp
	if err := fetchJSON(base+"/stats", &s); err != nil {
		fmt.Fprintf(w, "gateway: UNREACHABLE at %s (%v)\n", base, err)
		return 2
	}
	fmt.Fprintln(w, RenderUsage(s))
	return 0
}
