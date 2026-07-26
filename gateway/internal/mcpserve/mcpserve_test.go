package mcpserve

import (
	"context"
	"net/http"
	"net/http/httptest"
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
