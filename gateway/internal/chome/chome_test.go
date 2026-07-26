package chome

import (
	"path/filepath"
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
