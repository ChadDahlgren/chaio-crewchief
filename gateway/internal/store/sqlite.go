package store

import (
	"context"
	"database/sql"
	"fmt"
	_ "modernc.org/sqlite"
	"strings"
	"time"

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

// migration is one forward schema step, applied in order and recorded in
// schema_migrations by version so RegisterMigration-style plugin packages
// (per ARCHITECTURE.md) have a place to append future entries.
type migration struct {
	version int
	up      func(db *sql.DB) error
}

var migrations = []migration{
	{
		version: 2, // Phase 1 token/cost ledger
		up: func(db *sql.DB) error {
			if _, err := db.Exec(`ALTER TABLE attempts ADD COLUMN provider_class TEXT NOT NULL DEFAULT 'local'`); err != nil {
				return fmt.Errorf("add provider_class: %w", err)
			}
			if _, err := db.Exec(`ALTER TABLE attempts ADD COLUMN cost_usd REAL NOT NULL DEFAULT 0`); err != nil {
				return fmt.Errorf("add cost_usd: %w", err)
			}
			return nil
		},
	},
	{
		version: 3, // async result columns (fresh DBs: attempts table DDL predates the ledger, so ALTERs may partially no-op)
		up: func(db *sql.DB) error {
			// ignore "duplicate column" from DBs created after these columns
			// joined schemaDDL; sqlite has no ADD COLUMN IF NOT EXISTS.
			if _, err := db.Exec(`ALTER TABLE requests ADD COLUMN artifact TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
				return fmt.Errorf("add artifact: %w", err)
			}
			if _, err := db.Exec(`ALTER TABLE requests ADD COLUMN escalation TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
				return fmt.Errorf("add escalation: %w", err)
			}
			return nil
		},
	},
}

func applyMigrations(db *sql.DB) error {
	var current int
	if err := db.QueryRow("SELECT COALESCE(MAX(version), 1) FROM schema_migrations").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := m.up(db); err != nil {
			return fmt.Errorf("migration %d: %w", m.version, err)
		}
		if _, err := db.Exec("INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (?, ?)", m.version, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
		current = m.version
	}
	return nil
}

type SQLite struct {
	db *sql.DB
}

func Open(path string) (*SQLite, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	if _, err := db.Exec(schemaDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to execute schema DDL: %w", err)
	}

	if _, err := db.Exec("INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (1, ?)", time.Now().UTC().Format(time.RFC3339)); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to record schema migration: %w", err)
	}

	if err := applyMigrations(db); err != nil {
		db.Close()
		return nil, err
	}

	return &SQLite{db: db}, nil
}

func (s *SQLite) RecordRequest(ctx context.Context, id string, req types.DelegateRequest, status types.DelegateStatus) error {
	// mode/tests columns are retained (unused, empty) to avoid a schema
	// migration; the verify/mode concepts they held no longer exist.
	query := `INSERT INTO requests (id, task, model, mode, tests, lang, async, status, created_at) VALUES (?, ?, ?, '', '', ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query,
		id, req.Task, req.Model, req.Lang, req.Async, status,
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (s *SQLite) UpdateRequestStatus(ctx context.Context, id string, status types.DelegateStatus) error {
	query := `UPDATE requests SET status = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, status, id)
	return err
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
