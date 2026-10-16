package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/internal/mount"
	"github.com/e11en/agentgopher/internal/worktree"
	"github.com/e11en/agentgopher/sandbox"
)

// SandboxRepoDir is where the worktree is mounted inside a container.
const SandboxRepoDir = mount.SandboxRepoDir

const containerStartTimeout = 120 * time.Second

// ContainerStartTimeoutError reports a provider Create that ran longer than Timeout.
type ContainerStartTimeoutError struct{ Timeout time.Duration }

func (e *ContainerStartTimeoutError) Error() string {
	return fmt.Sprintf("sandbox container start timed out after %v", e.Timeout)
}

type session struct {
	handle           sandbox.Handle
	hostWorktreePath string
	sandboxRepoDir   string
	lifecycleBranch  string
	cleanup          func(ctx context.Context, failed bool) string
}

// startSession prepares the place the agent works in for one iteration.
func startSession(ctx context.Context, o Options) (*session, error) {
	headMode := o.BranchStrategy.Type == "" || o.BranchStrategy.Type == sandbox.BranchHead
	if headMode {
		if err := lifecycle.RunHostHooks(ctx, o.Hooks.HostOnWorktreeReady, o.HostRepoDir); err != nil {
			return nil, err
		}
		h, err := startHandle(ctx, o, o.HostRepoDir)
		if err != nil {
			return nil, err
		}
		return &session{
			handle: h, hostWorktreePath: o.HostRepoDir, sandboxRepoDir: h.WorktreePath(), lifecycleBranch: o.Branch,
			cleanup: func(ctx context.Context, _ bool) string { _ = h.Close(ctx); return "" },
		}, nil
	}

	wt := o.Worktree
	owned := wt == nil
	if owned {
		if err := worktree.Prune(ctx, o.HostRepoDir); err != nil {
			fmt.Fprintln(os.Stderr, "[agentgopher] Warning: failed to prune stale worktrees:", err)
		}
		var info worktree.Info
		var err error
		if o.BranchStrategy.Type == sandbox.BranchNamed {
			info, err = worktree.Create(ctx, o.HostRepoDir, o.BranchStrategy.Branch, o.BranchStrategy.BaseBranch)
		} else {
			info, err = worktree.CreateTemp(ctx, o.HostRepoDir, o.Name)
		}
		if err != nil {
			return nil, err
		}
		wt = &info
	}
	removeWorktree := func(ctx context.Context, failed bool) string {
		if !owned {
			return ""
		}
		defer wt.Unlock()
		dirty, _ := worktree.HasUncommittedChanges(ctx, wt.Path)
		if dirty {
			reason := "Worktree preserved at " + wt.Path
			if !failed {
				reason = "Run succeeded but worktree has uncommitted changes at " + wt.Path
			}
			fmt.Fprintf(os.Stderr, "\n%s\n  To review: cd %s\n  To clean up: git worktree remove --force %s\n", reason, wt.Path, wt.Path)
			return wt.Path
		}
		if failed {
			fmt.Fprintln(os.Stderr, "\nWorktree removed (no uncommitted changes)")
		}
		_ = worktree.Remove(ctx, wt.Path)
		return ""
	}

	fail := func(err error) (*session, error) {
		removeWorktree(ctx, true)
		return nil, err
	}
	if owned && len(o.CopyToWorktree) > 0 {
		err := o.Display.Task("Copying to worktree", func(func(string)) error {
			return worktree.CopyToWorktree(ctx, o.CopyToWorktree, o.HostRepoDir, wt.Path, o.Timeouts.CopyToWorktree)
		})
		if err != nil {
			return fail(err)
		}
	}
	if owned {
		if err := lifecycle.RunHostHooks(ctx, o.Hooks.HostOnWorktreeReady, wt.Path); err != nil {
			return fail(err)
		}
	}
	h, err := startHandle(ctx, o, wt.Path)
	if err != nil {
		return fail(err)
	}
	lifecycleBranch := ""
	if o.BranchStrategy.Type == sandbox.BranchNamed {
		lifecycleBranch = wt.Branch
	}
	return &session{
		handle: h, hostWorktreePath: wt.Path, sandboxRepoDir: h.WorktreePath(), lifecycleBranch: lifecycleBranch,
		cleanup: func(ctx context.Context, failed bool) string {
			_ = h.Close(ctx)
			return removeWorktree(ctx, failed)
		},
	}, nil
}

// startHandle creates the provider handle for worktreeOrRepoPath.
func startHandle(ctx context.Context, o Options, worktreeOrRepoPath string) (sandbox.Handle, error) {
	switch p := o.Sandbox.(type) {
	case sandbox.NoSandboxProvider:
		h, err := p.Create(ctx, sandbox.NoSandboxCreateOptions{WorktreePath: worktreeOrRepoPath, Env: o.Env})
		if err != nil {
			return nil, fmt.Errorf("provider '%s' create failed: %w", p.Name(), err)
		}
		return h, nil
	case sandbox.BindMountProvider:
		return startBindMount(ctx, p, o.HostRepoDir, worktreeOrRepoPath, o.Env)
	default:
		return nil, fmt.Errorf("sandbox provider kind %q is not supported yet; isolated providers arrive in phase 4", o.Sandbox.Kind())
	}
}

// startBindMount resolves the worktree and git mounts, patches them for
// Windows, and asks the provider for a container.
func startBindMount(ctx context.Context, p sandbox.BindMountProvider, hostRepoDir, worktreeOrRepoPath string, env map[string]string) (sandbox.BindMountHandle, error) {
	gitMounts, err := mount.ResolveGitMounts(filepath.Join(hostRepoDir, ".git"))
	if err != nil {
		return nil, &worktree.Error{Message: "failed to resolve git mounts: " + err.Error()}
	}
	gitMounts, err = mount.PatchGitMountsForWindows(gitMounts, worktreeOrRepoPath, SandboxRepoDir, mount.OSFS{}, runtime.GOOS)
	if err != nil {
		return nil, &worktree.Error{Message: err.Error()}
	}
	raw := append([]sandbox.Mount{{HostPath: worktreeOrRepoPath, SandboxPath: SandboxRepoDir}}, gitMounts...)
	mounts := mount.NormalizeMounts(raw, worktreeOrRepoPath, SandboxRepoDir, runtime.GOOS)
	wtPath := worktreeOrRepoPath
	if runtime.GOOS == "windows" {
		wtPath = strings.ReplaceAll(wtPath, `\`, "/")
	}
	cctx, cancel := context.WithTimeout(ctx, containerStartTimeout)
	defer cancel()
	h, err := p.Create(cctx, sandbox.BindMountCreateOptions{WorktreePath: wtPath, HostRepoPath: hostRepoDir, Mounts: mounts, Env: env})
	if cctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		return nil, &ContainerStartTimeoutError{Timeout: containerStartTimeout}
	}
	if err != nil {
		return nil, &worktree.Error{Message: fmt.Sprintf("provider '%s' create failed: %v", p.Name(), err)}
	}
	return h, nil
}
