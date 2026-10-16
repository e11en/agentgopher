// Package agent defines the pluggable AI coding agent invoked inside a sandbox.
package agent

import "strings"

// CommandOptions are passed to BuildPrintCommand.
type CommandOptions struct {
	Prompt                     string
	DangerouslySkipPermissions bool
	// ResumeSession, when set, resumes that session instead of starting fresh.
	ResumeSession string
	// ForkSession, with ResumeSession, writes the continuation as a new session.
	ForkSession bool
}

// PrintCommand is the shell command for one non-interactive agent run. When
// Stdin is set the sandbox pipes it to the process, which avoids the Linux
// per-argument size limit for large prompts.
type PrintCommand struct {
	Command string
	Stdin   *string
}

// Usage is a token usage snapshot for one iteration.
type Usage struct {
	InputTokens              int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	OutputTokens             int
}

// EventType classifies a StreamEvent.
type EventType string

const (
	EventText      EventType = "text"
	EventResult    EventType = "result"
	EventToolCall  EventType = "tool_call"
	EventSessionID EventType = "session_id"
	EventUsage     EventType = "usage"
)

// StreamEvent is one parsed item from the agent's stdout. Text is set for
// EventText and EventResult, Name and Args for EventToolCall, SessionID for
// EventSessionID, Usage for EventUsage.
type StreamEvent struct {
	Type      EventType
	Text      string
	Name      string
	Args      string
	SessionID string
	Usage     *Usage
}

// Provider builds the command for an agent CLI and parses its output.
type Provider interface {
	Name() string
	// Env is injected into the sandbox. Merged at launch time.
	Env() map[string]string
	// CaptureSessions enables copying the agent session back to the host.
	CaptureSessions() bool
	BuildPrintCommand(opts CommandOptions) PrintCommand
	ParseStreamLine(line string) []StreamEvent
}

// SessionUsageParser is implemented by providers that can read token usage
// from a captured session file.
type SessionUsageParser interface {
	ParseSessionUsage(content string) (Usage, bool)
}

// ShellEscape single-quotes s for a POSIX shell.
func ShellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
