package engine

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/types"
)

// ---- fakes ----

type fakeRegistry struct {
	presets map[string]types.Preset
	def     string
}

func (r *fakeRegistry) Get(name string) (types.Preset, bool) { p, ok := r.presets[name]; return p, ok }
func (r *fakeRegistry) Default() (types.Preset, bool) {
	if r.def == "" {
		return types.Preset{}, false
	}
	p, ok := r.presets[r.def]
	return p, ok
}
func (r *fakeRegistry) List() []types.Preset {
	var out []types.Preset
	for _, p := range r.presets {
		out = append(out, p)
	}
	return out
}

func mkRegistry() *fakeRegistry {
	return &fakeRegistry{
		presets: map[string]types.Preset{
			"glm": {Name: "glm", BaseURL: "http://x", Suffix: " /nothink", Temperature: 0.3, MaxTokens: 100, TimeoutSec: 5, Default: true},
		},
		def: "glm",
	}
}

type fakeRouter struct{ table map[string]string }

func (r *fakeRouter) Resolve(lang string) (string, bool) { m, ok := r.table[lang]; return m, ok }

// scriptedProvider replays one outcome per call: a content string, or an
// error (mechanical failure — the only kind the engine reacts to).
type scriptedProvider struct {
	mu           sync.Mutex
	i            int
	contents     []string
	errs         []error
	lastUser     []string
	temperatures []float64
}

func (p *scriptedProvider) Complete(ctx context.Context, base types.Preset, req types.CompletionRequest) (types.CompletionResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastUser = append(p.lastUser, req.User)
	p.temperatures = append(p.temperatures, req.Temperature)
	idx := p.i
	p.i++
	if idx < len(p.errs) && p.errs[idx] != nil {
		return types.CompletionResponse{}, p.errs[idx]
	}
	c := ""
	if idx < len(p.contents) {
		c = p.contents[idx]
	}
	return types.CompletionResponse{Content: c, OutputTokens: 5, PromptTokens: 5, TokPerSec: 1}, nil
}

type memStore struct {
	mu       sync.Mutex
	attempts []types.Attempt
	requests map[string]types.RequestRecord
	// recordErr, when set, makes RecordRequest fail — a ledger that is locked,
	// unwritable, or gone.
	recordErr error
}

func newMemStore() *memStore { return &memStore{requests: map[string]types.RequestRecord{}} }

func (s *memStore) RecordRequest(ctx context.Context, id string, req types.DelegateRequest, status types.DelegateStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recordErr != nil {
		return s.recordErr
	}
	s.requests[id] = types.RequestRecord{ID: id, Status: status}
	return nil
}
func (s *memStore) UpdateRequestStatus(ctx context.Context, id string, status types.DelegateStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.requests[id]
	r.Status = status
	s.requests[id] = r
	return nil
}
func (s *memStore) UpdateRequestResult(ctx context.Context, id string, status types.DelegateStatus, artifact, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[id] = types.RequestRecord{ID: id, Status: status, Artifact: artifact, Error: errMsg}
	return nil
}
func (s *memStore) GetRequest(ctx context.Context, id string) (types.RequestRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.requests[id]
	return r, ok, nil
}
func (s *memStore) RecordAttempt(ctx context.Context, a types.Attempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, a)
	return nil
}
func (s *memStore) GetAttempt(ctx context.Context, id string) (types.Attempt, bool, error) {
	return types.Attempt{}, false, nil
}
func (s *memStore) QueryAttempts(ctx context.Context, f types.AttemptFilter) ([]types.Attempt, error) {
	return nil, nil
}
func (s *memStore) Stats(ctx context.Context) ([]types.StatRow, error) { return nil, nil }
func (s *memStore) StatsTotals(ctx context.Context) (types.StatsTotals, error) {
	return types.StatsTotals{}, nil
}
func (s *memStore) Close() error { return nil }

type memArchiver struct{}

func (memArchiver) Put(ctx context.Context, blob []byte) (string, error) { return "ref", nil }
func (memArchiver) Get(ctx context.Context, ref string) ([]byte, error)  { return nil, nil }

// ---- tests ----

func TestDeliveredOnFirstResponse(t *testing.T) {
	prov := &scriptedProvider{contents: []string{"```python\nprint(1)\n```"}}
	e := New(mkRegistry(), nil, prov, newMemStore(), memArchiver{}, nil)
	res, err := e.Run(context.Background(), types.DelegateRequest{Task: "do it"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status != types.StatusDelivered {
		t.Fatalf("status = %s", res.Status)
	}
	if res.Artifact != "print(1)\n" {
		t.Fatalf("artifact = %q", res.Artifact)
	}
	if len(res.Attempts) != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry on success)", len(res.Attempts))
	}
	if res.Attempts[0].Outcome != types.OutcomeDelivered {
		t.Fatalf("outcome = %s", res.Attempts[0].Outcome)
	}
}

func TestRawSkipsCodeFenceExtraction(t *testing.T) {
	prov := &scriptedProvider{contents: []string{`{"is_financial": true}`}}
	e := New(mkRegistry(), nil, prov, newMemStore(), memArchiver{}, nil)
	res, _ := e.Run(context.Background(), types.DelegateRequest{Task: "classify", Raw: true})
	if res.Artifact != `{"is_financial": true}` {
		t.Fatalf("artifact = %q", res.Artifact)
	}
}

func TestNoCodeFenceIsStillDelivered(t *testing.T) {
	// Crew Chief does not require a fenced block to call something delivered —
	// only whether a response came back at all. Judging content is the brain's job.
	prov := &scriptedProvider{contents: []string{"just plain prose, no fence"}}
	e := New(mkRegistry(), nil, prov, newMemStore(), memArchiver{}, nil)
	res, _ := e.Run(context.Background(), types.DelegateRequest{Task: "do it"})
	if res.Status != types.StatusDelivered {
		t.Fatalf("status = %s", res.Status)
	}
	if res.Artifact != "just plain prose, no fence" {
		t.Fatalf("artifact = %q", res.Artifact)
	}
}

func TestMechanicalFailureRetriesThenDelivers(t *testing.T) {
	prov := &scriptedProvider{
		errs:     []error{errors.New("connection reset"), nil},
		contents: []string{"", "```python\nok\n```"},
	}
	e := New(mkRegistry(), nil, prov, newMemStore(), memArchiver{}, nil)
	res, _ := e.Run(context.Background(), types.DelegateRequest{Task: "do it"})
	if res.Status != types.StatusDelivered {
		t.Fatalf("status = %s", res.Status)
	}
	if len(res.Attempts) != 2 {
		t.Fatalf("attempts = %d, want 2 (1 fail + 1 delivered)", len(res.Attempts))
	}
	if res.Attempts[0].Outcome != types.OutcomeFailed || res.Attempts[1].Outcome != types.OutcomeDelivered {
		t.Fatalf("outcomes = %s, %s", res.Attempts[0].Outcome, res.Attempts[1].Outcome)
	}
}

func TestExhaustsRetriesThenFails(t *testing.T) {
	prov := &scriptedProvider{errs: []error{errors.New("e1"), errors.New("e2"), errors.New("e3")}}
	retries := 2
	e := New(mkRegistry(), nil, prov, newMemStore(), memArchiver{}, nil)
	res, _ := e.Run(context.Background(), types.DelegateRequest{Task: "do it", Retries: &retries})
	if res.Status != types.StatusFailed {
		t.Fatalf("status = %s", res.Status)
	}
	if len(res.Attempts) != 3 { // 1 initial + 2 retries
		t.Fatalf("attempts = %d, want 3", len(res.Attempts))
	}
	if res.Error == "" {
		t.Fatal("expected Error to carry the last failure detail")
	}
}

func TestZeroRetriesIsSingleShot(t *testing.T) {
	prov := &scriptedProvider{errs: []error{errors.New("boom")}}
	zero := 0
	e := New(mkRegistry(), nil, prov, newMemStore(), memArchiver{}, nil)
	res, _ := e.Run(context.Background(), types.DelegateRequest{Task: "do it", Retries: &zero})
	if res.Status != types.StatusFailed {
		t.Fatalf("status = %s", res.Status)
	}
	if len(res.Attempts) != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry when Retries=0)", len(res.Attempts))
	}
}

func TestDefaultRetriesIsTwo(t *testing.T) {
	prov := &scriptedProvider{errs: []error{errors.New("e1"), errors.New("e2"), errors.New("e3")}}
	e := New(mkRegistry(), nil, prov, newMemStore(), memArchiver{}, nil)
	res, _ := e.Run(context.Background(), types.DelegateRequest{Task: "do it"}) // Retries nil -> default
	if len(res.Attempts) != types.DefaultRetries+1 {
		t.Fatalf("attempts = %d, want %d", len(res.Attempts), types.DefaultRetries+1)
	}
	if res.Status != types.StatusFailed {
		t.Fatalf("status = %s", res.Status)
	}
}

func TestEmptyResponseIsMechanicalFailure(t *testing.T) {
	prov := &scriptedProvider{contents: []string{""}}
	zero := 0
	e := New(mkRegistry(), nil, prov, newMemStore(), memArchiver{}, nil)
	res, _ := e.Run(context.Background(), types.DelegateRequest{Task: "do it", Retries: &zero})
	if res.Status != types.StatusFailed {
		t.Fatalf("empty response should be a mechanical failure, got status = %s", res.Status)
	}
}

func TestRetryDoesNotRewritePrompt(t *testing.T) {
	// The engine's retry is purely mechanical: same task, no error-feedback
	// prompt engineering (that was the old TDD ladder — removed).
	prov := &scriptedProvider{
		errs:     []error{errors.New("e1"), nil},
		contents: []string{"", "```python\nok\n```"},
	}
	e := New(mkRegistry(), nil, prov, newMemStore(), memArchiver{}, nil)
	_, _ = e.Run(context.Background(), types.DelegateRequest{Task: "the exact task text"})
	if prov.lastUser[0] != "the exact task text" || prov.lastUser[1] != "the exact task text" {
		t.Fatalf("retry mutated the prompt: %v", prov.lastUser)
	}
}

func TestTemperatureOverrideReachesProvider(t *testing.T) {
	prov := &scriptedProvider{contents: []string{"ok"}}
	e := New(mkRegistry(), nil, prov, newMemStore(), memArchiver{}, nil)
	override := 0.9
	_, err := e.Run(context.Background(), types.DelegateRequest{Task: "x", Temperature: &override})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(prov.temperatures) != 1 || prov.temperatures[0] != override {
		t.Fatalf("temperatures = %v, want [%v]", prov.temperatures, override)
	}
}

func TestUnknownModelErrors(t *testing.T) {
	e := New(mkRegistry(), nil, &scriptedProvider{}, newMemStore(), memArchiver{}, nil)
	_, err := e.Run(context.Background(), types.DelegateRequest{Task: "x", Model: "nope"})
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
}

func TestLangResolvesViaRouter(t *testing.T) {
	reg := &fakeRegistry{presets: map[string]types.Preset{
		"glm":  {Name: "glm", BaseURL: "http://x", TimeoutSec: 5, Default: true},
		"ts-m": {Name: "ts-m", BaseURL: "http://y", TimeoutSec: 5},
	}, def: "glm"}
	router := &fakeRouter{table: map[string]string{"typescript": "ts-m"}}
	prov := &scriptedProvider{contents: []string{"ok"}}
	e := NewWithRouter(reg, router, prov, newMemStore(), memArchiver{}, nil)
	res, err := e.Run(context.Background(), types.DelegateRequest{Task: "x", Lang: "typescript"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Attempts[0].Model != "ts-m" {
		t.Fatalf("model = %s, want ts-m (routed)", res.Attempts[0].Model)
	}
}

func TestExplicitModelOverridesRouter(t *testing.T) {
	reg := &fakeRegistry{presets: map[string]types.Preset{
		"glm":  {Name: "glm", BaseURL: "http://x", TimeoutSec: 5, Default: true},
		"ts-m": {Name: "ts-m", BaseURL: "http://y", TimeoutSec: 5},
	}, def: "glm"}
	router := &fakeRouter{table: map[string]string{"typescript": "ts-m"}}
	prov := &scriptedProvider{contents: []string{"ok"}}
	e := NewWithRouter(reg, router, prov, newMemStore(), memArchiver{}, nil)
	res, _ := e.Run(context.Background(), types.DelegateRequest{Task: "x", Lang: "typescript", Model: "glm"})
	if res.Attempts[0].Model != "glm" {
		t.Fatalf("model = %s, want glm (explicit wins)", res.Attempts[0].Model)
	}
}

func TestLangWithNoRouteFallsBackToDefault(t *testing.T) {
	router := &fakeRouter{table: map[string]string{}}
	prov := &scriptedProvider{contents: []string{"ok"}}
	e := NewWithRouter(mkRegistry(), router, prov, newMemStore(), memArchiver{}, nil)
	res, _ := e.Run(context.Background(), types.DelegateRequest{Task: "x", Lang: "cobol"})
	if res.Attempts[0].Model != "glm" {
		t.Fatalf("model = %s, want glm (default fallback)", res.Attempts[0].Model)
	}
}

func TestAsyncResultPersistsAndPolls(t *testing.T) {
	prov := &scriptedProvider{contents: []string{"```python\nok\n```"}}
	store := newMemStore()
	e := New(mkRegistry(), nil, prov, store, memArchiver{}, nil)
	id := e.NewRequestID()
	res, err := e.RunWithID(context.Background(), id, types.DelegateRequest{Task: "x"})
	if err != nil {
		t.Fatalf("RunWithID: %v", err)
	}
	rec, found, _ := store.GetRequest(context.Background(), id)
	if !found || rec.Status != types.StatusDelivered || rec.Artifact != res.Artifact {
		t.Fatalf("persisted record = %+v", rec)
	}
}

// A ledger write that fails must abort the delegation, not proceed silently.
//
// This write used to be `_ = e.store.RecordRequest(...)`. Swallowing it meant
// the run still billed real tokens against a model, every later
// UpdateRequestResult no-opped against a row that did not exist, and the ledger
// under-reported spend — the one number this project exists to produce.
func TestRunAbortsWhenLedgerWriteFails(t *testing.T) {
	st := newMemStore()
	st.recordErr = errors.New("database is locked")
	prov := &scriptedProvider{contents: []string{"```python\nprint(1)\n```"}}
	e := New(mkRegistry(), nil, prov, st, memArchiver{}, nil)

	_, err := e.Run(context.Background(), types.DelegateRequest{Task: "do it"})
	if err == nil {
		t.Fatal("Run() error = nil, want the ledger failure surfaced")
	}
	if !strings.Contains(err.Error(), "database is locked") {
		t.Errorf("Run() error = %v, want it to name the underlying cause", err)
	}

	// The point of aborting is not spending money we cannot account for.
	if len(prov.lastUser) != 0 {
		t.Errorf("provider called %d times after a failed ledger write, want 0", len(prov.lastUser))
	}
	if len(st.attempts) != 0 {
		t.Errorf("recorded %d attempts against an unrecorded request, want 0", len(st.attempts))
	}
}
