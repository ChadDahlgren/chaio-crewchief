Package: `provider`, file path `internal/provider/openai.go`.

Types you consume, verbatim from `dispatch/internal/types/types.go` (import
`"dispatch/internal/types"`):

```go
package types

type Preset struct {
	Name          string
	BaseURL       string
	SystemPrompt  string
	Suffix        string
	Temperature   float64
	MaxTokens     int
	ThinkBudget   int
	ThinkEligible bool
	TimeoutSec    int
	Default       bool
}

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
```

Required exported API in package `provider`:

```go
package provider

func New() *OpenAI          // zero-config constructor, holds an *http.Client
func (o *OpenAI) Complete(ctx context.Context, base types.Preset, req types.CompletionRequest) (types.CompletionResponse, error)
```

`*OpenAI` must satisfy `types.Provider`.

Behavior table:

| Step | Behavior |
|---|---|
| Endpoint | POST to `{base.BaseURL}/v1/chat/completions`. base.BaseURL may or may not already end in `/v1`; normalize so the final URL always has exactly one `/v1/chat/completions` (e.g. `http://x:8080` and `http://x:8080/v1` both produce `http://x:8080/v1/chat/completions`). |
| Request body | JSON: `{"messages":[{"role":"system","content":req.System},{"role":"user","content":req.User}],"temperature":req.Temperature,"max_tokens":req.MaxTokens,"stream":false}`. |
| Timeout | Derive a context with timeout req.Timeout from ctx (context.WithTimeout), use it for the HTTP request. If req.Timeout <= 0, don't apply an additional timeout (just use ctx as-is). |
| Non-200 response | Return an error whose message includes the HTTP status code and the last portion of the response body (e.g. last 500 bytes or the whole body if shorter) so callers can see the failure without a debugger. |
| Success parsing | Parse JSON body. Extract `choices[0].message.content` into Content. Also look for `choices[0].message.reasoning_content` (may be absent) into Reasoning. Extract `usage.prompt_tokens` and `usage.completion_tokens` into PromptTokens/OutputTokens (0 if usage missing). Extract `timings.predicted_per_second` (llama.cpp-specific, top-level field, may be absent) into TokPerSec; if absent, leave TokPerSec as 0 (do not error). Raw = the full unparsed response body bytes. |
| Empty choices | If `choices` is empty or missing, return an error (do not panic on index access). |

Constraints:
- Deps: stdlib only (`net/http`, `encoding/json`, `context`, `fmt`, `io`, `bytes`, `time`, `strings`).
- Import `"dispatch/internal/types"`.
- No package-level globals besides maybe a default `http.Client` held on the struct, not a package var.
- Must not leak goroutines or file descriptors: close response bodies.

Edge cases you will be tested on:
1. base.BaseURL without trailing `/v1` and with trailing `/v1` both hit the same effective endpoint.
2. Non-200 response (e.g. 500 with a JSON error body) yields a Go error containing "500" and some body content, not a generic "unexpected status" with no detail.
3. A response with `reasoning_content` populates CompletionResponse.Reasoning; a response without it leaves Reasoning empty and does not error.
4. A response including a top-level `timings.predicted_per_second` populates TokPerSec; a response without a `timings` key at all still parses successfully with TokPerSec 0.
5. req.Timeout shorter than the server's simulated delay causes Complete to return a context-deadline-exceeded-flavored error, not hang forever.
