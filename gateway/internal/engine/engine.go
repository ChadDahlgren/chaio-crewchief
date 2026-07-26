// Package engine relays delegation requests to model providers. It is
// deliberately simple: it does not judge output quality, does not choose which
// model is "best" (routing.yaml is a data lookup, not a decision), and only
// retries mechanical failures — no response, a timeout, or a transport/API
// error. Whether an artifact is actually correct is the calling brain's job,
// not the engine's.
package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/rates"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/types"
)

var codeBlockRegex = regexp.MustCompile("(?s)```[a-zA-Z0-9_+-]*\\n(.*?)```")

// noopRates prices everything at 0, used when the caller has no rates table
// (e.g. existing tests, or a deployment with no rates.yaml).
type noopRates struct{}

func (noopRates) Price(model string, promptTokens, outputTokens int) float64 { return 0 }
func (noopRates) Counterfactual(promptTokens, outputTokens int) float64      { return 0 }

// noopRouter never resolves anything, so every request falls back to the
// registry default. Used when the deployment has no routing.yaml.
type noopRouter struct{}

func (noopRouter) Resolve(lang string) (string, bool) { return "", false }

// Engine relays one attempt per call, retrying only mechanical failures.
type Engine struct {
	reg    types.Registry
	router types.Router
	prov   types.Provider
	store  types.Store
	arch   types.Archiver
	rates  rates.Table
}

// New wires an Engine with no language routing (every request uses the
// registry default unless Model is set explicitly). rt may be nil.
func New(reg types.Registry, router types.Router, prov types.Provider, store types.Store, arch types.Archiver, rt rates.Table) *Engine {
	return NewWithRouter(reg, router, prov, store, arch, rt)
}

// NewWithRouter wires an Engine with a language→model lookup. router may be
// nil, in which case Lang hints are ignored and the registry default is used.
func NewWithRouter(reg types.Registry, router types.Router, prov types.Provider, store types.Store, arch types.Archiver, rt rates.Table) *Engine {
	if rt == nil {
		rt = noopRates{}
	}
	if router == nil {
		router = noopRouter{}
	}
	return &Engine{reg: reg, router: router, prov: prov, store: store, arch: arch, rates: rt}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func extractCodeBlock(content string) (string, bool) {
	matches := codeBlockRegex.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return "", false
	}
	best := matches[0][1]
	for _, m := range matches[1:] {
		if len(m[1]) > len(best) {
			best = m[1]
		}
	}
	return best, true
}

// resolvePreset picks the model: explicit Model wins, then a routing.yaml
// hit on Lang, then the registry default. Plain data lookups only — no
// inference happens here.
func (e *Engine) resolvePreset(req types.DelegateRequest) (types.Preset, error) {
	if req.Model != "" {
		p, ok := e.reg.Get(req.Model)
		if !ok {
			return types.Preset{}, fmt.Errorf("unknown model: %s", req.Model)
		}
		return p, nil
	}
	if req.Lang != "" {
		if name, ok := e.router.Resolve(req.Lang); ok {
			if p, ok := e.reg.Get(name); ok {
				return p, nil
			}
		}
	}
	p, ok := e.reg.Default()
	if !ok {
		return types.Preset{}, fmt.Errorf("no default model configured")
	}
	return p, nil
}

// NewRequestID mints the ID for a delegation before it runs, so async
// callers can be handed the ID immediately and poll GET /requests/{id}.
func (e *Engine) NewRequestID() string { return newID() }

func (e *Engine) Run(ctx context.Context, req types.DelegateRequest) (types.DelegateResult, error) {
	return e.RunWithID(ctx, newID(), req)
}

func (e *Engine) RunWithID(ctx context.Context, reqID string, req types.DelegateRequest) (types.DelegateResult, error) {
	preset, err := e.resolvePreset(req)
	if err != nil {
		return types.DelegateResult{}, err
	}
	if req.Temperature != nil {
		preset.Temperature = *req.Temperature
	}

	retries := types.DefaultRetries
	if req.Retries != nil {
		retries = *req.Retries
	}
	if retries < 0 {
		retries = 0
	}

	// A failed ledger write aborts the delegation instead of being swallowed.
	//
	// This is the one write that must not be best-effort. Without its row: the
	// delegation still bills real tokens against a model, every later
	// UpdateRequestResult silently no-ops against a row that does not exist, an
	// async caller polling GET /requests/{id} gets 404 forever with no way to
	// tell "never recorded" from "bad id", and the ledger under-reports spend —
	// which is the single number this project exists to produce. Reporting a
	// smaller total than was actually spent is worse than refusing the work,
	// because nobody goes looking for money that was never shown to them.
	//
	// Refusing before the provider call is also the cheap direction: it costs a
	// retry, not tokens. The realistic causes are a locked or unwritable
	// ledger, and Open's retry plus WAL already absorb the transient ones, so
	// what reaches here is a real problem worth surfacing.
	if err := e.store.RecordRequest(ctx, reqID, req, types.StatusRunning); err != nil {
		return types.DelegateResult{}, fmt.Errorf("record request in ledger: %w", err)
	}

	var attempts []types.Attempt
	var lastErr string

	for i := 0; i <= retries; i++ {
		a, artifact, delivered, callErr := e.attempt(ctx, reqID, preset, req)
		attempts = append(attempts, a)
		_ = e.store.RecordAttempt(ctx, a)

		if delivered {
			_ = e.store.UpdateRequestResult(ctx, reqID, types.StatusDelivered, artifact, "")
			return types.DelegateResult{RequestID: reqID, Status: types.StatusDelivered, Artifact: artifact, Attempts: attempts}, nil
		}
		if callErr != "" {
			lastErr = callErr
		}
	}

	_ = e.store.UpdateRequestResult(ctx, reqID, types.StatusFailed, "", lastErr)
	return types.DelegateResult{RequestID: reqID, Status: types.StatusFailed, Attempts: attempts, Error: lastErr}, nil
}

// attempt makes one model call. delivered is true iff a non-empty response
// came back — that's the only judgment this function makes.
func (e *Engine) attempt(ctx context.Context, reqID string, preset types.Preset, req types.DelegateRequest) (types.Attempt, string, bool, string) {
	start := time.Now()
	completionReq := types.CompletionRequest{
		System:      preset.SystemPrompt,
		User:        req.Task,
		Temperature: preset.Temperature,
		MaxTokens:   preset.MaxTokens,
		Timeout:     time.Duration(preset.TimeoutSec) * time.Second,
	}

	promptBlob, _ := json.Marshal(map[string]string{"system": preset.SystemPrompt, "user": req.Task})
	promptRef, _ := e.arch.Put(ctx, promptBlob)

	resp, err := e.prov.Complete(ctx, preset, completionReq)
	wallMS := time.Since(start).Milliseconds()

	a := types.Attempt{
		ID:            newID(),
		RequestID:     reqID,
		Model:         preset.Name,
		StartedAt:     start,
		WallMS:        wallMS,
		PromptRef:     promptRef,
		ProviderClass: preset.ProviderClass,
	}

	if err != nil {
		a.Outcome = types.OutcomeFailed
		a.Error = err.Error()
		return a, "", false, err.Error()
	}

	a.PromptTokens = resp.PromptTokens
	a.OutputTokens = resp.OutputTokens
	a.TokPerSec = resp.TokPerSec
	a.CostUSD = e.rates.Price(preset.Name, resp.PromptTokens, resp.OutputTokens)
	if respRef, rerr := e.arch.Put(ctx, resp.Raw); rerr == nil {
		a.ResponseRef = respRef
	}

	var artifact string
	if req.Raw {
		artifact = resp.Content
	} else if extracted, found := extractCodeBlock(resp.Content); found {
		artifact = extracted
	} else {
		artifact = resp.Content // no fence: still whatever the model said, unjudged
	}

	if artifact == "" {
		a.Outcome = types.OutcomeFailed
		a.Error = "empty response"
		return a, "", false, "empty response"
	}

	if artRef, aerr := e.arch.Put(ctx, []byte(artifact)); aerr == nil {
		a.ArtifactRef = artRef
	}
	a.Outcome = types.OutcomeDelivered
	return a, artifact, true, ""
}
