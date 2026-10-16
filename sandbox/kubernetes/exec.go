package kubernetes

import (
	"errors"

	corev1 "k8s.io/api/core/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

func defaultExecutor(_ k8sclient.Interface, _ *rest.Config, _, _ string, _ *corev1.PodExecOptions) (remotecommand.Executor, error) {
	return nil, errors.New("not implemented")
}
