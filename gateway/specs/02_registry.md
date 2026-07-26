Package: `registry`, file path `internal/registry/registry.go`.

Types you consume, verbatim from `dispatch/internal/types/types.go` (import
`"dispatch/internal/types"`, do not redefine these — use them by name):

```go
package types

type Preset struct {
	Name          string  `yaml:"name" json:"name"`
	BaseURL       string  `yaml:"base_url" json:"base_url"`
	SystemPrompt  string  `yaml:"system_prompt" json:"system_prompt"`
	Suffix        string  `yaml:"suffix" json:"suffix"`
	Temperature   float64 `yaml:"temperature" json:"temperature"`
	MaxTokens     int     `yaml:"max_tokens" json:"max_tokens"`
	ThinkBudget   int     `yaml:"think_budget" json:"think_budget"`
	ThinkEligible bool    `yaml:"think_eligible" json:"think_eligible"`
	TimeoutSec    int     `yaml:"timeout_sec" json:"timeout_sec"`
	Default       bool    `yaml:"default" json:"default"`
}

type Registry interface {
	Get(name string) (Preset, bool)
	Default() (Preset, bool)
	List() []Preset
}
```

The YAML file has a top-level `models:` key holding a list of Preset entries, e.g.:

```yaml
models:
  - name: glm-4.5-air
    base_url: http://localhost:8080
    temperature: 0.3
    default: true
```

Required exported API in package `registry`:

```go
package registry

func LoadRegistry(path string) (types.Registry, error)

// Watch polls path's mtime every 2 seconds; when it changes, reloads the file
// and calls onChange with the newly loaded types.Registry. Runs until ctx is
// canceled. Reload errors are ignored (keep serving the last good registry);
// do not crash or return from bad YAML on a later reload.
func Watch(ctx context.Context, path string, onChange func(types.Registry)) error
```

Behavior table:

| Function | Behavior |
|---|---|
| LoadRegistry(path) | Reads the file at path, YAML-unmarshals into a struct with a `Models []types.Preset` field mapped to the `models:` key. Applies defaults: if Temperature == 0, set to 0.3; if TimeoutSec == 0, set to 120. Then validates (see below). Returns a concrete type implementing types.Registry, or an error. |
| Validation | Error if: any Preset.Name is empty; any two Presets share the same Name; any Preset.BaseURL is empty; more than one Preset has Default == true. Zero presets is allowed (empty registry, no error) as long as the above hold vacuously. |
| Get(name) | Returns (preset, true) if found by exact Name match, else (zero Preset, false). |
| Default() | Returns the single Preset with Default == true and true. If none marked default, and exactly one preset total exists, returns that one preset with true (fallback). Otherwise returns (zero Preset, false). |
| List() | Returns all presets, in the order loaded from YAML. |
| Watch(ctx, path, onChange) | Every 2s, os.Stat(path).ModTime(); if changed since last check, call LoadRegistry(path); on success, call onChange(newRegistry) and update the tracked mtime; on error, log nothing fatal, just skip this cycle and keep the old mtime so it retries next tick. Returns nil when ctx.Done() fires. Use a time.Ticker, not a busy loop. |

Constraints:
- Deps: `gopkg.in/yaml.v3` (import path `gopkg.in/yaml.v3`) and stdlib only.
- Import `"dispatch/internal/types"` for Preset/Registry.
- No package-level globals; the concrete Registry impl holds its own preset slice.

Edge cases you will be tested on:
1. Valid YAML with 2 presets, one marked default: LoadRegistry succeeds, Default() returns the marked one, Get/List work.
2. Two presets with the same name: LoadRegistry returns an error.
3. Two presets both marked default: LoadRegistry returns an error.
4. A preset with temperature omitted (0) and timeout_sec omitted (0): defaults applied (0.3, 120) after load.
5. A preset with empty base_url: LoadRegistry returns an error.
