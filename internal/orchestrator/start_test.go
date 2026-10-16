package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/internal/testrepo"
	"github.com/e11en/agentgopher/internal/worktree"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

func TestHeadModeBindMountPassesWorktreeAndGitMounts(t *testing.T) {
	repo := testrepo.Init(t)
	p := &fakeBindMountProvider{handles: []*fakeHandle{{script: []scriptedLine{{0, "RESULT " + DefaultCompletionSignal}}}}}
	o, _ := baseOptions(t)
	o.HostRepoDir = repo
	o.Sandbox = p
	o.Iterations = 1
	if _, err := Orchestrate(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	got := p.opts[0]
	if got.WorktreePath != repo || got.HostRepoPath != repo || len(got.Mounts) != 2 {
		t.Fatalf("opts = %+v", got)
	}
	if got.Mounts[0].SandboxPath != SandboxRepoDir || got.Mounts[1].HostPath != filepath.Join(repo, ".git") {
		t.Fatalf("mounts = %+v", got.Mounts)
	}
}

func TestMergeToHeadCreatesWorktreeMergesAndRemovesIt(t *testing.T) {
	repo := testrepo.Init(t)
	o := Options{
		HostRepoDir: repo, Iterations: 1, Prompt: "work", Provider: fakeProvider{}, Sandbox: nosandbox.New(nil),
		BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead}, Display: display.NewWriter(&bytes.Buffer{}),
	}
	// A shell "agent": commits a file in its cwd, then prints the signal.
	o.Provider = shellProvider{script: `echo agent > agent.txt && git add agent.txt && git commit -q -m "agent" && echo "RESULT ` + DefaultCompletionSignal + `"`}
	res, err := Orchestrate(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "main" || len(res.Commits) != 1 || res.PreservedWorktreePath != "" {
		t.Fatalf("res = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(repo, "agent.txt")); err != nil {
		t.Fatal("not merged into main")
	}
	entries, _ := os.ReadDir(filepath.Join(repo, worktree.Dir))
	if len(entries) != 0 {
		t.Fatalf("worktree not removed: %v", entries)
	}
}

func TestNamedBranchKeepsCommitsOnBranch(t *testing.T) {
	repo := testrepo.Init(t)
	o := Options{
		HostRepoDir: repo, Iterations: 1, Prompt: "work", Sandbox: nosandbox.New(nil), Display: display.NewWriter(&bytes.Buffer{}),
		BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: "feat/agent"},
		Provider:       shellProvider{script: `echo x > f.txt && git add f.txt && git commit -q -m "agent" && echo "RESULT ` + DefaultCompletionSignal + `"`},
	}
	res, err := Orchestrate(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "feat/agent" || len(res.Commits) != 1 {
		t.Fatalf("res = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(repo, "f.txt")); err == nil {
		t.Fatal("main must not change")
	}
}

func TestDirtyWorktreeIsPreservedOnSuccessAndFailure(t *testing.T) {
	repo := testrepo.Init(t)
	base := Options{
		HostRepoDir: repo, Iterations: 1, Prompt: "work", Sandbox: nosandbox.New(nil), Display: display.NewWriter(&bytes.Buffer{}),
		BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead},
	}
	o := base
	o.Provider = shellProvider{script: `echo dirty > dirty.txt && echo "RESULT ` + DefaultCompletionSignal + `"`}
	res, err := Orchestrate(context.Background(), o)
	if err != nil || res.PreservedWorktreePath == "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, statErr := os.Stat(filepath.Join(res.PreservedWorktreePath, "dirty.txt")); statErr != nil {
		t.Fatal("preserved worktree missing dirty file")
	}

	o = base
	o.Provider = shellProvider{script: `echo dirty > dirty.txt && exit 3`}
	_, err = Orchestrate(context.Background(), o)
	var aerr *AgentError
	if !errors.As(err, &aerr) || aerr.PreservedWorktreePath == "" {
		t.Fatalf("err = %v", err)
	}
}

func TestCopyToWorktreeAndWorktreeReadyHook(t *testing.T) {
	repo := testrepo.Init(t)
	os.MkdirAll(filepath.Join(repo, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(repo, "node_modules", "x", "i.js"), []byte("1"), 0o644)
	o := Options{
		HostRepoDir: repo, Iterations: 1, Prompt: "work", Sandbox: nosandbox.New(nil), Display: display.NewWriter(&bytes.Buffer{}),
		BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead}, CopyToWorktree: []string{"node_modules"},
		Provider: shellProvider{script: `test -f node_modules/x/i.js && test -f hook.txt && echo "RESULT ` + DefaultCompletionSignal + `"`},
	}
	o.Hooks.HostOnWorktreeReady = []lifecycle.HostHook{{Command: "touch hook.txt"}}
	res, err := Orchestrate(context.Background(), o)
	if err != nil || res.CompletionSignal == "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestIsolatedProviderRejected(t *testing.T) {
	o, _ := baseOptions(t)
	o.Iterations = 1
	o.Sandbox = isolatedStub{}
	_, err := Orchestrate(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "phase 4") {
		t.Fatalf("err = %v", err)
	}
}
