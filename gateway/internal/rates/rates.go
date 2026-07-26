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

// Rate is one model's $/1M token pricing.
type Rate struct {
	InputPerMTok  float64 `yaml:"input_per_mtok"`
	OutputPerMTok float64 `yaml:"output_per_mtok"`
}

func (r Rate) price(promptTokens, outputTokens int) float64 {
	return float64(promptTokens)/1_000_000*r.InputPerMTok + float64(outputTokens)/1_000_000*r.OutputPerMTok
}

// Table prices attempts against known models and a frontier counterfactual.
type Table interface {
	// Price returns the USD cost of promptTokens+outputTokens against model's
	// rate, or 0 if model is not in the table (treated as local/electricity).
	Price(model string, promptTokens, outputTokens int) float64
	// Counterfactual returns what the same token counts would cost at the
	// configured frontier reference rate.
	Counterfactual(promptTokens, outputTokens int) float64
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

func (t *tableImpl) Price(model string, promptTokens, outputTokens int) float64 {
	r, ok := t.models[model]
	if !ok {
		return 0
	}
	return r.price(promptTokens, outputTokens)
}

func (t *tableImpl) Counterfactual(promptTokens, outputTokens int) float64 {
	return t.counterfactual.price(promptTokens, outputTokens)
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
