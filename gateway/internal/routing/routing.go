// Package routing resolves a language hint to a preferred model name via a
// plain data lookup (routing.yaml) — never inference. This is data the
// operator/brain maintains (informed by chaio-bench's offline grading);
// Crew Chief only ever reads it.
package routing

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Table struct {
	routes map[string]string // normalized (lowercase) lang -> model name
}

func (t *Table) Resolve(lang string) (string, bool) {
	if lang == "" {
		return "", false
	}
	m, ok := t.routes[strings.ToLower(lang)]
	return m, ok
}

type yamlConfig struct {
	Routes map[string]string `yaml:"routes"`
}

// Load reads routing.yaml at path. A missing file is not an error — it
// returns an empty table so every request falls back to the registry
// default, matching Crew Chief's rule that absence of config is never fatal.
func Load(path string) (*Table, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Table{routes: map[string]string{}}, nil
		}
		return nil, err
	}
	var cfg yamlConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid routing YAML: %w", err)
	}
	routes := make(map[string]string, len(cfg.Routes))
	for lang, model := range cfg.Routes {
		if lang == "" {
			continue
		}
		routes[strings.ToLower(lang)] = model
	}
	return &Table{routes: routes}, nil
}
