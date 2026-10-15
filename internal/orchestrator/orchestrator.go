package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/internal/prompt"
	"github.com/e11en/agentgopher/internal/stream"
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
	Sandbox             sandbox.NoSandboxProvider
	Env                 map[string]string
	CompletionSignals   []string
	IdleTimeout         time.Duration
	CompletionTimeout   time.Duration
	IdleWarningInterval time.Duration
	Name                string
	SkipPromptExpansion bool
	Timeouts            lifecycle.Timeouts
	Display             display.Display
	OnStreamEvent       func(StreamEvent)
}

// IterationResult is the outcome of one agent invocation.
type IterationResult struct {
	SessionID string
	Usage     *agent.Usage
}

// Result is the outcome of the whole loop.
type Result struct {
	Iterations       []IterationResult
	CompletionSignal string
	Stdout           string
	Commits          []string
	Branch           string
}

type iterationOutcome struct {
	completionSignal string
	stdout           string
	sessionID        string
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

		if err := lifecycle.RunHostHooks(ctx, o.Hooks.HostOnWorktreeReady, o.HostRepoDir); err != nil {
			return result, err
		}
		h, err := o.Sandbox.Create(ctx, sandbox.NoSandboxCreateOptions{WorktreePath: o.HostRepoDir, Env: o.Env})
		if err != nil {
			return result, fmt.Errorf("provider '%s' create failed: %w", o.Sandbox.Name(), err)
		}

		lc, err := lifecycle.Run(ctx, lifecycle.Options{
			HostRepoDir:    o.HostRepoDir,
			SandboxRepoDir: h.WorktreePath(),
			Branch:         o.Branch,
			Hooks:          o.Hooks,
			Timeouts:       o.Timeouts,
			Display:        o.Display,
			SkipGitSetup:   o.Sandbox.Kind() == sandbox.KindNone,
		}, h, func(lctx lifecycle.Context) (iterationOutcome, error) {
			return runIteration(ctx, o, lctx, i, signals, idle, completion, label)
		})
		_ = h.Close(ctx)
		if err != nil {
			return result, err
		}

		result.Commits = append(result.Commits, lc.Commits...)
		result.Stdout += lc.Value.stdout
		result.Branch = lc.Branch
		result.Iterations = append(result.Iterations, IterationResult{SessionID: lc.Value.sessionID, Usage: lc.Value.usage})

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
	for _, sig := range signals {
		if strings.Contains(res.Output, sig) {
			out.completionSignal = sig
			break
		}
	}
	return out, nil
}
