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
	defer func() { _ = rc.Close() }()
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
