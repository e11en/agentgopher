package orchestrator

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox"
)

// scriptedLine is one stdout line the fake handle emits after a delay.
type scriptedLine struct {
	after time.Duration
	line  string
}

// fakeHandle plays scripted stdout lines through OnLine. exitCode and stderr
// are returned when the script ends; hang keeps the process open after the
// last line until the context is cancelled. Git commands succeed immediately
// with "main" so the lifecycle's setup passes.
type fakeHandle struct {
	mu       sync.Mutex
	script   []scriptedLine
	exitCode int
	stderr   string
	hang     bool
	commands []string
	stdins   []string
}

func (f *fakeHandle) WorktreePath() string        { return "/fake" }
func (f *fakeHandle) Close(context.Context) error { return nil }

func (f *fakeHandle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) {
	f.mu.Lock()
	f.commands = append(f.commands, command)
	if opts.Stdin != nil {
		f.stdins = append(f.stdins, *opts.Stdin)
	}
	f.mu.Unlock()
	if strings.HasPrefix(command, "git ") {
		return sandbox.ExecResult{Stdout: "main"}, nil
	}
	var out []string
	for _, s := range f.script {
		select {
		case <-time.After(s.after):
		case <-ctx.Done():
			return sandbox.ExecResult{}, ctx.Err()
		}
		out = append(out, s.line)
		if opts.OnLine != nil {
			opts.OnLine(s.line)
		}
	}
	if f.hang {
		<-ctx.Done()
		return sandbox.ExecResult{}, ctx.Err()
	}
	return sandbox.ExecResult{Stdout: strings.Join(out, "\n"), Stderr: f.stderr, ExitCode: f.exitCode}, nil
}

// fakeProvider treats every line as text, except lines starting with
// "TOOL " (tool call), "RESULT " (result) and "SESSION " (session id).
type fakeProvider struct{}

func (fakeProvider) Name() string           { return "fake" }
func (fakeProvider) Env() map[string]string { return nil }
func (fakeProvider) CaptureSessions() bool  { return false }
func (fakeProvider) BuildPrintCommand(o agent.CommandOptions) agent.PrintCommand {
	p := o.Prompt
	cmd := "fake-agent"
	if o.DangerouslySkipPermissions {
		cmd += " --yolo"
	}
	if o.ResumeSession != "" {
		cmd += " --resume " + o.ResumeSession
	}
	return agent.PrintCommand{Command: cmd, Stdin: &p}
}
func (fakeProvider) ParseStreamLine(line string) []agent.StreamEvent {
	switch {
	case strings.HasPrefix(line, "TOOL "):
		name, args, _ := strings.Cut(strings.TrimPrefix(line, "TOOL "), " ")
		return []agent.StreamEvent{{Type: agent.EventToolCall, Name: name, Args: args}}
	case strings.HasPrefix(line, "RESULT "):
		return []agent.StreamEvent{{Type: agent.EventResult, Text: strings.TrimPrefix(line, "RESULT ")}}
	case strings.HasPrefix(line, "SESSION "):
		return []agent.StreamEvent{{Type: agent.EventSessionID, SessionID: strings.TrimPrefix(line, "SESSION ")}}
	case line == "":
		return nil
	}
	return []agent.StreamEvent{{Type: agent.EventText, Text: line}}
}

// fakeNoSandboxProvider hands out one scripted handle per Create call, in order.
type fakeNoSandboxProvider struct {
	mu      sync.Mutex
	handles []*fakeHandle
	created int
}

func (p *fakeNoSandboxProvider) Kind() sandbox.Kind     { return sandbox.KindNone }
func (p *fakeNoSandboxProvider) Name() string           { return "fake-sandbox" }
func (p *fakeNoSandboxProvider) Env() map[string]string { return nil }
func (p *fakeNoSandboxProvider) Create(context.Context, sandbox.NoSandboxCreateOptions) (sandbox.Handle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h := p.handles[p.created]
	p.created++
	return h, nil
}
