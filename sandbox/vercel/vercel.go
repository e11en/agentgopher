// Package vercel runs agents in Vercel Sandbox microVMs through the official
// `sandbox` CLI. Install it with `pnpm i -g sandbox` and log in with
// `sandbox login`, or pass Options.Token.
package vercel

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/sandbox"
)

// RepoPath is where the repo lives inside a Vercel sandbox.
const RepoPath = "/vercel/sandbox/workspace"

// Options configure the provider.
type Options struct {
	Token              string
	Project            string
	Team               string
	Timeout            string
	Image              string
	Snapshot           string
	VCPUs              int
	Env                map[string]string
	MaxOutputTailChars int
	Bin                string
}

type provider struct{ o Options }

// New returns an isolated provider backed by the `sandbox` CLI.
func New(opts *Options) sandbox.IsolatedProvider {
	p := &provider{}
	if opts != nil {
		p.o = *opts
	}
	if p.o.Bin == "" {
		p.o.Bin = "sandbox"
	}
	if p.o.Timeout == "" {
		p.o.Timeout = "1h"
	}
	if p.o.MaxOutputTailChars == 0 {
		p.o.MaxOutputTailChars = stream.MaxTailChars
	}
	return p
}

func (p *provider) Kind() sandbox.Kind     { return sandbox.KindIsolated }
func (p *provider) Name() string           { return "vercel" }
func (p *provider) Env() map[string]string { return p.o.Env }

func authArgs(o Options) []string {
	var a []string
	if o.Token != "" {
		a = append(a, "--token", o.Token)
	}
	if o.Project != "" {
		a = append(a, "--project", o.Project)
	}
	if o.Team != "" {
		a = append(a, "--scope", o.Team)
	}
	return a
}

// CreateArgs builds the `sandbox create` argument list.
func CreateArgs(name string, o Options, env map[string]string) []string {
	timeout := o.Timeout
	if timeout == "" {
		timeout = "1h"
	}
	a := []string{"create", "--silent", "--non-persistent", "--name", name, "--timeout", timeout}
	a = append(a, authArgs(o)...)
	if o.VCPUs > 0 {
		a = append(a, "--vcpus", fmt.Sprint(o.VCPUs))
	}
	if o.Image != "" {
		a = append(a, "--image", o.Image)
	}
	if o.Snapshot != "" {
		a = append(a, "--snapshot", o.Snapshot)
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a = append(a, "--env", k+"="+env[k])
	}
	return a
}

// ExecArgs builds the `sandbox exec` argument list.
func ExecArgs(name string, o Options, command, cwd string, sudo bool) []string {
	a := append([]string{"exec"}, authArgs(o)...)
	a = append(a, "--workdir", cwd)
	if sudo {
		a = append(a, "--sudo")
	}
	return append(a, name, "--", "sh", "-c", command)
}

func (p *provider) Create(ctx context.Context, opts sandbox.IsolatedCreateOptions) (sandbox.IsolatedHandle, error) {
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	name := "agentgopher-" + hex.EncodeToString(suffix)
	if out, err := exec.CommandContext(ctx, p.o.Bin, CreateArgs(name, p.o, opts.Env)...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("sandbox create: %w: %s", err, strings.TrimSpace(string(out)))
	}
	h := &handle{p: p, name: name}
	if _, err := h.Exec(ctx, "mkdir -p "+RepoPath, sandbox.ExecOptions{Cwd: "/"}); err != nil {
		_ = h.Close(ctx)
		return nil, err
	}
	return h, nil
}

type handle struct {
	p    *provider
	name string
}

func (h *handle) WorktreePath() string { return RepoPath }

func (h *handle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = RepoPath
	}
	// Whether `sandbox exec` forwards stdin is undocumented; deliver it through
	// a file in the sandbox instead, as the Daytona handle does.
	if opts.Stdin != nil {
		stdinFile, err := os.CreateTemp("", "agentgopher-stdin-*")
		if err != nil {
			return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
		}
		_, werr := stdinFile.WriteString(*opts.Stdin)
		_ = stdinFile.Close()
		defer func() { _ = os.Remove(stdinFile.Name()) }()
		if werr != nil {
			return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: werr}
		}
		remote := "/tmp/" + filepath.Base(stdinFile.Name())
		if err := h.copy(ctx, stdinFile.Name(), h.name+":"+remote); err != nil {
			return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
		}
		command = fmt.Sprintf("(%s) < %s; rc=$?; rm -f %s; exit $rc", command, remote, remote)
	}
	cmd := exec.CommandContext(ctx, h.p.o.Bin, ExecArgs(h.name, h.p.o, command, cwd, opts.Sudo)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdoutTail := stream.NewBoundedTail(h.p.o.MaxOutputTailChars, "\n")
	lw := stream.NewLineWriter(func(line string) {
		stdoutTail.Push(line)
		if opts.OnLine != nil {
			opts.OnLine(line)
		}
	})
	cmd.Stdout = lw
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	lw.Flush()
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
	errTail := stream.NewBoundedTail(h.p.o.MaxOutputTailChars, "")
	errTail.Push(stderr.String())
	return sandbox.ExecResult{Stdout: stdoutTail.String(), Stderr: errTail.String(), ExitCode: cmd.ProcessState.ExitCode()}, nil
}

func (h *handle) copy(ctx context.Context, src, dst string) error {
	a := append([]string{"copy"}, authArgs(h.p.o)...)
	out, err := exec.CommandContext(ctx, h.p.o.Bin, append(a, src, dst)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sandbox copy %s %s: %w: %s", src, dst, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (h *handle) CopyIn(ctx context.Context, hostPath, sandboxPath string) error {
	info, err := os.Stat(hostPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return h.copy(ctx, hostPath, h.name+":"+sandboxPath)
	}
	// The CLI copies one file per call, so a directory travels as a tarball.
	tar, err := os.CreateTemp("", "agentgopher-copyin-*.tar.gz")
	if err != nil {
		return err
	}
	_ = tar.Close()
	defer func() { _ = os.Remove(tar.Name()) }()
	if out, err := exec.CommandContext(ctx, "tar", "-czf", tar.Name(), "-C", hostPath, ".").CombinedOutput(); err != nil {
		return fmt.Errorf("tar: %w: %s", err, out)
	}
	remote := "/tmp/" + filepath.Base(tar.Name())
	if err := h.copy(ctx, tar.Name(), h.name+":"+remote); err != nil {
		return err
	}
	res, err := h.Exec(ctx, fmt.Sprintf(`mkdir -p "%s" && tar -xzf "%s" -C "%s" && rm -f "%s"`, sandboxPath, remote, sandboxPath, remote), sandbox.ExecOptions{Cwd: "/"})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("extract in sandbox failed: %s", res.Stderr)
	}
	return nil
}

func (h *handle) CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error {
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		return err
	}
	return h.copy(ctx, h.name+":"+sandboxPath, hostPath)
}

// Close removes the sandbox even when the run context is already cancelled,
// so an aborted run does not leak a billable sandbox.
func (h *handle) Close(context.Context) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	a := append([]string{"remove"}, authArgs(h.p.o)...)
	out, err := exec.CommandContext(ctx, h.p.o.Bin, append(a, h.name)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sandbox remove: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
