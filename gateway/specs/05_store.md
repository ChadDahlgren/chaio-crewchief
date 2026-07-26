Package: `store`, file path `internal/store/sqlite.go`.

Types you consume, verbatim from `dispatch/internal/types/types.go` (import
`"dispatch/internal/types"`):

```go
package types

type DelegateRequest struct {
	Task  string
	Model string
	Mode  Mode
	Tests string
	Lang  string
	Async bool
}

type Attempt struct {
	ID           string
	RequestID    string
	Stage        Stage
	Model        string
	StartedAt    time.Time
	WallMS       int64
	PromptTokens int
	OutputTokens int
	TokPerSec    float64
	Verdict      VerdictStatus
	VerdictInfo  string
	PromptRef    string
	ResponseRef  string
	ArtifactRef  string
}

type DelegateStatus string // "solved" | "escalate" | "running"

type AttemptFilter struct {
	Model   string
	Verdict VerdictStatus
	Stage   Stage
	Since   time.Time
	Search  string // substring match on task summary
	Limit   int
}

type StatRow struct {
	Model     string
	Stage     Stage
	Verdict   VerdictStatus
	Count     int
	AvgWallMS float64
	AvgTokens float64
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
```

Required exported API in package `store`:

```go
package store

func Open(path string) (*SQLite, error) // path = sqlite file path, e.g. "./dispatch.db"
```

`*SQLite` must implement every method of `types.Store` (paste all six methods plus Close above).

Driver: `modernc.org/sqlite` (pure-Go, CGO-free). Register/use with
`sql.Open("sqlite", path)` — the driver name string is exactly `"sqlite"`.

Schema (create in Open, idempotently, e.g. `CREATE TABLE IF NOT EXISTS`):

```sql
CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);

CREATE TABLE IF NOT EXISTS requests (
  id TEXT PRIMARY KEY,
  task TEXT NOT NULL,
  model TEXT,
  mode TEXT,
  tests TEXT,
  lang TEXT,
  async INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS attempts (
  id TEXT PRIMARY KEY,
  request_id TEXT NOT NULL,
  stage TEXT NOT NULL,
  model TEXT NOT NULL,
  started_at TEXT NOT NULL,
  wall_ms INTEGER NOT NULL,
  prompt_tokens INTEGER NOT NULL,
  output_tokens INTEGER NOT NULL,
  tok_per_sec REAL NOT NULL,
  verdict TEXT NOT NULL,
  verdict_info TEXT,
  prompt_ref TEXT,
  response_ref TEXT,
  artifact_ref TEXT
);
```

Insert a row into `schema_migrations` (version 1) on first successful create,
guarded so re-running Open on an existing DB does not error or duplicate it
(e.g. `INSERT OR IGNORE`).

Behavior table:

| Method | Behavior |
|---|---|
| Open(path) | Opens/creates the sqlite file, applies schema above, returns *SQLite wrapping *sql.DB. |
| RecordRequest(ctx, id, req, status) | INSERT into requests (id, task, model, mode, tests, lang, async, status, created_at=now UTC RFC3339). |
| UpdateRequestStatus(ctx, id, status) | UPDATE requests SET status=? WHERE id=?. No error if id not found (0 rows affected is fine). |
| RecordAttempt(ctx, a) | INSERT into attempts, all fields mapped 1:1; StartedAt stored as RFC3339 string. |
| GetAttempt(ctx, id) | SELECT by id. Returns (Attempt{}, false, nil) if not found — not an error. Returns (attempt, true, nil) if found. |
| QueryAttempts(ctx, f) | SELECT from attempts with WHERE clauses AND-combined for each non-zero field of f: Model (exact match, if non-empty), Verdict (exact, if non-empty), Stage (exact, if non-empty), Since (started_at >= f.Since, if not zero time), Search (task substring match — requires a JOIN or subquery against requests.task LIKE '%'||?||'%' via request_id, if f.Search non-empty). ORDER BY started_at DESC. LIMIT f.Limit if > 0, else no limit (or a sane large default like 1000). |
| Stats(ctx) | SELECT model, stage, verdict, COUNT(*), AVG(wall_ms), AVG(output_tokens) FROM attempts GROUP BY model, stage, verdict. |
| Close() | Closes the underlying *sql.DB. |

Constraints:
- Deps: `modernc.org/sqlite` (blank driver import `_ "modernc.org/sqlite"`) plus `database/sql` and stdlib only.
- Import `"dispatch/internal/types"`.
- Use parameterized queries everywhere (no string-formatted SQL with user input).
- No package-level globals besides perhaps a schema DDL string constant.

Edge cases you will be tested on:
1. Open on a fresh path creates all tables; calling Open again on the same path (or RecordAttempt+GetAttempt round trip) does not error.
2. RecordAttempt then GetAttempt by id returns the same field values (including float TokPerSec and empty-string optional fields).
3. GetAttempt on an unknown id returns (zero Attempt, false, nil) — not an error.
4. QueryAttempts with Model filter returns only matching rows; with Search filter matches against the associated request's Task substring.
5. Stats groups correctly: two attempts with the same model/stage/verdict produce one StatRow with Count=2 and correct averages.
