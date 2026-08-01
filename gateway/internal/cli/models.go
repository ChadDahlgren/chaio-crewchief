package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/chome"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/registry"
)

// Models prints the configured roster. It reads models.yaml only — no health
// probes, so it answers "what is configured" in milliseconds where `doctor`
// answers "what is reachable" and pays a network round trip per preset.
//
// It resolves the registry through chome like every other subcommand. The
// gateway's own --models flag defaults to a working-directory-relative
// "./models.yaml", which is meaningless for an operator command that can be run
// from anywhere.
func Models(w io.Writer, args []string) int {
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	fs.SetOutput(w)
	path := fs.String("models", "", "registry YAML path (default: the resolved home)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	resolved := *path
	if resolved == "" {
		paths, err := chome.Resolve()
		if err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
			return 1
		}
		resolved = paths.Models
	}

	reg, err := registry.LoadRegistry(resolved)
	if err != nil {
		fmt.Fprintf(w, "error: load registry: %v\n", err)
		return 1
	}

	presets := reg.List()
	if len(presets) == 0 {
		fmt.Fprintf(w, "No models configured in %s.\n\n", resolved)
		fmt.Fprintf(w, "Delegation will refuse until at least one preset is enabled.\n")
		fmt.Fprintf(w, "Run `chaio-crewchief init` to write a starter file, then uncomment a preset.\n")
		return 0
	}

	fmt.Fprintf(w, "registry: %s\n\n", resolved)
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCLASS\tMODEL ID\tBASE URL")
	for _, p := range presets {
		class := p.ProviderClass
		if class == "" {
			class = "local"
		}
		name := p.Name
		if p.Default {
			name += " (default)"
		}
		modelID := p.ModelID
		if modelID == "" {
			modelID = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, class, modelID, p.BaseURL)
	}
	if err := tw.Flush(); err != nil {
		fmt.Fprintf(w, "error: %v\n", err)
		return 1
	}
	return 0
}
