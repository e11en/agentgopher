package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/sandbox"
)

// DefaultInTimeout bounds a whole sync-in.
const DefaultInTimeout = 5 * time.Minute

// execOK runs command in the sandbox and fails with Error on a non-zero exit.
func execOK(ctx context.Context, h sandbox.Handle, command, cwd string) (sandbox.ExecResult, error) {
	res, err := h.Exec(ctx, command, sandbox.ExecOptions{Cwd: cwd})
	if err != nil {
		return res, &Error{Message: fmt.Sprintf("sandbox exec failed: %s\n%v", command, err)}
	}
	if res.ExitCode != 0 {
		return res, &Error{Message: fmt.Sprintf("sandbox command failed (exit %d): %s\n%s", res.ExitCode, command, res.Stderr)}
	}
	return res, nil
}

func hostGit(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := gitx.Run(ctx, dir, args...)
	if err != nil {
		return "", &Error{Message: "host command failed: " + err.Error()}
	}
	return out, nil
}

// In bundles hostWorktreePath, copies the bundle into the sandbox, clones it
// at sandboxRepoDir, checks out branch and verifies HEAD matches the host.
func In(ctx context.Context, h sandbox.IsolatedHandle, hostWorktreePath, sandboxRepoDir, branch string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := syncIn(ctx, h, hostWorktreePath, sandboxRepoDir, branch)
	if ctx.Err() == context.DeadlineExceeded {
		return &InTimeoutError{Timeout: timeout}
	}
	return err
}

func syncIn(ctx context.Context, h sandbox.IsolatedHandle, hostWorktreePath, sandboxRepoDir, branch string) error {
	bundleDir, err := os.MkdirTemp("", "agentgopher-bundle-")
	if err != nil {
		return &Error{Message: "failed to create temp dir: " + err.Error()}
	}
	defer func() { _ = os.RemoveAll(bundleDir) }()
	bundleHost := filepath.Join(bundleDir, "repo.bundle")
	if _, err := hostGit(ctx, hostWorktreePath, "bundle", "create", bundleHost, "--all"); err != nil {
		return err
	}

	tmp, err := execOK(ctx, h, "mktemp -d -t agentgopher-XXXXXX", "")
	if err != nil {
		return err
	}
	sandboxTmp := strings.TrimSpace(tmp.Stdout)
	bundleSandbox := sandboxTmp + "/repo.bundle"
	if err := h.CopyIn(ctx, bundleHost, bundleSandbox); err != nil {
		return &Error{Message: "failed to copy bundle into sandbox: " + err.Error()}
	}

	// git clone needs an empty target, so clone next to the repo dir and swap.
	steps := []string{
		fmt.Sprintf(`git clone -q "%s" "%s_clone"`, bundleSandbox, sandboxRepoDir),
		fmt.Sprintf(`rm -rf "%s" && mv "%s_clone" "%s"`, sandboxRepoDir, sandboxRepoDir, sandboxRepoDir),
	}
	for _, cmd := range steps {
		if _, err := execOK(ctx, h, cmd, ""); err != nil {
			return err
		}
	}
	if _, err := execOK(ctx, h, "git checkout -q "+agent.ShellEscape(branch), sandboxRepoDir); err != nil {
		return err
	}
	_, _ = h.Exec(ctx, fmt.Sprintf(`rm -rf "%s"`, sandboxTmp), sandbox.ExecOptions{})

	hostHead, err := hostGit(ctx, hostWorktreePath, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	res, err := execOK(ctx, h, "git rev-parse HEAD", sandboxRepoDir)
	if err != nil {
		return err
	}
	if sandboxHead := strings.TrimSpace(res.Stdout); sandboxHead != hostHead {
		return &Error{Message: fmt.Sprintf("HEAD mismatch after sync-in: host=%s sandbox=%s", hostHead, sandboxHead)}
	}
	// Pin the shipped commit as the sync-out base now, so a host branch that
	// moves while the agent works never leaves format-patch without a base.
	if _, err := execOK(ctx, h, "git update-ref "+BaseRef+" "+hostHead, sandboxRepoDir); err != nil {
		return err
	}
	return nil
}
