// Sequential reviewer: implement one issue on its own branch, then review it
// in the same sandbox. Stops when the implementer makes no commits.
// Run it from the repo root with: go run -C .agentgopher .
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/e11en/agentgopher"
	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox/docker"
)

// maxIterations bounds the number of implement-then-review cycles.
const maxIterations = 10

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	hooks := agentgopher.Hooks{SandboxOnSandboxReady: []agentgopher.SandboxHook{{Command: "npm install"}}}
	copyToWorktree := []string{"node_modules"}

	for i := 1; i <= maxIterations; i++ {
		fmt.Printf("\n=== Iteration %d/%d ===\n\n", i, maxIterations)
		branch := fmt.Sprintf("agentgopher/sequential-reviewer/%d", time.Now().UnixMilli())

		// One sandbox for both phases, so the reviewer sees the implementer's branch.
		sb, err := agentgopher.CreateSandbox(ctx, agentgopher.CreateSandboxOptions{
			Branch:         branch,
			Sandbox:        docker.New(nil),
			Hooks:          hooks,
			CopyToWorktree: copyToWorktree,
		})
		if err != nil {
			return err
		}
		stop, err := cycle(ctx, sb, branch)
		_, closeErr := sb.Close(ctx)
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if stop {
			break
		}
	}
	fmt.Println("\nAll done.")
	return nil
}

// cycle runs implementer then reviewer; stop is true when there was nothing to implement.
func cycle(ctx context.Context, sb *agentgopher.Sandbox, branch string) (stop bool, err error) {
	// One iteration so each outer pass implements a single issue, then hands it to the reviewer.
	implement, err := sb.Run(ctx, agentgopher.SandboxRunOptions{
		Name:          "implementer",
		MaxIterations: 1,
		Agent:         agent.ClaudeCode("claude-sonnet-4-6", nil),
		PromptFile:    "./.agentgopher/implement-prompt.md",
	})
	if err != nil {
		return false, err
	}
	if len(implement.Commits) == 0 {
		fmt.Println("Implementation agent made no commits. Stopping.")
		return true, nil
	}
	fmt.Printf("\nImplementation complete on branch: %s (%d commits)\n", branch, len(implement.Commits))

	_, err = sb.Run(ctx, agentgopher.SandboxRunOptions{
		Name:          "reviewer",
		MaxIterations: 1,
		Agent:         agent.ClaudeCode("claude-sonnet-4-6", nil),
		PromptFile:    "./.agentgopher/review-prompt.md",
		PromptArgs:    map[string]string{"BRANCH": branch},
	})
	if err != nil {
		return false, err
	}
	fmt.Println("\nReview complete.")
	return false, nil
}
