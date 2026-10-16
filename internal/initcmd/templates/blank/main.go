// Blank template: customize this to build your own orchestration.
// Run it from the repo root with: go run -C .agentgopher .
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/e11en/agentgopher"
	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox/docker"
)

func main() {
	_, err := agentgopher.Run(context.Background(), agentgopher.RunOptions{
		// Run with go run -C .agentgopher, so the repo root is one level up.
		Cwd:        "..",
		Agent:      agent.ClaudeCode("claude-opus-4-8", nil),
		Sandbox:    docker.New(nil),
		PromptFile: "./prompt.md",
		Hooks:      agentgopher.Hooks{SandboxOnSandboxReady: []agentgopher.SandboxHook{}},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
