package orchestrator

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/internal/sandboxtest"
	"github.com/e11en/agentgopher/internal/testrepo"
	"github.com/e11en/agentgopher/sandbox"
)

// shellAgent runs its Command as the "agent" inside the sandbox repo.
type shellAgent struct{ command string }

func (shellAgent) Name() string           { return "shell-agent" }
func (shellAgent) Env() map[string]string { return nil }
func (shellAgent) CaptureSessions() bool  { return false }
func (a shellAgent) BuildPrintCommand(agent.CommandOptions) agent.PrintCommand {
	return agent.PrintCommand{Command: a.command}
}
func (shellAgent) ParseStreamLine(line string) []agent.StreamEvent {
	if strings.HasPrefix(line, "RESULT ") {
		return []agent.StreamEvent{{Type: agent.EventResult, Text: strings.TrimPrefix(line, "RESULT ")}}
	}
	return []agent.StreamEvent{{Type: agent.EventText, Text: line}}
}

func TestIsolatedIterationSyncsInRunsAgentAndMergesBack(t *testing.T) {
	repo := testrepo.Init(t)
	log := &bytes.Buffer{}
	o := Options{
		HostRepoDir:    repo,
		Iterations:     1,
		Prompt:         "work",
		Provider:       shellAgent{command: `test -f README.md && echo made > made.txt && git add . && git commit -q -m "agent commit" && echo "RESULT <promise>COMPLETE</promise>"`},
		Sandbox:        sandboxtest.Isolated(),
		BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead},
		Display:        display.NewWriter(log),
		IdleTimeout:    10 * time.Second,
	}
	res, err := Orchestrate(context.Background(), o)
	if err != nil {
		t.Fatalf("err = %v\nlog:\n%s", err, log.String())
	}
	if res.CompletionSignal == "" || len(res.Commits) != 1 || res.Branch != "main" {
		t.Fatalf("res = %+v\nlog:\n%s", res, log.String())
	}
	if _, err := os.Stat(filepath.Join(repo, "made.txt")); err != nil {
		t.Fatal("agent commit did not reach the host")
	}
	for _, want := range []string{"Syncing repo into sandbox", "Syncing 1 commit to host"} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("missing %q in log:\n%s", want, log.String())
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(repo, ".agentgopher", "worktrees")); len(entries) != 0 {
		t.Fatalf("worktree not cleaned up: %v", entries)
	}
}

func TestIsolatedIterationCopiesPathsIntoSandbox(t *testing.T) {
	repo := testrepo.Init(t)
	os.MkdirAll(filepath.Join(repo, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(repo, "node_modules", "x", "index.js"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("node_modules\n"), 0o644)
	gitx.Run(context.Background(), repo, "add", ".gitignore")
	gitx.Run(context.Background(), repo, "commit", "-q", "-m", "ignore")
	log := &bytes.Buffer{}
	o := Options{
		HostRepoDir:    repo,
		Iterations:     1,
		Prompt:         "work",
		Provider:       shellAgent{command: `test -f node_modules/x/index.js && echo "RESULT <promise>COMPLETE</promise>" || echo "RESULT missing"`},
		Sandbox:        sandboxtest.Isolated(),
		BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead},
		CopyToWorktree: []string{"node_modules", "does-not-exist"},
		Display:        display.NewWriter(log),
		IdleTimeout:    10 * time.Second,
	}
	res, err := Orchestrate(context.Background(), o)
	if err != nil || res.CompletionSignal == "" {
		t.Fatalf("res=%+v err=%v\nlog:\n%s", res, err, log.String())
	}
}

func TestIsolatedIterationNamedBranch(t *testing.T) {
	repo := testrepo.Init(t)
	log := &bytes.Buffer{}
	o := Options{
		HostRepoDir:    repo,
		Iterations:     1,
		Prompt:         "work",
		Branch:         "feat/agent",
		Provider:       shellAgent{command: `echo x > x.txt && git add . && git commit -q -m "on branch" && echo "RESULT <promise>COMPLETE</promise>"`},
		Sandbox:        sandboxtest.Isolated(),
		BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: "feat/agent"},
		Display:        display.NewWriter(log),
		IdleTimeout:    10 * time.Second,
	}
	res, err := Orchestrate(context.Background(), o)
	if err != nil || len(res.Commits) != 1 || res.Branch != "feat/agent" {
		t.Fatalf("res=%+v err=%v\nlog:\n%s", res, err, log.String())
	}
	if subject, _ := gitx.Run(context.Background(), repo, "log", "-1", "--format=%s", "feat/agent"); subject != "on branch" {
		t.Fatalf("subject = %q", subject)
	}
	if b, _ := gitx.CurrentBranch(context.Background(), repo); b != "main" {
		t.Fatalf("host branch changed to %q", b)
	}
}
