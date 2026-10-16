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
		// The tracker lock is held while reactors run; create the pod outside it.
		go func() {
			_, _ = cs.CoreV1().Pods(job.Namespace).Create(context.Background(), pod, metav1.CreateOptions{})
		}()
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
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: job.Name + "-p", Namespace: job.Namespace, Labels: map[string]string{remote.LabelRun: job.Name}},
			Status:     corev1.PodStatus{Phase: corev1.PodFailed},
		}
		go func() {
			_, _ = cs.CoreV1().Pods(job.Namespace).Create(context.Background(), pod, metav1.CreateOptions{})
		}()
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
