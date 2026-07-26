// Package chome resolves where Crew Chief keeps its files when nobody passed
// explicit paths.
//
// `serve` takes every path as a flag, which works for a systemd unit with a
// WorkingDirectory. The MCP server has no such luxury: Claude Code launches it
// with an arbitrary working directory, so "./models.yaml" means nothing. This
// package is the answer to "then where?", and it is deliberately the only
// answer, so the CLI and the MCP server can never disagree.
package chome

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvHome overrides the default location entirely.
const EnvHome = "CHAIO_CREWCHIEF_HOME"

// dirName is the default directory, created under the user's home.
//
// A single directory holding both config and ledger is a deliberate choice
// over the XDG config/data split: the split is correct about filesystem
// taxonomy and wrong about what people do, which is go look at their files in
// one place. EnvHome exists for anyone who disagrees.
const dirName = ".chaio-crewchief"

// Paths are the files and directories Crew Chief uses inside a home.
type Paths struct {
	Home    string
	Models  string
	Rates   string
	Routing string
	DB      string
	Archive string
	Locks   string
}

// Dir returns the resolved home directory. It does not create it.
func Dir() (string, error) {
	if v := os.Getenv(EnvHome); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory (set %s to override): %w", EnvHome, err)
	}
	return filepath.Join(home, dirName), nil
}

// ResolveIn builds the paths under an explicit home directory.
func ResolveIn(home string) Paths {
	return Paths{
		Home:    home,
		Models:  filepath.Join(home, "models.yaml"),
		Rates:   filepath.Join(home, "rates.yaml"),
		Routing: filepath.Join(home, "routing.yaml"),
		DB:      filepath.Join(home, "chaio-crewchief.db"),
		Archive: filepath.Join(home, "archive"),
		Locks:   filepath.Join(home, "locks"),
	}
}

// Resolve builds the paths under the resolved home directory.
func Resolve() (Paths, error) {
	home, err := Dir()
	if err != nil {
		return Paths{}, err
	}
	return ResolveIn(home), nil
}
