// Command isolated runs Claude Code once in a Vercel or Daytona sandbox and
// merges the commits back into the current branch.
// Usage: go run ./examples/isolated [vercel|daytona|agent-sandbox] [path-to-repo]
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/e11en/agentgopher"
	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/agentsandbox"
	"github.com/e11en/agentgopher/sandbox/daytona"
	"github.com/e11en/agentgopher/sandbox/vercel"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	providerName, repo := "vercel", "."
	if len(os.Args) > 1 {
		providerName = os.Args[1]
	}
	if len(os.Args) > 2 {
		repo = os.Args[2]
	}
	var provider sandbox.Provider
	switch providerName {
	case "daytona":
		provider = daytona.New(nil)
	case "agent-sandbox":
		provider = agentsandbox.New(nil)
	default:
		provider = vercel.New(nil)
	}
	promptFile, _ := filepath.Abs("examples/simple/prompt.md")

	res, err := agentgopher.Run(ctx, agentgopher.RunOptions{
		Name:       "worker",
		Agent:      agent.ClaudeCode(agent.DefaultModel, nil),
		Sandbox:    provider,
		Cwd:        repo,
		PromptFile: promptFile,
		Hooks: agentgopher.Hooks{SandboxOnSandboxReady: []agentgopher.SandboxHook{
			{Command: "command -v claude >/dev/null || npm install -g @anthropic-ai/claude-code", Timeout: 0},
		}},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "run failed:", err)
		os.Exit(1)
	}
	fmt.Printf("done: signal=%q commits=%d branch=%s log=%s\n", res.CompletionSignal, len(res.Commits), res.Branch, res.LogFilePath)
}
