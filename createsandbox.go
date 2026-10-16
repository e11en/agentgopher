package agentgopher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/env"
	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/internal/orchestrator"
	"github.com/e11en/agentgopher/internal/prompt"
	"github.com/e11en/agentgopher/internal/shutdown"
	gitsync "github.com/e11en/agentgopher/internal/sync"
	"github.com/e11en/agentgopher/internal/worktree"
	"github.com/e11en/agentgopher/sandbox"
)

// CreateSandboxOptions configure CreateSandbox.
type CreateSandboxOptions struct {
	// Branch is the explicit branch for the worktree (required).
	Branch string
	// BaseBranch is forked from when Branch does not exist yet. Default HEAD.
	BaseBranch     string
	Sandbox        sandbox.Provider
	Cwd            string
	Hooks          Hooks
	CopyToWorktree []string
	Timeouts       Timeouts
}

// SandboxRunOptions configure (*Sandbox).Run.
type SandboxRunOptions struct {
	Agent             agent.Provider
	Prompt            string
	PromptFile        string
	MaxIterations     int
	PromptArgs        map[string]string
	CompletionSignal  []string
	IdleTimeout       time.Duration
	CompletionTimeout time.Duration
	Name              string
	Logging           *Logging
	ResumeSession     string
	forkSession       bool
}

// SandboxRunResult is the outcome of (*Sandbox).Run.
type SandboxRunResult struct {
	Iterations       []IterationResult
	CompletionSignal string
	Stdout           string
	Commits          []Commit
	LogFilePath      string
	sb               *Sandbox
	opts             SandboxRunOptions
}

// SandboxInteractiveOptions configure (*Sandbox).Interactive.
type SandboxInteractiveOptions struct {
	Agent      agent.Provider
	Prompt     string
	PromptFile string
	PromptArgs map[string]string
	Name       string
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

// SandboxInteractiveResult is the outcome of (*Sandbox).Interactive.
type SandboxInteractiveResult struct {
	Commits  []Commit
	ExitCode int
}

// SandboxExecOptions mirror the provider handle's exec options.
type SandboxExecOptions = sandbox.ExecOptions

// CloseResult reports a worktree kept because it had uncommitted changes.
type CloseResult struct{ PreservedWorktreePath string }

// Sandbox is a long-lived worktree plus sandbox reused across runs.
type Sandbox struct {
	Branch       string
	WorktreePath string

	hostRepoDir string
	repoDir     string
	handle      sandbox.Handle
	provider    sandbox.Provider
	strategy    sandbox.BranchStrategy
	applyToHost func(context.Context) error
	timeouts    Timeouts
	closeMu     sync.Mutex
	closed      bool
	closeFn     func(ctx context.Context) (CloseResult, error)
}

func toCommits(shas []string) []Commit {
	out := make([]Commit, len(shas))
	for i, s := range shas {
		out[i] = Commit{SHA: s}
	}
	return out
}

// startOnWorktree starts the provider sandbox on an existing worktree and runs
// the onSandboxReady hooks once.
func startOnWorktree(ctx context.Context, provider sandbox.Provider, hostRepoDir string, info worktree.Info, hooks Hooks, copyPaths []string, timeouts Timeouts) (*orchestrator.Session, error) {
	worktreePath := info.Path
	if len(copyPaths) > 0 && provider.Kind() != sandbox.KindIsolated {
		if err := worktree.CopyToWorktree(ctx, copyPaths, hostRepoDir, worktreePath, 0); err != nil {
			return nil, err
		}
	}
	if err := lifecycle.RunHostHooks(ctx, hooks.HostOnWorktreeReady, worktreePath); err != nil {
		return nil, err
	}
	resolvedEnv, err := env.Resolve(hostRepoDir)
	if err != nil {
		return nil, err
	}
	merged, err := env.MergeProviderEnv(resolvedEnv, nil, provider.Env())
	if err != nil {
		return nil, err
	}
	started, err := orchestrator.StartSession(ctx, orchestrator.Options{Sandbox: provider, HostRepoDir: hostRepoDir, Worktree: &info, BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: info.Branch}, Env: merged, CopyToWorktree: copyPaths, Hooks: hooks, Timeouts: timeouts, Display: display.NewWriter(io.Discard)})
	if err != nil {
		return nil, err
	}
	if len(hooks.SandboxOnSandboxReady) > 0 || len(hooks.HostOnSandboxReady) > 0 {
		// Hooks run through the lifecycle's setup step with no work and no
		// commit collection; the Branch is the worktree's own so nothing merges.
		_, err := lifecycle.Run(ctx, lifecycle.Options{
			HostRepoDir: hostRepoDir, SandboxRepoDir: started.SandboxRepoDir, HostWorktreePath: worktreePath,
			Branch: "HEAD", Hooks: Hooks{SandboxOnSandboxReady: hooks.SandboxOnSandboxReady, HostOnSandboxReady: hooks.HostOnSandboxReady},
			Timeouts: timeouts, Display: display.NewWriter(io.Discard),
		}, started.Handle, func(lifecycle.Context) (struct{}, error) { return struct{}{}, nil })
		if err != nil {
			_ = started.Handle.Close(ctx)
			return nil, err
		}
	}
	return started, nil
}

// CreateSandbox creates a worktree on an explicit branch and starts a sandbox
// on it. The returned Sandbox is reused across Run, Interactive and Exec
// calls until Close.
func CreateSandbox(ctx context.Context, o CreateSandboxOptions) (*Sandbox, error) {
	if o.Branch == "" {
		return nil, errors.New("CreateSandbox requires Branch")
	}
	hostRepoDir, err := resolveCwd(o.Cwd)
	if err != nil {
		return nil, err
	}
	_ = worktree.Prune(ctx, hostRepoDir)
	info, err := createWorktree(ctx, hostRepoDir, sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: o.Branch, BaseBranch: o.BaseBranch}, "")
	if err != nil {
		return nil, err
	}
	started, err := startOnWorktree(ctx, o.Sandbox, hostRepoDir, info, o.Hooks, o.CopyToWorktree, o.Timeouts)
	if err != nil {
		_ = worktree.Remove(ctx, info.Path)
		return nil, err
	}

	sb := newSandbox(hostRepoDir, info.Path, o.Branch, sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: o.Branch}, started, o.Sandbox, o.Timeouts)
	unregister := shutdown.Register(func() {
		fmt.Fprintf(os.Stderr, "\nWorktree preserved at %s\n  To review: cd %s\n  To clean up: git worktree remove --force %s\n", info.Path, info.Path, info.Path)
	})
	sb.closeFn = func(ctx context.Context) (CloseResult, error) {
		unregister()
		_ = started.Handle.Close(ctx)
		info.Unlock()
		if dirty, _ := worktree.HasUncommittedChanges(ctx, info.Path); dirty {
			return CloseResult{PreservedWorktreePath: info.Path}, nil
		}
		_ = worktree.Remove(ctx, info.Path)
		return CloseResult{}, nil
	}
	return sb, nil
}

func newSandbox(hostRepoDir, worktreePath, branch string, strategy sandbox.BranchStrategy, started *orchestrator.Session, provider sandbox.Provider, timeouts Timeouts) *Sandbox {
	sb := &Sandbox{Branch: branch, WorktreePath: worktreePath, hostRepoDir: hostRepoDir, repoDir: started.SandboxRepoDir, handle: started.Handle, provider: provider, strategy: strategy, timeouts: timeouts}
	if started.Isolated != nil {
		sb.applyToHost = func(ctx context.Context) error {
			return gitsync.Out(ctx, started.Isolated, worktreePath, started.SandboxRepoDir)
		}
	}
	return sb
}

// lifecycleBranch is "" in merge-to-head mode (the lifecycle merges into the
// host branch and keeps the worktree's source branch), else the branch itself.
func (s *Sandbox) lifecycleBranch() string {
	if s.strategy.Type == sandbox.BranchMergeToHead {
		return ""
	}
	return s.Branch
}

func (s *Sandbox) existing() *orchestrator.Existing {
	return &orchestrator.Existing{Handle: s.handle, SandboxRepoDir: s.repoDir, HostWorktreePath: s.WorktreePath, ApplyToHost: s.applyToHost, KeepSourceBranch: s.strategy.Type == sandbox.BranchMergeToHead}
}

func (s *Sandbox) resolvePrompt(ctx context.Context, inline, file string, args map[string]string, warn func(string)) (text string, isInline bool, err error) {
	r, err := prompt.Resolve(inline, file)
	if err != nil {
		return "", false, err
	}
	userArgs := prompt.Args(args)
	if r.Source == prompt.SourceInline {
		return r.Text, true, prompt.ValidateNoArgsWithInlinePrompt(userArgs)
	}
	if err := prompt.ValidateNoBuiltInOverride(userArgs); err != nil {
		return "", false, err
	}
	currentBranch, err := gitx.CurrentBranch(ctx, s.hostRepoDir)
	if err != nil {
		return "", false, err
	}
	all := prompt.Args{"SOURCE_BRANCH": s.Branch, "TARGET_BRANCH": currentBranch}
	for k, v := range userArgs {
		all[k] = v
	}
	text, err = prompt.Substitute(r.Text, all, map[string]bool{"SOURCE_BRANCH": true, "TARGET_BRANCH": true}, warn)
	return text, false, err
}

// Run invokes an agent inside the existing sandbox.
func (s *Sandbox) Run(ctx context.Context, o SandboxRunOptions) (SandboxRunResult, error) {
	if err := ctx.Err(); err != nil {
		return SandboxRunResult{}, err
	}
	maxIterations := o.MaxIterations
	if maxIterations == 0 {
		maxIterations = DefaultMaxIterations
	}
	if o.ResumeSession != "" && maxIterations > 1 {
		return SandboxRunResult{}, errors.New("ResumeSession cannot be combined with MaxIterations > 1. Resume applies to iteration 1 only (ADR 0011)")
	}
	if o.forkSession && o.ResumeSession == "" {
		return SandboxRunResult{}, errors.New("forkSession requires ResumeSession; use SandboxRunResult.Fork")
	}
	if o.ResumeSession != "" {
		if err := orchestrator.AssertResumeSessionExists(o.Agent, s.provider.Kind(), s.hostRepoDir, o.ResumeSession); err != nil {
			return SandboxRunResult{}, err
		}
	}

	logging := Logging{Type: LoggingFile}
	if o.Logging != nil {
		logging = *o.Logging
	}
	if logging.Type == LoggingFile && logging.Path == "" {
		logging.Path = filepath.Join(s.hostRepoDir, ".agentgopher", "logs", BuildLogFilename(s.Branch, "", o.Name))
	}
	var d display.Display
	logFilePath := ""
	if logging.Type == LoggingFile {
		f, err := display.OpenLogFile(logging.Path)
		if err != nil {
			return SandboxRunResult{}, err
		}
		defer func() { _ = f.Close() }()
		logFilePath = logging.Path
		printFileStartup(o.Name, s.Branch, logging.Path, s.hostRepoDir)
		d = display.NewWriter(f)
	} else {
		// Upstream keeps sandbox runs silent in stdout mode; the plain writer on
		// stdout is the closest useful behaviour.
		d = display.NewWriter(os.Stdout)
	}

	text, inline, err := s.resolvePrompt(ctx, o.Prompt, o.PromptFile, o.PromptArgs, func(m string) { d.Status(m, display.SeverityWarn) })
	if err != nil {
		return SandboxRunResult{}, logError(d, err)
	}
	d.Intro(orDefault(o.Name, "agentgopher"))

	result, err := orchestrator.Orchestrate(ctx, orchestrator.Options{
		HostRepoDir:         s.hostRepoDir,
		Iterations:          maxIterations,
		Prompt:              text,
		Branch:              s.lifecycleBranch(),
		Provider:            o.Agent,
		Sandbox:             s.provider,
		CompletionSignals:   o.CompletionSignal,
		IdleTimeout:         o.IdleTimeout,
		CompletionTimeout:   o.CompletionTimeout,
		Name:                o.Name,
		ResumeSession:       o.ResumeSession,
		ForkSession:         o.forkSession,
		SkipPromptExpansion: inline,
		Timeouts:            s.timeouts,
		Display:             d,
		OnStreamEvent:       buildStreamHandler(logging, d),
		Existing:            s.existing(),
	})
	if err != nil {
		if ctx.Err() != nil {
			return SandboxRunResult{}, ctx.Err()
		}
		return SandboxRunResult{}, logError(d, err)
	}
	if result.CompletionSignal != "" {
		d.Status(fmt.Sprintf("Run complete: agent finished after %d iteration(s).", len(result.Iterations)), display.SeveritySuccess)
	} else {
		d.Status(fmt.Sprintf("Run complete: reached %d iteration(s) without completion signal.", len(result.Iterations)), display.SeverityWarn)
	}
	return SandboxRunResult{Iterations: result.Iterations, CompletionSignal: result.CompletionSignal, Stdout: result.Stdout, Commits: toCommits(result.Commits), LogFilePath: logFilePath, sb: s, opts: o}, nil
}

func (r SandboxRunResult) continueRun(ctx context.Context, p string, o *ResumeOptions, fork bool) (SandboxRunResult, error) {
	if r.sb == nil || len(r.Iterations) == 0 || r.Iterations[len(r.Iterations)-1].SessionID == "" {
		return SandboxRunResult{}, errors.New("cannot resume: no session id was captured")
	}
	next := r.opts
	next.Prompt, next.PromptFile, next.PromptArgs = p, "", nil
	next.MaxIterations = 1
	next.ResumeSession = r.Iterations[len(r.Iterations)-1].SessionID
	next.forkSession = fork
	if o != nil {
		if o.PromptArgs != nil {
			next.PromptArgs = o.PromptArgs
		}
		if o.Logging != nil {
			next.Logging = o.Logging
		}
		if o.CompletionSignal != nil {
			next.CompletionSignal = o.CompletionSignal
		}
		if o.IdleTimeout != 0 {
			next.IdleTimeout = o.IdleTimeout
		}
		if o.CompletionTimeout != 0 {
			next.CompletionTimeout = o.CompletionTimeout
		}
		if o.Name != "" {
			next.Name = o.Name
		}
	}
	return r.sb.Run(ctx, next)
}

// Resume continues the last captured session for one iteration in the same sandbox.
func (r SandboxRunResult) Resume(ctx context.Context, p string, o *ResumeOptions) (SandboxRunResult, error) {
	return r.continueRun(ctx, p, o, false)
}

// Fork continues the last captured session as a new session (ADR 0018).
func (r SandboxRunResult) Fork(ctx context.Context, p string, o *ResumeOptions) (SandboxRunResult, error) {
	return r.continueRun(ctx, p, o, true)
}

// Interactive launches the agent's TUI inside the existing sandbox.
func (s *Sandbox) Interactive(ctx context.Context, o SandboxInteractiveOptions) (SandboxInteractiveResult, error) {
	if err := ctx.Err(); err != nil {
		return SandboxInteractiveResult{}, err
	}
	builder, ok := o.Agent.(agent.InteractiveArgsBuilder)
	if !ok {
		return SandboxInteractiveResult{}, fmt.Errorf("agent provider %q does not support interactive sessions", o.Agent.Name())
	}
	ix, ok := s.handle.(sandbox.Interactive)
	if !ok {
		return SandboxInteractiveResult{}, fmt.Errorf("sandbox provider %q does not support interactive sessions", s.provider.Name())
	}
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	d := display.NewTerminal(o.Stdout)
	text, inline, err := s.resolvePrompt(ctx, o.Prompt, o.PromptFile, o.PromptArgs, func(m string) { d.Status(m, display.SeverityWarn) })
	if err != nil {
		return SandboxInteractiveResult{}, err
	}
	lc, err := lifecycle.Run(ctx, lifecycle.Options{
		HostRepoDir: s.hostRepoDir, SandboxRepoDir: s.repoDir, HostWorktreePath: s.WorktreePath,
		Branch: s.lifecycleBranch(), Timeouts: s.timeouts, Display: d,
		ApplyToHost: s.applyToHost, KeepSourceBranch: s.strategy.Type == sandbox.BranchMergeToHead,
	}, s.handle, func(lctx lifecycle.Context) (int, error) {
		full := text
		if !inline {
			var err error
			full, err = prompt.Expand(ctx, text, lctx.Sandbox, lctx.SandboxRepoDir, nil)
			if err != nil {
				return 0, err
			}
		}
		args := builder.BuildInteractiveArgs(agent.CommandOptions{Prompt: full, DangerouslySkipPermissions: true})
		return ix.InteractiveExec(ctx, args, sandbox.InteractiveExecOptions{Stdin: o.Stdin, Stdout: o.Stdout, Stderr: o.Stderr, Cwd: s.repoDir})
	})
	if err != nil {
		return SandboxInteractiveResult{}, err
	}
	return SandboxInteractiveResult{Commits: toCommits(lc.Commits), ExitCode: lc.Value}, nil
}

// Exec runs a command inside the sandbox. Cwd defaults to the repo path.
// A non-zero exit code is returned in the result, not as an error.
func (s *Sandbox) Exec(ctx context.Context, command string, o SandboxExecOptions) (sandbox.ExecResult, error) {
	if o.Cwd == "" {
		o.Cwd = s.repoDir
	}
	return s.handle.Exec(ctx, command, o)
}

// Close tears the sandbox down. For CreateSandbox it also removes the
// worktree unless it has uncommitted changes. Safe to call twice.
func (s *Sandbox) Close(ctx context.Context) (CloseResult, error) {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return CloseResult{}, nil
	}
	s.closed = true
	return s.closeFn(ctx)
}
