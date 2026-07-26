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
