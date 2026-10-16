// Command docker runs Claude Code in a Docker container on a temp branch and
// merges the result back. Build the image first:
//
//	docker build -t agentgopher:example --build-arg AGENT_UID=$(id -u) --build-arg AGENT_GID=$(id -g) examples/docker
//
// Usage: go run ./examples/docker [path-to-repo]
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
	"github.com/e11en/agentgopher/sandbox/docker"
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
		Name:           "worker",
		Agent:          agent.ClaudeCode(agent.DefaultModel, nil),
		Sandbox:        docker.New(&docker.Options{ImageName: "agentgopher:example"}),
		Cwd:            repo,
		PromptFile:     promptFile,
		BranchStrategy: &sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "run failed:", err)
		os.Exit(1)
	}
	fmt.Printf("done: branch=%s commits=%d preserved=%q log=%s\n", res.Branch, len(res.Commits), res.PreservedWorktreePath, res.LogFilePath)
}
