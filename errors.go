// Package agentgopher orchestrates AI coding agents in sandboxes.
package agentgopher

import (
	"fmt"
	"time"

	"github.com/e11en/agentgopher/internal/prompt"
)

// ExecError reports a command that ran in a sandbox and exited non-zero,
// or could not be started (ExitCode -1).
type ExecError struct {
	Command  string
	ExitCode int
	Stderr   string
}

func (e *ExecError) Error() string {
	if e.ExitCode < 0 {
		return fmt.Sprintf("command could not be started: %s\n%s", e.Command, e.Stderr)
	}
	return fmt.Sprintf("command failed (exit %d): %s\n%s", e.ExitCode, e.Command, e.Stderr)
}

// PromptError reports a prompt that could not be resolved, substituted or expanded.
type PromptError = prompt.Error

// AgentError reports an agent process that exited non-zero.
type AgentError struct{ Message string }

func (e *AgentError) Error() string { return e.Message }

// AgentIdleTimeoutError reports an agent that produced no output for Timeout.
type AgentIdleTimeoutError struct{ Timeout time.Duration }

func (e *AgentIdleTimeoutError) Error() string {
	return fmt.Sprintf("agent idle for %v - no output received", e.Timeout)
}

// PromptExpansionTimeoutError reports a shell expression that ran longer than Timeout.
type PromptExpansionTimeoutError struct {
	Expression string
	Timeout    time.Duration
	Elapsed    time.Duration
}

func (e *PromptExpansionTimeoutError) Error() string {
	return fmt.Sprintf("shell expression `%s` timed out after %v", e.Expression, e.Elapsed)
}

// HookTimeoutError reports a lifecycle hook that ran longer than Timeout.
type HookTimeoutError struct {
	Command string
	Timeout time.Duration
}

func (e *HookTimeoutError) Error() string {
	return fmt.Sprintf("hook '%s' timed out after %v", e.Command, e.Timeout)
}

// GitSetupTimeoutError reports an in-sandbox git setup command that ran longer than Timeout.
type GitSetupTimeoutError struct {
	Command string
	Timeout time.Duration
}

func (e *GitSetupTimeoutError) Error() string {
	return fmt.Sprintf("git command timed out after %v: %s", e.Timeout, e.Command)
}

// CommitCollectionTimeoutError reports that listing the run's commits ran longer than Timeout.
type CommitCollectionTimeoutError struct{ Timeout time.Duration }

func (e *CommitCollectionTimeoutError) Error() string {
	return fmt.Sprintf("commit collection timed out after %v", e.Timeout)
}

// CwdError reports a Cwd option that is missing or not a directory.
type CwdError struct {
	Cwd     string
	Message string
}

func (e *CwdError) Error() string { return e.Message }
