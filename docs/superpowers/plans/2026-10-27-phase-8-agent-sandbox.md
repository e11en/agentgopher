# agentgopher Phase 8: GKE Agent Sandbox Provider and gVisor by Default Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Agent sandboxes on GKE run under gVisor by default, and a new `sandbox/agentsandbox` provider claims sandboxes from a GKE Agent Sandbox warm pool through the official Go SDK, so an in-cluster `remote run` gets kernel-isolated, sub-second sandboxes without any change to the scaffolded templates.

**Architecture:** Two independent parts. Part A adds `RuntimeClass` to `sandbox/kubernetes` with `gvisor` as the default and an explicit opt-out. Part B adds `sandbox/agentsandbox`, an isolated provider built on `sigs.k8s.io/agent-sandbox/clients/go/sandbox` (runtime `sandboxd`, in-cluster Pod IP connectivity inside a Job). Commands run through the SDK's gRPC `ProcessService` (streaming for `OnLine`), files move through the REST filesystem with `--root-dir=/` so every absolute sandbox path maps to a relative SDK path. The sandbox Pod is our own runner image with the `sandboxd` binary injected by an initContainer (upstream "topology B"), declared in a `SandboxTemplate` plus `SandboxWarmPool` in `deploy/k8s`, and the cluster gets the managed Agent Sandbox add-on through Terraform. The scaffolded templates pick `agentsandbox.New(nil)` inside a cluster.

**Tech Stack:** Go 1.26, `sigs.k8s.io/agent-sandbox` v1.0.8 (Go SDK, extensions API types; pulls `k8s.io/*` v0.37.1), existing `k8s.io/client-go`, Terraform `hashicorp/google` provider with `addons_config.agent_sandbox_config`, GKE Agent Sandbox (GA since 2026-05-20), gVisor (`runtimeClassName: gvisor`, enabled on Autopilot by default).

**Spec:** `docs/plan.md`, sections "Kubernetes and GKE" and "Decisions" (this plan adds the phase 8 line and two decisions in Task 7). Upstream reference for the SDK: `kubernetes-sigs/agent-sandbox` at commit `cf913b0` (clients/go/sandbox, packages/sandboxd/USER_GUIDE.md, examples/sandboxd-sandbox/deploy/b-inject-binary.yaml).

## Global Constraints

- Module path `github.com/e11en/agentgopher`, `go 1.26.0`.
- New third-party dependency: `sigs.k8s.io/agent-sandbox` only. It bumps `k8s.io/api`, `k8s.io/apimachinery`, `k8s.io/client-go` and `k8s.io/streaming` to one shared version; keep them aligned with `go mod tidy`.
- Public API added: `sandbox/agentsandbox`, `kubernetes.Options.RuntimeClass`, `kubernetes.NoRuntimeClass`. Shared tar code moves to `internal/tarx`.
- Code comments, test names, commit messages, manifests and Terraform in English. No em or en dashes anywhere.
- Commit messages: imperative, no attribution footer, no `Co-Authored-By`.
- Every `Exec` streams lines live through `OnLine` while the process runs.
- Go tests need no cluster, Docker or network: the provider takes an `Open` seam that returns a fake `API`.
- Terraform is validated with `terraform fmt -check` and `terraform validate` only. Never `terraform apply`.
- Cross-plan contract (phase 4): `sandbox.IsolatedHandle` is `sandbox.Handle` plus `CopyIn(ctx, hostPath, sandboxPath string) error` and `CopyFileOut(ctx, sandboxPath, hostPath string) error`; `sandbox.RepoDir` is `/home/agent/workspace`; `sync.In` copies the bundle to a `mktemp -d` directory under `/tmp`, `sync.Out` reads patches from one.
- Cross-plan contract (phase 7): the runner Job pod carries labels `app.kubernetes.io/name: agentgopher` and `app.kubernetes.io/component: runner`; the Job env has `AGENTGOPHER_IMAGE`; `deploy/k8s` placeholders are replaced with `sed` as documented in `docs/kubernetes.md`.

## Review Focus

1. A sandbox path outside the repo dir (`/tmp/agentgopher-XXXX/repo.bundle` from `sync.In`): `CopyIn` and `CopyFileOut` still work because the SDK path is the absolute path without its leading slash. Test added in Task 3.
2. Streamed stdout arrives in chunks that split a line: `OnLine` receives whole lines, the unterminated tail is delivered at exit, and the exit code comes from the exit event, not from an error. Test added in Task 3.
3. A process stream that ends without an exit event: `Exec` returns a `StartError`, never exit code 0. Test added in Task 3.
4. `Close` after the run context is cancelled still deletes the claim. Test added in Task 3.
5. `kubernetes.Options{RuntimeClass: kubernetes.NoRuntimeClass}` produces a Pod without `runtimeClassName`, and the zero value produces `gvisor`. Test added in Task 1.

---

## File structure

- Modify: `sandbox/kubernetes/options.go`, `sandbox/kubernetes/pod.go`, `sandbox/kubernetes/pod_test.go`, `sandbox/kubernetes/copy.go`, `sandbox/kubernetes/copy_test.go`
- Create: `internal/tarx/tarx.go`, `internal/tarx/tarx_test.go`
- Create: `sandbox/agentsandbox/options.go`, `sandbox/agentsandbox/api.go`, `sandbox/agentsandbox/agentsandbox.go`, `sandbox/agentsandbox/fakes_test.go`, `sandbox/agentsandbox/agentsandbox_test.go`
- Modify: `internal/initcmd/templates/*/main.go`, `internal/initcmd/templates_test.go`, `examples/isolated/main.go`
- Create: `deploy/k8s/sandboxtemplate.yaml`, `deploy/k8s/sandboxwarmpool.yaml`; Modify: `deploy/k8s/rbac.yaml`, `deploy/k8s/kustomization.yaml`
- Modify: `deploy/terraform/versions.tf`, `deploy/terraform/main.tf`, `deploy/terraform/README.md`
- Modify: `docs/kubernetes.md`, `docs/plan.md`, `README.md`

---

### Task 1: gVisor by default in `sandbox/kubernetes`

**Files:**
- Modify: `sandbox/kubernetes/options.go`, `sandbox/kubernetes/pod.go`, `sandbox/kubernetes/pod_test.go`

**Interfaces:**
- Produces: `Options.RuntimeClass string` (default `"gvisor"`), `const NoRuntimeClass = "none"`.

- [ ] **Step 1: Write the failing test** in `pod_test.go`:

```go
func TestPodSpecRuntimeClassDefaultsToGVisorAndCanBeDisabled(t *testing.T) {
	pod := podSpec(withDefaults(Options{Image: "img"}), "p", nil)
	if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != "gvisor" {
		t.Fatalf("runtimeClassName = %v, want gvisor", pod.Spec.RuntimeClassName)
	}
	pod = podSpec(withDefaults(Options{Image: "img", RuntimeClass: "kata"}), "p", nil)
	if *pod.Spec.RuntimeClassName != "kata" {
		t.Fatalf("runtimeClassName = %q", *pod.Spec.RuntimeClassName)
	}
	pod = podSpec(withDefaults(Options{Image: "img", RuntimeClass: NoRuntimeClass}), "p", nil)
	if pod.Spec.RuntimeClassName != nil {
		t.Fatalf("runtimeClassName = %q, want none", *pod.Spec.RuntimeClassName)
	}
}
```

- [ ] **Step 2: Implement.** In `options.go` add to `Options` after `NodeSelector`:

```go
	// RuntimeClass of the sandbox Pod. Default "gvisor" (GKE Sandbox), which
	// runs the agent on its own user-space kernel. Set NoRuntimeClass to run
	// on the node's default runtime.
	RuntimeClass string
```

and the constant after `WorkDir`:

```go
// NoRuntimeClass opts a provider out of the gVisor default.
const NoRuntimeClass = "none"
```

In `withDefaults`: `if o.RuntimeClass == "" { o.RuntimeClass = "gvisor" }`. In `podSpec`, after building the Pod: `if o.RuntimeClass != NoRuntimeClass { rc := o.RuntimeClass; pod.Spec.RuntimeClassName = &rc }`.

- [ ] **Step 3: Run** `go test -race ./sandbox/kubernetes/` and `golangci-lint run ./sandbox/kubernetes/`. Expected: PASS, no issues.
- [ ] **Step 4: Commit** `Run Kubernetes sandbox Pods under gVisor by default`.

### Task 2: Add the Agent Sandbox SDK dependency and move tar packing to `internal/tarx`

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/tarx/tarx.go`, `internal/tarx/tarx_test.go`
- Modify: `sandbox/kubernetes/copy.go`, `sandbox/kubernetes/copy_test.go`

- [ ] **Step 1:** `go get sigs.k8s.io/agent-sandbox@v1.0.8 && go mod tidy && go build ./... && go test -race ./...`. Expected: all green with `k8s.io/*` at the version the SDK requires.
- [ ] **Step 2:** Move `tarPath` from `sandbox/kubernetes/copy.go` to `internal/tarx/tarx.go` as `func Pack(w io.Writer, hostPath, name string) error` (same body, package `tarx`, doc comment "Pack writes hostPath (file or directory) to w as a tar stream whose entries are rooted at name."). Move `TestTarPathReproducesTreeWithSymlinksAndEmptyDirs` and `TestTarPathSingleFile` to `internal/tarx/tarx_test.go` (rename to `TestPack...`, call `Pack`), including the `keys` helper if it lives in `copy_test.go`. Replace the `tarPath(` call in `copy.go` with `tarx.Pack(` and drop the now unused imports.
- [ ] **Step 3:** `go test -race ./internal/tarx/ ./sandbox/kubernetes/ && golangci-lint run ./...`. Expected: PASS.
- [ ] **Step 4: Commit** `Add the Agent Sandbox SDK and share tar packing in internal/tarx`.

### Task 3: `sandbox/agentsandbox` provider

**Files:**
- Create: `sandbox/agentsandbox/options.go`, `api.go`, `agentsandbox.go`, `fakes_test.go`, `agentsandbox_test.go`

**Interfaces:**
- Produces: `agentsandbox.New(*Options) sandbox.IsolatedProvider`, `Options{Namespace, WarmPool, Kubeconfig, ReadyTimeout, ShutdownAfter, Env, MaxOutputTailChars, Open}`, `API`, `Process`, `sdkOptions(o Options, env map[string]string) sandboxsdk.Options`.
- Provider name `"agent-sandbox"`, kind isolated. `WorktreePath()` is `sandbox.RepoDir`.

- [ ] **Step 1: options.go**

```go
// Package agentsandbox runs each agent sandbox as a GKE Agent Sandbox
// (kubernetes-sigs/agent-sandbox): a gVisor-isolated Pod claimed from a warm
// pool and driven through the official Go SDK.
package agentsandbox

import (
	"context"
	"os"
	"sort"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	extv1beta1 "sigs.k8s.io/agent-sandbox/extensions/api/v1beta1"
	sandboxsdk "sigs.k8s.io/agent-sandbox/clients/go/sandbox"

	"github.com/e11en/agentgopher/internal/stream"
)

// Options configure the provider.
type Options struct {
	// Namespace of the SandboxClaim. Default: "agentgopher".
	Namespace string
	// WarmPool is the SandboxWarmPool to claim from. Default: the
	// AGENTGOPHER_WARM_POOL env var, then "agentgopher-sandboxes".
	WarmPool string
	// Kubeconfig path. Default: in-cluster config when KUBERNETES_SERVICE_HOST
	// is set, otherwise the standard kubeconfig loading rules.
	Kubeconfig string
	// ReadyTimeout bounds the wait for the claimed sandbox. Default: 5m.
	ReadyTimeout time.Duration
	// ShutdownAfter expires the claim as a safety net against leaks. Default: 2h.
	ShutdownAfter time.Duration
	// Env is injected into every sandbox. Merged at launch time.
	Env map[string]string
	// MaxOutputTailChars bounds the stdout and stderr kept in ExecResult.
	MaxOutputTailChars int
	// Open claims and connects a sandbox. Test seam; the default uses the SDK.
	Open func(ctx context.Context, env map[string]string) (API, error)
}

func withDefaults(o Options) Options {
	if o.Namespace == "" {
		o.Namespace = "agentgopher"
	}
	if o.WarmPool == "" {
		o.WarmPool = os.Getenv("AGENTGOPHER_WARM_POOL")
	}
	if o.WarmPool == "" {
		o.WarmPool = "agentgopher-sandboxes"
	}
	if o.ReadyTimeout == 0 {
		o.ReadyTimeout = 5 * time.Minute
	}
	if o.ShutdownAfter == 0 {
		o.ShutdownAfter = 2 * time.Hour
	}
	if o.MaxOutputTailChars == 0 {
		o.MaxOutputTailChars = stream.MaxTailChars
	}
	if o.Open == nil {
		o.Open = sdkOpen(o)
	}
	return o
}

// sdkOptions maps the provider options onto the SDK. Inside a cluster the
// Job talks to the sandbox Pod directly; elsewhere the SDK port-forwards.
func sdkOptions(o Options, env map[string]string) sandboxsdk.Options {
	so := sandboxsdk.Options{
		Runtime:             sandboxsdk.RuntimeSandboxd,
		Namespace:           o.Namespace,
		WarmPoolName:        o.WarmPool,
		SandboxReadyTimeout: o.ReadyTimeout,
		ShutdownAfter:       o.ShutdownAfter,
		Quiet:               true,
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		so.Connectivity = sandboxsdk.ConnectivityInClusterPodIP
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		so.Env = append(so.Env, extv1beta1.EnvVar{Name: k, Value: env[k]})
	}
	return so
}

func sdkOpen(o Options) func(ctx context.Context, env map[string]string) (API, error) {
	return func(ctx context.Context, env map[string]string) (API, error) {
		so := sdkOptions(o, env)
		if o.Kubeconfig != "" {
			cfg, err := clientcmd.BuildConfigFromFlags("", o.Kubeconfig)
			if err != nil {
				return nil, err
			}
			so.RestConfig = cfg
		}
		sb, err := sandboxsdk.New(ctx, so)
		if err != nil {
			return nil, err
		}
		if err := sb.Open(ctx); err != nil {
			return nil, err
		}
		return sdkAPI{sb: sb}, nil
	}
}

var _ = rest.Config{}
```

(Drop the `var _ = rest.Config{}` line and the `rest` import if `clientcmd` alone compiles; it is there only so the import list is complete when transcribing.)

- [ ] **Step 2: api.go**

```go
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
```

- [ ] **Step 3: agentsandbox.go**

```go
package agentsandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/internal/tarx"
	"github.com/e11en/agentgopher/sandbox"
)

type provider struct{ o Options }

// New returns the provider. nil opts use the defaults.
func New(opts *Options) sandbox.IsolatedProvider {
	var o Options
	if opts != nil {
		o = *opts
	}
	return &provider{o: withDefaults(o)}
}

func (p *provider) Kind() sandbox.Kind     { return sandbox.KindIsolated }
func (p *provider) Name() string           { return "agent-sandbox" }
func (p *provider) Env() map[string]string { return p.o.Env }

func (p *provider) Create(ctx context.Context, opts sandbox.IsolatedCreateOptions) (sandbox.IsolatedHandle, error) {
	api, err := p.o.Open(ctx, opts.Env)
	if err != nil {
		return nil, fmt.Errorf("agent-sandbox claim: %w", err)
	}
	h := &handle{api: api, o: p.o}
	res, err := api.Run(ctx, "mkdir -p "+shellQuote(sandbox.RepoDir))
	if err != nil || res.ExitCode != 0 {
		_ = h.Close(ctx)
		if err == nil {
			err = fmt.Errorf("exit %d: %s", res.ExitCode, res.Stderr)
		}
		return nil, fmt.Errorf("agent-sandbox create workspace: %w", err)
	}
	return h, nil
}

type handle struct {
	api API
	o   Options
}

func (h *handle) WorktreePath() string { return sandbox.RepoDir }

// sdkPath maps an absolute sandbox path onto the SDK filesystem, which is
// rooted at / (sandboxd runs with --root-dir=/).
func sdkPath(p string) string { return strings.TrimPrefix(p, "/") }

func (h *handle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = sandbox.RepoDir
	}
	if opts.Sudo {
		// sudo wraps the whole shell so every command in a chain runs as root.
		command = "sudo sh -c " + shellQuote(command)
	}
	// The SDK has no stdin pipe; deliver a prompt through a temp file.
	if opts.Stdin != nil {
		stdinPath := "/tmp/agentgopher-stdin-" + randomSuffix()
		if err := h.api.WriteReader(ctx, sdkPath(stdinPath), strings.NewReader(*opts.Stdin)); err != nil {
			return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
		}
		command = fmt.Sprintf("(%s) < %s; rc=$?; rm -f %s; exit $rc", command, stdinPath, stdinPath)
	}
	full := "cd " + shellQuote(cwd) + " && " + command

	if opts.OnLine == nil {
		res, err := h.api.Run(ctx, full)
		if ctx.Err() != nil {
			return sandbox.ExecResult{}, ctx.Err()
		}
		if err != nil {
			return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
		}
		return sandbox.ExecResult{Stdout: h.tail(res.Stdout, "\n"), Stderr: h.tail(res.Stderr, ""), ExitCode: res.ExitCode}, nil
	}

	proc, err := h.api.Start(ctx, full)
	if err != nil {
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
	}
	defer func() { _ = proc.Close() }()
	stdoutTail := stream.NewBoundedTail(h.o.MaxOutputTailChars, "\n")
	stderrTail := stream.NewBoundedTail(h.o.MaxOutputTailChars, "")
	lw := stream.NewLineWriter(func(line string) {
		stdoutTail.Push(line)
		opts.OnLine(line)
	})
	exitCode, exitSeen := 0, false
	for {
		ev, err := proc.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			lw.Flush()
			if ctx.Err() != nil {
				return sandbox.ExecResult{}, ctx.Err()
			}
			return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
		}
		switch ev.Type {
		case "stdout":
			_, _ = lw.Write(ev.Data)
		case "stderr":
			stderrTail.Push(string(ev.Data))
		case "exit":
			exitCode, exitSeen = ev.ExitCode, true
		}
	}
	lw.Flush()
	if !exitSeen {
		// A missing exit event must not read as success.
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: errors.New("agent-sandbox did not report an exit code")}
	}
	return sandbox.ExecResult{Stdout: stdoutTail.String(), Stderr: stderrTail.String(), ExitCode: exitCode}, nil
}

func (h *handle) tail(s, sep string) string {
	t := stream.NewBoundedTail(h.o.MaxOutputTailChars, sep)
	t.Push(s)
	return t.String()
}

func (h *handle) CopyIn(ctx context.Context, hostPath, sandboxPath string) error {
	info, err := os.Stat(hostPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		f, err := os.Open(hostPath)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		// sandboxd creates missing parent directories on write.
		return h.api.WriteReader(ctx, sdkPath(sandboxPath), f)
	}
	// A directory travels as one tar file and is unpacked in place.
	tarPath := "/tmp/agentgopher-copy-" + randomSuffix() + ".tar"
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(tarx.Pack(pw, hostPath, path.Base(sandboxPath))) }()
	err = h.api.WriteReader(ctx, sdkPath(tarPath), pr)
	_ = pr.Close()
	if err != nil {
		return err
	}
	dir := shellQuote(path.Dir(sandboxPath))
	res, err := h.api.Run(ctx, "mkdir -p "+dir+" && tar -xf "+shellQuote(tarPath)+" -C "+dir+"; rc=$?; rm -f "+shellQuote(tarPath)+"; exit $rc")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("copy %s into sandbox: tar exited %d: %s", hostPath, res.ExitCode, res.Stderr)
	}
	return nil
}

func (h *handle) CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error {
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(hostPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := h.api.ReadTo(ctx, sdkPath(sandboxPath), f); err != nil {
		return err
	}
	return f.Close()
}

// Close releases the claim even when the run context is already cancelled,
// so an aborted run does not leave a sandbox Pod behind.
func (h *handle) Close(context.Context) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return h.api.Close(ctx)
}

func randomSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
```

Note: `ev.Type` is `sandboxsdk.ProcessEventType`; compare against `sandboxsdk.ProcessEventStdout`, `ProcessEventStderr`, `ProcessEventExit` instead of string literals.

- [ ] **Step 4: fakes_test.go**

```go
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
	closeCtx context.Context
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
	f.closeCtx = ctx
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
```

- [ ] **Step 5: agentsandbox_test.go**

```go
package agentsandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sandboxsdk "sigs.k8s.io/agent-sandbox/clients/go/sandbox"

	"github.com/e11en/agentgopher/sandbox"
)

func TestProviderMetadataAndDefaults(t *testing.T) {
	p := New(&Options{Env: map[string]string{"A": "1"}})
	if p.Kind() != sandbox.KindIsolated || p.Name() != "agent-sandbox" || p.Env()["A"] != "1" {
		t.Fatalf("kind=%q name=%q env=%v", p.Kind(), p.Name(), p.Env())
	}
	t.Setenv("AGENTGOPHER_WARM_POOL", "")
	o := withDefaults(Options{})
	if o.Namespace != "agentgopher" || o.WarmPool != "agentgopher-sandboxes" || o.ShutdownAfter == 0 || o.ReadyTimeout == 0 {
		t.Fatalf("defaults = %+v", o)
	}
	t.Setenv("AGENTGOPHER_WARM_POOL", "pool-b")
	if got := withDefaults(Options{}).WarmPool; got != "pool-b" {
		t.Fatalf("WarmPool = %q", got)
	}
}

func TestSDKOptionsInClusterAndEnvSorted(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	so := sdkOptions(withDefaults(Options{}), map[string]string{"B": "2", "A": "1"})
	if so.Runtime != sandboxsdk.RuntimeSandboxd || so.Connectivity != "" || !so.Quiet {
		t.Fatalf("options = %+v", so)
	}
	if len(so.Env) != 2 || so.Env[0].Name != "A" || so.Env[1].Name != "B" || so.Env[1].Value != "2" {
		t.Fatalf("env = %+v", so.Env)
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	if got := sdkOptions(withDefaults(Options{}), nil).Connectivity; got != sandboxsdk.ConnectivityInClusterPodIP {
		t.Fatalf("Connectivity = %q", got)
	}
}

func TestCreateMakesRepoDirAndFailsOnError(t *testing.T) {
	f := newFakeAPI()
	p := New(&Options{Open: func(context.Context, map[string]string) (API, error) { return f, nil }})
	h, err := p.Create(context.Background(), sandbox.IsolatedCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if h.WorktreePath() != sandbox.RepoDir || f.lastCommand() != "mkdir -p '"+sandbox.RepoDir+"'" {
		t.Fatalf("path=%q cmd=%q", h.WorktreePath(), f.lastCommand())
	}
	failing := newFakeAPI()
	failing.script["mkdir"] = scripted{exitCode: 1, stderr: "ro"}
	p = New(&Options{Open: func(context.Context, map[string]string) (API, error) { return failing, nil }})
	if _, err := p.Create(context.Background(), sandbox.IsolatedCreateOptions{}); err == nil || !failing.closed {
		t.Fatalf("err=%v closed=%v", err, failing.closed)
	}
	boom := errors.New("no pool")
	p = New(&Options{Open: func(context.Context, map[string]string) (API, error) { return nil, boom }})
	if _, err := p.Create(context.Background(), sandbox.IsolatedCreateOptions{}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestExecDefaultsToRepoDirAndRespectsCwdAndSudo(t *testing.T) {
	f := newFakeAPI()
	f.script["ls"] = scripted{stdout: "a\n", exitCode: 0}
	h := newTestHandle(f)
	res, err := h.Exec(context.Background(), "ls", sandbox.ExecOptions{})
	if err != nil || res.Stdout != "a\n" || res.ExitCode != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := f.lastCommand(); got != "cd '"+sandbox.RepoDir+"' && ls" {
		t.Fatalf("command = %q", got)
	}
	f.script["false"] = scripted{exitCode: 3, stderr: "nope"}
	res, _ = h.Exec(context.Background(), "false", sandbox.ExecOptions{Cwd: "/tmp", Sudo: true})
	if res.ExitCode != 3 || res.Stderr != "nope" || f.lastCommand() != "cd '/tmp' && sudo sh -c 'false'" {
		t.Fatalf("res=%+v cmd=%q", res, f.lastCommand())
	}
}

func TestExecDeliversStdinThroughFile(t *testing.T) {
	f := newFakeAPI()
	h := newTestHandle(f)
	in := "hello"
	if _, err := h.Exec(context.Background(), "cat", sandbox.ExecOptions{Stdin: &in}); err != nil {
		t.Fatal(err)
	}
	cmd := f.lastCommand()
	if !strings.Contains(cmd, "(cat) < /tmp/agentgopher-stdin-") || !strings.Contains(cmd, "rm -f /tmp/agentgopher-stdin-") {
		t.Fatalf("command = %q", cmd)
	}
	var found bool
	for p, b := range f.files {
		if strings.HasPrefix(p, "tmp/agentgopher-stdin-") && string(b) == "hello" {
			found = true
		}
	}
	if !found {
		t.Fatalf("stdin file not written under tmp/: %v", f.files)
	}
}

func TestExecStreamsWholeLinesAndExitCode(t *testing.T) {
	f := newFakeAPI()
	f.script["work"] = scripted{stdout: "ab|c\nd|e", stderr: "warn", exitCode: 4}
	h := newTestHandle(f)
	var lines []string
	res, err := h.Exec(context.Background(), "work", sandbox.ExecOptions{OnLine: func(l string) { lines = append(lines, l) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0] != "abc" || lines[1] != "de" {
		t.Fatalf("lines = %q", lines)
	}
	if res.ExitCode != 4 || res.Stderr != "warn" || !strings.Contains(res.Stdout, "abc") {
		t.Fatalf("res = %+v", res)
	}
}

func TestExecWithoutExitEventIsStartError(t *testing.T) {
	f := newFakeAPI()
	f.script["work"] = scripted{stdout: "x", noExit: true}
	h := newTestHandle(f)
	_, err := h.Exec(context.Background(), "work", sandbox.ExecOptions{OnLine: func(string) {}})
	var se *sandbox.StartError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v", err)
	}
}

func TestExecCancelledContextIsReported(t *testing.T) {
	f := newFakeAPI()
	f.script["sleep"] = scripted{stdout: "a|b"}
	h := newTestHandle(f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Exec(ctx, "sleep 9", sandbox.ExecOptions{OnLine: func(string) {}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestCopyInFileUsesSDKPathWithoutLeadingSlash(t *testing.T) {
	f := newFakeAPI()
	h := newTestHandle(f)
	src := filepath.Join(t.TempDir(), "repo.bundle")
	os.WriteFile(src, []byte("bundle"), 0o644)
	if err := h.CopyIn(context.Background(), src, "/tmp/agentgopher-x/repo.bundle"); err != nil {
		t.Fatal(err)
	}
	if string(f.files["tmp/agentgopher-x/repo.bundle"]) != "bundle" {
		t.Fatalf("files = %v", f.files)
	}
}

func TestCopyInDirectoryTarsAndExtracts(t *testing.T) {
	f := newFakeAPI()
	h := newTestHandle(f)
	dir := filepath.Join(t.TempDir(), "node_modules")
	os.MkdirAll(filepath.Join(dir, "pkg"), 0o755)
	os.WriteFile(filepath.Join(dir, "pkg", "index.js"), []byte("1"), 0o644)
	if err := h.CopyIn(context.Background(), dir, sandbox.RepoDir+"/node_modules"); err != nil {
		t.Fatal(err)
	}
	var tarBytes []byte
	for p, b := range f.files {
		if strings.HasPrefix(p, "tmp/agentgopher-copy-") {
			tarBytes = b
		}
	}
	hdr, err := tar.NewReader(bytes.NewReader(tarBytes)).Next()
	if err != nil || hdr.Name != "node_modules" {
		t.Fatalf("hdr = %+v, %v", hdr, err)
	}
	cmd := f.lastCommand()
	if !strings.HasPrefix(cmd, "mkdir -p '"+sandbox.RepoDir+"' && tar -xf '/tmp/agentgopher-copy-") || !strings.Contains(cmd, "-C '"+sandbox.RepoDir+"'") {
		t.Fatalf("command = %q", cmd)
	}
	f.script["tar -xf"] = scripted{exitCode: 2, stderr: "bad"}
	if err := h.CopyIn(context.Background(), dir, sandbox.RepoDir+"/node_modules"); err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("err = %v", err)
	}
}

func TestCopyFileOutReadsSDKPath(t *testing.T) {
	f := newFakeAPI()
	f.reads["tmp/agentgopher-p/0001.patch"] = []byte("patch")
	h := newTestHandle(f)
	dst := filepath.Join(t.TempDir(), "out", "0001.patch")
	if err := h.CopyFileOut(context.Background(), "/tmp/agentgopher-p/0001.patch", dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "patch" {
		t.Fatalf("content = %q", b)
	}
	if err := h.CopyFileOut(context.Background(), "/tmp/missing", dst); err == nil {
		t.Fatal("expected error for a missing file")
	}
}

func TestCloseUsesDetachedContext(t *testing.T) {
	f := newFakeAPI()
	h := newTestHandle(f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.Close(ctx); err != nil || !f.closed || f.closeCtx.Err() != nil {
		t.Fatalf("err=%v closed=%v ctxErr=%v", err, f.closed, f.closeCtx.Err())
	}
}
```

- [ ] **Step 6:** `go vet ./sandbox/agentsandbox/ && go test -race ./sandbox/agentsandbox/ && golangci-lint run ./...`. Expected: PASS.
- [ ] **Step 7: Commit** `Add the agent-sandbox provider on the GKE Agent Sandbox Go SDK`.

### Task 4: Templates and example use the provider inside a cluster

**Files:**
- Modify: `internal/initcmd/templates/*/main.go` (the `sandboxProvider` helper), `internal/initcmd/templates_test.go`, `examples/isolated/main.go`

- [ ] **Step 1:** In every template, change the helper body to:

```go
// sandboxProvider picks Docker on a workstation and GKE Agent Sandbox inside
// an `agentgopher remote run` Job: gVisor-isolated Pods claimed from the
// warm pool that deploy/k8s defines, driven through the Agent Sandbox SDK.
func sandboxProvider() sandbox.Provider {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return agentsandbox.New(nil)
	}
	return docker.New(nil)
}
```

Replace the `sandbox/kubernetes` import with `"github.com/e11en/agentgopher/sandbox/agentsandbox"`. In `templates_test.go`, extend `TestTemplatesResolvePathsFromConfigDir` to also require `strings.Contains(src, "agentsandbox.New(nil)")`.

- [ ] **Step 2:** In `examples/isolated/main.go` add `case "agent-sandbox": provider = agentsandbox.New(nil)` next to the daytona case and mention it in the usage line and file header.
- [ ] **Step 3:** `go test -race ./internal/initcmd/ && go build ./... && golangci-lint run ./...`, then scaffold one template in the scratchpad with the CLI, add a replace directive, `go mod tidy`, `go vet .`. Expected: PASS.
- [ ] **Step 4: Commit** `Use GKE Agent Sandbox from scaffolded templates inside a cluster`.

### Task 5: SandboxTemplate, warm pool and RBAC manifests

**Files:**
- Create: `deploy/k8s/sandboxtemplate.yaml`, `deploy/k8s/sandboxwarmpool.yaml`
- Modify: `deploy/k8s/rbac.yaml`, `deploy/k8s/kustomization.yaml`

- [ ] **Step 1: sandboxtemplate.yaml**

```yaml
# Blueprint for agent sandboxes claimed by sandbox/agentsandbox. The runner
# image is reused as the sandbox image; an initContainer injects the sandboxd
# binary (upstream "topology B"), so commands run with the image's own tools.
apiVersion: extensions.agents.x-k8s.io/v1beta1
kind: SandboxTemplate
metadata:
  name: agentgopher-sandbox
  namespace: agentgopher
spec:
  # Replaces the managed default policy, so every rule is spelled out:
  # ingress only from runner Jobs on the sandboxd ports, egress to cluster
  # DNS and the public internet but not to private ranges or the metadata server.
  networkPolicy:
    ingress:
      - from:
          - podSelector:
              matchLabels:
                app.kubernetes.io/component: runner
        ports:
          - { protocol: TCP, port: 8080 }
          - { protocol: TCP, port: 9090 }
    egress:
      - to:
          - namespaceSelector: {}
            podSelector:
              matchLabels:
                k8s-app: kube-dns
        ports:
          - { protocol: UDP, port: 53 }
          - { protocol: TCP, port: 53 }
      - to:
          - ipBlock:
              cidr: 0.0.0.0/0
              except:
                - 10.0.0.0/8
                - 172.16.0.0/12
                - 192.168.0.0/16
                - 169.254.0.0/16
        ports:
          - { protocol: TCP, port: 443 }
  podTemplate:
    spec:
      runtimeClassName: gvisor
      automountServiceAccountToken: false
      restartPolicy: OnFailure
      securityContext:
        runAsNonRoot: true
        fsGroup: 1000
      initContainers:
        - name: install-sandboxd
          image: us-central1-docker.pkg.dev/k8s-staging-images/agent-sandbox/sandboxd:latest-main
          command: ["/bin/sh", "-c", "cp /usr/local/bin/sandboxd /sandboxd-bin/sandboxd"]
          securityContext:
            runAsUser: 1000
            runAsGroup: 1000
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
          resources:
            requests: { cpu: 100m, memory: 128Mi }
            limits: { cpu: 500m, memory: 512Mi }
          volumeMounts:
            - name: sandboxd-bin
              mountPath: /sandboxd-bin
      containers:
        - name: agent
          image: REPLACE_WITH_RUNNER_IMAGE
          # root-dir / lets the provider address any absolute sandbox path.
          command: ["/sandboxd-bin/sandboxd", "--root-dir=/", "--rest-port=8080", "--grpc-port=9090"]
          securityContext:
            runAsUser: 1000
            runAsGroup: 1000
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
          resources:
            requests: { cpu: 500m, memory: 1Gi, ephemeral-storage: 2Gi }
            limits: { cpu: "2", memory: 4Gi }
          ports:
            - containerPort: 8080
            - containerPort: 9090
          readinessProbe:
            httpGet:
              path: /v1/health
              port: 8080
          volumeMounts:
            - name: sandboxd-bin
              mountPath: /sandboxd-bin
              readOnly: true
      volumes:
        - name: sandboxd-bin
          emptyDir: {}
```

- [ ] **Step 2: sandboxwarmpool.yaml**

```yaml
# Pre-started sandboxes. A claim that injects env (every agentgopher run does,
# for the agent's tokens) cold-starts from the template, so one replica keeps
# the image warm on a node without holding idle capacity.
apiVersion: extensions.agents.x-k8s.io/v1beta1
kind: SandboxWarmPool
metadata:
  name: agentgopher-sandboxes
  namespace: agentgopher
spec:
  replicas: 1
  updateStrategy:
    type: Recreate
  sandboxTemplateRef:
    name: agentgopher-sandbox
```

- [ ] **Step 3:** Append to the Role rules in `rbac.yaml`:

```yaml
  - apiGroups: ["extensions.agents.x-k8s.io"]
    resources: ["sandboxclaims"]
    verbs: ["create", "get", "list", "watch", "delete"]
  - apiGroups: ["agents.x-k8s.io"]
    resources: ["sandboxes"]
    verbs: ["get", "list", "watch"]
```

Add both new files to `kustomization.yaml`.

- [ ] **Step 4:** `kubectl kustomize deploy/k8s | grep -c '^kind:'` (expect 7) and `ruby -ryaml -e 'Dir["deploy/k8s/*.yaml"].each { |f| YAML.load_stream(File.read(f)) }'`.
- [ ] **Step 5: Commit** `Add the Agent Sandbox template, warm pool and claim permissions`.

### Task 6: Terraform enables the Agent Sandbox add-on

**Files:**
- Modify: `deploy/terraform/versions.tf`, `deploy/terraform/main.tf`, `deploy/terraform/README.md`

- [ ] **Step 1:** In `versions.tf` set the google provider constraint to `">= 7.34.0, < 9.0.0"` (`addons_config.agent_sandbox_config` exists from 7.34). In `main.tf` add inside `google_container_cluster.agentgopher`:

```hcl
  addons_config {
    agent_sandbox_config {
      enabled = true
    }
  }
```

In `deploy/terraform/README.md` add one sentence: the cluster has the managed Agent Sandbox add-on, which installs the `Sandbox` CRDs and controller; gVisor is on by default on Autopilot.

- [ ] **Step 2:** `terraform -chdir=deploy/terraform fmt -check && terraform -chdir=deploy/terraform init -upgrade -backend=false -input=false > /dev/null && terraform -chdir=deploy/terraform validate`. Expected: `Success! The configuration is valid.` If validate rejects the block on the resolved provider version, raise the lower bound to the first version whose changelog lists `agent_sandbox_config` and record it in the ledger.
- [ ] **Step 3: Commit** `Enable the managed Agent Sandbox add-on on the GKE cluster`.

### Task 7: Documentation

**Files:**
- Modify: `docs/kubernetes.md`, `README.md`, `docs/plan.md`

- [ ] **Step 1:** `docs/kubernetes.md`: in the apply step add `REPLACE_WITH_RUNNER_IMAGE` to the `sed` (value `$(terraform -chdir=deploy/terraform output -raw image_repository)/agentgopher:latest`). Rewrite "Sandboxes inside the cluster": the templates use GKE Agent Sandbox (`agentsandbox.New(nil)`): gVisor-isolated Pods from the `agentgopher-sandboxes` warm pool, template in `deploy/k8s/sandboxtemplate.yaml`, env `AGENTGOPHER_WARM_POOL` selects another pool; `sandbox/kubernetes` remains for plain Pods and now runs them under gVisor unless `RuntimeClass: kubernetes.NoRuntimeClass`. Cleanup line: `kubectl delete sandboxclaims -n agentgopher --all`.
- [ ] **Step 2:** `README.md`: provider list mentions `agentsandbox` (GKE Agent Sandbox) and gVisor default for `kubernetes`.
- [ ] **Step 3:** `docs/plan.md`: add phase `8. **Agent Sandbox**: gVisor by default for the kubernetes provider, the agentsandbox provider on the GKE Agent Sandbox SDK, warm pool manifests, add-on in Terraform.` and two decisions: gVisor default with explicit opt-out; Agent Sandbox as the in-cluster sandbox because of kernel isolation and warm pools, `sandbox/kubernetes` kept as the plain fallback.
- [ ] **Step 4:** `perl -ne 'print "$ARGV:$.: $_" if /\x{2014}|\x{2013}/' docs/kubernetes.md README.md docs/plan.md deploy/k8s/*.yaml deploy/terraform/*.tf sandbox/agentsandbox/*.go internal/tarx/*.go`. Expected: nothing.
- [ ] **Step 5: Commit** `Document gVisor and GKE Agent Sandbox`.

### Task 8: Manual end-to-end (needs the cluster; skipped when GCP is out of scope)

`terraform apply`, push the image, apply `deploy/k8s`, `kubectl get sandboxwarmpool -n agentgopher` shows 1 ready replica, `agentgopher remote run --repo <demo repo>`; the Job log shows a claim and an agent run inside it. Open questions to verify here and nowhere else: `--root-dir=/` is accepted by sandboxd; the sandbox Pod can reach the agent's API over 443; the `sandboxd:latest-main` image tag still exists (pin it to a release tag once one is published).
