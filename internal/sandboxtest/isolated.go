// Package sandboxtest holds filesystem-backed providers for tests.
package sandboxtest

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

// Isolated returns a provider whose sandbox is a temp directory on the host.
// Exec runs `sh -c` in it, so sync in and out can be tested without a remote.
func Isolated() sandbox.IsolatedProvider {
	return sandbox.NewIsolatedProvider(sandbox.IsolatedProviderConfig{
		Name: "test-isolated",
		Create: func(ctx context.Context, opts sandbox.IsolatedCreateOptions) (sandbox.IsolatedHandle, error) {
			root, err := os.MkdirTemp("", "agentgopher-test-")
			if err != nil {
				return nil, err
			}
			// pwd inside the sandbox prints the resolved path; macOS temp dirs sit behind a symlink.
			if resolved, err := filepath.EvalSymlinks(root); err == nil {
				root = resolved
			}
			worktree := filepath.Join(root, "workspace")
			if err := os.MkdirAll(worktree, 0o755); err != nil {
				return nil, err
			}
			inner, err := nosandbox.New(nil).Create(ctx, sandbox.NoSandboxCreateOptions{WorktreePath: worktree, Env: opts.Env})
			if err != nil {
				return nil, err
			}
			return &handle{Handle: inner, root: root}, nil
		},
	})
}

type handle struct {
	sandbox.Handle
	root string
}

func (h *handle) Close(context.Context) error { return os.RemoveAll(h.root) }

func (h *handle) CopyIn(_ context.Context, hostPath, sandboxPath string) error {
	info, err := os.Stat(hostPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.CopyFS(sandboxPath, os.DirFS(hostPath))
	}
	return copyFile(hostPath, sandboxPath)
}

func (h *handle) CopyFileOut(_ context.Context, sandboxPath, hostPath string) error {
	return copyFile(sandboxPath, hostPath)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
