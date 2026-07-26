// Package gwurl resolves the gateway's base URL from the environment.
//
// This lives in one place on purpose. It was previously duplicated between
// internal/cli and internal/mcpserve, and the two copies drifted during the
// rename to chaio-crewchief: one learned the new variable name and the other
// didn't, so `chaio-crewchief usage` silently ignored a perfectly valid
// CREWCHIEF_URL. One resolver, one behavior.
package gwurl

import "os"

// DefaultURL is where the gateway listens out of the box.
const DefaultURL = "http://localhost:8181"

// envKeys are consulted in order, most current name first. The older names
// are honored so configs written under this project's previous names
// (Crew Chief, and Dispatch before that) keep working.
var envKeys = []string{"CHAIO_CREWCHIEF_URL", "CREWCHIEF_URL", "DISPATCH_URL"}

// URL returns the first non-empty gateway URL found in the environment,
// falling back to DefaultURL.
func URL() string {
	for _, key := range envKeys {
		if u := os.Getenv(key); u != "" {
			return u
		}
	}
	return DefaultURL
}
