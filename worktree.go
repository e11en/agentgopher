package agentgopher

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/internal/worktree"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

// CreateWorktreeOptions configure CreateWorktree.
type CreateWorktreeOptions struct {
	// BranchStrategy must be BranchMergeToHead or BranchNamed.
	BranchStrategy sandbox.BranchStrategy
	Cwd            string
	CopyToWorktree []string
	// Hooks: only HostOnWorktreeReady runs here; the rest are passed to Run.
	Hooks    Hooks
	Timeouts Timeouts
}

// WorktreeRunOptions configure Worktree.Run. Sandbox is required.
type WorktreeRunOptions struct {
	Agent             agent.Provider
	Sandbox           sandbox.Provider
	Prompt            string
	PromptFile        string
	PromptArgs        map[string]string
	MaxIterations     int
	CompletionSignal  []string
	IdleTimeout       time.Duration
	CompletionTimeout time.Duration
	Name              string
	Logging           *Logging
	Hooks             Hooks
}

// Worktree is a long-lived git worktree to run agents in.
type Worktree struct {
	Branch string
	Path   string

	hostRepoDir string
	info        worktree.Info
	mergeToHead bool
	closeOnce   sync.Once
}

// CreateWorktree creates (or reuses) a worktree for the strategy, copies
// CopyToWorktree into it and runs HostOnWorktreeReady hooks.
func CreateWorktree(ctx context.Context, o CreateWorktreeOptions) (*Worktree, error) {
	if o.BranchStrategy.Type != sandbox.BranchMergeToHead && o.BranchStrategy.Type != sandbox.BranchNamed {
		return nil, fmt.Errorf("CreateWorktree requires the merge-to-head or branch strategy, got %q (head has no worktree)", o.BranchStrategy.Type)
	}
	hostRepoDir, err := resolveCwd(o.Cwd)
	if err != nil {
		return nil, err
	}
	_ = worktree.Prune(ctx, hostRepoDir)
	var info worktree.Info
	if o.BranchStrategy.Type == sandbox.BranchNamed {
		info, err = worktree.Create(ctx, hostRepoDir, o.BranchStrategy.Branch, o.BranchStrategy.BaseBranch)
	} else {
		info, err = worktree.CreateTemp(ctx, hostRepoDir, "")
	}
	if err != nil {
		return nil, err
	}
	if len(o.CopyToWorktree) > 0 {
		if err := worktree.CopyToWorktree(ctx, o.CopyToWorktree, hostRepoDir, info.Path, o.Timeouts.CopyToWorktree); err != nil {
			info.Unlock()
			return nil, err
		}
	}
	if err := lifecycle.RunHostHooks(ctx, o.Hooks.HostOnWorktreeReady, info.Path); err != nil {
		info.Unlock()
		return nil, err
	}
	return &Worktree{Branch: info.Branch, Path: info.Path, hostRepoDir: hostRepoDir, info: info, mergeToHead: o.BranchStrategy.Type == sandbox.BranchMergeToHead}, nil
}

// Run runs an agent in this worktree. With merge-to-head the commits are
// merged into the host branch after the run and the worktree stays on its
// branch for the next Run.
func (w *Worktree) Run(ctx context.Context, o WorktreeRunOptions) (RunResult, error) {
	if o.Sandbox == nil {
		return RunResult{}, fmt.Errorf("Worktree.Run requires a Sandbox")
	}
	return runWith(ctx, RunOptions{
		Agent: o.Agent, Sandbox: o.Sandbox, Cwd: w.hostRepoDir, Prompt: o.Prompt, PromptFile: o.PromptFile,
		PromptArgs: o.PromptArgs, MaxIterations: o.MaxIterations, CompletionSignal: o.CompletionSignal,
		IdleTimeout: o.IdleTimeout, CompletionTimeout: o.CompletionTimeout, Name: o.Name, Logging: o.Logging, Hooks: o.Hooks,
	}, &w.info, w.mergeToHead)
}

// Close removes the worktree when it is clean and keeps it when it has
// uncommitted changes. Calling Close twice is safe.
func (w *Worktree) Close(ctx context.Context) error {
	var err error
	w.closeOnce.Do(func() {
		defer w.info.Unlock()
		dirty, derr := worktree.HasUncommittedChanges(ctx, w.Path)
		if derr == nil && dirty {
			return
		}
		err = worktree.Remove(ctx, w.Path)
	})
	return err
}

// createWorktree creates the worktree a non-head strategy needs.
func createWorktree(ctx context.Context, hostRepoDir string, strategy sandbox.BranchStrategy, name string) (worktree.Info, error) {
	if strategy.Type == sandbox.BranchNamed {
		return worktree.Create(ctx, hostRepoDir, strategy.Branch, strategy.BaseBranch)
	}
	return worktree.CreateTemp(ctx, hostRepoDir, name)
}

// WorktreeCreateSandboxOptions configure (*Worktree).CreateSandbox.
type WorktreeCreateSandboxOptions struct {
	Sandbox        sandbox.Provider
	Hooks          Hooks
	CopyToWorktree []string
	Timeouts       Timeouts
}

// CreateSandbox starts a sandbox on this worktree. Close tears down the
// sandbox only; the worktree belongs to the Worktree handle.
func (w *Worktree) CreateSandbox(ctx context.Context, o WorktreeCreateSandboxOptions) (*Sandbox, error) {
	started, err := startOnWorktree(ctx, o.Sandbox, w.hostRepoDir, w.info, o.Hooks, o.CopyToWorktree, o.Timeouts)
	if err != nil {
		return nil, err
	}
	strategy := sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: w.Branch}
	if w.mergeToHead {
		strategy = sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead}
	}
	sb := newSandbox(w.hostRepoDir, w.Path, w.Branch, strategy, started, o.Sandbox, o.Timeouts)
	sb.closeFn = func(ctx context.Context) (CloseResult, error) {
		return CloseResult{}, started.Handle.Close(ctx)
	}
	return sb, nil
}

// WorktreeInteractiveOptions configure (*Worktree).Interactive.
type WorktreeInteractiveOptions struct {
	Agent          agent.Provider
	Sandbox        sandbox.Provider // nil means nosandbox.New(nil)
	Prompt         string
	PromptFile     string
	PromptArgs     map[string]string
	Name           string
	Hooks          Hooks
	CopyToWorktree []string
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
}

// Interactive runs an interactive session on this worktree.
func (w *Worktree) Interactive(ctx context.Context, o WorktreeInteractiveOptions) (InteractiveResult, error) {
	if o.Sandbox == nil {
		o.Sandbox = nosandbox.New(nil)
	}
	sb, err := w.CreateSandbox(ctx, WorktreeCreateSandboxOptions{Sandbox: o.Sandbox, Hooks: o.Hooks, CopyToWorktree: o.CopyToWorktree})
	if err != nil {
		return InteractiveResult{}, err
	}
	defer func() { _, _ = sb.Close(ctx) }()
	r, err := sb.Interactive(ctx, SandboxInteractiveOptions{Agent: o.Agent, Prompt: o.Prompt, PromptFile: o.PromptFile, PromptArgs: o.PromptArgs, Name: o.Name, Stdin: o.Stdin, Stdout: o.Stdout, Stderr: o.Stderr})
	if err != nil {
		return InteractiveResult{}, err
	}
	return InteractiveResult{Commits: r.Commits, Branch: w.Branch, ExitCode: r.ExitCode}, nil
}
