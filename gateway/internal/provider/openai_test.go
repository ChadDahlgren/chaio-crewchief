package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/types"
)

func TestCompleteBasic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"content": "hello", "reasoning_content": "because"}},
			},
			"usage": map[string]interface{}{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	defer srv.Close()

	o := New()
	resp, err := o.Complete(context.Background(), types.Preset{BaseURL: srv.URL}, types.CompletionRequest{
		System: "sys", User: "usr", Temperature: 0.3, MaxTokens: 100, Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hello" {
		t.Fatalf("Content = %q", resp.Content)
	}
	if resp.Reasoning != "because" {
		t.Fatalf("Reasoning = %q", resp.Reasoning)
	}
	if resp.PromptTokens != 10 || resp.OutputTokens != 5 {
		t.Fatalf("tokens = %d/%d", resp.PromptTokens, resp.OutputTokens)
	}
	if len(resp.Raw) == 0 {
		t.Fatal("Raw empty")
	}
}

func TestBaseURLNormalization(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/chat/completions" {
			hits++
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{{"message": map[string]interface{}{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	o := New()
	for _, base := range []string{srv.URL, srv.URL + "/v1"} {
		_, err := o.Complete(context.Background(), types.Preset{BaseURL: base}, types.CompletionRequest{Timeout: 5 * time.Second})
		if err != nil {
			t.Fatalf("Complete(%s): %v", base, err)
		}
	}
	if hits != 2 {
		t.Fatalf("expected 2 hits to /v1/chat/completions, got %d", hits)
	}
}

func TestNon200IncludesDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"boom detail"}`))
	}))
	defer srv.Close()

	o := New()
	_, err := o.Complete(context.Background(), types.Preset{BaseURL: srv.URL}, types.CompletionRequest{Timeout: 5 * time.Second})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "boom detail") {
		t.Fatalf("error missing detail: %v", err)
	}
}

func TestTimingsOptional(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{{"message": map[string]interface{}{"content": "x"}}},
			"timings": map[string]interface{}{"predicted_per_second": 42.5},
		})
	}))
	defer srv.Close()

	o := New()
	resp, err := o.Complete(context.Background(), types.Preset{BaseURL: srv.URL}, types.CompletionRequest{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.TokPerSec != 42.5 {
		t.Fatalf("TokPerSec = %v, want 42.5", resp.TokPerSec)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{{"message": map[string]interface{}{"content": "x"}}},
		})
	}))
	defer srv2.Close()
	resp2, err := o.Complete(context.Background(), types.Preset{BaseURL: srv2.URL}, types.CompletionRequest{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Complete no timings: %v", err)
	}
	if resp2.TokPerSec != 0 {
		t.Fatalf("TokPerSec = %v, want 0", resp2.TokPerSec)
	}
}

func TestTimeoutExceeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{{"message": map[string]interface{}{"content": "x"}}},
		})
	}))
	defer srv.Close()

	o := New()
	_, err := o.Complete(context.Background(), types.Preset{BaseURL: srv.URL}, types.CompletionRequest{Timeout: 50 * time.Millisecond})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestAuthAndModelID(t *testing.T) {
	var gotAuth string
	var gotModel interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		gotModel = body["model"]
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{{"message": map[string]interface{}{"content": "ok"}}},
			"usage":   map[string]interface{}{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer srv.Close()

	t.Setenv("TEST_CF_TOKEN", "sekret")
	o := New()
	preset := types.Preset{Name: "cf-test", BaseURL: srv.URL, ModelID: "@cf/openai/gpt-oss-120b", APIKeyEnv: "TEST_CF_TOKEN"}
	if _, err := o.Complete(context.Background(), preset, types.CompletionRequest{Timeout: 5 * time.Second}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotAuth != "Bearer sekret" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotModel != "@cf/openai/gpt-oss-120b" {
		t.Fatalf("model = %v", gotModel)
	}
}

func TestAuthMissingEnv(t *testing.T) {
	t.Setenv("TEST_CF_TOKEN_MISSING", "")
	o := New()
	preset := types.Preset{Name: "cf-test", BaseURL: "http://localhost:1", APIKeyEnv: "TEST_CF_TOKEN_MISSING"}
	_, err := o.Complete(context.Background(), preset, types.CompletionRequest{Timeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "TEST_CF_TOKEN_MISSING") {
		t.Fatalf("expected missing-env error, got: %v", err)
	}
}

func TestNoModelFieldWhenEmpty(t *testing.T) {
	var hasModel bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		_, hasModel = body["model"]
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{{"message": map[string]interface{}{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	o := New()
	if _, err := o.Complete(context.Background(), types.Preset{BaseURL: srv.URL}, types.CompletionRequest{Timeout: 5 * time.Second}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if hasModel {
		t.Fatal("model field should be omitted when ModelID is empty")
	}
}

// A preset that omits timeout_sec produces CompletionRequest.Timeout == 0.
// That must complete normally, not panic on a nil context.CancelFunc.
func TestCompleteZeroTimeoutDoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]interface{}{"content": "ok"}},
			},
			"usage": map[string]interface{}{"prompt_tokens": 1, "completion_tokens": 1},
		})
	}))
	defer srv.Close()

	o := New()
	resp, err := o.Complete(context.Background(), types.Preset{BaseURL: srv.URL}, types.CompletionRequest{
		System: "sys", User: "usr", MaxTokens: 100, // Timeout deliberately unset
	})
	if err != nil {
		t.Fatalf("Complete with zero timeout: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("Content = %q", resp.Content)
	}
}
