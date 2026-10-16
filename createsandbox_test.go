package agentgopher

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/internal/testrepo"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

func TestCreateSandboxRunsTwiceOnTheSameWorktreeAndCloses(t *testing.T) {
	repo := testrepo.Init(t)
	sb, err := CreateSandbox(context.Background(), CreateSandboxOptions{Branch: "feature/x", Sandbox: nosandbox.New(nil), Cwd: repo,
		Hooks: Hooks{SandboxOnSandboxReady: []SandboxHook{{Command: "touch hooked"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if sb.Branch != "feature/x" || !strings.Contains(sb.WorktreePath, filepath.Join(".agentgopher", "worktrees")) {
		t.Fatalf("sb = %+v", sb)
	}
	if _, err := os.Stat(filepath.Join(sb.WorktreePath, "hooked")); err != nil {
		t.Fatal("onSandboxReady hook did not run in the worktree")
	}

	script := `echo "a line" >> notes.txt; git add -A; git commit -q -m "agent"; echo "RESULT <promise>COMPLETE</promise>"`
	r1, err := sb.Run(context.Background(), SandboxRunOptions{Agent: echoAgent{script: script}, Prompt: "one", Logging: &Logging{Type: LoggingFile, Path: filepath.Join(repo, "r1.log")}})
	if err != nil || len(r1.Commits) != 1 {
		t.Fatalf("r1 = %+v, %v", r1, err)
	}
	r2, err := sb.Run(context.Background(), SandboxRunOptions{Agent: echoAgent{script: script}, Prompt: "two"})
	if err != nil || len(r2.Commits) != 1 {
		t.Fatalf("r2 = %+v, %v", r2, err)
	}
	if res, err := sb.Exec(context.Background(), "wc -l < notes.txt", SandboxExecOptions{}); err != nil || strings.TrimSpace(res.Stdout) != "2" {
		t.Fatalf("exec = %+v, %v", res, err)
	}
	shas, _ := gitx.RevList(context.Background(), repo, "main", "refs/heads/feature/x")
	if len(shas) != 2 {
		t.Fatalf("branch has %d commits", len(shas))
	}

	cr, err := sb.Close(context.Background())
	if err != nil || cr.PreservedWorktreePath != "" {
		t.Fatalf("close = %+v, %v", cr, err)
	}
	if _, err := os.Stat(sb.WorktreePath); !os.IsNotExist(err) {
		t.Fatal("worktree not removed")
	}
	if _, err := sb.Close(context.Background()); err != nil {
		t.Fatal("second close must be a no-op")
	}
}

func TestCreateSandboxClosePreservesDirtyWorktree(t *testing.T) {
	repo := testrepo.Init(t)
	sb, err := CreateSandbox(context.Background(), CreateSandboxOptions{Branch: "dirty", Sandbox: nosandbox.New(nil), Cwd: repo})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(sb.WorktreePath, "untracked.txt"), []byte("x"), 0o644)
	cr, err := sb.Close(context.Background())
	if err != nil || cr.PreservedWorktreePath != sb.WorktreePath {
		t.Fatalf("close = %+v, %v", cr, err)
	}
}

func TestCreateSandboxInteractiveAndResumeGuards(t *testing.T) {
	repo := testrepo.Init(t)
	sb, err := CreateSandbox(context.Background(), CreateSandboxOptions{Branch: "tui", Sandbox: nosandbox.New(nil), Cwd: repo})
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close(context.Background())
	var out bytes.Buffer
	ir, err := sb.Interactive(context.Background(), SandboxInteractiveOptions{Agent: tuiAgent{}, Prompt: "hello", Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out})
	if err != nil || ir.ExitCode != 3 || len(ir.Commits) != 1 {
		t.Fatalf("ir = %+v, %v", ir, err)
	}
	r, err := sb.Run(context.Background(), SandboxRunOptions{Agent: echoAgent{script: `echo "RESULT <promise>COMPLETE</promise>"`}, Prompt: "x", Logging: &Logging{Type: LoggingFile, Path: filepath.Join(repo, "r.log")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resume(context.Background(), "again", nil); err == nil || !strings.Contains(err.Error(), "no session") {
		t.Fatalf("resume: %v", err)
	}
	_, err = sb.Run(context.Background(), SandboxRunOptions{Agent: echoAgent{script: "true"}, Prompt: "x", ResumeSession: "s", MaxIterations: 2})
	if err == nil || !strings.Contains(err.Error(), "MaxIterations > 1") {
		t.Fatalf("validation: %v", err)
	}
	_ = sandbox.KindNone
}

func TestWorktreeCreateSandboxLeavesWorktreeOnClose(t *testing.T) {
	repo := testrepo.Init(t)
	wt, err := CreateWorktree(context.Background(), CreateWorktreeOptions{Cwd: repo, BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: "wt-branch"}})
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close(context.Background())
	sb, err := wt.CreateSandbox(context.Background(), WorktreeCreateSandboxOptions{Sandbox: nosandbox.New(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if sb.WorktreePath != wt.Path {
		t.Fatalf("sandbox must reuse the worktree: %q vs %q", sb.WorktreePath, wt.Path)
	}
	if _, err := sb.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatal("worktree-backed sandbox close must keep the worktree")
	}
}
