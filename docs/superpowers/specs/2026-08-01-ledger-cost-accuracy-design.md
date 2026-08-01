# Ledger cost accuracy — reasoning tokens, cached input, provider verification

**Date:** 2026-08-01
**Status:** design, awaiting approval

## Problem

The cost ledger is wrong, not imprecise. It reads `completion_tokens` as the
billable output of an attempt. On reasoning models that field excludes the
reasoning tokens the provider actually bills for, so every such attempt is
priced at a fraction of its true cost.

Measured against xAI's `grok-build-0.1` on 2026-08-01, three calls, provider
usage payload compared to the ledger's pricing model:

| call | uncached in | cached in | completion | reasoning | ledger prices | actually cost |
|---|---|---|---|---|---|---|
| short-cold | 61 | 128 | 2 | 128 | $0.000090 | $0.000347 |
| short-repeat | 61 | 128 | 2 | 156 | $0.000090 | $0.000403 |
| long-cold | 1522 | 128 | 4 | 2259 | $0.001558 | $0.006074 |

The third row is a **3.9× undercount**, with reasoning tokens accounting for
**74% of true cost**. This is not an edge case: the model emitted 128 reasoning
tokens to answer "Say OK."

Two smaller errors compound it, in opposite directions:

- **Cached input is overcharged.** Providers bill cached prompt tokens at a
  discount (xAI: $0.20/MTok vs $1.00). The ledger has no cached-token field, so
  it prices every input token at full rate.
- **`savings` inherits both.** The counterfactual is computed from the same
  token counts, so a wrong output count corrupts the headline number in both
  terms.

## What the provider already tells us

No estimation is required. The OpenAI-compatible payload carries everything:

```json
"usage": {
  "prompt_tokens": 189,
  "completion_tokens": 2,
  "total_tokens": 330,
  "prompt_tokens_details":     { "cached_tokens": 128 },
  "completion_tokens_details": { "reasoning_tokens": 139 },
  "cost_in_usd_ticks": 3686000
}
```

Note `189 + 2 + 139 = 330` — **xAI's `completion_tokens` excludes reasoning
tokens**, which is not the OpenAI convention (where `completion_tokens` is
inclusive). Any provider adapter must not assume one or the other; see
*Total-token reconciliation* below.

`cost_in_usd_ticks` is xAI's own billing figure. At **1 tick = 10⁻¹⁰ USD** it
reproduces the modeled price to 0% error across all three calls above, spanning
a 17× cost range. That scale is derived empirically from those observations and
is **not confirmed by xAI documentation** — the design below therefore treats
provider cost as a *check*, never as the source of truth.

## Approach: model from rates, verify against the provider

Pricing stays modeled from `rates.yaml` for every provider, so there is one
uniform code path and `rates.yaml` remains the single source of truth. Where a
provider also reports its own cost, record it alongside and compare.

Rejected alternative: trusting provider-reported cost when present. It is exact
and self-maintaining, but it is vendor-specific (the local llama.cpp fleet
reports nothing, so both paths would exist anyway), it embeds an undocumented
unit scale in the pricing path, and it makes rate drift invisible rather than
detectable.

The chosen design is the only one of the two that would have **caught this bug
on its own**: a modeled price that ignores reasoning tokens diverges 290% from
the provider's figure, which is exactly the alarm this adds.

## Non-goals

- **Backfilling historical rows.** `cost_usd` is priced at write time and never
  recomputed — that is existing, documented behavior. Rows written before this
  change keep their old prices; `usage` totals will read low until they age out.
  Silently rewriting recorded spend would be worse than a stated discontinuity.
- **Changing the meaning of `output_tokens`.** See below.
- **Per-provider cost adapters beyond xAI.** One field, one documented vendor.

## Schema

Migration **version 5**, three additive columns on `attempts`, following the
existing `addColumn` pattern:

```sql
ALTER TABLE attempts ADD COLUMN cached_prompt_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE attempts ADD COLUMN reasoning_tokens     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE attempts ADD COLUMN provider_cost_usd    REAL    NOT NULL DEFAULT 0;
```

**`output_tokens` keeps its current meaning** — the provider's
`completion_tokens`, reasoning excluded. Reasoning lands in its own column and
pricing sums the two.

This is deliberate. Redefining `output_tokens` to mean "billable output" would
make historical rows silently wrong: a pre-migration row would claim a
reasoning-inclusive count it never had. Additive columns defaulting to 0 make
old rows price exactly as they always did, which is the honest reading.

`provider_cost_usd = 0` means "not reported" — indistinguishable from a genuine
zero, but a provider that reports cost never reports zero for a billed attempt,
and providers that don't report it are locals priced at $0 anyway.

## Pricing

`rates.Rate` gains one field:

```yaml
models:
  grok-build:
    input_per_mtok: 1.00
    cached_input_per_mtok: 0.20   # optional
    output_per_mtok: 2.00
```

**`cached_input_per_mtok` is a `*float64`, not a `float64`.** A plain zero value
is ambiguous between "cached tokens are free" and "not configured," and guessing
wrong makes every cache hit price at $0. Nil means unconfigured, and cached
tokens are billed at the full `input_per_mtok` — the conservative reading, and
identical to today's behavior, so adding the column changes no existing number.

The `Table` interface changes shape rather than growing parameters:

```go
type Usage struct {
    PromptTokens       int  // total, inclusive of cached
    CachedPromptTokens int
    OutputTokens       int  // provider's completion_tokens
    ReasoningTokens    int
}

type Table interface {
    Price(model string, u Usage) float64
    Counterfactual(u Usage) float64
    HasCounterfactual() bool
}
```

with

```
billable_input  = PromptTokens - CachedPromptTokens
billable_output = OutputTokens + ReasoningTokens
price = billable_input/1e6 * input + CachedPromptTokens/1e6 * cachedInput
      + billable_output/1e6 * output
```

**The counterfactual uses the same formula**, including reasoning tokens. The
frontier model would have had to generate that reasoning too, so excluding it
would understate what was avoided — the mirror of the bug being fixed. It prices
cached tokens at the full frontier input rate, since a hypothetical frontier run
has no cache to hit.

## Provider parsing

`internal/provider/openai.go` extends its usage struct:

```go
Usage struct {
    PromptTokens        int `json:"prompt_tokens"`
    CompletionTokens    int `json:"completion_tokens"`
    TotalTokens         int `json:"total_tokens"`
    PromptTokensDetails struct {
        CachedTokens int `json:"cached_tokens"`
    } `json:"prompt_tokens_details"`
    CompletionTokensDetails struct {
        ReasoningTokens int `json:"reasoning_tokens"`
    } `json:"completion_tokens_details"`
    CostInUSDTicks int64 `json:"cost_in_usd_ticks"`
}
```

Every field is optional; a provider that omits them yields zeros, which price
exactly as today. No provider allowlist and no per-vendor branching.

`cost_in_usd_ticks` is xAI-specific. It converts as `float64(ticks) / 1e10`,
with the constant carrying a comment recording that the scale is empirically
derived, matched to 0% error across the three observations above, and
unconfirmed by vendor documentation.

### Total-token reconciliation

Because `completion_tokens` is inclusive on some providers and exclusive on
others, `OutputTokens + ReasoningTokens` would double-count reasoning on an
OpenAI-convention provider.

Disambiguate from `total_tokens`, which every provider reports:

- If `prompt + completion + reasoning == total` → exclusive (xAI). Keep both;
  bill their sum.
- If `prompt + completion == total` → inclusive (OpenAI). Reasoning is already
  inside `completion_tokens`; store it for visibility but **do not add it** to
  billable output.
- If neither matches, or `total_tokens` is absent → assume **inclusive**, the
  documented OpenAI convention, and log once per preset.

The inclusive assumption is the safe default: it can undercount a
non-conforming exclusive provider, whereas assuming exclusive would
double-bill every standard one. The reconciliation result is what makes this
correct across the fleet rather than correct for xAI.

## Verification

When an attempt has both a modeled price and a provider-reported cost, compare:

```
drift = |modeled - provider| / provider
```

Flag when `drift > 0.05` **and** `|modeled - provider| > $0.0001`. The absolute
floor exists because a sub-cent attempt trivially blows through 5% on rounding
alone, and an alarm that fires constantly is one nobody reads.

Two surfaces:

- **At write time** — one log line naming the preset, both figures, and the
  drift. It is an accounting warning, never a request failure; the attempt still
  records normally.
- **In `usage`** — a summary line when any attempt in the window carried a
  provider cost:

  ```
  spend:    $0.0041 modeled
  provider: $0.0043 reported (+4.9%, 12 of 14 attempts)
  ```

Sustained drift means `rates.yaml` no longer matches the vendor's prices. That
is the failure this design exists to surface — under the rejected
provider-cost-of-truth design it would have been invisible by construction.

## Failure handling

Consistent with the gateway's posture that accounting must never break relaying:

- Missing or malformed usage details → zeros, priced as today.
- Absent `cost_in_usd_ticks` → no verification for that attempt, no warning.
- A nil cached rate → cached tokens at full input rate.
- Any drift computation error → skip the check; never fail the attempt.

## Testing

Pricing is a pure function of `(Usage, Rate)` and tests directly:

1. No reasoning, no cache → identical to current pricing (regression guard)
2. Reasoning present, exclusive convention → billed as output
3. Reasoning present, inclusive convention → **not** double-counted
4. `total_tokens` absent → inclusive assumed
5. Cached tokens with a configured cached rate → discounted
6. Cached tokens with nil cached rate → full input rate
7. `cached_input_per_mtok: 0.0` explicitly set → genuinely free (proves the
   pointer distinguishes it from unconfigured)
8. Counterfactual includes reasoning tokens
9. Drift above both thresholds → flagged
10. Drift above percentage but below absolute floor → not flagged
11. Provider cost absent → no drift computed

Migration tests follow the existing pattern: a v4 database opens, migrates to
v5, and its pre-existing rows price unchanged.

The three measured xAI calls become a golden-file test of the pricing formula —
they are real observations with known-correct answers.

## Sequencing against the delegation-hooks work

Independent; either can land first. This one is a correctness fix to numbers
already being recorded, so it arguably has the stronger claim: every attempt
logged before it lands is mispriced, and it does not backfill.

## Open question for review

Should `usage` visually mark rows whose cost predates this change? They are
priced by the old formula and are not comparable to newer rows. A footnote when
the window spans the migration would be honest; a per-row marker may be more
noise than it is worth.
