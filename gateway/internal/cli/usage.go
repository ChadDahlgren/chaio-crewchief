package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/chome"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/embed"
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
		// False when the gateway has no frontier reference rate — no
		// rates.yaml at all, or a rates.yaml with no `counterfactual:`
		// block — i.e. the two fields above are absent rather than
		// measured. A pre-0.5 gateway omits the field entirely, which
		// decodes to false even though its counterfactual is real; see
		// savingsLine, which checks the amount before the flag so that
		// case is not misreported.
		CounterfactualConfigured bool `json:"counterfactual_configured"`
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
	b.WriteString(savingsLine(s))
	return b.String()
}

// savingsLine renders the headline number. It is the product, so it refuses to
// state anything it cannot support: no frontier reference rate means there is
// no price to compare against, and no comparison means no percentage — "n/a"
// with a reason is a real answer where "0.0%" would be a false one. When the
// local run cost more than the frontier would have, that is not savings and is
// not reported as such; a ratio is used instead of a percentage because an
// overspend has no upper bound and reads absurdly as one (-3749900.0%).
//
// The configured flag is only trusted where the counterfactual is itself
// absent, deliberately. A pre-0.5 gateway does not send
// counterfactual_configured at all, so it decodes to false while
// counterfactual_usd carries a real, priced number; a flag-first order printed
// "no frontier price to compare against" directly beneath a $551.10
// counterfactual. Gating on the amount too makes a new CLI against an old
// gateway degrade to the old, correct behavior, while a 0.5+ gateway with no
// `counterfactual:` block (which sends a zero and a false flag) still gets the
// actionable reason rather than the bare "priced to $0.00".
func savingsLine(s statsResp) string {
	t := s.Totals
	switch {
	case t.Attempts == 0:
		return "savings:  n/a — no attempts recorded yet\n"
	case !finite(t.CostUSD) || !finite(t.CounterfactualUSD):
		return "savings:  n/a — the ledger totals are not a usable number\n"
	case t.CounterfactualUSD < 0:
		return fmt.Sprintf("savings:  n/a — the frontier counterfactual priced to %s, which is not a price\n", money(t.CounterfactualUSD))
	case t.CounterfactualUSD == 0 && !t.CounterfactualConfigured:
		return "savings:  n/a — no frontier reference rate configured; add a `counterfactual:` block to rates.yaml\n"
	case t.CounterfactualUSD == 0:
		return "savings:  n/a — the frontier counterfactual priced to $0.00, so there is nothing to compare against\n"
	case nearParity(t.CostUSD, t.CounterfactualUSD):
		return "savings:  none — this ran at the frontier price, neither above nor below it\n"
	case t.CostUSD > t.CounterfactualUSD:
		return fmt.Sprintf("overspend: %s — this ran %.1fx the frontier price, not below it\n",
			money(t.CostUSD-t.CounterfactualUSD), t.CostUSD/t.CounterfactualUSD)
	default:
		return fmt.Sprintf("savings:  %s (%.1f%%)\n", money(t.CounterfactualUSD-t.CostUSD), t.SavingsPct*100)
	}
}

// nearParity reports whether cost and counterfactual are close enough that the
// overspend line would contradict itself — "$0.0001 — this ran 1.0x the
// frontier price, not below it" states a difference the ratio it prints denies.
// The threshold is the rounding the ratio itself does: anything that renders as
// 1.0x is parity as far as the reader can tell.
func nearParity(cost, cf float64) bool {
	return math.Abs(cost/cf-1) < 0.05
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// money formats a dollar amount, applying the decimal rule to the magnitude so
// the sign lands outside the currency symbol: -$3.75, not $-3.7500.
func money(v float64) string {
	if !finite(v) {
		return "n/a"
	}
	sign := ""
	// math.Signbit, not v < 0: negative zero is not less than zero but is still
	// negative, and would otherwise fall to the v == 0 branch as "$-0.00".
	if math.Signbit(v) {
		sign, v = "-", -v
	}
	if v >= 1 || v == 0 {
		return fmt.Sprintf("%s$%.2f", sign, v)
	}
	return fmt.Sprintf("%s$%.4f", sign, v)
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
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	fs.SetOutput(w)
	var mf ModeFlags
	mf.Register(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	mode, base, err := mf.Resolve()
	if err != nil {
		fmt.Fprintf(w, "error: %v\n", err)
		return 2
	}
	// Name the ledger before rendering it, the way doctor does. Two ledgers
	// produce visually identical reports, and the likeliest upgrade surprise is
	// someone who relied on the old implicit localhost:8181 default now reading
	// an empty local ledger and concluding their data is gone. A totals line of
	// zeros under "mode: embedded (home ...)" is a wrong-ledger diagnosis; the
	// same line alone is not.
	if mode == gwurl.ModeEmbedded {
		paths, err := chome.Resolve()
		if err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(w, "mode: embedded (home %s)\n", paths.Home)
		inst, err := embed.Start(context.Background(), embed.Config{Paths: paths})
		if err != nil {
			fmt.Fprintf(w, "error: start embedded gateway: %v\n", err)
			return 1
		}
		defer func() {
			if closeErr := inst.Close(); closeErr != nil {
				fmt.Fprintf(w, "warning: close embedded gateway: %v\n", closeErr)
			}
		}()
		base = inst.BaseURL
	} else {
		fmt.Fprintf(w, "mode: gateway (%s)\n", base)
	}

	var s statsResp
	if err := fetchJSON(base+"/stats", &s); err != nil {
		fmt.Fprintf(w, "gateway: UNREACHABLE at %s (%v)\n", base, err)
		return 2
	}
	fmt.Fprintln(w, RenderUsage(s))
	return 0
}
