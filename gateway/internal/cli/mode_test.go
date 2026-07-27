package cli

import (
	"errors"
	"flag"
	"testing"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/gwurl"
)

func TestModeFlags(t *testing.T) {
	tests := []struct {
		name     string
		envURL   string
		args     []string
		wantMode gwurl.Mode
		wantErr  error
	}{
		{name: "no env, no flag", envURL: "", args: nil, wantMode: gwurl.ModeEmbedded},
		{name: "env set", envURL: "http://gx10:8181", args: nil, wantMode: gwurl.ModeGateway},
		{name: "--local overrides env", envURL: "http://gx10:8181", args: []string{"--local"}, wantMode: gwurl.ModeEmbedded},
		{name: "--gateway with env", envURL: "http://gx10:8181", args: []string{"--gateway"}, wantMode: gwurl.ModeGateway},
		{name: "--gateway with no URL is an error", envURL: "", args: []string{"--gateway"}, wantErr: ErrNoGatewayURL},
		{name: "both flags is an error", envURL: "", args: []string{"--local", "--gateway"}, wantErr: ErrModeConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CHAIO_CREWCHIEF_URL", tt.envURL)
			t.Setenv("CREWCHIEF_URL", "")
			t.Setenv("DISPATCH_URL", "")

			var mf ModeFlags
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			mf.Register(fs)
			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("parse: %v", err)
			}
			mode, _, err := mf.Resolve()
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Resolve() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if mode != tt.wantMode {
				t.Errorf("mode = %q, want %q", mode, tt.wantMode)
			}
		})
	}
}
