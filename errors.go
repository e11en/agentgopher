// Package agentgopher orchestrates AI coding agents in sandboxes.
package agentgopher

import (
	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/internal/orchestrator"
	"github.com/e11en/agentgopher/internal/prompt"
)

// ExecError reports a command that exited non-zero in a sandbox or on the host, or could not be started (ExitCode -1).
type ExecError = lifecycle.ExecError

// PromptError reports a prompt that could not be resolved, substituted or expanded.
type PromptError = prompt.Error

// AgentError reports an agent process that exited non-zero.
type AgentError = orchestrator.AgentError

// AgentIdleTimeoutError reports an agent that produced no output for Timeout.
type AgentIdleTimeoutError = orchestrator.AgentIdleTimeoutError

// PromptExpansionTimeoutError reports a shell expression that ran longer than its timeout.
type PromptExpansionTimeoutError = prompt.ExpansionTimeoutError

// HookTimeoutError reports a lifecycle hook that ran longer than Timeout.
type HookTimeoutError = lifecycle.HookTimeoutError

// GitSetupTimeoutError reports an in-sandbox git setup command that ran longer than Timeout.
type GitSetupTimeoutError = lifecycle.GitSetupTimeoutError

// CommitCollectionTimeoutError reports that listing the run's commits ran longer than Timeout.
type CommitCollectionTimeoutError = lifecycle.CommitCollectionTimeoutError

// CwdError reports a Cwd option that is missing or not a directory.
type CwdError struct {
	Cwd     string
	Message string
}

func (e *CwdError) Error() string { return e.Message }

// MergeToHostTimeoutError reports a merge-to-head that ran longer than its timeout.
type MergeToHostTimeoutError = lifecycle.MergeToHostTimeoutError

// MergeError reports a failed merge-to-head; the temp branch is preserved.
type MergeError = lifecycle.MergeError
