// Package container drives a container through its CLI (docker or podman).
package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/e11en/agentgopher/internal/shutdown"
	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/sandbox"
)

// Error reports a failed container CLI call.
type Error struct {
	Binary  string
	Message string
}

func (e *Error) Error() string { return e.Binary + " " + e.Message }

// Run executes the container binary and returns trimmed stdout.
func Run(ctx context.Context, binary string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		verb := ""
		if len(args) > 0 {
			verb = args[0]
		}
		return "", &Error{Binary: binary, Message: fmt.Sprintf("%s failed: %s", verb, msg)}
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Handle is a running container addressed by name.
type Handle struct {
	Binary     string
	Name       string
	Worktree   string
	MaxTail    int
	unregister func()
}

// NewHandle wraps a started container and registers its removal on shutdown.
func NewHandle(binary, name, worktree string, maxTail int) *Handle {
	h := &Handle{Binary: binary, Name: name, Worktree: worktree, MaxTail: maxTail}
	h.unregister = shutdown.Register(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = Run(ctx, binary, "rm", "-f", name)
	})
	return h
}

func (h *Handle) WorktreePath() string { return h.Worktree }

func (h *Handle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) {
	args := []string{"exec"}
	if opts.Stdin != nil {
		args = append(args, "-i")
	}
	if opts.Cwd != "" {
		args = append(args, "-w", opts.Cwd)
	}
	args = append(args, h.Name)
	if opts.Sudo {
		// sudo wraps the whole shell so every command in a chain runs as root.
		args = append(args, "sudo")
	}
	args = append(args, "sh", "-c", command)
	cmd := exec.CommandContext(ctx, h.Binary, args...)
	cmd.WaitDelay = 2 * time.Second
	if opts.Stdin != nil {
		cmd.Stdin = strings.NewReader(*opts.Stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	var stdout string
	var err error
	if opts.OnLine == nil {
		var out bytes.Buffer
		cmd.Stdout = &out
		err = cmd.Run()
		stdout = out.String()
	} else {
		tail := stream.NewBoundedTail(h.MaxTail, "\n")
		lw := stream.NewLineWriter(func(line string) {
			tail.Push(line)
			opts.OnLine(line)
		})
		cmd.Stdout = lw
		err = cmd.Run()
		lw.Flush()
		stdout = tail.String()
	}
	if ctx.Err() != nil {
		return sandbox.ExecResult{}, ctx.Err()
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil, errors.As(err, &exitErr):
	case errors.Is(err, exec.ErrWaitDelay):
		// The shell exited; a child kept the pipe open past WaitDelay (ADR 0019).
	default:
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
	}
	errTail := stream.NewBoundedTail(h.MaxTail, "")
	errTail.Push(stderr.String())
	return sandbox.ExecResult{Stdout: stdout, Stderr: errTail.String(), ExitCode: cmd.ProcessState.ExitCode()}, nil
}

func (h *Handle) CopyFileIn(ctx context.Context, hostPath, sandboxPath string) error {
	_, err := Run(ctx, h.Binary, "cp", hostPath, h.Name+":"+sandboxPath)
	return err
}

func (h *Handle) CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error {
	_, err := Run(ctx, h.Binary, "cp", h.Name+":"+sandboxPath, hostPath)
	return err
}

// Close removes the container and drops the shutdown registration.
func (h *Handle) Close(ctx context.Context) error {
	if h.unregister != nil {
		h.unregister()
	}
	_, err := Run(ctx, h.Binary, "rm", "-f", h.Name)
	return err
}
