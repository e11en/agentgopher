package kubernetes

import (
	"context"

	"github.com/e11en/agentgopher/sandbox"
)

// New returns an isolated sandbox provider that runs each sandbox as a Pod.
func New(opts *Options) sandbox.IsolatedProvider {
	var o Options
	if opts != nil {
		o = *opts
	}
	o = withDefaults(o)
	return sandbox.NewIsolatedProvider(sandbox.IsolatedProviderConfig{
		Name: "kubernetes",
		Env:  o.Env,
		Create: func(ctx context.Context, co sandbox.IsolatedCreateOptions) (sandbox.IsolatedHandle, error) {
			client, cfg, err := loadClient(o)
			if err != nil {
				return nil, err
			}
			env := map[string]string{}
			for k, v := range o.Env {
				env[k] = v
			}
			for k, v := range co.Env {
				env[k] = v
			}
			pod, err := createPod(ctx, client, o, newPodName(), env)
			if err != nil {
				return nil, err
			}
			return &handle{client: client, cfg: cfg, o: o, pod: pod.Name}, nil
		},
	})
}
