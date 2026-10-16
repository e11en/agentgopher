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
