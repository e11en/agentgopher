package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/e11en/agentgopher/sandbox"
)

// dirHandle is a bind-mount handle whose "sandbox filesystem" is a host temp
// dir. Sandbox paths are mapped under root; exec runs sh on the host with
// the same mapping applied to absolute paths in the command.
type dirHandle struct {
	root string
}

func (h *dirHandle) WorktreePath() string        { return filepath.Join(h.root, "home/agent/workspace") }
func (h *dirHandle) Close(context.Context) error { return nil }
func (h *dirHandle) mapPath(p string) string     { return filepath.Join(h.root, p) }

func (h *dirHandle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", strings.ReplaceAll(command, "/home/agent", h.mapPath("/home/agent")))
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = 1
	}
	// Paths printed by the command come back as sandbox paths, as a real handle would report them.
	return sandbox.ExecResult{Stdout: strings.ReplaceAll(string(out), h.mapPath("/home/agent"), "/home/agent"), ExitCode: code}, nil
}

func (h *dirHandle) CopyFileIn(_ context.Context, hostPath, sandboxPath string) error {
	b, err := os.ReadFile(hostPath)
	if err != nil {
		return err
	}
	dst := h.mapPath(sandboxPath)
	os.MkdirAll(filepath.Dir(dst), 0o755)
	return os.WriteFile(dst, b, 0o644)
}

func (h *dirHandle) CopyFileOut(_ context.Context, sandboxPath, hostPath string) error {
	b, err := os.ReadFile(h.mapPath(sandboxPath))
	if err != nil {
		return err
	}
	os.MkdirAll(filepath.Dir(hostPath), 0o755)
	return os.WriteFile(hostPath, b, 0o644)
}
