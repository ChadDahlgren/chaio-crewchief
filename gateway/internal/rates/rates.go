// Package rates prices Attempts in USD so /stats can answer "frontier
// tokens per unit of verified work." A model missing from rates.yaml is
// priced at 0 (treated as local/electricity), never an error.
package rates

import (
	"context"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Usage is the token breakdown of one attempt, as reported by the provider.
//
// It exists because pricing needs four numbers, not two: cached prompt tokens
// bill at a discount, and reasoning tokens bill as output but are not always
// counted in the provider's completion_tokens.
type Usage struct {
	// PromptTokens is the provider's total prompt count, inclusive of cached.
	PromptTokens int
	// CachedPromptTokens is the subset of PromptTokens served from the
	// provider's cache, billed at CachedInputPerMTok when one is configured.
	CachedPromptTokens int
	// OutputTokens is the provider's completion_tokens verbatim.
	OutputTokens int
	// ReasoningTokens is billable output the provider generated but did not
	// include in OutputTokens. Providers following the OpenAI convention count
	// reasoning inside completion_tokens; for those, the provider layer leaves
	// this at 0 so it is not billed twice. See internal/provider.
	ReasoningTokens int
}

// Rate is one model's $/1M token pricing.
type Rate struct {
	InputPerMTok float64 `yaml:"input_per_mtok"`
	// CachedInputPerMTok prices prompt tokens served from the provider's cache.
	//
	// A pointer because zero is a meaningful rate: providers do offer free
	// cache reads, and a plain float64 could not tell "cached tokens are free"
	// from "the operator never configured this." Guessing the first would price
	// every cache hit at nothing. Nil means unconfigured, and cached tokens
	// bill at InputPerMTok — the conservative reading, and identical to the
	// behavior before this field existed.
	CachedInputPerMTok *float64 `yaml:"cached_input_per_mtok"`
	OutputPerMTok      float64  `yaml:"output_per_mtok"`
}

// cachedRate resolves the effective $/MTok for cached prompt tokens.
func (r Rate) cachedRate() float64 {
	if r.CachedInputPerMTok == nil {
		return r.InputPerMTok
	}
	return *r.CachedInputPerMTok
}

func (r Rate) price(u Usage) float64 {
	// Guard against a provider reporting more cached tokens than prompt tokens;
	// a negative billable-input term would silently credit the attempt.
	cached := u.CachedPromptTokens
	if cached > u.PromptTokens {
		cached = u.PromptTokens
	}
	if cached < 0 {
		cached = 0
	}
	uncached := u.PromptTokens - cached
	output := u.OutputTokens + u.ReasoningTokens

	return float64(uncached)/1_000_000*r.InputPerMTok +
		float64(cached)/1_000_000*r.cachedRate() +
		float64(output)/1_000_000*r.OutputPerMTok
}

// Table prices attempts against known models and a frontier counterfactual.
type Table interface {
	// Price returns the USD cost of u against model's rate, or 0 if model is
	// not in the table (treated as local/electricity).
	Price(model string, u Usage) float64
	// Counterfactual returns what the same usage would cost at the configured
	// frontier reference rate.
	//
	// Cached tokens are priced at the full frontier input rate: a hypothetical
	// frontier run has no warm cache to hit, so discounting them would credit
	// the counterfactual for a saving it would not have had. Reasoning tokens
	// are included for the same reason — the frontier model would have had to
	// generate them too, and excluding them would understate what was avoided.
	Counterfactual(u Usage) float64
	// HasCounterfactual reports whether a frontier reference rate was actually
	// configured. Counterfactual returns 0 both when no rates file exists and
	// when the tokens genuinely price to nothing, and a caller reporting
	// savings must not present the first as the second.
	HasCounterfactual() bool
}

type yamlConfig struct {
	Models         map[string]Rate `yaml:"models"`
	Counterfactual Rate            `yaml:"counterfactual"`
}

type tableImpl struct {
	models         map[string]Rate
	counterfactual Rate
}

func (t *tableImpl) Price(model string, u Usage) float64 {
	r, ok := t.models[model]
	if !ok {
		return 0
	}
	return r.price(u)
}

func (t *tableImpl) Counterfactual(u Usage) float64 {
	// Cached tokens carry no discount in the counterfactual — see Table.
	u.CachedPromptTokens = 0
	return t.counterfactual.price(u)
}

// HasCounterfactual is false for the empty table Load returns when rates.yaml
// is missing, and equally for a rates.yaml with no `counterfactual:` block —
// both leave nothing to compare a local run against.
func (t *tableImpl) HasCounterfactual() bool {
	return t.counterfactual.InputPerMTok != 0 || t.counterfactual.OutputPerMTok != 0
}

// Load reads rates.yaml at path. A missing file is not an error — it
// returns an all-zero (all-local) table so callers can run without one;
// the caller is expected to log a warning once in that case.
func Load(path string) (Table, error) {
	cfg, err := readConfig(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &tableImpl{models: map[string]Rate{}}, nil
		}
		return nil, err
	}
	return &tableImpl{models: cfg.Models, counterfactual: cfg.Counterfactual}, nil
}

func readConfig(path string) (yamlConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return yamlConfig{}, err
	}
	var cfg yamlConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return yamlConfig{}, fmt.Errorf("invalid rates YAML: %w", err)
	}
	if cfg.Models == nil {
		cfg.Models = map[string]Rate{}
	}
	return cfg, nil
}

// Watch polls path for changes and calls onChange with a freshly loaded
// Table, mirroring internal/registry.Watch's hot-reload pattern.
func Watch(ctx context.Context, path string, onChange func(Table)) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastModTime time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if info.ModTime().After(lastModTime) {
				newTable, err := Load(path)
				if err != nil {
					continue
				}
				onChange(newTable)
				lastModTime = info.ModTime()
			}
		}
	}
}
