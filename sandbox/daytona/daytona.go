// Package daytona runs agents in Daytona sandboxes through the official Go SDK.
package daytona

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/daytonaio/daytona/libs/sdk-go/pkg/daytona"
	"github.com/daytonaio/daytona/libs/sdk-go/pkg/types"

	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/sandbox"
)

// Options configure the provider. Empty auth fields fall back to the
// DAYTONA_API_KEY, DAYTONA_API_URL and DAYTONA_TARGET environment variables.
type Options struct {
	APIKey             string
	APIURL             string
	Target             string
	Snapshot           string
	Image              string
	Env                map[string]string
	MaxOutputTailChars int
}

type provider struct{ o Options }

// New returns an isolated provider that creates one Daytona sandbox per run.
func New(opts *Options) sandbox.IsolatedProvider {
	p := &provider{}
	if opts != nil {
		p.o = *opts
	}
	if p.o.MaxOutputTailChars == 0 {
		p.o.MaxOutputTailChars = stream.MaxTailChars
	}
	return p
}

func (p *provider) Kind() sandbox.Kind     { return sandbox.KindIsolated }
func (p *provider) Name() string           { return "daytona" }
func (p *provider) Env() map[string]string { return p.o.Env }

func (p *provider) Create(ctx context.Context, opts sandbox.IsolatedCreateOptions) (sandbox.IsolatedHandle, error) {
	client, err := daytona.NewClientWithConfig(&types.DaytonaConfig{APIKey: p.o.APIKey, APIUrl: p.o.APIURL, Target: p.o.Target})
	if err != nil {
		return nil, fmt.Errorf("daytona client: %w", err)
	}
	var params any
	switch {
	case p.o.Snapshot != "":
		params = types.SnapshotParams{Snapshot: p.o.Snapshot, SandboxBaseParams: types.SandboxBaseParams{EnvVars: opts.Env}}
	default:
		params = types.ImageParams{Image: p.o.Image, SandboxBaseParams: types.SandboxBaseParams{EnvVars: opts.Env}}
	}
	sb, err := client.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("daytona create: %w", err)
	}
	workdir, err := sb.GetWorkingDir(ctx)
	if err != nil || workdir == "" {
		workdir = "/home/daytona"
	}
	return &handle{p: p, sb: sb, workdir: workdir}, nil
}

type handle struct {
	p       *provider
	sb      *daytona.Sandbox
	workdir string
}

func (h *handle) WorktreePath() string { return h.workdir }

func (h *handle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = h.workdir
	}
	if opts.Sudo {
		command = "sudo " + command
	}
	// The SDK has no stdin pipe; deliver a prompt through a temp file.
	if opts.Stdin != nil {
		stdinPath := path.Join("/tmp", "agentgopher-stdin-"+randomSuffix())
		if err := h.sb.FileSystem.UploadFile(ctx, []byte(*opts.Stdin), stdinPath); err != nil {
			return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
		}
		command = fmt.Sprintf("(%s) < %s; rc=$?; rm -f %s; exit $rc", command, stdinPath, stdinPath)
	}
	full := fmt.Sprintf("cd %s && %s", shellQuote(cwd), command)

	if opts.OnLine == nil {
		res, err := h.sb.Process.ExecuteCommand(ctx, full)
		if err != nil {
			return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
		}
		return sandbox.ExecResult{Stdout: res.Result, ExitCode: res.ExitCode}, nil
	}

	session := "agentgopher-" + randomSuffix()
	if err := h.sb.Process.CreateSession(ctx, session); err != nil {
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
	}
	defer func() { _ = h.sb.Process.DeleteSession(context.Background(), session) }()

	started, err := h.sb.Process.ExecuteSessionCommand(ctx, session, full, true, false)
	if err != nil {
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
	}
	cmdID, _ := started["cmdId"].(string)

	stdoutTail := stream.NewBoundedTail(h.p.o.MaxOutputTailChars, "\n")
	stderrTail := stream.NewBoundedTail(h.p.o.MaxOutputTailChars, "")
	lw := stream.NewLineWriter(func(line string) {
		stdoutTail.Push(line)
		opts.OnLine(line)
	})
	stdoutCh, stderrCh := make(chan string), make(chan string)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for chunk := range stdoutCh {
			_, _ = lw.Write([]byte(chunk))
		}
		lw.Flush()
	}()
	go func() {
		defer wg.Done()
		for chunk := range stderrCh {
			stderrTail.Push(chunk)
		}
	}()
	streamErr := h.sb.Process.GetSessionCommandLogsStream(ctx, session, cmdID, stdoutCh, stderrCh)
	close(stdoutCh)
	close(stderrCh)
	wg.Wait()
	if ctx.Err() != nil {
		return sandbox.ExecResult{}, ctx.Err()
	}
	if streamErr != nil {
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: streamErr}
	}
	info, err := h.sb.Process.GetSessionCommand(ctx, session, cmdID)
	if err != nil {
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
	}
	exit := 0
	switch v := info["exitCode"].(type) {
	case float64:
		exit = int(v)
	case int:
		exit = v
	case int32:
		exit = int(v)
	case int64:
		exit = int(v)
	}
	return sandbox.ExecResult{Stdout: stdoutTail.String(), Stderr: stderrTail.String(), ExitCode: exit}, nil
}

func (h *handle) CopyIn(ctx context.Context, hostPath, sandboxPath string) error {
	info, err := os.Stat(hostPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return h.sb.FileSystem.UploadFile(ctx, hostPath, sandboxPath)
	}
	return filepath.WalkDir(hostPath, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(hostPath, p)
		return h.sb.FileSystem.UploadFile(ctx, p, path.Join(sandboxPath, filepath.ToSlash(rel)))
	})
}

func (h *handle) CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error {
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		return err
	}
	_, err := h.sb.FileSystem.DownloadFile(ctx, sandboxPath, &hostPath)
	return err
}

func (h *handle) Close(ctx context.Context) error { return h.sb.Delete(ctx) }

func randomSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
