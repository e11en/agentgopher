package kubernetes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/e11en/agentgopher/internal/tarx"
	"github.com/e11en/agentgopher/sandbox"
)

// execRaw runs command with raw stdin and stdout streams.
func (h *handle) execRaw(ctx context.Context, command string, stdin io.Reader, stdout io.Writer) (int, string, error) {
	execOpts := &corev1.PodExecOptions{Container: "agent", Command: []string{"sh", "-c", command}, Stdin: stdin != nil, Stdout: true, Stderr: true}
	exec, err := h.o.Executor(h.client, h.cfg, h.o.Namespace, h.pod, execOpts)
	if err != nil {
		return 0, "", &sandbox.StartError{Command: command, Cause: err}
	}
	var stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: stdin, Stdout: stdout, Stderr: &stderr})
	if ctx.Err() != nil {
		return 0, "", ctx.Err()
	}
	if err != nil {
		var ce utilexec.CodeExitError
		if !errors.As(err, &ce) {
			return 0, "", &sandbox.StartError{Command: command, Cause: err}
		}
		return ce.Code, stderr.String(), nil
	}
	return 0, stderr.String(), nil
}

// CopyIn copies a host file or directory to sandboxPath inside the Pod.
func (h *handle) CopyIn(ctx context.Context, hostPath, sandboxPath string) error {
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(tarx.Pack(pw, hostPath, filepath.Base(sandboxPath))) }()
	dir := shellQuote(filepath.Dir(sandboxPath))
	code, stderr, err := h.execRaw(ctx, "mkdir -p "+dir+" && tar -xf - -C "+dir, pr, io.Discard)
	_ = pr.Close()
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("copy %s into sandbox: tar exited %d: %s", hostPath, code, stderr)
	}
	return nil
}

// CopyFileOut copies one file from the Pod to hostPath.
func (h *handle) CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error {
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(hostPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	code, stderr, err := h.execRaw(ctx, "cat "+shellQuote(sandboxPath), nil, f)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("copy %s out of sandbox: cat exited %d: %s", sandboxPath, code, stderr)
	}
	return f.Close()
}
