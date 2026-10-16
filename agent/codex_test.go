package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/session"
)

func TestCodexPrintCommand(t *testing.T) {
	p := Codex("gpt-5", nil)
	pc := p.BuildPrintCommand(CommandOptions{Prompt: "x", DangerouslySkipPermissions: true})
	if pc.Command != "codex exec --json --dangerously-bypass-approvals-and-sandbox -m 'gpt-5'" || pc.Stdin == nil || *pc.Stdin != "x" {
		t.Fatalf("got %q", pc.Command)
	}
	pc = Codex("m", &CodexOptions{Effort: "high", ApprovalsReviewer: "auto_review"}).BuildPrintCommand(CommandOptions{Prompt: "x", ResumeSession: "t1"})
	want := `codex exec resume 't1' --json -a on-request -s danger-full-access -c 'approvals_reviewer="auto_review"' -m 'm' -c 'model_reasoning_effort="high"' -`
	if pc.Command != want {
		t.Fatalf("got  %q\nwant %q", pc.Command, want)
	}
	pc = Codex("m", nil).BuildPrintCommand(CommandOptions{Prompt: "x", ResumeSession: "t1", ForkSession: true})
	if !strings.HasPrefix(pc.Command, "codex exec fork 't1' --json") || !strings.HasSuffix(pc.Command, " -") {
		t.Fatalf("got %q", pc.Command)
	}
}

func TestCodexParseStreamLine(t *testing.T) {
	p := Codex("m", nil)
	cases := []struct {
		line string
		want []StreamEvent
	}{
		{`{"type":"thread.started","thread_id":"t1"}`, []StreamEvent{{Type: EventSessionID, SessionID: "t1"}}},
		{`{"type":"item.completed","item":{"type":"agent_message","text":"done"}}`, []StreamEvent{{Type: EventText, Text: "done"}, {Type: EventResult, Text: "done"}}},
		{`{"type":"item.started","item":{"type":"command_execution","command":"ls"}}`, []StreamEvent{{Type: EventToolCall, Name: "Bash", Args: "ls"}}},
		{`{"type":"error","message":"rate limited"}`, []StreamEvent{{Type: EventResult, Text: "rate limited"}}},
		{`{"type":"error","error":{"message":"nested"}}`, []StreamEvent{{Type: EventResult, Text: "nested"}}},
		{`{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":7}}`, []StreamEvent{{Type: EventUsage, Usage: &Usage{InputTokens: 60, CacheReadInputTokens: 40, OutputTokens: 7}}}},
		{`{"type":"turn.completed","usage":{"input_tokens":"x"}}`, nil},
		{`garbage`, nil},
	}
	for _, c := range cases {
		got := p.ParseStreamLine(c.line)
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %+v", c.line, got)
		}
		for i := range got {
			if got[i].Usage != nil {
				if *got[i].Usage != *c.want[i].Usage {
					t.Fatalf("%s: usage %+v", c.line, *got[i].Usage)
				}
				continue
			}
			if got[i] != c.want[i] {
				t.Fatalf("%s: event %d = %+v, want %+v", c.line, i, got[i], c.want[i])
			}
		}
	}
}

func TestCodexStorageRoundTrip(t *testing.T) {
	root := t.TempDir()
	hostSessions := t.TempDir()
	h := &dirHandle{root: root}
	st := Codex("m", &CodexOptions{SessionStorage: &SessionDirs{Host: hostSessions, Sandbox: "/home/agent/.codex/sessions"}}).(SessionStorageProvider).SessionStorage()

	sandboxFile := h.mapPath("/home/agent/.codex/sessions/2026/10/15/rollout-2026-10-15T10-00-00-t1.jsonl")
	os.MkdirAll(filepath.Dir(sandboxFile), 0o755)
	os.WriteFile(sandboxFile, []byte(`{"type":"session_meta","payload":{"cwd":"/home/agent/workspace"}}`), 0o644)

	tr := SessionTransfer{HostCwd: "/h", SandboxCwd: "/home/agent/workspace", SessionID: "t1", Handle: h}
	if err := st.CaptureToHost(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	hostFile := filepath.Join(hostSessions, "2026", "10", "15", "rollout-2026-10-15T10-00-00-t1.jsonl")
	b, err := os.ReadFile(hostFile)
	if err != nil || !strings.Contains(string(b), `"cwd":"/h"`) {
		t.Fatalf("host file: %q %v", b, err)
	}
	if st.HostSessionFilePath("/h", "t1") != hostFile {
		t.Fatalf("HostSessionFilePath = %q", st.HostSessionFilePath("/h", "t1"))
	}
	if st.HostSessionFilePath("/h", "unknown") != "" {
		t.Fatal("unknown id must yield empty path")
	}
	if ok, _ := st.ExistsOnHost("/h", "t1"); !ok {
		t.Fatal("ExistsOnHost")
	}

	os.Remove(sandboxFile)
	if err := st.ResumeIntoSandbox(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(sandboxFile)
	if err != nil || !strings.Contains(string(b), `"cwd":"/home/agent/workspace"`) {
		t.Fatalf("sandbox file: %q %v", b, err)
	}
	if _, err := LocateCodexSandbox(context.Background(), h, "missing", "/home/agent/.codex/sessions"); err == nil {
		t.Fatal("expected error for missing sandbox session")
	}
	_ = session.IsCodexSessionFilename
}

func TestCodexInteractiveArgs(t *testing.T) {
	args := Codex("m", nil).(InteractiveArgsBuilder).BuildInteractiveArgs(CommandOptions{Prompt: "go"})
	if strings.Join(args, " ") != "codex --model m go" {
		t.Fatalf("args = %v", args)
	}
}
