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
//
// A non-absolute override is rejected rather than accepted and quietly
// resolved. Two values are worth naming. A literal unexpanded "~" is exactly
// what a quoted value in an MCP server config JSON produces, and it used to
// create a directory actually named "~" in whatever the working directory was,
// reporting success indistinguishable from having written the real home. Any
// relative value resolves per-CWD, and as this package's doc comment says,
// Claude Code launches the MCP server with an arbitrary working directory — so
// the CLI and the MCP session would silently use different ledgers and
// different lock directories, which also means each would see the other's
// in-flight rows as unowned. Failing here is the only way the user finds out.
func Dir() (string, error) {
	if v := os.Getenv(EnvHome); v != "" {
		if !filepath.IsAbs(v) {
			return "", fmt.Errorf("%s must be an absolute path, got %q (shell ~ is not expanded inside a quoted value; write the full path)", EnvHome, v)
		}
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory (set %s to override): %w", EnvHome, err)
	}
	return filepath.Join(home, dirName), nil
}

// dbName is the ledger filename inside a home.
const dbName = "chaio-crewchief.db"

// LocksDirFor returns the ownership-lock directory for a database path.
//
// This exists so there is exactly one derivation. The lock directory must be a
// pure function of the database location, because two processes sharing a
// ledger must agree on where the locks live or they will declare each other
// dead: an embedded `mcp` that looked in the wrong directory would find no
// lock file for a running `serve`'s PID, conclude that process had exited, and
// mark its genuinely in-flight rows failed. `serve` takes an explicit --db and
// has no home, so it cannot use Paths.Locks; it calls this instead, and the
// two agree by construction rather than by coincidence.
func LocksDirFor(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "locks")
}

// ResolveIn builds the paths under an explicit home directory.
func ResolveIn(home string) Paths {
	db := filepath.Join(home, dbName)
	return Paths{
		Home:    home,
		Models:  filepath.Join(home, "models.yaml"),
		Rates:   filepath.Join(home, "rates.yaml"),
		Routing: filepath.Join(home, "routing.yaml"),
		DB:      db,
		Archive: filepath.Join(home, "archive"),
		Locks:   LocksDirFor(db),
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
