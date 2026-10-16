package agent

import (
	"errors"
	"strings"
	"testing"
)

func TestCursorCommands(t *testing.T) {
	pc := Cursor("m", nil).BuildPrintCommand(CommandOptions{Prompt: "do it", DangerouslySkipPermissions: true})
	if pc.Command != "agent --print --output-format stream-json --model 'm' --force 'do it'" || pc.Stdin != nil || pc.Err != nil {
		t.Fatalf("got %+v", pc)
	}
	big := strings.Repeat("x", ArgvPromptMaxBytes+1)
	var tooLarge *PromptTooLargeError
	if pc := Cursor("m", nil).BuildPrintCommand(CommandOptions{Prompt: big}); !errors.As(pc.Err, &tooLarge) || tooLarge.Provider != "cursor" {
		t.Fatalf("err = %v", pc.Err)
	}
	args := Cursor("m", nil).(InteractiveArgsBuilder).BuildInteractiveArgs(CommandOptions{Prompt: "p", DangerouslySkipPermissions: true})
	if strings.Join(args, " ") != "agent --model m --force p" {
		t.Fatalf("args = %v", args)
	}
	ev := Cursor("m", nil).ParseStreamLine(`{"type":"tool_call","subtype":"started","tool_call":{"readToolCall":{"args":{"path":"a.go"}}}}`)
	if len(ev) != 1 || ev[0] != (StreamEvent{Type: EventToolCall, Name: "Read", Args: "a.go"}) {
		t.Fatalf("read: %+v", ev)
	}
	ev = Cursor("m", nil).ParseStreamLine(`{"type":"tool_call","subtype":"started","tool_call":{"function":{"name":"shell","arguments":"{\"command\":\"ls\"}"}}}`)
	if len(ev) != 1 || ev[0] != (StreamEvent{Type: EventToolCall, Name: "Bash", Args: "ls"}) {
		t.Fatalf("function: %+v", ev)
	}
	ev = Cursor("m", nil).ParseStreamLine(`{"type":"result","result":"r"}`)
	if len(ev) != 1 || ev[0].Type != EventResult {
		t.Fatalf("fallback to stream-json: %+v", ev)
	}
}

func TestOpenCodeCommands(t *testing.T) {
	pc := OpenCode("m", &OpenCodeOptions{Variant: "high", Agent: "plan"}).BuildPrintCommand(CommandOptions{Prompt: "p", DangerouslySkipPermissions: true})
	if pc.Command != "opencode run --format json --model 'm' --variant 'high' --agent 'plan' --dangerously-skip-permissions 'p'" {
		t.Fatalf("got %q", pc.Command)
	}
	args := OpenCode("m", &OpenCodeOptions{Agent: "build"}).(InteractiveArgsBuilder).BuildInteractiveArgs(CommandOptions{Prompt: "p"})
	if strings.Join(args, " ") != "opencode --model m --agent build --prompt p" {
		t.Fatalf("args = %v", args)
	}
	p := OpenCode("m", nil)
	if ev := p.ParseStreamLine(`{"type":"step_start","sessionID":"s"}`); len(ev) != 1 || ev[0].SessionID != "s" {
		t.Fatalf("session: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"text","part":{"type":"text","text":"t"}}`); len(ev) != 2 || ev[1].Type != EventResult {
		t.Fatalf("text: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"tool_use","part":{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"ls"}}}}`); len(ev) != 1 || ev[0].Args != "ls" || ev[0].Name != "bash" {
		t.Fatalf("tool: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"tool_use","part":{"type":"tool","tool":"bash","state":{"status":"running","input":{"command":"ls"}}}}`); len(ev) != 0 {
		t.Fatalf("running must be skipped: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"tool_use","part":{"type":"tool","tool":"edit","state":{"status":"completed","input":{"file":"a"}}}}`); len(ev) != 1 || ev[0].Args != `{"file":"a"}` {
		t.Fatalf("json fallback: %+v", ev)
	}
}

func TestCopilotCommands(t *testing.T) {
	pc := Copilot("m", &CopilotOptions{Effort: "high"}).BuildPrintCommand(CommandOptions{Prompt: "p", DangerouslySkipPermissions: true})
	if pc.Command != "copilot -p 'p' --output-format json --model 'm' --allow-all-tools --effort high" {
		t.Fatalf("got %q", pc.Command)
	}
	args := Copilot("m", nil).(InteractiveArgsBuilder).BuildInteractiveArgs(CommandOptions{Prompt: "p"})
	if strings.Join(args, " ") != "copilot --model m -i p" {
		t.Fatalf("args = %v", args)
	}
	p := Copilot("m", nil)
	if ev := p.ParseStreamLine(`{"type":"assistant.message_delta","data":{"deltaContent":"d"}}`); len(ev) != 1 || ev[0].Text != "d" {
		t.Fatalf("delta: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"tool.execution_start","data":{"toolName":"bash","arguments":{"command":"ls"}}}`); len(ev) != 1 || ev[0].Name != "Bash" {
		t.Fatalf("tool: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"assistant.message","data":{"content":"final"}}`); len(ev) != 1 || ev[0].Type != EventResult {
		t.Fatalf("message: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"result","sessionId":"s"}`); len(ev) != 1 || ev[0].SessionID != "s" {
		t.Fatalf("result: %+v", ev)
	}
	for _, p := range []Provider{Cursor("m", nil), OpenCode("m", nil), Copilot("m", nil)} {
		if p.CaptureSessions() {
			t.Fatalf("%s must not capture sessions", p.Name())
		}
		if _, ok := p.(SessionStorageProvider); ok {
			t.Fatalf("%s must not have session storage", p.Name())
		}
	}
}
