package chome

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDirPrefersEnvOverride(t *testing.T) {
	t.Setenv("CHAIO_CREWCHIEF_HOME", "/custom/place")
	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	if got != "/custom/place" {
		t.Errorf("Dir() = %q, want /custom/place", got)
	}
}

func TestDirFallsBackToHomeDotDir(t *testing.T) {
	t.Setenv("CHAIO_CREWCHIEF_HOME", "")
	t.Setenv("HOME", "/home/tester")
	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	want := filepath.Join("/home/tester", ".chaio-crewchief")
	if got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestResolveInBuildsAllPaths(t *testing.T) {
	p := ResolveIn("/h")
	cases := map[string]string{
		"Home":    "/h",
		"Models":  "/h/models.yaml",
		"Rates":   "/h/rates.yaml",
		"Routing": "/h/routing.yaml",
		"DB":      "/h/chaio-crewchief.db",
		"Archive": "/h/archive",
		"Locks":   "/h/locks",
	}
	got := map[string]string{
		"Home": p.Home, "Models": p.Models, "Rates": p.Rates,
		"Routing": p.Routing, "DB": p.DB, "Archive": p.Archive, "Locks": p.Locks,
	}
	for field, want := range cases {
		if got[field] != want {
			t.Errorf("%s = %q, want %q", field, got[field], want)
		}
	}
}

// The whole point of LocksDirFor: an embedded process resolving a home and a
// `serve` given that home's database as --db must land on the same lock
// directory. If they diverge, each finds no lock file for the other's PID,
// concludes it is dead, and fails its live rows.
func TestLocksDirForAgreesWithResolvedHome(t *testing.T) {
	p := ResolveIn("/h")
	if got := LocksDirFor(p.DB); got != p.Locks {
		t.Errorf("LocksDirFor(%q) = %q, want %q", p.DB, got, p.Locks)
	}
}

// serve's --db default is relative, so the derivation must survive that too.
func TestLocksDirForRelativePath(t *testing.T) {
	if got := LocksDirFor("./chaio-crewchief.db"); got != "locks" {
		t.Errorf("LocksDirFor(\"./chaio-crewchief.db\") = %q, want %q", got, "locks")
	}
}

// A non-absolute CHAIO_CREWCHIEF_HOME cannot work, so it must fail loudly
// rather than resolve per-CWD.
func TestDirRejectsNonAbsoluteHome(t *testing.T) {
	cases := map[string]string{
		// Exactly what a quoted value in an MCP server config JSON produces:
		// no shell runs, so the tilde stays literal and used to create a
		// directory named "~" in the working directory.
		"unexpanded tilde":      "~",
		"unexpanded tilde path": "~/.chaio-crewchief",
		// Claude Code launches the MCP server with an arbitrary working
		// directory, so a relative home means the CLI and the MCP session use
		// different ledgers and different lock directories.
		"relative":     "crewchief",
		"dot relative": "./crewchief",
		"parent":       "../crewchief",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(EnvHome, value)
			got, err := Dir()
			if err == nil {
				t.Fatalf("Dir() = %q, nil; want an error for %q", got, value)
			}
			if !strings.Contains(err.Error(), EnvHome) {
				t.Errorf("Dir() error = %v, want it to name %s", err, EnvHome)
			}
			if !strings.Contains(err.Error(), value) {
				t.Errorf("Dir() error = %v, want it to quote the offending value %q", err, value)
			}
		})
	}
}

func TestDirAcceptsAbsoluteHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvHome, dir)
	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	if got != dir {
		t.Errorf("Dir() = %q, want %q", got, dir)
	}
}
