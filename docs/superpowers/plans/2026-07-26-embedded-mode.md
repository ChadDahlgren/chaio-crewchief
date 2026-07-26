# Embedded Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `chaio-crewchief mcp` work standalone — no separate `serve` process, no port to configure, no environment variable — while keeping gateway mode available opt-in.

**Architecture:** A new `internal/embed` package performs the same component wiring `serve()` does today and serves `internal/server`'s handler on a kernel-assigned loopback port. The MCP server points its existing HTTP client at that URL, so both modes share one set of handlers and cannot drift. Mode is chosen by whether `CHAIO_CREWCHIEF_URL` is set.

**Tech Stack:** Go (module `github.com/ChadDahlgren/chaio-crewchief/gateway`), stdlib `testing`, `modernc.org/sqlite` (pure Go, `CGO_ENABLED=0`), `github.com/modelcontextprotocol/go-sdk/mcp`, `gopkg.in/yaml.v3`.

## Global Constraints

- Module path prefix: `github.com/ChadDahlgren/chaio-crewchief/gateway`. All work happens under `gateway/`; run every Go command from that directory.
- Build must stay `CGO_ENABLED=0` clean. Do not add cgo-dependent dependencies.
- **Do not add any new third-party module.** Everything here uses the standard library.
- Target platforms are linux/amd64, linux/arm64, darwin/arm64. `syscall.Flock` is available on all three. There is no Windows target.
- Service name constant is `serviceName = "chaio-crewchief"` in `cmd/chaio-crewchief/main.go`. Reuse it; do not hardcode the name in new code where the constant is reachable.
- The project never judges model output. `AttemptOutcome` has exactly two values, `delivered` and `failed`. Do not add a third, and do not add any code that grades an artifact.
- Every commit message ends with the trailer `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.
- Tests use stdlib `testing` only — no testify, no gomock. Table tests where there is more than one case.
- Run `go vet ./...` and `go test -race ./...` before each commit.

## File Structure

**Created:**
- `internal/chome/chome.go` — resolves the home directory and the paths inside it. One responsibility: turning environment into paths.
- `internal/chome/chome_test.go`
- `internal/ownership/ownership.go` — flock-based liveness. Answers exactly one question: is the process that wrote this row still running?
- `internal/ownership/ownership_test.go`
- `internal/embed/embed.go` — component wiring plus the loopback listener.
- `internal/embed/embed_test.go`
- `internal/cli/init.go` — the `init` subcommand.
- `internal/cli/init_test.go`

**Modified:**
- `internal/registry/registry.go` — distinguish absent from malformed.
- `internal/store/sqlite.go` — migration 4 (owner columns), `SetRequestOwner`, `ReapOrphans`.
- `internal/gwurl/gwurl.go` — mode resolution; drop the localhost default.
- `internal/mcpserve/mcpserve.go` — accept a base URL; reject embedded async.
- `cmd/chaio-crewchief/main.go` — `serve()` uses the shared wiring; `mcp` starts embedded; `init` dispatch.
- `internal/cli/usage.go`, `internal/cli/doctor.go` — `--local` / `--gateway`.
- `.github/workflows/ci.yml` — embedded smoke test.
- `README.md`, `SECURITY.md`, `CHANGELOG.md`.

Tasks are ordered so each one compiles and tests green on its own. Tasks 1–4 are leaf packages with no dependencies on each other and could be done in any order; Tasks 5 onward depend on them.

---

### Task 1: Home directory resolution

**Files:**
- Create: `gateway/internal/chome/chome.go`
- Test: `gateway/internal/chome/chome_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `chome.Dir() (string, error)`, `chome.Paths` struct with fields `Home, Models, Rates, Routing, DB, Archive, Locks string`, `chome.Resolve() (Paths, error)`, `chome.ResolveIn(home string) Paths`.

- [ ] **Step 1: Write the failing test**

Create `gateway/internal/chome/chome_test.go`:

```go
package chome

import (
	"path/filepath"
	"testing"
)

func TestDirPrefersEnvOverride(t *testing.T) {
	t.Setenv("CHAIO_CREWCHIEF_HOME", "/custom/place")
	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	if got != "/custom/place" {
		t.Errorf("Dir() = %q, want /custom/place", got)
	}
}

func TestDirFallsBackToHomeDotDir(t *testing.T) {
	t.Setenv("CHAIO_CREWCHIEF_HOME", "")
	t.Setenv("HOME", "/home/tester")
	got, err := Dir()
	if err != nil {
		t.Fatalf("Dir() error = %v", err)
	}
	want := filepath.Join("/home/tester", ".chaio-crewchief")
	if got != want {
		t.Errorf("Dir() = %q, want %q", got, want)
	}
}

func TestResolveInBuildsAllPaths(t *testing.T) {
	p := ResolveIn("/h")
	cases := map[string]string{
		"Home":    "/h",
		"Models":  "/h/models.yaml",
		"Rates":   "/h/rates.yaml",
		"Routing": "/h/routing.yaml",
		"DB":      "/h/chaio-crewchief.db",
		"Archive": "/h/archive",
		"Locks":   "/h/locks",
	}
	got := map[string]string{
		"Home": p.Home, "Models": p.Models, "Rates": p.Rates,
		"Routing": p.Routing, "DB": p.DB, "Archive": p.Archive, "Locks": p.Locks,
	}
	for field, want := range cases {
		if got[field] != want {
			t.Errorf("%s = %q, want %q", field, got[field], want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd gateway && go test ./internal/chome/ -v`
Expected: FAIL — the package does not build, `undefined: Dir`.

- [ ] **Step 3: Write the implementation**

Create `gateway/internal/chome/chome.go`:

```go
// Package chome resolves where Crew Chief keeps its files when nobody passed
// explicit paths.
//
// `serve` takes every path as a flag, which works for a systemd unit with a
// WorkingDirectory. The MCP server has no such luxury: Claude Code launches it
// with an arbitrary working directory, so "./models.yaml" means nothing. This
// package is the answer to "then where?", and it is deliberately the only
// answer, so the CLI and the MCP server can never disagree.
package chome

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvHome overrides the default location entirely.
const EnvHome = "CHAIO_CREWCHIEF_HOME"

// dirName is the default directory, created under the user's home.
//
// A single directory holding both config and ledger is a deliberate choice
// over the XDG config/data split: the split is correct about filesystem
// taxonomy and wrong about what people do, which is go look at their files in
// one place. EnvHome exists for anyone who disagrees.
const dirName = ".chaio-crewchief"

// Paths are the files and directories Crew Chief uses inside a home.
type Paths struct {
	Home    string
	Models  string
	Rates   string
	Routing string
	DB      string
	Archive string
	Locks   string
}

// Dir returns the resolved home directory. It does not create it.
func Dir() (string, error) {
	if v := os.Getenv(EnvHome); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory (set %s to override): %w", EnvHome, err)
	}
	return filepath.Join(home, dirName), nil
}

// ResolveIn builds the paths under an explicit home directory.
func ResolveIn(home string) Paths {
	return Paths{
		Home:    home,
		Models:  filepath.Join(home, "models.yaml"),
		Rates:   filepath.Join(home, "rates.yaml"),
		Routing: filepath.Join(home, "routing.yaml"),
		DB:      filepath.Join(home, "chaio-crewchief.db"),
		Archive: filepath.Join(home, "archive"),
		Locks:   filepath.Join(home, "locks"),
	}
}

// Resolve builds the paths under the resolved home directory.
func Resolve() (Paths, error) {
	home, err := Dir()
	if err != nil {
		return Paths{}, err
	}
	return ResolveIn(home), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd gateway && go test ./internal/chome/ -v && go vet ./internal/chome/`
Expected: three PASS, vet clean.

- [ ] **Step 5: Commit**

```bash
git add gateway/internal/chome
git commit -m "feat: add chome for home-directory path resolution

The MCP server is launched with an arbitrary working directory, so the
relative path defaults serve() uses are meaningless there. One resolver so
the CLI and MCP server cannot disagree about where files live.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Distinguish an absent registry from a malformed one

**Files:**
- Modify: `gateway/internal/registry/registry.go:129-148` (`LoadRegistry`)
- Test: `gateway/internal/registry/registry_test.go` (append)

**Interfaces:**
- Consumes: nothing.
- Produces: `registry.ErrNotFound` (an `error` value), and `LoadRegistry` wrapping it so `errors.Is(err, registry.ErrNotFound)` is true when the file does not exist. `registry.ErrInvalidYAML` already exists and keeps its current behavior.

Embedded mode must tolerate a missing `models.yaml` (a fresh install has none) but must still fail loudly on a malformed one — silently reading a YAML typo as "no models configured" would be worse than failing. `LoadRegistry` currently wraps every `os.ReadFile` error as `"failed to read file"`, so the caller cannot tell the two apart. That is what this task fixes.

- [ ] **Step 1: Write the failing test**

Append to `gateway/internal/registry/registry_test.go`:

```go
func TestLoadRegistryMissingFileIsDistinguishable(t *testing.T) {
	_, err := LoadRegistry(filepath.Join(t.TempDir(), "absent.yaml"))
	if err == nil {
		t.Fatal("LoadRegistry(absent) = nil error, want ErrNotFound")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("errors.Is(err, ErrNotFound) = false; err = %v", err)
	}
}

func TestLoadRegistryMalformedIsNotNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.yaml")
	if err := os.WriteFile(path, []byte("models: [oh no: :"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadRegistry(path)
	if err == nil {
		t.Fatal("LoadRegistry(malformed) = nil error, want an error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("malformed YAML reported as ErrNotFound; it must stay a loud failure")
	}
}
```

Ensure the file's import block includes `errors`, `os`, and `path/filepath`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd gateway && go test ./internal/registry/ -run 'TestLoadRegistry(Missing|Malformed)' -v`
Expected: FAIL — `undefined: ErrNotFound`.

- [ ] **Step 3: Write the implementation**

In `gateway/internal/registry/registry.go`, add near the existing `ErrInvalidYAML` declaration:

```go
// ErrNotFound reports that the registry file does not exist, as distinct from
// existing and being unreadable or malformed. Embedded mode tolerates a
// missing models.yaml — a fresh install has none — but a malformed one must
// stay a loud failure, because silently treating a YAML typo as "no models
// configured" would send the user hunting in the wrong place.
var ErrNotFound = errors.New("registry file not found")
```

Replace the `os.ReadFile` error branch in `LoadRegistry`:

```go
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
```

Add `"errors"` and `"io/fs"` to the imports if not already present.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd gateway && go test ./internal/registry/ -v && go vet ./internal/registry/`
Expected: all PASS, including the pre-existing tests. Nothing about the malformed or valid paths changes.

- [ ] **Step 5: Commit**

```bash
git add gateway/internal/registry
git commit -m "feat: distinguish a missing registry file from a malformed one

Embedded mode must start with no models.yaml — a fresh install has none —
while a malformed file stays a loud failure. LoadRegistry wrapped both as
one opaque read error, so no caller could tell them apart.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Process-liveness ownership via flock

**Files:**
- Create: `gateway/internal/ownership/ownership.go`
- Test: `gateway/internal/ownership/ownership_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `ownership.Acquire(lockDir string) (*Owner, error)`; `(*Owner).PID() int`; `(*Owner).Release() error`; `ownership.OwnerAlive(lockDir string, pid int) (bool, error)`; `ownership.EnsureDir(lockDir string) error`; `ownership.Host() string`.

Why a lock file rather than checking the PID: PIDs get reused, so a dead owner's number can belong to an unrelated live process, and reaping would then skip a genuinely orphaned row forever. Comparing process start times would settle it, but that is `/proc/<pid>/stat` on Linux and a `KERN_PROC` sysctl on macOS — per-OS code or a new dependency, for a question `flock` answers exactly. A live process holds its lock; when it dies for any reason, including SIGKILL, the kernel releases it. If we can take the lock, the owner is gone.

- [ ] **Step 1: Write the failing test**

Create `gateway/internal/ownership/ownership_test.go`:

```go
package ownership

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAcquireCreatesLockAndSelfIsAlive(t *testing.T) {
	dir := t.TempDir()
	o, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer o.Release()

	if o.PID() != os.Getpid() {
		t.Errorf("PID() = %d, want %d", o.PID(), os.Getpid())
	}
	alive, err := OwnerAlive(dir, o.PID())
	if err != nil {
		t.Fatalf("OwnerAlive() error = %v", err)
	}
	if !alive {
		t.Error("OwnerAlive() = false for a lock this process holds; flock must conflict across fds")
	}
}

func TestOwnerAliveFalseAfterRelease(t *testing.T) {
	dir := t.TempDir()
	o, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	pid := o.PID()
	if err := o.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	alive, err := OwnerAlive(dir, pid)
	if err != nil {
		t.Fatalf("OwnerAlive() error = %v", err)
	}
	if alive {
		t.Error("OwnerAlive() = true after Release; a released lock means the owner is gone")
	}
}

func TestOwnerAliveFalseForUnknownPID(t *testing.T) {
	dir := t.TempDir()
	alive, err := OwnerAlive(dir, 999999)
	if err != nil {
		t.Fatalf("OwnerAlive() error = %v", err)
	}
	if alive {
		t.Error("OwnerAlive() = true with no lock file present, want false")
	}
}

// A lock held by a *different* live process must read as alive. This is the
// case that protects a sibling Claude Code session's in-flight row.
func TestOwnerAliveTrueForLiveExternalProcess(t *testing.T) {
	dir := t.TempDir()
	helper := exec.Command(os.Args[0], "-test.run=TestHelperHoldsLock")
	helper.Env = append(os.Environ(), "OWNERSHIP_HELPER=1", "OWNERSHIP_LOCKDIR="+dir)
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = helper.Process.Kill()
		_ = helper.Wait()
	}()

	// The helper prints one line as soon as it holds the lock.
	buf := make([]byte, 64)
	n, err := stdout.Read(buf)
	if err != nil || n == 0 {
		t.Fatalf("helper never reported readiness: n=%d err=%v", n, err)
	}

	alive, err := OwnerAlive(dir, helper.Process.Pid)
	if err != nil {
		t.Fatalf("OwnerAlive() error = %v", err)
	}
	if !alive {
		t.Error("OwnerAlive() = false while another process holds the lock")
	}
}

// TestHelperHoldsLock is not a real test. It runs as a subprocess, takes the
// lock, announces itself, and blocks until killed.
func TestHelperHoldsLock(t *testing.T) {
	if os.Getenv("OWNERSHIP_HELPER") != "1" {
		t.Skip("helper process only")
	}
	o, err := Acquire(os.Getenv("OWNERSHIP_LOCKDIR"))
	if err != nil {
		t.Fatalf("helper Acquire() error = %v", err)
	}
	defer o.Release()
	os.Stdout.WriteString("ready\n")
	select {} // blocked until the parent kills us
}

func TestReleaseRemovesLockFile(t *testing.T) {
	dir := t.TempDir()
	o, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	path := filepath.Join(dir, lockName(o.PID()))
	if err := o.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("lock file still present after Release: stat err = %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd gateway && go test ./internal/ownership/ -v`
Expected: FAIL — package does not build, `undefined: Acquire`.

- [ ] **Step 3: Write the implementation**

Create `gateway/internal/ownership/ownership.go`:

```go
// Package ownership answers one question: is the process that wrote this row
// still running?
//
// It matters because a delegation left in flight by a process that died — a
// SIGKILLed daemon, a Claude Code session the user closed — leaves a request
// stuck in "running" forever, and the next session would report that stale
// status as though the work were still happening.
//
// The obvious implementation, storing a PID and checking whether it exists, is
// wrong: PIDs are reused, so a dead owner's number can belong to an unrelated
// live process and the orphan is never cleaned up. Comparing process start
// times would fix that, but obtaining one portably means /proc on Linux and a
// KERN_PROC sysctl on macOS. A lock file answers the same question exactly and
// costs nothing: the kernel releases an flock when the holder dies, however it
// dies. If we can take the lock, nobody owns it.
package ownership

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Owner is a held lock representing this process's claim. Release it on
// shutdown; if the process dies first, the kernel does it.
type Owner struct {
	pid  int
	path string
	f    *os.File
}

func lockName(pid int) string { return fmt.Sprintf("%d.lock", pid) }

// Host returns this machine's hostname, used to scope reaping. Rows written on
// another machine are never reaped: a shared or copied ledger must not have a
// laptop declaring a server's live rows dead.
func Host() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}

// Acquire takes this process's lock inside lockDir, creating the directory if
// needed.
func Acquire(lockDir string) (*Owner, error) {
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	pid := os.Getpid()
	path := filepath.Join(lockDir, lockName(pid))

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		// A PID's lock is already held: the number was reused while a stale
		// lock file lingered. Nothing here is safe to assume, so refuse.
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return &Owner{pid: pid, path: path, f: f}, nil
}

// PID is the process id recorded with rows this owner writes.
func (o *Owner) PID() int { return o.pid }

// Release drops the lock and removes the file.
func (o *Owner) Release() error {
	if o == nil || o.f == nil {
		return nil
	}
	_ = syscall.Flock(int(o.f.Fd()), syscall.LOCK_UN)
	err := o.f.Close()
	o.f = nil
	if rmErr := os.Remove(o.path); rmErr != nil && !os.IsNotExist(rmErr) && err == nil {
		err = rmErr
	}
	return err
}

// OwnerAlive reports whether the process that recorded pid still holds its
// lock. A missing lock file means the owner is gone and cleaned up after
// itself. Anything ambiguous returns true, because leaving a stale row alone
// is a smaller harm than failing a live one.
func OwnerAlive(lockDir string, pid int) (bool, error) {
	path := filepath.Join(lockDir, lockName(pid))
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return true, fmt.Errorf("open lock file: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true, nil // held by a live process
	}
	// We took it, so nobody owned it. Drop it again and remove the stale file.
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = os.Remove(path)
	return false, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd gateway && go test -race ./internal/ownership/ -v && go vet ./internal/ownership/`
Expected: all PASS. `TestHelperHoldsLock` reports SKIP in the parent run — that is correct, it only executes as a subprocess.

- [ ] **Step 5: Commit**

```bash
git add gateway/internal/ownership
git commit -m "feat: add flock-based process ownership for orphan detection

A request left running by a process that died stays running forever. A
stored PID cannot settle liveness because PIDs are reused; process start
times are not portably readable without per-OS code. The kernel releases an
flock when its holder dies, which answers the question exactly.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Owner columns and orphan reaping in the store

**Files:**
- Modify: `gateway/internal/store/sqlite.go` (schema DDL ~line 17-29, `migrations` var ~line 57, new methods after `UpdateRequestStatus`)
- Test: `gateway/internal/store/sqlite_test.go` (append)

**Interfaces:**
- Consumes: `ownership.OwnerAlive`, `ownership.Host` (Task 3).
- Produces: `(*SQLite).AssumeOwnership(pid int, host string)`, `(*SQLite).SetRequestOwner(ctx context.Context, id string, pid int, host string) error`, and `(*SQLite).ReapOrphans(ctx context.Context, lockDir, host string) (int, error)`, returning the number of rows failed.

`AssumeOwnership` is what makes reaping work in production. `ReapOrphans` only considers rows with a recorded owner, and the engine calls `RecordRequest` without knowing anything about processes — so without this, the owner columns stay zero and reaping silently never fires. Telling the store once who it belongs to, and having `RecordRequest` stamp every row, keeps process identity out of the engine entirely.

Note the correction to the design document: reaping targets the **`requests`** table, not `attempts`. `attempts` has no status column — it records `verdict` after an attempt completes — while `StatusRunning` lives on `requests`. The owner columns therefore go on `requests`.

- [ ] **Step 1: Write the failing test**

Append to `gateway/internal/store/sqlite_test.go`:

```go
func TestReapOrphansOnlyFailsDeadLocalRows(t *testing.T) {
	dir := t.TempDir()
	lockDir := filepath.Join(dir, "locks")
	st, err := Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	host := ownership.Host()

	// A live owner: this process, holding its lock.
	live, err := ownership.Acquire(lockDir)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer live.Release()

	seed := func(id string, pid int, h string) {
		if err := st.RecordRequest(ctx, id, types.DelegateRequest{Task: "t"}, types.StatusRunning); err != nil {
			t.Fatalf("RecordRequest(%s) error = %v", id, err)
		}
		if err := st.SetRequestOwner(ctx, id, pid, h); err != nil {
			t.Fatalf("SetRequestOwner(%s) error = %v", id, err)
		}
	}
	seed("dead-local", 999999, host)     // no lock file -> orphan
	seed("live-local", live.PID(), host) // lock held -> must survive
	seed("other-host", 999998, "some-other-box")
	// A finished row must never be touched regardless of owner.
	if err := st.RecordRequest(ctx, "done", types.DelegateRequest{Task: "t"}, types.StatusDelivered); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRequestOwner(ctx, "done", 999997, host); err != nil {
		t.Fatal(err)
	}

	n, err := st.ReapOrphans(ctx, lockDir, host)
	if err != nil {
		t.Fatalf("ReapOrphans() error = %v", err)
	}
	if n != 1 {
		t.Errorf("ReapOrphans() reaped %d rows, want 1", n)
	}

	want := map[string]types.DelegateStatus{
		"dead-local": types.StatusFailed,
		"live-local": types.StatusRunning,
		"other-host": types.StatusRunning,
		"done":       types.StatusDelivered,
	}
	for id, wantStatus := range want {
		rec, ok, err := st.GetRequest(ctx, id)
		if err != nil || !ok {
			t.Fatalf("GetRequest(%s) ok=%v err=%v", id, ok, err)
		}
		if rec.Status != wantStatus {
			t.Errorf("%s status = %q, want %q", id, rec.Status, wantStatus)
		}
	}
}

func TestReapOrphansIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	lockDir := filepath.Join(dir, "locks")
	st, err := Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()
	ctx := context.Background()
	host := ownership.Host()

	if err := st.RecordRequest(ctx, "x", types.DelegateRequest{Task: "t"}, types.StatusRunning); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRequestOwner(ctx, "x", 999999, host); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReapOrphans(ctx, lockDir, host); err != nil {
		t.Fatal(err)
	}
	n, err := st.ReapOrphans(ctx, lockDir, host)
	if err != nil {
		t.Fatalf("second ReapOrphans() error = %v", err)
	}
	if n != 0 {
		t.Errorf("second ReapOrphans() reaped %d rows, want 0", n)
	}
}

// Rows written before the owner columns existed have no owner. They must not
// be reaped on the strength of a zero PID.
func TestReapOrphansSkipsRowsWithNoRecordedOwner(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	if err := st.RecordRequest(ctx, "legacy", types.DelegateRequest{Task: "t"}, types.StatusRunning); err != nil {
		t.Fatal(err)
	}
	n, err := st.ReapOrphans(ctx, filepath.Join(dir, "locks"), ownership.Host())
	if err != nil {
		t.Fatalf("ReapOrphans() error = %v", err)
	}
	if n != 0 {
		t.Errorf("ReapOrphans() reaped %d unowned rows, want 0", n)
	}
}
```

Ensure the test file imports `context`, `path/filepath`, `testing`, the `ownership` package, and `types`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd gateway && go test ./internal/store/ -run TestReapOrphans -v`
Expected: FAIL — `undefined: SetRequestOwner`, `undefined: ReapOrphans`.

- [ ] **Step 3: Write the implementation**

In `gateway/internal/store/sqlite.go`, add the three columns to the `requests` table in `schemaDDL` so fresh databases have them:

```sql
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
```

Append migration 4 to the `migrations` slice, following the duplicate-column pattern migration 3 already established (a fresh database gets these columns from the DDL above, so the ALTERs no-op there):

```go
	{
		version: 4, // request ownership, for orphan reaping
		up: func(db *sql.DB) error {
			for _, stmt := range []string{
				`ALTER TABLE requests ADD COLUMN owner_pid INTEGER NOT NULL DEFAULT 0`,
				`ALTER TABLE requests ADD COLUMN owner_host TEXT NOT NULL DEFAULT ''`,
			} {
				if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
					return fmt.Errorf("add owner columns: %w", err)
				}
			}
			return nil
		},
	},
```

Add the two methods after `UpdateRequestStatus`:

```go
// AssumeOwnership tells the store which process owns the requests it records,
// so RecordRequest can stamp every row without the engine knowing anything
// about processes. Call it once, right after Open. A store that was never told
// records no owner, and rows without one are never reaped.
func (s *SQLite) AssumeOwnership(pid int, host string) {
	s.ownerPID, s.ownerHost = pid, host
}

// SetRequestOwner records which process is working a request, so a later run
// can tell an orphan from work still in flight.
func (s *SQLite) SetRequestOwner(ctx context.Context, id string, pid int, host string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE requests SET owner_pid = ?, owner_host = ? WHERE id = ?`, pid, host, id)
	return err
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
```

Add the two owner fields to the `SQLite` struct:

```go
type SQLite struct {
	db        *sql.DB
	ownerPID  int
	ownerHost string
}
```

And have `RecordRequest` stamp them. Extend its existing INSERT to include the two columns, passing `s.ownerPID` and `s.ownerHost`. A store that was never given an owner writes `0` and `""`, which `ReapOrphans` skips by design.

Add the `ownership` package to the file's imports.

- [ ] **Step 4: Write the failing test for automatic stamping**

Append to `gateway/internal/store/sqlite_test.go`:

```go
// Without this, ReapOrphans silently never fires in production: the engine
// records requests knowing nothing about processes, so every row would carry
// owner_pid 0 and be skipped.
func TestRecordRequestStampsAssumedOwner(t *testing.T) {
	dir := t.TempDir()
	lockDir := filepath.Join(dir, "locks")
	st, err := Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer st.Close()
	ctx := context.Background()
	host := ownership.Host()

	st.AssumeOwnership(999999, host) // a PID with no lock: an orphan by construction
	if err := st.RecordRequest(ctx, "stamped", types.DelegateRequest{Task: "t"}, types.StatusRunning); err != nil {
		t.Fatal(err)
	}

	n, err := st.ReapOrphans(ctx, lockDir, host)
	if err != nil {
		t.Fatalf("ReapOrphans() error = %v", err)
	}
	if n != 1 {
		t.Fatalf("ReapOrphans() = %d, want 1; RecordRequest did not stamp the owner", n)
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd gateway && go test -race ./internal/store/ -v && go vet ./internal/store/`
Expected: all PASS, including every pre-existing store test. The migration must not disturb them.

- [ ] **Step 6: Verify the migration works on an existing database**

Run:

```bash
cd gateway && cat > /tmp/migcheck.sh <<'SH'
set -e
rm -rf /tmp/migtest && mkdir -p /tmp/migtest
git stash list >/dev/null
SH
sh /tmp/migcheck.sh
git stash push -q gateway/internal/store/sqlite.go
go run ./cmd/chaio-crewchief serve --db /tmp/migtest/old.db --models /dev/null --archive /tmp/migtest/a --addr 127.0.0.1:18199 &
sleep 2; kill %1 || true
git stash pop -q
go test ./internal/store/ -count=1
```

Expected: the pre-owner-column database opens cleanly under the new code. If `serve` refuses `--models /dev/null`, create a minimal valid `models.yaml` in `/tmp/migtest` and point at that instead — the goal is only to produce a database at the old schema version.

- [ ] **Step 7: Commit**

```bash
git add gateway/internal/store
git commit -m "feat: record request ownership and reap orphaned running rows

A process killed mid-delegation leaves a request in running forever, and a
later session reports that stale status as live work. Reaping is scoped to
this host and to rows with a recorded owner, so a sibling session's in-flight
work and a remote gateway's rows are never touched.

Corrects the design doc, which said attempts: attempts has no status column,
so running lives on requests and the owner columns go there.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: The embed package

**Files:**
- Create: `gateway/internal/embed/embed.go`
- Test: `gateway/internal/embed/embed_test.go`

**Interfaces:**
- Consumes: `chome.Paths` (Task 1), `registry.ErrNotFound` (Task 2), `ownership.Acquire`/`Host` (Task 3), `(*store.SQLite).ReapOrphans` (Task 4), and the existing `registry.LoadRegistry`, `rates.Load`, `store.Open`, `archive.New`, `routing.Load`, `provider.New`, `engine.NewWithRouter`, `server.New`.
- Produces: `embed.Config{Paths chome.Paths}`, `embed.Instance{BaseURL string, Owner *ownership.Owner, ModelsConfigured bool}`, `(*Instance).Close() error`, and `embed.Start(ctx context.Context, cfg Config) (*Instance, error)`.

Note that `Start` does **not** launch the registry and rates hot-reload watchers `serve()` runs. Those exist so a long-lived daemon picks up config edits; a process that lives for one Claude Code session does not need them, and they would cost two goroutines and two fsnotify watchers per session.

`Start` fails on exactly two conditions — an unreadable store, and a malformed (not missing) config file. Every other condition yields a running server that explains itself through tool responses, because a startup failure reaches the user as an opaque "MCP server failed to start" with nothing to act on.

- [ ] **Step 1: Write the failing test**

Create `gateway/internal/embed/embed_test.go`:

```go
package embed

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/chome"
)

func startIn(t *testing.T, home string) *Instance {
	t.Helper()
	inst, err := Start(context.Background(), Config{Paths: chome.ResolveIn(home)})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { inst.Close() })
	return inst
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s error = %v", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(b)
}

func TestStartWithNoConfigServesHealth(t *testing.T) {
	inst := startIn(t, t.TempDir())

	if !strings.HasPrefix(inst.BaseURL, "http://127.0.0.1:") {
		t.Errorf("BaseURL = %q, want a loopback URL", inst.BaseURL)
	}
	code, _ := get(t, inst.BaseURL+"/health")
	if code != http.StatusOK {
		t.Errorf("GET /health = %d, want 200", code)
	}
}

func TestStartWithNoConfigHasEmptyRoster(t *testing.T) {
	inst := startIn(t, t.TempDir())
	code, body := get(t, inst.BaseURL+"/models")
	if code != http.StatusOK {
		t.Fatalf("GET /models = %d, want 200", code)
	}
	var models []any
	if err := json.Unmarshal([]byte(body), &models); err != nil {
		// The handler may wrap the list; assert on content instead of shape.
		if strings.Contains(body, "\"name\"") {
			t.Errorf("GET /models returned presets with no models.yaml: %s", body)
		}
		return
	}
	if len(models) != 0 {
		t.Errorf("GET /models returned %d presets with no models.yaml, want 0", len(models))
	}
}

func TestStartCreatesHomeAndArchive(t *testing.T) {
	home := filepath.Join(t.TempDir(), "nested", "home")
	startIn(t, home)

	for _, p := range []string{home, filepath.Join(home, "archive"), filepath.Join(home, "locks")} {
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			t.Errorf("%s not created as a directory: err = %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "models.yaml")); !os.IsNotExist(err) {
		t.Error("Start created a models.yaml; it must never write config")
	}
}

func TestStartFailsOnMalformedRegistry(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "models.yaml"), []byte("models: [oh no: :"), 0o600); err != nil {
		t.Fatal(err)
	}
	inst, err := Start(context.Background(), Config{Paths: chome.ResolveIn(home)})
	if err == nil {
		inst.Close()
		t.Fatal("Start() with malformed models.yaml = nil error, want failure")
	}
}

func TestCloseReleasesPortAndIsIdempotent(t *testing.T) {
	inst := startIn(t, t.TempDir())
	url := inst.BaseURL
	if err := inst.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := http.Get(url + "/health"); err == nil {
		t.Error("server still answering after Close()")
	}
	if err := inst.Close(); err != nil {
		t.Errorf("second Close() error = %v, want nil", err)
	}
}

func TestEachStartGetsItsOwnPort(t *testing.T) {
	a := startIn(t, t.TempDir())
	b := startIn(t, t.TempDir())
	if a.BaseURL == b.BaseURL {
		t.Errorf("both instances bound %s; ports must be kernel-assigned", a.BaseURL)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd gateway && go test ./internal/embed/ -v`
Expected: FAIL — package does not build, `undefined: Start`.

- [ ] **Step 3: Write the implementation**

Create `gateway/internal/embed/embed.go`:

```go
// Package embed runs Crew Chief inside the calling process.
//
// `chaio-crewchief mcp` was a pure HTTP proxy, so the plugin registered fine
// and then failed every tool call unless the user happened to be running
// `serve` somewhere. Nothing in the install path said so. Embedded mode is the
// default answer: the same components, wired the same way, served on an
// ephemeral loopback port that dies with the process.
//
// It reuses internal/server rather than calling the engine directly so that
// both modes share one set of handlers. Tool behavior is then identical by
// construction instead of by discipline — and the /stats and /history
// aggregation, which is where a divergence would quietly make `usage` and
// `crewchief_stats` disagree about money, exists exactly once.
//
// The cost is a real listener. It binds 127.0.0.1 on a kernel-assigned port
// with no authentication, so any local process can reach it while it is up.
// That is documented in README and SECURITY rather than hidden.
package embed

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/archive"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/chome"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/engine"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/ownership"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/provider"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/rates"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/registry"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/routing"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/server"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/store"
	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/types"
)

// Config selects where the embedded instance keeps its files.
type Config struct {
	Paths chome.Paths
}

// Instance is a running embedded gateway.
type Instance struct {
	// BaseURL is the loopback address of the in-process server.
	BaseURL string
	// Owner is this process's ownership lock, held for the instance's life.
	Owner *ownership.Owner
	// ModelsConfigured reports whether a registry was loaded. False means the
	// server is running but delegation will refuse with guidance.
	ModelsConfigured bool

	srv    *http.Server
	st     *store.SQLite
	closed bool
}

// emptyRegistry stands in for a missing models.yaml. A fresh install has none,
// and failing to start would surface to Claude Code as an opaque "server
// failed to start" with nothing actionable in it. Starting empty lets health,
// models, and delegate each answer with the path to fix.
type emptyRegistry struct{}

func (emptyRegistry) Get(string) (types.Preset, bool) { return types.Preset{}, false }
func (emptyRegistry) Default() (types.Preset, bool)   { return types.Preset{}, false }
func (emptyRegistry) List() []types.Preset            { return nil }

// Start wires the components and serves them on a loopback port.
//
// It fails on exactly two things: a store it cannot open, and a config file
// that exists but is malformed. A missing config is not an error; a malformed
// one must never be read as "nothing configured", because that sends the user
// hunting in the wrong place for a YAML typo.
func Start(ctx context.Context, cfg Config) (*Instance, error) {
	p := cfg.Paths
	if err := os.MkdirAll(p.Home, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", p.Home, err)
	}

	reg, configured, err := loadRegistry(p.Models)
	if err != nil {
		return nil, err
	}

	rt, err := rates.Load(p.Rates)
	if err != nil {
		return nil, fmt.Errorf("load rates %s: %w", p.Rates, err)
	}

	router, err := routing.Load(p.Routing)
	if err != nil {
		return nil, fmt.Errorf("load routing %s: %w", p.Routing, err)
	}

	st, err := store.Open(p.DB)
	if err != nil {
		return nil, fmt.Errorf("open store %s: %w", p.DB, err)
	}

	// Reap BEFORE acquiring, and create the lock directory first. Lock files
	// outlive their processes and pids get reused — realistically after a
	// reboot, which reallocates low ones — so acquiring first can leave this
	// process holding the lock file of a dead owner whose pid it drew. That
	// owner's abandoned rows then read as live and stay `running` forever,
	// unfixable by any later run. And OwnerAlive stats the lock directory and
	// returns "assume alive" when it is missing, so without EnsureDir nothing
	// is ever reaped.
	if err := ownership.EnsureDir(p.Locks); err != nil {
		st.Close()
		return nil, fmt.Errorf("prepare lock directory: %w", err)
	}
	if n, err := st.ReapOrphans(ctx, p.Locks, ownership.Host()); err != nil {
		log.Printf("warning: reaping orphaned requests failed: %v", err)
	} else if n > 0 {
		log.Printf("failed %d orphaned request(s) left by exited processes", n)
	}

	owner, err := ownership.Acquire(p.Locks)
	if err != nil {
		st.Close()
		return nil, fmt.Errorf("acquire ownership lock: %w", err)
	}
	// Every request this instance records is stamped with this owner, so a
	// later run can tell work we abandoned from work still in flight.
	st.AssumeOwnership(owner.PID(), ownership.Host())

	arch, err := archive.New(p.Archive)
	if err != nil {
		owner.Release()
		st.Close()
		return nil, fmt.Errorf("open archive %s: %w", p.Archive, err)
	}

	eng := engine.NewWithRouter(reg, router, provider.New(), st, arch, rt)
	handler := server.New(eng, st, reg, arch, rt)

	// Port 0: the kernel picks a free port, so concurrent sessions never
	// collide and nothing has to be configured.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		owner.Release()
		st.Close()
		return nil, fmt.Errorf("listen on loopback: %w", err)
	}

	srv := &http.Server{Handler: handler}
	inst := &Instance{
		BaseURL:          "http://" + ln.Addr().String(),
		Owner:            owner,
		ModelsConfigured: configured,
		srv:              srv,
		st:               st,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("embedded server stopped: %v", err)
		}
	}()
	return inst, nil
}

// loadRegistry returns the registry and whether one was actually configured.
func loadRegistry(path string) (types.Registry, bool, error) {
	reg, err := registry.LoadRegistry(path)
	if err == nil {
		return reg, true, nil
	}
	if errors.Is(err, registry.ErrNotFound) {
		return emptyRegistry{}, false, nil
	}
	return nil, false, fmt.Errorf("load registry %s: %w", path, err)
}

// Close shuts the server, releases the ownership lock, and closes the store.
// It is safe to call more than once.
func (i *Instance) Close() error {
	if i == nil || i.closed {
		return nil
	}
	i.closed = true

	var firstErr error
	if err := i.srv.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		firstErr = err
	}
	if err := i.Owner.Release(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := i.st.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
```

If `rates.Load` or `routing.Load` returns an error for a missing file rather than an empty table, mirror the registry treatment: tolerate absent, fail on malformed. Check their behavior before assuming — `serve()` already calls both with paths that commonly do not exist, and it only warns, so they most likely tolerate absence already.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd gateway && go test -race ./internal/embed/ -v && go vet ./internal/embed/`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add gateway/internal/embed
git commit -m "feat: add embed for running the gateway in-process

Wires the same components serve() does and serves internal/server on a
kernel-assigned loopback port, so embedded and gateway modes share one set of
handlers and cannot drift. Starts successfully with no models.yaml, since a
startup failure reaches Claude Code as an opaque error with nothing to act on.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Mode resolution

**Files:**
- Modify: `gateway/internal/gwurl/gwurl.go`
- Test: `gateway/internal/gwurl/gwurl_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `gwurl.Mode` (a string type) with constants `gwurl.ModeGateway = "gateway"` and `gwurl.ModeEmbedded = "embedded"`; `gwurl.Resolve() (Mode, string)` returning the mode and the gateway URL (empty in embedded mode); `gwurl.URLFromEnv() string`. **`gwurl.DefaultURL` and `gwurl.URL()` are removed.**

This is the behavior change: an unset `CHAIO_CREWCHIEF_URL` no longer means "proxy to localhost:8181", it means "run embedded". The old default was the worst of the three options — it produced a plugin that looked installed and failed every call.

- [ ] **Step 1: Write the failing test**

Replace the contents of `gateway/internal/gwurl/gwurl_test.go` with:

```go
package gwurl

import "testing"

func TestResolve(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		wantMode Mode
		wantURL  string
	}{
		{
			name:     "no variables set means embedded",
			env:      map[string]string{},
			wantMode: ModeEmbedded,
			wantURL:  "",
		},
		{
			name:     "current variable selects gateway",
			env:      map[string]string{"CHAIO_CREWCHIEF_URL": "http://gx10:8181"},
			wantMode: ModeGateway,
			wantURL:  "http://gx10:8181",
		},
		{
			name:     "legacy CREWCHIEF_URL still works",
			env:      map[string]string{"CREWCHIEF_URL": "http://old:8181"},
			wantMode: ModeGateway,
			wantURL:  "http://old:8181",
		},
		{
			name:     "legacy DISPATCH_URL still works",
			env:      map[string]string{"DISPATCH_URL": "http://older:8181"},
			wantMode: ModeGateway,
			wantURL:  "http://older:8181",
		},
		{
			name: "current variable wins over legacy",
			env: map[string]string{
				"CHAIO_CREWCHIEF_URL": "http://new:8181",
				"DISPATCH_URL":        "http://older:8181",
			},
			wantMode: ModeGateway,
			wantURL:  "http://new:8181",
		},
		{
			name:     "empty value is treated as unset",
			env:      map[string]string{"CHAIO_CREWCHIEF_URL": ""},
			wantMode: ModeEmbedded,
			wantURL:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, k := range envKeys {
				t.Setenv(k, "")
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			mode, url := Resolve()
			if mode != tt.wantMode {
				t.Errorf("mode = %q, want %q", mode, tt.wantMode)
			}
			if url != tt.wantURL {
				t.Errorf("url = %q, want %q", url, tt.wantURL)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd gateway && go test ./internal/gwurl/ -v`
Expected: FAIL — `undefined: Mode`, `undefined: Resolve`.

- [ ] **Step 3: Write the implementation**

Replace the body of `gateway/internal/gwurl/gwurl.go` below the package clause:

```go
import "os"

// Mode is how this process reaches a gateway.
type Mode string

const (
	// ModeGateway proxies to a gateway someone else is running.
	ModeGateway Mode = "gateway"
	// ModeEmbedded runs the gateway in this process.
	ModeEmbedded Mode = "embedded"
)

// envKeys are consulted in order, most current name first. The older names
// are honored so configs written under this project's previous names
// (Crew Chief, and Dispatch before that) keep working.
var envKeys = []string{"CHAIO_CREWCHIEF_URL", "CREWCHIEF_URL", "DISPATCH_URL"}

// URLFromEnv returns the first non-empty gateway URL in the environment, or ""
// if none is set.
func URLFromEnv() string {
	for _, key := range envKeys {
		if u := os.Getenv(key); u != "" {
			return u
		}
	}
	return ""
}

// Resolve reports which mode to run in, and the gateway URL when there is one.
//
// An unset variable means embedded, not localhost:8181. That default was the
// worst of the available options: it produced a plugin that registered
// successfully and then failed every call against a port nothing was listening
// on, with nothing anywhere saying a second process was required.
func Resolve() (Mode, string) {
	if u := URLFromEnv(); u != "" {
		return ModeGateway, u
	}
	return ModeEmbedded, ""
}
```

Keep the existing package doc comment. Delete `DefaultURL` and `URL()`.

- [ ] **Step 4: Fix the call sites and run the whole suite**

Run: `cd gateway && go build ./... 2>&1 | head -20`

`mcpserve.GatewayURL`, `internal/cli/doctor.go`, and `internal/cli/usage.go` reference the removed `URL()`. Tasks 7 and 9 rewrite them properly; for now, get the tree compiling by having each call `gwurl.URLFromEnv()`.

Run: `cd gateway && go test -race ./... && go vet ./...`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add gateway/internal/gwurl gateway/internal/mcpserve gateway/internal/cli
git commit -m "feat!: unset gateway URL now means embedded, not localhost:8181

The localhost default produced a plugin that registered successfully and then
failed every tool call against a port nothing was listening on. Unset now
selects embedded mode; set an explicit URL to proxy to a shared gateway.

BREAKING: anyone relying on the implicit localhost:8181 default must now set
CHAIO_CREWCHIEF_URL explicitly.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: MCP server runs against either mode, and refuses embedded async

**Files:**
- Modify: `gateway/internal/mcpserve/mcpserve.go`
- Test: `gateway/internal/mcpserve/mcpserve_test.go` (append)

**Interfaces:**
- Consumes: `gwurl.Resolve` (Task 6).
- Produces: `mcpserve.Options{BaseURL string, Embedded bool, ModelsConfigured bool, ModelsPath string}` and `mcpserve.RunWith(ctx context.Context, version string, opts Options) error`. `Run` is removed; `main.go` calls `RunWith`.

Two behaviors change. Async is refused in embedded mode, because the process dies with the Claude Code session and an accepted async job would simply vanish. And with no `models.yaml`, delegation returns actionable guidance instead of failing obscurely.

- [ ] **Step 1: Write the failing test**

Append to `gateway/internal/mcpserve/mcpserve_test.go`:

```go
func TestDelegateRejectsAsyncWhenEmbedded(t *testing.T) {
	err := checkDelegate(Options{Embedded: true, ModelsConfigured: true}, delegateIn{Task: "t", Async: true})
	if err == nil {
		t.Fatal("async accepted in embedded mode; the process dies with the session")
	}
	for _, want := range []string{"async", "CHAIO_CREWCHIEF_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestDelegateAllowsAsyncWhenGateway(t *testing.T) {
	if err := checkDelegate(Options{Embedded: false, ModelsConfigured: true}, delegateIn{Task: "t", Async: true}); err != nil {
		t.Errorf("async rejected in gateway mode: %v", err)
	}
}

func TestDelegateRejectsWhenNoModelsConfigured(t *testing.T) {
	opts := Options{Embedded: true, ModelsConfigured: false, ModelsPath: "/home/u/.chaio-crewchief/models.yaml"}
	err := checkDelegate(opts, delegateIn{Task: "t"})
	if err == nil {
		t.Fatal("delegate accepted with no models configured")
	}
	for _, want := range []string{"/home/u/.chaio-crewchief/models.yaml", "init"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestDelegateAllowedWhenConfigured(t *testing.T) {
	if err := checkDelegate(Options{Embedded: true, ModelsConfigured: true}, delegateIn{Task: "t"}); err != nil {
		t.Errorf("configured sync delegate rejected: %v", err)
	}
}
```

Ensure the file imports `strings`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd gateway && go test ./internal/mcpserve/ -run TestDelegate -v`
Expected: FAIL — `undefined: checkDelegate`, `undefined: Options`.

- [ ] **Step 3: Write the implementation**

In `gateway/internal/mcpserve/mcpserve.go`, replace the `GatewayURL` function with:

```go
// Options tell the MCP server which gateway to talk to and what it may
// promise. The two modes share these handlers exactly; only these fields
// differ between them.
type Options struct {
	// BaseURL is the gateway to call, embedded or remote.
	BaseURL string
	// Embedded reports that the gateway is in this process and dies with it.
	Embedded bool
	// ModelsConfigured reports whether a registry was loaded.
	ModelsConfigured bool
	// ModelsPath is where a registry should live, named in guidance messages.
	ModelsPath string
}

// checkDelegate rejects delegations this mode cannot honestly perform.
func checkDelegate(opts Options, in delegateIn) error {
	if !opts.ModelsConfigured {
		return fmt.Errorf("no models configured: create %s, or run `chaio-crewchief init` to write a starter file, then restart", opts.ModelsPath)
	}
	if in.Async && opts.Embedded {
		// An embedded gateway dies with the Claude Code session, so an
		// accepted async job would vanish rather than finish. Say so instead
		// of taking work we cannot complete.
		return fmt.Errorf("async delegation needs a gateway that outlives this session: run `chaio-crewchief serve` and set CHAIO_CREWCHIEF_URL to it, or drop async to run this synchronously")
	}
	return nil
}
```

Change `Run` to `RunWith`, taking `Options`:

```go
// RunWith serves the MCP tools over stdio until the client disconnects.
func RunWith(ctx context.Context, version string, opts Options) error {
	c := &client{base: opts.BaseURL, hc: &http.Client{}}
	s := mcp.NewServer(&mcp.Implementation{Name: "chaio-crewchief", Version: version}, nil)
```

Add the guard as the first statement in the `crewchief_delegate` handler:

```go
		func(ctx context.Context, req *mcp.CallToolRequest, in delegateIn) (*mcp.CallToolResult, any, error) {
			if err := checkDelegate(opts, in); err != nil {
				return nil, nil, err
			}
			out, err := c.post(ctx, "/delegate", in, delegateTimeout)
```

Add the same `ModelsConfigured` guidance to the `crewchief_models` handler, returning the message as a normal text result rather than an error — an empty roster is a legitimate answer to "what models are there", and returning guidance text puts the fix in front of the model that asked:

```go
		func(ctx context.Context, req *mcp.CallToolRequest, in emptyIn) (*mcp.CallToolResult, any, error) {
			if !opts.ModelsConfigured {
				return textResult(fmt.Sprintf(
					"No models configured. Create %s, or run `chaio-crewchief init` to write a starter file, then restart this session.",
					opts.ModelsPath)), nil, nil
			}
			out, err := c.get(ctx, "/models", defaultTimeout)
```

Update the package doc comment, which currently claims the package is only a proxy:

```go
// Package mcpserve exposes Crew Chief as a stdio MCP server
// (`chaio-crewchief mcp`). Every tool call maps to one HTTP request against a
// gateway. That gateway is either embedded in this process (the default) or a
// remote one named by CHAIO_CREWCHIEF_URL, so the MCP process can run on a
// laptop while the fleet runs on the box with the GPUs.
//
// The only logic here is refusing what the current mode cannot honestly
// deliver: async when the gateway dies with the session, and delegation when
// no models are configured. Everything else is the gateway's.
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd gateway && go test -race ./internal/mcpserve/ -v && go vet ./internal/mcpserve/`
Expected: all PASS. The build will still fail in `cmd/` until Task 8 — that is expected, and `go test ./internal/...` avoids it.

- [ ] **Step 5: Commit**

```bash
git add gateway/internal/mcpserve
git commit -m "feat: mode-aware MCP server that refuses what it cannot deliver

Embedded async is rejected: the gateway dies with the Claude Code session, so
an accepted job would vanish rather than finish. Delegation with no models
configured returns the path to fix instead of an obscure failure.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: Wire the binary — shared serve wiring, embedded mcp, init

**Files:**
- Modify: `gateway/cmd/chaio-crewchief/main.go`
- Create: `gateway/internal/cli/init.go`
- Test: `gateway/internal/cli/init_test.go`

**Interfaces:**
- Consumes: `chome.Resolve` (Task 1), `embed.Start` (Task 5), `gwurl.Resolve` (Task 6), `mcpserve.RunWith`/`Options` (Task 7).
- Produces: `cli.Init(w io.Writer, args []string) int` and `cli.StarterModelsYAML` (a `string` constant).

- [ ] **Step 1: Write the failing test**

Create `gateway/internal/cli/init_test.go`:

```go
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/registry"
)

func TestInitWritesStarterConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CHAIO_CREWCHIEF_HOME", home)

	var out bytes.Buffer
	if code := Init(&out, nil); code != 0 {
		t.Fatalf("Init() = %d, want 0; output: %s", code, out.String())
	}
	path := filepath.Join(home, "models.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("models.yaml not written: %v", err)
	}
	if !strings.Contains(out.String(), path) {
		t.Errorf("output %q does not name the file it wrote", out.String())
	}
}

// A starter file that the registry rejects is worse than none: the user edits
// it, restarts, and gets a parse error they did not cause.
func TestStarterConfigIsValidRegistryInput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CHAIO_CREWCHIEF_HOME", home)
	if code := Init(&bytes.Buffer{}, nil); code != 0 {
		t.Fatalf("Init() = %d, want 0", code)
	}
	if _, err := registry.LoadRegistry(filepath.Join(home, "models.yaml")); err != nil {
		t.Errorf("LoadRegistry(starter) error = %v; the starter file must parse", err)
	}
}

func TestInitRefusesToOverwrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CHAIO_CREWCHIEF_HOME", home)
	path := filepath.Join(home, "models.yaml")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("models: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if code := Init(&out, nil); code == 0 {
		t.Fatal("Init() = 0 over an existing file, want non-zero")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "models: []\n" {
		t.Error("Init overwrote an existing models.yaml without --force")
	}
	if !strings.Contains(out.String(), "--force") {
		t.Errorf("refusal %q does not mention --force", out.String())
	}
}

func TestInitForceOverwrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CHAIO_CREWCHIEF_HOME", home)
	path := filepath.Join(home, "models.yaml")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("models: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if code := Init(&bytes.Buffer{}, []string{"--force"}); code != 0 {
		t.Fatalf("Init(--force) = %d, want 0", code)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) == "models: []\n" {
		t.Error("Init(--force) did not overwrite")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd gateway && go test ./internal/cli/ -run TestInit -v`
Expected: FAIL — `undefined: Init`.

- [ ] **Step 3: Write the init subcommand**

Create `gateway/internal/cli/init.go`:

```go
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/chome"
)

// StarterModelsYAML is the file `init` writes.
//
// Every preset is commented out, so the file parses as an empty roster until
// the user deliberately enables one. Auto-detecting a running Ollama was
// considered and rejected: it hard-codes one vendor into a deliberately
// vendor-neutral project, and it fails confusingly when Ollama is running with
// no models pulled.
const StarterModelsYAML = `# Crew Chief model roster.
#
# Each entry is a preset Crew Chief can relay a work order to. Any
# OpenAI-compatible chat-completions endpoint works. Uncomment one, point it at
# something real, and restart your Claude Code session.
#
# Crew Chief never judges what a model returns and never picks a model for
# you — it relays the work order and records what it cost.

models: []

# A local Ollama:
#
# models:
#   - name: local
#     base_url: http://localhost:11434/v1
#     model: qwen2.5-coder:7b
#     system_prompt: "You are a precise coding assistant."
#     timeout_sec: 300
#     provider_class: local
#     default: true
#
# Any hosted OpenAI-compatible endpoint. api_key_env names an environment
# variable; never put a key in this file.
#
# models:
#   - name: cloud
#     base_url: https://api.example.com/v1
#     model: some-model
#     api_key_env: EXAMPLE_API_KEY
#     system_prompt: "You are a precise coding assistant."
#     timeout_sec: 120
#     provider_class: cloud
`

// Init writes a starter models.yaml into the resolved home directory.
//
// This is the only code path in the binary that writes configuration, and it
// runs only when invoked by name. Writing config as a side effect of starting
// up would create files in a home directory the user never asked for.
func Init(w io.Writer, args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(w)
	force := fs.Bool("force", false, "overwrite an existing models.yaml")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	paths, err := chome.Resolve()
	if err != nil {
		fmt.Fprintf(w, "error: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(paths.Home, 0o700); err != nil {
		fmt.Fprintf(w, "error: create %s: %v\n", paths.Home, err)
		return 1
	}

	if _, err := os.Stat(paths.Models); err == nil && !*force {
		fmt.Fprintf(w, "%s already exists; not overwriting. Use --force to replace it.\n", paths.Models)
		return 1
	}

	if err := os.WriteFile(paths.Models, []byte(StarterModelsYAML), 0o600); err != nil {
		fmt.Fprintf(w, "error: write %s: %v\n", paths.Models, err)
		return 1
	}

	fmt.Fprintf(w, "Wrote %s\n\nEdit it to add a model, then restart your Claude Code session.\n", paths.Models)
	return 0
}
```

- [ ] **Step 4: Run the init tests**

Run: `cd gateway && go test ./internal/cli/ -run TestInit -v`
Expected: four PASS.

- [ ] **Step 5: Rewire main.go**

In `gateway/cmd/chaio-crewchief/main.go`, replace the `mcp` case in the subcommand switch:

```go
		case "mcp":
			if err := runMCP(); err != nil {
				log.Fatalf("mcp: %v", err)
			}
			return
		case "init":
			os.Exit(cli.Init(os.Stdout, os.Args[2:]))
```

Add `runMCP` below `main`:

```go
// runMCP serves MCP over stdio, against an embedded gateway by default or a
// remote one when CHAIO_CREWCHIEF_URL names it.
//
// Startup errors go to stderr in full, because that is what lands in the MCP
// logs — Claude Code itself shows only "server failed to start".
func runMCP() error {
	ctx := context.Background()
	mode, url := gwurl.Resolve()

	if mode == gwurl.ModeGateway {
		return mcpserve.RunWith(ctx, version, mcpserve.Options{
			BaseURL: url,
			// A remote gateway owns its own roster; asking about it here would
			// mean a second round trip before the first tool call.
			ModelsConfigured: true,
		})
	}

	paths, err := chome.Resolve()
	if err != nil {
		return err
	}
	inst, err := embed.Start(ctx, embed.Config{Paths: paths})
	if err != nil {
		return err
	}
	defer inst.Close()

	return mcpserve.RunWith(ctx, version, mcpserve.Options{
		BaseURL:          inst.BaseURL,
		Embedded:         true,
		ModelsConfigured: inst.ModelsConfigured,
		ModelsPath:       paths.Models,
	})
}
```

Add `chome`, `embed`, and `gwurl` to the imports.

Then make `serve()` reap orphans too, so a SIGKILLed daemon cleans up after itself on restart. Immediately after the existing `store.Open` block:

```go
	owner, err := ownership.Acquire(filepath.Join(filepath.Dir(*dbPath), "locks"))
	if err != nil {
		log.Fatalf("acquire ownership lock: %v", err)
	}
	defer owner.Release()
	st.AssumeOwnership(owner.PID(), ownership.Host())

	if n, err := st.ReapOrphans(context.Background(), filepath.Join(filepath.Dir(*dbPath), "locks"), ownership.Host()); err != nil {
		log.Printf("warning: reaping orphaned requests failed: %v", err)
	} else if n > 0 {
		log.Printf("failed %d orphaned request(s) left by exited processes", n)
	}
```

Add `path/filepath` and the `ownership` package to the imports.

- [ ] **Step 6: Add the usage line**

Find where the binary prints subcommand help (search for `"doctor"` in a usage string). Add `init` alongside `serve`, `mcp`, `doctor`, `usage`, `version`, described as: `init    write a starter models.yaml into ~/.chaio-crewchief`. If no usage text exists, skip this step.

- [ ] **Step 7: Verify the whole thing end to end by hand**

Run:

```bash
cd gateway && go build -o /tmp/cc ./cmd/chaio-crewchief
export CHAIO_CREWCHIEF_HOME=$(mktemp -d)
unset CHAIO_CREWCHIEF_URL CREWCHIEF_URL DISPATCH_URL

# Embedded MCP starts and lists tools with no config at all.
{ printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}\n'
  sleep 1
  printf '{"jsonrpc":"2.0","method":"notifications/initialized"}\n{"jsonrpc":"2.0","id":2,"method":"tools/list"}\n'
  sleep 2; } | /tmp/cc mcp | grep -q crewchief_delegate && echo "OK: embedded mcp starts with no config"

/tmp/cc init && ls -l "$CHAIO_CREWCHIEF_HOME"
/tmp/cc init; echo "exit=$? (want non-zero)"
```

Expected: the handshake prints OK, `init` writes the file, and the second `init` refuses with a non-zero exit.

- [ ] **Step 8: Run everything and commit**

Run: `cd gateway && go vet ./... && go test -race ./...`
Expected: all PASS.

```bash
git add gateway/cmd gateway/internal/cli
git commit -m "feat: embedded MCP by default, plus a chaio-crewchief init subcommand

mcp now starts an in-process gateway unless CHAIO_CREWCHIEF_URL names a remote
one, so the plugin works after brew install with nothing else running. init
writes a starter models.yaml on request only, refusing to overwrite without
--force. serve reaps orphans on startup too, since a SIGKILLed daemon strands
rows the same way.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: `--local` / `--gateway` on usage and doctor

**Files:**
- Modify: `gateway/internal/cli/usage.go:107`, `gateway/internal/cli/doctor.go:128`
- Create: `gateway/internal/cli/mode.go`
- Test: `gateway/internal/cli/mode_test.go`

**Interfaces:**
- Consumes: `gwurl.Resolve` (Task 6), `chome.Resolve` (Task 1), `embed.Start` (Task 5).
- Produces: `cli.ModeFlags` with `Register(fs *flag.FlagSet)` and `Resolve() (gwurl.Mode, string, error)`.

Without this, a user with `CHAIO_CREWCHIEF_URL` exported can never inspect their local ledger, and a user without it can never inspect the shared one. Both are real: the local ledger holds this laptop's delegations, the GX10 ledger holds production.

- [ ] **Step 1: Write the failing test**

Create `gateway/internal/cli/mode_test.go`:

```go
package cli

import (
	"flag"
	"testing"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/gwurl"
)

func TestModeFlags(t *testing.T) {
	tests := []struct {
		name     string
		envURL   string
		args     []string
		wantMode gwurl.Mode
		wantErr  bool
	}{
		{name: "no env, no flag", envURL: "", args: nil, wantMode: gwurl.ModeEmbedded},
		{name: "env set", envURL: "http://gx10:8181", args: nil, wantMode: gwurl.ModeGateway},
		{name: "--local overrides env", envURL: "http://gx10:8181", args: []string{"--local"}, wantMode: gwurl.ModeEmbedded},
		{name: "--gateway with env", envURL: "http://gx10:8181", args: []string{"--gateway"}, wantMode: gwurl.ModeGateway},
		{name: "--gateway with no URL is an error", envURL: "", args: []string{"--gateway"}, wantErr: true},
		{name: "both flags is an error", envURL: "", args: []string{"--local", "--gateway"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CHAIO_CREWCHIEF_URL", tt.envURL)
			t.Setenv("CREWCHIEF_URL", "")
			t.Setenv("DISPATCH_URL", "")

			var mf ModeFlags
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			mf.Register(fs)
			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("parse: %v", err)
			}
			mode, _, err := mf.Resolve()
			if tt.wantErr {
				if err == nil {
					t.Fatal("Resolve() = nil error, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if mode != tt.wantMode {
				t.Errorf("mode = %q, want %q", mode, tt.wantMode)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd gateway && go test ./internal/cli/ -run TestModeFlags -v`
Expected: FAIL — `undefined: ModeFlags`.

- [ ] **Step 3: Write the implementation**

Create `gateway/internal/cli/mode.go`:

```go
package cli

import (
	"errors"
	"flag"

	"github.com/ChadDahlgren/chaio-crewchief/gateway/internal/gwurl"
)

// ModeFlags let a subcommand override the environment's choice of ledger.
//
// A user with CHAIO_CREWCHIEF_URL exported would otherwise have no way to look
// at their local ledger, and a user without it no way to look at a shared one.
// Both ledgers are real and they hold different work.
type ModeFlags struct {
	local   bool
	gateway bool
}

// Register adds --local and --gateway to a flag set.
func (m *ModeFlags) Register(fs *flag.FlagSet) {
	fs.BoolVar(&m.local, "local", false, "read the local embedded ledger, ignoring CHAIO_CREWCHIEF_URL")
	fs.BoolVar(&m.gateway, "gateway", false, "query the gateway named by CHAIO_CREWCHIEF_URL")
}

// Resolve reports the mode to use and, in gateway mode, the URL.
func (m *ModeFlags) Resolve() (gwurl.Mode, string, error) {
	if m.local && m.gateway {
		return "", "", errors.New("--local and --gateway are mutually exclusive")
	}
	envMode, url := gwurl.Resolve()
	switch {
	case m.local:
		return gwurl.ModeEmbedded, "", nil
	case m.gateway:
		if url == "" {
			return "", "", errors.New("--gateway given but no gateway URL is set; export CHAIO_CREWCHIEF_URL")
		}
		return gwurl.ModeGateway, url, nil
	default:
		return envMode, url, nil
	}
}
```

- [ ] **Step 4: Use it in usage and doctor**

In both `Usage` and `Doctor`, register `ModeFlags` on the existing `flag.FlagSet` before parsing, then branch on the resolved mode. In gateway mode, keep the current HTTP behavior against the resolved URL. In embedded mode, start an instance and point the same HTTP calls at `inst.BaseURL`:

```go
	var mf ModeFlags
	mf.Register(fs)
	// ... existing flag registrations and fs.Parse(args) ...

	mode, url, err := mf.Resolve()
	if err != nil {
		fmt.Fprintf(w, "error: %v\n", err)
		return 2
	}
	if mode == gwurl.ModeEmbedded {
		paths, err := chome.Resolve()
		if err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
			return 1
		}
		inst, err := embed.Start(context.Background(), embed.Config{Paths: paths})
		if err != nil {
			fmt.Fprintf(w, "error: start embedded gateway: %v\n", err)
			return 1
		}
		defer inst.Close()
		url = inst.BaseURL
	}
```

In `Doctor`, print the resolved mode before anything else, so the first line of the most-likely-run diagnostic command answers "which ledger am I even looking at":

```go
	if mode == gwurl.ModeEmbedded {
		fmt.Fprintf(w, "mode: embedded (no CHAIO_CREWCHIEF_URL set; home %s)\n", paths.Home)
	} else {
		fmt.Fprintf(w, "mode: gateway (%s)\n", url)
	}
```

- [ ] **Step 5: Run tests and check by hand**

Run:

```bash
cd gateway && go test -race ./... && go vet ./...
go build -o /tmp/cc ./cmd/chaio-crewchief
export CHAIO_CREWCHIEF_HOME=$(mktemp -d)
unset CHAIO_CREWCHIEF_URL CREWCHIEF_URL DISPATCH_URL
/tmp/cc doctor | head -3
/tmp/cc usage | head -3
CHAIO_CREWCHIEF_URL=http://127.0.0.1:9 /tmp/cc doctor --local | head -3
```

Expected: tests pass; the first two report embedded mode; the third reports embedded mode despite the URL being set.

- [ ] **Step 6: Commit**

```bash
git add gateway/internal/cli
git commit -m "feat: add --local and --gateway to usage and doctor

An exported CHAIO_CREWCHIEF_URL otherwise makes the local ledger unreachable,
and its absence makes a shared one unreachable. doctor now leads with which
mode it resolved, since that is the first thing to establish.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: CI coverage and documentation

**Files:**
- Modify: `.github/workflows/ci.yml` (the `smoke` job, after the existing "MCP stdio handshake" step)
- Modify: `README.md`, `SECURITY.md`, `CHANGELOG.md`

**Interfaces:**
- Consumes: everything above.
- Produces: nothing consumed by other tasks.

The embedded-with-no-config path is exactly what is broken today, so CI should be the thing that keeps it fixed.

- [ ] **Step 1: Add the CI step**

Append to the `smoke` job in `.github/workflows/ci.yml`:

```yaml
      # The scenario a fresh installer actually hits: no gateway running, no
      # config written, no environment variable set. This is what shipped
      # broken in v0.4.0, so it is what CI guards.
      - name: Embedded MCP starts with no gateway and no config
        env:
          CHAIO_CREWCHIEF_HOME: /tmp/embedded-home
        run: |
          unset CHAIO_CREWCHIEF_URL CREWCHIEF_URL DISPATCH_URL
          out=$({ printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"ci","version":"0"}}}\n'
            sleep 1
            printf '{"jsonrpc":"2.0","method":"notifications/initialized"}\n{"jsonrpc":"2.0","id":2,"method":"tools/list"}\n'
            sleep 2; } | /tmp/chaio-crewchief mcp)
          echo "$out" | grep -q crewchief_delegate
          test -d /tmp/embedded-home
          test ! -f /tmp/embedded-home/models.yaml   # startup must never write config

      - name: init writes a starter config and refuses to clobber it
        env:
          CHAIO_CREWCHIEF_HOME: /tmp/embedded-home
        run: |
          /tmp/chaio-crewchief init
          test -f /tmp/embedded-home/models.yaml
          if /tmp/chaio-crewchief init; then echo "init overwrote without --force"; exit 1; fi
          /tmp/chaio-crewchief init --force
```

- [ ] **Step 2: Verify the CI step logic locally**

Run the same commands from Step 1 against a locally built binary, substituting `/tmp/chaio-crewchief` with your build output. All assertions must pass before pushing.

- [ ] **Step 3: Update README.md**

Restructure the install section to lead with the Claude Code path:

1. `brew install ChadDahlgren/tap/chaio-crewchief`
2. Add the plugin
3. `chaio-crewchief init`, edit `~/.chaio-crewchief/models.yaml`
4. Restart the session and delegate

Then a **Sharing a fleet across machines** section covering `serve` and `CHAIO_CREWCHIEF_URL`, stating plainly that setting the variable switches every command to the remote gateway, and that async delegation requires it.

Add to the modes description: embedded mode binds an ephemeral, unauthenticated port on `127.0.0.1` for the life of the session, reachable by any local process.

- [ ] **Step 4: Update SECURITY.md**

Extend the threat model with embedded mode. Same shape as the gateway — no authentication, provider API keys in the process environment, anyone who reaches the port can spend money — but bound to loopback and lasting only as long as the session. Say that a shared or multi-user machine is the case where this matters.

- [ ] **Step 5: Update CHANGELOG.md**

Under Unreleased:

```markdown
### Added
- Embedded mode: `chaio-crewchief mcp` now runs the gateway in-process, so the
  plugin works after `brew install` with nothing else running.
- `chaio-crewchief init` writes a starter `models.yaml` to `~/.chaio-crewchief`.
- `--local` and `--gateway` on `usage` and `doctor` to pick a ledger explicitly.
- Requests left running by a process that exited are failed on startup instead
  of reporting a stale status forever.

### Changed
- **Breaking:** an unset `CHAIO_CREWCHIEF_URL` now selects embedded mode rather
  than defaulting to `http://localhost:8181`. Set the variable explicitly to
  keep proxying to a gateway.
- `~/.chaio-crewchief/` (or `CHAIO_CREWCHIEF_HOME`) is the default location for
  config and the ledger when paths are not given as flags. `serve`'s flags are
  unchanged.
```

- [ ] **Step 6: Full verification and commit**

Run: `cd gateway && go vet ./... && go test -race -count=1 ./...`
Expected: all PASS.

```bash
git add .github/workflows/ci.yml README.md SECURITY.md CHANGELOG.md
git commit -m "docs+ci: cover embedded mode and lead the README with the plugin

CI now runs the scenario that shipped broken in v0.4.0: MCP with no gateway,
no config, and no environment variable. README leads with the Claude Code
path, and both README and SECURITY name the loopback listener rather than
leaving it implicit.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

## Corrections applied during execution

This plan was executed on 2026-07-26. Review found defects **in the plan's own
code** — the implementers transcribed it faithfully and the bugs were mine. The
shipped code in git is correct; the task text above is not. Anyone re-running
this plan should apply these first.

**Task 3 — two PID-reuse races in the plan's `ownership` code.**
`OwnerAlive`'s `os.Remove(path)` can unlink the lock file of a *live* process:
if a PID was reused and that process is inside `Acquire` (file opened, not yet
flocked), the reaper takes the lock, concludes "gone", and unlinks the entry.
Every later check then reports that live process dead — permanently, silently,
and un-self-healing. The removal was deleted outright; a stale lock file is a
few bytes and `Acquire` reuses it via `O_CREATE`. `Release` had the same shape
and now removes the file *while still holding the lock*. `OwnerAlive` also
stats the lock directory first and returns "alive" when it is missing, so a
misconfigured path cannot mass-fail every running row.

**Task 5 — the plan reaped *after* acquiring, and never created the lock
directory.** `embed.Start` in the plan text called `ownership.Acquire(p.Locks)`
and only then `st.ReapOrphans(...)`. Lock files outlive their processes and pids
get reused — realistically after a reboot, which reallocates low ones — so
acquiring first can leave this process holding the lock file of a dead owner
whose pid it drew. That owner's abandoned rows then read as live and stay
`running` permanently, unfixable by any later run: exactly the class of defect
the `OwnerAlive` correction above describes. The shipped code reaps first.

It also calls a `ownership.EnsureDir(lockDir)` the plan's interface list does
not mention. `OwnerAlive` stats the lock directory and returns "assume alive"
when it is missing, so on a fresh home — where nothing has created the directory
yet — every pid reads as live and nothing is ever reaped. Both the plan's code
block and its Task 3 `ownership` API list have been corrected above; a re-runner
following the earlier text verbatim reintroduces both.

**Task 4 — the plan's tests assumed a lock directory that nothing creates.**
Following from the above, `TestReapOrphansIsIdempotent` and
`TestRecordRequestStampsAssumedOwner` must `os.MkdirAll` the lock directory
before reaping, or `OwnerAlive` correctly reports "alive" and nothing is
reaped.

**Task 8 — the starter config named a key the loader ignores.**
`StarterModelsYAML` used `model:`, but `types.Preset` declares
`yaml:"model_id"`. YAML decoding is non-strict, so the key was dropped
silently: a user uncommenting the example got an empty model ID, no parse
error, and an upstream failure that looked like their own mistake. Fixed, and
the examples now live uncommented in a separately-tested constant so the test
actually exercises them.

**Task 5 — `Close` needed real synchronization.** The plan's plain `bool` flag
is a data race against the signal-handler shape Task 8 introduces; it is now a
`sync.Once` with a stored error.

**Task 1 — the plan hardcoded the lock directory under the home.**
`chome.ResolveIn` sets `Locks: filepath.Join(home, "locks")` in the plan text.
The shipped code instead derives it from the database path via a new
`chome.LocksDirFor(dbPath)`, and `ResolveIn` calls that. The plan's version is
wrong for `serve`, which takes an explicit `--db` and has no home at all, so it
could not use `Paths.Locks` and would have had to invent its own derivation —
and two processes sharing a ledger that disagree about where the locks live
declare each other dead: the one looking in the wrong directory finds no lock
file for the other's PID, concludes it exited, and reaps its genuinely
in-flight rows. One function makes them agree by construction.

**Task 1 — `chome.Dir` must reject a relative `CHAIO_CREWCHIEF_HOME`.** The
plan resolves the variable as given. Claude Code launches the MCP server with
an arbitrary working directory, so a relative value has the CLI and the MCP
session silently using different ledgers and different lock directories. The
shipped code requires an absolute path, which also catches a literal
unexpanded `~` — what a quoted value in an MCP server config JSON produces,
and which used to create a directory named `~` and report success.

**Task 2 — the DSN carries pragmas the plan omits.** `store.dsn` appends
`?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)`. Embedded mode makes
the ledger multi-writer by design, and under the default rollback journal a
writer that loses a collision gets an immediate "database is locked" rather
than waiting. Note this is a persistent, one-way change to any existing
database file and does not work on NFS.

**Task 2 — `Open` retries, and migrations are transactional.** The plan opens
once and reads `MAX(version)` outside a transaction. Both are wrong under the
concurrency embedded mode introduces: unsynchronized version reads have two
processes running the same `ALTER`, and the `journal_mode(WAL)` conversion
takes an exclusive lock `busy_timeout` does not cover. Each migration now runs
in its own `BEGIN IMMEDIATE` transaction that commits the DDL and the version
row together, and `Open` retries the whole sequence on a locked database with a
linear backoff spanning ~6.5s — longer than the 5s `busy_timeout`, since the
uncovered pre-WAL case is the one that needs the patience.

**Task 2 — anything indexing a migrated column must be created after the
migrations run.** The plan puts `idx_requests_owner_status` (over
`owner_host`, added by migration 4) in `schemaDDL`, which `openOnce` executes
*before* `applyMigrations`. On any database that already exists,
`CREATE TABLE IF NOT EXISTS` no-ops and the table keeps its old columns, so the
index statement hits a column that is not there yet and `Open` fails — on every
pre-existing ledger, deterministically, unrecoverable without hand-running
`sqlite3`. The shipped code moves it to a `postMigrationDDL` constant executed
after `applyMigrations` returns. A re-runner following the plan verbatim would
reintroduce a bug that breaks every existing install.

**Task 4 — the engine aborts when `RecordRequest` fails.** The plan swallows
it. Without the parent row, `UpdateRequestResult` no-ops, async pollers get 404
forever, and reaping never sees the request. This is a `POST /delegate`
behavior change for `serve`: it can now 500 where it previously returned an
artifact. The attempt and result writes stay best-effort — their tokens are
already spent by the time they run — but log loudly rather than being
discarded.

**Task 4 — the async goroutine must record the failure it just caught.** The
plan's `serve` handler has a bare `_, _ = eng.RunWithID(...)` in the async
goroutine. Apply this together with the engine-abort correction directly above,
or that correction defeats itself: once `RunWithID` aborts on a failed
`RecordRequest`, the async path has already handed the caller an ID for a
request with no row, and swallowing the error leaves them polling a permanent
404 — the exact failure the abort was added to prevent. The shipped code logs
the error, calls `RecordRequest` with `StatusFailed` if `GetRequest` finds no
row, then `UpdateRequestResult`. Both writes are best effort: if the ledger is
what broke, they fail too and the log line is the only surviving trace.

**Task 4 — `recordResult` is a helper, and the best-effort writes are loud.**
The plan writes the terminal status with a bare `_ = e.store.UpdateRequestResult(...)`
at each of its two call sites. The shipped code routes both through an
`(*Engine).recordResult` helper that logs `LEDGER STALE` on failure, and
`RecordAttempt`'s discarded error got the same treatment. Neither aborts — the
tokens are spent and the caller is being handed the artifact either way — but a
silently dropped ledger write is a number the user will later read as fact.

**Task 9 — `usage` prints a mode header.** New behavior in neither the plan nor
this list. Before rendering, `usage` prints `mode: embedded (home %s)` or
`mode: gateway (%s)`, as `doctor` does. Two ledgers produce visually identical
reports, and the likeliest upgrade surprise is someone who relied on the old
implicit `localhost:8181` default now reading an empty local ledger and
concluding their data is gone.

**Task 9 — `doctor`'s mode header wording.** The plan prints
`mode: embedded (no CHAIO_CREWCHIEF_URL set; home %s)`; the shipped code prints
`mode: embedded (home %s)`.

**Task 9 — `usage` does not report a missing counterfactual as 0% savings.**
The plan renders `savings: %s (%.1f%%)` unconditionally over
`money(counterfactual - cost)`. Nothing in the binary writes a `rates.yaml`, so
"no rates table" is every embedded home's starting state, and any ledger with
priced history renders as `savings: $-3.7500 (0.0%)` — a negative figure
labelled savings beside a percentage claiming break-even. The shipped code adds
`counterfactual_configured` to `StatsTotalsView` (an absent rates table and a
genuine zero are otherwise identical over the wire, especially at zero
attempts), prints `savings: n/a` with the reason and *no* percentage when there
is nothing to compare against or no attempts yet, reports `cost > counterfactual`
as `overspend` with a ratio rather than an unbounded negative percentage, and
formats money's magnitude with the sign outside the currency symbol (`-$3.75`,
not `$-3.7500`). Non-finite totals render `n/a` rather than `$NaN`.

Two later corrections to that field. First, it is computed from
`rates.Table.HasCounterfactual()`, not from `rt != nil` as the first cut had it:
a `rates.yaml` that exists but declares no `counterfactual:` block loads a
non-nil table whose counterfactual prices everything at $0, so `rt != nil`
reported a configured frontier that does not exist. The flag therefore means
"there is a frontier reference rate", not "a rates file is loaded".

Second, `savingsLine` checks the *amount* before the flag: it only trusts
`counterfactual_configured == false` when `counterfactual_usd` is also zero. A
pre-0.5 gateway omits the field entirely, so it decodes to false beside a real,
priced counterfactual, and a flag-first order printed "no frontier price to
compare against" directly under a $551.10 counterfactual — on precisely the
upgrade shape where the CLI moves ahead of a still-running daemon. The n/a text
also no longer names `rates.yaml` as missing, since the CLI never checked for
it; it names the `counterfactual:` block instead. `plugin/scripts/
fleet-statusline.sh` needs the same jq ordering, and there it is the *only* case
that occurs, since the statusline runs only in gateway mode. Two further fixes

in the same function: a negative counterfactual is reported as its actual value
rather than as "$0.00", and a cost within rounding distance of the frontier
prints parity rather than an "overspend ... 1.0x" that contradicts itself.

Smaller corrections: `usage`/`doctor` reject flags placed after a positional
argument rather than silently discarding them; `init` rejects trailing
arguments rather than reporting success; `-h` exits 0.

## Follow-up, not in this plan

The design document names one related fix that is genuinely separate: the plugin should fail with a readable message when the binary is not on `PATH`, instead of a cryptic MCP startup error. That is a `plugin/` concern with no dependency on anything here, and it deserves its own change.
