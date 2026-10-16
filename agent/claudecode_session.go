package agent

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/session"
)

const defaultClaudeSandboxProjectsDir = "/home/agent/.claude/projects"

type claudeStorage struct {
	hostDir    string // "" means the default
	sandboxDir string
}

func newClaudeStorage(dirs *SessionDirs) *claudeStorage {
	s := &claudeStorage{sandboxDir: defaultClaudeSandboxProjectsDir}
	if dirs != nil {
		s.hostDir = dirs.Host
		if dirs.Sandbox != "" {
			s.sandboxDir = dirs.Sandbox
		}
	}
	return s
}

func (s *claudeStorage) HostSessionFilePath(cwd, id string) string {
	return session.ClaudeHostPath(cwd, id, s.hostDir)
}

func (s *claudeStorage) ExistsOnHost(cwd, id string) (bool, error) {
	return fileExists(s.HostSessionFilePath(cwd, id))
}

func (s *claudeStorage) ReadHostSession(cwd, id string) (string, bool, error) {
	return readHostFile(s.HostSessionFilePath(cwd, id))
}

func (s *claudeStorage) FindByIDOnHost(id string) (HostSessionLookup, error) {
	return session.FindClaudeOnHost(id, s.hostDir), nil
}

func (s *claudeStorage) copyOut(ctx context.Context, h sandbox.BindMountHandle, src, fromCwd, toCwd, dst string) error {
	jsonl, err := readSandboxFile(ctx, h, src)
	if err != nil {
		return err
	}
	return writeHostFile(dst, session.TransferClaude(jsonl, fromCwd, toCwd))
}

func (s *claudeStorage) CaptureToHost(ctx context.Context, t SessionTransfer) error {
	// The main session is fatal when missing; the user expects their session.
	main := session.ClaudeSandboxPath(t.SandboxCwd, t.SessionID, s.sandboxDir)
	if err := s.copyOut(ctx, t.Handle, main, t.SandboxCwd, t.HostCwd, s.HostSessionFilePath(t.HostCwd, t.SessionID)); err != nil {
		return fmt.Errorf("capture session %s: %w", t.SessionID, err)
	}
	// Subagent transcripts are best-effort: a missing directory is the normal case.
	subDir := session.ClaudeSubagentsDirInSandbox(t.SandboxCwd, t.SessionID, s.sandboxDir)
	res, err := t.Handle.Exec(ctx, fmt.Sprintf("find %s -type f -name 'agent-*.jsonl' 2>/dev/null", ShellEscape(subDir)), sandbox.ExecOptions{})
	if err != nil || res.ExitCode != 0 || strings.TrimSpace(res.Stdout) == "" {
		return nil
	}
	hostSubDir := session.ClaudeSubagentsDirOnHost(t.HostCwd, t.SessionID, s.hostDir)
	for _, p := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if err := s.copyOut(ctx, t.Handle, p, t.SandboxCwd, t.HostCwd, filepath.Join(hostSubDir, path.Base(p))); err != nil {
			fmt.Fprintf(os.Stderr, "agentgopher: failed to capture Claude subagent transcript %s: %v\n", p, err)
		}
	}
	return nil
}

func (s *claudeStorage) ResumeIntoSandbox(ctx context.Context, t SessionTransfer) error {
	b, err := os.ReadFile(s.HostSessionFilePath(t.HostCwd, t.SessionID))
	if err != nil {
		return err
	}
	dst := session.ClaudeSandboxPath(t.SandboxCwd, t.SessionID, s.sandboxDir)
	return writeSandboxFile(ctx, t.Handle, dst, session.TransferClaude(string(b), t.HostCwd, t.SandboxCwd))
}
