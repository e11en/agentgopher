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
