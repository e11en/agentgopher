package agentgopher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/env"
	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/internal/orchestrator"
	"github.com/e11en/agentgopher/internal/prompt"
	"github.com/e11en/agentgopher/internal/worktree"
	"github.com/e11en/agentgopher/sandbox"
)

// DefaultMaxIterations is used when RunOptions.MaxIterations is zero.
const DefaultMaxIterations = 1

type (
	// HostHook runs on the host. Timeout zero means 60 s.
	HostHook = lifecycle.HostHook
	// SandboxHook runs inside the sandbox. Timeout zero means 60 s.
	SandboxHook = lifecycle.SandboxHook
	// Hooks groups lifecycle hooks by location and moment.
	Hooks = lifecycle.Hooks
	// Timeouts override built-in step timeouts. Zero keeps the default.
	Timeouts = lifecycle.Timeouts
	// StreamEvent is one item of the agent's output stream.
	StreamEvent = orchestrator.StreamEvent
	// IterationResult is the outcome of one agent invocation.
	IterationResult = orchestrator.IterationResult
)

// LoggingType selects where progress is written.
type LoggingType string

const (
	// LoggingFile writes progress and agent output to a log file.
	LoggingFile LoggingType = "file"
	// LoggingStdout writes progress and agent output to stdout.
	LoggingStdout LoggingType = "stdout"
)

// Logging configures where progress is written.
type Logging struct {
	Type LoggingType
	// Path of the log file in file mode. Default: .agentgopher/logs/<branch>[-<name>].log.
	Path string
	// OnAgentStreamEvent receives every stream event (file mode only).
	OnAgentStreamEvent func(StreamEvent)
	// Verbose also writes every raw stdout line of the agent, in real time.
	Verbose bool
}

// RunOptions configure Run.
type RunOptions struct {
	Agent   agent.Provider
	Sandbox sandbox.Provider
	// Cwd is the host repo directory. Relative paths resolve against the
	// process working directory. Default: the process working directory.
	Cwd string
	// Prompt is an inline prompt, delivered verbatim. Mutually exclusive with PromptFile.
	Prompt string
	// PromptFile is a prompt template with {{KEY}} and !`cmd` support.
	PromptFile string
	// MaxIterations defaults to DefaultMaxIterations.
	MaxIterations int
	Hooks         Hooks
	// PromptArgs fill {{KEY}} placeholders in PromptFile.
	PromptArgs map[string]string
	// Logging defaults to file mode under .agentgopher/logs/.
	Logging *Logging
	// CompletionSignal substrings stop the loop early. Default: <promise>COMPLETE</promise>.
	CompletionSignal []string
	// IdleTimeout fails the run when the agent is silent this long. Default: 10 min.
	IdleTimeout time.Duration
	// CompletionTimeout is the grace window after the completion signal. Default: 60 s.
	CompletionTimeout time.Duration
	// Name prefixes status messages and the log filename.
	Name string
	// CopyToWorktree lists paths relative to the repo root copied into the
	// worktree before the sandbox starts. Not allowed with the head strategy.
	CopyToWorktree []string
	// BranchStrategy defaults to head. Isolated providers (phase 4) default to merge-to-head.
	BranchStrategy *sandbox.BranchStrategy
	Timeouts       Timeouts
	// ResumeSession resumes a captured session on iteration 1; requires MaxIterations <= 1 (ADR 0011).
	ResumeSession string
	forkSession   bool
	// Output declares structured output extracted from the agent's stdout after the run (ADR 0010).
	Output OutputDefinition
}

// Commit identifies a commit the agent made.
type Commit struct{ SHA string }

// RunResult is the outcome of Run.
type RunResult struct {
	Iterations       []IterationResult
	CompletionSignal string
	Stdout           string
	Commits          []Commit
	Branch           string
	// LogFilePath is set in file logging mode.
	LogFilePath string
	// PreservedWorktreePath is set when the run succeeded but the worktree had uncommitted changes.
	PreservedWorktreePath string
	// Output is the decoded structured output: T for OutputObject, string for OutputString, nil otherwise.
	Output any

	opts RunOptions
}

var invalidFilenameChars = regexp.MustCompile(`[/\\:*?"<>|]`)

// SanitizeBranchForFilename replaces path-unsafe characters with dashes.
func SanitizeBranchForFilename(branch string) string {
	return invalidFilenameChars.ReplaceAllString(branch, "-")
}

var nonFilenameRunes = regexp.MustCompile(`[^a-z0-9_.-]`)

// BuildLogFilename returns "<target>-<branch>[-<name>].log", or
// "<branch>[-<name>].log" when targetBranch is empty.
func BuildLogFilename(resolvedBranch, targetBranch, name string) string {
	base := SanitizeBranchForFilename(resolvedBranch)
	if name != "" {
		base += "-" + nonFilenameRunes.ReplaceAllString(strings.ToLower(name), "-")
	}
	if targetBranch != "" {
		return SanitizeBranchForFilename(targetBranch) + "-" + base + ".log"
	}
	return base + ".log"
}

func resolveCwd(cwd string) (string, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", &CwdError{Cwd: cwd, Message: err.Error()}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", &CwdError{Cwd: abs, Message: "cwd does not exist: " + abs}
	}
	if !info.IsDir() {
		return "", &CwdError{Cwd: abs, Message: "cwd is not a directory: " + abs}
	}
	return abs, nil
}

// Run drives the agent for up to MaxIterations iterations and returns what it produced.
func Run(ctx context.Context, o RunOptions) (RunResult, error) { return runWith(ctx, o, nil, false) }

func runWith(ctx context.Context, o RunOptions, wt *worktree.Info, keepSourceBranch bool) (RunResult, error) {
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}
	maxIterations := o.MaxIterations
	if maxIterations == 0 {
		maxIterations = DefaultMaxIterations
	}
	if o.Agent == nil || o.Sandbox == nil {
		return RunResult{}, fmt.Errorf("RunOptions.Agent and RunOptions.Sandbox are required")
	}
	strategy := sandbox.BranchStrategy{Type: sandbox.BranchHead}
	if o.BranchStrategy != nil {
		strategy = *o.BranchStrategy
	}
	if o.Sandbox.Kind() == sandbox.KindIsolated {
		if o.BranchStrategy == nil {
			strategy = sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead}
		} else if o.BranchStrategy.Type == sandbox.BranchHead {
			return RunResult{}, fmt.Errorf("head branch strategy is not supported with isolated providers")
		}
	}
	if wt != nil {
		strategy.Type = sandbox.BranchNamed
		strategy.Branch = wt.Branch
		if keepSourceBranch {
			strategy.Type = sandbox.BranchMergeToHead
		}
	}
	if o.ResumeSession != "" && maxIterations > 1 {
		return RunResult{}, errors.New("ResumeSession cannot be combined with MaxIterations > 1. Resume applies to iteration 1 only (ADR 0011)")
	}
	if o.forkSession && o.ResumeSession == "" {
		return RunResult{}, errors.New("forkSession requires ResumeSession; use RunResult.Fork")
	}
	outputRetries := 0
	if o.Output != nil {
		if maxIterations != 1 {
			return RunResult{}, errors.New("output requires MaxIterations to be 1. Structured output is only supported for single-iteration runs")
		}
		outputRetries = o.Output.MaxRetries()
		if outputRetries < 0 {
			return RunResult{}, fmt.Errorf("output max retries must be non-negative, got %d", outputRetries)
		}
		if _, ok := o.Agent.(agent.SessionStorageProvider); outputRetries > 0 && !ok {
			return RunResult{}, fmt.Errorf("output retries requires an agent provider that supports session resumption; %q does not. Use ClaudeCode, Codex or Pi, or set retries to 0", o.Agent.Name())
		}
	}
	if strategy.Type == sandbox.BranchHead && len(o.CopyToWorktree) > 0 {
		return RunResult{}, fmt.Errorf("copyToWorktree is not supported with head branch strategy. In head mode the host working directory is bind-mounted directly")
	}

	hostRepoDir, err := resolveCwd(o.Cwd)
	if err != nil {
		return RunResult{}, err
	}

	if o.ResumeSession != "" {
		if err := orchestrator.AssertResumeSessionExists(o.Agent, o.Sandbox.Kind(), hostRepoDir, o.ResumeSession); err != nil {
			return RunResult{}, err
		}
	}

	resolved, err := prompt.Resolve(o.Prompt, o.PromptFile)
	if err != nil {
		return RunResult{}, err
	}
	isInline := resolved.Source == prompt.SourceInline
	if o.Output != nil && !strings.Contains(resolved.Text, "<"+o.Output.Tag()+">") {
		return RunResult{}, fmt.Errorf("output tag <%s> not found in the resolved prompt. The caller must instruct the agent to emit the configured tag", o.Output.Tag())
	}

	resolvedEnv, err := env.Resolve(hostRepoDir)
	if err != nil {
		return RunResult{}, err
	}
	merged, err := env.MergeProviderEnv(resolvedEnv, o.Agent.Env(), o.Sandbox.Env())
	if err != nil {
		return RunResult{}, err
	}

	currentBranch, err := gitx.CurrentBranch(ctx, hostRepoDir)
	if err != nil {
		return RunResult{}, err
	}

	resolvedBranch := currentBranch
	targetBranch := ""
	switch {
	case wt != nil:
		resolvedBranch = wt.Branch
	case strategy.Type == sandbox.BranchNamed:
		resolvedBranch = strategy.Branch
	case strategy.Type == sandbox.BranchMergeToHead:
		// shortcut: this name is only for SOURCE_BRANCH and the log filename; the
		// orchestrator generates its own temp branch per iteration, as upstream does.
		resolvedBranch = worktree.GenerateTempBranchName(o.Name)
		targetBranch = currentBranch
	}
	targetBranchArg := currentBranch
	if wt != nil {
		targetBranchArg = wt.Branch
	}

	logging := Logging{Type: LoggingFile}
	if o.Logging != nil {
		logging = *o.Logging
	}
	if logging.Type == LoggingFile && logging.Path == "" {
		logging.Path = filepath.Join(hostRepoDir, ".agentgopher", "logs", BuildLogFilename(resolvedBranch, targetBranch, o.Name))
	}

	var out io.Writer = os.Stdout
	logFilePath := ""
	if logging.Type == LoggingFile {
		f, err := display.OpenLogFile(logging.Path)
		if err != nil {
			return RunResult{}, err
		}
		defer func() { _ = f.Close() }()
		out = f
		logFilePath = logging.Path
		printFileStartup(o.Name, resolvedBranch, logging.Path, hostRepoDir)
	}
	d := display.NewWriter(out)
	if logging.Type == LoggingStdout {
		d = display.NewTerminal(os.Stdout)
	}

	d.Intro(orDefault(o.Name, "agentgopher"))
	d.Summary("agentgopher Run", [][2]string{
		{"Agent", orDefault(o.Name, o.Agent.Name())},
		{"Sandbox", o.Sandbox.Name()},
		{"Max iterations", fmt.Sprint(maxIterations)},
		{"Branch", resolvedBranch},
	})

	userArgs := prompt.Args(o.PromptArgs)
	resolvedPrompt := resolved.Text
	if isInline {
		if err := prompt.ValidateNoArgsWithInlinePrompt(userArgs); err != nil {
			return RunResult{}, logError(d, err)
		}
	} else {
		if err := prompt.ValidateNoBuiltInOverride(userArgs); err != nil {
			return RunResult{}, logError(d, err)
		}
		args := prompt.Args{"SOURCE_BRANCH": resolvedBranch, "TARGET_BRANCH": targetBranchArg}
		for k, v := range userArgs {
			args[k] = v
		}
		silent := map[string]bool{}
		for _, k := range prompt.BuiltInKeys {
			silent[k] = true
		}
		resolvedPrompt, err = prompt.Substitute(resolved.Text, args, silent, func(m string) { d.Status(m, display.SeverityWarn) })
		if err != nil {
			return RunResult{}, logError(d, err)
		}
	}

	result, err := orchestrator.Orchestrate(ctx, orchestrator.Options{
		HostRepoDir:         hostRepoDir,
		Iterations:          maxIterations,
		Hooks:               o.Hooks,
		Prompt:              resolvedPrompt,
		Branch:              currentBranch,
		BranchStrategy:      strategy,
		CopyToWorktree:      o.CopyToWorktree,
		Worktree:            wt,
		KeepSourceBranch:    keepSourceBranch,
		Provider:            o.Agent,
		Sandbox:             o.Sandbox,
		Env:                 merged,
		CompletionSignals:   o.CompletionSignal,
		IdleTimeout:         o.IdleTimeout,
		CompletionTimeout:   o.CompletionTimeout,
		Name:                o.Name,
		SkipPromptExpansion: isInline,
		ResumeSession:       o.ResumeSession,
		ForkSession:         o.forkSession,
		Timeouts:            o.Timeouts,
		Display:             d,
		OnStreamEvent:       buildStreamHandler(logging, d),
	})
	if err != nil {
		if ctx.Err() != nil {
			return RunResult{}, ctx.Err()
		}
		return RunResult{}, logError(d, err)
	}

	if result.CompletionSignal != "" {
		d.Status(fmt.Sprintf("Run complete: agent finished after %d iteration(s).", len(result.Iterations)), display.SeveritySuccess)
	} else {
		d.Status(fmt.Sprintf("Run complete: reached %d iteration(s) without completion signal.", len(result.Iterations)), display.SeverityWarn)
	}
	for _, it := range result.Iterations {
		if it.Usage != nil {
			total := it.Usage.InputTokens + it.Usage.CacheCreationInputTokens + it.Usage.CacheReadInputTokens
			d.Text(fmt.Sprintf("Context window: %dk", (total+999)/1000))
		}
	}

	commits := make([]Commit, len(result.Commits))
	for i, sha := range result.Commits {
		commits[i] = Commit{SHA: sha}
	}
	runResult := RunResult{
		Iterations:            result.Iterations,
		CompletionSignal:      result.CompletionSignal,
		Stdout:                result.Stdout,
		Commits:               commits,
		Branch:                result.Branch,
		LogFilePath:           logFilePath,
		PreservedWorktreePath: result.PreservedWorktreePath,
		opts:                  o,
	}
	if o.Output == nil {
		return runResult, nil
	}
	var last IterationResult
	if n := len(runResult.Iterations); n > 0 {
		last = runResult.Iterations[n-1]
	}
	value, err := extractStructuredOutput(runResult.Stdout, o.Output, extractionContext{
		Commits: runResult.Commits, Branch: runResult.Branch, PreservedWorktreePath: runResult.PreservedWorktreePath,
		SessionID: last.SessionID, SessionFilePath: last.SessionFilePath,
	})
	if err == nil {
		runResult.Output = value
		return runResult, nil
	}
	var soe *StructuredOutputError
	if errors.As(err, &soe) && outputRetries > 0 && soe.SessionID != "" {
		retry := o
		retry.Prompt = BuildStructuredOutputRetryFeedback(soe, outputRetries-1)
		retry.PromptFile, retry.PromptArgs = "", nil
		retry.ResumeSession = soe.SessionID
		retry.forkSession = false
		retry.Output = withRetries(o.Output, outputRetries-1)
		return Run(ctx, retry)
	}
	return RunResult{}, err
}

// withRetries returns a copy of def with a new retry budget.
func withRetries(def OutputDefinition, n int) OutputDefinition {
	switch d := def.(type) {
	case outputString:
		d.maxRetries = n
		return d
	default:
		return def.(interface{ withRetries(int) OutputDefinition }).withRetries(n)
	}
}

func (d outputObject[T]) withRetries(n int) OutputDefinition { d.maxRetries = n; return d }

// buildStreamHandler composes the user's callback (file mode only) with the
// verbose raw-line sink. Returns nil when neither is configured. Raw lines go
// through the display so they never land in the middle of a streamed chunk.
func buildStreamHandler(logging Logging, d display.Display) func(StreamEvent) {
	var user func(StreamEvent)
	if logging.Type == LoggingFile {
		user = logging.OnAgentStreamEvent
	}
	if user == nil && !logging.Verbose {
		return nil
	}
	return func(ev StreamEvent) {
		if user != nil {
			user(ev)
		}
		if logging.Verbose && ev.Type == orchestrator.StreamRaw {
			d.Text(ev.Line)
		}
	}
}

// logError writes err to the display so a file log records why the run
// stopped, then returns it unchanged.
func logError(d display.Display, err error) error {
	d.Status(err.Error(), display.SeverityError)
	return err
}

func printFileStartup(name, branch, logPath, hostRepoDir string) {
	if name == "" {
		name = "Agent"
	}
	shown := logPath
	if wd, err := os.Getwd(); err == nil && wd == hostRepoDir {
		if rel, err := filepath.Rel(wd, logPath); err == nil {
			shown = rel
		}
	}
	fmt.Printf("[%s] Started on branch %s\n  tail -f %s\n", name, branch, shown)
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

// ResumeOptions override run options for Resume and Fork. Nil pointers and
// zero values keep the original run's values.
type ResumeOptions struct {
	PromptArgs        map[string]string
	Logging           *Logging
	CompletionSignal  []string
	IdleTimeout       time.Duration
	CompletionTimeout time.Duration
	Name              string
	Timeouts          Timeouts
	Hooks             *Hooks
	CopyToWorktree    []string
	BranchStrategy    *sandbox.BranchStrategy
}

func (r RunResult) continueRun(ctx context.Context, prompt string, o *ResumeOptions, fork bool) (RunResult, error) {
	if len(r.Iterations) == 0 || r.Iterations[len(r.Iterations)-1].SessionID == "" {
		return RunResult{}, errors.New("cannot resume: no session id was captured")
	}
	if _, ok := r.opts.Agent.(agent.SessionStorageProvider); !ok {
		return RunResult{}, fmt.Errorf("cannot resume: %s does not support session storage", r.opts.Agent.Name())
	}
	next := r.opts
	next.Prompt, next.PromptFile, next.PromptArgs = prompt, "", nil
	next.MaxIterations = 1
	next.ResumeSession = r.Iterations[len(r.Iterations)-1].SessionID
	next.forkSession = fork
	next.Output = nil
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
		if o.Timeouts != (Timeouts{}) {
			next.Timeouts = o.Timeouts
		}
		if o.Hooks != nil {
			next.Hooks = *o.Hooks
		}
		if o.CopyToWorktree != nil {
			next.CopyToWorktree = o.CopyToWorktree
		}
		if o.BranchStrategy != nil {
			next.BranchStrategy = o.BranchStrategy
		}
	}
	return Run(ctx, next)
}

// Resume continues the last captured session for exactly one iteration (ADR 0011).
func (r RunResult) Resume(ctx context.Context, prompt string, o *ResumeOptions) (RunResult, error) {
	return r.continueRun(ctx, prompt, o, false)
}

// Fork continues the last captured session as a new session, leaving the
// parent record intact. It isolates the session only, not the branch or
// sandbox (ADR 0018): concurrent forks need distinct named branches.
func (r RunResult) Fork(ctx context.Context, prompt string, o *ResumeOptions) (RunResult, error) {
	return r.continueRun(ctx, prompt, o, true)
}
