package agent

import "encoding/json"

// OpenCodeOptions configures the OpenCode provider.
type OpenCodeOptions struct {
	// Variant is the provider-specific reasoning effort (for example "high").
	Variant string
	// Agent selects a named OpenCode agent such as "build" or "plan".
	Agent string
	Env   map[string]string
}

var openCodeToolArgFields = map[string]string{"bash": "command", "webfetch": "url", "task": "description"}

type opencode struct {
	model string
	opts  OpenCodeOptions
}

// OpenCode returns a provider for the `opencode` CLI. Not resumable: its
// sessions live in SQLite (ADR 0016).
func OpenCode(model string, opts *OpenCodeOptions) Provider {
	p := &opencode{model: model}
	if opts != nil {
		p.opts = *opts
	}
	return p
}

func (p *opencode) Name() string           { return "opencode" }
func (p *opencode) Env() map[string]string { return p.opts.Env }
func (p *opencode) CaptureSessions() bool  { return false }

func (p *opencode) BuildPrintCommand(o CommandOptions) PrintCommand {
	cmd := "opencode run --format json --model " + ShellEscape(p.model)
	if p.opts.Variant != "" {
		cmd += " --variant " + ShellEscape(p.opts.Variant)
	}
	if p.opts.Agent != "" {
		cmd += " --agent " + ShellEscape(p.opts.Agent)
	}
	if o.DangerouslySkipPermissions {
		cmd += " --dangerously-skip-permissions"
	}
	return PrintCommand{Command: cmd + " " + ShellEscape(o.Prompt)}
}

func (p *opencode) BuildInteractiveArgs(o CommandOptions) []string {
	args := []string{"opencode", "--model", p.model}
	if p.opts.Agent != "" {
		args = append(args, "--agent", p.opts.Agent)
	}
	// --prompt pre-fills the TUI textbox; -p is the basic-auth password flag.
	if o.Prompt != "" {
		args = append(args, "--prompt", o.Prompt)
	}
	return args
}

func (p *opencode) ParseStreamLine(line string) []StreamEvent {
	obj, ok := parseJSONObject(line)
	if !ok {
		return nil
	}
	part, _ := obj["part"].(map[string]any)
	switch obj["type"] {
	case "step_start":
		if id, ok := obj["sessionID"].(string); ok {
			return []StreamEvent{{Type: EventSessionID, SessionID: id}}
		}
	case "text":
		if text, ok := part["text"].(string); ok && part["type"] == "text" {
			return []StreamEvent{{Type: EventText, Text: text}, {Type: EventResult, Text: text}}
		}
	case "tool_use":
		tool, ok := part["tool"].(string)
		if !ok || part["type"] != "tool" {
			return nil
		}
		state, _ := part["state"].(map[string]any)
		if state["status"] != "completed" {
			return nil
		}
		input, ok := state["input"].(map[string]any)
		if !ok {
			return nil
		}
		if v, ok := input[openCodeToolArgFields[tool]].(string); ok {
			return []StreamEvent{{Type: EventToolCall, Name: tool, Args: v}}
		}
		b, _ := json.Marshal(input)
		return []StreamEvent{{Type: EventToolCall, Name: tool, Args: string(b)}}
	case "error":
		if msg := extractErrorMessage(obj); msg != "" {
			return []StreamEvent{{Type: EventResult, Text: msg}}
		}
	}
	return nil
}
