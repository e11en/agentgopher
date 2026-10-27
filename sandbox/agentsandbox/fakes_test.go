package agentsandbox

import (
	"context"
	"io"
	"strings"
	"sync"

	sandboxsdk "sigs.k8s.io/agent-sandbox/clients/go/sandbox"
)

// scripted is one fake command outcome. Stdout is split at "|" into chunks
// so tests can check line reassembly.
type scripted struct {
	stdout, stderr string
	exitCode       int
	noExit         bool
}

type fakeAPI struct {
	mu       sync.Mutex
	script   map[string]scripted // keyed on a substring of the command
	commands []string
	files    map[string][]byte // written files by SDK path
	reads    map[string][]byte // files served to ReadTo
	closed   bool
	closeErr error // ctx.Err() at the time of Close
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{script: map[string]scripted{}, files: map[string][]byte{}, reads: map[string][]byte{}}
}

func (f *fakeAPI) lookup(command string) scripted {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, command)
	for key, sc := range f.script {
		if strings.Contains(command, key) {
			return sc
		}
	}
	return scripted{}
}

func (f *fakeAPI) Run(_ context.Context, command string) (*sandboxsdk.ExecutionResult, error) {
	sc := f.lookup(command)
	return &sandboxsdk.ExecutionResult{Stdout: strings.ReplaceAll(sc.stdout, "|", ""), Stderr: sc.stderr, ExitCode: sc.exitCode}, nil
}

func (f *fakeAPI) Start(ctx context.Context, command string) (Process, error) {
	sc := f.lookup(command)
	var events []*sandboxsdk.ProcessEvent
	for _, chunk := range strings.Split(sc.stdout, "|") {
		if chunk != "" {
			events = append(events, &sandboxsdk.ProcessEvent{Type: sandboxsdk.ProcessEventStdout, Data: []byte(chunk)})
		}
	}
	if sc.stderr != "" {
		events = append(events, &sandboxsdk.ProcessEvent{Type: sandboxsdk.ProcessEventStderr, Data: []byte(sc.stderr)})
	}
	if !sc.noExit {
		events = append(events, &sandboxsdk.ProcessEvent{Type: sandboxsdk.ProcessEventExit, ExitCode: sc.exitCode})
	}
	return &fakeProcess{ctx: ctx, events: events}, nil
}

type fakeProcess struct {
	ctx    context.Context
	events []*sandboxsdk.ProcessEvent
}

func (p *fakeProcess) Recv() (*sandboxsdk.ProcessEvent, error) {
	if err := p.ctx.Err(); err != nil {
		return nil, err
	}
	if len(p.events) == 0 {
		return nil, io.EOF
	}
	ev := p.events[0]
	p.events = p.events[1:]
	return ev, nil
}

func (p *fakeProcess) Close() error { return nil }

func (f *fakeAPI) WriteReader(_ context.Context, path string, content io.Reader) error {
	b, err := io.ReadAll(content)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = b
	return nil
}

func (f *fakeAPI) ReadTo(_ context.Context, path string, destination io.Writer) (int64, error) {
	f.mu.Lock()
	b, ok := f.reads[path]
	f.mu.Unlock()
	if !ok {
		return 0, &sandboxsdk.HTTPError{StatusCode: 404, Operation: "read"}
	}
	n, err := destination.Write(b)
	return int64(n), err
}

func (f *fakeAPI) Close(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	f.closeErr = ctx.Err()
	return nil
}

func (f *fakeAPI) lastCommand() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.commands[len(f.commands)-1]
}

func newTestHandle(f *fakeAPI) *handle {
	return &handle{api: f, o: withDefaults(Options{Open: func(context.Context, map[string]string) (API, error) { return f, nil }})}
}
