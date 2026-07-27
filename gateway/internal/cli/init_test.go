package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/chome"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/embed"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/registry"
)

func TestInitWritesStarterConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CHAIO_CREWCHIEF_HOME", home)

	var out bytes.Buffer
	if code := Init(&out, nil); code != 0 {
		t.Fatalf("Init() = %d, want 0; output: %s", code, out.String())
	}
	path := filepath.Join(home, "models.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("models.yaml not written: %v", err)
	}
	if !strings.Contains(out.String(), path) {
		t.Errorf("output %q does not name the file it wrote", out.String())
	}
}

// A starter file that the registry rejects is worse than none: the user edits
// it, restarts, and gets a parse error they did not cause.
func TestStarterConfigIsValidRegistryInput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CHAIO_CREWCHIEF_HOME", home)
	if code := Init(&bytes.Buffer{}, nil); code != 0 {
		t.Fatalf("Init() = %d, want 0", code)
	}
	if _, err := registry.LoadRegistry(filepath.Join(home, "models.yaml")); err != nil {
		t.Errorf("LoadRegistry(starter) error = %v; the starter file must parse", err)
	}
}

// The emitted starter file is `models: []` plus comments, so parsing it proves
// nothing about the example a user actually uncomments. Parse the example
// itself, and check the fields survive the round trip: yaml.Unmarshal ignores
// unknown keys, so a key that does not match types.Preset's yaml tag loads
// clean and leaves the field empty.
func TestExampleConfigIsValidRegistryInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.yaml")
	if err := os.WriteFile(path, []byte(exampleModelsYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	reg, err := registry.LoadRegistry(path)
	if err != nil {
		t.Fatalf("LoadRegistry(example) error = %v; the commented example must parse", err)
	}
	presets := reg.List()
	if len(presets) == 0 {
		t.Fatal("example yielded no presets")
	}
	for _, p := range presets {
		if p.Name == "" {
			t.Error("example preset has an empty Name")
		}
		if p.BaseURL == "" {
			t.Errorf("preset %q has an empty BaseURL; check the base_url key against types.Preset", p.Name)
		}
		if p.ModelID == "" {
			t.Errorf("preset %q has an empty ModelID; check the model_id key against types.Preset", p.Name)
		}
		if p.SystemPrompt == "" {
			t.Errorf("preset %q has an empty SystemPrompt; check the system_prompt key", p.Name)
		}
		if p.TimeoutSec == 0 {
			t.Errorf("preset %q has a zero TimeoutSec; check the timeout_sec key", p.Name)
		}
		if p.ProviderClass == "" {
			t.Errorf("preset %q has an empty ProviderClass; check the provider_class key", p.Name)
		}
	}
	if p, ok := reg.Get("cloud"); !ok {
		t.Error(`example has no preset named "cloud"`)
	} else if p.APIKeyEnv == "" {
		t.Error("cloud preset has an empty APIKeyEnv; check the api_key_env key")
	}
	if _, ok := reg.Default(); !ok {
		t.Error("example declares no default preset; check the default key")
	}
}

// The starter file must embed exactly the example the test above validated,
// or the two drift and the validation stops meaning anything.
func TestStarterEmbedsTheValidatedExample(t *testing.T) {
	if !strings.Contains(StarterModelsYAML, commentOut(exampleModelsYAML)) {
		t.Error("StarterModelsYAML does not contain the commented-out exampleModelsYAML verbatim")
	}
	// Uncommenting by hand is what a user does; it must give the example back.
	var got []string
	for _, line := range strings.Split(commentOut(exampleModelsYAML), "\n") {
		switch {
		case line == "#":
			got = append(got, "")
		case strings.HasPrefix(line, "# "):
			got = append(got, strings.TrimPrefix(line, "# "))
		default:
			got = append(got, line)
		}
	}
	if strings.Join(got, "\n") != exampleModelsYAML {
		t.Error("un-commenting the embedded block does not reproduce exampleModelsYAML")
	}
}

func TestInitRejectsPositionalArgs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CHAIO_CREWCHIEF_HOME", home)

	var out bytes.Buffer
	if code := Init(&out, []string{"somewhere.yaml"}); code != 2 {
		t.Errorf("Init(somewhere.yaml) = %d, want 2", code)
	}
	if _, err := os.Stat(filepath.Join(home, "models.yaml")); err == nil {
		t.Error("Init wrote models.yaml despite rejecting the arguments")
	}
	if !strings.Contains(out.String(), filepath.Join(home, "models.yaml")) {
		t.Errorf("usage %q does not name the real target path", out.String())
	}
}

func TestInitRefusesToOverwrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CHAIO_CREWCHIEF_HOME", home)
	path := filepath.Join(home, "models.yaml")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("models: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if code := Init(&out, nil); code == 0 {
		t.Fatal("Init() = 0 over an existing file, want non-zero")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "models: []\n" {
		t.Error("Init overwrote an existing models.yaml without --force")
	}
	if !strings.Contains(out.String(), "--force") {
		t.Errorf("refusal %q does not mention --force", out.String())
	}
}

func TestInitForceOverwrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CHAIO_CREWCHIEF_HOME", home)
	path := filepath.Join(home, "models.yaml")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("models: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if code := Init(&bytes.Buffer{}, []string{"--force"}); code != 0 {
		t.Fatalf("Init(--force) = %d, want 0", code)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) == "models: []\n" {
		t.Error("Init(--force) did not overwrite")
	}
}

// End to end through the real `init`: the file it writes must leave the
// embedded gateway reporting "no models configured", so delegation returns the
// actionable guidance rather than a 500.
//
// `init` writes `models: []` with every preset commented out. That parses fine,
// so ModelsConfigured — keyed off the absence of a load error — came back true
// with an empty roster, and following the documented setup made the failure
// strictly less helpful: with no config, delegation named the file to create;
// after `init`, the same call returned a bare 500.
func TestInitLeavesModelsReportedAsNotConfigured(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CHAIO_CREWCHIEF_HOME", home)

	var out bytes.Buffer
	if code := Init(&out, nil); code != 0 {
		t.Fatalf("Init() = %d, want 0; output: %s", code, out.String())
	}

	inst, err := embed.Start(context.Background(), embed.Config{Paths: chome.ResolveIn(home)})
	if err != nil {
		t.Fatalf("embed.Start() after init error = %v", err)
	}
	defer inst.Close()

	if inst.ModelsConfigured {
		t.Error("ModelsConfigured = true after `init`, want false: every preset it writes is commented out")
	}
}

// The success message must say the file is inert. "Edit it to add a model"
// read as optional polish next to a file that visibly contains two presets.
func TestInitSuccessMessageSaysPresetsAreCommentedOut(t *testing.T) {
	t.Setenv("CHAIO_CREWCHIEF_HOME", t.TempDir())

	var out bytes.Buffer
	if code := Init(&out, nil); code != 0 {
		t.Fatalf("Init() = %d, want 0", code)
	}
	msg := out.String()
	for _, want := range []string{"commented out", "Uncomment"} {
		if !strings.Contains(msg, want) {
			t.Errorf("init message %q, want it to mention %q", msg, want)
		}
	}
}
