// Simple loop: an agent that picks open issues one by one and closes them.
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
)

func main() {
	_, err := agentgopher.Run(context.Background(), agentgopher.RunOptions{
		// Run with go run -C .agentgopher, so the repo root is one level up.
		Cwd: "..",
		// Shown as a prefix in log output.
		Name: "worker",
		// Runs the agent inside an isolated container.
		Sandbox: docker.New(nil),
		// Sonnet balances capability and speed; switch to claude-opus-4-8 for harder problems.
		Agent: agent.ClaudeCode("claude-sonnet-4-6", nil),
		// Shell expressions in the prompt run inside the sandbox at the start of each iteration.
		PromptFile: "./prompt.md",
		// Each iteration works on a single issue. Set to 1 for single-shot mode.
		MaxIterations: 3,
		// merge-to-head: the agent works on a temporary branch that is merged back to HEAD.
		// Required for CopyToWorktree, since head mode mounts the host directory directly.
		BranchStrategy: &sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead},
		// Copy node_modules into the worktree so the sandbox skips a cold install;
		// the hook below still runs npm install as a safety net.
		CopyToWorktree: []string{"node_modules"},
		Hooks:          agentgopher.Hooks{SandboxOnSandboxReady: []agentgopher.SandboxHook{{Command: "npm install"}}},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
