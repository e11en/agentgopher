package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/session"
)

func TestClaudeStorageCaptureRewritesCwdAndCopiesSubagents(t *testing.T) {
	root := t.TempDir()
	hostProjects := filepath.Join(t.TempDir(), "projects")
	h := &dirHandle{root: root}
	p := ClaudeCode("m", &ClaudeCodeOptions{SessionStorage: &SessionDirs{Host: hostProjects, Sandbox: "/home/agent/.claude/projects"}})
	st := p.(SessionStorageProvider).SessionStorage()

	sandboxCwd, hostCwd := "/home/agent/workspace", "/Users/ellen/repo"
	sessPath := h.mapPath(session.ClaudeSandboxPath(sandboxCwd, "s1", "/home/agent/.claude/projects"))
	os.MkdirAll(filepath.Dir(sessPath), 0o755)
	os.WriteFile(sessPath, []byte(`{"cwd":"/home/agent/workspace","type":"user"}`+"\n"), 0o644)
	subDir := h.mapPath(session.ClaudeSubagentsDirInSandbox(sandboxCwd, "s1", "/home/agent/.claude/projects"))
	os.MkdirAll(subDir, 0o755)
	os.WriteFile(filepath.Join(subDir, "agent-a.jsonl"), []byte(`{"cwd":"/home/agent/workspace"}`), 0o644)

	if err := st.CaptureToHost(context.Background(), SessionTransfer{HostCwd: hostCwd, SandboxCwd: sandboxCwd, SessionID: "s1", Handle: h}); err != nil {
		t.Fatal(err)
	}
	content, ok, err := st.ReadHostSession(hostCwd, "s1")
	if err != nil || !ok || !strings.Contains(content, `"cwd":"/Users/ellen/repo"`) {
		t.Fatalf("content=%q ok=%v err=%v", content, ok, err)
	}
	if st.HostSessionFilePath(hostCwd, "s1") != session.ClaudeHostPath(hostCwd, "s1", hostProjects) {
		t.Fatal("HostSessionFilePath mismatch")
	}
	sub, err := os.ReadFile(filepath.Join(session.ClaudeSubagentsDirOnHost(hostCwd, "s1", hostProjects), "agent-a.jsonl"))
	if err != nil || !strings.Contains(string(sub), "/Users/ellen/repo") {
		t.Fatalf("subagent: %q %v", sub, err)
	}
	exists, _ := st.ExistsOnHost(hostCwd, "s1")
	if !exists {
		t.Fatal("ExistsOnHost false")
	}
	if lookup, _ := st.FindByIDOnHost("s1"); lookup.Path == "" {
		t.Fatal("FindByIDOnHost failed")
	}
}

func TestClaudeStorageResumeIntoSandbox(t *testing.T) {
	root := t.TempDir()
	hostProjects := t.TempDir()
	h := &dirHandle{root: root}
	st := ClaudeCode("m", &ClaudeCodeOptions{SessionStorage: &SessionDirs{Host: hostProjects, Sandbox: "/home/agent/.claude/projects"}}).(SessionStorageProvider).SessionStorage()
	hostCwd, sandboxCwd := "/Users/ellen/repo", "/home/agent/workspace"
	hostPath := session.ClaudeHostPath(hostCwd, "s2", hostProjects)
	os.MkdirAll(filepath.Dir(hostPath), 0o755)
	os.WriteFile(hostPath, []byte(`{"cwd":"/Users/ellen/repo"}`), 0o644)

	if err := st.ResumeIntoSandbox(context.Background(), SessionTransfer{HostCwd: hostCwd, SandboxCwd: sandboxCwd, SessionID: "s2", Handle: h}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(h.mapPath(session.ClaudeSandboxPath(sandboxCwd, "s2", "/home/agent/.claude/projects")))
	if err != nil || !strings.Contains(string(b), `"cwd":"/home/agent/workspace"`) {
		t.Fatalf("got %q, %v", b, err)
	}
}

func TestClaudeStorageMissingSessionIsFatal(t *testing.T) {
	st := ClaudeCode("m", &ClaudeCodeOptions{SessionStorage: &SessionDirs{Host: t.TempDir(), Sandbox: "/home/agent/.claude/projects"}}).(SessionStorageProvider).SessionStorage()
	err := st.CaptureToHost(context.Background(), SessionTransfer{HostCwd: "/h", SandboxCwd: "/s", SessionID: "none", Handle: &dirHandle{root: t.TempDir()}})
	if err == nil {
		t.Fatal("expected error when the main session file is missing")
	}
	if _, ok, _ := st.ReadHostSession("/h", "none"); ok {
		t.Fatal("expected ok=false")
	}
}

func TestClaudeInteractiveArgs(t *testing.T) {
	args := ClaudeCode("m", &ClaudeCodeOptions{Effort: "high"}).(InteractiveArgsBuilder).BuildInteractiveArgs(CommandOptions{Prompt: "hi", DangerouslySkipPermissions: true})
	want := []string{"claude", "--dangerously-skip-permissions", "--model", "m", "--effort", "high", "hi"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v", args)
	}
	args = ClaudeCode("m", &ClaudeCodeOptions{PermissionMode: "plan"}).(InteractiveArgsBuilder).BuildInteractiveArgs(CommandOptions{DangerouslySkipPermissions: true})
	if strings.Join(args, " ") != "claude --permission-mode plan --model m" {
		t.Fatalf("args = %v", args)
	}
}
