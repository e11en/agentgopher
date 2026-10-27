package agentsandbox

import (
	"context"
	"io"

	sandboxsdk "sigs.k8s.io/agent-sandbox/clients/go/sandbox"
)

// API is the slice of the SDK sandbox the provider uses. Tests fake it.
type API interface {
	Run(ctx context.Context, command string) (*sandboxsdk.ExecutionResult, error)
	Start(ctx context.Context, command string) (Process, error)
	WriteReader(ctx context.Context, path string, content io.Reader) error
	ReadTo(ctx context.Context, path string, destination io.Writer) (int64, error)
	Close(ctx context.Context) error
}

// Process is a started command whose events are read until io.EOF.
type Process interface {
	Recv() (*sandboxsdk.ProcessEvent, error)
	Close() error
}

type sdkAPI struct{ sb *sandboxsdk.Sandbox }

func (a sdkAPI) Run(ctx context.Context, command string) (*sandboxsdk.ExecutionResult, error) {
	return a.sb.Run(ctx, command)
}

func (a sdkAPI) Start(ctx context.Context, command string) (Process, error) {
	return a.sb.Commands().Start(ctx, command)
}

func (a sdkAPI) WriteReader(ctx context.Context, path string, content io.Reader) error {
	return a.sb.WriteReader(ctx, path, content)
}

func (a sdkAPI) ReadTo(ctx context.Context, path string, destination io.Writer) (int64, error) {
	return a.sb.ReadTo(ctx, path, destination)
}

func (a sdkAPI) Close(ctx context.Context) error { return a.sb.Close(ctx) }
