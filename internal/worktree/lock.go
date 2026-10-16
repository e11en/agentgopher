package worktree

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LocksDir holds one lock file per managed worktree.
const LocksDir = ".agentgopher/locks"

// LockedError reports a worktree in use by another live process.
type LockedError struct {
	WorktreeName string
	PID          int
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("worktree '%s' is in use by process %d", e.WorktreeName, e.PID)
}

type lockFile struct {
	PID        int       `json:"pid"`
	Branch     string    `json:"branch"`
	AcquiredAt time.Time `json:"acquiredAt"`
}

// Acquire takes the lock for worktreeName. A lock held by a dead process is
// reclaimed; one held by a live process fails with LockedError. release is
// idempotent.
func Acquire(repoDir, worktreeName, branch string) (func(), error) {
	dir := filepath.Join(repoDir, LocksDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &Error{Message: err.Error()}
	}
	path := filepath.Join(dir, worktreeName+".lock")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			content, _ := json.Marshal(lockFile{PID: os.Getpid(), Branch: branch, AcquiredAt: time.Now().UTC()})
			_, werr := f.Write(content)
			_ = f.Close()
			if werr != nil {
				_ = os.Remove(path)
				return nil, &Error{Message: werr.Error()}
			}
			var once sync.Once
			return func() { once.Do(func() { _ = os.Remove(path) }) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, &Error{Message: err.Error()}
		}
		existing, readErr := os.ReadFile(path)
		var lf lockFile
		if readErr == nil && json.Unmarshal(existing, &lf) == nil && lf.PID > 0 && processAlive(lf.PID) {
			return nil, &LockedError{WorktreeName: worktreeName, PID: lf.PID}
		}
		// Stale or unreadable lock: remove and retry once.
		_ = os.Remove(path)
	}
	return nil, &Error{Message: "could not acquire worktree lock at " + path}
}
