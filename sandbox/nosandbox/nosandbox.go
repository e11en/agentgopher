// Package nosandbox runs the agent directly on the host with no isolation.
package nosandbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/sandbox"
)

// Options configures the provider.
type Options struct {
	// Env is injected into every process. Merged at launch time.
	Env map[string]string
	// MaxOutputTailChars bounds the stdout and stderr returned from a streamed
	// Exec. Default: stream.MaxTailChars.
	MaxOutputTailChars int
}

type provider struct{ opts Options }

// New returns a provider that runs commands on the host through `sh -c`.
func New(opts *Options) sandbox.NoSandboxProvider {
	p := &provider{}
	if opts != nil {
		p.opts = *opts
	}
	if p.opts.MaxOutputTailChars == 0 {
		p.opts.MaxOutputTailChars = stream.MaxTailChars
	}
	return p
}

func (p *provider) Kind() sandbox.Kind     { return sandbox.KindNone }
func (p *provider) Name() string           { return "no-sandbox" }
func (p *provider) Env() map[string]string { return p.opts.Env }

func (p *provider) Create(_ context.Context, opts sandbox.NoSandboxCreateOptions) (sandbox.Handle, error) {
	env := os.Environ()
	for k, v := range opts.Env {
		env = append(env, k+"="+v)
	}
	return &handle{worktree: opts.WorktreePath, env: env, maxTail: p.opts.MaxOutputTailChars}, nil
}

type handle struct {
	worktree string
	env      []string
	maxTail  int
}

func (h *handle) WorktreePath() string { return h.worktree }

func (h *handle) Close(context.Context) error { return nil }

func (h *handle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = opts.Cwd
	if cmd.Dir == "" {
		cmd.Dir = h.worktree
	}
	cmd.Env = h.env
	if opts.Stdin != nil {
		cmd.Stdin = strings.NewReader(*opts.Stdin)
	}
	// A child that inherits stdout (a hanging gh or MCP server) must not keep
	// Exec blocked after sh exits or is killed; see ADR 0019.
	cmd.WaitDelay = 2 * time.Second

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if opts.OnLine == nil {
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		err := cmd.Run()
		return h.result(ctx, stdout.String(), stderr.String(), cmd, err)
	}

	tail := stream.NewBoundedTail(h.maxTail, "\n")
	lw := &lineWriter{onLine: func(line string) {
		tail.Push(line)
		opts.OnLine(line)
	}}
	cmd.Stdout = lw
	err := cmd.Run()
	lw.flush()
	errTail := stream.NewBoundedTail(h.maxTail, "")
	errTail.Push(stderr.String())
	return h.result(ctx, tail.String(), errTail.String(), cmd, err)
}

// lineWriter splits a byte stream into lines and hands each complete line to
// onLine as soon as it arrives.
type lineWriter struct {
	onLine func(string)
	buf    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		w.onLine(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.onLine(string(w.buf))
		w.buf = nil
	}
}

func (h *handle) result(ctx context.Context, stdout, stderr string, cmd *exec.Cmd, err error) (sandbox.ExecResult, error) {
	if ctx.Err() != nil {
		return sandbox.ExecResult{}, ctx.Err()
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
	case errors.Is(err, exec.ErrWaitDelay):
		// sh exited; a child kept the pipe open past WaitDelay. The exit code is valid.
	default:
		return sandbox.ExecResult{}, &sandbox.StartError{Command: strings.Join(cmd.Args[2:], " "), Cause: err}
	}
	return sandbox.ExecResult{Stdout: stdout, Stderr: stderr, ExitCode: cmd.ProcessState.ExitCode()}, nil
}
