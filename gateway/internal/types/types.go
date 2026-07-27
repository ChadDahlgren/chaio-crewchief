// Package types defines this service's frozen contracts.
// HAND-WRITTEN — do not let generators modify this file.
package types

import (
	"context"
	"time"
)

// ---- requests & results ----------------------------------------------------

// DelegateRequest is what callers POST to /delegate.
//
// Crew Chief does not judge output: it relays the task to a model and returns
// whatever came back. The calling brain decides whether that's good enough,
// and re-delegates itself if not. Crew Chief only retries mechanical failures
// (no response, timeout, network/API error) — never "the answer was wrong",
// because Crew Chief has no way to know that and shouldn't pretend to.
type DelegateRequest struct {
	Task        string   `json:"task"`              // the work, in plain language
	Model       string   `json:"model,omitempty"`   // registry name; empty = resolved via Lang, then registry default
	Lang        string   `json:"lang,omitempty"`    // hint for routing.yaml lookup when Model is empty, e.g. "typescript"
	Retries     *int     `json:"retries,omitempty"` // mechanical-failure retries; nil = default (2), 0 = no retry
	Async       bool     `json:"async,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"` // when non-nil, overrides the preset temperature
	Raw         bool     `json:"raw,omitempty"`         // when true, skip code-fence extraction; the artifact is the full model content verbatim
}

// DefaultRetries is used when DelegateRequest.Retries is nil.
const DefaultRetries = 2

// AttemptOutcome is a single attempt's mechanical result — never a quality
// judgment. Crew Chief cannot tell "wrong answer" from "right answer"; only the
// calling brain can. This only distinguishes "a response came back" from
// "the call itself failed."
type AttemptOutcome string

const (
	OutcomeDelivered AttemptOutcome = "delivered" // got a non-empty response
	OutcomeFailed    AttemptOutcome = "failed"    // no response: timeout, network/API error, empty body
)

// Attempt is one model call. One DelegateRequest produces 1..(1+Retries).
type Attempt struct {
	ID            string         `json:"id"` // ULID
	RequestID     string         `json:"request_id"`
	Model         string         `json:"model"`
	StartedAt     time.Time      `json:"started_at"`
	WallMS        int64          `json:"wall_ms"`
	PromptTokens  int            `json:"prompt_tokens"`
	OutputTokens  int            `json:"output_tokens"`
	TokPerSec     float64        `json:"tok_per_sec"`
	Outcome       AttemptOutcome `json:"outcome"`
	Error         string         `json:"error,omitempty"`        // truncated failure detail when Outcome is failed
	PromptRef     string         `json:"prompt_ref"`             // Archiver ref, full request body
	ResponseRef   string         `json:"response_ref"`           // Archiver ref, full response body
	ArtifactRef   string         `json:"artifact_ref,omitempty"` // Archiver ref, extracted/raw content
	ProviderClass string         `json:"provider_class"`         // local|cloud|frontier, copied from the preset
	CostUSD       float64        `json:"cost_usd"`               // priced via internal/rates; 0 for local/unknown models
}

type DelegateStatus string

const (
	StatusDelivered DelegateStatus = "delivered" // a response came back; caller judges it
	StatusFailed    DelegateStatus = "failed"    // every attempt was a mechanical failure
	// StatusRunning is recorded for every request, not just async ones:
	// engine.RunWithID writes it before the first provider call and overwrites
	// it on the way out. That is what justifies the orphan-reaping machinery —
	// the likeliest way a row is stranded in "running" is an ordinary
	// synchronous delegation interrupted by the user closing Claude Code, not
	// an async job. Do not read this as async-only and conclude reaping is dead
	// code in embedded mode.
	StatusRunning DelegateStatus = "running"
)

// DelegateResult is the terminal answer for a request.
type DelegateResult struct {
	RequestID string         `json:"request_id"`
	Status    DelegateStatus `json:"status"`
	Artifact  string         `json:"artifact,omitempty"` // the model's output, unjudged
	Attempts  []Attempt      `json:"attempts"`
	Error     string         `json:"error,omitempty"` // last failure detail, when Status is failed
}

// ---- registry ---------------------------------------------------------------

// Preset is one model's entry in models.yaml.
type Preset struct {
	Name            string  `yaml:"name" json:"name"`
	BaseURL         string  `yaml:"base_url" json:"base_url"`                 // OpenAI-compatible root; ${ENV} vars expanded at load
	ModelID         string  `yaml:"model_id" json:"model_id"`                 // "model" field sent upstream; empty for single-model servers (llama-server)
	APIKeyEnv       string  `yaml:"api_key_env" json:"api_key_env"`           // env var holding the Bearer token; empty = unauthenticated (local)
	HealthPath      string  `yaml:"health_path" json:"health_path"`           // GET path probed by /health; default "/health"
	OmitTemperature bool    `yaml:"omit_temperature" json:"omit_temperature"` // don't send temperature (Claude 4.6+ rejects it)
	SystemPrompt    string  `yaml:"system_prompt" json:"system_prompt"`
	Suffix          string  `yaml:"suffix" json:"suffix"` // e.g. " /nothink"
	Temperature     float64 `yaml:"temperature" json:"temperature"`
	MaxTokens       int     `yaml:"max_tokens" json:"max_tokens"`
	TimeoutSec      int     `yaml:"timeout_sec" json:"timeout_sec"`
	Default         bool    `yaml:"default" json:"default"`
	ProviderClass   string  `yaml:"provider_class" json:"provider_class"` // local|cloud|frontier; default "local" when empty
}

type Registry interface {
	Get(name string) (Preset, bool)
	Default() (Preset, bool)
	List() []Preset
}

// Router resolves a language hint to a preferred model name via routing.yaml
// — a plain data lookup, never inference. Crew Chief only reads the table;
// deciding what belongs in it is the operator's/brain's job (informed by
// chaio-bench, which grades candidate models offline).
type Router interface {
	// Resolve returns the preferred model name for lang, and false if
	// there's no entry (caller falls back to the registry default).
	Resolve(lang string) (string, bool)
}

// ---- plugin seams -----------------------------------------------------------

// CompletionRequest is provider-agnostic.
type CompletionRequest struct {
	System      string
	User        string
	Temperature float64
	MaxTokens   int
	Timeout     time.Duration
}

type CompletionResponse struct {
	Content      string
	Reasoning    string // reasoning_content if present
	PromptTokens int
	OutputTokens int
	TokPerSec    float64
	Raw          []byte // full response body for archiving
}

type Provider interface {
	Complete(ctx context.Context, base Preset, req CompletionRequest) (CompletionResponse, error)
}

type AttemptFilter struct {
	Model   string
	Outcome AttemptOutcome
	Since   time.Time
	Search  string // substring match on task summary
	Limit   int
}

// RequestRecord is the queryable state of one delegation (GET /requests/{id}).
type RequestRecord struct {
	ID        string         `json:"request_id"`
	Status    DelegateStatus `json:"status"`
	Artifact  string         `json:"artifact,omitempty"`
	Error     string         `json:"error,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type Store interface {
	RecordRequest(ctx context.Context, id string, req DelegateRequest, status DelegateStatus) error
	UpdateRequestStatus(ctx context.Context, id string, status DelegateStatus) error
	// UpdateRequestResult stores the terminal outcome so async callers can poll it.
	UpdateRequestResult(ctx context.Context, id string, status DelegateStatus, artifact, errMsg string) error
	GetRequest(ctx context.Context, id string) (RequestRecord, bool, error)
	RecordAttempt(ctx context.Context, a Attempt) error
	GetAttempt(ctx context.Context, id string) (Attempt, bool, error)
	QueryAttempts(ctx context.Context, f AttemptFilter) ([]Attempt, error)
	Stats(ctx context.Context) ([]StatRow, error)
	StatsTotals(ctx context.Context) (StatsTotals, error)
	Close() error
}

// StatRow is one aggregate bucket: model × outcome.
type StatRow struct {
	Model     string         `json:"model"`
	Outcome   AttemptOutcome `json:"outcome"`
	Count     int            `json:"count"`
	AvgWallMS float64        `json:"avg_wall_ms"`
	AvgTokens float64        `json:"avg_tokens"`
	CostUSD   float64        `json:"cost_usd"`
}

// StatsTotals is the whole-store rollup used to answer "frontier tokens per
// dollar spent": total spend vs. what the same tokens would have cost at
// frontier rates. Counterfactual/savings are computed by the server from
// PromptTokens+OutputTokens via internal/rates, not stored here.
type StatsTotals struct {
	Attempts     int     `json:"attempts"`
	PromptTokens int64   `json:"prompt_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

type Archiver interface {
	Put(ctx context.Context, blob []byte) (ref string, err error) // content-addressed
	Get(ctx context.Context, ref string) ([]byte, error)
}
