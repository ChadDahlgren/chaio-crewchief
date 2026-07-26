package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/types"
)

func TestOpenTwiceNoError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s1.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("Open again: %v", err)
	}
	defer s2.Close()
}

func TestRecordAndGetAttempt(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	if err := s.RecordRequest(ctx, "req1", types.DelegateRequest{Task: "do the thing", Model: "glm"}, types.StatusRunning); err != nil {
		t.Fatalf("RecordRequest: %v", err)
	}

	a := types.Attempt{
		ID: "att1", RequestID: "req1", Model: "glm",
		StartedAt: time.Now().UTC().Truncate(time.Second), WallMS: 1234,
		PromptTokens: 10, OutputTokens: 20, TokPerSec: 15.5,
		Outcome: types.OutcomeDelivered, PromptRef: "pref", ResponseRef: "rref", ArtifactRef: "aref",
	}
	if err := s.RecordAttempt(ctx, a); err != nil {
		t.Fatalf("RecordAttempt: %v", err)
	}

	got, ok, err := s.GetAttempt(ctx, "att1")
	if err != nil {
		t.Fatalf("GetAttempt: %v", err)
	}
	if !ok {
		t.Fatal("expected found")
	}
	if got.Model != "glm" || got.WallMS != 1234 || got.TokPerSec != 15.5 || got.Outcome != types.OutcomeDelivered {
		t.Fatalf("got = %+v", got)
	}
	if got.ArtifactRef != "aref" {
		t.Fatalf("ArtifactRef = %q", got.ArtifactRef)
	}
}

func TestGetAttemptMissing(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, "test.db"))
	defer s.Close()
	_, ok, err := s.GetAttempt(context.Background(), "nope")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if ok {
		t.Fatal("expected not found")
	}
}

func TestQueryAttemptsFilters(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, "test.db"))
	defer s.Close()
	ctx := context.Background()

	s.RecordRequest(ctx, "r1", types.DelegateRequest{Task: "build a widget"}, types.StatusDelivered)
	s.RecordRequest(ctx, "r2", types.DelegateRequest{Task: "fix a bug"}, types.StatusDelivered)
	s.RecordAttempt(ctx, types.Attempt{ID: "a1", RequestID: "r1", Model: "glm", StartedAt: time.Now(), Outcome: types.OutcomeDelivered})
	s.RecordAttempt(ctx, types.Attempt{ID: "a2", RequestID: "r2", Model: "other", StartedAt: time.Now(), Outcome: types.OutcomeFailed})

	byModel, err := s.QueryAttempts(ctx, types.AttemptFilter{Model: "glm"})
	if err != nil {
		t.Fatalf("QueryAttempts: %v", err)
	}
	if len(byModel) != 1 || byModel[0].ID != "a1" {
		t.Fatalf("byModel = %+v", byModel)
	}

	bySearch, err := s.QueryAttempts(ctx, types.AttemptFilter{Search: "widget"})
	if err != nil {
		t.Fatalf("QueryAttempts search: %v", err)
	}
	if len(bySearch) != 1 || bySearch[0].ID != "a1" {
		t.Fatalf("bySearch = %+v", bySearch)
	}
}

func TestStatsGrouping(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, "test.db"))
	defer s.Close()
	ctx := context.Background()

	s.RecordRequest(ctx, "r1", types.DelegateRequest{Task: "t"}, types.StatusDelivered)
	s.RecordAttempt(ctx, types.Attempt{ID: "a1", RequestID: "r1", Model: "glm", StartedAt: time.Now(), WallMS: 100, OutputTokens: 10, Outcome: types.OutcomeDelivered})
	s.RecordAttempt(ctx, types.Attempt{ID: "a2", RequestID: "r1", Model: "glm", StartedAt: time.Now(), WallMS: 200, OutputTokens: 20, Outcome: types.OutcomeDelivered})

	rows, err := s.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	var found bool
	for _, r := range rows {
		if r.Model == "glm" && r.Outcome == types.OutcomeDelivered {
			found = true
			if r.Count != 2 {
				t.Fatalf("Count = %d, want 2", r.Count)
			}
			if r.AvgWallMS != 150 {
				t.Fatalf("AvgWallMS = %v, want 150", r.AvgWallMS)
			}
		}
	}
	if !found {
		t.Fatal("expected grouped stat row not found")
	}
}

func TestAttemptCostRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	s.RecordRequest(ctx, "req1", types.DelegateRequest{Task: "x", Model: "bedrock-qwen"}, types.StatusRunning)
	a := types.Attempt{
		ID: "att1", RequestID: "req1", Model: "bedrock-qwen",
		StartedAt: time.Now().UTC().Truncate(time.Second), WallMS: 100,
		PromptTokens: 10, OutputTokens: 20, Outcome: types.OutcomeDelivered,
		ProviderClass: "cloud", CostUSD: 1.25,
	}
	if err := s.RecordAttempt(ctx, a); err != nil {
		t.Fatalf("RecordAttempt: %v", err)
	}

	got, ok, err := s.GetAttempt(ctx, "att1")
	if err != nil {
		t.Fatalf("GetAttempt: %v", err)
	}
	if !ok {
		t.Fatal("expected found")
	}
	if got.ProviderClass != "cloud" || got.CostUSD != 1.25 {
		t.Fatalf("got = %+v", got)
	}
}

func TestAttemptProviderClassDefaultsToLocal(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, "test.db"))
	defer s.Close()
	ctx := context.Background()

	s.RecordRequest(ctx, "req1", types.DelegateRequest{Task: "x"}, types.StatusRunning)
	a := types.Attempt{
		ID: "att1", RequestID: "req1", Model: "glm",
		StartedAt: time.Now().UTC().Truncate(time.Second), Outcome: types.OutcomeDelivered,
	}
	if err := s.RecordAttempt(ctx, a); err != nil {
		t.Fatalf("RecordAttempt: %v", err)
	}
	got, _, err := s.GetAttempt(ctx, "att1")
	if err != nil {
		t.Fatalf("GetAttempt: %v", err)
	}
	if got.ProviderClass != "local" {
		t.Fatalf("ProviderClass = %q, want local", got.ProviderClass)
	}
	if got.CostUSD != 0 {
		t.Fatalf("CostUSD = %v, want 0", got.CostUSD)
	}
}

func TestStatsTotals(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, "test.db"))
	defer s.Close()
	ctx := context.Background()

	s.RecordRequest(ctx, "r1", types.DelegateRequest{Task: "t"}, types.StatusDelivered)
	s.RecordAttempt(ctx, types.Attempt{
		ID: "a1", RequestID: "r1", Model: "glm", StartedAt: time.Now(),
		PromptTokens: 100, OutputTokens: 50, Outcome: types.OutcomeDelivered, CostUSD: 0,
	})
	s.RecordAttempt(ctx, types.Attempt{
		ID: "a2", RequestID: "r1", Model: "bedrock", StartedAt: time.Now(),
		PromptTokens: 200, OutputTokens: 100, Outcome: types.OutcomeDelivered, CostUSD: 0.75,
	})

	totals, err := s.StatsTotals(ctx)
	if err != nil {
		t.Fatalf("StatsTotals: %v", err)
	}
	if totals.Attempts != 2 {
		t.Fatalf("Attempts = %d, want 2", totals.Attempts)
	}
	if totals.PromptTokens != 300 {
		t.Fatalf("PromptTokens = %d, want 300", totals.PromptTokens)
	}
	if totals.OutputTokens != 150 {
		t.Fatalf("OutputTokens = %d, want 150", totals.OutputTokens)
	}
	if totals.CostUSD != 0.75 {
		t.Fatalf("CostUSD = %v, want 0.75", totals.CostUSD)
	}
}

func TestStatsTotalsEmptyStore(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, "test.db"))
	defer s.Close()

	totals, err := s.StatsTotals(context.Background())
	if err != nil {
		t.Fatalf("StatsTotals: %v", err)
	}
	if totals.Attempts != 0 || totals.CostUSD != 0 {
		t.Fatalf("totals = %+v, want zero", totals)
	}
}

func TestRequestResultRoundtrip(t *testing.T) {
	s, err := Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	req := types.DelegateRequest{Task: "do thing", Async: true}
	if err := s.RecordRequest(ctx, "req-1", req, types.StatusRunning); err != nil {
		t.Fatalf("RecordRequest: %v", err)
	}

	rec, found, err := s.GetRequest(ctx, "req-1")
	if err != nil || !found {
		t.Fatalf("GetRequest running: found=%v err=%v", found, err)
	}
	if rec.Status != types.StatusRunning || rec.Artifact != "" {
		t.Fatalf("unexpected running record: %+v", rec)
	}

	if err := s.UpdateRequestResult(ctx, "req-1", types.StatusDelivered, "def f(): pass\n", ""); err != nil {
		t.Fatalf("UpdateRequestResult: %v", err)
	}
	rec, found, _ = s.GetRequest(ctx, "req-1")
	if !found || rec.Status != types.StatusDelivered || rec.Artifact != "def f(): pass\n" {
		t.Fatalf("unexpected solved record: %+v", rec)
	}

	if _, found, _ := s.GetRequest(ctx, "nope"); found {
		t.Fatal("GetRequest should not find unknown id")
	}
}
