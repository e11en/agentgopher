package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/internal/prompt"
	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/internal/worktree"
	"github.com/e11en/agentgopher/sandbox"
)

const (
	DefaultCompletionSignal  = "<promise>COMPLETE</promise>"
	DefaultIdleTimeout       = 10 * time.Minute
	DefaultCompletionTimeout = 60 * time.Second
)

// StreamEventType classifies a StreamEvent.
type StreamEventType string

const (
	StreamText     StreamEventType = "text"
	StreamToolCall StreamEventType = "toolCall"
	StreamRaw      StreamEventType = "raw"
)

// StreamEvent is one item of the agent's output, surfaced to the caller.
// Message is set for StreamText, Name and FormattedArgs for StreamToolCall,
// Line for StreamRaw.
type StreamEvent struct {
	Type          StreamEventType
	Message       string
	Name          string
	FormattedArgs string
	Line          string
	Iteration     int
	Timestamp     time.Time
}

// Options configure Orchestrate.
type Options struct {
	HostRepoDir         string
	Iterations          int
	Hooks               lifecycle.Hooks
	Prompt              string
	Branch              string
	Provider            agent.Provider
	Sandbox             sandbox.Provider
	BranchStrategy      sandbox.BranchStrategy
	CopyToWorktree      []string
	Worktree            *worktree.Info
	KeepSourceBranch    bool
	Env                 map[string]string
	CompletionSignals   []string
	IdleTimeout         time.Duration
	CompletionTimeout   time.Duration
	IdleWarningInterval time.Duration
	Name                string
	SkipPromptExpansion bool
	// ResumeSession resumes this session on iteration 1; ForkSession writes a new session instead.
	ResumeSession string
	ForkSession   bool
	Timeouts      lifecycle.Timeouts
	Display       display.Display
	OnStreamEvent func(StreamEvent)
}

// IterationResult is the outcome of one agent invocation.
type IterationResult struct {
	SessionID       string
	SessionFilePath string
	Usage           *agent.Usage
}

// Result is the outcome of the whole loop.
type Result struct {
	Iterations       []IterationResult
	CompletionSignal string
	Stdout           string
	Commits          []string
	Branch           string
	// PreservedWorktreePath is set when a worktree was kept because it held uncommitted changes.
	PreservedWorktreePath string
}

type iterationOutcome struct {
	completionSignal string
	stdout           string
	sessionID        string
	sessionFilePath  string
	usage            *agent.Usage
}

// Orchestrate runs up to o.Iterations agent invocations, stopping early when
// the agent emits a completion signal.
func Orchestrate(ctx context.Context, o Options) (Result, error) {
	signals := o.CompletionSignals
	if len(signals) == 0 {
		signals = []string{DefaultCompletionSignal}
	}
	idle, completion := o.IdleTimeout, o.CompletionTimeout
	if idle == 0 {
		idle = DefaultIdleTimeout
	}
	if completion == 0 {
		completion = DefaultCompletionTimeout
	}
	label := func(msg string) string {
		if o.Name != "" {
			return "[" + o.Name + "] " + msg
		}
		return msg
	}

	var result Result
	for i := 1; i <= o.Iterations; i++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		o.Display.Status(label(fmt.Sprintf("Iteration %d/%d", i, o.Iterations)), display.SeverityInfo)

		s, err := startSession(ctx, o)
		if err != nil {
			return result, err
		}
		var baseHead string
		var apply func(context.Context) error
		if s.isolated != nil {
			apply = applyToHost(s.isolated, s.hostWorktreePath, s.sandboxRepoDir, o.Display, &baseHead)
		}
		lc, err := lifecycle.Run(ctx, lifecycle.Options{
			HostRepoDir:      o.HostRepoDir,
			SandboxRepoDir:   s.sandboxRepoDir,
			HostWorktreePath: s.hostWorktreePath,
			Branch:           s.lifecycleBranch,
			KeepSourceBranch: o.KeepSourceBranch,
			Hooks:            o.Hooks,
			Timeouts:         o.Timeouts,
			Display:          o.Display,
			ApplyToHost:      apply,
		}, s.handle, func(lctx lifecycle.Context) (iterationOutcome, error) {
			baseHead = lctx.BaseHead
			return runIteration(ctx, o, lctx, i, signals, idle, completion, label)
		})
		preserved := s.cleanup(ctx, err != nil)
		result.PreservedWorktreePath = preserved
		if err != nil {
			return result, attachPreserved(err, preserved)
		}

		result.Commits = append(result.Commits, lc.Commits...)
		result.Stdout += lc.Value.stdout
		result.Branch = lc.Branch
		result.Iterations = append(result.Iterations, IterationResult{SessionID: lc.Value.sessionID, SessionFilePath: lc.Value.sessionFilePath, Usage: lc.Value.usage})

		if lc.Value.completionSignal != "" {
			o.Display.Status(label(fmt.Sprintf("Agent signaled completion after %d iteration(s).", i)), display.SeveritySuccess)
			result.CompletionSignal = lc.Value.completionSignal
			return result, nil
		}
	}
	o.Display.Status(label(fmt.Sprintf("Reached max iterations (%d).", o.Iterations)), display.SeverityInfo)
	return result, nil
}

func runIteration(ctx context.Context, o Options, lctx lifecycle.Context, iteration int, signals []string, idle, completion time.Duration, label func(string) string) (iterationOutcome, error) {
	bm, _ := lctx.Sandbox.(sandbox.BindMountHandle)
	storage := sessionStorageOf(o.Provider)
	resume, fork := "", false
	if iteration == 1 {
		resume, fork = o.ResumeSession, o.ForkSession
	}
	if resume != "" && bm != nil && storage != nil {
		o.Display.Status(label("Resuming session"), display.SeverityInfo)
		if err := storage.ResumeIntoSandbox(ctx, agent.SessionTransfer{HostCwd: o.HostRepoDir, SandboxCwd: lctx.SandboxRepoDir, SessionID: resume, Handle: bm}); err != nil {
			return iterationOutcome{}, fmt.Errorf("session resume failed: %w", err)
		}
	}

	fullPrompt := o.Prompt
	if !o.SkipPromptExpansion {
		err := o.Display.Task("Expanding shell expressions", func(log func(string)) error {
			var expandErr error
			fullPrompt, expandErr = prompt.Expand(ctx, o.Prompt, lctx.Sandbox, lctx.SandboxRepoDir, log)
			return expandErr
		})
		if err != nil {
			return iterationOutcome{}, err
		}
	}

	o.Display.Status(label("Agent started"), display.SeveritySuccess)

	emit := func(ev StreamEvent) {
		if o.OnStreamEvent == nil {
			return
		}
		ev.Iteration = iteration
		ev.Timestamp = time.Now()
		// A broken forwarder must not kill the run.
		defer func() { _ = recover() }()
		o.OnStreamEvent(ev)
	}
	textBuffer := stream.NewTextDeltaBuffer(func(chunk string) {
		o.Display.TextChunk(chunk)
		emit(StreamEvent{Type: StreamText, Message: chunk})
	})

	res, err := invokeAgent(ctx, lctx.Sandbox, lctx.SandboxRepoDir, invokeOptions{
		Prompt:              fullPrompt,
		Provider:            o.Provider,
		SkipPermissions:     true,
		IdleTimeout:         idle,
		CompletionTimeout:   completion,
		IdleWarningInterval: o.IdleWarningInterval,
		CompletionSignals:   signals,
		ResumeSession:       resume,
		ForkSession:         fork,
		OnText:              textBuffer.Write,
		OnToolCall: func(name, args string) {
			textBuffer.Flush()
			o.Display.ToolCall(name, args)
			emit(StreamEvent{Type: StreamToolCall, Name: name, FormattedArgs: args})
		},
		OnRawLine: func(line string) { emit(StreamEvent{Type: StreamRaw, Line: line}) },
		OnIdleWarning: func(minutes int) {
			msg := "Agent idle for 1 minute"
			if minutes != 1 {
				msg = fmt.Sprintf("Agent idle for %d minutes", minutes)
			}
			o.Display.Status(label(msg), display.SeverityWarn)
		},
		OnCompletionTimeout: func(grace time.Duration) {
			o.Display.Status(label(fmt.Sprintf("Completion signal seen but agent process is hanging - force-completing after %v grace window.", grace)), display.SeverityWarn)
		},
	})
	textBuffer.Close()
	if err != nil {
		return iterationOutcome{}, err
	}
	o.Display.Status(label("Agent stopped"), display.SeverityInfo)

	out := iterationOutcome{stdout: res.Output, sessionID: res.SessionID, usage: res.Usage}
	if o.Provider.CaptureSessions() && storage != nil && res.SessionID != "" && bm != nil {
		o.Display.Status(label("Capturing session"), display.SeverityInfo)
		if err := storage.CaptureToHost(ctx, agent.SessionTransfer{HostCwd: o.HostRepoDir, SandboxCwd: lctx.SandboxRepoDir, SessionID: res.SessionID, Handle: bm}); err != nil {
			return iterationOutcome{}, fmt.Errorf("session capture failed: %w", err)
		}
		out.sessionFilePath = storage.HostSessionFilePath(o.HostRepoDir, res.SessionID)
		if parser, ok := o.Provider.(agent.SessionUsageParser); ok {
			if content, found, _ := storage.ReadHostSession(o.HostRepoDir, res.SessionID); found {
				if u, ok := parser.ParseSessionUsage(content); ok {
					out.usage = &u
				}
			}
		}
	}
	for _, sig := range signals {
		if strings.Contains(res.Output, sig) {
			out.completionSignal = sig
			break
		}
	}
	return out, nil
}

// attachPreserved copies the preserved worktree path onto the agent errors so
// callers can build on the preserved worktree.
func attachPreserved(err error, path string) error {
	if path == "" {
		return err
	}
	var aerr *AgentError
	if errors.As(err, &aerr) {
		aerr.PreservedWorktreePath = path
	}
	var ierr *AgentIdleTimeoutError
	if errors.As(err, &ierr) {
		ierr.PreservedWorktreePath = path
	}
	return err
}

func sessionStorageOf(p agent.Provider) agent.SessionStorage {
	if sp, ok := p.(agent.SessionStorageProvider); ok {
		return sp.SessionStorage()
	}
	return nil
}
