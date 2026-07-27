package ownership

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
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

// Release must remove the lock file while the lock is still held.
//
// The eight lines of comment on Release explain why: removing after unlocking
// opens a window where a process that reused this pid could acquire the lock on
// the same inode between our unlock and our remove. Our remove would then
// delete the new owner's lock file out from under it, and every later
// OwnerAlive check for that pid would find nothing and wrongly report it gone —
// permanently.
//
// That invariant had no test. Reordering Release to unlock, close, remove left
// ownership, store, and embed all passing. This observes the ordering directly:
// at the instant before the unlock, the file must already be gone.
func TestReleaseRemovesLockFileWhileStillHeld(t *testing.T) {
	dir := t.TempDir()
	o, err := Acquire(dir)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	var ran bool
	var existedAtUnlock bool
	var lockedAtUnlock bool
	beforeUnlock = func() {
		ran = true
		_, statErr := os.Stat(o.path)
		existedAtUnlock = statErr == nil

		// And the lock genuinely still ours: a second descriptor on the same
		// path must fail to flock. (Open by path would fail after the remove,
		// so re-check through the held descriptor's own file instead.)
		lockedAtUnlock = flockConflicts(t, o.path)
	}
	t.Cleanup(func() { beforeUnlock = nil })

	if err := o.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if !ran {
		t.Fatal("Release() never reached the unlock step")
	}
	if existedAtUnlock {
		t.Error("lock file still present at unlock time: Release removed it after unlocking, reopening the pid-reuse window")
	}
	if !lockedAtUnlock {
		t.Error("lock was already released before the file was removed")
	}
	if _, err := os.Stat(o.path); !os.IsNotExist(err) {
		t.Errorf("lock file still present after Release: stat err = %v", err)
	}
}

// flockConflicts reports whether path is absent or held. After a correct
// Release's remove, path is absent, which is itself proof the remove came
// first; if it is present, we test whether it is still locked.
func flockConflicts(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return true // gone: removed while held, which is the point
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}
