package agentsandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	sandboxsdk "sigs.k8s.io/agent-sandbox/clients/go/sandbox"

	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/internal/tarx"
	"github.com/e11en/agentgopher/sandbox"
)

type provider struct{ o Options }

// New returns the provider. nil opts use the defaults.
func New(opts *Options) sandbox.IsolatedProvider {
	var o Options
	if opts != nil {
		o = *opts
	}
	return &provider{o: withDefaults(o)}
}

func (p *provider) Kind() sandbox.Kind     { return sandbox.KindIsolated }
func (p *provider) Name() string           { return "agent-sandbox" }
func (p *provider) Env() map[string]string { return p.o.Env }

func (p *provider) Create(ctx context.Context, opts sandbox.IsolatedCreateOptions) (sandbox.IsolatedHandle, error) {
	api, err := p.o.Open(ctx, opts.Env)
	if err != nil {
		return nil, fmt.Errorf("agent-sandbox claim: %w", err)
	}
	h := &handle{api: api, o: p.o}
	res, err := api.Run(ctx, "mkdir -p "+shellQuote(sandbox.RepoDir))
	if err != nil || res.ExitCode != 0 {
		_ = h.Close(ctx)
		if err == nil {
			err = fmt.Errorf("exit %d: %s", res.ExitCode, res.Stderr)
		}
		return nil, fmt.Errorf("agent-sandbox create workspace: %w", err)
	}
	return h, nil
}

type handle struct {
	api API
	o   Options
}

func (h *handle) WorktreePath() string { return sandbox.RepoDir }

// sdkPath maps an absolute sandbox path onto the SDK filesystem, which is
// rooted at / (sandboxd runs with --root-dir=/).
func sdkPath(p string) string { return strings.TrimPrefix(p, "/") }

func (h *handle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = sandbox.RepoDir
	}
	if opts.Sudo {
		// sudo wraps the whole shell so every command in a chain runs as root.
		command = "sudo sh -c " + shellQuote(command)
	}
	// The SDK has no stdin pipe; deliver a prompt through a temp file.
	if opts.Stdin != nil {
		stdinPath := "/tmp/agentgopher-stdin-" + randomSuffix()
		if err := h.api.WriteReader(ctx, sdkPath(stdinPath), strings.NewReader(*opts.Stdin)); err != nil {
			return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
		}
		command = fmt.Sprintf("(%s) < %s; rc=$?; rm -f %s; exit $rc", command, stdinPath, stdinPath)
	}
	full := "cd " + shellQuote(cwd) + " && " + command

	if opts.OnLine == nil {
		res, err := h.api.Run(ctx, full)
		if ctx.Err() != nil {
			return sandbox.ExecResult{}, ctx.Err()
		}
		if err != nil {
			return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
		}
		return sandbox.ExecResult{Stdout: h.tail(res.Stdout, "\n"), Stderr: h.tail(res.Stderr, ""), ExitCode: res.ExitCode}, nil
	}

	proc, err := h.api.Start(ctx, full)
	if err != nil {
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
	}
	defer func() { _ = proc.Close() }()
	stdoutTail := stream.NewBoundedTail(h.o.MaxOutputTailChars, "\n")
	stderrTail := stream.NewBoundedTail(h.o.MaxOutputTailChars, "")
	lw := stream.NewLineWriter(func(line string) {
		stdoutTail.Push(line)
		opts.OnLine(line)
	})
	exitCode, exitSeen := 0, false
	for {
		ev, err := proc.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			lw.Flush()
			if ctx.Err() != nil {
				return sandbox.ExecResult{}, ctx.Err()
			}
			return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
		}
		switch ev.Type {
		case sandboxsdk.ProcessEventStdout:
			_, _ = lw.Write(ev.Data)
		case sandboxsdk.ProcessEventStderr:
			stderrTail.Push(string(ev.Data))
		case sandboxsdk.ProcessEventExit:
			exitCode, exitSeen = ev.ExitCode, true
		}
	}
	lw.Flush()
	if !exitSeen {
		// A missing exit event must not read as success.
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: errors.New("agent-sandbox did not report an exit code")}
	}
	return sandbox.ExecResult{Stdout: stdoutTail.String(), Stderr: stderrTail.String(), ExitCode: exitCode}, nil
}

func (h *handle) tail(s, sep string) string {
	t := stream.NewBoundedTail(h.o.MaxOutputTailChars, sep)
	t.Push(s)
	return t.String()
}

func (h *handle) CopyIn(ctx context.Context, hostPath, sandboxPath string) error {
	info, err := os.Stat(hostPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		f, err := os.Open(hostPath)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		// sandboxd creates missing parent directories on write.
		return h.api.WriteReader(ctx, sdkPath(sandboxPath), f)
	}
	// A directory travels as one tar file and is unpacked in place.
	tarPath := "/tmp/agentgopher-copy-" + randomSuffix() + ".tar"
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(tarx.Pack(pw, hostPath, path.Base(sandboxPath))) }()
	err = h.api.WriteReader(ctx, sdkPath(tarPath), pr)
	_ = pr.Close()
	if err != nil {
		return err
	}
	dir := shellQuote(path.Dir(sandboxPath))
	res, err := h.api.Run(ctx, "mkdir -p "+dir+" && tar -xf "+shellQuote(tarPath)+" -C "+dir+"; rc=$?; rm -f "+shellQuote(tarPath)+"; exit $rc")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("copy %s into sandbox: tar exited %d: %s", hostPath, res.ExitCode, res.Stderr)
	}
	return nil
}

func (h *handle) CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error {
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(hostPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := h.api.ReadTo(ctx, sdkPath(sandboxPath), f); err != nil {
		return err
	}
	return f.Close()
}

// Close releases the claim even when the run context is already cancelled,
// so an aborted run does not leave a sandbox Pod behind.
func (h *handle) Close(context.Context) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return h.api.Close(ctx)
}

func randomSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
