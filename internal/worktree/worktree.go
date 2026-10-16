// Package worktree manages the git worktrees agentgopher runs agents in.
package worktree

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Dir is the worktree directory relative to the host repo.
const Dir = ".agentgopher/worktrees"

// Timeout bounds worktree create and prune.
const Timeout = 30 * time.Second

// Error reports a failed worktree operation.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

// TimeoutError reports a worktree operation that ran longer than Timeout.
type TimeoutError struct {
	Path      string
	Operation string
	Timeout   time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("worktree %s timed out after %v", e.Operation, e.Timeout)
}

// Info describes a worktree handed out by Create. Unlock releases the lock
// taken on it (a no-op when no lock was taken).
type Info struct {
	Path   string
	Branch string
	Unlock func()
}

// Entry is one line group of `git worktree list --porcelain`.
type Entry struct {
	Path   string
	Branch string
}

// noConfigLockFlags stop `git worktree add -b` from writing upstream tracking
// config, which races with other processes holding .git/config.lock.
var noConfigLockFlags = []string{"-c", "branch.autoSetupMerge=false", "-c", "push.autoSetupRemote=false"}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]`)

// SanitizeName lowercases name and replaces every non-alphanumeric run with a dash.
func SanitizeName(name string) string {
	return nonAlnum.ReplaceAllString(strings.ToLower(name), "-")
}

func timestamp() string { return time.Now().Format("20060102-150405") }

func randomSuffix() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// GenerateTempBranchName returns agentgopher/[<name>/]<timestamp>-<random>.
func GenerateTempBranchName(name string) string {
	if name != "" {
		return "agentgopher/" + SanitizeName(name) + "/" + timestamp() + "-" + randomSuffix()
	}
	return "agentgopher/" + timestamp() + "-" + randomSuffix()
}

// WorktreeName is the directory name used for branch.
func WorktreeName(branch string) string { return strings.ReplaceAll(branch, "/", "-") }

// gitRun runs git in dir with the C locale so stderr matching is stable.
func gitRun(ctx context.Context, dir string, args ...string) (string, error) {
	full := append(append([]string{}, noConfigLockFlags...), args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", &Error{Message: msg}
	}
	return stdout.String(), nil
}

func toSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// resolve follows symlinks so paths git reports (resolved) compare equal to
// paths we built from the repo dir (macOS keeps temp dirs behind /private).
func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func samePath(a, b string) bool { return toSlash(resolve(a)) == toSlash(resolve(b)) }

// ListWorktrees parses `git worktree list --porcelain`.
func ListWorktrees(ctx context.Context, repoDir string) ([]Entry, error) {
	out, err := gitRun(ctx, repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var entries []Entry
	var cur *Entry
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			entries = append(entries, Entry{Path: strings.TrimSpace(strings.TrimPrefix(line, "worktree "))})
			cur = &entries[len(entries)-1]
		case strings.HasPrefix(line, "branch refs/heads/") && cur != nil:
			cur.Branch = strings.TrimSpace(strings.TrimPrefix(line, "branch refs/heads/"))
		}
	}
	return entries, nil
}

// FindCollidingWorktree matches by branch first, then by path, which covers a
// detached HEAD mid-rebase.
func FindCollidingWorktree(entries []Entry, branch, worktreePath string) (Entry, bool) {
	for _, e := range entries {
		if e.Branch == branch {
			return e, true
		}
	}
	for _, e := range entries {
		if samePath(e.Path, worktreePath) {
			return e, true
		}
	}
	return Entry{}, false
}

// IsManagedWorktreePath reports whether worktreePath lives under worktreesDir.
func IsManagedWorktreePath(worktreePath, worktreesDir string) bool {
	return strings.HasPrefix(toSlash(resolve(worktreePath)), toSlash(resolve(worktreesDir)))
}

// HasUncommittedChanges reports staged, unstaged or untracked changes.
func HasUncommittedChanges(ctx context.Context, worktreePath string) (bool, error) {
	out, err := gitRun(ctx, worktreePath, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// Remove deletes a managed worktree and its git metadata. worktreePath must be
// <repo>/.agentgopher/worktrees/<name>.
func Remove(ctx context.Context, worktreePath string) error {
	repoDir := filepath.Join(worktreePath, "..", "..", "..")
	_, err := gitRun(ctx, repoDir, "worktree", "remove", "--force", worktreePath)
	return err
}

// CreateTemp creates a worktree on a fresh temp branch.
func CreateTemp(ctx context.Context, repoDir, name string) (Info, error) {
	branch := GenerateTempBranchName(name)
	worktreeName := "agentgopher-" + strings.TrimPrefix(strings.ReplaceAll(branch, "/", "-"), "agentgopher-")
	return create(ctx, repoDir, branch, "HEAD", worktreeName, false)
}

// Create checks out branch in a managed worktree, creating the branch from
// baseBranch (default HEAD) when it does not exist. A clean managed worktree
// for the same branch is reused and refreshed from origin (ADR 0003); a dirty
// one is reused with a warning.
func Create(ctx context.Context, repoDir, branch, baseBranch string) (Info, error) {
	if branch == "" {
		return CreateTemp(ctx, repoDir, "")
	}
	if baseBranch == "" {
		baseBranch = "HEAD"
	}
	return create(ctx, repoDir, branch, baseBranch, WorktreeName(branch), true)
}

func create(ctx context.Context, repoDir, branch, baseBranch, worktreeName string, reusable bool) (Info, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	info, err := createLocked(ctx, repoDir, branch, baseBranch, worktreeName, reusable)
	if ctx.Err() == context.DeadlineExceeded {
		return Info{}, &TimeoutError{Path: repoDir, Operation: "create", Timeout: Timeout}
	}
	return info, err
}

func createLocked(ctx context.Context, repoDir, branch, baseBranch, worktreeName string, reusable bool) (Info, error) {
	worktreesDir := filepath.Join(repoDir, Dir)
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		return Info{}, &Error{Message: err.Error()}
	}
	worktreePath := filepath.Join(worktreesDir, worktreeName)
	unlock := func() {}
	if reusable {
		var err error
		if unlock, err = Acquire(repoDir, worktreeName, branch); err != nil {
			return Info{}, err
		}
	}

	if reusable {
		entries, err := ListWorktrees(ctx, repoDir)
		if err != nil {
			unlock()
			return Info{}, err
		}
		if hit, ok := FindCollidingWorktree(entries, branch, worktreePath); ok {
			if !IsManagedWorktreePath(hit.Path, worktreesDir) {
				unlock()
				return Info{}, &Error{Message: fmt.Sprintf("Branch '%s' is already checked out in worktree at '%s'. agentgopher's branch and merge-to-head strategies run the agent in a git worktree under %s, and git refuses to check out the same branch in two worktrees at once. Pick a different branch, or switch the main working tree to a different branch before re-running.", branch, hit.Path, Dir)}
			}
			dirty, err := HasUncommittedChanges(ctx, hit.Path)
			if err != nil {
				unlock()
				return Info{}, err
			}
			if dirty {
				fmt.Fprintf(os.Stderr, "Reusing worktree at %s (branch '%s') - worktree has uncommitted changes\n", hit.Path, branch)
			} else {
				fastForwardFromOrigin(ctx, hit.Path, branch)
			}
			path := hit.Path
			if samePath(hit.Path, worktreePath) {
				path = worktreePath
			}
			return Info{Path: filepath.Clean(path), Branch: branch, Unlock: unlock}, nil
		}
		_, err = gitRun(ctx, repoDir, "worktree", "add", worktreePath, branch)
		if err != nil && strings.Contains(err.Error(), "invalid reference") {
			_, err = gitRun(ctx, repoDir, "worktree", "add", "-b", branch, worktreePath, baseBranch)
		}
		if err != nil {
			unlock()
			return Info{}, err
		}
		return Info{Path: worktreePath, Branch: branch, Unlock: unlock}, nil
	}

	if _, err := gitRun(ctx, repoDir, "worktree", "add", "-b", branch, worktreePath, "HEAD"); err != nil {
		if strings.Contains(err.Error(), "already checked out") || strings.Contains(err.Error(), "already exists") {
			return Info{}, &Error{Message: fmt.Sprintf("Branch '%s' is already checked out in another worktree. Use a different branch name, or wait for the other run to finish.", branch)}
		}
		return Info{}, err
	}
	return Info{Path: worktreePath, Branch: branch, Unlock: unlock}, nil
}
