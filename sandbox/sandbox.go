// Package sandbox defines the pluggable execution environment for an agent.
package sandbox

import (
	"context"
	"fmt"
)

// ExecResult is the outcome of a command run inside a sandbox.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// ExecOptions configures a single Exec call.
type ExecOptions struct {
	// OnLine receives each stdout line while the process runs. Implementations
	// must stream; a buffered implementation breaks idle timeouts.
	OnLine func(line string)
	// Cwd defaults to the handle's worktree path.
	Cwd string
	// Sudo runs the command as root where the provider supports it.
	Sudo bool
	// Stdin, when non-nil, is piped to the process and closed.
	Stdin *string
}

// Handle is a running sandbox.
type Handle interface {
	WorktreePath() string
	Exec(ctx context.Context, command string, opts ExecOptions) (ExecResult, error)
	Close(ctx context.Context) error
}

// Kind tells the orchestrator how a provider relates to the host filesystem.
type Kind string

const (
	KindNone      Kind = "none"
	KindBindMount Kind = "bind-mount"
	KindIsolated  Kind = "isolated"
)

// Provider is the common part of every sandbox provider.
type Provider interface {
	Kind() Kind
	Name() string
	Env() map[string]string
}

// NoSandboxCreateOptions are passed to a NoSandboxProvider.
type NoSandboxCreateOptions struct {
	WorktreePath string
	Env          map[string]string
}

// NoSandboxProvider runs the agent directly on the host.
type NoSandboxProvider interface {
	Provider
	Create(ctx context.Context, opts NoSandboxCreateOptions) (Handle, error)
}

// BranchType selects how the agent's commits relate to branches.
type BranchType string

const (
	BranchHead        BranchType = "head"
	BranchMergeToHead BranchType = "merge-to-head"
	BranchNamed       BranchType = "branch"
)

// BranchStrategy configures branching for a run. Branch and BaseBranch are
// used by BranchNamed only.
type BranchStrategy struct {
	Type       BranchType
	Branch     string
	BaseBranch string
}

// StartError reports a process that could not be started.
type StartError struct {
	Command string
	Cause   error
}

func (e *StartError) Error() string { return fmt.Sprintf("exec failed: %s: %v", e.Command, e.Cause) }
func (e *StartError) Unwrap() error { return e.Cause }
