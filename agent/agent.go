// Package agent defines the pluggable AI coding agent invoked inside a sandbox.
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/session"
)

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
	// Err is set when the command could not be built (for example a prompt
	// too large for argv); the orchestrator fails the iteration.
	Err error
}

// ArgvPromptMaxBytes bounds prompts passed as a command-line argument.
// Linux limits a single argument to about 128 KiB.
const ArgvPromptMaxBytes = 120 * 1024

// PromptTooLargeError reports a prompt that does not fit in argv.
type PromptTooLargeError struct {
	Provider string
	Bytes    int
}

func (e *PromptTooLargeError) Error() string {
	return fmt.Sprintf("%s print-mode prompt is %d bytes (max %d bytes). This provider passes the prompt as a command-line argument; shorten the prompt or split the work. Other providers use stdin for large prompts.", e.Provider, e.Bytes, ArgvPromptMaxBytes)
}

func argvPromptError(provider, prompt string) error {
	if len(prompt) > ArgvPromptMaxBytes {
		return &PromptTooLargeError{Provider: provider, Bytes: len(prompt)}
	}
	return nil
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

// HostSessionLookup is re-exported for providers implementing SessionStorage.
type HostSessionLookup = session.HostSessionLookup

// SessionTransfer names the two sides of a session move.
type SessionTransfer struct {
	HostCwd    string
	SandboxCwd string
	SessionID  string
	Handle     sandbox.BindMountHandle
}

// SessionStorage is implemented by providers whose sessions can be captured
// and resumed. Storage layout and transfer rules are provider-owned (ADR 0012).
type SessionStorage interface {
	CaptureToHost(ctx context.Context, t SessionTransfer) error
	ResumeIntoSandbox(ctx context.Context, t SessionTransfer) error
	// ReadHostSession returns the captured content, ok=false when absent.
	ReadHostSession(cwd, sessionID string) (string, bool, error)
	ExistsOnHost(cwd, sessionID string) (bool, error)
	// HostSessionFilePath is the host path a session would have, or "" when
	// the layout does not allow deriving it from (cwd, id).
	HostSessionFilePath(cwd, sessionID string) string
	FindByIDOnHost(sessionID string) (HostSessionLookup, error)
}

// SessionStorageProvider is implemented by resumable providers.
type SessionStorageProvider interface {
	SessionStorage() SessionStorage
}

// InteractiveArgsBuilder is implemented by providers that support Interactive.
type InteractiveArgsBuilder interface {
	BuildInteractiveArgs(opts CommandOptions) []string
}

// SessionDirs overrides a provider's session directories.
type SessionDirs struct {
	Host    string
	Sandbox string
}
