package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/internal/testrepo"
	"github.com/e11en/agentgopher/sandbox"
)

// synced returns a host repo and a sandbox handle holding a clone of it.
func synced(t *testing.T) (string, sandbox.IsolatedHandle) {
	t.Helper()
	repo := testrepo.Init(t)
	h := newIsolated(t)
	if err := In(context.Background(), h, repo, h.WorktreePath(), "main", DefaultInTimeout); err != nil {
		t.Fatal(err)
	}
	return repo, h
}

func hostLog(t *testing.T, repo string) []string {
	t.Helper()
	out, err := gitx.Run(context.Background(), repo, "log", "--format=%s")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(out, "\n")
}

func TestCountCommitsToSyncFallsBackToHostHead(t *testing.T) {
	repo, h := synced(t)
	ctx := context.Background()
	hostHead, _ := gitx.Head(ctx, repo)
	n, err := CountCommitsToSync(ctx, h, h.WorktreePath(), hostHead)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	commitFile(t, h.WorktreePath(), "one.txt", "1", "one")
	commitFile(t, h.WorktreePath(), "two.txt", "2", "two")
	if n, _ = CountCommitsToSync(ctx, h, h.WorktreePath(), hostHead); n != 2 {
		t.Fatalf("n = %d", n)
	}
}

func TestCountCommitsToSyncUsesBaseRefWhenSet(t *testing.T) {
	repo, h := synced(t)
	ctx := context.Background()
	hostHead, _ := gitx.Head(ctx, repo)
	commitFile(t, h.WorktreePath(), "one.txt", "1", "one")
	shipped, _ := gitx.Head(ctx, h.WorktreePath())
	gitx.Run(ctx, h.WorktreePath(), "update-ref", BaseRef, shipped)
	commitFile(t, h.WorktreePath(), "two.txt", "2", "two")
	// hostHead is stale on purpose: the ref must win.
	if n, _ := CountCommitsToSync(ctx, h, h.WorktreePath(), hostHead); n != 1 {
		t.Fatalf("n = %d", n)
	}
}

func TestCountCommitsToSyncDegradesToZero(t *testing.T) {
	_, h := synced(t)
	if n, err := CountCommitsToSync(context.Background(), h, h.WorktreePath(), "not-a-sha"); n != 0 || err != nil {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestOutShipsOneCommit(t *testing.T) {
	repo, h := synced(t)
	commitFile(t, h.WorktreePath(), "new.txt", "hello", "add new file")
	if err := Out(context.Background(), h, repo, h.WorktreePath()); err != nil {
		t.Fatal(err)
	}
	if log := hostLog(t, repo); log[0] != "add new file" {
		t.Fatalf("log = %v", log)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "new.txt")); string(b) != "hello" {
		t.Fatalf("content = %q", b)
	}
	if _, err := os.Stat(filepath.Join(repo, PatchesDir)); !os.IsNotExist(err) {
		t.Fatal("patch artifacts must be removed after success")
	}
}

func TestOutShipsMultipleCommitsInOrderWithMetadata(t *testing.T) {
	repo, h := synced(t)
	ctx := context.Background()
	gitx.Run(ctx, h.WorktreePath(), "config", "user.name", "Agent Smith")
	gitx.Run(ctx, h.WorktreePath(), "config", "user.email", "agent@example.com")
	commitFile(t, h.WorktreePath(), "1.txt", "1", "first")
	commitFile(t, h.WorktreePath(), "2.txt", "2", "second")
	commitFile(t, h.WorktreePath(), "3.txt", "3", "third")
	if err := Out(ctx, h, repo, h.WorktreePath()); err != nil {
		t.Fatal(err)
	}
	log := hostLog(t, repo)
	if len(log) != 4 || log[0] != "third" || log[1] != "second" || log[2] != "first" {
		t.Fatalf("log = %v", log)
	}
	author, _ := gitx.Run(ctx, repo, "log", "-1", "--format=%an <%ae>")
	if author != "Agent Smith <agent@example.com>" {
		t.Fatalf("author = %q", author)
	}
}

func TestOutNoNewCommitsIsNoop(t *testing.T) {
	repo, h := synced(t)
	before := hostLog(t, repo)
	if err := Out(context.Background(), h, repo, h.WorktreePath()); err != nil {
		t.Fatal(err)
	}
	if after := hostLog(t, repo); len(after) != len(before) {
		t.Fatalf("log changed: %v", after)
	}
	if _, err := os.Stat(filepath.Join(repo, PatchesDir)); !os.IsNotExist(err) {
		t.Fatal("no patch dir expected")
	}
}

func TestOutSkipsHeaderOnlyMergeCommitPatches(t *testing.T) {
	repo, h := synced(t)
	ctx := context.Background()
	wt := h.WorktreePath()
	gitx.Run(ctx, wt, "checkout", "-q", "-b", "side")
	commitFile(t, wt, "side.txt", "s", "side work")
	gitx.Run(ctx, wt, "checkout", "-q", "main")
	commitFile(t, wt, "main.txt", "m", "main work")
	if _, err := gitx.Run(ctx, wt, "merge", "--no-ff", "-q", "-m", "merge side", "side"); err != nil {
		t.Fatal(err)
	}
	if err := Out(ctx, h, repo, wt); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"side.txt", "main.txt"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Fatalf("%s missing on host", f)
		}
	}
}

func TestOutAbortsStaleAmSession(t *testing.T) {
	repo, h := synced(t)
	ctx := context.Background()
	// Leave a half-applied am session behind on the host.
	os.MkdirAll(filepath.Join(repo, ".git", "rebase-apply"), 0o755)
	os.WriteFile(filepath.Join(repo, ".git", "rebase-apply", "applying"), nil, 0o644)
	commitFile(t, h.WorktreePath(), "a.txt", "a", "after stale")
	if err := Out(ctx, h, repo, h.WorktreePath()); err != nil {
		t.Fatal(err)
	}
	if log := hostLog(t, repo); log[0] != "after stale" {
		t.Fatalf("log = %v", log)
	}
}

func TestOutTwiceOnSameSandboxShipsOnlyNewCommits(t *testing.T) {
	repo, h := synced(t)
	ctx := context.Background()
	commitFile(t, h.WorktreePath(), "1.txt", "1", "first")
	if err := Out(ctx, h, repo, h.WorktreePath()); err != nil {
		t.Fatal(err)
	}
	commitFile(t, h.WorktreePath(), "2.txt", "2", "second")
	if err := Out(ctx, h, repo, h.WorktreePath()); err != nil {
		t.Fatal(err)
	}
	log := hostLog(t, repo)
	if len(log) != 3 || log[0] != "second" || log[1] != "first" {
		t.Fatalf("log = %v", log)
	}
	// Third call with nothing new: no-op, no artifacts.
	if err := Out(ctx, h, repo, h.WorktreePath()); err != nil {
		t.Fatal(err)
	}
	if after := hostLog(t, repo); len(after) != 3 {
		t.Fatalf("log = %v", after)
	}
	hostHead, _ := gitx.Head(ctx, repo)
	if n, _ := CountCommitsToSync(ctx, h, h.WorktreePath(), hostHead); n != 0 {
		t.Fatalf("count after sync = %d", n)
	}
}

func TestOutShipsUncommittedChanges(t *testing.T) {
	repo, h := synced(t)
	wt := h.WorktreePath()
	os.WriteFile(filepath.Join(wt, "README.md"), []byte("hi\nstaged\n"), 0o644)
	gitx.Run(context.Background(), wt, "add", "README.md")
	os.WriteFile(filepath.Join(wt, "README.md"), []byte("hi\nstaged\nunstaged\n"), 0o644)
	if err := Out(context.Background(), h, repo, wt); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "README.md")); string(b) != "hi\nstaged\nunstaged\n" {
		t.Fatalf("content = %q", b)
	}
	if log := hostLog(t, repo); len(log) != 1 {
		t.Fatalf("no commit expected, log = %v", log)
	}
}

func TestOutShipsUntrackedFilesIncludingNewDirectories(t *testing.T) {
	repo, h := synced(t)
	wt := h.WorktreePath()
	os.MkdirAll(filepath.Join(wt, "src", "deep"), 0o755)
	os.WriteFile(filepath.Join(wt, "src", "deep", "new.go"), []byte("package deep"), 0o644)
	os.WriteFile(filepath.Join(wt, "top.txt"), []byte("t"), 0o644)
	if err := Out(context.Background(), h, repo, wt); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"src/deep/new.go", "top.txt"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Fatalf("%s missing on host", f)
		}
	}
}

func TestOutShipsCommitsDiffAndUntrackedTogether(t *testing.T) {
	repo, h := synced(t)
	wt := h.WorktreePath()
	commitFile(t, wt, "c.txt", "c", "committed")
	os.WriteFile(filepath.Join(wt, "c.txt"), []byte("c-modified"), 0o644)
	os.WriteFile(filepath.Join(wt, "u.txt"), []byte("u"), 0o644)
	if err := Out(context.Background(), h, repo, wt); err != nil {
		t.Fatal(err)
	}
	if log := hostLog(t, repo); log[0] != "committed" {
		t.Fatalf("log = %v", log)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "c.txt")); string(b) != "c-modified" {
		t.Fatalf("c.txt = %q", b)
	}
	if _, err := os.Stat(filepath.Join(repo, "u.txt")); err != nil {
		t.Fatal("u.txt missing")
	}
	if _, err := os.Stat(filepath.Join(repo, PatchesDir)); !os.IsNotExist(err) {
		t.Fatal("artifacts must be cleaned up")
	}
}

func TestOutPreservesArtifactsWhenAmFails(t *testing.T) {
	repo, h := synced(t)
	ctx := context.Background()
	wt := h.WorktreePath()
	// Conflict: both sides change the same line of README.md.
	commitFile(t, repo, "README.md", "host version\n", "host change")
	commitFile(t, wt, "README.md", "sandbox version\n", "sandbox change")
	os.WriteFile(filepath.Join(wt, "extra.txt"), []byte("x"), 0o644)

	if err := Out(ctx, h, repo, wt); err != nil {
		t.Fatalf("a failed apply is reported, not returned: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(repo, PatchesDir))
	if err != nil || len(entries) != 1 {
		t.Fatalf("patch dir entries = %v, %v", entries, err)
	}
	patchDir := filepath.Join(repo, PatchesDir, entries[0].Name())
	if _, err := os.Stat(filepath.Join(patchDir, "untracked", "extra.txt")); err != nil {
		t.Fatal("untracked artifact missing")
	}
	// The base ref must not advance: the commit never landed.
	res, _ := h.Exec(ctx, "git rev-parse --verify --quiet "+BaseRef, sandbox.ExecOptions{Cwd: wt})
	sandboxHead, _ := gitx.Head(ctx, wt)
	if strings.TrimSpace(res.Stdout) == sandboxHead {
		t.Fatal("base ref advanced after a failed git am")
	}
	// Leave the host usable for the next test run.
	gitx.Run(ctx, repo, "am", "--abort")
}

func TestOutPreservesDiffWhenApplyFails(t *testing.T) {
	repo, h := synced(t)
	ctx := context.Background()
	wt := h.WorktreePath()
	// Uncommitted change in the sandbox that cannot apply on the host.
	os.WriteFile(filepath.Join(wt, "README.md"), []byte("sandbox edit\n"), 0o644)
	commitFile(t, repo, "README.md", "host rewrote everything\n", "host change")

	if err := Out(ctx, h, repo, wt); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(repo, PatchesDir))
	if len(entries) != 1 {
		t.Fatalf("entries = %v", entries)
	}
	if _, err := os.Stat(filepath.Join(repo, PatchesDir, entries[0].Name(), "changes.patch")); err != nil {
		t.Fatal("changes.patch not preserved")
	}
}
