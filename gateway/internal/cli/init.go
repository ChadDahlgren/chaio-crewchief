package cli

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/chome"
)

// StarterModelsYAML is the file `init` writes.
//
// Every preset is commented out, so the file parses as an empty roster until
// the user deliberately enables one. Auto-detecting a running Ollama was
// considered and rejected: it hard-codes one vendor into a deliberately
// vendor-neutral project, and it fails confusingly when Ollama is running with
// no models pulled.
const StarterModelsYAML = `# Crew Chief model roster.
#
# Each entry is a preset Crew Chief can relay a work order to. Any
# OpenAI-compatible chat-completions endpoint works. Uncomment one, point it at
# something real, and restart your Claude Code session.
#
# Crew Chief never judges what a model returns and never picks a model for
# you — it relays the work order and records what it cost.

models: []

# A local Ollama:
#
# models:
#   - name: local
#     base_url: http://localhost:11434/v1
#     model: qwen2.5-coder:7b
#     system_prompt: "You are a precise coding assistant."
#     timeout_sec: 300
#     provider_class: local
#     default: true
#
# Any hosted OpenAI-compatible endpoint. api_key_env names an environment
# variable; never put a key in this file.
#
# models:
#   - name: cloud
#     base_url: https://api.example.com/v1
#     model: some-model
#     api_key_env: EXAMPLE_API_KEY
#     system_prompt: "You are a precise coding assistant."
#     timeout_sec: 120
#     provider_class: cloud
`

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
