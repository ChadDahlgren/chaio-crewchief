package rates

import (
	"math"

	"testing"
)

func ptr(f float64) *float64 { return &f }

const eps = 1e-12

func closeTo(got, want float64) bool { return math.Abs(got-want) < eps }

// A usage with no cached and no reasoning tokens must price exactly as it did
// before either concept existed. This is the regression guard for every
// existing ledger row and every provider that reports neither.
func TestPriceUnchangedWithoutCachedOrReasoning(t *testing.T) {
	r := Rate{InputPerMTok: 1.0, OutputPerMTok: 2.0}
	got := r.price(Usage{PromptTokens: 1_000_000, OutputTokens: 500_000})
	want := 1.0 + 1.0
	if !closeTo(got, want) {
		t.Fatalf("price = %v, want %v", got, want)
	}
}

// The bug this whole change exists to fix: reasoning tokens are billable output.
func TestReasoningTokensAreBilledAsOutput(t *testing.T) {
	r := Rate{InputPerMTok: 1.0, OutputPerMTok: 2.0}

	without := r.price(Usage{PromptTokens: 1_000, OutputTokens: 10})
	with := r.price(Usage{PromptTokens: 1_000, OutputTokens: 10, ReasoningTokens: 1_000})

	wantDelta := 1_000.0 / 1_000_000 * 2.0
	if !closeTo(with-without, wantDelta) {
		t.Fatalf("reasoning delta = %v, want %v", with-without, wantDelta)
	}
}

// Cached tokens are a discounted subset of prompt tokens, not an addition to
// them: total input billed must not change, only its price.
func TestCachedTokensDiscountWithoutInflatingInput(t *testing.T) {
	r := Rate{InputPerMTok: 1.0, CachedInputPerMTok: ptr(0.20), OutputPerMTok: 2.0}

	got := r.price(Usage{PromptTokens: 1_000_000, CachedPromptTokens: 500_000})
	want := 0.5*1.0 + 0.5*0.20
	if !closeTo(got, want) {
		t.Fatalf("price = %v, want %v", got, want)
	}
}

// A nil cached rate means unconfigured, and must bill cached tokens at the full
// input rate — identical to the behavior before the field existed. Pricing them
// at zero would silently make every cache hit free.
func TestNilCachedRateBillsAtFullInputRate(t *testing.T) {
	r := Rate{InputPerMTok: 1.0, OutputPerMTok: 2.0} // CachedInputPerMTok nil

	withCache := r.price(Usage{PromptTokens: 1_000_000, CachedPromptTokens: 900_000})
	withoutCache := r.price(Usage{PromptTokens: 1_000_000})
	if !closeTo(withCache, withoutCache) {
		t.Fatalf("nil cached rate changed price: %v vs %v", withCache, withoutCache)
	}
}

// The reason CachedInputPerMTok is a pointer: an explicitly configured 0.0 means
// free cache reads, and must be distinguishable from "not configured".
func TestExplicitZeroCachedRateIsFree(t *testing.T) {
	r := Rate{InputPerMTok: 1.0, CachedInputPerMTok: ptr(0.0), OutputPerMTok: 2.0}

	got := r.price(Usage{PromptTokens: 1_000_000, CachedPromptTokens: 1_000_000})
	if !closeTo(got, 0) {
		t.Fatalf("explicit zero cached rate = %v, want 0 (free)", got)
	}
}

// A provider reporting more cached tokens than prompt tokens must not produce a
// negative billable-input term, which would credit the attempt.
func TestCachedExceedingPromptDoesNotGoNegative(t *testing.T) {
	r := Rate{InputPerMTok: 1.0, CachedInputPerMTok: ptr(0.20), OutputPerMTok: 2.0}

	got := r.price(Usage{PromptTokens: 100, CachedPromptTokens: 500})
	want := 100.0 / 1_000_000 * 0.20 // all 100 treated as cached, none negative
	if !closeTo(got, want) {
		t.Fatalf("price = %v, want %v", got, want)
	}
	if got < 0 {
		t.Fatalf("price went negative: %v", got)
	}
}

// The counterfactual must include reasoning tokens (the frontier model would
// have generated them too) and must NOT discount cached tokens (a frontier run
// has no warm cache to hit).
func TestCounterfactualIgnoresCacheAndCountsReasoning(t *testing.T) {
	path := writeRatesFile(t, `
models: {}
counterfactual:
  input_per_mtok: 5.0
  cached_input_per_mtok: 0.5
  output_per_mtok: 25.0
`)
	tbl, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := tbl.Counterfactual(Usage{
		PromptTokens:       1_000_000,
		CachedPromptTokens: 900_000, // must be ignored
		OutputTokens:       100_000,
		ReasoningTokens:    900_000, // must be billed
	})
	want := 1.0*5.0 + 1.0*25.0
	if !closeTo(got, want) {
		t.Fatalf("counterfactual = %v, want %v", got, want)
	}
}

func TestCachedRateParsesFromYAML(t *testing.T) {
	path := writeRatesFile(t, `
models:
  grok-build:
    input_per_mtok: 1.00
    cached_input_per_mtok: 0.20
    output_per_mtok: 2.00
  no-cached-rate:
    input_per_mtok: 1.00
    output_per_mtok: 2.00
`)
	cfg, err := readConfig(path)
	if err != nil {
		t.Fatalf("readConfig: %v", err)
	}
	if got := cfg.Models["grok-build"].CachedInputPerMTok; got == nil || *got != 0.20 {
		t.Fatalf("cached rate = %v, want 0.20", got)
	}
	if got := cfg.Models["no-cached-rate"].CachedInputPerMTok; got != nil {
		t.Fatalf("absent cached rate parsed as %v, want nil", *got)
	}
}

// Golden file: three real grok-build-0.1 responses measured 2026-08-01, with
// xAI's own cost_in_usd_ticks as the expected answer. These are observations,
// not hand-computed expectations — if the pricing formula drifts from what the
// vendor actually charges, this fails.
func TestGoldenXAIObservations(t *testing.T) {
	path := writeRatesFile(t, `
models:
  grok-build:
    input_per_mtok: 1.00
    cached_input_per_mtok: 0.20
    output_per_mtok: 2.00
`)
	tbl, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// usdPerTick mirrors internal/provider's constant. Duplicated rather than
	// imported to keep the pricing package free of a provider dependency; if
	// the two ever diverge, these cases fail, which is the point.
	const usdPerTick = 1e-10

	cases := []struct {
		name  string
		usage Usage
		ticks int64 // xAI's reported cost_in_usd_ticks, verbatim
	}{
		{
			name:  "short-cold",
			usage: Usage{PromptTokens: 189, CachedPromptTokens: 128, OutputTokens: 2, ReasoningTokens: 128},
			ticks: 3470000,
		},
		{
			name:  "short-repeat",
			usage: Usage{PromptTokens: 189, CachedPromptTokens: 128, OutputTokens: 2, ReasoningTokens: 156},
			ticks: 4030000,
		},
		{
			// The call that exposed the bug: completion_tokens is 4, but the
			// 2259 reasoning tokens are 74% of the true cost. Priced on
			// completion alone this comes to $0.001558 against a real $0.006074.
			name:  "long-cold",
			usage: Usage{PromptTokens: 1650, CachedPromptTokens: 128, OutputTokens: 4, ReasoningTokens: 2259},
			ticks: 60740000,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantUSD := float64(c.ticks) * usdPerTick
			got := tbl.Price("grok-build", c.usage)
			if math.Abs(got-wantUSD) > 5e-7 {
				t.Fatalf("modeled $%.9f, provider reported $%.9f (%d ticks)", got, wantUSD, c.ticks)
			}
		})
	}
}

// Guards the specific regression: pricing on completion_tokens alone, as the
// ledger did before this change, is dramatically wrong on a reasoning model.
// If someone "simplifies" the formula by dropping ReasoningTokens, this fails
// loudly instead of silently under-billing.
func TestIgnoringReasoningWouldUnderprice(t *testing.T) {
	r := Rate{InputPerMTok: 1.00, CachedInputPerMTok: ptr(0.20), OutputPerMTok: 2.00}
	u := Usage{PromptTokens: 1650, CachedPromptTokens: 128, OutputTokens: 4, ReasoningTokens: 2259}

	correct := r.price(u)
	u.ReasoningTokens = 0
	blind := r.price(u)

	if ratio := correct / blind; ratio < 3.5 {
		t.Fatalf("reasoning-blind pricing is only %.2fx off; expected ~3.9x on this observation", ratio)
	}
}
