package agentgopher

import (
	"context"
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
	if o.Sandbox.Kind() == sandbox.KindIsolated {
		return RunResult{}, fmt.Errorf("sandbox provider kind %q is not supported yet; isolated providers arrive in phase 4", o.Sandbox.Kind())
	}
	strategy := sandbox.BranchStrategy{Type: sandbox.BranchHead}
	if o.BranchStrategy != nil {
		strategy = *o.BranchStrategy
	}
	if wt != nil {
		strategy.Type = sandbox.BranchNamed
		strategy.Branch = wt.Branch
		if keepSourceBranch {
			strategy.Type = sandbox.BranchMergeToHead
		}
	}
	if strategy.Type == sandbox.BranchHead && len(o.CopyToWorktree) > 0 {
		return RunResult{}, fmt.Errorf("copyToWorktree is not supported with head branch strategy. In head mode the host working directory is bind-mounted directly")
	}

	hostRepoDir, err := resolveCwd(o.Cwd)
	if err != nil {
		return RunResult{}, err
	}

	resolved, err := prompt.Resolve(o.Prompt, o.PromptFile)
	if err != nil {
		return RunResult{}, err
	}
	isInline := resolved.Source == prompt.SourceInline

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
	return RunResult{
		Iterations:            result.Iterations,
		CompletionSignal:      result.CompletionSignal,
		Stdout:                result.Stdout,
		Commits:               commits,
		Branch:                result.Branch,
		LogFilePath:           logFilePath,
		PreservedWorktreePath: result.PreservedWorktreePath,
	}, nil
}

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
