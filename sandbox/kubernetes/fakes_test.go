package kubernetes

import (
	"context"
	"fmt"
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
