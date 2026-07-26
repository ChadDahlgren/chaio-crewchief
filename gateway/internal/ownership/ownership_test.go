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

func TestOwnerAliveFalseWhenNoLockFile(t *testing.T) {
	dir := t.TempDir()
	alive, err := OwnerAlive(dir, 999999)
	if err != nil {
		t.Fatalf("OwnerAlive() error = %v", err)
	}
	if alive {
		t.Error("OwnerAlive() = true with no lock file present, want false")
	}
}

func TestOwnerAliveTrueWhenLockDirMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	alive, err := OwnerAlive(dir, 999999)
	if err == nil {
		t.Fatal("OwnerAlive() error = nil, want non-nil for a missing lockDir")
	}
	if !alive {
		t.Error("OwnerAlive() = false for a missing lockDir, want true: unsure must mean alive")
	}
}

func TestOwnerAliveTrueWhenLockFileUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file mode bits")
	}
	dir := t.TempDir()
	pid := 424242
	path := filepath.Join(dir, lockName(pid))
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	alive, err := OwnerAlive(dir, pid)
	if err == nil {
		t.Fatal("OwnerAlive() error = nil, want non-nil for an unreadable lock file")
	}
	if !alive {
		t.Error("OwnerAlive() = false for an unreadable lock file, want true: unsure must mean alive")
	}
}

func TestOwnerAliveFalseForStaleLockFileAndLeavesItInPlace(t *testing.T) {
	dir := t.TempDir()
	pid := 555555
	path := filepath.Join(dir, lockName(pid))
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	alive, err := OwnerAlive(dir, pid)
	if err != nil {
		t.Fatalf("OwnerAlive() error = %v", err)
	}
	if alive {
		t.Error("OwnerAlive() = true for a lock file left behind by a dead owner, want false")
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("stale lock file removed by OwnerAlive, want it left in place: stat err = %v", err)
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

	// The helper prints one line as soon as it holds the lock. If Acquire
	// failed in the helper, t.Fatalf's output lands on stdout instead and
	// must not be mistaken for readiness.
	buf := make([]byte, 64)
	n, err := stdout.Read(buf)
	if err != nil || n == 0 {
		t.Fatalf("helper never reported readiness: n=%d err=%v", n, err)
	}
	if got := string(buf[:n]); got != "ready\n" {
		t.Fatalf("helper reported %q, want %q (helper likely failed to Acquire)", got, "ready\n")
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
