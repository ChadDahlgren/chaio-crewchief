package provider

import "testing"

// Providers disagree on whether completion_tokens already includes reasoning
// tokens. Getting this wrong in the permissive direction double-bills every
// OpenAI-convention provider, so the reconciliation is load-bearing.
func TestBillableReasoningTokens(t *testing.T) {
	cases := []struct {
		name                                 string
		prompt, completion, reasoning, total int
		want                                 int
		why                                  string
	}{
		{
			// Observed from grok-build-0.1 on 2026-08-01: 189+2+139 == 330,
			// which only reconciles if reasoning sits outside completion.
			name:   "xai exclusive convention",
			prompt: 189, completion: 2, reasoning: 139, total: 330,
			want: 139,
			why:  "reasoning is billable output not yet counted",
		},
		{
			// OpenAI convention: completion_tokens already includes reasoning,
			// so prompt+completion reconciles on its own.
			name:   "openai inclusive convention",
			prompt: 100, completion: 150, reasoning: 120, total: 250,
			want: 0,
			why:  "already inside completion_tokens; adding would double-bill",
		},
		{
			name:   "no reasoning reported",
			prompt: 100, completion: 50, reasoning: 0, total: 150,
			want: 0,
			why:  "nothing to add",
		},
		{
			name:   "total_tokens absent",
			prompt: 100, completion: 50, reasoning: 30, total: 0,
			want: 0,
			why:  "cannot reconcile; assume inclusive (safe direction)",
		},
		{
			name:   "unreconcilable totals",
			prompt: 100, completion: 50, reasoning: 30, total: 999,
			want: 0,
			why:  "neither sum matches; assume inclusive rather than over-bill",
		},
		{
			name:   "negative reasoning is ignored",
			prompt: 100, completion: 50, reasoning: -5, total: 145,
			want: 0,
			why:  "malformed payload must not credit the attempt",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := billableReasoningTokens(c.prompt, c.completion, c.reasoning, c.total)
			if got != c.want {
				t.Fatalf("billableReasoningTokens = %d, want %d (%s)", got, c.want, c.why)
			}
		})
	}
}

func TestTicksToUSD(t *testing.T) {
	// The observation the scale was derived from.
	if got := ticksToUSD(3686000); got < 0.00036859 || got > 0.00036861 {
		t.Fatalf("ticksToUSD(3686000) = %v, want ~0.0003686", got)
	}
	if got := ticksToUSD(0); got != 0 {
		t.Fatalf("ticksToUSD(0) = %v, want 0 (not reported)", got)
	}
	if got := ticksToUSD(-1); got != 0 {
		t.Fatalf("ticksToUSD(-1) = %v, want 0", got)
	}
}
