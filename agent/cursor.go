package agent

import "encoding/json"

// CursorOptions configures the Cursor provider.
type CursorOptions struct{ Env map[string]string }

type cursor struct {
	model string
	opts  CursorOptions
}

// Cursor returns a provider for the Cursor `agent` CLI. Not resumable (ADR 0016).
func Cursor(model string, opts *CursorOptions) Provider {
	p := &cursor{model: model}
	if opts != nil {
		p.opts = *opts
	}
	return p
}

func (p *cursor) Name() string           { return "cursor" }
func (p *cursor) Env() map[string]string { return p.opts.Env }
func (p *cursor) CaptureSessions() bool  { return false }

func (p *cursor) BuildPrintCommand(o CommandOptions) PrintCommand {
	if err := argvPromptError("cursor", o.Prompt); err != nil {
		return PrintCommand{Err: err}
	}
	force := ""
	if o.DangerouslySkipPermissions {
		force = " --force"
	}
	return PrintCommand{Command: "agent --print --output-format stream-json --model " + ShellEscape(p.model) + force + " " + ShellEscape(o.Prompt)}
}

func (p *cursor) BuildInteractiveArgs(o CommandOptions) []string {
	args := []string{"agent", "--model", p.model}
	if o.DangerouslySkipPermissions {
		args = append(args, "--force")
	}
	if o.Prompt != "" {
		args = append(args, o.Prompt)
	}
	return args
}

// ParseStreamLine handles Cursor's top-level tool_call events and falls back
// to the Claude-compatible stream-json parser for everything else.
func (p *cursor) ParseStreamLine(line string) []StreamEvent {
	obj, ok := parseJSONObject(line)
	if !ok {
		return nil
	}
	if obj["type"] != "tool_call" {
		return ParseStreamJSONLine(line)
	}
	if obj["subtype"] != "started" {
		return nil
	}
	tc, _ := obj["tool_call"].(map[string]any)
	// Read before Write, as upstream checks them in that order.
	for _, pair := range [][2]string{{"Read", "readToolCall"}, {"Write", "writeToolCall"}} {
		if call, ok := tc[pair[1]].(map[string]any); ok {
			if args, ok := call["args"].(map[string]any); ok {
				if path, ok := args["path"].(string); ok {
					return []StreamEvent{{Type: EventToolCall, Name: pair[0], Args: path}}
				}
			}
		}
	}
	fn, _ := tc["function"].(map[string]any)
	name, ok := fn["name"].(string)
	if !ok {
		return nil
	}
	raw, _ := fn["arguments"].(string)
	if raw == "" {
		return []StreamEvent{{Type: EventToolCall, Name: name, Args: ""}}
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(raw), &parsed) == nil {
		if cmd, ok := parsed["command"].(string); ok {
			return []StreamEvent{{Type: EventToolCall, Name: "Bash", Args: cmd}}
		}
	}
	return []StreamEvent{{Type: EventToolCall, Name: name, Args: raw}}
}
