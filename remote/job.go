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
