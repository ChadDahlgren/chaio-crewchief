package gwurl

import "testing"

func TestResolve(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		wantMode Mode
		wantURL  string
	}{
		{
			name:     "no variables set means embedded",
			env:      map[string]string{},
			wantMode: ModeEmbedded,
			wantURL:  "",
		},
		{
			name:     "current variable selects gateway",
			env:      map[string]string{"CHAIO_CREWCHIEF_URL": "http://gx10:8181"},
			wantMode: ModeGateway,
			wantURL:  "http://gx10:8181",
		},
		{
			name:     "legacy CREWCHIEF_URL still works",
			env:      map[string]string{"CREWCHIEF_URL": "http://old:8181"},
			wantMode: ModeGateway,
			wantURL:  "http://old:8181",
		},
		{
			name:     "legacy DISPATCH_URL still works",
			env:      map[string]string{"DISPATCH_URL": "http://older:8181"},
			wantMode: ModeGateway,
			wantURL:  "http://older:8181",
		},
		{
			name: "current variable wins over legacy",
			env: map[string]string{
				"CHAIO_CREWCHIEF_URL": "http://new:8181",
				"DISPATCH_URL":        "http://older:8181",
			},
			wantMode: ModeGateway,
			wantURL:  "http://new:8181",
		},
		{
			name:     "empty value is treated as unset",
			env:      map[string]string{"CHAIO_CREWCHIEF_URL": ""},
			wantMode: ModeEmbedded,
			wantURL:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range envKeys {
				t.Setenv(k, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			mode, url := Resolve()
			if mode != tt.wantMode {
				t.Errorf("mode = %q, want %q", mode, tt.wantMode)
			}
			if url != tt.wantURL {
				t.Errorf("url = %q, want %q", url, tt.wantURL)
			}
		})
	}
}
