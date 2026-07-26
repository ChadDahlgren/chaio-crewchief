Package: `engine`, file path `internal/engine/engine.go`.

Types you consume, verbatim from `dispatch/internal/types/types.go` (import
`"dispatch/internal/types"`):

```go
package types

type Mode string
const (
	ModeFast    Mode = "fast"
	ModeCareful Mode = "careful"
	ModeAuto    Mode = "auto"
)

type DelegateRequest struct {
	Task  string
	Model string
	Mode  Mode
	Tests string
	Lang  string
	Async bool
}

type Stage string
const (
	StageNothink Stage = "nothink"
	StageRetry   Stage = "retry"
	StageThink   Stage = "think"
)

type VerdictStatus string
const (
	VerdictPass       VerdictStatus = "pass"
	VerdictFail       VerdictStatus = "fail"
	VerdictNoArtifact VerdictStatus = "no_artifact"
	VerdictSkipped    VerdictStatus = "skipped"
	VerdictError      VerdictStatus = "error"
)

type Attempt struct {
	ID, RequestID string
	Stage         Stage
	Model         string
	StartedAt     time.Time
	WallMS        int64
	PromptTokens, OutputTokens int
	TokPerSec     float64
	Verdict       VerdictStatus
	VerdictInfo   string
	PromptRef, ResponseRef, ArtifactRef string
}

type DelegateStatus string
const (
	StatusSolved   DelegateStatus = "solved"
	StatusEscalate DelegateStatus = "escalate"
	StatusRunning  DelegateStatus = "running"
)

type DelegateResult struct {
	RequestID string
	Status    DelegateStatus
	Artifact  string
	Attempts  []Attempt
	Escalation string
}

type Preset struct {
	Name, BaseURL, SystemPrompt, Suffix string
	Temperature float64
	MaxTokens, ThinkBudget int
	ThinkEligible bool
	TimeoutSec int
	Default bool
}

type Registry interface {
	Get(name string) (Preset, bool)
	Default() (Preset, bool)
	List() []Preset
}

type CompletionRequest struct {
	System, User string
	Temperature  float64
	MaxTokens    int
	Timeout      time.Duration
}

type CompletionResponse struct {
	Content, Reasoning string
	PromptTokens, OutputTokens int
	TokPerSec float64
	Raw []byte
}

type Provider interface {
	Complete(ctx context.Context, base Preset, req CompletionRequest) (CompletionResponse, error)
}

type Criteria struct {
	Tests, Lang string
}

type Verdict struct {
	Status VerdictStatus
	Output string
}

type Verifier interface {
	Verify(ctx context.Context, artifact string, c Criteria) (Verdict, error)
}

type Store interface {
	RecordRequest(ctx context.Context, id string, req DelegateRequest, status DelegateStatus) error
	UpdateRequestStatus(ctx context.Context, id string, status DelegateStatus) error
	RecordAttempt(ctx context.Context, a Attempt) error
	GetAttempt(ctx context.Context, id string) (Attempt, bool, error)
	QueryAttempts(ctx context.Context, f AttemptFilter) ([]Attempt, error)
	Stats(ctx context.Context) ([]StatRow, error)
	Close() error
}

type Archiver interface {
	Put(ctx context.Context, blob []byte) (ref string, err error)
	Get(ctx context.Context, ref string) ([]byte, error)
}
```

Required exported API in package `engine`:

```go
package engine

func New(reg types.Registry, prov types.Provider, ver types.Verifier, store types.Store, arch types.Archiver) *Engine
func (e *Engine) Run(ctx context.Context, req types.DelegateRequest) (types.DelegateResult, error)
```

You will need a request-id and attempt-id generator. Use a simple one: a
package-level (or receiver-level) counter combined with time is NOT allowed to
be a bare global var mutated without synchronization — instead generate IDs via
`crypto/rand` hex strings (e.g. 16 random bytes hex-encoded) or via
`fmt.Sprintf("%d-%d", time.Now().UnixNano(), <atomic counter>)` using
`sync/atomic`. Either is fine; no external ULID library — stdlib only.

Algorithm (`Run`):

1. Resolve preset: if req.Model != "", `reg.Get(req.Model)`; if not found, return error `"unknown model: <name>"`. If req.Model == "", use `reg.Default()`; if none, return error `"no default model configured"`.
2. Generate a RequestID. Call `store.RecordRequest(ctx, id, req, types.StatusRunning)` (ignore/log error, don't abort the run over a telemetry write failure — but do return it wrapped if RecordRequest fails AND you have no better path forward; prefer to continue best-effort).
3. mode := req.Mode; if empty, ModeAuto.
4. **Stage nothink**: build CompletionRequest{System: preset.SystemPrompt, User: req.Task + preset.Suffix, Temperature: preset.Temperature, MaxTokens: preset.MaxTokens, Timeout: time.Duration(preset.TimeoutSec)*time.Second}. Call prov.Complete. Archive the prompt (marshal a small JSON of system+user) and the raw response via arch.Put, capturing PromptRef/ResponseRef. Extract the first fenced code block (any language tag, or none) from resp.Content using a regex like triple-backtick fenced blocks; take the largest one if multiple. If none found: Verdict = VerdictNoArtifact, ArtifactRef empty, skip Verify. If found: archive it (ArtifactRef), and if req.Tests != "": call ver.Verify(ctx, artifact, Criteria{Tests: req.Tests, Lang: req.Lang}); if req.Tests == "": Verdict = VerdictSkipped (treat as pass-through — code produced, no way to check it, so treat as solved: Status VerdictSkipped counts as terminal-success for this stage, i.e. move straight to StatusSolved). Record the Attempt via store.RecordAttempt with real WallMS/tokens/tok-per-sec from resp. If Verdict is Pass or Skipped → build DelegateResult{Status: StatusSolved, Artifact: artifact, Attempts: [...]}, call store.UpdateRequestStatus(ctx, id, StatusSolved), return.
5. If mode == ModeFast and verdict was Fail/NoArtifact/Error on nothink: still allowed ONE retry (fast = stages 1-3, i.e. nothink+retry only, no think). All modes get the retry stage.
6. **Stage retry**: user = req.Task + "\n\nThis attempt failed:\n```\n" + <previous artifact or raw content if no artifact> + "\n```\n\nError:\n```\n" + <verdict.Output or "no code block found"> + "\n```\n\nOutput the complete fixed file." + preset.Suffix. Same System/Temperature/MaxTokens/Timeout as stage 1. Same archive/verify/record dance, Stage: StageRetry. Pass/Skipped → StatusSolved as above.
7. If retry also fails: think stage runs only if `mode == ModeCareful || (mode == ModeAuto && preset.ThinkEligible)`. If mode == ModeFast, or the think condition isn't met, stop here: build DelegateResult{Status: StatusEscalate, Attempts: [...], Escalation: <JSON summary: task, attempts count, last stage, last verdict, last output>}, call store.UpdateRequestStatus(ctx, id, StatusEscalate), return.
8. **Stage think**: user = req.Task + "\n\nThis attempt failed:\n```\n<code>\n```\n\nError:\n```\n<output>\n```\n\nOutput the complete fixed file." (NO suffix appended — thinking mode does not use `/nothink`). MaxTokens = preset.ThinkBudget, Timeout = time.Duration(preset.TimeoutSec)*time.Second (or a larger multiple — use preset.TimeoutSec*4 seconds to give thinking room; either is acceptable, document your choice is fine, just be generous, e.g. at least preset.TimeoutSec*4). Same archive/verify/record dance, Stage: StageThink. Pass/Skipped → StatusSolved. Fail → StatusEscalate (same Escalation JSON shape as step 7, now covering all 3 attempts).
9. Any Provider.Complete error at any stage (network failure, non-200, timeout): record the attempt with Verdict VerdictError and VerdictInfo = err.Error() (PromptRef still set if you archived the prompt before calling; ResponseRef empty), and treat it like a Fail for progression purposes (continue to next stage per rules above, or escalate if this was the last allowed stage).

Constraints:
- Deps: stdlib only (`context`, `regexp`, `encoding/json`, `time`, `crypto/rand`/`sync/atomic`, `fmt`, `strings`) plus `"dispatch/internal/types"`.
- No package-level mutable globals; counters/state live on `*Engine` guarded by atomic or mutex if needed.
- `DelegateResult.Attempts` must be in chronological order (nothink, [retry], [think]).
- Keep the fenced-code-block extraction regex simple: matches ```` ```lang\ncode``` ```` or ```` ```\ncode``` ````, non-greedy per block, across multiple blocks in one response, picking the longest.

Edge cases you will be tested on:
1. A Provider that returns a passing artifact on the first call (nothink) with req.Tests set and a Verifier that returns Pass → StatusSolved after exactly 1 attempt, Attempts has length 1 with Stage nothink.
2. A Provider that fails verification on stage 1 (Verifier returns Fail) but passes on stage 2 (retry) → StatusSolved after 2 attempts, second attempt's user prompt contains the word "failed" and the previous error output (verify this via a fake Provider that inspects req.User).
3. req.Tests == "" (no tests supplied): first stage's extracted code block with no verification requested → StatusSolved immediately (Skipped verdict treated as success), 1 attempt.
4. A Provider that returns no fenced code block at all on every stage, with mode=ModeFast and a Registry preset that has ThinkEligible=false → after nothink (NoArtifact) + retry (NoArtifact) stops there: StatusEscalate with exactly 2 attempts (think stage never invoked because mode is Fast).
5. Unknown req.Model (not in registry, and not empty) → Run returns a non-nil error immediately, no attempts recorded.
