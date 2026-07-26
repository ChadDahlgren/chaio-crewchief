package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/types"
)

type fakeEngine struct {
	result types.DelegateResult
	err    error
	block  chan struct{} // when non-nil, RunWithID waits on it before finishing
	nextID string
	store  *fakeStore // when set, RunWithID records its terminal result like the real engine
}

func (f *fakeEngine) Run(ctx context.Context, req types.DelegateRequest) (types.DelegateResult, error) {
	return f.result, f.err
}

func (f *fakeEngine) NewRequestID() string {
	if f.nextID != "" {
		return f.nextID
	}
	return "fake-id"
}

func (f *fakeEngine) RunWithID(ctx context.Context, reqID string, req types.DelegateRequest) (types.DelegateResult, error) {
	if f.block != nil {
		<-f.block
	}
	if f.store != nil {
		_ = f.store.UpdateRequestResult(ctx, reqID, f.result.Status, f.result.Artifact, f.result.Error)
	}
	return f.result, f.err
}

type fakeStore struct {
	attempt      types.Attempt
	attemptFound bool
	lastFilter   types.AttemptFilter
	mu           sync.Mutex
	requests     map[string]types.RequestRecord
}

func (s *fakeStore) RecordRequest(ctx context.Context, id string, req types.DelegateRequest, status types.DelegateStatus) error {
	return nil
}
func (s *fakeStore) UpdateRequestStatus(ctx context.Context, id string, status types.DelegateStatus) error {
	return nil
}
func (s *fakeStore) UpdateRequestResult(ctx context.Context, id string, status types.DelegateStatus, artifact, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requests == nil {
		s.requests = map[string]types.RequestRecord{}
	}
	s.requests[id] = types.RequestRecord{ID: id, Status: status, Artifact: artifact, Error: errMsg}
	return nil
}
func (s *fakeStore) GetRequest(ctx context.Context, id string) (types.RequestRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.requests[id]
	return rec, ok, nil
}
func (s *fakeStore) RecordAttempt(ctx context.Context, a types.Attempt) error { return nil }
func (s *fakeStore) GetAttempt(ctx context.Context, id string) (types.Attempt, bool, error) {
	return s.attempt, s.attemptFound, nil
}
func (s *fakeStore) QueryAttempts(ctx context.Context, f types.AttemptFilter) ([]types.Attempt, error) {
	s.lastFilter = f
	return []types.Attempt{}, nil
}
func (s *fakeStore) Stats(ctx context.Context) ([]types.StatRow, error) { return nil, nil }
func (s *fakeStore) StatsTotals(ctx context.Context) (types.StatsTotals, error) {
	return types.StatsTotals{}, nil
}
func (s *fakeStore) Close() error { return nil }

type fakeRegistry struct{ presets []types.Preset }

func (r *fakeRegistry) Get(name string) (types.Preset, bool) {
	for _, p := range r.presets {
		if p.Name == name {
			return p, true
		}
	}
	return types.Preset{}, false
}
func (r *fakeRegistry) Default() (types.Preset, bool) {
	if len(r.presets) > 0 {
		return r.presets[0], true
	}
	return types.Preset{}, false
}
func (r *fakeRegistry) List() []types.Preset { return r.presets }

type fakeArchiver struct{}

func (a *fakeArchiver) Put(ctx context.Context, blob []byte) (string, error) { return "ref", nil }
func (a *fakeArchiver) Get(ctx context.Context, ref string) ([]byte, error)  { return nil, nil }

func TestDelegateSolved(t *testing.T) {
	eng := &fakeEngine{result: types.DelegateResult{RequestID: "r1", Status: types.StatusDelivered}}
	h := New(eng, &fakeStore{}, &fakeRegistry{}, &fakeArchiver{}, nil)

	body := strings.NewReader(`{"task":"do it"}`)
	req := httptest.NewRequest("POST", "/delegate", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"delivered"`) {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestDelegateMalformedJSON(t *testing.T) {
	eng := &fakeEngine{}
	h := New(eng, &fakeStore{}, &fakeRegistry{}, &fakeArchiver{}, nil)

	req := httptest.NewRequest("POST", "/delegate", strings.NewReader(`{bad json`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body.String())
	}
	var m map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("body not JSON: %v", w.Body.String())
	}
	if _, ok := m["error"]; !ok {
		t.Fatalf("missing error key: %v", m)
	}
}

func TestAttemptNotFound(t *testing.T) {
	h := New(&fakeEngine{}, &fakeStore{attemptFound: false}, &fakeRegistry{}, &fakeArchiver{}, nil)
	req := httptest.NewRequest("GET", "/attempts/missing", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHistoryFilterParsing(t *testing.T) {
	st := &fakeStore{}
	h := New(&fakeEngine{}, st, &fakeRegistry{}, &fakeArchiver{}, nil)
	req := httptest.NewRequest("GET", "/history?model=glm&limit=5", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if st.lastFilter.Model != "glm" || st.lastFilter.Limit != 5 {
		t.Fatalf("filter = %+v", st.lastFilter)
	}
}

func TestHealthAlwaysOK(t *testing.T) {
	reg := &fakeRegistry{presets: []types.Preset{{Name: "dead", BaseURL: "http://127.0.0.1:1"}}}
	h := New(&fakeEngine{}, &fakeStore{}, reg, &fakeArchiver{}, nil)
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	// bound the test in case of slow probes
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(w, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("health handler took too long")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestAsyncDelegateLifecycle(t *testing.T) {
	release := make(chan struct{})
	st := &fakeStore{requests: map[string]types.RequestRecord{}}
	eng := &fakeEngine{result: types.DelegateResult{RequestID: "async-1", Status: types.StatusDelivered, Artifact: "CODE"}, block: release, nextID: "async-1", store: st}
	h := New(eng, st, &fakeRegistry{}, &fakeArchiver{}, nil)

	// async POST returns 202 + request_id immediately
	body := strings.NewReader(`{"task":"t","async":true}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/delegate", body))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d", rec.Code)
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["request_id"] != "async-1" || resp["status"] != "running" {
		t.Fatalf("resp = %v", resp)
	}

	// while running: GET /requests/{id} says running
	st.mu.Lock()
	st.requests["async-1"] = types.RequestRecord{ID: "async-1", Status: types.StatusRunning}
	st.mu.Unlock()
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/requests/async-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get running code = %d", rec.Code)
	}
	var rr types.RequestRecord
	json.Unmarshal(rec.Body.Bytes(), &rr)
	if rr.Status != types.StatusRunning {
		t.Fatalf("rr = %+v", rr)
	}

	// let the engine finish; it records the result
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		st.mu.Lock()
		r := st.requests["async-1"]
		st.mu.Unlock()
		if r.Status == types.StatusDelivered || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/requests/async-1", nil))
	json.Unmarshal(rec.Body.Bytes(), &rr)
	if rr.Status != types.StatusDelivered || rr.Artifact != "CODE" {
		t.Fatalf("final rr = %+v", rr)
	}

	// unknown id -> 404
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/requests/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id code = %d", rec.Code)
	}
}
