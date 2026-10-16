package agent

// CopilotOptions configures the GitHub Copilot CLI provider.
type CopilotOptions struct {
	Effort string // low, medium, high
	Env    map[string]string
}

type copilot struct {
	model string
	opts  CopilotOptions
}

// Copilot returns a provider for the `copilot` CLI. Not resumable: its
// session state is indexed by SQLite (ADR 0016).
func Copilot(model string, opts *CopilotOptions) Provider {
	p := &copilot{model: model}
	if opts != nil {
		p.opts = *opts
	}
	return p
}

func (p *copilot) Name() string           { return "copilot" }
func (p *copilot) Env() map[string]string { return p.opts.Env }
func (p *copilot) CaptureSessions() bool  { return false }

func (p *copilot) BuildPrintCommand(o CommandOptions) PrintCommand {
	if err := argvPromptError("copilot", o.Prompt); err != nil {
		return PrintCommand{Err: err}
	}
	cmd := "copilot -p " + ShellEscape(o.Prompt) + " --output-format json --model " + ShellEscape(p.model)
	if o.DangerouslySkipPermissions {
		cmd += " --allow-all-tools"
	}
	if p.opts.Effort != "" {
		cmd += " --effort " + p.opts.Effort
	}
	return PrintCommand{Command: cmd}
}

func (p *copilot) BuildInteractiveArgs(o CommandOptions) []string {
	args := []string{"copilot", "--model", p.model}
	// -i starts an interactive session seeded with the prompt; -p would print and exit.
	if o.Prompt != "" {
		args = append(args, "-i", o.Prompt)
	}
	return args
}

func (p *copilot) ParseStreamLine(line string) []StreamEvent {
	obj, ok := parseJSONObject(line)
	if !ok {
		return nil
	}
	data, _ := obj["data"].(map[string]any)
	switch obj["type"] {
	case "assistant.message_delta":
		if d, ok := data["deltaContent"].(string); ok {
			return []StreamEvent{{Type: EventText, Text: d}}
		}
	case "tool.execution_start":
		name, _ := data["toolName"].(string)
		if name == "bash" {
			name = "Bash"
		}
		field, ok := toolArgFields[name]
		if !ok {
			return nil
		}
		if args, ok := data["arguments"].(map[string]any); ok {
			if v, ok := args[field].(string); ok {
				return []StreamEvent{{Type: EventToolCall, Name: name, Args: v}}
			}
		}
	case "assistant.message":
		if c, ok := data["content"].(string); ok && c != "" {
			return []StreamEvent{{Type: EventResult, Text: c}}
		}
	case "result":
		if id, ok := obj["sessionId"].(string); ok {
			return []StreamEvent{{Type: EventSessionID, SessionID: id}}
		}
	case "error", "agent_error":
		if msg := extractErrorMessage(obj); msg != "" {
			return []StreamEvent{{Type: EventResult, Text: msg}}
		}
	}
	return nil
}
