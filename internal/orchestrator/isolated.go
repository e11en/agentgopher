package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/sync"
	"github.com/e11en/agentgopher/internal/worktree"
	"github.com/e11en/agentgopher/sandbox"
)

// CopyPathsTimeout bounds copying CopyToWorktree paths into an isolated sandbox.
const CopyPathsTimeout = 60 * time.Second

// startIsolated creates an isolated sandbox for the worktree, ships the repo
// in and copies the CopyToWorktree paths after it.
func startIsolated(ctx context.Context, o Options, p sandbox.IsolatedProvider, wt worktree.Info) (sandbox.IsolatedHandle, error) {
	cctx, cancel := context.WithTimeout(ctx, containerStartTimeout)
	h, err := p.Create(cctx, sandbox.IsolatedCreateOptions{Env: o.Env})
	cancel()
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			return nil, &ContainerStartTimeoutError{Timeout: containerStartTimeout}
		}
		return nil, fmt.Errorf("isolated provider '%s' setup failed: %w", p.Name(), err)
	}
	repoDir := h.WorktreePath()
	err = o.Display.Task("Syncing repo into sandbox", func(func(string)) error {
		return sync.In(ctx, h, wt.Path, repoDir, wt.Branch, sync.DefaultInTimeout)
	})
	if err == nil {
		err = copyPathsIn(ctx, h, o.HostRepoDir, repoDir, o.CopyToWorktree)
	}
	if err != nil {
		_ = h.Close(ctx)
		return nil, err
	}
	return h, nil
}

// applyToHost returns the lifecycle's ApplyToHost step for an isolated
// sandbox: ship the sandbox commits, diff and untracked files to the host
// worktree. baseHead is read when the step runs, after the lifecycle set it.
func applyToHost(h sandbox.IsolatedHandle, hostWorktreePath, repoDir string, d display.Display, baseHead *string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		n, _ := sync.CountCommitsToSync(ctx, h, repoDir, *baseHead)
		title := "No commits to sync out"
		switch {
		case n == 1:
			title = "Syncing 1 commit to host"
		case n > 1:
			title = fmt.Sprintf("Syncing %d commits to host", n)
		}
		return d.Task(title, func(func(string)) error { return sync.Out(ctx, h, hostWorktreePath, repoDir) })
	}
}

// copyPathsIn copies CopyToWorktree paths that exist on the host into the
// sandbox repo. Sandbox paths are POSIX regardless of the host platform.
func copyPathsIn(ctx context.Context, h sandbox.IsolatedHandle, hostRepoDir, repoDir string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, CopyPathsTimeout)
	defer cancel()
	for _, rel := range paths {
		hostPath := filepath.Join(hostRepoDir, rel)
		if _, err := os.Stat(hostPath); err != nil {
			continue
		}
		if err := h.CopyIn(cctx, hostPath, path.Join(repoDir, filepath.ToSlash(rel))); err != nil {
			if cctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
				return &worktree.CopyTimeoutError{Timeout: CopyPathsTimeout, Paths: paths}
			}
			return fmt.Errorf("failed to copy %s into sandbox: %w", rel, err)
		}
	}
	return nil
}
