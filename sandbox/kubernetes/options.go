// Package kubernetes runs each agent sandbox as a Kubernetes Pod.
package kubernetes

import (
	"errors"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/e11en/agentgopher/sandbox"
)

// WorkDir is the repo path inside the sandbox Pod.
const WorkDir = sandbox.RepoDir

// NoRuntimeClass opts a provider out of the gVisor default.
const NoRuntimeClass = "none"

// ExecutorFactory builds the remotecommand executor for one exec call. The
// default builds a WebSocket executor with SPDY fallback; tests inject a fake.
type ExecutorFactory func(client k8sclient.Interface, cfg *rest.Config, namespace, pod string, opts *corev1.PodExecOptions) (remotecommand.Executor, error)

// Options configure the provider.
type Options struct {
	// Namespace the sandbox Pods are created in. Default: "agentgopher".
	Namespace string
	// Image of the sandbox container. Required. It must contain git and the agent CLI.
	Image string
	// Resources of the sandbox container. Default: requests 500m CPU and 1Gi,
	// limits 2 CPU and 4Gi, which fits GKE Autopilot's general-purpose class.
	Resources corev1.ResourceRequirements
	// ServiceAccount for the Pod. Default: the namespace default.
	ServiceAccount string
	NodeSelector   map[string]string
	// RuntimeClass of the sandbox Pod. Default "gvisor" (GKE Sandbox), which
	// runs the agent on its own user-space kernel. Set NoRuntimeClass to run
	// on the node's default runtime.
	RuntimeClass string
	// PodTTL bounds the Pod's lifetime as a safety net against leaks. Default: 2h.
	PodTTL time.Duration
	// ReadyTimeout bounds the wait for the Pod to reach Running. Default: 5m.
	ReadyTimeout time.Duration
	// Kubeconfig path. Default: in-cluster config when KUBERNETES_SERVICE_HOST
	// is set, otherwise the standard kubeconfig loading rules.
	Kubeconfig string
	// Env is injected into every sandbox. Merged at launch time.
	Env map[string]string

	// Client, Config and Executor are test seams; zero values use the real ones.
	Client   k8sclient.Interface
	Config   *rest.Config
	Executor ExecutorFactory
}

func withDefaults(o Options) Options {
	if o.Namespace == "" {
		o.Namespace = "agentgopher"
	}
	if o.PodTTL == 0 {
		o.PodTTL = 2 * time.Hour
	}
	if o.ReadyTimeout == 0 {
		o.ReadyTimeout = 5 * time.Minute
	}
	if o.RuntimeClass == "" {
		o.RuntimeClass = "gvisor"
	}
	if o.Resources.Requests == nil && o.Resources.Limits == nil {
		o.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("1Gi")},
			Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2"), corev1.ResourceMemory: resource.MustParse("4Gi")},
		}
	}
	if o.Executor == nil {
		o.Executor = defaultExecutor
	}
	return o
}

// loadClient returns the configured client, or builds one from the kubeconfig
// or the in-cluster environment.
func loadClient(o Options) (k8sclient.Interface, *rest.Config, error) {
	if o.Image == "" {
		return nil, nil, errors.New("kubernetes: Options.Image is required")
	}
	if o.Client != nil {
		return o.Client, o.Config, nil
	}
	var cfg *rest.Config
	var err error
	switch {
	case o.Kubeconfig != "":
		cfg, err = clientcmd.BuildConfigFromFlags("", o.Kubeconfig)
	case os.Getenv("KUBERNETES_SERVICE_HOST") != "":
		cfg, err = rest.InClusterConfig()
	default:
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), &clientcmd.ConfigOverrides{}).ClientConfig()
	}
	if err != nil {
		return nil, nil, err
	}
	client, err := k8sclient.NewForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	return client, cfg, nil
}
