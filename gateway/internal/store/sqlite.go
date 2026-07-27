package store

import (
	"context"
	"database/sql"
	"fmt"
	_ "modernc.org/sqlite"
	"strings"
	"time"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/ownership"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/types"
)

var schemaDDL = `
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
  artifact TEXT NOT NULL DEFAULT '',
  escalation TEXT NOT NULL DEFAULT '',
  owner_pid INTEGER NOT NULL DEFAULT 0,
  owner_host TEXT NOT NULL DEFAULT '',
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
`

// postMigrationDDL holds schema objects that depend on columns a migration
// adds, and so cannot live in schemaDDL.
//
// schemaDDL runs before applyMigrations, and on any database that already
// exists its CREATE TABLE IF NOT EXISTS statements no-op — the table keeps
// whatever columns it had. An index over owner_host therefore met a requests
// table without owner_host on every ledger the previous release created, and
// Open failed at the DDL step before migration 4 could add the column. That is
// every existing install, deterministically, with no way out but sqlite3 by
// hand. Anything referencing a migrated column belongs here, after migrations.
//
// The index itself: ReapOrphans filters on exactly this pair on every embedded
// start, which is now once per Claude Code session rather than once per daemon
// start. The ledger is append-only and grows forever, so the scan it replaces
// gets slower for the life of the install.
const postMigrationDDL = `
CREATE INDEX IF NOT EXISTS idx_requests_owner_status ON requests (owner_host, status);
`

// migration is one forward schema step, applied in order and recorded in
// schema_migrations by version so RegisterMigration-style plugin packages
// (per ARCHITECTURE.md) have a place to append future entries.
//
// up runs inside the same transaction that records the version, so a migration
// is all-or-nothing: an interrupted run leaves the database exactly as it was,
// never half-migrated with nothing recorded.
type migration struct {
	version int
	up      func(ctx context.Context, tx execer) error
}

// execer is the write half of a pinned connection running an open transaction.
// It exists because database/sql's BeginTx cannot express BEGIN IMMEDIATE, so
// the transaction is driven by hand on a *sql.Conn rather than a *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// addColumn applies one ADD COLUMN, tolerating a column that is already there.
//
// SQLite has no ADD COLUMN IF NOT EXISTS, and every one of these migrations can
// legitimately meet a database that already has the column: either because
// schemaDDL grew it later, or — before migrations were transactional — because
// an earlier run applied the ALTER and died before recording the version. That
// second case used to be unrecoverable, since applyMigrations floors the
// version at 1 and would re-run the ALTER forever. Tolerating the duplicate is
// what heals those databases on the next open.
func addColumn(ctx context.Context, tx execer, what, stmt string) error {
	if _, err := tx.ExecContext(ctx, stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

var migrations = []migration{
	{
		version: 2, // Phase 1 token/cost ledger
		up: func(ctx context.Context, tx execer) error {
			if err := addColumn(ctx, tx, "add provider_class", `ALTER TABLE attempts ADD COLUMN provider_class TEXT NOT NULL DEFAULT 'local'`); err != nil {
				return err
			}
			return addColumn(ctx, tx, "add cost_usd", `ALTER TABLE attempts ADD COLUMN cost_usd REAL NOT NULL DEFAULT 0`)
		},
	},
	{
		version: 3, // async result columns (fresh DBs: attempts table DDL predates the ledger, so ALTERs may partially no-op)
		up: func(ctx context.Context, tx execer) error {
			if err := addColumn(ctx, tx, "add artifact", `ALTER TABLE requests ADD COLUMN artifact TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
			return addColumn(ctx, tx, "add escalation", `ALTER TABLE requests ADD COLUMN escalation TEXT NOT NULL DEFAULT ''`)
		},
	},
	{
		version: 4, // request ownership, for orphan reaping
		up: func(ctx context.Context, tx execer) error {
			for _, stmt := range []string{
				`ALTER TABLE requests ADD COLUMN owner_pid INTEGER NOT NULL DEFAULT 0`,
				`ALTER TABLE requests ADD COLUMN owner_host TEXT NOT NULL DEFAULT ''`,
			} {
				if err := addColumn(ctx, tx, "add owner columns", stmt); err != nil {
					return err
				}
			}
			return nil
		},
	},
}

// applyMigrations brings the database up to the highest known version.
//
// Each migration gets its own write transaction, opened with BEGIN IMMEDIATE so
// the write lock is taken before the version is read rather than upgraded
// afterwards. Two processes opening the same ledger at once therefore serialize
// here: the loser waits out its busy_timeout and then re-reads the version
// inside its own transaction, finds the work already recorded, and skips it.
// Reading MAX(version) outside a transaction, as this used to, let both
// processes decide to run the same ALTER.
func applyMigrations(ctx context.Context, db *sql.DB) error {
	for _, m := range migrations {
		if err := applyMigration(ctx, db, m); err != nil {
			return fmt.Errorf("migration %d: %w", m.version, err)
		}
	}
	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	// database/sql's BeginTx cannot express BEGIN IMMEDIATE, so drive the
	// transaction on a pinned connection by hand.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	var current int
	if err := conn.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 1) FROM schema_migrations").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if m.version <= current {
		return nil
	}

	if err := m.up(ctx, conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (?, ?)", m.version, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("record migration %d: %w", m.version, err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}

type SQLite struct {
	db        *sql.DB
	ownerPID  int
	ownerHost string
}

// dsn builds the driver connection string for a database path.
//
// Embedded mode made this ledger multi-writer by design: two Claude Code
// sessions, or `usage --local` alongside a live MCP session, all open the same
// file. Under the default rollback journal a writer that loses a collision
// gets an immediate "database is locked (5)" rather than waiting, which
// surfaces to the user as a delegation that failed for no visible reason.
//
// WAL lets readers proceed during a write, and busy_timeout turns most
// remaining writer-writer collisions into a short wait instead of an error.
//
// "Most", not all: switching a database into WAL takes an exclusive lock that
// the busy handler does not cover, so busy_timeout does not protect the very
// first open of a new file, nor the first open of a legacy rollback-journal
// ledger. Concurrent first opens genuinely can fail with "database is locked",
// which is why Open retries rather than trusting the pragma. Once the database
// is in WAL, busy_timeout covers every writer collision as described.
//
// modernc.org/sqlite takes pragmas as _pragma query parameters and applies
// them per connection, ordering busy_timeout first. The path is left outside
// any file: URI on purpose: without the file: prefix the driver truncates the
// DSN at '?' and passes the remainder through literally, so relative paths and
// paths containing spaces need no escaping. (A path containing a literal '?'
// would break this, and would break the file: form too.)
//
// WAL creates -wal and -shm siblings next to the database, so a deployment
// that whitelists writable paths must grant the directory, not just the file.
func dsn(path string) string {
	return path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
}

// openAttempts bounds the retry described on Open. Twenty-five tries with the
// 20ms*(i+1) backoff below spans about 6.5s.
//
// It used to be ten, about 1.1s, sized against the WAL conversion itself —
// microseconds of exclusive lock. That was the wrong thing to size against.
// The conversion cannot start while another process holds the database, so the
// real wait is however long that other writer takes, and on a pre-WAL ledger
// nothing shortens it: busy_timeout does not cover the exclusive lock the
// conversion needs. A writer holding a legacy database for longer than a
// second made Open give up with "database is locked" — and the first open
// after upgrading is exactly when every install passes through this window.
//
// The budget now exceeds the 5s busy_timeout in dsn, so the pre-WAL case is no
// longer the impatient one: once the database is in WAL, busy_timeout absorbs
// the collision first and this retry never runs. 6.5s is still short enough
// that a genuinely stuck ledger surfaces as an error rather than a hang.
const openAttempts = 25

// isLocked reports whether err is SQLite's transient busy/locked condition,
// the only class of failure Open retries. Anything else — a bad path, a corrupt
// file, a genuine schema conflict — is returned immediately, because retrying
// it just delays the same error.
func isLocked(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "database is locked") || strings.Contains(s, "table is locked")
}

// Open opens (creating if needed) the ledger at path and brings its schema up
// to date.
//
// The create-and-migrate window is the one place this is not simply safe to do
// concurrently, and embedded mode made concurrency the normal case: a fresh
// install where `usage --local` and an MCP session start together, or two
// Claude Code sessions launching at once. Two things can collide there — the
// journal_mode(WAL) conversion, which takes an exclusive lock the busy handler
// does not cover (see dsn), and the migrations, which now serialize on their
// own BEGIN IMMEDIATE. Both failures are transient and clear as soon as the
// other process finishes, so Open retries the whole sequence with a short
// backoff instead of surfacing "database is locked" to the user as a server
// that would not start.
//
// Steady state is untouched: an already-migrated WAL database succeeds on the
// first attempt and never sleeps.
func Open(path string) (*SQLite, error) {
	var err error
	for i := 0; i < openAttempts; i++ {
		var s *SQLite
		s, err = openOnce(path)
		if err == nil {
			return s, nil
		}
		if !isLocked(err) {
			return nil, err
		}
		// Linear backoff, jittered by nothing: the contending process holds the
		// exclusive lock for microseconds, so spacing retries out is enough and
		// two losers colliding again merely costs another 20ms.
		time.Sleep(time.Duration(i+1) * 20 * time.Millisecond)
	}
	return nil, fmt.Errorf("open database after %d attempts: %w", openAttempts, err)
}

func openOnce(path string) (*SQLite, error) {
	ctx := context.Background()

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, err
	}

	if _, err := db.ExecContext(ctx, schemaDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to execute schema DDL: %w", err)
	}

	if _, err := db.ExecContext(ctx, "INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (1, ?)", time.Now().UTC().Format(time.RFC3339)); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to record schema migration: %w", err)
	}

	if err := applyMigrations(ctx, db); err != nil {
		db.Close()
		return nil, err
	}

	if _, err := db.ExecContext(ctx, postMigrationDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to execute post-migration schema DDL: %w", err)
	}

	return &SQLite{db: db}, nil
}

func (s *SQLite) RecordRequest(ctx context.Context, id string, req types.DelegateRequest, status types.DelegateStatus) error {
	// mode/tests columns are retained (unused, empty) to avoid a schema
	// migration; the verify/mode concepts they held no longer exist.
	query := `INSERT INTO requests (id, task, model, mode, tests, lang, async, status, owner_pid, owner_host, created_at) VALUES (?, ?, ?, '', '', ?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query,
		id, req.Task, req.Model, req.Lang, req.Async, status,
		s.ownerPID, s.ownerHost,
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (s *SQLite) UpdateRequestStatus(ctx context.Context, id string, status types.DelegateStatus) error {
	query := `UPDATE requests SET status = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, status, id)
	return err
}

// AssumeOwnership tells the store which process owns the requests it records,
// so RecordRequest can stamp every row without the engine knowing anything
// about processes. Call it once, right after Open. A store that was never told
// records no owner, and rows without one are never reaped.
func (s *SQLite) AssumeOwnership(pid int, host string) {
	s.ownerPID, s.ownerHost = pid, host
}

// ReapOrphans fails every request left running by a process that no longer
// exists, and reports how many it failed.
//
// Two guards keep it from failing live work. Rows recorded on another host are
// never touched: a ledger written by a server elsewhere must not have this
// machine declaring its rows dead. Rows with no recorded owner are never
// touched either, since they predate ownership and a zero PID says nothing.
// Anything else ambiguous is left alone — a stale row is a smaller harm than a
// wrongly failed one.
func (s *SQLite) ReapOrphans(ctx context.Context, lockDir, host string) (int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, owner_pid FROM requests
		  WHERE status = ? AND owner_host = ? AND owner_pid > 0`,
		string(types.StatusRunning), host)
	if err != nil {
		return 0, fmt.Errorf("query running requests: %w", err)
	}

	type candidate struct {
		id  string
		pid int
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.pid); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan running request: %w", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("iterate running requests: %w", err)
	}
	rows.Close()

	reaped := 0
	for _, c := range candidates {
		alive, err := ownership.OwnerAlive(lockDir, c.pid)
		if err != nil || alive {
			continue // ambiguous or live: leave it alone
		}
		if err := s.UpdateRequestResult(ctx, c.id, types.StatusFailed, "",
			fmt.Sprintf("orphaned: owning process %d on %s exited before finishing", c.pid, host)); err != nil {
			return reaped, fmt.Errorf("fail orphan %s: %w", c.id, err)
		}
		reaped++
	}
	return reaped, nil
}

func (s *SQLite) UpdateRequestResult(ctx context.Context, id string, status types.DelegateStatus, artifact, errMsg string) error {
	// "escalation" column retained under its original name (schema stability);
	// it now stores the last mechanical-failure error message, if any.
	query := `UPDATE requests SET status = ?, artifact = ?, escalation = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, status, artifact, errMsg, id)
	return err
}

func (s *SQLite) GetRequest(ctx context.Context, id string) (types.RequestRecord, bool, error) {
	query := `SELECT id, status, artifact, escalation, created_at FROM requests WHERE id = ?`
	var rec types.RequestRecord
	var createdAt string
	err := s.db.QueryRowContext(ctx, query, id).Scan(&rec.ID, &rec.Status, &rec.Artifact, &rec.Error, &createdAt)
	if err == sql.ErrNoRows {
		return types.RequestRecord{}, false, nil
	}
	if err != nil {
		return types.RequestRecord{}, false, err
	}
	rec.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return rec, true, nil
}

// stage/verdict/verdict_info columns are retained under their original
// names (schema stability, no migration) but now hold the simplified
// outcome model: stage is always "attempt", verdict is delivered|failed,
// verdict_info is the failure detail (empty when delivered).

func (s *SQLite) RecordAttempt(ctx context.Context, a types.Attempt) error {
	query := `INSERT INTO attempts (
		id, request_id, stage, model, started_at, wall_ms, prompt_tokens, output_tokens, tok_per_sec, verdict, verdict_info, prompt_ref, response_ref, artifact_ref, provider_class, cost_usd
	) VALUES (?, ?, 'attempt', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	providerClass := a.ProviderClass
	if providerClass == "" {
		providerClass = "local"
	}
	_, err := s.db.ExecContext(ctx, query,
		a.ID, a.RequestID, a.Model, a.StartedAt.Format(time.RFC3339),
		a.WallMS, a.PromptTokens, a.OutputTokens, a.TokPerSec, a.Outcome, a.Error,
		a.PromptRef, a.ResponseRef, a.ArtifactRef, providerClass, a.CostUSD,
	)
	return err
}

func (s *SQLite) GetAttempt(ctx context.Context, id string) (types.Attempt, bool, error) {
	if s.db == nil {
		return types.Attempt{}, false, fmt.Errorf("database not initialized")
	}
	query := `SELECT id, request_id, model, started_at, wall_ms, prompt_tokens, output_tokens, tok_per_sec, verdict, verdict_info, prompt_ref, response_ref, artifact_ref, provider_class, cost_usd FROM attempts WHERE id = ?`
	var a types.Attempt
	var startedAtStr string
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&a.ID, &a.RequestID, &a.Model, &startedAtStr,
		&a.WallMS, &a.PromptTokens, &a.OutputTokens, &a.TokPerSec,
		&a.Outcome, &a.Error, &a.PromptRef, &a.ResponseRef, &a.ArtifactRef,
		&a.ProviderClass, &a.CostUSD,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return types.Attempt{}, false, nil
		}
		return types.Attempt{}, false, err
	}
	a.StartedAt, err = time.Parse(time.RFC3339, startedAtStr)
	if err != nil {
		return types.Attempt{}, false, fmt.Errorf("failed to parse started_at: %w", err)
	}
	return a, true, nil
}

func (s *SQLite) QueryAttempts(ctx context.Context, f types.AttemptFilter) ([]types.Attempt, error) {
	var whereClauses []string
	var args []interface{}

	baseQuery := `
		SELECT a.id, a.request_id, a.model, a.started_at, a.wall_ms, a.prompt_tokens, a.output_tokens, a.tok_per_sec, a.verdict, a.verdict_info, a.prompt_ref, a.response_ref, a.artifact_ref, a.provider_class, a.cost_usd
		FROM attempts a
		LEFT JOIN requests r ON a.request_id = r.id
	`
	if f.Search != "" {
		whereClauses = append(whereClauses, "r.task LIKE ?")
		args = append(args, "%"+f.Search+"%")
	}
	if f.Model != "" {
		whereClauses = append(whereClauses, "a.model = ?")
		args = append(args, f.Model)
	}
	if f.Outcome != "" {
		whereClauses = append(whereClauses, "a.verdict = ?")
		args = append(args, f.Outcome)
	}
	if !f.Since.IsZero() {
		whereClauses = append(whereClauses, "a.started_at >= ?")
		args = append(args, f.Since.Format(time.RFC3339))
	}

	var query strings.Builder
	query.WriteString(baseQuery)
	if len(whereClauses) > 0 {
		query.WriteString(" WHERE ")
		query.WriteString(strings.Join(whereClauses, " AND "))
	}
	query.WriteString(" ORDER BY a.started_at DESC")

	if f.Limit > 0 {
		query.WriteString(" LIMIT ?")
		args = append(args, f.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attempts []types.Attempt
	for rows.Next() {
		var a types.Attempt
		var startedAtStr string
		err := rows.Scan(
			&a.ID, &a.RequestID, &a.Model, &startedAtStr,
			&a.WallMS, &a.PromptTokens, &a.OutputTokens, &a.TokPerSec,
			&a.Outcome, &a.Error, &a.PromptRef, &a.ResponseRef, &a.ArtifactRef,
			&a.ProviderClass, &a.CostUSD,
		)
		if err != nil {
			return nil, err
		}
		a.StartedAt, err = time.Parse(time.RFC3339, startedAtStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse started_at: %w", err)
		}
		attempts = append(attempts, a)
	}
	return attempts, nil
}

func (s *SQLite) Stats(ctx context.Context) ([]types.StatRow, error) {
	query := `
		SELECT model, verdict, COUNT(*), AVG(wall_ms), AVG(output_tokens), SUM(cost_usd)
		FROM attempts
		GROUP BY model, verdict
	`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []types.StatRow
	for rows.Next() {
		var sr types.StatRow
		err := rows.Scan(
			&sr.Model, &sr.Outcome,
			&sr.Count, &sr.AvgWallMS, &sr.AvgTokens, &sr.CostUSD,
		)
		if err != nil {
			return nil, err
		}
		stats = append(stats, sr)
	}
	return stats, nil
}

func (s *SQLite) StatsTotals(ctx context.Context) (types.StatsTotals, error) {
	query := `
		SELECT COUNT(*), COALESCE(SUM(prompt_tokens), 0), COALESCE(SUM(output_tokens), 0), COALESCE(SUM(cost_usd), 0)
		FROM attempts
	`
	var t types.StatsTotals
	err := s.db.QueryRowContext(ctx, query).Scan(&t.Attempts, &t.PromptTokens, &t.OutputTokens, &t.CostUSD)
	if err != nil {
		return types.StatsTotals{}, err
	}
	return t, nil
}

func (s *SQLite) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}
