package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/chome"
)

// exampleModelsYAML is the worked example `init` embeds, commented out, at the
// bottom of the starter file.
//
// It lives here uncommented and as valid standalone YAML so a test can feed it
// straight to registry.LoadRegistry. An example whose keys the loader silently
// drops is the worst kind of wrong: yaml.Unmarshal ignores unknown fields, so a
// misspelled key produces a preset that loads clean and then fails upstream,
// looking like the user's mistake. Keys are verified against types.Preset's
// yaml tags by TestExampleConfigIsValidRegistryInput.
const exampleModelsYAML = `models:
  - name: local
    base_url: http://localhost:11434/v1
    model_id: qwen2.5-coder:7b
    system_prompt: "You are a precise coding assistant."
    timeout_sec: 300
    provider_class: local
    default: true

  - name: cloud
    base_url: https://api.example.com/v1
    model_id: some-model
    api_key_env: EXAMPLE_API_KEY
    system_prompt: "You are a precise coding assistant."
    timeout_sec: 120
    provider_class: cloud
`

// starterHeader is the prose above the empty roster in the file `init` writes.
const starterHeader = `# Crew Chief model roster.
#
# Each entry is a preset Crew Chief can relay a work order to. Any
# OpenAI-compatible chat-completions endpoint works. Uncomment one below, point
# it at something real, and restart your Claude Code session.
#
# Crew Chief never judges what a model returns and never picks a model for
# you — it relays the work order and records what it cost.

models: []

# A local Ollama and a hosted OpenAI-compatible endpoint. api_key_env names an
# environment variable holding the token; never put a key in this file.
#
`

// StarterModelsYAML is the file `init` writes.
//
// Every preset is commented out, so the file parses as an empty roster until
// the user deliberately enables one. Auto-detecting a running Ollama was
// considered and rejected: it hard-codes one vendor into a deliberately
// vendor-neutral project, and it fails confusingly when Ollama is running with
// no models pulled.
//
// It is assembled from exampleModelsYAML rather than written out again, so the
// example the user uncomments is the exact text the tests parse.
var StarterModelsYAML = starterHeader + commentOut(exampleModelsYAML)

// commentOut prefixes every line with "# ", leaving blank lines as a bare "#"
// so the block reads as one comment rather than several.
func commentOut(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = "#"
		} else {
			lines[i] = "# " + line
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// Init writes a starter models.yaml into the resolved home directory.
//
// This is the only code path in the binary that writes configuration, and it
// runs only when invoked by name. Writing config as a side effect of starting
// up would create files in a home directory the user never asked for.
func Init(w io.Writer, args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(w)
	force := fs.Bool("force", false, "overwrite an existing models.yaml")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	paths, err := chome.Resolve()
	if err != nil {
		fmt.Fprintf(w, "error: %v\n", err)
		return 1
	}

	// init takes no positional arguments. Accepting and ignoring one would
	// report success for a file it never wrote, so name the real target and
	// fail instead.
	if fs.NArg() > 0 {
		fmt.Fprintf(w, "usage: chaio-crewchief init [--force]\n\ninit takes no arguments; it always writes %s.\n",
			paths.Models)
		return 2
	}
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		fmt.Fprintf(w, "error: create %s: %v\n", paths.Home, err)
		return 1
	}

	if _, err := os.Stat(paths.Models); err == nil && !*force {
		fmt.Fprintf(w, "%s already exists; not overwriting. Use --force to replace it.\n", paths.Models)
		return 1
	}

	if err := os.WriteFile(paths.Models, []byte(StarterModelsYAML), 0o600); err != nil {
		fmt.Fprintf(w, "error: write %s: %v\n", paths.Models, err)
		return 1
	}

	fmt.Fprintf(w, "Wrote %s\n\nEdit it to add a model, then restart your Claude Code session.\n", paths.Models)
	return 0
}
