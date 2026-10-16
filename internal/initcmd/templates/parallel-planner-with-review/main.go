// Parallel planner with review: plan, then per issue implement and review in a
// shared sandbox, all issues in parallel, then merge.
// Run it from the repo root with: go run -C .agentgopher .
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/e11en/agentgopher"
	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/docker"
	"github.com/e11en/agentgopher/sandbox/kubernetes"
)

const maxIterations = 10

type issue struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Branch string `json:"branch"`
}

type plan struct {
	Issues []issue `json:"issues"`
}

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

		// Phase 1: plan. Opus reads the backlog and emits a <plan> JSON block.
		planned, err := agentgopher.Run(ctx, agentgopher.RunOptions{
			// Run with go run -C .agentgopher, so the repo root is one level up.
			Cwd:           "..",
			Hooks:         hooks,
			Sandbox:       sandboxProvider(),
			Name:          "planner",
			MaxIterations: 1,
			Agent:         agent.ClaudeCode("claude-opus-4-8", nil),
			PromptFile:    "./plan-prompt.md",
			Output:        agentgopher.OutputString("plan"),
		})
		if err != nil {
			return err
		}
		var p plan
		if err := json.Unmarshal([]byte(planned.Output.(string)), &p); err != nil {
			return fmt.Errorf("planner output is not valid JSON: %w", err)
		}
		if len(p.Issues) == 0 {
			fmt.Println("No unblocked issues to work on. Exiting.")
			break
		}
		fmt.Printf("Planning complete. %d issue(s) to work in parallel:\n", len(p.Issues))
		for _, is := range p.Issues {
			fmt.Printf("  %s: %s -> %s\n", is.ID, is.Title, is.Branch)
		}

		// Phase 2: execute, one agent per issue on its own branch. A failing
		// agent does not cancel the others.
		completed := execute(ctx, p.Issues, hooks, copyToWorktree)
		if len(completed) == 0 {
			fmt.Println("No commits produced. Nothing to merge.")
			continue
		}

		// Phase 3: merge every branch that produced commits.
		branches := make([]string, 0, len(completed))
		issues := make([]string, 0, len(completed))
		for _, is := range completed {
			branches = append(branches, "- "+is.Branch)
			issues = append(issues, "- "+is.ID+": "+is.Title)
		}
		if _, err := agentgopher.Run(ctx, agentgopher.RunOptions{
			// Run with go run -C .agentgopher, so the repo root is one level up.
			Cwd:           "..",
			Hooks:         hooks,
			Sandbox:       sandboxProvider(),
			Name:          "merger",
			MaxIterations: 1,
			Agent:         agent.ClaudeCode("claude-sonnet-4-6", nil),
			PromptFile:    "./merge-prompt.md",
			PromptArgs:    map[string]string{"BRANCHES": strings.Join(branches, "\n"), "ISSUES": strings.Join(issues, "\n")},
		}); err != nil {
			return err
		}
		fmt.Println("\nBranches merged.")
	}
	fmt.Println("\nAll done.")
	return nil
}

// execute runs implementer then reviewer per issue, each pair in its own
// sandbox, all issues concurrently. Returns the issues whose branch received commits.
func execute(ctx context.Context, issues []issue, hooks agentgopher.Hooks, copyToWorktree []string) []issue {
	var mu sync.Mutex
	var wg sync.WaitGroup
	var completed []issue
	for _, is := range issues {
		wg.Add(1)
		go func(is issue) {
			defer wg.Done()
			ok, err := implementAndReview(ctx, is, hooks, copyToWorktree)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  x %s (%s) failed: %v\n", is.ID, is.Branch, err)
				return
			}
			if ok {
				mu.Lock()
				completed = append(completed, is)
				mu.Unlock()
			}
		}(is)
	}
	wg.Wait()
	fmt.Printf("\nExecution complete. %d branch(es) with commits.\n", len(completed))
	return completed
}

func implementAndReview(ctx context.Context, is issue, hooks agentgopher.Hooks, copyToWorktree []string) (bool, error) {
	sb, err := agentgopher.CreateSandbox(ctx, agentgopher.CreateSandboxOptions{
		// Run with go run -C .agentgopher, so the repo root is one level up.
		Cwd:    "..",
		Branch: is.Branch, Sandbox: sandboxProvider(), Hooks: hooks, CopyToWorktree: copyToWorktree,
	})
	if err != nil {
		return false, err
	}
	defer func() { _, _ = sb.Close(ctx) }()

	implement, err := sb.Run(ctx, agentgopher.SandboxRunOptions{
		Name: "implementer", MaxIterations: 100,
		Agent:      agent.ClaudeCode("claude-sonnet-4-6", nil),
		PromptFile: "./implement-prompt.md",
		PromptArgs: map[string]string{"TASK_ID": is.ID, "ISSUE_TITLE": is.Title, "BRANCH": is.Branch},
	})
	if err != nil {
		return false, err
	}
	if len(implement.Commits) == 0 {
		return false, nil
	}
	review, err := sb.Run(ctx, agentgopher.SandboxRunOptions{
		Name: "reviewer", MaxIterations: 1,
		Agent:      agent.ClaudeCode("claude-sonnet-4-6", nil),
		PromptFile: "./review-prompt.md",
		PromptArgs: map[string]string{"BRANCH": is.Branch},
	})
	if err != nil {
		return false, err
	}
	return len(implement.Commits)+len(review.Commits) > 0, nil
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
