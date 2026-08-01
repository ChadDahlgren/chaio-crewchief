package store

import (
	"context"
	"testing"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/types"
)

// A ledger written by a release predating the cost-accuracy columns must open,
// migrate, and keep its existing rows intact and unchanged.
//
// Migration 5 is purely additive with zero defaults, so the pre-existing
// attempt must read back with exactly the cost it was written with — nothing is
// backfilled or recomputed. That is deliberate: cost_usd is priced at write
// time, and silently rewriting recorded spend would be worse than a stated
// discontinuity in the numbers.
func TestMigrationPreservesLegacyAttemptCost(t *testing.T) {
	ctx := context.Background()
	path := writeV040Ledger(t)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a pre-migration ledger: %v", err)
	}
	defer s.Close()

	a, ok, err := s.GetAttempt(ctx, "old-a")
	if err != nil {
		t.Fatalf("GetAttempt: %v", err)
	}
	if !ok {
		t.Fatal("legacy attempt disappeared across migration")
	}

	if a.CostUSD != 2.50 {
		t.Errorf("legacy cost_usd = %v, want 2.50 unchanged (nothing is backfilled)", a.CostUSD)
	}
	if a.PromptTokens != 5 || a.OutputTokens != 5 {
		t.Errorf("legacy token counts changed: prompt=%d output=%d, want 5/5", a.PromptTokens, a.OutputTokens)
	}
	if a.CachedPromptTokens != 0 || a.ReasoningTokens != 0 || a.ProviderCostUSD != 0 {
		t.Errorf("new columns should default to 0 on legacy rows, got cached=%d reasoning=%d providerCost=%v",
			a.CachedPromptTokens, a.ReasoningTokens, a.ProviderCostUSD)
	}
}

// The new columns must survive a write/read round trip, and StatsTotals must
// aggregate them — otherwise `usage` reports reasoning-blind numbers even
// though the ledger holds the right data.
func TestCostColumnsRoundTripAndAggregate(t *testing.T) {
	ctx := context.Background()
	path := writeV040Ledger(t)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	want := types.Attempt{
		ID:                 "new-a",
		RequestID:          "old",
		Model:              "grok-build",
		WallMS:             1200,
		PromptTokens:       1650,
		CachedPromptTokens: 128,
		OutputTokens:       4,
		ReasoningTokens:    2259,
		Outcome:            types.OutcomeDelivered,
		ProviderClass:      "cloud",
		CostUSD:            0.006074,
		ProviderCostUSD:    0.006074,
	}
	if err := s.RecordAttempt(ctx, want); err != nil {
		t.Fatalf("RecordAttempt: %v", err)
	}

	got, ok, err := s.GetAttempt(ctx, "new-a")
	if err != nil || !ok {
		t.Fatalf("GetAttempt: %v ok=%v", err, ok)
	}
	if got.CachedPromptTokens != want.CachedPromptTokens {
		t.Errorf("cached = %d, want %d", got.CachedPromptTokens, want.CachedPromptTokens)
	}
	if got.ReasoningTokens != want.ReasoningTokens {
		t.Errorf("reasoning = %d, want %d", got.ReasoningTokens, want.ReasoningTokens)
	}
	if got.ProviderCostUSD != want.ProviderCostUSD {
		t.Errorf("providerCost = %v, want %v", got.ProviderCostUSD, want.ProviderCostUSD)
	}

	totals, err := s.StatsTotals(ctx)
	if err != nil {
		t.Fatalf("StatsTotals: %v", err)
	}
	// The legacy row contributes 5/5 tokens and no reasoning; the new row adds
	// 1650 prompt, 128 cached, 4 output, 2259 reasoning.
	if totals.ReasoningTokens != 2259 {
		t.Errorf("total reasoning = %d, want 2259", totals.ReasoningTokens)
	}
	if totals.CachedPromptTokens != 128 {
		t.Errorf("total cached = %d, want 128", totals.CachedPromptTokens)
	}
	// Only the new row reported a provider cost, so the count must be 1 of 2 —
	// summing without counting would imply every attempt was verified.
	if totals.ProviderCostAttempts != 1 {
		t.Errorf("provider cost attempts = %d, want 1 (the legacy row reported none)", totals.ProviderCostAttempts)
	}
	if totals.Attempts != 2 {
		t.Errorf("attempts = %d, want 2", totals.Attempts)
	}
}
