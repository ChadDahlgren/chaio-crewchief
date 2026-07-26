package mcpserve

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistoryQuery(t *testing.T) {
	cases := []struct {
		model, outcome string
		limit          int
		want           string
	}{
		{"", "", 0, "/history"},
		{"m1", "", 0, "/history?model=m1"},
		{"", "failed", 10, "/history?limit=10&outcome=failed"},
		{"a b", "delivered", 0, "/history?model=a+b&outcome=delivered"},
	}
	for _, c := range cases {
		if got := HistoryQuery(c.model, c.outcome, c.limit); got != c.want {
			t.Errorf("HistoryQuery(%q,%q,%d) = %q, want %q", c.model, c.outcome, c.limit, got, c.want)
		}
	}
}

func TestClientErrorSurfacesGatewayBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unknown model: nope"}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	c := &client{base: srv.URL, hc: &http.Client{}}
	_, err := c.get(context.Background(), "/models", 2*time.Second)
	if err == nil {
		t.Fatal("expected error")
	}
	if want := "unknown model: nope"; !contains(err.Error(), want) {
		t.Errorf("error %q should contain %q", err.Error(), want)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestDelegateRejectsAsyncWhenEmbedded(t *testing.T) {
	err := checkDelegate(Options{Embedded: true, ModelsConfigured: true}, delegateIn{Task: "t", Async: true})
	if err == nil {
		t.Fatal("async accepted in embedded mode; the process dies with the session")
	}
	for _, want := range []string{"async", "CHAIO_CREWCHIEF_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestDelegateAllowsAsyncWhenGateway(t *testing.T) {
	if err := checkDelegate(Options{Embedded: false, ModelsConfigured: true}, delegateIn{Task: "t", Async: true}); err != nil {
		t.Errorf("async rejected in gateway mode: %v", err)
	}
}

func TestDelegateRejectsWhenNoModelsConfigured(t *testing.T) {
	opts := Options{Embedded: true, ModelsConfigured: false, ModelsPath: "/home/u/.chaio-crewchief/models.yaml"}
	err := checkDelegate(opts, delegateIn{Task: "t"})
	if err == nil {
		t.Fatal("delegate accepted with no models configured")
	}
	for _, want := range []string{"/home/u/.chaio-crewchief/models.yaml", "init"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestDelegateAllowedWhenConfigured(t *testing.T) {
	if err := checkDelegate(Options{Embedded: true, ModelsConfigured: true}, delegateIn{Task: "t"}); err != nil {
		t.Errorf("configured sync delegate rejected: %v", err)
	}
}

// The guidance must not send someone who has already run `init` back to `init`.
// Its roster is empty because every preset is commented out, and that is the
// actionable thing to say.
func TestNoModelsGuidanceDependsOnWhetherFileExists(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "models.yaml")
	if got := noModelsGuidance(missing); !strings.Contains(got, "chaio-crewchief init") {
		t.Errorf("guidance for a missing file = %q, want it to suggest init", got)
	}

	existing := filepath.Join(t.TempDir(), "models.yaml")
	if err := os.WriteFile(existing, []byte("models: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := noModelsGuidance(existing)
	if strings.Contains(got, "chaio-crewchief init") {
		t.Errorf("guidance for an existing file = %q, want it not to suggest a step already taken", got)
	}
	if !strings.Contains(got, "Uncomment") {
		t.Errorf("guidance for an existing file = %q, want it to say to uncomment a preset", got)
	}
	if !strings.Contains(got, existing) {
		t.Errorf("guidance = %q, want it to name the path", got)
	}
}
