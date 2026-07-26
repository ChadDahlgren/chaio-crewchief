// Package cli implements the chaio-crewchief binary's operator subcommands
// (doctor, usage). Pure logic is kept separate from I/O for testability;
// the doctor logic is a Go port of the fleet-built tools/doctor.py.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/chome"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/embed"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/gwurl"
)

// ParseEnvFile parses KEY=VALUE lines: blank/# lines skipped, whitespace
// stripped, one pair of matching quotes stripped, later duplicates win.
func ParseEnvFile(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 && (val[0] == '\'' || val[0] == '"') && val[0] == val[len(val)-1] {
			val = val[1 : len(val)-1]
		}
		out[key] = val
	}
	return out
}

type PresetCheck struct {
	Name          string
	ProviderClass string
	Issues        []string
}

type presetInfo struct {
	Name          string `json:"name"`
	APIKeyEnv     string `json:"api_key_env"`
	ProviderClass string `json:"provider_class"`
}

// CheckPresets flags presets whose key env is missing/empty or whose
// provider_class is unknown.
func CheckPresets(models []presetInfo, env map[string]string) []PresetCheck {
	out := make([]PresetCheck, 0, len(models))
	for _, m := range models {
		c := PresetCheck{Name: m.Name, ProviderClass: m.ProviderClass, Issues: []string{}}
		if m.APIKeyEnv != "" && env[m.APIKeyEnv] == "" {
			c.Issues = append(c.Issues, "missing key: "+m.APIKeyEnv)
		}
		switch m.ProviderClass {
		case "local", "cloud", "frontier":
		default:
			c.Issues = append(c.Issues, "unknown provider_class: "+m.ProviderClass)
		}
		out = append(out, c)
	}
	return out
}

type healthResp struct {
	Status string `json:"status"`
	Models []struct {
		Name    string `json:"name"`
		Healthy bool   `json:"healthy"`
	} `json:"models"`
}

// Summarize renders the doctor report: gateway status, one line per model
// (UNHEALTHY when unhealthy or misconfigured), and a ready count.
func Summarize(health healthResp, checks []PresetCheck) string {
	issuesFor := map[string][]string{}
	for _, c := range checks {
		issuesFor[c.Name] = c.Issues
	}
	var b strings.Builder
	fmt.Fprintf(&b, "gateway: %s\n", health.Status)
	ready := 0
	for _, m := range health.Models {
		issues := issuesFor[m.Name]
		ok := m.Healthy && len(issues) == 0
		label := "OK"
		if !ok {
			label = "UNHEALTHY"
		} else {
			ready++
		}
		if len(issues) > 0 {
			fmt.Fprintf(&b, "  %s: %s; %s\n", m.Name, label, strings.Join(issues, "; "))
		} else {
			fmt.Fprintf(&b, "  %s: %s\n", m.Name, label)
		}
	}
	fmt.Fprintf(&b, "%d of %d models ready", ready, len(health.Models))
	return b.String()
}

func fetchJSON(url string, v any) error {
	hc := &http.Client{Timeout: 10 * time.Second}
	resp, err := hc.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %d", url, resp.StatusCode)
	}
	return json.Unmarshal(body, v)
}

// Doctor runs the diagnostic against the gateway (arg 0: optional env file
// whose keys augment the process env for key checks). Returns exit code.
func Doctor(w io.Writer, args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(w)
	var mf ModeFlags
	mf.Register(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}

	mode, base, err := mf.Resolve()
	if err != nil {
		fmt.Fprintf(w, "error: %v\n", err)
		return 2
	}

	var inst *embed.Instance
	if mode == gwurl.ModeEmbedded {
		paths, err := chome.Resolve()
		if err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
			return 1
		}
		fmt.Fprintf(w, "mode: embedded (home %s)\n", paths.Home)
		inst, err = embed.Start(context.Background(), embed.Config{Paths: paths})
		if err != nil {
			fmt.Fprintf(w, "error: start embedded gateway: %v\n", err)
			return 1
		}
		defer inst.Close()
		base = inst.BaseURL
	} else {
		fmt.Fprintf(w, "mode: gateway (%s)\n", base)
	}

	env := map[string]string{}
	for _, kv := range os.Environ() {
		if eq := strings.Index(kv, "="); eq > 0 {
			env[kv[:eq]] = kv[eq+1:]
		}
	}
	rest := fs.Args()
	if len(rest) > 0 {
		data, err := os.ReadFile(rest[0])
		if err != nil {
			fmt.Fprintf(w, "cannot read env file %s: %v\n", rest[0], err)
			return 2
		}
		for k, v := range ParseEnvFile(string(data)) {
			env[k] = v
		}
	}

	var health healthResp
	if err := fetchJSON(base+"/health", &health); err != nil {
		fmt.Fprintf(w, "gateway: UNREACHABLE at %s (%v)\n", base, err)
		return 2
	}
	var models []presetInfo
	if err := fetchJSON(base+"/models", &models); err != nil {
		fmt.Fprintf(w, "gateway: /models failed (%v)\n", err)
		return 2
	}
	report := Summarize(health, CheckPresets(models, env))
	fmt.Fprintln(w, report)
	if strings.Contains(report, "UNHEALTHY") {
		return 1
	}
	return 0
}
