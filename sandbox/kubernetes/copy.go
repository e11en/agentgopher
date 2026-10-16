package kubernetes

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/e11en/agentgopher/sandbox"
)

// tarPath writes hostPath (file or directory) to w as a tar stream whose
// entries are rooted at name.
func tarPath(w io.Writer, hostPath, name string) error {
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(hostPath, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(hostPath, p)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(filepath.Join(name, rel))
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

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
	go func() { pw.CloseWithError(tarPath(pw, hostPath, filepath.Base(sandboxPath))) }()
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
