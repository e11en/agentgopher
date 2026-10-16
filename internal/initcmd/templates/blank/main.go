// Blank template: customize this to build your own orchestration.
// Run it from the repo root with: go run -C .agentgopher .
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/e11en/agentgopher"
	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/docker"
	"github.com/e11en/agentgopher/sandbox/kubernetes"
)

func main() {
	_, err := agentgopher.Run(context.Background(), agentgopher.RunOptions{
		// Run with go run -C .agentgopher, so the repo root is one level up.
		Cwd:        "..",
		Agent:      agent.ClaudeCode("claude-opus-4-8", nil),
		Sandbox:    sandboxProvider(),
		PromptFile: "./prompt.md",
		Hooks:      agentgopher.Hooks{SandboxOnSandboxReady: []agentgopher.SandboxHook{}},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// sandboxProvider picks Docker on a workstation and the Kubernetes provider
// inside an `agentgopher remote run` Job, where no Docker daemon exists and
// the runner image doubles as the sandbox image.
func sandboxProvider() sandbox.Provider {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return kubernetes.New(&kubernetes.Options{Image: os.Getenv("AGENTGOPHER_IMAGE")})
	}
	return docker.New(nil)
}
