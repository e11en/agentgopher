// Package sandbox defines the pluggable execution environment for an agent.
package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"
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

// Mount is a resolved bind mount.
type Mount struct {
	HostPath    string
	SandboxPath string
	ReadOnly    bool
}

// MountConfig is a user-facing bind mount. HostPath may use ~ and relative
// paths (resolved from the working directory); SandboxPath may use ~ (the
// sandbox home) and relative paths (resolved from the workspace).
type MountConfig struct {
	HostPath    string
	SandboxPath string
	ReadOnly    bool
}

// BindMountCreateOptions are passed to a BindMountProvider.
type BindMountCreateOptions struct {
	WorktreePath string
	HostRepoPath string
	Mounts       []Mount
	Env          map[string]string
}

// BindMountHandle is a running container that shares the host filesystem.
type BindMountHandle interface {
	Handle
	CopyFileIn(ctx context.Context, hostPath, sandboxPath string) error
	CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error
}

// BindMountProvider creates containers that bind-mount the worktree.
type BindMountProvider interface {
	Provider
	// SandboxHomedir is the home directory inside the sandbox, or "" when unknown.
	SandboxHomedir() string
	Create(ctx context.Context, opts BindMountCreateOptions) (BindMountHandle, error)
}

// BindMountProviderConfig builds a BindMountProvider from a Create function.
type BindMountProviderConfig struct {
	Name           string
	Env            map[string]string
	SandboxHomedir string
	Create         func(ctx context.Context, opts BindMountCreateOptions) (BindMountHandle, error)
}

type bindMountProvider struct{ cfg BindMountProviderConfig }

// NewBindMountProvider wraps cfg as a provider usable by Run.
func NewBindMountProvider(cfg BindMountProviderConfig) BindMountProvider {
	return &bindMountProvider{cfg: cfg}
}

func (p *bindMountProvider) Kind() Kind             { return KindBindMount }
func (p *bindMountProvider) Name() string           { return p.cfg.Name }
func (p *bindMountProvider) Env() map[string]string { return p.cfg.Env }
func (p *bindMountProvider) SandboxHomedir() string { return p.cfg.SandboxHomedir }
func (p *bindMountProvider) Create(ctx context.Context, opts BindMountCreateOptions) (BindMountHandle, error) {
	return p.cfg.Create(ctx, opts)
}

// RepoDir is where isolated providers put the repo unless they report
// another working directory.
const RepoDir = "/home/agent/workspace"

// IsolatedCreateOptions are passed to an IsolatedProvider.
type IsolatedCreateOptions struct {
	Env map[string]string
}

// IsolatedHandle is a running sandbox with its own filesystem. Code moves in
// and out through CopyIn and CopyFileOut.
type IsolatedHandle interface {
	Handle
	// CopyIn copies a file or directory from the host into the sandbox.
	CopyIn(ctx context.Context, hostPath, sandboxPath string) error
	// CopyFileOut copies one file from the sandbox to the host, creating
	// parent directories.
	CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error
}

// IsolatedProvider creates sandboxes that do not share the host filesystem.
type IsolatedProvider interface {
	Provider
	Create(ctx context.Context, opts IsolatedCreateOptions) (IsolatedHandle, error)
}

// IsolatedProviderConfig builds a custom IsolatedProvider.
type IsolatedProviderConfig struct {
	Name   string
	Env    map[string]string
	Create func(ctx context.Context, opts IsolatedCreateOptions) (IsolatedHandle, error)
}

type isolatedProvider struct{ cfg IsolatedProviderConfig }

// NewIsolatedProvider wraps cfg into a provider accepted by Run.
func NewIsolatedProvider(cfg IsolatedProviderConfig) IsolatedProvider {
	return &isolatedProvider{cfg: cfg}
}

func (p *isolatedProvider) Kind() Kind             { return KindIsolated }
func (p *isolatedProvider) Name() string           { return p.cfg.Name }
func (p *isolatedProvider) Env() map[string]string { return p.cfg.Env }
func (p *isolatedProvider) Create(ctx context.Context, opts IsolatedCreateOptions) (IsolatedHandle, error) {
	return p.cfg.Create(ctx, opts)
}

// InteractiveExecOptions wire the caller's streams to an interactive process.
type InteractiveExecOptions struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Cwd defaults to the handle's worktree path.
	Cwd string
}

// Interactive is implemented by handles that can attach a terminal to a
// process inside the sandbox.
type Interactive interface {
	InteractiveExec(ctx context.Context, args []string, opts InteractiveExecOptions) (int, error)
}

// StdinIsTerminal reports whether r is a terminal, which decides whether a
// container exec allocates a pseudo-terminal.
func StdinIsTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
