# agentgopher Phase 7: Kubernetes Sandbox Provider and GKE Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `sandbox/kubernetes` isolated provider that runs each agent sandbox as a Pod, plus `agentgopher remote run`, which starts an agentgopher run as a Kubernetes Job on a new GKE Autopilot cluster, streams its log, and prints the pull request links it opened.

**Architecture:** Part A plugs into the isolated-provider seam from phase 4: `kubernetes.New` returns a `sandbox.IsolatedProvider` whose handle creates one Pod, execs commands through the Kubernetes `exec` subresource, moves files with `tar` over exec, and deletes the Pod on close. Part B adds a public `remote` package (Job builder, log streaming, wait, in-cluster entrypoint), a `remote` cobra command, a container image, Kubernetes manifests and Terraform for the cluster. In the cluster there is no host repo, so the entrypoint clones the repo, runs the user's script with the named branch strategy, pushes the branch and opens a PR with `gh`.

**Tech Stack:** Go 1.25, `k8s.io/client-go`, `k8s.io/api`, `k8s.io/apimachinery` (all `v0.36.3`, the newest release found on pkg.go.dev in July 2026; confirm with `go list -m -versions k8s.io/client-go` and use the newest patch of the same minor), cobra (from phase 6), Docker, GitHub Actions, Terraform (installed: v1.5.7) with the `hashicorp/google` provider `~> 6.0`, GKE Autopilot, Artifact Registry, Secret Manager add-on for GKE, Workload Identity.

**Spec:** `docs/plan.md`, sections "Kubernetes and GKE" and "Decisions". Upstream has no Kubernetes provider; the closest reference is `src/sandboxes/vercel.ts` (isolated provider) at `mattpocock/sandcastle` commit `7a4e7f7`. The phase 4 plan defines the isolated-provider contract this plan builds on.

## Global Constraints

- Module path `github.com/e11en/agentgopher`, `go 1.25` in `go.mod`.
- Allowed third-party Go dependencies in this plan: `k8s.io/client-go`, `k8s.io/api`, `k8s.io/apimachinery` (one shared version), plus cobra which phase 6 already added. Nothing else.
- Config directory is `.agentgopher/`. Env file `.agentgopher/.env`, logs in `.agentgopher/logs/`.
- Public API added here: `sandbox/kubernetes`, `remote`. Everything else under `internal/`.
- Code comments, test names, commit messages, manifests and Terraform in English. No em or en dashes anywhere (use ` - ` or commas).
- Commit messages: imperative, no attribution footer, no `Co-Authored-By`.
- Every `Exec` implementation streams lines live through `OnLine` while the process runs.
- Go tests must not need a cluster, Docker, gcloud or network: use `k8s.io/client-go/kubernetes/fake` and a fake `remotecommand.Executor`. Real-cluster checks are manual steps, marked as such.
- Terraform is validated with `terraform fmt -check` and `terraform validate` only. The plan never runs `terraform apply`.
- `project_id` and `region` are Terraform variables without defaults. Never write a real or made-up GCP project id into the repo.
- Secrets never enter the repo or the image. They live in Secret Manager and reach the Job as files through the GKE Secret Manager add-on (CSI).
- Cross-plan contract (from phase 4): `sandbox.IsolatedHandle` is `sandbox.Handle` plus `CopyIn(ctx, hostPath, sandboxPath string) error` and `CopyFileOut(ctx, sandboxPath, hostPath string) error`; `sandbox.IsolatedProvider` is built with `sandbox.NewIsolatedProvider(sandbox.IsolatedProviderConfig{Name, Env, Create})` where `Create func(ctx, sandbox.IsolatedCreateOptions{Env}) (sandbox.IsolatedHandle, error)`; `sandbox.RepoDir` is the in-sandbox repo path (`/home/agent/workspace`). The orchestrator needs no change for a new isolated provider.
- Cross-plan contract (from phase 5): `agentgopher.RunOptions.BranchStrategy *sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch, BaseBranch}`.
- Cross-plan contract (from phase 6): `cmd/agentgopher/main.go` builds a cobra root command `rootCmd` with `rootCmd.AddCommand(...)` calls in `main.go`; one file per command; init templates read `AGENTGOPHER_BRANCH` and `AGENTGOPHER_BASE_BRANCH` when set and pass them as the named branch strategy (add that to the phase 6 templates if it is missing; Task 9 of this plan says exactly what to add).

## Review Focus

1. The agent's stdout from a Pod arrives in chunks that split a line: `OnLine` still receives whole lines, and the final unterminated line is delivered when the process ends. Test added in Task 3.
2. A sandbox Pod never reaches `Running` (image pull error, no capacity): `Create` fails with a clear error naming the Pod and the last container waiting reason, and the Pod is deleted. Test added in Task 2.
3. `CopyIn` of a directory containing symlinks and empty subdirectories: the tar stream reproduces them, and the destination directory is created first. Test added in Task 4.
4. `remote run` is interrupted with Ctrl-C while the Job runs: the Job keeps running in the cluster and the command prints how to follow it (`kubectl logs job/<name> -n agentgopher -f`) instead of deleting it. Test added in Task 9.
5. The in-cluster script exits 0 but produced no commits: the entrypoint prints `No commits produced, no PR` and exits 0, without pushing an empty branch. Test added in Task 8.

---

## File structure

```
go.mod / go.sum                          add client-go, api, apimachinery
internal/stream/linewriter.go            NewLineWriter (shared with nosandbox from phase 4; skip if present)
internal/stream/linewriter_test.go
sandbox/kubernetes/options.go            Options, defaults, client and REST config loading
sandbox/kubernetes/pod.go                Pod spec, create, wait for Running, delete
sandbox/kubernetes/exec.go               Exec via remotecommand, exit code mapping
sandbox/kubernetes/copy.go               CopyIn (tar over exec stdin), CopyFileOut (cat over exec stdout)
sandbox/kubernetes/kubernetes.go         New() returning sandbox.IsolatedProvider
sandbox/kubernetes/fakes_test.go         fake executor, fake clientset helpers
sandbox/kubernetes/*_test.go
remote/spec.go                           RunSpec, defaults, validation, Job name
remote/job.go                            Submit (Job builder)
remote/logs.go                           StreamLogs, Wait, Outcome
remote/entrypoint.go                     Entrypoint: clone, run script, push, open PR
remote/*_test.go
cmd/agentgopher/remote.go                `remote run` and hidden `remote entrypoint`
cmd/agentgopher/remote_test.go
Dockerfile                               runner image: agentgopher, git, gh, go, node, claude
.github/workflows/release-image.yml      build and push on tag
deploy/k8s/namespace.yaml
deploy/k8s/serviceaccount.yaml
deploy/k8s/rbac.yaml
deploy/k8s/secretproviderclass.yaml
deploy/k8s/job-example.yaml              reference copy of what Submit builds
deploy/terraform/versions.tf
deploy/terraform/variables.tf
deploy/terraform/main.tf                 APIs, cluster, registry
deploy/terraform/iam.tf                  runner SA, Workload Identity, secrets IAM
deploy/terraform/github.tf               Workload Identity Federation for the release workflow
deploy/terraform/outputs.tf
deploy/terraform/README.md
docs/kubernetes.md                       how to deploy and run
```

Secrets approach: the GKE Secret Manager add-on mounts secrets as files through a `SecretProviderClass` (driver `secrets-store-gke.csi.k8s.io`), authenticated by Workload Identity. Chosen over synced Kubernetes Secrets because it needs no sync controller and never copies secret values into etcd; the entrypoint reads `/var/secrets/<NAME>` files into its environment.

---

### Task 1: Dependencies and shared line writer

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/stream/linewriter.go`, `internal/stream/linewriter_test.go` (skip both if phase 4 already added `stream.NewLineWriter` with the same signature)

**Interfaces:**
- Produces: `stream.NewLineWriter(onLine func(string)) *LineWriter`, `(*LineWriter).Write([]byte) (int, error)`, `(*LineWriter).Flush()`. `Write` delivers each complete line without its `\n`; `Flush` delivers a trailing partial line.

- [ ] **Step 1: Add the Kubernetes modules**

```bash
go list -m -versions k8s.io/client-go | tr ' ' '\n' | tail -3
go get k8s.io/client-go@v0.36.3 k8s.io/api@v0.36.3 k8s.io/apimachinery@v0.36.3
go mod tidy
```

Use the newest `v0.36.x` printed by the first command if it is higher than `v0.36.3`, for all three modules.

- [ ] **Step 2: Write the failing line writer test**

`internal/stream/linewriter_test.go`:
```go
package stream

import "testing"

func TestLineWriterSplitsAcrossWrites(t *testing.T) {
	var lines []string
	w := NewLineWriter(func(s string) { lines = append(lines, s) })
	w.Write([]byte("hel"))
	w.Write([]byte("lo\nwor"))
	w.Write([]byte("ld\n\npartial"))
	if len(lines) != 3 || lines[0] != "hello" || lines[1] != "world" || lines[2] != "" {
		t.Fatalf("lines = %q", lines)
	}
	w.Flush()
	if len(lines) != 4 || lines[3] != "partial" {
		t.Fatalf("after flush: %q", lines)
	}
	w.Flush()
	if len(lines) != 4 {
		t.Fatal("second flush must not repeat")
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/stream/ -run LineWriter`
Expected: FAIL, "undefined: NewLineWriter".

- [ ] **Step 4: Write linewriter.go**

```go
package stream

import "bytes"

// LineWriter splits a byte stream into lines and hands each complete line to
// onLine as soon as it arrives.
type LineWriter struct {
	onLine func(string)
	buf    []byte
}

// NewLineWriter returns a writer that calls onLine per complete line.
func NewLineWriter(onLine func(string)) *LineWriter { return &LineWriter{onLine: onLine} }

func (w *LineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		w.onLine(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
}

// Flush delivers a trailing partial line, if any.
func (w *LineWriter) Flush() {
	if len(w.buf) > 0 {
		w.onLine(string(w.buf))
		w.buf = nil
	}
}
```

If `sandbox/nosandbox/nosandbox.go` still has its own private `lineWriter`, replace it with `stream.NewLineWriter` and run `go test ./sandbox/nosandbox/`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go build ./... && go test ./internal/stream/ ./sandbox/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/stream/ sandbox/nosandbox/
git commit -m "Add Kubernetes client modules and shared line writer"
```

---

### Task 2: Kubernetes options, client loading, Pod create and delete

**Files:**
- Create: `sandbox/kubernetes/options.go`, `sandbox/kubernetes/pod.go`, `sandbox/kubernetes/fakes_test.go`, `sandbox/kubernetes/pod_test.go`

**Interfaces:**
- Produces:
  ```go
  package kubernetes
  type Options struct {
      Namespace      string                       // default "agentgopher"
      Image          string                       // required
      Resources      corev1.ResourceRequirements  // default requests cpu 500m, memory 1Gi; limits cpu 2, memory 4Gi
      ServiceAccount string                       // default "" (namespace default)
      NodeSelector   map[string]string
      PodTTL         time.Duration                // default 2h; becomes activeDeadlineSeconds
      ReadyTimeout   time.Duration                // default 5m
      Kubeconfig     string                       // default: in-cluster when KUBERNETES_SERVICE_HOST is set, else kubeconfig loading rules
      Env            map[string]string            // provider env, merged at launch
      // Test seams. Zero values use the real implementations.
      Client   k8sclient.Interface
      Config   *rest.Config
      Executor ExecutorFactory
  }
  type ExecutorFactory func(client k8sclient.Interface, cfg *rest.Config, namespace, pod string, opts *corev1.PodExecOptions) (remotecommand.Executor, error)
  const WorkDir = sandbox.RepoDir
  func loadClient(o Options) (k8sclient.Interface, *rest.Config, error)
  func podSpec(o Options, name string, env map[string]string) *corev1.Pod
  func createPod(ctx, client, o Options, name string, env map[string]string) (*corev1.Pod, error) // creates and waits for Running
  func deletePod(ctx, client, namespace, name string) error                                       // ignores NotFound
  ```
- Pod labels: `app.kubernetes.io/name: agentgopher`, `app.kubernetes.io/component: sandbox`. Pod name `agentgopher-sb-<8 hex>`. Container `agent`, command `["sleep","infinity"]`, `RestartPolicy: Never`, `ActiveDeadlineSeconds` from `PodTTL`, `WorkingDir: sandbox.RepoDir`, env from the merged map (sorted by key for a stable spec).
- Errors: `*kubernetes.PodNotReadyError{Pod, Namespace, Phase, Reason string}`.

- [ ] **Step 1: Write the test helpers**

`sandbox/kubernetes/fakes_test.go`:
```go
package kubernetes

import (
	"context"
	"io"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"
)

// newRunningClientset returns a fake clientset whose created pods are
// immediately Running (phase set by a reactor before the tracker stores them).
func newRunningClientset() *fake.Clientset {
	cs := fake.NewClientset()
	cs.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		pod := action.(k8stesting.CreateAction).GetObject().(*corev1.Pod)
		pod.Status.Phase = corev1.PodRunning
		return false, nil, nil
	})
	return cs
}

// scriptedExec is one fake command outcome.
type scriptedExec struct {
	stdout   string // written in chunks split at every "|" so tests can check line splitting
	stderr   string
	exitCode int
}

// fakeExecutor records exec calls and answers them from a script keyed on the
// last argument of the exec command (the `sh -c` body).
type fakeExecutor struct {
	mu     sync.Mutex
	script map[string]scriptedExec
	calls  []execCall
}

type execCall struct {
	pod     string
	command []string
	stdin   string
}

func (f *fakeExecutor) factory() ExecutorFactory {
	return func(_ k8sclient.Interface, _ *rest.Config, _, pod string, opts *corev1.PodExecOptions) (remotecommand.Executor, error) {
		return &fakeStream{f: f, pod: pod, command: opts.Command}, nil
	}
}

type fakeStream struct {
	f       *fakeExecutor
	pod     string
	command []string
}

func (s *fakeStream) Stream(o remotecommand.StreamOptions) error {
	return s.StreamWithContext(context.Background(), o)
}

func (s *fakeStream) StreamWithContext(ctx context.Context, o remotecommand.StreamOptions) error {
	var stdin string
	if o.Stdin != nil {
		b, _ := io.ReadAll(o.Stdin)
		stdin = string(b)
	}
	s.f.mu.Lock()
	s.f.calls = append(s.f.calls, execCall{pod: s.pod, command: s.command, stdin: stdin})
	body := s.command[len(s.command)-1]
	sc, ok := s.f.script[body]
	s.f.mu.Unlock()
	if !ok {
		sc = scriptedExec{}
	}
	for _, chunk := range strings.Split(sc.stdout, "|") {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if o.Stdout != nil {
			_, _ = io.WriteString(o.Stdout, chunk)
		}
	}
	if o.Stderr != nil && sc.stderr != "" {
		_, _ = io.WriteString(o.Stderr, sc.stderr)
	}
	if sc.exitCode != 0 {
		return utilexec.CodeExitError{Err: fmt.Errorf("command terminated with exit code %d", sc.exitCode), Code: sc.exitCode}
	}
	return nil
}
```

Add `"fmt"` to the imports of that file.

- [ ] **Step 2: Write the failing tests**

`sandbox/kubernetes/pod_test.go`:
```go
package kubernetes

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestPodSpecDefaults(t *testing.T) {
	o := withDefaults(Options{Image: "img:1"})
	pod := podSpec(o, "agentgopher-sb-abc", map[string]string{"B": "2", "A": "1"})
	c := pod.Spec.Containers[0]
	if pod.Namespace != "agentgopher" || c.Image != "img:1" || c.Name != "agent" || c.WorkingDir != WorkDir {
		t.Fatalf("pod = %+v", pod)
	}
	if len(c.Command) != 2 || c.Command[0] != "sleep" || pod.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Fatalf("spec = %+v", pod.Spec)
	}
	if *pod.Spec.ActiveDeadlineSeconds != int64((2 * time.Hour).Seconds()) {
		t.Fatalf("deadline = %d", *pod.Spec.ActiveDeadlineSeconds)
	}
	if c.Env[0].Name != "A" || c.Env[1].Name != "B" {
		t.Fatalf("env not sorted: %+v", c.Env)
	}
	if !c.Resources.Requests.Cpu().Equal(resource.MustParse("500m")) || !c.Resources.Limits.Memory().Equal(resource.MustParse("4Gi")) {
		t.Fatalf("resources = %+v", c.Resources)
	}
	if pod.Labels["app.kubernetes.io/name"] != "agentgopher" {
		t.Fatalf("labels = %v", pod.Labels)
	}
}

func TestPodSpecOverrides(t *testing.T) {
	o := withDefaults(Options{Image: "img", Namespace: "ns", ServiceAccount: "sa", NodeSelector: map[string]string{"pool": "agents"}, PodTTL: time.Minute})
	pod := podSpec(o, "p", nil)
	if pod.Namespace != "ns" || pod.Spec.ServiceAccountName != "sa" || pod.Spec.NodeSelector["pool"] != "agents" || *pod.Spec.ActiveDeadlineSeconds != 60 {
		t.Fatalf("pod = %+v", pod.Spec)
	}
}

func TestCreatePodWaitsForRunning(t *testing.T) {
	cs := newRunningClientset()
	o := withDefaults(Options{Image: "img", ReadyTimeout: time.Second})
	pod, err := createPod(context.Background(), cs, o, "agentgopher-sb-1", nil)
	if err != nil || pod.Status.Phase != corev1.PodRunning {
		t.Fatalf("pod = %+v, err = %v", pod, err)
	}
	if _, err := cs.CoreV1().Pods("agentgopher").Get(context.Background(), "agentgopher-sb-1", metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestCreatePodFailsWhenNeverReady(t *testing.T) {
	cs := fake.NewClientset()
	cs.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		pod := action.(k8stesting.CreateAction).GetObject().(*corev1.Pod)
		pod.Status.Phase = corev1.PodPending
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull"}}}}
		return false, nil, nil
	})
	o := withDefaults(Options{Image: "img", ReadyTimeout: 300 * time.Millisecond})
	_, err := createPod(context.Background(), cs, o, "agentgopher-sb-2", nil)
	var nr *PodNotReadyError
	if !errors.As(err, &nr) || nr.Reason != "ErrImagePull" || nr.Pod != "agentgopher-sb-2" {
		t.Fatalf("err = %v", err)
	}
	if _, err := cs.CoreV1().Pods("agentgopher").Get(context.Background(), "agentgopher-sb-2", metav1.GetOptions{}); err == nil {
		t.Fatal("pod must be deleted after a failed wait")
	}
}

func TestDeletePodIgnoresNotFound(t *testing.T) {
	if err := deletePod(context.Background(), fake.NewClientset(), "agentgopher", "missing"); err != nil {
		t.Fatal(err)
	}
}

func TestWithDefaultsRequiresImage(t *testing.T) {
	if _, _, err := loadClient(withDefaults(Options{})); err == nil {
		t.Fatal("expected error for missing image")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./sandbox/kubernetes/`
Expected: FAIL, "undefined: withDefaults".

- [ ] **Step 4: Write options.go**

```go
// Package kubernetes runs each agent sandbox as a Kubernetes Pod.
package kubernetes

import (
	"errors"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/e11en/agentgopher/sandbox"
)

// WorkDir is the repo path inside the sandbox Pod.
const WorkDir = sandbox.RepoDir

// ExecutorFactory builds the remotecommand executor for one exec call. The
// default builds a WebSocket executor with SPDY fallback; tests inject a fake.
type ExecutorFactory func(client k8sclient.Interface, cfg *rest.Config, namespace, pod string, opts *corev1.PodExecOptions) (remotecommand.Executor, error)

// Options configure the provider.
type Options struct {
	// Namespace the sandbox Pods are created in. Default: "agentgopher".
	Namespace string
	// Image of the sandbox container. Required. It must contain git and the agent CLI.
	Image string
	// Resources of the sandbox container. Default: requests 500m CPU and 1Gi,
	// limits 2 CPU and 4Gi, which fits GKE Autopilot's general-purpose class.
	Resources corev1.ResourceRequirements
	// ServiceAccount for the Pod. Default: the namespace default.
	ServiceAccount string
	NodeSelector   map[string]string
	// PodTTL bounds the Pod's lifetime as a safety net against leaks. Default: 2h.
	PodTTL time.Duration
	// ReadyTimeout bounds the wait for the Pod to reach Running. Default: 5m.
	ReadyTimeout time.Duration
	// Kubeconfig path. Default: in-cluster config when KUBERNETES_SERVICE_HOST
	// is set, otherwise the standard kubeconfig loading rules.
	Kubeconfig string
	// Env is injected into every sandbox. Merged at launch time.
	Env map[string]string

	// Client, Config and Executor are test seams; zero values use the real ones.
	Client   k8sclient.Interface
	Config   *rest.Config
	Executor ExecutorFactory
}

func withDefaults(o Options) Options {
	if o.Namespace == "" {
		o.Namespace = "agentgopher"
	}
	if o.PodTTL == 0 {
		o.PodTTL = 2 * time.Hour
	}
	if o.ReadyTimeout == 0 {
		o.ReadyTimeout = 5 * time.Minute
	}
	if o.Resources.Requests == nil && o.Resources.Limits == nil {
		o.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("1Gi")},
			Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi")},
		}
	}
	if o.Executor == nil {
		o.Executor = defaultExecutor
	}
	return o
}

// loadClient returns the configured client, or builds one from the kubeconfig
// or the in-cluster environment.
func loadClient(o Options) (k8sclient.Interface, *rest.Config, error) {
	if o.Image == "" {
		return nil, nil, errors.New("kubernetes: Options.Image is required")
	}
	if o.Client != nil {
		return o.Client, o.Config, nil
	}
	var cfg *rest.Config
	var err error
	switch {
	case o.Kubeconfig != "":
		cfg, err = clientcmd.BuildConfigFromFlags("", o.Kubeconfig)
	case os.Getenv("KUBERNETES_SERVICE_HOST") != "":
		cfg, err = rest.InClusterConfig()
	default:
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
	}
	if err != nil {
		return nil, nil, err
	}
	client, err := k8sclient.NewForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	return client, cfg, nil
}
```

`defaultExecutor` is written in Task 3; until then add a stub in `exec.go` so the package compiles:
```go
package kubernetes

func defaultExecutor(_ k8sclient.Interface, _ *rest.Config, _, _ string, _ *corev1.PodExecOptions) (remotecommand.Executor, error) {
	return nil, errors.New("not implemented")
}
```
with the matching imports.

- [ ] **Step 5: Write pod.go**

```go
package kubernetes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sclient "k8s.io/client-go/kubernetes"
)

// PodNotReadyError reports a sandbox Pod that did not reach Running in time.
type PodNotReadyError struct {
	Pod       string
	Namespace string
	Phase     string
	Reason    string
}

func (e *PodNotReadyError) Error() string {
	return fmt.Sprintf("sandbox pod %s/%s not ready: phase %s, reason %q", e.Namespace, e.Pod, e.Phase, e.Reason)
}

func newPodName() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "agentgopher-sb-" + hex.EncodeToString(b)
}

func podSpec(o Options, name string, env map[string]string) *corev1.Pod {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	envVars := make([]corev1.EnvVar, 0, len(keys))
	for _, k := range keys {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: env[k]})
	}
	deadline := int64(o.PodTTL.Seconds())
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: o.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "agentgopher",
				"app.kubernetes.io/component": "sandbox",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:         corev1.RestartPolicyNever,
			ActiveDeadlineSeconds: &deadline,
			ServiceAccountName:    o.ServiceAccount,
			NodeSelector:          o.NodeSelector,
			Containers: []corev1.Container{{
				Name:       "agent",
				Image:      o.Image,
				Command:    []string{"sleep", "infinity"},
				WorkingDir: WorkDir,
				Env:        envVars,
				Resources:  o.Resources,
			}},
		},
	}
}

// createPod creates the Pod and waits until it is Running. On timeout the Pod
// is deleted and a PodNotReadyError is returned.
func createPod(ctx context.Context, client k8sclient.Interface, o Options, name string, env map[string]string) (*corev1.Pod, error) {
	pods := client.CoreV1().Pods(o.Namespace)
	if _, err := pods.Create(ctx, podSpec(o, name, env), metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("create sandbox pod: %w", err)
	}
	deadline := time.Now().Add(o.ReadyTimeout)
	var last *corev1.Pod
	for {
		pod, err := pods.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			_ = deletePod(context.WithoutCancel(ctx), client, o.Namespace, name)
			return nil, err
		}
		last = pod
		if pod.Status.Phase == corev1.PodRunning {
			return pod, nil
		}
		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			_ = deletePod(context.WithoutCancel(ctx), client, o.Namespace, name)
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	_ = deletePod(context.WithoutCancel(ctx), client, o.Namespace, name)
	return nil, &PodNotReadyError{Pod: name, Namespace: o.Namespace, Phase: string(last.Status.Phase), Reason: waitingReason(last)}
}

func waitingReason(pod *corev1.Pod) string {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			return cs.State.Waiting.Reason
		}
	}
	return pod.Status.Reason
}

// deletePod deletes the Pod and treats NotFound as success.
func deletePod(ctx context.Context, client k8sclient.Interface, namespace, name string) error {
	err := client.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./sandbox/kubernetes/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add sandbox/kubernetes/
git commit -m "Add Kubernetes sandbox options and Pod lifecycle"
```

---

### Task 3: Exec through the Kubernetes exec subresource

**Files:**
- Create: `sandbox/kubernetes/exec.go` (replace the stub), `sandbox/kubernetes/exec_test.go`

**Interfaces:**
- Consumes: `stream.NewLineWriter` (Task 1), `Options.Executor` (Task 2).
- Produces:
  ```go
  type handle struct { client k8sclient.Interface; cfg *rest.Config; o Options; pod string }
  func (h *handle) WorktreePath() string                       // WorkDir
  func (h *handle) Exec(ctx, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error)
  func (h *handle) Close(ctx) error                            // deletePod
  func defaultExecutor(client, cfg, namespace, pod string, opts *corev1.PodExecOptions) (remotecommand.Executor, error)
  ```
- `Exec` runs `["sh","-c",command]` (prefixed with `cd <cwd> &&` when `opts.Cwd` is set), streams stdout through `OnLine`, bounds stdout and stderr with `stream.NewBoundedTail`, pipes `opts.Stdin`, and maps `utilexec.CodeExitError` to `ExecResult.ExitCode`. `Sudo` is ignored: the container user is fixed by the image. Context cancellation returns `ctx.Err()`.

- [ ] **Step 1: Write the failing tests**

`sandbox/kubernetes/exec_test.go`:
```go
package kubernetes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/e11en/agentgopher/sandbox"
)

func newTestHandle(t *testing.T, fe *fakeExecutor) *handle {
	t.Helper()
	cs := newRunningClientset()
	o := withDefaults(Options{Image: "img", Client: cs, Executor: fe.factory()})
	pod, err := createPod(context.Background(), cs, o, "agentgopher-sb-t", nil)
	if err != nil {
		t.Fatal(err)
	}
	return &handle{client: cs, o: o, pod: pod.Name}
}

func TestExecStreamsWholeLinesAcrossChunks(t *testing.T) {
	fe := &fakeExecutor{script: map[string]scriptedExec{"echo hi": {stdout: "hel|lo\nwor|ld\ntail"}}}
	h := newTestHandle(t, fe)
	var lines []string
	res, err := h.Exec(context.Background(), "echo hi", sandbox.ExecOptions{OnLine: func(l string) { lines = append(lines, l) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[0] != "hello" || lines[1] != "world" || lines[2] != "tail" {
		t.Fatalf("lines = %q", lines)
	}
	if res.Stdout != "hello\nworld\ntail" || res.ExitCode != 0 {
		t.Fatalf("res = %+v", res)
	}
	if got := fe.calls[0].command; got[0] != "sh" || got[1] != "-c" || got[2] != "echo hi" {
		t.Fatalf("command = %q", got)
	}
}

func TestExecExitCodeStderrAndCwd(t *testing.T) {
	fe := &fakeExecutor{script: map[string]scriptedExec{"cd /tmp && false": {stderr: "nope", exitCode: 3}}}
	h := newTestHandle(t, fe)
	res, err := h.Exec(context.Background(), "false", sandbox.ExecOptions{Cwd: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.Stderr != "nope" {
		t.Fatalf("res = %+v", res)
	}
}

func TestExecPipesStdinAndIgnoresSudo(t *testing.T) {
	fe := &fakeExecutor{script: map[string]scriptedExec{"cat": {stdout: "x"}}}
	h := newTestHandle(t, fe)
	in := "from host"
	if _, err := h.Exec(context.Background(), "cat", sandbox.ExecOptions{Stdin: &in, Sudo: true}); err != nil {
		t.Fatal(err)
	}
	if fe.calls[0].stdin != "from host" || fe.calls[0].command[0] != "sh" {
		t.Fatalf("call = %+v", fe.calls[0])
	}
}

func TestExecCancelledContext(t *testing.T) {
	fe := &fakeExecutor{script: map[string]scriptedExec{"sleep 9": {stdout: "a|b|c|d"}}}
	h := newTestHandle(t, fe)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := h.Exec(ctx, "sleep 9", sandbox.ExecOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestExecutorFactoryErrorIsStartError(t *testing.T) {
	cs := newRunningClientset()
	o := withDefaults(Options{Image: "img", Client: cs})
	o.Executor = func(_ k8sclient.Interface, _ *rest.Config, _, _ string, _ *corev1.PodExecOptions) (remotecommand.Executor, error) {
		return nil, errors.New("no transport")
	}
	h := &handle{client: cs, o: o, pod: "p"}
	_, err := h.Exec(context.Background(), "true", sandbox.ExecOptions{})
	var se *sandbox.StartError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v", err)
	}
	_ = time.Second
}
```

Add the imports the last test needs: `corev1 "k8s.io/api/core/v1"`, `k8sclient "k8s.io/client-go/kubernetes"`, `"k8s.io/client-go/rest"`, `"k8s.io/client-go/tools/remotecommand"`, and drop the `_ = time.Second` line and the `time` import.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./sandbox/kubernetes/ -run Exec`
Expected: FAIL, "undefined: handle".

- [ ] **Step 3: Write exec.go**

```go
package kubernetes

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/sandbox"
)

type handle struct {
	client k8sclient.Interface
	cfg    *rest.Config
	o      Options
	pod    string
}

func (h *handle) WorktreePath() string { return WorkDir }

func (h *handle) Close(ctx context.Context) error {
	return deletePod(ctx, h.client, h.o.Namespace, h.pod)
}

func (h *handle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) {
	if opts.Cwd != "" {
		command = "cd " + shellQuote(opts.Cwd) + " && " + command
	}
	execOpts := &corev1.PodExecOptions{
		Container: "agent",
		Command:   []string{"sh", "-c", command},
		Stdin:     opts.Stdin != nil,
		Stdout:    true,
		Stderr:    true,
	}
	exec, err := h.o.Executor(h.client, h.cfg, h.o.Namespace, h.pod, execOpts)
	if err != nil {
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
	}

	stdoutTail := stream.NewBoundedTail(stream.MaxTailChars, "\n")
	var stderr bytes.Buffer
	lw := stream.NewLineWriter(func(line string) {
		stdoutTail.Push(line)
		if opts.OnLine != nil {
			opts.OnLine(line)
		}
	})
	so := remotecommand.StreamOptions{Stdout: lw, Stderr: &stderr}
	if opts.Stdin != nil {
		so.Stdin = strings.NewReader(*opts.Stdin)
	}

	err = exec.StreamWithContext(ctx, so)
	lw.Flush()
	if ctx.Err() != nil {
		return sandbox.ExecResult{}, ctx.Err()
	}
	exitCode := 0
	if err != nil {
		var ce utilexec.CodeExitError
		if !errors.As(err, &ce) {
			return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
		}
		exitCode = ce.Code
	}
	stderrTail := stream.NewBoundedTail(stream.MaxTailChars, "")
	stderrTail.Push(stderr.String())
	return sandbox.ExecResult{Stdout: stdoutTail.String(), Stderr: stderrTail.String(), ExitCode: exitCode}, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// defaultExecutor opens a WebSocket exec stream and falls back to SPDY when
// the API server or a proxy does not support the upgrade.
func defaultExecutor(client k8sclient.Interface, cfg *rest.Config, namespace, pod string, opts *corev1.PodExecOptions) (remotecommand.Executor, error) {
	req := client.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(namespace).Name(pod).SubResource("exec").
		VersionedParams(opts, scheme.ParameterCodec)
	u := req.URL()
	ws, err := remotecommand.NewWebSocketExecutor(cfg, http.MethodPost, u.String())
	if err != nil {
		return nil, err
	}
	spdy, err := remotecommand.NewSPDYExecutor(cfg, http.MethodPost, u)
	if err != nil {
		return nil, err
	}
	return remotecommand.NewFallbackExecutor(ws, spdy, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
}

var _ = url.URL{}
```

Remove the `var _ = url.URL{}` line and the `net/url` import once the file compiles (they only guard against an unused import while editing). Verified signatures (pkg.go.dev, client-go v0.37.1 docs, same in v0.36): `NewWebSocketExecutor(config *rest.Config, method, url string)`, `NewSPDYExecutor(config *rest.Config, method string, url *url.URL)`, `NewFallbackExecutor(primary, secondary Executor, shouldFallback func(error) bool)`, `Executor.StreamWithContext(ctx, StreamOptions{Stdin, Stdout, Stderr, Tty, TerminalSizeQueue})`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./sandbox/kubernetes/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sandbox/kubernetes/
git commit -m "Add exec over the Kubernetes exec subresource"
```

---

### Task 4: CopyIn and CopyFileOut

**Files:**
- Create: `sandbox/kubernetes/copy.go`, `sandbox/kubernetes/copy_test.go`

**Interfaces:**
- Produces: `(*handle).CopyIn(ctx, hostPath, sandboxPath string) error` (file or directory; streams a tar of `hostPath` to `mkdir -p <dir> && tar -xf - -C <dir>`), `(*handle).CopyFileOut(ctx, sandboxPath, hostPath string) error` (runs `cat <sandboxPath>` and writes stdout to `hostPath`, creating parent directories), and `tarPath(w io.Writer, hostPath, name string) error` (pure helper, tested on its own).
- For a file, the tar contains one entry named `filepath.Base(sandboxPath)` and is extracted in `filepath.Dir(sandboxPath)`. For a directory, entries are rooted at `filepath.Base(sandboxPath)` and extracted in `filepath.Dir(sandboxPath)`. Symlinks are stored as symlinks, empty directories as directory entries.
- `CopyFileOut` uses an exec with a raw stdout writer, not `OnLine`, so binary files survive. Add a private `execRaw(ctx, command string, stdin io.Reader, stdout io.Writer) (exitCode int, stderr string, err error)` in `copy.go` that both copy functions use, built from the same `Executor` factory as `Exec`.

- [ ] **Step 1: Write the failing tests**

`sandbox/kubernetes/copy_test.go`:
```go
package kubernetes

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTarPathReproducesTreeWithSymlinksAndEmptyDirs(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	os.MkdirAll(filepath.Join(src, "empty"), 0o755)
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "sub", "a.txt"), []byte("A"), 0o644)
	os.Symlink("sub/a.txt", filepath.Join(src, "link"))

	var buf bytes.Buffer
	if err := tarPath(&buf, src, "dest"); err != nil {
		t.Fatal(err)
	}
	entries := map[string]*tar.Header{}
	tr := tar.NewReader(&buf)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		entries[h.Name] = h
	}
	if entries["dest/empty"] == nil || entries["dest/empty"].Typeflag != tar.TypeDir {
		t.Fatalf("empty dir missing: %v", keys(entries))
	}
	if entries["dest/sub/a.txt"] == nil || entries["dest/sub/a.txt"].Size != 1 {
		t.Fatalf("file missing: %v", keys(entries))
	}
	if l := entries["dest/link"]; l == nil || l.Typeflag != tar.TypeSymlink || l.Linkname != "sub/a.txt" {
		t.Fatalf("symlink wrong: %+v", l)
	}
}

func TestTarPathSingleFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "only.txt")
	os.WriteFile(f, []byte("x"), 0o644)
	var buf bytes.Buffer
	if err := tarPath(&buf, f, "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	h, err := tar.NewReader(&buf).Next()
	if err != nil || h.Name != "renamed.txt" || h.Size != 1 {
		t.Fatalf("h = %+v, %v", h, err)
	}
}

func TestCopyInSendsTarToExtractCommand(t *testing.T) {
	fe := &fakeExecutor{script: map[string]scriptedExec{}}
	h := newTestHandle(t, fe)
	f := filepath.Join(t.TempDir(), "node_modules")
	os.MkdirAll(f, 0o755)
	if err := h.CopyIn(context.Background(), f, "/home/agent/workspace/node_modules"); err != nil {
		t.Fatal(err)
	}
	call := fe.calls[len(fe.calls)-1]
	if call.command[2] != "mkdir -p '/home/agent/workspace' && tar -xf - -C '/home/agent/workspace'" {
		t.Fatalf("command = %q", call.command[2])
	}
	hdr, err := tar.NewReader(strings.NewReader(call.stdin)).Next()
	if err != nil || hdr.Name != "node_modules" {
		t.Fatalf("hdr = %+v, %v", hdr, err)
	}
}

func TestCopyInFailsOnNonZeroExit(t *testing.T) {
	fe := &fakeExecutor{script: map[string]scriptedExec{"mkdir -p '/x' && tar -xf - -C '/x'": {stderr: "read-only", exitCode: 2}}}
	h := newTestHandle(t, fe)
	f := filepath.Join(t.TempDir(), "a")
	os.WriteFile(f, []byte("1"), 0o644)
	err := h.CopyIn(context.Background(), f, "/x/a")
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("err = %v", err)
	}
}

func TestCopyFileOutWritesStdoutToHost(t *testing.T) {
	fe := &fakeExecutor{script: map[string]scriptedExec{"cat '/home/agent/out.bin'": {stdout: "bin|ary\x00data"}}}
	h := newTestHandle(t, fe)
	dest := filepath.Join(t.TempDir(), "nested", "out.bin")
	if err := h.CopyFileOut(context.Background(), "/home/agent/out.bin", dest); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dest)
	if string(b) != "binary\x00data" {
		t.Fatalf("got %q", b)
	}
}

func keys(m map[string]*tar.Header) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./sandbox/kubernetes/ -run 'Tar|Copy'`
Expected: FAIL, "undefined: tarPath".

- [ ] **Step 3: Write copy.go**

```go
package kubernetes

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/e11en/agentgopher/sandbox"
)

// tarPath writes hostPath (file or directory) to w as a tar stream whose
// entries are rooted at name.
func tarPath(w io.Writer, hostPath, name string) error {
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(hostPath, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(hostPath, p)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(filepath.Join(name, rel))
		if info.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return err
	}
	return tw.Close()
}

// execRaw runs command with raw stdin and stdout streams.
func (h *handle) execRaw(ctx context.Context, command string, stdin io.Reader, stdout io.Writer) (int, string, error) {
	execOpts := &corev1.PodExecOptions{Container: "agent", Command: []string{"sh", "-c", command}, Stdin: stdin != nil, Stdout: true, Stderr: true}
	exec, err := h.o.Executor(h.client, h.cfg, h.o.Namespace, h.pod, execOpts)
	if err != nil {
		return 0, "", &sandbox.StartError{Command: command, Cause: err}
	}
	var stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: stdin, Stdout: stdout, Stderr: &stderr})
	if ctx.Err() != nil {
		return 0, "", ctx.Err()
	}
	if err != nil {
		var ce utilexec.CodeExitError
		if !errors.As(err, &ce) {
			return 0, "", &sandbox.StartError{Command: command, Cause: err}
		}
		return ce.Code, stderr.String(), nil
	}
	return 0, stderr.String(), nil
}

// CopyIn copies a host file or directory to sandboxPath inside the Pod.
func (h *handle) CopyIn(ctx context.Context, hostPath, sandboxPath string) error {
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(tarPath(pw, hostPath, filepath.Base(sandboxPath))) }()
	dir := shellQuote(filepath.Dir(sandboxPath))
	code, stderr, err := h.execRaw(ctx, "mkdir -p "+dir+" && tar -xf - -C "+dir, pr, io.Discard)
	pr.Close()
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("copy %s into sandbox: tar exited %d: %s", hostPath, code, stderr)
	}
	return nil
}

// CopyFileOut copies one file from the Pod to hostPath.
func (h *handle) CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error {
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(hostPath)
	if err != nil {
		return err
	}
	defer f.Close()
	code, stderr, err := h.execRaw(ctx, "cat "+shellQuote(sandboxPath), nil, f)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("copy %s out of sandbox: cat exited %d: %s", sandboxPath, code, stderr)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./sandbox/kubernetes/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sandbox/kubernetes/
git commit -m "Add tar-based file transfer for Kubernetes sandboxes"
```

---

### Task 5: Provider constructor and manual cluster smoke test

**Files:**
- Create: `sandbox/kubernetes/kubernetes.go`, `sandbox/kubernetes/kubernetes_test.go`
- Modify: `README.md` (one line in the provider table)

**Interfaces:**
- Consumes: `sandbox.NewIsolatedProvider`, `sandbox.IsolatedProviderConfig`, `sandbox.IsolatedCreateOptions` (phase 4).
- Produces: `kubernetes.New(opts *Options) sandbox.IsolatedProvider` with `Name() == "kubernetes"`. `Create` merges `IsolatedCreateOptions.Env` over `Options.Env`, creates the Pod and returns the handle.

- [ ] **Step 1: Write the failing tests**

`sandbox/kubernetes/kubernetes_test.go`:
```go
package kubernetes

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/e11en/agentgopher/sandbox"
)

func TestNewCreatesPodAndCloseDeletesIt(t *testing.T) {
	cs := newRunningClientset()
	fe := &fakeExecutor{script: map[string]scriptedExec{"echo ok": {stdout: "ok\n"}}}
	p := New(&Options{Image: "img", Client: cs, Executor: fe.factory(), Env: map[string]string{"A": "provider"}})
	if p.Kind() != sandbox.KindIsolated || p.Name() != "kubernetes" {
		t.Fatalf("kind=%q name=%q", p.Kind(), p.Name())
	}
	h, err := p.Create(context.Background(), sandbox.IsolatedCreateOptions{Env: map[string]string{"A": "run", "B": "2"}})
	if err != nil {
		t.Fatal(err)
	}
	if h.WorktreePath() != WorkDir {
		t.Fatalf("WorktreePath = %q", h.WorktreePath())
	}
	pods, _ := cs.CoreV1().Pods("agentgopher").List(context.Background(), metav1.ListOptions{})
	if len(pods.Items) != 1 {
		t.Fatalf("pods = %d", len(pods.Items))
	}
	env := map[string]string{}
	for _, e := range pods.Items[0].Spec.Containers[0].Env {
		env[e.Name] = e.Value
	}
	if env["A"] != "run" || env["B"] != "2" {
		t.Fatalf("env = %v", env)
	}
	res, err := h.Exec(context.Background(), "echo ok", sandbox.ExecOptions{})
	if err != nil || res.Stdout != "ok" {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	pods, _ = cs.CoreV1().Pods("agentgopher").List(context.Background(), metav1.ListOptions{})
	if len(pods.Items) != 0 {
		t.Fatal("pod must be deleted on Close")
	}
}

func TestNewWithoutImageFailsOnCreate(t *testing.T) {
	p := New(&Options{Client: newRunningClientset()})
	if _, err := p.Create(context.Background(), sandbox.IsolatedCreateOptions{}); err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./sandbox/kubernetes/ -run New`
Expected: FAIL, "undefined: New".

- [ ] **Step 3: Write kubernetes.go**

```go
package kubernetes

import (
	"context"

	"github.com/e11en/agentgopher/sandbox"
)

// New returns an isolated sandbox provider that runs each sandbox as a Pod.
func New(opts *Options) sandbox.IsolatedProvider {
	var o Options
	if opts != nil {
		o = *opts
	}
	o = withDefaults(o)
	return sandbox.NewIsolatedProvider(sandbox.IsolatedProviderConfig{
		Name: "kubernetes",
		Env:  o.Env,
		Create: func(ctx context.Context, co sandbox.IsolatedCreateOptions) (sandbox.IsolatedHandle, error) {
			client, cfg, err := loadClient(o)
			if err != nil {
				return nil, err
			}
			env := map[string]string{}
			for k, v := range o.Env {
				env[k] = v
			}
			for k, v := range co.Env {
				env[k] = v
			}
			pod, err := createPod(ctx, client, o, newPodName(), env)
			if err != nil {
				return nil, err
			}
			return &handle{client: client, cfg: cfg, o: o, pod: pod.Name}, nil
		},
	})
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./sandbox/kubernetes/ && go vet ./... && golangci-lint run ./...`
Expected: PASS, no lint output.

- [ ] **Step 5: Manual smoke test against a real cluster (skip in CI)**

Only with a reachable cluster in the current kubeconfig (the Task 12 cluster, or any cluster with `kubectl auth can-i create pods -n agentgopher` answering yes):
```bash
kubectl create namespace agentgopher --dry-run=client -o yaml | kubectl apply -f -
cat > /tmp/k8s-smoke.go <<'GO'
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/kubernetes"
)

func main() {
	p := kubernetes.New(&kubernetes.Options{Image: "debian:bookworm-slim"})
	h, err := p.Create(context.Background(), sandbox.IsolatedCreateOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer h.Close(context.Background())
	res, err := h.Exec(context.Background(), "uname -a; id", sandbox.ExecOptions{OnLine: func(l string) { fmt.Println("line:", l) }})
	fmt.Printf("exit=%d err=%v\n", res.ExitCode, err)
}
GO
mkdir -p /tmp/k8s-smoke && mv /tmp/k8s-smoke.go /tmp/k8s-smoke/main.go
go run /tmp/k8s-smoke/main.go
kubectl get pods -n agentgopher
```
Expected: two `line:` entries, `exit=0 err=<nil>`, and no leftover Pod. Record the outcome (or "skipped, no cluster") in the Step 6 commit message body.

- [ ] **Step 6: Update README and commit**

Add to the sandbox provider table in `README.md`:
```markdown
| Kubernetes | `github.com/e11en/agentgopher/sandbox/kubernetes` | Isolated | one Pod per sandbox; needs an image with git and the agent CLI |
```

```bash
git add sandbox/kubernetes/ README.md
git commit -m "Add Kubernetes isolated sandbox provider"
```

---

### Task 6: `remote` package: RunSpec and Job submission

**Files:**
- Create: `remote/spec.go`, `remote/job.go`, `remote/job_test.go`

**Interfaces:**
- Produces:
  ```go
  package remote
  const (
      DefaultNamespace      = "agentgopher"
      DefaultServiceAccount = "agentgopher-runner"
      DefaultBranchPrefix   = "agentgopher/"
      DefaultBaseBranch     = "main"
      DefaultTimeout        = 2 * time.Hour
      SecretsMountPath      = "/var/secrets"
      SecretProviderClass   = "agentgopher-secrets"
      LabelRun              = "agentgopher.dev/run"
  )
  type RunSpec struct {
      RepoURL        string            // required, https clone URL
      Script         string            // path inside the repo to `go run`; default ".agentgopher"
      Branch         string            // full branch name; default BranchPrefix + timestamp, set by Defaults
      BranchPrefix   string            // default "agentgopher/"
      BaseBranch     string            // default "main"
      Namespace      string
      Image          string            // required
      ServiceAccount string
      Timeout        time.Duration     // Job activeDeadlineSeconds; default 2h
      Env            map[string]string // extra env for the Job container
  }
  func (s RunSpec) Defaults(now time.Time) RunSpec   // fills defaults, Branch = BranchPrefix + now.UTC().Format("20060102-150405")
  func (s RunSpec) Validate() error                 // RepoURL and Image required, Branch non-empty
  func JobName(now time.Time) string                // "agentgopher-run-20060102-150405-<4 hex>"
  func BuildJob(s RunSpec, name string) *batchv1.Job
  func Submit(ctx, client kubernetes.Interface, s RunSpec) (jobName string, err error)
  ```
- Job: `BackoffLimit 0`, `TTLSecondsAfterFinished 86400`, `ActiveDeadlineSeconds` from Timeout, Pod `RestartPolicy Never`, `ServiceAccountName`, label `LabelRun: <jobName>` on Job and Pod template, container `runner` with `Image`, command `["agentgopher","remote","entrypoint"]`, env `AGENTGOPHER_REPO_URL`, `AGENTGOPHER_SCRIPT`, `AGENTGOPHER_BRANCH`, `AGENTGOPHER_BASE_BRANCH`, `AGENTGOPHER_SECRETS_DIR=/var/secrets`, plus `Env` sorted; a CSI volume `secrets` (`driver: secrets-store-gke.csi.k8s.io`, `readOnly: true`, `volumeAttributes: {secretProviderClass: agentgopher-secrets}`) mounted read-only at `/var/secrets`; an `emptyDir` volume `work` mounted at `/work`.

- [ ] **Step 1: Write the failing tests**

`remote/job_test.go`:
```go
package remote

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

var fixedNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func TestDefaultsAndValidate(t *testing.T) {
	s := RunSpec{RepoURL: "https://github.com/e11en/demo.git", Image: "img"}.Defaults(fixedNow)
	if s.Namespace != "agentgopher" || s.ServiceAccount != "agentgopher-runner" || s.Script != ".agentgopher" || s.BaseBranch != "main" || s.Timeout != 2*time.Hour {
		t.Fatalf("spec = %+v", s)
	}
	if s.Branch != "agentgopher/20261015-120000" {
		t.Fatalf("Branch = %q", s.Branch)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (RunSpec{Image: "img"}.Defaults(fixedNow)).Validate(); err == nil || !strings.Contains(err.Error(), "RepoURL") {
		t.Fatalf("err = %v", err)
	}
	if err := (RunSpec{RepoURL: "x"}.Defaults(fixedNow)).Validate(); err == nil || !strings.Contains(err.Error(), "Image") {
		t.Fatalf("err = %v", err)
	}
	custom := RunSpec{RepoURL: "x", Image: "i", Branch: "feature/keep"}.Defaults(fixedNow)
	if custom.Branch != "feature/keep" {
		t.Fatalf("explicit branch overwritten: %q", custom.Branch)
	}
}

func TestJobName(t *testing.T) {
	n := JobName(fixedNow)
	if !strings.HasPrefix(n, "agentgopher-run-20261015-120000-") || len(n) != len("agentgopher-run-20261015-120000-")+4 {
		t.Fatalf("name = %q", n)
	}
}

func TestBuildJobShape(t *testing.T) {
	s := RunSpec{RepoURL: "https://github.com/e11en/demo.git", Image: "img:1", Env: map[string]string{"ZZ": "1", "AA": "2"}}.Defaults(fixedNow)
	job := BuildJob(s, "agentgopher-run-x")
	if job.Name != "agentgopher-run-x" || job.Namespace != "agentgopher" || *job.Spec.BackoffLimit != 0 || *job.Spec.TTLSecondsAfterFinished != 86400 || *job.Spec.ActiveDeadlineSeconds != 7200 {
		t.Fatalf("job = %+v", job.Spec)
	}
	pt := job.Spec.Template
	if pt.Labels[LabelRun] != "agentgopher-run-x" || job.Labels[LabelRun] != "agentgopher-run-x" || pt.Spec.ServiceAccountName != "agentgopher-runner" {
		t.Fatalf("labels/sa = %v %v %q", job.Labels, pt.Labels, pt.Spec.ServiceAccountName)
	}
	c := pt.Spec.Containers[0]
	if c.Name != "runner" || c.Image != "img:1" || strings.Join(c.Command, " ") != "agentgopher remote entrypoint" {
		t.Fatalf("container = %+v", c)
	}
	env := map[string]string{}
	var order []string
	for _, e := range c.Env {
		env[e.Name] = e.Value
		order = append(order, e.Name)
	}
	if env["AGENTGOPHER_REPO_URL"] != s.RepoURL || env["AGENTGOPHER_SCRIPT"] != ".agentgopher" || env["AGENTGOPHER_BRANCH"] != s.Branch || env["AGENTGOPHER_BASE_BRANCH"] != "main" || env["AGENTGOPHER_SECRETS_DIR"] != "/var/secrets" {
		t.Fatalf("env = %v", env)
	}
	if order[len(order)-2] != "AA" || order[len(order)-1] != "ZZ" {
		t.Fatalf("extra env not sorted: %v", order)
	}
	var csi, work bool
	for _, v := range pt.Spec.Volumes {
		if v.CSI != nil && v.CSI.Driver == "secrets-store-gke.csi.k8s.io" && v.CSI.VolumeAttributes["secretProviderClass"] == "agentgopher-secrets" {
			csi = true
		}
		if v.Name == "work" && v.EmptyDir != nil {
			work = true
		}
	}
	if !csi || !work {
		t.Fatalf("volumes = %+v", pt.Spec.Volumes)
	}
	var mounted bool
	for _, m := range c.VolumeMounts {
		if m.Name == "secrets" && m.MountPath == "/var/secrets" && m.ReadOnly {
			mounted = true
		}
	}
	if !mounted {
		t.Fatalf("mounts = %+v", c.VolumeMounts)
	}
}

func TestSubmitCreatesJob(t *testing.T) {
	cs := fake.NewClientset()
	s := RunSpec{RepoURL: "https://x", Image: "img"}.Defaults(fixedNow)
	name, err := Submit(context.Background(), cs, s)
	if err != nil || !strings.HasPrefix(name, "agentgopher-run-") {
		t.Fatalf("name=%q err=%v", name, err)
	}
	if _, err := cs.BatchV1().Jobs("agentgopher").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Submit(context.Background(), cs, RunSpec{}); err == nil {
		t.Fatal("expected validation error")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./remote/`
Expected: FAIL, "undefined: RunSpec".

- [ ] **Step 3: Write spec.go**

```go
// Package remote runs agentgopher as a Kubernetes Job and follows it.
package remote

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

const (
	DefaultNamespace      = "agentgopher"
	DefaultServiceAccount = "agentgopher-runner"
	DefaultBranchPrefix   = "agentgopher/"
	DefaultBaseBranch     = "main"
	DefaultScript         = ".agentgopher"
	DefaultTimeout        = 2 * time.Hour
	SecretsMountPath      = "/var/secrets"
	SecretProviderClass   = "agentgopher-secrets"
	// LabelRun carries the Job name on the Job and its Pod.
	LabelRun = "agentgopher.dev/run"
)

// RunSpec describes one remote run.
type RunSpec struct {
	RepoURL        string
	Script         string
	Branch         string
	BranchPrefix   string
	BaseBranch     string
	Namespace      string
	Image          string
	ServiceAccount string
	Timeout        time.Duration
	Env            map[string]string
}

// Defaults fills unset fields. Branch becomes BranchPrefix plus a UTC timestamp.
func (s RunSpec) Defaults(now time.Time) RunSpec {
	if s.Script == "" {
		s.Script = DefaultScript
	}
	if s.BranchPrefix == "" {
		s.BranchPrefix = DefaultBranchPrefix
	}
	if s.Branch == "" {
		s.Branch = s.BranchPrefix + now.UTC().Format("20060102-150405")
	}
	if s.BaseBranch == "" {
		s.BaseBranch = DefaultBaseBranch
	}
	if s.Namespace == "" {
		s.Namespace = DefaultNamespace
	}
	if s.ServiceAccount == "" {
		s.ServiceAccount = DefaultServiceAccount
	}
	if s.Timeout == 0 {
		s.Timeout = DefaultTimeout
	}
	return s
}

// Validate checks the required fields.
func (s RunSpec) Validate() error {
	switch {
	case s.RepoURL == "":
		return errors.New("remote: RepoURL is required")
	case s.Image == "":
		return errors.New("remote: Image is required")
	case s.Branch == "":
		return errors.New("remote: Branch is required")
	}
	return nil
}

// JobName returns a unique Job name for now.
func JobName(now time.Time) string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return "agentgopher-run-" + now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}
```

- [ ] **Step 4: Write job.go**

```go
package remote

import (
	"context"
	"fmt"
	"sort"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// BuildJob returns the Job object for s.
func BuildJob(s RunSpec, name string) *batchv1.Job {
	env := []corev1.EnvVar{
		{Name: "AGENTGOPHER_REPO_URL", Value: s.RepoURL},
		{Name: "AGENTGOPHER_SCRIPT", Value: s.Script},
		{Name: "AGENTGOPHER_BRANCH", Value: s.Branch},
		{Name: "AGENTGOPHER_BASE_BRANCH", Value: s.BaseBranch},
		{Name: "AGENTGOPHER_SECRETS_DIR", Value: SecretsMountPath},
	}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, corev1.EnvVar{Name: k, Value: s.Env[k]})
	}
	backoff := int32(0)
	ttl := int32(86400)
	deadline := int64(s.Timeout.Seconds())
	readOnly := true
	labels := map[string]string{LabelRun: name, "app.kubernetes.io/name": "agentgopher", "app.kubernetes.io/component": "runner"}
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: s.Namespace, Labels: labels},
		Spec: batchv1.JobSpec{
			BackoffLimit:            &backoff,
			TTLSecondsAfterFinished: &ttl,
			ActiveDeadlineSeconds:   &deadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: s.ServiceAccount,
					Containers: []corev1.Container{{
						Name:    "runner",
						Image:   s.Image,
						Command: []string{"agentgopher", "remote", "entrypoint"},
						Env:     env,
						VolumeMounts: []corev1.VolumeMount{
							{Name: "secrets", MountPath: SecretsMountPath, ReadOnly: true},
							{Name: "work", MountPath: "/work"},
						},
					}},
					Volumes: []corev1.Volume{
						{Name: "secrets", VolumeSource: corev1.VolumeSource{CSI: &corev1.CSIVolumeSource{
							Driver:           "secrets-store-gke.csi.k8s.io",
							ReadOnly:         &readOnly,
							VolumeAttributes: map[string]string{"secretProviderClass": SecretProviderClass},
						}}},
						{Name: "work", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}
}

// Submit validates s and creates its Job. It returns the Job name.
func Submit(ctx context.Context, client kubernetes.Interface, s RunSpec) (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	name := JobName(time.Now())
	if _, err := client.BatchV1().Jobs(s.Namespace).Create(ctx, BuildJob(s, name), metav1.CreateOptions{}); err != nil {
		return "", fmt.Errorf("create job: %w", err)
	}
	return name, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./remote/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add remote/
git commit -m "Add remote run spec and Job submission"
```

---

### Task 7: `remote` package: log streaming and completion wait

**Files:**
- Create: `remote/logs.go`, `remote/logs_test.go`

**Interfaces:**
- Produces:
  ```go
  type Outcome struct { Succeeded bool; Reason string; Message string }
  func StreamLogs(ctx, client kubernetes.Interface, namespace, jobName string, w io.Writer) error
  func Wait(ctx, client kubernetes.Interface, namespace, jobName string) (Outcome, error)
  var pollInterval = 2 * time.Second   // tests shorten it
  ```
- `StreamLogs` finds the Job's Pod by label `LabelRun=<jobName>`, waits until it is Running or finished (polling), then copies `GetLogs(pod, &PodLogOptions{Container: "runner", Follow: true}).Stream(ctx)` to `w`. Returns nil when the stream ends, `ctx.Err()` on cancellation, and an error when no Pod appears within the context deadline. `Wait` polls the Job until a `Complete` or `Failed` condition is `True`.

- [ ] **Step 1: Write the failing tests**

`remote/logs_test.go`:
```go
package remote

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestStreamLogsCopiesPodLogs(t *testing.T) {
	cs := fake.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "job-x-abc", Namespace: "agentgopher", Labels: map[string]string{LabelRun: "job-x"}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	})
	old := pollInterval
	pollInterval = 10 * time.Millisecond
	defer func() { pollInterval = old }()
	var buf bytes.Buffer
	if err := StreamLogs(context.Background(), cs, "agentgopher", "job-x", &buf); err != nil {
		t.Fatal(err)
	}
	// The fake clientset serves the literal body "fake logs".
	if buf.String() != "fake logs" {
		t.Fatalf("got %q", buf.String())
	}
}

func TestStreamLogsTimesOutWithoutPod(t *testing.T) {
	old := pollInterval
	pollInterval = 10 * time.Millisecond
	defer func() { pollInterval = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := StreamLogs(ctx, fake.NewClientset(), "agentgopher", "job-none", &bytes.Buffer{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestWaitReturnsOutcome(t *testing.T) {
	old := pollInterval
	pollInterval = 10 * time.Millisecond
	defer func() { pollInterval = old }()
	cs := fake.NewClientset(&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "j", Namespace: "agentgopher"}})
	go func() {
		time.Sleep(30 * time.Millisecond)
		j, _ := cs.BatchV1().Jobs("agentgopher").Get(context.Background(), "j", metav1.GetOptions{})
		j.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "DeadlineExceeded", Message: "too slow"}}
		cs.BatchV1().Jobs("agentgopher").UpdateStatus(context.Background(), j, metav1.UpdateOptions{})
	}()
	out, err := Wait(context.Background(), cs, "agentgopher", "j")
	if err != nil || out.Succeeded || out.Reason != "DeadlineExceeded" || out.Message != "too slow" {
		t.Fatalf("out = %+v, %v", out, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./remote/ -run 'Stream|Wait'`
Expected: FAIL, "undefined: StreamLogs".

- [ ] **Step 3: Write logs.go**

```go
package remote

import (
	"context"
	"fmt"
	"io"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var pollInterval = 2 * time.Second

// Outcome is the final state of a Job.
type Outcome struct {
	Succeeded bool
	Reason    string
	Message   string
}

func sleep(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(pollInterval):
		return nil
	}
}

// findPod returns the Job's Pod once it is Running or finished.
func findPod(ctx context.Context, client kubernetes.Interface, namespace, jobName string) (*corev1.Pod, error) {
	for {
		list, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: LabelRun + "=" + jobName})
		if err != nil {
			return nil, err
		}
		for i := range list.Items {
			p := &list.Items[i]
			switch p.Status.Phase {
			case corev1.PodRunning, corev1.PodSucceeded, corev1.PodFailed:
				return p, nil
			}
		}
		if err := sleep(ctx); err != nil {
			return nil, fmt.Errorf("waiting for pod of job %s: %w", jobName, err)
		}
	}
}

// StreamLogs follows the runner container's log until it ends.
func StreamLogs(ctx context.Context, client kubernetes.Interface, namespace, jobName string, w io.Writer) error {
	pod, err := findPod(ctx, client, namespace, jobName)
	if err != nil {
		return err
	}
	rc, err := client.CoreV1().Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: "runner", Follow: true}).Stream(ctx)
	if err != nil {
		return fmt.Errorf("stream logs of %s: %w", pod.Name, err)
	}
	defer rc.Close()
	_, err = io.Copy(w, rc)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// Wait polls the Job until it completes or fails.
func Wait(ctx context.Context, client kubernetes.Interface, namespace, jobName string) (Outcome, error) {
	for {
		job, err := client.BatchV1().Jobs(namespace).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			return Outcome{}, err
		}
		for _, c := range job.Status.Conditions {
			if c.Status != corev1.ConditionTrue {
				continue
			}
			switch c.Type {
			case batchv1.JobComplete:
				return Outcome{Succeeded: true, Reason: c.Reason, Message: c.Message}, nil
			case batchv1.JobFailed:
				return Outcome{Succeeded: false, Reason: c.Reason, Message: c.Message}, nil
			}
		}
		if err := sleep(ctx); err != nil {
			return Outcome{}, err
		}
	}
}
```

Verified on pkg.go.dev (client-go v0.37.1, same in v0.36): `PodInterface.GetLogs(name string, opts *v1.PodLogOptions) *rest.Request` and `(*rest.Request).Stream(ctx) (io.ReadCloser, error)`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./remote/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add remote/
git commit -m "Add remote log streaming and Job wait"
```

---

### Task 8: `remote` package: in-cluster entrypoint

The entrypoint runs inside the Job: load secrets from files into the environment, clone the repo, run the script, push the branch when it has commits, open a PR, print `PR: <url>`.

**Files:**
- Create: `remote/entrypoint.go`, `remote/entrypoint_test.go`

**Interfaces:**
- Produces:
  ```go
  type EntrypointConfig struct {
      RepoURL, Script, Branch, BaseBranch, SecretsDir, WorkDir string
      Stdout io.Writer
  }
  func ConfigFromEnv() (EntrypointConfig, error)       // AGENTGOPHER_* vars; WorkDir defaults to /work/repo; SecretsDir may be empty
  func LoadSecrets(dir string) (map[string]string, error) // each regular file <dir>/<NAME> becomes NAME=<trimmed content>; missing dir is fine
  func Entrypoint(ctx context.Context) error            // ConfigFromEnv + run
  func run(ctx context.Context, cfg EntrypointConfig) error
  ```
- Steps of `run`, each a shell-free `exec.CommandContext` with `Dir` set and stdout/stderr forwarded to `cfg.Stdout`:
  1. `LoadSecrets` and `os.Setenv` each (secrets win over existing env).
  2. `gh auth setup-git` (uses `GH_TOKEN`; skipped when `GH_TOKEN` is empty).
  3. `git clone --branch <BaseBranch> <RepoURL> <WorkDir>`, then `git -C <WorkDir> checkout -b <Branch>`, and `git config user.name agentgopher` plus `user.email agentgopher@users.noreply.github.com` when unset.
  4. `go run ./<Script>` in `WorkDir` with env `AGENTGOPHER_BRANCH`, `AGENTGOPHER_BASE_BRANCH` set (the init templates pass them as the named branch strategy).
  5. `git rev-list <BaseBranch>..<Branch> --count`; when 0 print `No commits produced, no PR` and return nil.
  6. `git push -u origin <Branch>`.
  7. `gh pr create --base <BaseBranch> --head <Branch> --title "agentgopher: <Branch>" --body "Opened by agentgopher remote run."`; the URL on its stdout is printed as `PR: <url>`.
- Tests shim `git`, `gh` and `go` with shell scripts placed first on `PATH`, recording their arguments to a file.

- [ ] **Step 1: Write the failing tests**

`remote/entrypoint_test.go`:
```go
package remote

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shimTools puts fake git, gh and go on PATH. Each appends its argv to
// <dir>/calls and behaves per the given script body.
func shimTools(t *testing.T, revListCount string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\necho \""+name+" $*\" >> "+filepath.Join(dir, "calls")+"\n"+body), 0o755)
	}
	write("git", `case "$1 $2" in
  "rev-list "*) echo `+revListCount+` ;;
  "config user.name") exit 1 ;;
  "config user.email") exit 1 ;;
esac
exit 0`)
	write("gh", `case "$1 $2" in
  "pr create") echo https://github.com/e11en/demo/pull/7 ;;
esac
exit 0`)
	write("go", `exit 0`)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return dir
}

func calls(t *testing.T, dir string) string {
	b, _ := os.ReadFile(filepath.Join(dir, "calls"))
	return string(b)
}

func TestLoadSecretsReadsFilesAndIgnoresMissingDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "GH_TOKEN"), []byte("tok\n"), 0o600)
	os.MkdirAll(filepath.Join(dir, "..data"), 0o755)
	got, err := LoadSecrets(dir)
	if err != nil || len(got) != 1 || got["GH_TOKEN"] != "tok" {
		t.Fatalf("got %v, %v", got, err)
	}
	if got, err := LoadSecrets(filepath.Join(dir, "nope")); err != nil || len(got) != 0 {
		t.Fatalf("missing dir: %v, %v", got, err)
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("AGENTGOPHER_REPO_URL", "https://x/y.git")
	t.Setenv("AGENTGOPHER_SCRIPT", ".agentgopher")
	t.Setenv("AGENTGOPHER_BRANCH", "agentgopher/1")
	t.Setenv("AGENTGOPHER_BASE_BRANCH", "main")
	cfg, err := ConfigFromEnv()
	if err != nil || cfg.WorkDir != "/work/repo" || cfg.Branch != "agentgopher/1" {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
	t.Setenv("AGENTGOPHER_REPO_URL", "")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error without repo url")
	}
}

func TestRunPushesAndOpensPR(t *testing.T) {
	dir := shimTools(t, "2")
	t.Setenv("GH_TOKEN", "tok")
	var out bytes.Buffer
	cfg := EntrypointConfig{RepoURL: "https://x/y.git", Script: ".agentgopher", Branch: "agentgopher/1", BaseBranch: "main", WorkDir: filepath.Join(t.TempDir(), "repo"), Stdout: &out}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	c := calls(t, dir)
	for _, want := range []string{
		"gh auth setup-git",
		"git clone --branch main https://x/y.git " + cfg.WorkDir,
		"git checkout -b agentgopher/1",
		"go run ./.agentgopher",
		"git push -u origin agentgopher/1",
		"gh pr create --base main --head agentgopher/1",
	} {
		if !strings.Contains(c, want) {
			t.Fatalf("missing %q in calls:\n%s", want, c)
		}
	}
	if !strings.Contains(out.String(), "PR: https://github.com/e11en/demo/pull/7\n") {
		t.Fatalf("stdout:\n%s", out.String())
	}
}

func TestRunWithoutCommitsSkipsPushAndPR(t *testing.T) {
	dir := shimTools(t, "0")
	var out bytes.Buffer
	cfg := EntrypointConfig{RepoURL: "https://x/y.git", Script: ".agentgopher", Branch: "agentgopher/1", BaseBranch: "main", WorkDir: filepath.Join(t.TempDir(), "repo"), Stdout: &out}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	c := calls(t, dir)
	if strings.Contains(c, "git push") || strings.Contains(c, "pr create") {
		t.Fatalf("must not push or open PR:\n%s", c)
	}
	if !strings.Contains(out.String(), "No commits produced, no PR") {
		t.Fatalf("stdout:\n%s", out.String())
	}
}

func TestRunSecretsOverrideEnvAndScriptSeesBranch(t *testing.T) {
	dir := shimTools(t, "0")
	secrets := t.TempDir()
	os.WriteFile(filepath.Join(secrets, "GH_TOKEN"), []byte("from-secret"), 0o600)
	t.Setenv("GH_TOKEN", "stale")
	os.WriteFile(filepath.Join(dir, "go"), []byte("#!/bin/sh\necho \"go $* GH_TOKEN=$GH_TOKEN BRANCH=$AGENTGOPHER_BRANCH\" >> "+filepath.Join(dir, "calls")+"\nexit 0"), 0o755)
	cfg := EntrypointConfig{RepoURL: "u", Script: "s", Branch: "b", BaseBranch: "main", SecretsDir: secrets, WorkDir: filepath.Join(t.TempDir(), "repo"), Stdout: &bytes.Buffer{}}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls(t, dir), "GH_TOKEN=from-secret BRANCH=b") {
		t.Fatalf("calls:\n%s", calls(t, dir))
	}
}

func TestRunFailingScriptReturnsError(t *testing.T) {
	dir := shimTools(t, "0")
	os.WriteFile(filepath.Join(dir, "go"), []byte("#!/bin/sh\necho boom 1>&2\nexit 5"), 0o755)
	cfg := EntrypointConfig{RepoURL: "u", Script: "s", Branch: "b", BaseBranch: "main", WorkDir: filepath.Join(t.TempDir(), "repo"), Stdout: &bytes.Buffer{}}
	err := run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "go run") {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./remote/ -run 'Secrets|Config|Run'`
Expected: FAIL, "undefined: LoadSecrets".

- [ ] **Step 3: Write entrypoint.go**

```go
package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// EntrypointConfig configures the in-cluster run.
type EntrypointConfig struct {
	RepoURL    string
	Script     string
	Branch     string
	BaseBranch string
	SecretsDir string
	WorkDir    string
	Stdout     io.Writer
}

// ConfigFromEnv reads the AGENTGOPHER_* variables set by BuildJob.
func ConfigFromEnv() (EntrypointConfig, error) {
	cfg := EntrypointConfig{
		RepoURL:    os.Getenv("AGENTGOPHER_REPO_URL"),
		Script:     os.Getenv("AGENTGOPHER_SCRIPT"),
		Branch:     os.Getenv("AGENTGOPHER_BRANCH"),
		BaseBranch: os.Getenv("AGENTGOPHER_BASE_BRANCH"),
		SecretsDir: os.Getenv("AGENTGOPHER_SECRETS_DIR"),
		WorkDir:    "/work/repo",
		Stdout:     os.Stdout,
	}
	if cfg.RepoURL == "" || cfg.Branch == "" || cfg.BaseBranch == "" || cfg.Script == "" {
		return cfg, errors.New("AGENTGOPHER_REPO_URL, AGENTGOPHER_SCRIPT, AGENTGOPHER_BRANCH and AGENTGOPHER_BASE_BRANCH must be set")
	}
	return cfg, nil
}

// LoadSecrets reads every regular file in dir as NAME=content (trimmed).
// A missing dir yields an empty map.
func LoadSecrets(dir string) (map[string]string, error) {
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		// The CSI driver uses a ..data symlink and ..<timestamp> dirs; skip them.
		if strings.HasPrefix(e.Name(), "..") {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out[e.Name()] = strings.TrimSpace(string(b))
	}
	return out, nil
}

// Entrypoint is the Job's main: clone, run the script, push, open a PR.
func Entrypoint(ctx context.Context) error {
	cfg, err := ConfigFromEnv()
	if err != nil {
		return err
	}
	return run(ctx, cfg)
}

func run(ctx context.Context, cfg EntrypointConfig) error {
	secrets, err := LoadSecrets(cfg.SecretsDir)
	if err != nil {
		return err
	}
	for k, v := range secrets {
		os.Setenv(k, v)
	}
	os.Setenv("AGENTGOPHER_BRANCH", cfg.Branch)
	os.Setenv("AGENTGOPHER_BASE_BRANCH", cfg.BaseBranch)

	cmd := func(dir string, name string, args ...string) (string, error) {
		c := exec.CommandContext(ctx, name, args...)
		c.Dir = dir
		var out bytes.Buffer
		c.Stdout = io.MultiWriter(cfg.Stdout, &out)
		c.Stderr = cfg.Stdout
		if err := c.Run(); err != nil {
			return out.String(), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return strings.TrimSpace(out.String()), nil
	}

	if os.Getenv("GH_TOKEN") != "" {
		if _, err := cmd("", "gh", "auth", "setup-git"); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(cfg.WorkDir), 0o755); err != nil {
		return err
	}
	if _, err := cmd("", "git", "clone", "--branch", cfg.BaseBranch, cfg.RepoURL, cfg.WorkDir); err != nil {
		return err
	}
	if _, err := cmd(cfg.WorkDir, "git", "checkout", "-b", cfg.Branch); err != nil {
		return err
	}
	if _, err := cmd(cfg.WorkDir, "git", "config", "user.name"); err != nil {
		if _, err := cmd(cfg.WorkDir, "git", "config", "user.name", "agentgopher"); err != nil {
			return err
		}
	}
	if _, err := cmd(cfg.WorkDir, "git", "config", "user.email"); err != nil {
		if _, err := cmd(cfg.WorkDir, "git", "config", "user.email", "agentgopher@users.noreply.github.com"); err != nil {
			return err
		}
	}

	if _, err := cmd(cfg.WorkDir, "go", "run", "./"+strings.TrimPrefix(cfg.Script, "./")); err != nil {
		return err
	}

	countOut, err := cmd(cfg.WorkDir, "git", "rev-list", cfg.BaseBranch+".."+cfg.Branch, "--count")
	if err != nil {
		return err
	}
	count, _ := strconv.Atoi(strings.TrimSpace(countOut))
	if count == 0 {
		fmt.Fprintln(cfg.Stdout, "No commits produced, no PR")
		return nil
	}
	if _, err := cmd(cfg.WorkDir, "git", "push", "-u", "origin", cfg.Branch); err != nil {
		return err
	}
	url, err := cmd(cfg.WorkDir, "gh", "pr", "create", "--base", cfg.BaseBranch, "--head", cfg.Branch,
		"--title", "agentgopher: "+cfg.Branch, "--body", "Opened by agentgopher remote run.")
	if err != nil {
		return err
	}
	fmt.Fprintf(cfg.Stdout, "PR: %s\n", strings.TrimSpace(url))
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./remote/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add remote/
git commit -m "Add in-cluster entrypoint that clones, runs, pushes and opens a PR"
```

---

### Task 9: `agentgopher remote run` and `remote entrypoint` commands

**Files:**
- Create: `cmd/agentgopher/remote.go`, `cmd/agentgopher/remote_test.go`
- Modify: `cmd/agentgopher/main.go` (one `rootCmd.AddCommand(remoteCmd)` line), the init templates from phase 6 (see Step 5)

**Interfaces:**
- Consumes: `remote.Submit`, `remote.StreamLogs`, `remote.Wait`, `remote.Entrypoint` (Tasks 6 to 8).
- Produces: `remoteCmd` (cobra, `Use: "remote"`), subcommands `run` and hidden `entrypoint`; `runRemote(ctx, client kubernetes.Interface, spec remote.RunSpec, out io.Writer) (prs []string, err error)`; `collectPRs(r io.Reader, w io.Writer) []string` (tees the log to `w` and returns the URLs of `PR: ` lines); `loadKubeClient(kubeconfig, kubeContext string) (kubernetes.Interface, error)`.
- Flags of `remote run`: `--repo` (required), `--script` (default `.agentgopher`), `--branch-prefix` (default `agentgopher/`), `--branch`, `--base-branch` (default `main`), `--namespace` (default `agentgopher`), `--image` (required; or env `AGENTGOPHER_IMAGE`), `--service-account` (default `agentgopher-runner`), `--timeout` (default `2h`), `--kubeconfig`, `--context`, `--env KEY=VALUE` (repeatable).
- On Ctrl-C while following: print `Job <name> keeps running. Follow it with: kubectl logs job/<name> -n <ns> -f` and exit 130 without deleting the Job.

- [ ] **Step 1: Write the failing tests**

`cmd/agentgopher/remote_test.go`:
```go
package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/e11en/agentgopher/remote"
)

func TestCollectPRsTeesAndExtracts(t *testing.T) {
	var out bytes.Buffer
	prs := collectPRs(strings.NewReader("Iteration 1/1\nPR: https://github.com/e11en/demo/pull/7\nbye\nPR:https://x/2\n"), &out)
	if len(prs) != 2 || prs[0] != "https://github.com/e11en/demo/pull/7" || prs[1] != "https://x/2" {
		t.Fatalf("prs = %v", prs)
	}
	if !strings.Contains(out.String(), "Iteration 1/1\n") || !strings.Contains(out.String(), "PR: https://github.com/e11en/demo/pull/7\n") {
		t.Fatalf("tee lost output:\n%s", out.String())
	}
}

// completingClientset makes every created Job immediately have a Running pod
// (so logs can stream) and a Complete condition (so Wait returns).
func completingClientset() *fake.Clientset {
	cs := fake.NewClientset()
	cs.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		job := action.(k8stesting.CreateAction).GetObject().(*batchv1.Job)
		job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: job.Name + "-p", Namespace: job.Namespace, Labels: map[string]string{remote.LabelRun: job.Name}},
			Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
		}
		_, _ = cs.CoreV1().Pods(job.Namespace).Create(context.Background(), pod, metav1.CreateOptions{})
		return false, nil, nil
	})
	return cs
}

func TestRunRemoteSubmitsStreamsAndWaits(t *testing.T) {
	cs := completingClientset()
	var out bytes.Buffer
	spec := remote.RunSpec{RepoURL: "https://x/y.git", Image: "img"}.Defaults(time.Now())
	prs, err := runRemote(context.Background(), cs, spec, &out)
	if err != nil {
		t.Fatal(err)
	}
	// The fake clientset serves "fake logs" as the pod log body.
	if !strings.Contains(out.String(), "fake logs") || !strings.Contains(out.String(), "Job agentgopher-run-") {
		t.Fatalf("out:\n%s", out.String())
	}
	if len(prs) != 0 {
		t.Fatalf("prs = %v", prs)
	}
}

func TestRunRemoteFailedJobIsAnError(t *testing.T) {
	cs := fake.NewClientset()
	cs.PrependReactor("create", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		job := action.(k8stesting.CreateAction).GetObject().(*batchv1.Job)
		job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: "BackoffLimitExceeded"}}
		_, _ = cs.CoreV1().Pods(job.Namespace).Create(context.Background(), &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: job.Name + "-p", Namespace: job.Namespace, Labels: map[string]string{remote.LabelRun: job.Name}},
			Status:     corev1.PodStatus{Phase: corev1.PodFailed},
		}, metav1.CreateOptions{})
		return false, nil, nil
	})
	spec := remote.RunSpec{RepoURL: "https://x/y.git", Image: "img"}.Defaults(time.Now())
	_, err := runRemote(context.Background(), cs, spec, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "BackoffLimitExceeded") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunRemoteCancelledKeepsJobAndTellsHowToFollow(t *testing.T) {
	cs := fake.NewClientset() // no pod ever appears, so StreamLogs blocks until cancel
	ctx, cancel := context.WithCancel(context.Background())
	var out bytes.Buffer
	spec := remote.RunSpec{RepoURL: "https://x/y.git", Image: "img"}.Defaults(time.Now())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := runRemote(ctx, cs, spec, &out)
	if err == nil {
		t.Fatal("expected error")
	}
	jobs, _ := cs.BatchV1().Jobs("agentgopher").List(context.Background(), metav1.ListOptions{})
	if len(jobs.Items) != 1 {
		t.Fatal("job must not be deleted on cancel")
	}
	if !strings.Contains(out.String(), "kubectl logs job/"+jobs.Items[0].Name+" -n agentgopher -f") {
		t.Fatalf("out:\n%s", out.String())
	}
}

func TestRemoteRunFlagsBuildSpec(t *testing.T) {
	cmd := newRemoteRunCmd()
	if err := cmd.ParseFlags([]string{"--repo", "https://x/y.git", "--image", "img", "--branch-prefix", "bot/", "--env", "A=1", "--env", "B=2", "--timeout", "30m"}); err != nil {
		t.Fatal(err)
	}
	spec, err := specFromFlags(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if spec.RepoURL != "https://x/y.git" || spec.Image != "img" || spec.BranchPrefix != "bot/" || spec.Env["A"] != "1" || spec.Env["B"] != "2" || spec.Timeout != 30*time.Minute {
		t.Fatalf("spec = %+v", spec)
	}
	if !strings.HasPrefix(spec.Branch, "bot/") {
		t.Fatalf("Branch = %q", spec.Branch)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/agentgopher/ -run 'Remote|CollectPRs'`
Expected: FAIL, "undefined: collectPRs".

- [ ] **Step 3: Write remote.go**

```go
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/e11en/agentgopher/remote"
)

var remoteCmd = &cobra.Command{
	Use:   "remote",
	Short: "Run agentgopher as a Kubernetes Job",
}

func init() {
	remoteCmd.AddCommand(newRemoteRunCmd(), newRemoteEntrypointCmd())
}

func newRemoteRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start a run in the cluster, follow its log and print the PR links",
		RunE: func(cmd *cobra.Command, _ []string) error {
			spec, err := specFromFlags(cmd)
			if err != nil {
				return err
			}
			kubeconfig, _ := cmd.Flags().GetString("kubeconfig")
			kubeContext, _ := cmd.Flags().GetString("context")
			client, err := loadKubeClient(kubeconfig, kubeContext)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			prs, err := runRemote(ctx, client, spec, cmd.OutOrStdout())
			if errors.Is(err, context.Canceled) {
				os.Exit(130)
			}
			if err != nil {
				return err
			}
			for _, pr := range prs {
				fmt.Fprintln(cmd.OutOrStdout(), "Pull request:", pr)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.String("repo", "", "https clone URL of the repo to work on (required)")
	f.String("script", remote.DefaultScript, "path inside the repo passed to `go run`")
	f.String("branch-prefix", remote.DefaultBranchPrefix, "prefix of the generated branch name")
	f.String("branch", "", "full branch name (overrides --branch-prefix)")
	f.String("base-branch", remote.DefaultBaseBranch, "branch to clone and open the PR against")
	f.String("namespace", remote.DefaultNamespace, "namespace of the Job")
	f.String("image", os.Getenv("AGENTGOPHER_IMAGE"), "runner image (required; env AGENTGOPHER_IMAGE)")
	f.String("service-account", remote.DefaultServiceAccount, "service account of the Job")
	f.Duration("timeout", remote.DefaultTimeout, "Job deadline")
	f.String("kubeconfig", "", "kubeconfig path (default: standard loading rules)")
	f.String("context", "", "kubeconfig context")
	f.StringArray("env", nil, "extra KEY=VALUE for the Job container (repeatable)")
	_ = cmd.MarkFlagRequired("repo")
	return cmd
}

func specFromFlags(cmd *cobra.Command) (remote.RunSpec, error) {
	f := cmd.Flags()
	get := func(name string) string { v, _ := f.GetString(name); return v }
	timeout, _ := f.GetDuration("timeout")
	envFlags, _ := f.GetStringArray("env")
	env := map[string]string{}
	for _, kv := range envFlags {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return remote.RunSpec{}, fmt.Errorf("--env %q is not KEY=VALUE", kv)
		}
		env[k] = v
	}
	spec := remote.RunSpec{
		RepoURL:        get("repo"),
		Script:         get("script"),
		Branch:         get("branch"),
		BranchPrefix:   get("branch-prefix"),
		BaseBranch:     get("base-branch"),
		Namespace:      get("namespace"),
		Image:          get("image"),
		ServiceAccount: get("service-account"),
		Timeout:        timeout,
		Env:            env,
	}.Defaults(time.Now())
	return spec, spec.Validate()
}

func loadKubeClient(kubeconfig, kubeContext string) (kubernetes.Interface, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: kubeContext}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w (for GKE, run gcloud container clusters get-credentials first)", err)
	}
	return kubernetes.NewForConfig(cfg)
}

// runRemote submits the Job, streams its log to out while collecting PR
// links, and waits for the Job to finish.
func runRemote(ctx context.Context, client kubernetes.Interface, spec remote.RunSpec, out io.Writer) ([]string, error) {
	name, err := remote.Submit(ctx, client, spec)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(out, "Job %s created in namespace %s on branch %s\n", name, spec.Namespace, spec.Branch)

	pr, pw := io.Pipe()
	prsCh := make(chan []string, 1)
	go func() { prsCh <- collectPRs(pr, out) }()
	streamErr := remote.StreamLogs(ctx, client, spec.Namespace, name, pw)
	pw.Close()
	prs := <-prsCh

	if ctx.Err() != nil {
		fmt.Fprintf(out, "\nJob %s keeps running. Follow it with: kubectl logs job/%s -n %s -f\n", name, name, spec.Namespace)
		return prs, ctx.Err()
	}
	if streamErr != nil {
		fmt.Fprintf(out, "log stream ended early: %v\n", streamErr)
	}
	outcome, err := remote.Wait(ctx, client, spec.Namespace, name)
	if err != nil {
		return prs, err
	}
	if !outcome.Succeeded {
		return prs, fmt.Errorf("job %s failed: %s %s", name, outcome.Reason, outcome.Message)
	}
	return prs, nil
}

// collectPRs copies r to w line by line and returns the URLs of "PR: " lines.
func collectPRs(r io.Reader, w io.Writer) []string {
	var prs []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		fmt.Fprintln(w, line)
		if rest, ok := strings.CutPrefix(line, "PR:"); ok {
			prs = append(prs, strings.TrimSpace(rest))
		}
	}
	return prs
}

func newRemoteEntrypointCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "entrypoint",
		Hidden: true,
		Short:  "In-cluster entrypoint used by the Job image",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return remote.Entrypoint(ctx)
		},
	}
}
```

In `cmd/agentgopher/main.go` add `rootCmd.AddCommand(remoteCmd)` next to the other `AddCommand` calls.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./cmd/agentgopher/ && go build ./... && go run ./cmd/agentgopher remote run --help | head -5`
Expected: PASS, and the help text lists `--repo`.

- [ ] **Step 5: Teach the init templates about the remote branch**

In every phase 6 template `main.go` (`templates/*/main.go`), the `BranchStrategy` of each `agentgopher.Run` call becomes:
```go
	// Set by `agentgopher remote run`; a plain local run keeps the template's default.
	branchStrategy := templateBranchStrategy()
```
with this helper copied into each template (templates share no code, ADR 0009):
```go
func templateBranchStrategy() *sandbox.BranchStrategy {
	if b := os.Getenv("AGENTGOPHER_BRANCH"); b != "" {
		return &sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: b, BaseBranch: os.Getenv("AGENTGOPHER_BASE_BRANCH")}
	}
	return &sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead}
}
```
Keep whatever default the template had instead of `BranchMergeToHead` where it differs. Add a test in `internal/initcmd` (phase 6 test file) that scaffolds each template and asserts the generated `main.go` contains `AGENTGOPHER_BRANCH`.

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/agentgopher/ templates/ internal/initcmd/
git commit -m "Add remote run command and remote-aware init templates"
```

---

### Task 10: Runner image and release workflow

**Files:**
- Create: `Dockerfile`, `.dockerignore`, `.github/workflows/release-image.yml`

- [ ] **Step 1: Write the Dockerfile**

```dockerfile
# Build the agentgopher CLI.
FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/agentgopher ./cmd/agentgopher

# Runner image: runs user scripts with `go run`, so it keeps the Go toolchain.
# git and gh for clone, push and PR; node for the Claude Code CLI.
FROM golang:1.25-bookworm
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl git gnupg \
  && curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
  && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" > /etc/apt/sources.list.d/github-cli.list \
  && curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
  && apt-get install -y --no-install-recommends gh nodejs \
  && npm install -g @anthropic-ai/claude-code \
  && rm -rf /var/lib/apt/lists/*
RUN useradd --create-home --uid 1000 agent
COPY --from=build /out/agentgopher /usr/local/bin/agentgopher
USER agent
WORKDIR /work
ENV GOFLAGS=-mod=mod GOPATH=/home/agent/go GOCACHE=/home/agent/.cache/go-build
ENTRYPOINT ["agentgopher"]
CMD ["remote", "entrypoint"]
```

`.dockerignore`:
```
.git
.agentgopher
deploy/terraform/.terraform
docs
```

- [ ] **Step 2: Build it locally (manual, needs Docker)**

Run: `docker build -t agentgopher:dev . && docker run --rm agentgopher:dev --help | head -3 && docker run --rm --entrypoint sh agentgopher:dev -c 'git --version && gh --version | head -1 && claude --version && go version'`
Expected: the CLI help and four version lines. Skip when Docker is unavailable and say so in the commit message.

- [ ] **Step 3: Write the release workflow**

`.github/workflows/release-image.yml`:
```yaml
name: release-image
on:
  push:
    tags: ["v*"]
permissions:
  contents: read
  id-token: write
jobs:
  push:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - id: auth
        uses: google-github-actions/auth@v2
        with:
          workload_identity_provider: ${{ secrets.GCP_WORKLOAD_IDENTITY_PROVIDER }}
          service_account: ${{ secrets.GCP_PUBLISHER_SERVICE_ACCOUNT }}
      - uses: google-github-actions/setup-gcloud@v2
      - run: gcloud auth configure-docker ${{ secrets.GCP_REGION }}-docker.pkg.dev --quiet
      - uses: docker/setup-buildx-action@v3
      - uses: docker/build-push-action@v6
        with:
          context: .
          push: true
          tags: |
            ${{ secrets.GCP_REGION }}-docker.pkg.dev/${{ secrets.GCP_PROJECT_ID }}/agentgopher/runner:${{ github.ref_name }}
            ${{ secrets.GCP_REGION }}-docker.pkg.dev/${{ secrets.GCP_PROJECT_ID }}/agentgopher/runner:latest
```

The four repository secrets (`GCP_WORKLOAD_IDENTITY_PROVIDER`, `GCP_PUBLISHER_SERVICE_ACCOUNT`, `GCP_PROJECT_ID`, `GCP_REGION`) come from the Terraform outputs of Task 12; `docs/kubernetes.md` (Task 13) tells how to set them with `gh secret set`.

- [ ] **Step 4: Validate the workflow syntax**

Run: `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/release-image.yml'))" && echo ok`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add Dockerfile .dockerignore .github/workflows/release-image.yml
git commit -m "Add runner image and release workflow"
```

---

### Task 11: Kubernetes manifests

**Files:**
- Create: `deploy/k8s/namespace.yaml`, `deploy/k8s/serviceaccount.yaml`, `deploy/k8s/rbac.yaml`, `deploy/k8s/secretproviderclass.yaml`, `deploy/k8s/job-example.yaml`, `deploy/k8s/kustomization.yaml`

- [ ] **Step 1: Write the manifests**

`deploy/k8s/namespace.yaml`:
```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: agentgopher
```

`deploy/k8s/serviceaccount.yaml` (the annotation value is patched by Terraform output; see Task 13):
```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: agentgopher-runner
  namespace: agentgopher
  annotations:
    iam.gke.io/gcp-service-account: REPLACE_WITH_RUNNER_SERVICE_ACCOUNT_EMAIL
```

`deploy/k8s/rbac.yaml`:
```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: agentgopher-runner
  namespace: agentgopher
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["create", "get", "list", "watch", "delete"]
  - apiGroups: [""]
    resources: ["pods/exec"]
    verbs: ["create"]
  - apiGroups: [""]
    resources: ["pods/log"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: agentgopher-runner
  namespace: agentgopher
subjects:
  - kind: ServiceAccount
    name: agentgopher-runner
    namespace: agentgopher
roleRef:
  kind: Role
  name: agentgopher-runner
  apiGroup: rbac.authorization.k8s.io
```

`deploy/k8s/secretproviderclass.yaml` (`PROJECT_ID` is replaced at apply time, see Task 13):
```yaml
apiVersion: secrets-store.csi.x-k8s.io/v1
kind: SecretProviderClass
metadata:
  name: agentgopher-secrets
  namespace: agentgopher
spec:
  provider: gke
  parameters:
    secrets: |
      - resourceName: "projects/PROJECT_ID/secrets/agentgopher-claude-code-oauth-token/versions/latest"
        path: "CLAUDE_CODE_OAUTH_TOKEN"
      - resourceName: "projects/PROJECT_ID/secrets/agentgopher-gh-token/versions/latest"
        path: "GH_TOKEN"
```

`deploy/k8s/job-example.yaml` is a reference copy of what `remote.BuildJob` produces, for people who want `kubectl apply` instead of the CLI:
```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: agentgopher-run-example
  namespace: agentgopher
  labels:
    agentgopher.dev/run: agentgopher-run-example
spec:
  backoffLimit: 0
  ttlSecondsAfterFinished: 86400
  activeDeadlineSeconds: 7200
  template:
    metadata:
      labels:
        agentgopher.dev/run: agentgopher-run-example
    spec:
      restartPolicy: Never
      serviceAccountName: agentgopher-runner
      containers:
        - name: runner
          image: REGION-docker.pkg.dev/PROJECT_ID/agentgopher/runner:latest
          command: ["agentgopher", "remote", "entrypoint"]
          env:
            - { name: AGENTGOPHER_REPO_URL, value: "https://github.com/OWNER/REPO.git" }
            - { name: AGENTGOPHER_SCRIPT, value: ".agentgopher" }
            - { name: AGENTGOPHER_BRANCH, value: "agentgopher/example" }
            - { name: AGENTGOPHER_BASE_BRANCH, value: "main" }
            - { name: AGENTGOPHER_SECRETS_DIR, value: "/var/secrets" }
          volumeMounts:
            - { name: secrets, mountPath: /var/secrets, readOnly: true }
            - { name: work, mountPath: /work }
      volumes:
        - name: secrets
          csi:
            driver: secrets-store-gke.csi.k8s.io
            readOnly: true
            volumeAttributes:
              secretProviderClass: agentgopher-secrets
        - name: work
          emptyDir: {}
```

`deploy/k8s/kustomization.yaml`:
```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - namespace.yaml
  - serviceaccount.yaml
  - rbac.yaml
  - secretproviderclass.yaml
```

- [ ] **Step 2: Validate offline**

Run: `kubectl kustomize deploy/k8s > /dev/null && kubectl create --dry-run=client --validate=false -f deploy/k8s/job-example.yaml -o name`
Expected: `job.batch/agentgopher-run-example`, no errors.

- [ ] **Step 3: Keep the Go builder and the example in sync**

Add to `remote/job_test.go`:
```go
func TestJobExampleMatchesBuilder(t *testing.T) {
	b, err := os.ReadFile("../deploy/k8s/job-example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"agentgopher.dev/run", "secrets-store-gke.csi.k8s.io", "secretProviderClass: agentgopher-secrets", "AGENTGOPHER_SECRETS_DIR", "ttlSecondsAfterFinished: 86400", "serviceAccountName: agentgopher-runner"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("job-example.yaml lacks %q", want)
		}
	}
}
```
with `"os"` imported.

Run: `go test ./remote/ -run JobExample`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add deploy/k8s/ remote/job_test.go
git commit -m "Add Kubernetes manifests for the runner namespace"
```

---

### Task 12: Terraform for the GKE Autopilot cluster

**Files:**
- Create: `deploy/terraform/versions.tf`, `variables.tf`, `main.tf`, `iam.tf`, `github.tf`, `outputs.tf`, `README.md`, `terraform.tfvars.example`, `.gitignore`

Verified against the `hashicorp/google` provider documentation for `google_container_cluster`: `enable_autopilot` ("Enable Autopilot for this cluster"), `workload_identity_config { workload_pool }`, `secret_manager_config { enabled }` ("Enable the Secret Manager add-on for this cluster"), `deletion_protection` (must be set explicitly on provider 5.0+), `release_channel { channel }`. The GKE docs confirm the Secret Manager add-on works on Autopilot and exposes the driver `secrets-store-gke.csi.k8s.io`.

- [ ] **Step 1: Write versions.tf and variables.tf**

`deploy/terraform/versions.tf`:
```hcl
terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.0"
    }
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}
```

`deploy/terraform/variables.tf`:
```hcl
variable "project_id" {
  description = "GCP project that hosts the cluster. No default on purpose."
  type        = string
}

variable "region" {
  description = "Region of the Autopilot cluster and the Artifact Registry repository, for example europe-west4."
  type        = string
}

variable "cluster_name" {
  description = "Name of the GKE Autopilot cluster."
  type        = string
  default     = "agentgopher"
}

variable "github_repository" {
  description = "GitHub repository allowed to push images through Workload Identity Federation, as owner/name."
  type        = string
  default     = "e11en/agentgopher"
}

variable "secret_names" {
  description = "Secret Manager secret ids the runner may read. Values are added outside Terraform."
  type        = list(string)
  default     = ["agentgopher-claude-code-oauth-token", "agentgopher-gh-token"]
}
```

- [ ] **Step 2: Write main.tf**

```hcl
locals {
  services = [
    "container.googleapis.com",
    "artifactregistry.googleapis.com",
    "secretmanager.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "sts.googleapis.com",
  ]
}

resource "google_project_service" "apis" {
  for_each           = toset(local.services)
  service            = each.value
  disable_on_destroy = false
}

resource "google_container_cluster" "agentgopher" {
  name     = var.cluster_name
  location = var.region

  enable_autopilot    = true
  deletion_protection = false

  release_channel {
    channel = "REGULAR"
  }

  workload_identity_config {
    workload_pool = "${var.project_id}.svc.id.goog"
  }

  secret_manager_config {
    enabled = true
  }

  depends_on = [google_project_service.apis]
}

resource "google_artifact_registry_repository" "images" {
  location      = var.region
  repository_id = "agentgopher"
  format        = "DOCKER"
  description   = "agentgopher runner images"

  depends_on = [google_project_service.apis]
}

resource "google_secret_manager_secret" "runner" {
  for_each  = toset(var.secret_names)
  secret_id = each.value

  replication {
    auto {}
  }

  depends_on = [google_project_service.apis]
}
```

- [ ] **Step 3: Write iam.tf**

```hcl
# Google service account the Job's Kubernetes service account impersonates.
resource "google_service_account" "runner" {
  account_id   = "agentgopher-runner"
  display_name = "agentgopher runner"
}

resource "google_service_account_iam_member" "runner_workload_identity" {
  service_account_id = google_service_account.runner.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "serviceAccount:${var.project_id}.svc.id.goog[agentgopher/agentgopher-runner]"
}

resource "google_secret_manager_secret_iam_member" "runner_reads_secrets" {
  for_each  = google_secret_manager_secret.runner
  secret_id = each.value.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runner.email}"
}

# Autopilot nodes pull images with the project's default compute service account.
data "google_project" "current" {}

resource "google_artifact_registry_repository_iam_member" "nodes_pull" {
  location   = google_artifact_registry_repository.images.location
  repository = google_artifact_registry_repository.images.name
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${data.google_project.current.number}-compute@developer.gserviceaccount.com"
}
```

- [ ] **Step 4: Write github.tf**

```hcl
# Lets the release-image workflow push to Artifact Registry without a key file.
resource "google_iam_workload_identity_pool" "github" {
  workload_identity_pool_id = "github"
  display_name              = "GitHub Actions"
}

resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "github-oidc"
  display_name                       = "GitHub OIDC"

  attribute_mapping = {
    "google.subject"             = "assertion.sub"
    "attribute.repository"       = "assertion.repository"
    "attribute.repository_owner" = "assertion.repository_owner"
  }
  attribute_condition = "assertion.repository == \"${var.github_repository}\""

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
}

resource "google_service_account" "publisher" {
  account_id   = "agentgopher-publisher"
  display_name = "agentgopher image publisher (GitHub Actions)"
}

resource "google_service_account_iam_member" "publisher_federation" {
  service_account_id = google_service_account.publisher.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/attribute.repository/${var.github_repository}"
}

resource "google_artifact_registry_repository_iam_member" "publisher_push" {
  location   = google_artifact_registry_repository.images.location
  repository = google_artifact_registry_repository.images.name
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${google_service_account.publisher.email}"
}
```

- [ ] **Step 5: Write outputs.tf, tfvars example, gitignore and README**

`deploy/terraform/outputs.tf`:
```hcl
output "cluster_name" {
  value = google_container_cluster.agentgopher.name
}

output "get_credentials_command" {
  value = "gcloud container clusters get-credentials ${google_container_cluster.agentgopher.name} --region ${var.region} --project ${var.project_id}"
}

output "runner_service_account_email" {
  value = google_service_account.runner.email
}

output "image_repository" {
  value = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}"
}

output "github_workload_identity_provider" {
  value = google_iam_workload_identity_pool_provider.github.name
}

output "github_publisher_service_account" {
  value = google_service_account.publisher.email
}
```

`deploy/terraform/terraform.tfvars.example`:
```hcl
project_id = "your-gcp-project-id"
region     = "europe-west4"
```

`deploy/terraform/.gitignore`:
```
.terraform/
*.tfstate
*.tfstate.backup
terraform.tfvars
```

`deploy/terraform/README.md`:
```markdown
# agentgopher on GKE

Creates a new GKE Autopilot cluster with Workload Identity and the Secret
Manager add-on, an Artifact Registry repository, the runner service account,
the two Secret Manager secrets (empty; add versions yourself) and Workload
Identity Federation for the release workflow.

```bash
cp terraform.tfvars.example terraform.tfvars   # fill in project_id and region
terraform init
terraform plan
terraform apply
```

State is local by default. Add a GCS backend block to `versions.tf` before
sharing this setup with others.

See `docs/kubernetes.md` for the steps after apply.
```

- [ ] **Step 6: Validate**

Run:
```bash
cd deploy/terraform && terraform fmt -check -recursive && terraform init -backend=false -input=false > /dev/null && terraform validate && cd -
```
Expected: `Success! The configuration is valid.` `terraform init -backend=false` downloads the provider; this is the only network access in the plan and runs only here.

- [ ] **Step 7: Commit**

```bash
git add deploy/terraform/
git commit -m "Add Terraform for a GKE Autopilot cluster with Workload Identity and Secret Manager"
```

---

### Task 13: Documentation and end-to-end check

**Files:**
- Create: `docs/kubernetes.md`
- Modify: `README.md`

- [ ] **Step 1: Write docs/kubernetes.md**

```markdown
# Running agentgopher on GKE

## One-time setup

1. Create the infrastructure: see `deploy/terraform/README.md`. Note the outputs.
2. Add secret values (never in the repo):
   ```bash
   claude setup-token | gcloud secrets versions add agentgopher-claude-code-oauth-token --data-file=-
   printf '%s' "$GH_TOKEN" | gcloud secrets versions add agentgopher-gh-token --data-file=-
   ```
   The GitHub token needs `contents: write` and `pull_requests: write` on the repos the agent works on.
3. Point kubectl at the cluster: run the `get_credentials_command` output. This needs `gke-gcloud-auth-plugin` (`gcloud components install gke-gcloud-auth-plugin`).
4. Apply the manifests, filling in the two placeholders:
   ```bash
   RUNNER_SA=$(terraform -chdir=deploy/terraform output -raw runner_service_account_email)
   PROJECT_ID=$(terraform -chdir=deploy/terraform output -raw project_id 2>/dev/null || gcloud config get-value project)
   kubectl kustomize deploy/k8s \
     | sed "s/REPLACE_WITH_RUNNER_SERVICE_ACCOUNT_EMAIL/$RUNNER_SA/; s/PROJECT_ID/$PROJECT_ID/g" \
     | kubectl apply -f -
   ```
5. Set the GitHub repository secrets for the release workflow:
   ```bash
   gh secret set GCP_WORKLOAD_IDENTITY_PROVIDER --body "$(terraform -chdir=deploy/terraform output -raw github_workload_identity_provider)"
   gh secret set GCP_PUBLISHER_SERVICE_ACCOUNT --body "$(terraform -chdir=deploy/terraform output -raw github_publisher_service_account)"
   gh secret set GCP_PROJECT_ID --body "$PROJECT_ID"
   gh secret set GCP_REGION --body "<region>"
   ```
6. Publish an image: `git tag v0.1.0 && git push origin v0.1.0`. The workflow pushes `<image_repository>/runner:v0.1.0` and `:latest`.

## Starting a run

```bash
export AGENTGOPHER_IMAGE=$(terraform -chdir=deploy/terraform output -raw image_repository)/runner:latest
agentgopher remote run --repo https://github.com/OWNER/REPO.git --script .agentgopher
```

The command creates a Job, follows its log and ends with the pull request
links. Ctrl-C leaves the Job running and prints the `kubectl logs` command to
follow it. Jobs are deleted 24 hours after they finish.

The target repo needs an `.agentgopher/` directory from `agentgopher init`.
Templates read `AGENTGOPHER_BRANCH` and work on that branch; the entrypoint
pushes it and opens the PR.

## Sandboxes inside the cluster

A script that runs in the cluster can use `kubernetes.New(&kubernetes.Options{Image: ...})`
as its sandbox provider. The Job's service account may create Pods in the
`agentgopher` namespace (see `deploy/k8s/rbac.yaml`). Leftover sandbox Pods
are labelled `app.kubernetes.io/component=sandbox` and expire after
`Options.PodTTL` (default 2h):

```bash
kubectl delete pods -n agentgopher -l app.kubernetes.io/component=sandbox
```
```

- [ ] **Step 2: Update README**

Add under the provider table in `README.md`:
```markdown
## Running in Kubernetes

`agentgopher remote run` starts a run as a Job on a GKE cluster and prints the
pull requests it opened. Setup and usage: `docs/kubernetes.md`.
```

- [ ] **Step 3: Full verification**

Run: `go vet ./... && golangci-lint run ./... && go test -race ./... && terraform -chdir=deploy/terraform fmt -check && perl -ne 'print "$ARGV:$.: $_" if /\x{2014}|\x{2013}/' docs/kubernetes.md README.md $(find deploy -type f)`
Expected: tests pass, no lint output, and the perl check prints nothing (no em or en dash in the new files).

- [ ] **Step 4: Manual end-to-end (needs the cluster, Docker and tokens)**

```bash
terraform -chdir=deploy/terraform apply
# steps 2 to 6 of docs/kubernetes.md
agentgopher remote run --repo https://github.com/e11en/agentgopher-demo.git
```
Expected: log lines from the Job, then `Pull request: https://github.com/e11en/agentgopher-demo/pull/<n>`. Record the result (or "skipped") in the Step 5 commit message body. Create `agentgopher-demo` as a throwaway private repo with an `.agentgopher/` directory from `agentgopher init` first.

- [ ] **Step 5: Commit and push**

```bash
git add docs/kubernetes.md README.md
git commit -m "Document running agentgopher on GKE"
git push origin main
gh run watch --repo e11en/agentgopher --exit-status
```
Expected: the `ci` workflow passes.

---

## Self-review notes

- Spec coverage: part A (Pod per sandbox, client-go exec, sync via the phase 4 `IsolatedHandle` contract, namespace, image, resources, service account, node selector, Pod TTL) is Tasks 2 to 5. Part B (image, Job, `remote run`, clone plus named branch plus push plus PR, Secret Manager, Terraform for a new cluster, manifests, RBAC for sandbox Pods) is Tasks 6 to 13. The "Decisions" section (on-demand CLI trigger, new cluster, no CronJob or webhook) is honoured: nothing runs in the cluster between runs.
- Deliberate departures from upstream: upstream has no Kubernetes provider (`.out-of-scope/built-in-sandbox-providers.md`); this one is documented as part of `docs/plan.md`.
- Type consistency: `sandbox.StartError`, `sandbox.ExecOptions.Stdin *string`, `stream.NewBoundedTail` and `stream.MaxTailChars` match the phase 1+2 plan; `stream.NewLineWriter` is introduced in Task 1 and the phase 4 plan is told to reuse it; `remote.LabelRun` is used by `BuildJob`, `findPod` and the tests alike; `ExecutorFactory` has one signature in Tasks 2, 3 and 4.
- Verified APIs (pkg.go.dev, client-go v0.37.1 pages, same shapes in v0.36): `remotecommand.NewWebSocketExecutor`, `NewSPDYExecutor`, `NewFallbackExecutor`, `Executor.StreamWithContext`, `StreamOptions`; `PodInterface.GetLogs(name, *PodLogOptions) *rest.Request`; `(*rest.Request).Stream(ctx)`; `utilexec.CodeExitError{Err, Code}`. `fake.NewClientset` exists since client-go v0.30; if the chosen version lacks it, use `fake.NewSimpleClientset`.
- Verified Terraform arguments (provider docs): `enable_autopilot`, `workload_identity_config.workload_pool`, `secret_manager_config.enabled`, `deletion_protection`, `release_channel.channel`. Not verified by running: `terraform validate` is a plan step.
- Review Focus items map to tests: 1 in Task 3 (`TestExecStreamsWholeLinesAcrossChunks`), 2 in Task 2 (`TestCreatePodFailsWhenNeverReady`), 3 in Task 4 (`TestTarPathReproducesTreeWithSymlinksAndEmptyDirs`), 4 in Task 9 (`TestRunRemoteCancelledKeepsJobAndTellsHowToFollow`), 5 in Task 8 (`TestRunWithoutCommitsSkipsPushAndPR`).
- Open decisions before implementation: the GCP project id and region for `terraform.tfvars`; whether the Job may also use the Kubernetes sandbox provider by default (the templates currently keep their own provider choice); the GitHub token scope (fine-grained token limited to the target repos is recommended).
