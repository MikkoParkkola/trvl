package watch

import (
	"fmt"
	"os"
	"path/filepath"
)

// SchedulerLock is a cross-process advisory lock ensuring at most one price
// scheduler runs against a given ~/.trvl directory.
//
// Every `trvl mcp` process starts its own scheduler. That is harmless with one
// client, but MCP clients spawn a server per session and some leak them: 15
// orphaned `trvl mcp` processes were observed alive simultaneously, each running
// a full scheduler. With 468 active watches that is ~7,000 live provider queries
// per 30-minute round instead of 468, all writing the same watches.json and
// price-history.json — rate-limit exposure and concurrent-write contention on
// top of the wasted work.
//
// The lock is advisory and held for the process lifetime. A process that cannot
// acquire it still serves tool calls normally; it simply does not schedule.
type SchedulerLock struct {
	file *os.File
}

// LockPath is the lockfile backing the scheduler singleton.
func LockPath(dir string) string {
	return filepath.Join(dir, "scheduler.lock")
}

// TryLockScheduler attempts to become the single scheduler for dir. It returns
// (lock, true, nil) on success and (nil, false, nil) when another live process
// already holds it — the latter is a normal outcome, not an error.
//
// The OS releases the lock automatically if the holder dies, so a crashed or
// SIGKILLed process cannot wedge scheduling for everyone else.
func TryLockScheduler(dir string) (*SchedulerLock, bool, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, fmt.Errorf("create storage dir: %w", err)
	}
	f, err := os.OpenFile(LockPath(dir), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open scheduler lock: %w", err)
	}
	held, err := tryLockFile(f)
	if err != nil {
		_ = f.Close()
		return nil, false, fmt.Errorf("lock scheduler: %w", err)
	}
	if !held {
		_ = f.Close()
		return nil, false, nil
	}
	return &SchedulerLock{file: f}, true, nil
}

// Release drops the lock. Safe to call on a nil lock and to call more than once.
func (l *SchedulerLock) Release() {
	if l == nil || l.file == nil {
		return
	}
	_ = unlockFile(l.file)
	_ = l.file.Close()
	l.file = nil
}
