// Command simple runs Claude Code once on the current repo, on the host.
// Usage: go run ./examples/simple [path-to-repo]
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/e11en/agentgopher"
	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	repo := "."
	if len(os.Args) > 1 {
		repo = os.Args[1]
	}
	promptFile, _ := filepath.Abs("examples/simple/prompt.md")

	res, err := agentgopher.Run(ctx, agentgopher.RunOptions{
		Name:          "worker",
		Agent:         agent.ClaudeCode(agent.DefaultModel, nil),
		Sandbox:       nosandbox.New(nil),
		Cwd:           repo,
		PromptFile:    promptFile,
		MaxIterations: 1,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "run failed:", err)
		os.Exit(1)
	}
	fmt.Printf("done: signal=%q commits=%d log=%s\n", res.CompletionSignal, len(res.Commits), res.LogFilePath)
}
