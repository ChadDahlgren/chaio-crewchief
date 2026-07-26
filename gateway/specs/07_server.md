Package: `server`, file path `internal/server/server.go`.

Types you consume, verbatim from `dispatch/internal/types/types.go` (import
`"dispatch/internal/types"`):

```go
package types

type DelegateRequest struct {
	Task  string `json:"task"`
	Model string `json:"model,omitempty"`
	Mode  Mode   `json:"mode,omitempty"`
	Tests string `json:"tests,omitempty"`
	Lang  string `json:"lang,omitempty"`
	Async bool   `json:"async,omitempty"`
}

type DelegateResult struct {
	RequestID string         `json:"request_id"`
	Status    DelegateStatus `json:"status"`
	Artifact  string         `json:"artifact,omitempty"`
	Attempts  []Attempt      `json:"attempts"`
	Escalation string        `json:"escalation,omitempty"`
}

type AttemptFilter struct {
	Model   string
	Verdict VerdictStatus
	Stage   Stage
	Since   time.Time
	Search  string
	Limit   int
}

type Preset struct {
	Name string `json:"name"`
	BaseURL string `json:"base_url"`
	// ... other fields, all json-tagged; see types.go
}

type Registry interface {
	Get(name string) (Preset, bool)
	Default() (Preset, bool)
	List() []Preset
}

type Archiver interface {
	Put(ctx context.Context, blob []byte) (ref string, err error)
	Get(ctx context.Context, ref string) ([]byte, error)
}

type Store interface {
	// ... QueryAttempts(ctx, AttemptFilter) ([]Attempt, error); Stats(ctx) ([]StatRow, error); GetAttempt(ctx, id) (Attempt, bool, error); etc.
}
```

Define this small interface YOURSELF in the server package (do NOT import
`dispatch/internal/engine` — the server package must depend only on
`dispatch/internal/types`, keeping it independently testable with fakes):

```go
package server

type Engine interface {
	Run(ctx context.Context, req types.DelegateRequest) (types.DelegateResult, error)
}
```

Required exported API in package `server`:

```go
package server

func New(eng Engine, store types.Store, reg types.Registry, arch types.Archiver) http.Handler
```

(The real `*engine.Engine` type built elsewhere in this project already has a
matching `Run` method, so it satisfies this `Engine` interface automatically —
that wiring happens in `cmd/dispatch/main.go`, not here.)

Routes (use Go 1.22+ stdlib `http.ServeMux` pattern syntax like `"POST /delegate"`):

| Route | Behavior |
|---|---|
| `POST /delegate` | Decode JSON body into `types.DelegateRequest`. If `async` field is false/absent: call `eng.Run(r.Context(), req)` synchronously, write JSON result with 200 (or 500 on internal error, with `{"error": "..."}` body). If `async` is true: generate a request id is NOT your job (engine does that internally) — instead: respond immediately with 202 and `{"request_id": "<id>"}` where `<id>` is obtained by launching `eng.Run` in a goroutine using `context.Background()` (detached from the request) and you need SOME way to know the id before Run returns. Simplify: for this spec, when async=true, still call eng.Run but in a goroutine, and return 202 immediately with body `{"status":"accepted"}` (no id needed since engine doesn't expose ids ahead of time) — do not block the goroutine's errors from crashing the process (recover from panics, just log via a comment, ignoring the error is fine). |
| `GET /attempts/{id}` | Path value `id` via `r.PathValue("id")`. Call `store.GetAttempt`. Found → 200 + JSON attempt. Not found → 404 + `{"error":"not found"}`. |
| `GET /history` | Parse query params: `model`, `verdict`, `stage`, `search`, `limit` (int, default 50 if absent/invalid), `since` (RFC3339 timestamp string, if absent zero time). Build `types.AttemptFilter`, call `store.QueryAttempts`, return 200 + JSON array. |
| `GET /stats` | Call `store.Stats`, return 200 + JSON array. |
| `GET /models` | Call `reg.List()`, return 200 + JSON array of presets (health probing NOT required here — keep it simple, just list). |
| `GET /health` | For each preset in `reg.List()`, do a best-effort `http.Get(preset.BaseURL + "/health")` with a 2-second client timeout in a goroutine (parallel probes), collect `{"name":..., "healthy": bool}` per preset. Always return 200 with `{"status":"ok","models":[...]}` — never fail this endpoint due to a downstream being down. |

Constraints:
- Deps: stdlib only (`net/http`, `encoding/json`, `context`, `net/url`, `strconv`, `time`, `sync`) plus `"dispatch/internal/types"` and `"dispatch/internal/engine"`.
- Use `http.NewServeMux()` with Go 1.22+ method+path patterns (`mux.HandleFunc("POST /delegate", ...)`).
- All responses `Content-Type: application/json`.
- Never panic on malformed input — return 400 with `{"error":"..."}` for bad JSON bodies or bad query params (except `limit`/`since` parse failures, which should just fall back to defaults, not 400).
- No package-level globals.

Edge cases you will be tested on:
1. `POST /delegate` with a valid body and a fake engine that returns `StatusSolved` → response is 200 with JSON containing `"status":"solved"`.
2. `POST /delegate` with malformed JSON body → 400, JSON error body (not a crash, not an empty 500).
3. `GET /attempts/{id}` for a missing id → 404 with a JSON error body.
4. `GET /history?model=glm&limit=5` → the filter passed to the fake Store's QueryAttempts has Model=="glm" and Limit==5.
5. `GET /health` never returns non-200 even when a preset's BaseURL points to an address that will refuse the connection (e.g. an unused local port) — response still 200 with that model marked unhealthy.
