package mcpserve

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestGatewayURLDefault(t *testing.T) {
	t.Setenv("CHAIO_CREWCHIEF_URL", "")
	t.Setenv("DISPATCH_URL", "")
	if got := GatewayURL(); got != "http://localhost:8181" {
		t.Errorf("default = %q", got)
	}
	t.Setenv("CHAIO_CREWCHIEF_URL", "http://box:9999")
	if got := GatewayURL(); got != "http://box:9999" {
		t.Errorf("env override = %q", got)
	}
}

func TestGatewayURLFallsBackToDispatchURL(t *testing.T) {
	t.Setenv("CHAIO_CREWCHIEF_URL", "")
	t.Setenv("CREWCHIEF_URL", "")
	t.Setenv("DISPATCH_URL", "http://legacy:8181")
	if got := GatewayURL(); got != "http://legacy:8181" {
		t.Errorf("fallback = %q", got)
	}
}

// Configs written under the two earlier project names keep working, but the
// current name wins when more than one is set.
func TestGatewayURLPrecedence(t *testing.T) {
	t.Setenv("CHAIO_CREWCHIEF_URL", "")
	t.Setenv("CREWCHIEF_URL", "http://prev:2")
	t.Setenv("DISPATCH_URL", "http://legacy:8181")
	if got := GatewayURL(); got != "http://prev:2" {
		t.Errorf("got = %q, want CREWCHIEF_URL to beat DISPATCH_URL", got)
	}

	t.Setenv("CHAIO_CREWCHIEF_URL", "http://new:1")
	if got := GatewayURL(); got != "http://new:1" {
		t.Errorf("got = %q, want CHAIO_CREWCHIEF_URL to win", got)
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
