// Package agentsandbox runs each agent sandbox as a GKE Agent Sandbox
// (kubernetes-sigs/agent-sandbox): a gVisor-isolated Pod claimed from a warm
// pool and driven through the official Go SDK.
package agentsandbox

import (
	"context"
	"os"
	"sort"
	"time"

	"k8s.io/client-go/tools/clientcmd"
	sandboxsdk "sigs.k8s.io/agent-sandbox/clients/go/sandbox"
	extv1beta1 "sigs.k8s.io/agent-sandbox/extensions/api/v1beta1"

	"github.com/e11en/agentgopher/internal/stream"
)

// Options configure the provider.
type Options struct {
	// Namespace of the SandboxClaim. Default: "agentgopher".
	Namespace string
	// WarmPool is the SandboxWarmPool to claim from. Default: the
	// AGENTGOPHER_WARM_POOL env var, then "agentgopher-sandboxes".
	WarmPool string
	// Kubeconfig path. Default: in-cluster config when KUBERNETES_SERVICE_HOST
	// is set, otherwise the standard kubeconfig loading rules.
	Kubeconfig string
	// ReadyTimeout bounds the wait for the claimed sandbox. Default: 5m.
	ReadyTimeout time.Duration
	// ShutdownAfter expires the claim as a safety net against leaks. Default: 2h.
	ShutdownAfter time.Duration
	// Env is injected into every sandbox. Merged at launch time.
	Env map[string]string
	// MaxOutputTailChars bounds the stdout and stderr kept in ExecResult.
	MaxOutputTailChars int
	// Open claims and connects a sandbox. Test seam; the default uses the SDK.
	Open func(ctx context.Context, env map[string]string) (API, error)
}

// maxTransferSize bounds one file upload or download through the SDK.
const maxTransferSize = 8 << 30

func withDefaults(o Options) Options {
	if o.Namespace == "" {
		o.Namespace = "agentgopher"
	}
	if o.WarmPool == "" {
		o.WarmPool = os.Getenv("AGENTGOPHER_WARM_POOL")
	}
	if o.WarmPool == "" {
		o.WarmPool = "agentgopher-sandboxes"
	}
	if o.ReadyTimeout == 0 {
		o.ReadyTimeout = 5 * time.Minute
	}
	if o.ShutdownAfter == 0 {
		o.ShutdownAfter = 2 * time.Hour
	}
	if o.MaxOutputTailChars == 0 {
		o.MaxOutputTailChars = stream.MaxTailChars
	}
	if o.Open == nil {
		o.Open = sdkOpen(o)
	}
	return o
}

// sdkOptions maps the provider options onto the SDK. Inside a cluster the
// Job talks to the sandbox Pod directly; elsewhere the SDK port-forwards.
func sdkOptions(o Options, env map[string]string) sandboxsdk.Options {
	so := sandboxsdk.Options{
		Runtime:             sandboxsdk.RuntimeSandboxd,
		Namespace:           o.Namespace,
		WarmPoolName:        o.WarmPool,
		SandboxReadyTimeout: o.ReadyTimeout,
		ShutdownAfter:       o.ShutdownAfter,
		Quiet:               true,
		// CopyToWorktree ships whole directories (node_modules) as one tar;
		// the SDK's 256 MB default is too small for that. RequestTimeout caps
		// REST calls whose ctx has no deadline, such as CopyFileOut.
		MaxUploadSize:   maxTransferSize,
		MaxDownloadSize: maxTransferSize,
		RequestTimeout:  10 * time.Minute,
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		so.Connectivity = sandboxsdk.ConnectivityInClusterPodIP
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		so.Env = append(so.Env, extv1beta1.EnvVar{Name: k, Value: env[k]})
	}
	return so
}

func sdkOpen(o Options) func(ctx context.Context, env map[string]string) (API, error) {
	return func(ctx context.Context, env map[string]string) (API, error) {
		so := sdkOptions(o, env)
		if o.Kubeconfig != "" {
			cfg, err := clientcmd.BuildConfigFromFlags("", o.Kubeconfig)
			if err != nil {
				return nil, err
			}
			so.RestConfig = cfg
		}
		sb, err := sandboxsdk.New(ctx, so)
		if err != nil {
			return nil, err
		}
		if err := sb.Open(ctx); err != nil {
			return nil, err
		}
		return sdkAPI{sb: sb}, nil
	}
}
