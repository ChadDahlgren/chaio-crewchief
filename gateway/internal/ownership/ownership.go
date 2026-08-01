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

// EnsureDir creates lockDir if it is missing.
//
// Callers reap before they acquire (see the ordering note on OwnerAlive) and so
// cannot rely on Acquire to create the directory for them. It matters: a
// missing lockDir makes OwnerAlive answer "unsure, assume alive" for every pid,
// which would silently reap nothing on any install whose lock directory was
// never created or was cleaned away.
func EnsureDir(lockDir string) error {
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}
	return nil
}

// Acquire takes this process's lock inside lockDir, creating the directory if
// needed.
func Acquire(lockDir string) (*Owner, error) {
	if err := EnsureDir(lockDir); err != nil {
		return nil, err
	}
	pid := os.Getpid()
	path := filepath.Join(lockDir, lockName(pid))

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		closeErr := f.Close()
		// Either a genuinely live process already holds this pid's lock, or
		// this same process called Acquire twice: flock conflicts across
		// separate descriptors within one process, which is what the
		// self-alive test relies on. A stale file left by a dead owner flocks
		// successfully, so it cannot produce this error.
		if closeErr != nil {
			return nil, fmt.Errorf("lock %s: %w (also failed to close lock file: %v)", path, err, closeErr)
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return &Owner{pid: pid, path: path, f: f}, nil
}

// PID is the process id recorded with rows this owner writes.
func (o *Owner) PID() int { return o.pid }

// Release drops the lock and removes the file.
//
// The file is removed while the lock is still held, before unlocking or
// closing. Removing after unlocking would open a window where a process that
// reused this pid could acquire the lock on the same inode between our
// unlock and our remove; the remove would then delete the new owner's lock
// file out from under it, and every later OwnerAlive check for that pid
// would find nothing and wrongly report it gone, permanently.
// beforeUnlock runs inside Release, after the file is removed and while the
// lock is still held. It exists so a test can assert that ordering, which is a
// correctness invariant with no other observable signature: reversing Release
// to unlock, close, remove leaves every package's tests green while
// reintroducing the pid-reuse window described above. Production leaves it nil.
var beforeUnlock func()

func (o *Owner) Release() error {
	if o == nil || o.f == nil {
		return nil
	}
	rmErr := os.Remove(o.path)
	if beforeUnlock != nil {
		beforeUnlock()
	}
	_ = syscall.Flock(int(o.f.Fd()), syscall.LOCK_UN)
	err := o.f.Close()
	o.f = nil
	if rmErr != nil && !os.IsNotExist(rmErr) && err == nil {
		err = rmErr
	}
	return err
}

// OwnerAlive reports whether the process that recorded pid still holds its
// lock. A missing lock file means the owner is gone and cleaned up after
// itself. Anything ambiguous — an unreadable lock file, a missing lockDir —
// returns true, because leaving a stale row alone is a smaller harm than
// failing a live one.
//
// A lock file left behind by a dead owner is never removed here: doing so
// while another process is mid-Acquire on the same pid (a reused pid racing
// this call) would delete that new owner's lock file, and the pid would read
// as gone forever after. The stale file costs a few bytes; Acquire reuses it
// happily via O_CREATE, so leaving it in place is free.
//
// Ordering note: ask this before taking your own lock, never after. Lock files
// outlive the processes that made them, and pids are reallocated — low ones
// especially, after a reboot. If a dead owner had the pid this process now
// draws, acquiring first means we are holding that owner's lock file when we
// come to check it, so its abandoned rows read as live and stay `running`
// forever, unfixable by any later run. Reaping first sees the file unlocked and
// correctly calls it dead.
func OwnerAlive(lockDir string, pid int) (alive bool, retErr error) {
	if _, err := os.Stat(lockDir); err != nil {
		// Cannot tell "this pid never had a lock here" from "this directory
		// was never the right one to ask." Either way, unsure means alive.
		return true, fmt.Errorf("stat lock dir: %w", err)
	}

	path := filepath.Join(lockDir, lockName(pid))
	f, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return true, fmt.Errorf("open lock file: %w", err)
	}
	defer func() {
		if cerr := f.Close(); retErr == nil && cerr != nil {
			retErr = fmt.Errorf("close lock file: %w", cerr)
		}
	}()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true, nil // held by a live process
	}
	// We took it, so nobody owned it. Drop it again; leave the file in place.
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false, nil
}
