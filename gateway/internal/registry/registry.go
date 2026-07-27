package registry

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/types"
	"gopkg.in/yaml.v3"
)

var (
	ErrEmptyName        = errors.New("preset name cannot be empty")
	ErrDuplicateName    = errors.New("duplicate preset name found")
	ErrEmptyBaseURL     = errors.New("preset base_url cannot be empty")
	ErrMultipleDefaults = errors.New("multiple presets marked as default")
	ErrInvalidYAML      = errors.New("invalid YAML structure")
	ErrPresetValidation = errors.New("preset validation failed")

	// ErrNotFound reports that the registry file does not exist, as distinct from
	// existing and being unreadable or malformed. Embedded mode tolerates a
	// missing models.yaml — a fresh install has none — but a malformed one must
	// stay a loud failure, because silently treating a YAML typo as "no models
	// configured" would send the user hunting in the wrong place.
	ErrNotFound = errors.New("registry file not found")
)

type registryImpl struct {
	presets []types.Preset
	mu      sync.RWMutex
}

func (r *registryImpl) Get(name string) (types.Preset, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.presets {
		if p.Name == name {
			return p, true
		}
	}
	return types.Preset{}, false
}

func (r *registryImpl) Default() (types.Preset, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var defaultPreset types.Preset
	var defaultCount int
	var singlePreset types.Preset

	if len(r.presets) == 1 {
		singlePreset = r.presets[0]
	}

	for _, p := range r.presets {
		if p.Default {
			defaultPreset = p
			defaultCount++
		}
	}

	if defaultCount == 1 {
		return defaultPreset, true
	}

	if len(r.presets) == 1 {
		return singlePreset, true
	}

	return types.Preset{}, false
}

func (r *registryImpl) List() []types.Preset {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]types.Preset, len(r.presets))
	copy(list, r.presets)
	return list
}

func applyDefaults(p *types.Preset) {
	p.BaseURL = os.ExpandEnv(p.BaseURL)
	if p.Temperature == 0 {
		p.Temperature = 0.3
	}
	if p.TimeoutSec == 0 {
		p.TimeoutSec = 120
	}
	if p.ProviderClass == "" {
		p.ProviderClass = "local"
	}
	if p.HealthPath == "" {
		p.HealthPath = "/health"
	}
}

func validatePresets(presets []types.Preset) error {
	nameSet := make(map[string]struct{})
	var defaultCount int

	for _, p := range presets {
		if p.Name == "" {
			return fmt.Errorf("%w: %v", ErrPresetValidation, ErrEmptyName)
		}
		if _, exists := nameSet[p.Name]; exists {
			return fmt.Errorf("%w: %v", ErrPresetValidation, ErrDuplicateName)
		}
		nameSet[p.Name] = struct{}{}

		if p.BaseURL == "" {
			return fmt.Errorf("%w: %v", ErrPresetValidation, ErrEmptyBaseURL)
		}

		if p.Default {
			defaultCount++
		}
	}

	if defaultCount > 1 {
		return fmt.Errorf("%w: %v", ErrPresetValidation, ErrMultipleDefaults)
	}

	return nil
}

type yamlConfig struct {
	Models []types.Preset `yaml:"models"`
}

func LoadRegistry(path string) (types.Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var yamlConf yamlConfig
	if err := yaml.Unmarshal(data, &yamlConf); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidYAML, err)
	}

	for i := range yamlConf.Models {
		applyDefaults(&yamlConf.Models[i])
	}

	if err := validatePresets(yamlConf.Models); err != nil {
		return nil, err
	}

	return &registryImpl{presets: yamlConf.Models}, nil
}

func Watch(ctx context.Context, path string, onChange func(types.Registry)) error {
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
				newReg, err := LoadRegistry(path)
				if err != nil {
					continue
				}

				onChange(newReg)
				lastModTime = info.ModTime()
			}
		}
	}
}
