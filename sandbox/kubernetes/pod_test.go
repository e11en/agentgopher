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
