// Package gwurl resolves the gateway's base URL from the environment.
//
// This lives in one place on purpose. It was previously duplicated between
// internal/cli and internal/mcpserve, and the two copies drifted during the
// rename to chaio-crewchief: one learned the new variable name and the other
// didn't, so `chaio-crewchief usage` silently ignored a perfectly valid
// CREWCHIEF_URL. One resolver, one behavior.
package gwurl

import "os"

// Mode is how this process reaches a gateway.
type Mode string

const (
	// ModeGateway proxies to a gateway someone else is running.
	ModeGateway Mode = "gateway"
	// ModeEmbedded runs the gateway in this process.
	ModeEmbedded Mode = "embedded"
)

// envKeys are consulted in order, most current name first. The older names
// are honored so configs written under this project's previous names
// (Crew Chief, and Dispatch before that) keep working.
var envKeys = []string{"CHAIO_CREWCHIEF_URL", "CREWCHIEF_URL", "DISPATCH_URL"}

// URLFromEnv returns the first non-empty gateway URL in the environment, or ""
// if none is set.
func URLFromEnv() string {
	for _, key := range envKeys {
		if u := os.Getenv(key); u != "" {
			return u
		}
	}
	return ""
}

// Resolve reports which mode to run in, and the gateway URL when there is one.
//
// An unset variable means embedded, not localhost:8181. That default was the
// worst of the available options: it produced a plugin that registered
// successfully and then failed every call against a port nothing was listening
// on, with nothing anywhere saying a second process was required.
func Resolve() (Mode, string) {
	if u := URLFromEnv(); u != "" {
		return ModeGateway, u
	}
	return ModeEmbedded, ""
}
