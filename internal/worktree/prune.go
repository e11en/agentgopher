package worktree

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Prune removes stale git worktree metadata, orphaned directories under
// .agentgopher/worktrees/ and lock files that are dead or have no worktree.
func Prune(ctx context.Context, repoDir string) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	err := prune(ctx, repoDir)
	if ctx.Err() == context.DeadlineExceeded {
		return &TimeoutError{Path: repoDir, Operation: "prune", Timeout: Timeout}
	}
	return err
}

func prune(ctx context.Context, repoDir string) error {
	if _, err := gitRun(ctx, repoDir, "worktree", "prune"); err != nil {
		return err
	}
	worktreesDir := filepath.Join(repoDir, Dir)
	entries, err := os.ReadDir(worktreesDir)
	if errors.Is(err, os.ErrNotExist) {
		return pruneLocks(repoDir, worktreesDir)
	}
	if err != nil {
		return &Error{Message: err.Error()}
	}
	// git reports realpaths; resolve the prefix once so symlinked repos match.
	realDir, err := filepath.EvalSymlinks(worktreesDir)
	if err != nil {
		realDir = worktreesDir
	}
	list, err := ListWorktrees(ctx, repoDir)
	if err != nil {
		return err
	}
	active := map[string]bool{}
	for _, e := range list {
		active[toSlash(e.Path)] = true
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(realDir, e.Name())
		if !active[toSlash(p)] {
			if err := os.RemoveAll(p); err != nil {
				return &Error{Message: "failed to remove " + p + ": " + err.Error()}
			}
		}
	}
	return pruneLocks(repoDir, worktreesDir)
}

func pruneLocks(repoDir, worktreesDir string) error {
	locksDir := filepath.Join(repoDir, LocksDir)
	locks, err := os.ReadDir(locksDir)
	if err != nil {
		return nil
	}
	for _, l := range locks {
		name := strings.TrimSuffix(l.Name(), ".lock")
		if name == l.Name() {
			continue
		}
		path := filepath.Join(locksDir, l.Name())
		_, dirErr := os.Stat(filepath.Join(worktreesDir, name))
		var lf lockFile
		b, _ := os.ReadFile(path)
		alive := json.Unmarshal(b, &lf) == nil && lf.PID > 0 && processAlive(lf.PID)
		if dirErr != nil || !alive {
			_ = os.Remove(path)
		}
	}
	return nil
}
