package agentgopher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/testrepo"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

func TestCreateWorktreeNamedBranchRunTwiceAndClose(t *testing.T) {
	repo := testrepo.Init(t)
	wt, err := CreateWorktree(context.Background(), CreateWorktreeOptions{
		BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: "feat/wt"},
		Cwd:            repo,
		Hooks:          Hooks{HostOnWorktreeReady: []HostHook{{Command: "touch ready.txt"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if wt.Branch != "feat/wt" || !strings.HasSuffix(wt.Path, filepath.Join(".agentgopher", "worktrees", "feat-wt")) {
		t.Fatalf("wt = %+v", wt)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "ready.txt")); err != nil {
		t.Fatal("onWorktreeReady hook did not run")
	}
	script := `n=$(ls *.step 2>/dev/null | wc -l); touch "$n.step"; git add .; git commit -q -m step; echo "RESULT <promise>COMPLETE</promise>"`
	for i := 0; i < 2; i++ {
		res, err := wt.Run(context.Background(), WorktreeRunOptions{Agent: echoAgent{script: script}, Sandbox: nosandbox.New(nil), Prompt: "go"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Branch != "feat/wt" || len(res.Commits) != 1 {
			t.Fatalf("run %d: %+v", i, res)
		}
	}
	if err := wt.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt.Path); err == nil {
		t.Fatal("clean worktree must be removed on Close")
	}
	if err := wt.Close(context.Background()); err != nil {
		t.Fatal("Close must be idempotent")
	}
}

func TestCreateWorktreeMergeToHeadKeepsBranchAcrossRuns(t *testing.T) {
	repo := testrepo.Init(t)
	wt, err := CreateWorktree(context.Background(), CreateWorktreeOptions{BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead}, Cwd: repo})
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close(context.Background())
	script := `echo x >> log.txt; git add log.txt; git commit -q -m step; echo "RESULT <promise>COMPLETE</promise>"`
	for i := 0; i < 2; i++ {
		res, err := wt.Run(context.Background(), WorktreeRunOptions{Agent: echoAgent{script: script}, Sandbox: nosandbox.New(nil), Prompt: "go"})
		if err != nil || res.Branch != "main" {
			t.Fatalf("run %d: %+v, %v", i, res, err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(repo, "log.txt"))
	if strings.Count(string(b), "x") != 2 {
		t.Fatalf("main log.txt = %q", b)
	}
	if got := strings.TrimSpace(testrepo.Git(t, wt.Path, "rev-parse", "--abbrev-ref", "HEAD")); got != wt.Branch {
		t.Fatalf("worktree left on %q", got)
	}
}

func TestCreateWorktreeRejectsHead(t *testing.T) {
	_, err := CreateWorktree(context.Background(), CreateWorktreeOptions{BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchHead}, Cwd: testrepo.Init(t)})
	if err == nil || !strings.Contains(err.Error(), "head") {
		t.Fatalf("err = %v", err)
	}
}

func TestCloseKeepsDirtyWorktree(t *testing.T) {
	repo := testrepo.Init(t)
	wt, _ := CreateWorktree(context.Background(), CreateWorktreeOptions{BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: "dirty"}, Cwd: repo})
	os.WriteFile(filepath.Join(wt.Path, "wip.txt"), []byte("x"), 0o644)
	if err := wt.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatal("dirty worktree must be preserved")
	}
}
