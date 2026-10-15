// Package lifecycle prepares a sandbox for an agent and collects its commits.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/sandbox"
)

const (
	DefaultHookTimeout             = 60 * time.Second
	defaultGitSetupTimeout         = 10 * time.Second
	defaultCommitCollectionTimeout = 30 * time.Second
	gitSetupMaxRetries             = 2
	gitSetupRetryDelay             = 250 * time.Millisecond
)

// ExecError reports a command that exited non-zero in the sandbox or on the
// host, or could not be started (ExitCode -1).
type ExecError struct {
	Command  string
	ExitCode int
	Stderr   string
}

func (e *ExecError) Error() string {
	if e.ExitCode < 0 {
		return fmt.Sprintf("command could not be started: %s\n%s", e.Command, e.Stderr)
	}
	return fmt.Sprintf("command failed (exit %d): %s\n%s", e.ExitCode, e.Command, e.Stderr)
}

// HookTimeoutError reports a lifecycle hook that ran longer than Timeout.
type HookTimeoutError struct {
	Command string
	Timeout time.Duration
}

func (e *HookTimeoutError) Error() string {
	return fmt.Sprintf("hook '%s' timed out after %v", e.Command, e.Timeout)
}

// GitSetupTimeoutError reports an in-sandbox git setup command that ran longer than Timeout.
type GitSetupTimeoutError struct {
	Command string
	Timeout time.Duration
}

func (e *GitSetupTimeoutError) Error() string {
	return fmt.Sprintf("git command timed out after %v: %s", e.Timeout, e.Command)
}

// CommitCollectionTimeoutError reports that listing the run's commits ran longer than Timeout.
type CommitCollectionTimeoutError struct{ Timeout time.Duration }

func (e *CommitCollectionTimeoutError) Error() string {
	return fmt.Sprintf("commit collection timed out after %v", e.Timeout)
}

// HostHook runs on the host. Timeout zero means DefaultHookTimeout.
type HostHook struct {
	Command string
	Timeout time.Duration
}

// SandboxHook runs inside the sandbox. Timeout zero means DefaultHookTimeout.
type SandboxHook struct {
	Command string
	Sudo    bool
	Timeout time.Duration
}

// Hooks groups lifecycle hooks by location and moment.
type Hooks struct {
	HostOnWorktreeReady   []HostHook
	HostOnSandboxReady    []HostHook
	SandboxOnSandboxReady []SandboxHook
}

// Timeouts override built-in step timeouts. Zero keeps the default.
type Timeouts struct {
	GitSetup         time.Duration
	CommitCollection time.Duration
}

// Options configure Run.
type Options struct {
	HostRepoDir    string
	SandboxRepoDir string
	// Branch is the branch commits are collected from.
	Branch   string
	Hooks    Hooks
	Timeouts Timeouts
	Display  display.Display
	// SkipGitSetup leaves git's global config alone. Set for no-sandbox
	// providers, where the "sandbox" is the host and already has its identity.
	SkipGitSetup bool
}

// Context is handed to the caller's work function.
type Context struct {
	Sandbox        sandbox.Handle
	SandboxRepoDir string
	BaseHead       string
}

// Result wraps the work's value with the branch and the commits it produced.
type Result[A any] struct {
	Value   A
	Branch  string
	Commits []string
}

// Run sets up git inside the sandbox, runs the onSandboxReady hooks, runs
// work, and returns the commits on opts.Branch made since work started.
func Run[A any](ctx context.Context, opts Options, h sandbox.Handle, work func(Context) (A, error)) (Result[A], error) {
	var zero Result[A]
	gitTimeout := orDefault(opts.Timeouts.GitSetup, defaultGitSetupTimeout)
	collectTimeout := orDefault(opts.Timeouts.CommitCollection, defaultCommitCollectionTimeout)

	hostName := gitx.ConfigValue(ctx, opts.HostRepoDir, "user.name")
	hostEmail := gitx.ConfigValue(ctx, opts.HostRepoDir, "user.email")

	err := opts.Display.Task("Setting up sandbox", func(log func(string)) error {
		var setup []string
		if !opts.SkipGitSetup {
			// The mounted worktree may be owned by another UID; mark it safe so git
			// does not reject it with "dubious ownership".
			setup = append(setup, "git config --global --add safe.directory "+agent.ShellEscape(opts.SandboxRepoDir))
			if hostName != "" {
				setup = append(setup, "git config --global user.name "+agent.ShellEscape(hostName))
			}
			if hostEmail != "" {
				setup = append(setup, "git config --global user.email "+agent.ShellEscape(hostEmail))
			}
		}
		for _, cmd := range setup {
			if _, err := execWithGitTimeout(ctx, h, cmd, "", gitTimeout); err != nil {
				return err
			}
		}
		return runOnSandboxReady(ctx, opts, h, log)
	})
	if err != nil {
		return zero, err
	}

	baseHead, err := gitx.Head(ctx, opts.HostRepoDir)
	if err != nil {
		return zero, err
	}

	value, err := work(Context{Sandbox: h, SandboxRepoDir: opts.SandboxRepoDir, BaseHead: baseHead})
	if err != nil {
		return zero, err
	}

	var commits []string
	err = opts.Display.Task("Collecting commits", func(func(string)) error {
		cctx, cancel := context.WithTimeout(ctx, collectTimeout)
		defer cancel()
		var listErr error
		commits, listErr = gitx.RevList(cctx, opts.HostRepoDir, baseHead, "refs/heads/"+opts.Branch)
		if cctx.Err() == context.DeadlineExceeded {
			return &CommitCollectionTimeoutError{Timeout: collectTimeout}
		}
		return listErr
	})
	if err != nil {
		return zero, err
	}
	return Result[A]{Value: value, Branch: opts.Branch, Commits: commits}, nil
}

func orDefault(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

// execOK runs a command in the sandbox and converts a non-zero exit to ExecError.
func execOK(ctx context.Context, h sandbox.Handle, command, cwd string, sudo bool) (sandbox.ExecResult, error) {
	res, err := h.Exec(ctx, command, sandbox.ExecOptions{Cwd: cwd, Sudo: sudo})
	if err != nil {
		var start *sandbox.StartError
		if errors.As(err, &start) {
			return res, &ExecError{Command: command, ExitCode: -1, Stderr: start.Cause.Error()}
		}
		return res, err
	}
	if res.ExitCode != 0 {
		return res, &ExecError{Command: command, ExitCode: res.ExitCode, Stderr: res.Stderr}
	}
	return res, nil
}

// execWithGitTimeout bounds each attempt and retries exit codes 126 and 137,
// which signal a transient exec race under container load, not a git error.
func execWithGitTimeout(ctx context.Context, h sandbox.Handle, command, cwd string, timeout time.Duration) (sandbox.ExecResult, error) {
	var res sandbox.ExecResult
	var err error
	for attempt := 0; attempt <= gitSetupMaxRetries; attempt++ {
		actx, cancel := context.WithTimeout(ctx, timeout)
		res, err = execOK(actx, h, command, cwd, false)
		cancel()
		if actx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			return res, &GitSetupTimeoutError{Command: command, Timeout: timeout}
		}
		var execErr *ExecError
		if errors.As(err, &execErr) && (execErr.ExitCode == 126 || execErr.ExitCode == 137) {
			time.Sleep(gitSetupRetryDelay)
			continue
		}
		return res, err
	}
	return res, err
}

func runOnSandboxReady(ctx context.Context, opts Options, h sandbox.Handle, log func(string)) error {
	for _, hook := range opts.Hooks.SandboxOnSandboxReady {
		log(hook.Command)
	}
	for _, hook := range opts.Hooks.HostOnSandboxReady {
		log("[host] " + hook.Command)
	}
	total := len(opts.Hooks.SandboxOnSandboxReady) + len(opts.Hooks.HostOnSandboxReady)
	if total == 0 {
		return nil
	}
	errs := make([]error, total)
	var wg sync.WaitGroup
	for i, hook := range opts.Hooks.SandboxOnSandboxReady {
		wg.Add(1)
		go func(i int, hook SandboxHook) {
			defer wg.Done()
			timeout := orDefault(hook.Timeout, DefaultHookTimeout)
			hctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			_, err := execOK(hctx, h, hook.Command, opts.SandboxRepoDir, hook.Sudo)
			if hctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
				err = &HookTimeoutError{Command: hook.Command, Timeout: timeout}
			}
			errs[i] = err
		}(i, hook)
	}
	offset := len(opts.Hooks.SandboxOnSandboxReady)
	for i, hook := range opts.Hooks.HostOnSandboxReady {
		wg.Add(1)
		go func(i int, hook HostHook) {
			defer wg.Done()
			errs[offset+i] = runHostHook(ctx, hook, opts.HostRepoDir)
		}(i, hook)
	}
	wg.Wait()
	return errors.Join(errs...)
}

// RunHostHooks runs hooks sequentially on the host in cwd, failing fast.
func RunHostHooks(ctx context.Context, hooks []HostHook, cwd string) error {
	for _, hook := range hooks {
		if err := runHostHook(ctx, hook, cwd); err != nil {
			return err
		}
	}
	return nil
}

func runHostHook(ctx context.Context, hook HostHook, cwd string) error {
	timeout := orDefault(hook.Timeout, DefaultHookTimeout)
	hctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(hctx, "sh", "-c", hook.Command)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if hctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		return &HookTimeoutError{Command: hook.Command, Timeout: timeout}
	}
	if err != nil {
		code := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		return &ExecError{Command: hook.Command, ExitCode: code, Stderr: "host hook failed: " + strings.TrimSpace(string(out))}
	}
	return nil
}
