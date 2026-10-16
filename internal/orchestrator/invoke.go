// Package orchestrator runs the agent iteration loop.
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/sandbox"
)

const defaultIdleWarningInterval = time.Minute

// AgentError reports an agent process that exited non-zero.
type AgentError struct{ Message string }

func (e *AgentError) Error() string { return e.Message }

// AgentIdleTimeoutError reports an agent that produced no output for Timeout.
type AgentIdleTimeoutError struct{ Timeout time.Duration }

func (e *AgentIdleTimeoutError) Error() string {
	return fmt.Sprintf("agent idle for %v - no output received", e.Timeout)
}

type invokeOptions struct {
	Prompt              string
	Provider            agent.Provider
	SkipPermissions     bool
	IdleTimeout         time.Duration
	CompletionTimeout   time.Duration
	IdleWarningInterval time.Duration
	CompletionSignals   []string
	ResumeSession       string
	ForkSession         bool
	OnText              func(text string)
	OnToolCall          func(name, args string)
	OnRawLine           func(line string)
	OnIdleWarning       func(minutes int)
	OnCompletionTimeout func(grace time.Duration)
}

type invokeResult struct {
	Output    string
	SessionID string
	Usage     *agent.Usage
}

// streamState is shared between the exec goroutine (writer) and the watchdog (reader).
type streamState struct {
	mu          sync.Mutex
	result      string
	accumulated string
	sessionID   string
	usage       *agent.Usage
	completed   bool
}

type execOutcome struct {
	res sandbox.ExecResult
	err error
}

// invokeAgent runs one agent iteration and returns its output. It fails with
// AgentIdleTimeoutError when no output arrives for IdleTimeout before the
// completion signal, and force-completes successfully when the process hangs
// for CompletionTimeout after the signal (ADR 0019).
func invokeAgent(ctx context.Context, h sandbox.Handle, repoDir string, o invokeOptions) (invokeResult, error) {
	warnInterval := o.IdleWarningInterval
	if warnInterval == 0 {
		warnInterval = defaultIdleWarningInterval
	}

	execCtx, cancelExec := context.WithCancel(ctx)
	defer cancelExec()

	state := &streamState{}
	activity := make(chan struct{}, 1)
	done := make(chan execOutcome, 1)

	printCmd := o.Provider.BuildPrintCommand(agent.CommandOptions{
		Prompt:                     o.Prompt,
		DangerouslySkipPermissions: o.SkipPermissions,
		ResumeSession:              o.ResumeSession,
		ForkSession:                o.ForkSession,
	})

	go func() {
		res, err := h.Exec(execCtx, printCmd.Command, sandbox.ExecOptions{
			Cwd:   repoDir,
			Stdin: printCmd.Stdin,
			OnLine: func(line string) {
				if o.OnRawLine != nil {
					o.OnRawLine(line)
				}
				state.mu.Lock()
				for _, ev := range o.Provider.ParseStreamLine(line) {
					switch ev.Type {
					case agent.EventText:
						state.accumulated += ev.Text
						state.mu.Unlock()
						if o.OnText != nil {
							o.OnText(ev.Text)
						}
						state.mu.Lock()
					case agent.EventResult:
						state.result = ev.Text
						state.accumulated += ev.Text
					case agent.EventToolCall:
						state.mu.Unlock()
						if o.OnToolCall != nil {
							o.OnToolCall(ev.Name, ev.Args)
						}
						state.mu.Lock()
					case agent.EventSessionID:
						state.sessionID = ev.SessionID
					case agent.EventUsage:
						state.usage = ev.Usage
					}
				}
				if !state.completed {
					for _, sig := range o.CompletionSignals {
						if strings.Contains(state.accumulated, sig) {
							state.completed = true
							break
						}
					}
				}
				state.mu.Unlock()
				select {
				case activity <- struct{}{}:
				default:
				}
			},
		})
		done <- execOutcome{res: res, err: err}
	}()

	snapshot := func() invokeResult {
		state.mu.Lock()
		defer state.mu.Unlock()
		out := state.result
		if out == "" {
			out = state.accumulated
		}
		return invokeResult{Output: out, SessionID: state.sessionID, Usage: state.usage}
	}
	isCompleted := func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.completed
	}

	timer := time.NewTimer(o.IdleTimeout)
	defer timer.Stop()
	warn := time.NewTicker(warnInterval)
	defer warn.Stop()
	idleMinutes := 0

	for {
		select {
		case <-activity:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if isCompleted() {
				timer.Reset(o.CompletionTimeout)
			} else {
				timer.Reset(o.IdleTimeout)
				warn.Reset(warnInterval)
				idleMinutes = 0
			}
		case <-warn.C:
			if !isCompleted() {
				idleMinutes++
				if o.OnIdleWarning != nil {
					o.OnIdleWarning(idleMinutes)
				}
			}
		case <-timer.C:
			if isCompleted() {
				if o.OnCompletionTimeout != nil {
					o.OnCompletionTimeout(o.CompletionTimeout)
				}
				cancelExec()
				<-done
				return snapshot(), nil
			}
			cancelExec()
			<-done
			return invokeResult{}, &AgentIdleTimeoutError{Timeout: o.IdleTimeout}
		case out := <-done:
			if ctx.Err() != nil {
				return invokeResult{}, ctx.Err()
			}
			if out.err != nil {
				var start *sandbox.StartError
				if errors.As(out.err, &start) {
					return invokeResult{}, &lifecycle.ExecError{Command: start.Command, ExitCode: -1, Stderr: start.Cause.Error()}
				}
				return invokeResult{}, out.err
			}
			if out.res.ExitCode != 0 {
				state.mu.Lock()
				parsedResult := state.result
				state.mu.Unlock()
				return invokeResult{}, &AgentError{Message: fmt.Sprintf("%s exited with code %d:\n%s", o.Provider.Name(), out.res.ExitCode, errorDetail(out.res, parsedResult))}
			}
			r := snapshot()
			if r.Output == "" {
				r.Output = out.res.Stdout
			}
			return r, nil
		}
	}
}

// errorDetail prefers stderr, then the parsed result text, then the last 20
// non-empty stdout lines.
func errorDetail(res sandbox.ExecResult, parsed string) string {
	if strings.TrimSpace(res.Stderr) != "" {
		return res.Stderr
	}
	if strings.TrimSpace(parsed) != "" {
		return parsed
	}
	var lines []string
	for _, l := range strings.Split(res.Stdout, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	return strings.Join(lines, "\n")
}
