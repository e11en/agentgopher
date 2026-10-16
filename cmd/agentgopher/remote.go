package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/e11en/agentgopher/remote"
)

func init() {
	subcommandFactories = append(subcommandFactories, newRemoteCmd)
}

func newRemoteCmd() *cobra.Command {
	root := &cobra.Command{Use: "remote", Short: "Run agentgopher as a Kubernetes Job"}
	root.AddCommand(newRemoteRunCmd(), newRemoteEntrypointCmd())
	return root
}

func newRemoteRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start a run in the cluster, follow its log and print the PR links",
		RunE: func(cmd *cobra.Command, _ []string) error {
			spec, err := specFromFlags(cmd)
			if err != nil {
				return err
			}
			kubeconfig, _ := cmd.Flags().GetString("kubeconfig")
			kubeContext, _ := cmd.Flags().GetString("context")
			client, err := loadKubeClient(kubeconfig, kubeContext)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			prs, err := runRemote(ctx, client, spec, cmd.OutOrStdout())
			if errors.Is(err, context.Canceled) {
				os.Exit(130)
			}
			if err != nil {
				return err
			}
			for _, pr := range prs {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Pull request:", pr)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.String("repo", "", "https clone URL of the repo to work on (required)")
	f.String("script", remote.DefaultScript, "path inside the repo passed to `go run`")
	f.String("branch-prefix", remote.DefaultBranchPrefix, "prefix of the generated branch name")
	f.String("branch", "", "full branch name (overrides --branch-prefix)")
	f.String("base-branch", remote.DefaultBaseBranch, "branch to clone and open the PR against")
	f.String("namespace", remote.DefaultNamespace, "namespace of the Job")
	f.String("image", os.Getenv("AGENTGOPHER_IMAGE"), "runner image (required; env AGENTGOPHER_IMAGE)")
	f.String("service-account", remote.DefaultServiceAccount, "service account of the Job")
	f.Duration("timeout", remote.DefaultTimeout, "Job deadline")
	f.String("kubeconfig", "", "kubeconfig path (default: standard loading rules)")
	f.String("context", "", "kubeconfig context")
	f.StringArray("env", nil, "extra KEY=VALUE for the Job container (repeatable)")
	_ = cmd.MarkFlagRequired("repo")
	return cmd
}

func specFromFlags(cmd *cobra.Command) (remote.RunSpec, error) {
	f := cmd.Flags()
	get := func(name string) string { v, _ := f.GetString(name); return v }
	timeout, _ := f.GetDuration("timeout")
	envFlags, _ := f.GetStringArray("env")
	env := map[string]string{}
	for _, kv := range envFlags {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return remote.RunSpec{}, fmt.Errorf("--env %q is not KEY=VALUE", kv)
		}
		env[k] = v
	}
	spec := remote.RunSpec{
		RepoURL:        get("repo"),
		Script:         get("script"),
		Branch:         get("branch"),
		BranchPrefix:   get("branch-prefix"),
		BaseBranch:     get("base-branch"),
		Namespace:      get("namespace"),
		Image:          get("image"),
		ServiceAccount: get("service-account"),
		Timeout:        timeout,
		Env:            env,
	}.Defaults(time.Now())
	return spec, spec.Validate()
}

func loadKubeClient(kubeconfig, kubeContext string) (kubernetes.Interface, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		rules.ExplicitPath = kubeconfig
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: kubeContext}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w (for GKE, run gcloud container clusters get-credentials first)", err)
	}
	return kubernetes.NewForConfig(cfg)
}

// runRemote submits the Job, streams its log to out while collecting PR
// links, and waits for the Job to finish.
func runRemote(ctx context.Context, client kubernetes.Interface, spec remote.RunSpec, out io.Writer) ([]string, error) {
	name, err := remote.Submit(ctx, client, spec)
	if err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(out, "Job %s created in namespace %s on branch %s\n", name, spec.Namespace, spec.Branch)

	pr, pw := io.Pipe()
	prsCh := make(chan []string, 1)
	go func() { prsCh <- collectPRs(pr, out) }()
	streamErr := remote.StreamLogs(ctx, client, spec.Namespace, name, pw)
	_ = pw.Close()
	prs := <-prsCh

	if ctx.Err() != nil {
		_, _ = fmt.Fprintf(out, "\nJob %s keeps running. Follow it with: kubectl logs job/%s -n %s -f\n", name, name, spec.Namespace)
		return prs, ctx.Err()
	}
	if streamErr != nil {
		_, _ = fmt.Fprintf(out, "log stream ended early: %v\n", streamErr)
	}
	outcome, err := remote.Wait(ctx, client, spec.Namespace, name)
	if err != nil {
		return prs, err
	}
	if !outcome.Succeeded {
		return prs, fmt.Errorf("job %s failed: %s %s", name, outcome.Reason, outcome.Message)
	}
	return prs, nil
}

// collectPRs copies r to w line by line and returns the URLs of "PR: " lines.
func collectPRs(r io.Reader, w io.Writer) []string {
	var prs []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		_, _ = fmt.Fprintln(w, line)
		if rest, ok := strings.CutPrefix(line, "PR:"); ok {
			prs = append(prs, strings.TrimSpace(rest))
		}
	}
	return prs
}

func newRemoteEntrypointCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "entrypoint",
		Hidden: true,
		Short:  "In-cluster entrypoint used by the Job image",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			return remote.Entrypoint(ctx)
		},
	}
}
