package kubernetes

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

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
	fe := &fakeExecutor{script: map[string]scriptedExec{"cd '/tmp' && false": {stderr: "nope", exitCode: 3}}}
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
