package agent

import (
	"strings"
	"testing"
)

func TestClaudeCodePrintCommandDefaults(t *testing.T) {
	p := ClaudeCode("claude-opus-4-8", nil)
	pc := p.BuildPrintCommand(CommandOptions{Prompt: "hello", DangerouslySkipPermissions: true})
	want := "claude --print --verbose --dangerously-skip-permissions --output-format stream-json --model 'claude-opus-4-8' -p -"
	if pc.Command != want {
		t.Fatalf("Command = %q\nwant      %q", pc.Command, want)
	}
	if pc.Stdin == nil || *pc.Stdin != "hello" {
		t.Fatal("prompt must be delivered over stdin")
	}
	if p.Name() != "claude-code" || !p.CaptureSessions() {
		t.Fatalf("name=%q capture=%v", p.Name(), p.CaptureSessions())
	}
}

func TestClaudeCodePrintCommandOptions(t *testing.T) {
	p := ClaudeCode("m", &ClaudeCodeOptions{PermissionMode: "plan", Effort: "high"})
	pc := p.BuildPrintCommand(CommandOptions{Prompt: "x", DangerouslySkipPermissions: true, ResumeSession: "abc'd", ForkSession: true})
	want := "claude --print --verbose --permission-mode plan --output-format stream-json --model 'm' --effort high --resume 'abc'\\''d' --fork-session -p -"
	if pc.Command != want {
		t.Fatalf("Command = %q\nwant      %q", pc.Command, want)
	}
}

func TestClaudeCodeForkWithoutResumeIsIgnored(t *testing.T) {
	pc := ClaudeCode("m", nil).BuildPrintCommand(CommandOptions{Prompt: "x", ForkSession: true})
	if strings.Contains(pc.Command, "--fork-session") || strings.Contains(pc.Command, "--dangerously") {
		t.Fatalf("Command = %q", pc.Command)
	}
}

func TestParseStreamLineAssistantTextAndToolCalls(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"Let me "},{"type":"text","text":"look."},{"type":"tool_use","name":"Bash","input":{"command":"ls -la"}},{"type":"tool_use","name":"Read","input":{"file_path":"a.go"}},{"type":"text","text":"done"}]}}`
	events := ClaudeCode("m", nil).ParseStreamLine(line)
	want := []StreamEvent{
		{Type: EventText, Text: "Let me look."},
		{Type: EventToolCall, Name: "Bash", Args: "ls -la"},
		{Type: EventText, Text: "done"},
	}
	if len(events) != len(want) {
		t.Fatalf("got %+v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v", i, events[i], want[i])
		}
	}
}

func TestParseStreamLineResultInitAndJunk(t *testing.T) {
	p := ClaudeCode("m", nil)
	if ev := p.ParseStreamLine(`{"type":"result","result":"all good"}`); len(ev) != 1 || ev[0].Type != EventResult || ev[0].Text != "all good" {
		t.Fatalf("result: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"system","subtype":"init","session_id":"s-1"}`); len(ev) != 1 || ev[0].Type != EventSessionID || ev[0].SessionID != "s-1" {
		t.Fatalf("init: %+v", ev)
	}
	if ev := p.ParseStreamLine("not json"); len(ev) != 0 {
		t.Fatalf("junk: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"assistant","message":{"content":"string"}}`); len(ev) != 0 {
		t.Fatalf("non-array content: %+v", ev)
	}
}

func TestParseSessionUsageTakesLastAssistantMessage(t *testing.T) {
	content := `{"type":"assistant","message":{"usage":{"input_tokens":1,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"output_tokens":4}}}
{"type":"user"}
{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"output_tokens":40}}}
`
	u, ok := ClaudeCode("m", nil).(SessionUsageParser).ParseSessionUsage(content)
	if !ok || u != (Usage{10, 20, 30, 40}) {
		t.Fatalf("got %+v ok=%v", u, ok)
	}
	if _, ok := ClaudeCode("m", nil).(SessionUsageParser).ParseSessionUsage("{}\n"); ok {
		t.Fatal("expected no usage")
	}
}

func TestShellEscape(t *testing.T) {
	if got := ShellEscape("it's"); got != `'it'\''s'` {
		t.Fatalf("got %s", got)
	}
}
