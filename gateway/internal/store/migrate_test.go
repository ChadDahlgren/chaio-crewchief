package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/types"
)

// A migration interrupted between its ALTER and its version row used to brick
// the ledger permanently: applyMigrations floors the version at 1, so every
// later Open re-ran the same ALTER and died on "duplicate column". Every
// subcommand that opens the store was dead until someone hand-edited
// schema_migrations.
//
// This reproduces that state on a real database and asserts Open heals it with
// the data intact. It must keep passing even though migrations are now atomic:
// databases already in the broken state exist and have to recover on their own.
func TestOpenRecoversFromInterruptedMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "interrupted.db")
	ctx := context.Background()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("initial Open() error = %v", err)
	}
	if err := st.RecordRequest(ctx, "survivor", types.DelegateRequest{Task: "keep me"}, types.StatusDelivered); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// Exactly what a SIGKILL or power loss mid-migration leaves behind: the
	// schema changes are on disk, the version rows recording them are not.
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version > 1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(path)
	if err != nil {
		t.Fatalf("Open() on interrupted database error = %v, want nil", err)
	}
	defer st.Close()

	rec, ok, err := st.GetRequest(ctx, "survivor")
	if err != nil || !ok {
		t.Fatalf("GetRequest() = %v, %v, %v; want the row to survive", rec, ok, err)
	}
	if rec.Status != types.StatusDelivered {
		t.Errorf("status = %q, want %q", rec.Status, types.StatusDelivered)
	}

	// The heal must be complete, not merely non-fatal: the migrated columns
	// have to be usable afterwards.
	if err := st.RecordAttempt(ctx, types.Attempt{
		ID: "a1", RequestID: "survivor", Model: "m",
		ProviderClass: "cloud", CostUSD: 1.25,
	}); err != nil {
		t.Fatalf("RecordAttempt() after heal error = %v", err)
	}
}

// Every migration's DDL and its version row must land together. Rerunning
// applyMigrations against an already-migrated database is the observable proxy:
// it must be a no-op rather than a second round of ALTERs.
func TestApplyMigrationsIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idem.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	for i := 0; i < 3; i++ {
		if err := applyMigrations(context.Background(), st.db); err != nil {
			t.Fatalf("applyMigrations() rerun %d error = %v", i, err)
		}
	}
}

// Concurrent first-open on a database that does not exist yet raced two ways:
// unsynchronized MAX(version) reads had several processes running the same
// ALTER, and the journal_mode(WAL) conversion takes an exclusive lock the busy
// handler does not cover. Run with -count=20 or more; a race that shows up
// sometimes is still a race.
func TestConcurrentOpenOnFreshDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	stores := make([]*SQLite, n)
	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // widen the window: all n hit the create path together
			stores[i], errs[i] = Open(path)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent Open #%d error = %v", i, err)
			continue
		}
		defer stores[i].Close()
	}
	if t.Failed() {
		return
	}

	// Every store must be fully migrated, not merely open: a writer that raced
	// past the migrations would fail on the columns they add.
	ctx := context.Background()
	for i, st := range stores {
		id := "req" + string(rune('a'+i))
		if err := st.RecordRequest(ctx, id, types.DelegateRequest{Task: "t"}, types.StatusRunning); err != nil {
			t.Errorf("store #%d RecordRequest() error = %v", i, err)
		}
		if err := st.RecordAttempt(ctx, types.Attempt{ID: id + "-a", RequestID: id, Model: "m", ProviderClass: "local"}); err != nil {
			t.Errorf("store #%d RecordAttempt() error = %v", i, err)
		}
	}

	// And the schema must have been migrated once, not n times.
	rows, err := stores[0].db.QueryContext(ctx, "SELECT COUNT(*) FROM schema_migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	rows.Next()
	var count int
	if err := rows.Scan(&count); err != nil {
		t.Fatal(err)
	}
	if want := len(migrations) + 1; count != want {
		t.Errorf("schema_migrations has %d rows, want %d", count, want)
	}
}

// Legacy ledgers are still on the rollback journal, so their first embedded
// open performs the WAL conversion under contention — the same exclusive lock,
// reached by a different route than a brand-new file.
func TestConcurrentOpenOnLegacyJournalDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	ctx := context.Background()

	// Build a pre-WAL database with the pre-ledger schema, the way a v0.1
	// install left it.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	legacyDDL := strings.NewReplacer(
		"provider_class TEXT NOT NULL DEFAULT 'local',", "",
		"cost_usd REAL NOT NULL DEFAULT 0,", "",
	).Replace(schemaDDL)
	if _, err := db.ExecContext(ctx, legacyDDL); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	stores := make([]*SQLite, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			stores[i], errs[i] = Open(path)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent Open #%d on legacy database error = %v", i, err)
			continue
		}
		defer stores[i].Close()
	}
}
