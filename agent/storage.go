package agent

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/e11en/agentgopher/sandbox"
)

// readSandboxFile copies a sandbox file to a temp file on the host and returns its content.
func readSandboxFile(ctx context.Context, h sandbox.BindMountHandle, sandboxPath string) (string, error) {
	tmp, err := os.CreateTemp("", "agentgopher-session-*.jsonl")
	if err != nil {
		return "", err
	}
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := h.CopyFileOut(ctx, sandboxPath, tmp.Name()); err != nil {
		return "", err
	}
	b, err := os.ReadFile(tmp.Name())
	return string(b), err
}

// writeSandboxFile writes content to a sandbox path, creating its directory.
func writeSandboxFile(ctx context.Context, h sandbox.BindMountHandle, sandboxPath, content string) error {
	tmp, err := os.CreateTemp("", "agentgopher-session-*.jsonl")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	_ = tmp.Close()
	if _, err := h.Exec(ctx, fmt.Sprintf("mkdir -p %s", ShellEscape(path.Dir(sandboxPath))), sandbox.ExecOptions{}); err != nil {
		return err
	}
	return h.CopyFileIn(ctx, tmp.Name(), sandboxPath)
}

func writeHostFile(p, content string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func readHostFile(p string) (string, bool, error) {
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

func fileExists(p string) (bool, error) {
	_, err := os.Stat(p)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
