package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultCopyTimeout bounds CopyToWorktree when no timeout is given.
const DefaultCopyTimeout = 60 * time.Second

// CopyError reports a path that could not be copied, after the plain cp fallback.
type CopyError struct {
	Path     string
	Stderr   string
	ExitCode int
}

func (e *CopyError) Error() string {
	return fmt.Sprintf("failed to copy %s to worktree: %s", e.Path, e.Stderr)
}

// CopyTimeoutError reports that copying ran longer than Timeout.
type CopyTimeoutError struct {
	Paths   []string
	Timeout time.Duration
}

func (e *CopyTimeoutError) Error() string {
	return fmt.Sprintf("copying files to worktree timed out after %v", e.Timeout)
}

// CopyOnWriteFlags returns the cp flags that clone instead of copy where the
// filesystem supports it: APFS clonefile on macOS, reflink elsewhere.
func CopyOnWriteFlags(goos string) []string {
	if goos == "darwin" {
		return []string{"-cR"}
	}
	return []string{"-R", "--reflink=auto"}
}

// CopyToWorktree copies each path (relative to hostRepoDir) to the same
// relative location under worktreePath. Missing sources are skipped.
func CopyToWorktree(ctx context.Context, paths []string, hostRepoDir, worktreePath string, timeout time.Duration) error {
	if timeout == 0 {
		timeout = DefaultCopyTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for _, rel := range paths {
		src := filepath.Join(hostRepoDir, rel)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dest := filepath.Join(worktreePath, rel)
		// A reused worktree already holds the copy; cp -R would nest it inside.
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		if err := cp(ctx, append(CopyOnWriteFlags(runtime.GOOS), src, dest)); err == nil {
			continue
		}
		if err := cp(ctx, []string{"-R", src, dest}); err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return &CopyTimeoutError{Paths: paths, Timeout: timeout}
			}
			var exitErr *exec.ExitError
			code := -1
			stderr := err.Error()
			if errors.As(err, &exitErr) {
				code = exitErr.ExitCode()
				stderr = strings.TrimSpace(string(exitErr.Stderr))
			}
			return &CopyError{Path: rel, Stderr: stderr, ExitCode: code}
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		return &CopyTimeoutError{Paths: paths, Timeout: timeout}
	}
	return nil
}

func cp(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, "cp", args...)
	_, err := cmd.Output() // Output captures stderr into ExitError.Stderr
	return err
}
