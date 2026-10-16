package agent

import (
	"encoding/json"
	"strings"
)

// DefaultModel is used by examples and init templates.
const DefaultModel = "claude-opus-4-8"

// ClaudeCodeOptions configures the Claude Code provider.
type ClaudeCodeOptions struct {
	Env map[string]string
	// CaptureSessions defaults to true.
	CaptureSessions *bool
	// PermissionMode replaces --dangerously-skip-permissions when set.
	PermissionMode string
	Effort         string
	// SessionStorage overrides the session directories on host and in the sandbox.
	SessionStorage *SessionDirs
}

// toolArgFields maps allowlisted tool names to the input field shown as the
// tool call argument.
var toolArgFields = map[string]string{
	"Bash":      "command",
	"WebSearch": "query",
	"WebFetch":  "url",
	"Agent":     "description",
}

type claudeCode struct {
	model   string
	opts    ClaudeCodeOptions
	storage *claudeStorage
}

// ClaudeCode returns a provider for the `claude` CLI.
func ClaudeCode(model string, opts *ClaudeCodeOptions) Provider {
	p := &claudeCode{model: model}
	if opts != nil {
		p.opts = *opts
	}
	p.storage = newClaudeStorage(p.opts.SessionStorage)
	return p
}

func (p *claudeCode) Name() string           { return "claude-code" }
func (p *claudeCode) Env() map[string]string { return p.opts.Env }
func (p *claudeCode) CaptureSessions() bool {
	return p.opts.CaptureSessions == nil || *p.opts.CaptureSessions
}

func (p *claudeCode) BuildPrintCommand(o CommandOptions) PrintCommand {
	var b strings.Builder
	b.WriteString("claude --print --verbose")
	switch {
	case p.opts.PermissionMode != "":
		b.WriteString(" --permission-mode " + p.opts.PermissionMode)
	case o.DangerouslySkipPermissions:
		b.WriteString(" --dangerously-skip-permissions")
	}
	b.WriteString(" --output-format stream-json --model " + ShellEscape(p.model))
	if p.opts.Effort != "" {
		b.WriteString(" --effort " + p.opts.Effort)
	}
	if o.ResumeSession != "" {
		b.WriteString(" --resume " + ShellEscape(o.ResumeSession))
		if o.ForkSession {
			b.WriteString(" --fork-session")
		}
	}
	b.WriteString(" -p -")
	prompt := o.Prompt
	return PrintCommand{Command: b.String(), Stdin: &prompt}
}

func (p *claudeCode) ParseStreamLine(line string) []StreamEvent {
	return ParseStreamJSONLine(line)
}

func (p *claudeCode) ParseSessionUsage(content string) (Usage, bool) {
	lines := strings.Split(content, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.HasPrefix(lines[i], "{") {
			continue
		}
		var obj struct {
			Type    string `json:"type"`
			Message struct {
				Usage *struct {
					Input         *int `json:"input_tokens"`
					CacheCreation *int `json:"cache_creation_input_tokens"`
					CacheRead     *int `json:"cache_read_input_tokens"`
					Output        *int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(lines[i]), &obj) != nil || obj.Type != "assistant" || obj.Message.Usage == nil {
			continue
		}
		u := obj.Message.Usage
		if u.Input == nil || u.CacheCreation == nil || u.CacheRead == nil || u.Output == nil {
			continue
		}
		return Usage{*u.Input, *u.CacheCreation, *u.CacheRead, *u.Output}, true
	}
	return Usage{}, false
}

// ParseStreamJSONLine parses one line of Claude Code's stream-json output.
// Shared with the Cursor provider in a later phase.
func ParseStreamJSONLine(line string) []StreamEvent {
	if !strings.HasPrefix(line, "{") {
		return nil
	}
	var obj struct {
		Type      string          `json:"type"`
		Subtype   string          `json:"subtype"`
		Result    *string         `json:"result"`
		SessionID *string         `json:"session_id"`
		Message   json.RawMessage `json:"message"`
	}
	if json.Unmarshal([]byte(line), &obj) != nil {
		return nil
	}
	switch {
	case obj.Type == "assistant":
		var msg struct {
			Content []struct {
				Type  string                     `json:"type"`
				Text  *string                    `json:"text"`
				Name  *string                    `json:"name"`
				Input map[string]json.RawMessage `json:"input"`
			} `json:"content"`
		}
		if json.Unmarshal(obj.Message, &msg) != nil || msg.Content == nil {
			return nil
		}
		var events []StreamEvent
		var texts strings.Builder
		flush := func() {
			if texts.Len() > 0 {
				events = append(events, StreamEvent{Type: EventText, Text: texts.String()})
				texts.Reset()
			}
		}
		for _, block := range msg.Content {
			switch {
			case block.Type == "text" && block.Text != nil:
				texts.WriteString(*block.Text)
			case block.Type == "tool_use" && block.Name != nil && block.Input != nil:
				field, ok := toolArgFields[*block.Name]
				if !ok {
					continue
				}
				var arg string
				if json.Unmarshal(block.Input[field], &arg) != nil {
					continue
				}
				flush()
				events = append(events, StreamEvent{Type: EventToolCall, Name: *block.Name, Args: arg})
			}
		}
		flush()
		return events
	case obj.Type == "result" && obj.Result != nil:
		return []StreamEvent{{Type: EventResult, Text: *obj.Result}}
	case obj.Type == "system" && obj.Subtype == "init" && obj.SessionID != nil:
		return []StreamEvent{{Type: EventSessionID, SessionID: *obj.SessionID}}
	}
	return nil
}

func (p *claudeCode) SessionStorage() SessionStorage { return p.storage }

func (p *claudeCode) BuildInteractiveArgs(o CommandOptions) []string {
	args := []string{"claude"}
	switch {
	case p.opts.PermissionMode != "":
		args = append(args, "--permission-mode", p.opts.PermissionMode)
	case o.DangerouslySkipPermissions:
		args = append(args, "--dangerously-skip-permissions")
	}
	args = append(args, "--model", p.model)
	if p.opts.Effort != "" {
		args = append(args, "--effort", p.opts.Effort)
	}
	if o.Prompt != "" {
		args = append(args, o.Prompt)
	}
	return args
}
