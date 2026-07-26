package cli

import (
	"errors"
	"flag"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/gwurl"
)

// ModeFlags let a subcommand override the environment's choice of ledger.
//
// A user with CHAIO_CREWCHIEF_URL exported would otherwise have no way to look
// at their local ledger, and a user without it no way to look at a shared one.
// Both ledgers are real and they hold different work.
type ModeFlags struct {
	local   bool
	gateway bool
}

// Register adds --local and --gateway to a flag set.
func (m *ModeFlags) Register(fs *flag.FlagSet) {
	fs.BoolVar(&m.local, "local", false, "read the local embedded ledger, ignoring CHAIO_CREWCHIEF_URL")
	fs.BoolVar(&m.gateway, "gateway", false, "query the gateway named by CHAIO_CREWCHIEF_URL")
}

// Resolve reports the mode to use and, in gateway mode, the URL.
func (m *ModeFlags) Resolve() (gwurl.Mode, string, error) {
	if m.local && m.gateway {
		return "", "", errors.New("--local and --gateway are mutually exclusive")
	}
	envMode, url := gwurl.Resolve()
	switch {
	case m.local:
		return gwurl.ModeEmbedded, "", nil
	case m.gateway:
		if url == "" {
			return "", "", errors.New("--gateway given but no gateway URL is set; export CHAIO_CREWCHIEF_URL")
		}
		return gwurl.ModeGateway, url, nil
	default:
		return envMode, url, nil
	}
}
