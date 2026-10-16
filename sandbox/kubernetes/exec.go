package kubernetes

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	corev1 "k8s.io/api/core/v1"
	k8sclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"
	"k8s.io/streaming/pkg/httpstream"

	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/sandbox"
)

type handle struct {
	client k8sclient.Interface
	cfg    *rest.Config
	o      Options
	pod    string
}

func (h *handle) WorktreePath() string { return WorkDir }

func (h *handle) Close(ctx context.Context) error {
	return deletePod(ctx, h.client, h.o.Namespace, h.pod)
}

func (h *handle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) {
	if opts.Cwd != "" {
		command = "cd " + shellQuote(opts.Cwd) + " && " + command
	}
	execOpts := &corev1.PodExecOptions{
		Container: "agent",
		Command:   []string{"sh", "-c", command},
		Stdin:     opts.Stdin != nil,
		Stdout:    true,
		Stderr:    true,
	}
	exec, err := h.o.Executor(h.client, h.cfg, h.o.Namespace, h.pod, execOpts)
	if err != nil {
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
	}

	stdoutTail := stream.NewBoundedTail(stream.MaxTailChars, "\n")
	var stderr bytes.Buffer
	lw := stream.NewLineWriter(func(line string) {
		stdoutTail.Push(line)
		if opts.OnLine != nil {
			opts.OnLine(line)
		}
	})
	so := remotecommand.StreamOptions{Stdout: lw, Stderr: &stderr}
	if opts.Stdin != nil {
		so.Stdin = strings.NewReader(*opts.Stdin)
	}

	err = exec.StreamWithContext(ctx, so)
	lw.Flush()
	if ctx.Err() != nil {
		return sandbox.ExecResult{}, ctx.Err()
	}
	exitCode := 0
	if err != nil {
		var ce utilexec.CodeExitError
		if !errors.As(err, &ce) {
			return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
		}
		exitCode = ce.Code
	}
	stderrTail := stream.NewBoundedTail(stream.MaxTailChars, "")
	stderrTail.Push(stderr.String())
	return sandbox.ExecResult{Stdout: stdoutTail.String(), Stderr: stderrTail.String(), ExitCode: exitCode}, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// defaultExecutor opens a WebSocket exec stream and falls back to SPDY when
// the API server or a proxy does not support the upgrade.
func defaultExecutor(client k8sclient.Interface, cfg *rest.Config, namespace, pod string, opts *corev1.PodExecOptions) (remotecommand.Executor, error) {
	req := client.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(namespace).Name(pod).SubResource("exec").
		VersionedParams(opts, scheme.ParameterCodec)
	u := req.URL()
	ws, err := remotecommand.NewWebSocketExecutor(cfg, http.MethodPost, u.String())
	if err != nil {
		return nil, err
	}
	spdy, err := remotecommand.NewSPDYExecutor(cfg, http.MethodPost, u)
	if err != nil {
		return nil, err
	}
	return remotecommand.NewFallbackExecutor(ws, spdy, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
}

var _ = url.URL{}
