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
