package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/session"
)

func TestPiPrintCommand(t *testing.T) {
	pc := Pi("m", &PiOptions{Thinking: "high"}).BuildPrintCommand(CommandOptions{Prompt: "x", ResumeSession: "s"})
	if pc.Command != "pi -p --mode json --model 'm' --thinking high --session 's'" || *pc.Stdin != "x" {
		t.Fatalf("got %q", pc.Command)
	}
	if pc := Pi("m", nil).BuildPrintCommand(CommandOptions{Prompt: "x"}); pc.Command != "pi -p --mode json --model 'm'" {
		t.Fatalf("got %q", pc.Command)
	}
}

func TestPiParseStreamLine(t *testing.T) {
	p := Pi("m", nil)
	cases := []struct {
		line string
		want []StreamEvent
	}{
		{`{"type":"session","id":"s1"}`, []StreamEvent{{Type: EventSessionID, SessionID: "s1"}}},
		{`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hi"}}`, []StreamEvent{{Type: EventText, Text: "hi"}}},
		{`{"type":"message_update","assistantMessageEvent":{"type":"other"}}`, nil},
		{`{"type":"tool_execution_start","toolName":"Bash","args":{"command":"ls"}}`, []StreamEvent{{Type: EventToolCall, Name: "Bash", Args: "ls"}}},
		{`{"type":"tool_execution_start","toolName":"Read","args":{"path":"x"}}`, nil},
		{`{"type":"agent_error","error":"boom"}`, []StreamEvent{{Type: EventResult, Text: "boom"}}},
		{`{"type":"agent_end","messages":[{"role":"user","content":[{"type":"text","text":"q"}]},{"role":"assistant","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}]}`, []StreamEvent{{Type: EventResult, Text: "ab"}}},
	}
	for _, c := range cases {
		got := p.ParseStreamLine(c.line)
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %+v", c.line, got)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: %+v want %+v", c.line, got[i], c.want[i])
			}
		}
	}
}

func TestPiStorageRoundTrip(t *testing.T) {
	root := t.TempDir()
	hostSessions := t.TempDir()
	h := &dirHandle{root: root}
	st := Pi("m", &PiOptions{SessionStorage: &SessionDirs{Host: hostSessions, Sandbox: "/home/agent/.pi/agent/sessions"}}).(SessionStorageProvider).SessionStorage()
	hostCwd, sandboxCwd := "/Users/ellen/repo", "/home/agent/workspace"

	sandboxFile := h.mapPath("/home/agent/.pi/agent/sessions/" + session.EncodePiSessionDir(sandboxCwd) + "/2026-10-15_s9.jsonl")
	os.MkdirAll(filepath.Dir(sandboxFile), 0o755)
	os.WriteFile(sandboxFile, []byte(`{"type":"session","id":"s9","cwd":"/home/agent/workspace"}`), 0o644)

	tr := SessionTransfer{HostCwd: hostCwd, SandboxCwd: sandboxCwd, SessionID: "s9", Handle: h}
	if err := st.CaptureToHost(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	// Landed under the host cwd's encoded dir, not the sandbox's (pi resolves --session against the current project first).
	hostFile := filepath.Join(session.PiSessionDirPath(hostCwd, hostSessions), "2026-10-15_s9.jsonl")
	b, err := os.ReadFile(hostFile)
	if err != nil || !strings.Contains(string(b), `"cwd":"/Users/ellen/repo"`) {
		t.Fatalf("host: %q %v", b, err)
	}
	if st.HostSessionFilePath(hostCwd, "s9") != session.PiSessionDirPath(hostCwd, hostSessions) {
		t.Fatal("HostSessionFilePath must be the session directory")
	}

	os.Remove(sandboxFile)
	if err := st.ResumeIntoSandbox(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(sandboxFile)
	if err != nil || !strings.Contains(string(b), `"cwd":"/home/agent/workspace"`) {
		t.Fatalf("sandbox: %q %v", b, err)
	}
}
