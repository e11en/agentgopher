package agentgopher

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/env"
	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/internal/orchestrator"
	"github.com/e11en/agentgopher/internal/prompt"
	"github.com/e11en/agentgopher/internal/sync"
	"github.com/e11en/agentgopher/internal/worktree"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

// InteractiveOptions configure Interactive.
type InteractiveOptions struct {
	Agent          agent.Provider
	Sandbox        sandbox.Provider
	Prompt         string
	PromptFile     string
	Name           string
	BranchStrategy *sandbox.BranchStrategy
	Hooks          Hooks
	CopyToWorktree []string
	PromptArgs     map[string]string
	Env            map[string]string
	Cwd            string
	Timeouts       Timeouts
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
}

// InteractiveResult is the outcome of an interactive session.
type InteractiveResult struct {
	Commits               []Commit
	Branch                string
	PreservedWorktreePath string
	ExitCode              int
}

// collectMissingArgs asks the terminal for every {{KEY}} that args does not
// provide. An empty answer is rejected and asked again; EOF fails.
func collectMissingArgs(text string, args prompt.Args, in io.Reader, out io.Writer) (prompt.Args, error) {
	merged := prompt.Args{}
	for k, v := range args {
		merged[k] = v
	}
	reader := bufio.NewReader(in)
	for _, key := range prompt.FindMissingKeys(text, args) {
		for {
			_, _ = fmt.Fprintf(out, "Enter value for {{%s}}: ", key)
			line, err := reader.ReadString('\n')
			if err != nil && line == "" {
				return nil, errors.New("prompt arg collection cancelled")
			}
			line = strings.TrimSpace(line)
			if line != "" {
				merged[key] = line
				break
			}
			_, _ = fmt.Fprintf(out, "A value is required for {{%s}}\n", key)
		}
	}
	return merged, nil
}

// Interactive launches the agent's own TUI inside a sandbox and collects the
// commits it made when the session ends.
func Interactive(ctx context.Context, o InteractiveOptions) (InteractiveResult, error) {
	if err := ctx.Err(); err != nil {
		return InteractiveResult{}, err
	}
	if o.Sandbox == nil {
		o.Sandbox = nosandbox.New(nil)
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
	builder, ok := o.Agent.(agent.InteractiveArgsBuilder)
	if !ok {
		return InteractiveResult{}, fmt.Errorf("agent provider %q does not support interactive sessions", o.Agent.Name())
	}

	strategy := sandbox.BranchStrategy{Type: sandbox.BranchHead}
	if o.Sandbox.Kind() == sandbox.KindIsolated {
		strategy.Type = sandbox.BranchMergeToHead
	}
	if o.BranchStrategy != nil {
		strategy = *o.BranchStrategy
	}
	if strategy.Type == sandbox.BranchHead && o.Sandbox.Kind() == sandbox.KindIsolated {
		return InteractiveResult{}, errors.New("head branch strategy is not supported with isolated providers")
	}
	if strategy.Type == sandbox.BranchHead && len(o.CopyToWorktree) > 0 {
		return InteractiveResult{}, errors.New("CopyToWorktree is not supported with the head branch strategy")
	}
	headMode := strategy.Type == sandbox.BranchHead

	hostRepoDir, err := resolveCwd(o.Cwd)
	if err != nil {
		return InteractiveResult{}, err
	}
	d := display.NewTerminal(o.Stdout)

	hasPrompt := o.Prompt != "" || o.PromptFile != ""
	resolvedText, inline := "", false
	if hasPrompt {
		r, err := prompt.Resolve(o.Prompt, o.PromptFile)
		if err != nil {
			return InteractiveResult{}, err
		}
		resolvedText, inline = r.Text, r.Source == prompt.SourceInline
	}

	resolvedEnv, err := env.Resolve(hostRepoDir)
	if err != nil {
		return InteractiveResult{}, err
	}
	merged, err := env.MergeProviderEnv(resolvedEnv, o.Agent.Env(), o.Sandbox.Env())
	if err != nil {
		return InteractiveResult{}, err
	}
	for k, v := range o.Env {
		merged[k] = v
	}

	currentBranch, err := gitx.CurrentBranch(ctx, hostRepoDir)
	if err != nil {
		return InteractiveResult{}, err
	}
	resolvedBranch := currentBranch
	switch strategy.Type {
	case sandbox.BranchNamed:
		resolvedBranch = strategy.Branch
	case sandbox.BranchMergeToHead:
		resolvedBranch = worktree.GenerateTempBranchName(o.Name)
	}

	userArgs := prompt.Args(o.PromptArgs)
	substituted := resolvedText
	switch {
	case hasPrompt && !inline:
		if err := prompt.ValidateNoBuiltInOverride(userArgs); err != nil {
			return InteractiveResult{}, err
		}
		collected, err := collectMissingArgs(resolvedText, userArgs, o.Stdin, o.Stdout)
		if err != nil {
			return InteractiveResult{}, err
		}
		args := prompt.Args{"SOURCE_BRANCH": resolvedBranch, "TARGET_BRANCH": currentBranch}
		for k, v := range collected {
			args[k] = v
		}
		silent := map[string]bool{"SOURCE_BRANCH": true, "TARGET_BRANCH": true}
		substituted, err = prompt.Substitute(resolvedText, args, silent, func(m string) { d.Status(m, display.SeverityWarn) })
		if err != nil {
			return InteractiveResult{}, err
		}
	case inline:
		if err := prompt.ValidateNoArgsWithInlinePrompt(userArgs); err != nil {
			return InteractiveResult{}, err
		}
	}

	d.Intro(orDefault(o.Name, "agentgopher interactive"))
	d.Summary("Interactive Session", [][2]string{{"Agent", orDefault(o.Name, o.Agent.Name())}, {"Sandbox", o.Sandbox.Name()}, {"Branch", resolvedBranch}})

	var wt *worktree.Info
	if !headMode {
		var info worktree.Info
		err := d.Task("Creating worktree", func(func(string)) error {
			_ = worktree.Prune(ctx, hostRepoDir)
			var err error
			info, err = createWorktree(ctx, hostRepoDir, strategy, o.Name)
			return err
		})
		if err != nil {
			return InteractiveResult{}, err
		}
		wt = &info
	}
	removeWorktree := func() {
		if wt != nil {
			_ = worktree.Remove(ctx, wt.Path)
		}
	}

	worktreeOrRepo := hostRepoDir
	if wt != nil {
		worktreeOrRepo = wt.Path
	}
	if wt != nil && len(o.CopyToWorktree) > 0 && o.Sandbox.Kind() != sandbox.KindIsolated {
		if err := d.Task("Copying files to worktree", func(func(string)) error {
			return worktree.CopyToWorktree(ctx, o.CopyToWorktree, hostRepoDir, wt.Path, 0)
		}); err != nil {
			removeWorktree()
			return InteractiveResult{}, err
		}
	}
	if err := lifecycle.RunHostHooks(ctx, o.Hooks.HostOnWorktreeReady, worktreeOrRepo); err != nil {
		removeWorktree()
		return InteractiveResult{}, err
	}

	var started *orchestrator.Session
	err = d.Task("Starting sandbox", func(func(string)) error {
		var err error
		// wt is nil in head mode; startSession then works on hostRepoDir directly.
		started, err = orchestrator.StartSession(ctx, orchestrator.Options{Sandbox: o.Sandbox, HostRepoDir: hostRepoDir, Worktree: wt, BranchStrategy: strategy, Env: merged, CopyToWorktree: o.CopyToWorktree, Hooks: o.Hooks, Timeouts: o.Timeouts, Display: d, Name: o.Name})
		return err
	})
	if err != nil {
		removeWorktree()
		return InteractiveResult{}, err
	}
	defer func() { _ = started.Handle.Close(ctx) }()

	ix, ok := started.Handle.(sandbox.Interactive)
	if !ok {
		removeWorktree()
		return InteractiveResult{}, fmt.Errorf("sandbox provider %q does not support interactive sessions", o.Sandbox.Name())
	}

	lcOpts := lifecycle.Options{
		HostRepoDir:      hostRepoDir,
		SandboxRepoDir:   started.SandboxRepoDir,
		HostWorktreePath: worktreeOrRepo,
		Hooks:            o.Hooks,
		Timeouts:         o.Timeouts,
		Display:          d,
	}
	switch strategy.Type {
	case sandbox.BranchHead:
		lcOpts.Branch = currentBranch
	case sandbox.BranchNamed:
		lcOpts.Branch = strategy.Branch
	}
	if started.Isolated != nil && wt != nil {
		lcOpts.ApplyToHost = func(ctx context.Context) error {
			return sync.Out(ctx, started.Isolated, wt.Path, started.SandboxRepoDir)
		}
	}

	lc, err := lifecycle.Run(ctx, lcOpts, started.Handle, func(lctx lifecycle.Context) (int, error) {
		full := substituted
		if hasPrompt && !inline {
			err := d.Task("Expanding shell expressions", func(log func(string)) error {
				var expandErr error
				full, expandErr = prompt.Expand(ctx, substituted, lctx.Sandbox, lctx.SandboxRepoDir, log)
				return expandErr
			})
			if err != nil {
				return 0, err
			}
		}
		args := builder.BuildInteractiveArgs(agent.CommandOptions{Prompt: full, DangerouslySkipPermissions: o.Sandbox.Kind() != sandbox.KindNone})
		return ix.InteractiveExec(ctx, args, sandbox.InteractiveExecOptions{Stdin: o.Stdin, Stdout: o.Stdout, Stderr: o.Stderr, Cwd: lctx.SandboxRepoDir})
	})
	if err != nil {
		removeWorktree()
		return InteractiveResult{}, err
	}

	preserved := ""
	if wt != nil {
		if dirty, _ := worktree.HasUncommittedChanges(ctx, wt.Path); dirty {
			preserved = wt.Path
		} else {
			removeWorktree()
		}
	}
	rows := [][2]string{{"Commits", fmt.Sprint(len(lc.Commits))}, {"Branch", lc.Branch}, {"Exit code", fmt.Sprint(lc.Value)}}
	if preserved != "" {
		rows = append(rows, [2]string{"Preserved worktree", preserved})
	}
	d.Summary("Session Complete", rows)

	commits := make([]Commit, len(lc.Commits))
	for i, sha := range lc.Commits {
		commits[i] = Commit{SHA: sha}
	}
	return InteractiveResult{Commits: commits, Branch: lc.Branch, PreservedWorktreePath: preserved, ExitCode: lc.Value}, nil
}
