package worktree

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/testrepo"
)

var ctx = context.Background()

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{"My Agent": "my-agent", "a.b/c": "a-b-c", "worker": "worker"}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Errorf("SanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGenerateTempBranchName(t *testing.T) {
	plain := regexp.MustCompile(`^agentgopher/\d{8}-\d{6}-[0-9a-f]{6}$`)
	named := regexp.MustCompile(`^agentgopher/my-worker/\d{8}-\d{6}-[0-9a-f]{6}$`)
	if n := GenerateTempBranchName(""); !plain.MatchString(n) {
		t.Fatalf("got %q", n)
	}
	if n := GenerateTempBranchName("My Worker"); !named.MatchString(n) {
		t.Fatalf("got %q", n)
	}
	first, second := GenerateTempBranchName(""), GenerateTempBranchName("")
	if first == second {
		t.Fatal("two names in the same second must differ")
	}
}

func TestCreateTempWorktree(t *testing.T) {
	repo := testrepo.Init(t)
	info, err := CreateTemp(ctx, repo, "worker")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(info.Path, filepath.Join(repo, ".agentgopher", "worktrees", "agentgopher-worker-")) {
		t.Fatalf("Path = %q", info.Path)
	}
	if got := strings.TrimSpace(testrepo.Git(t, info.Path, "rev-parse", "--abbrev-ref", "HEAD")); got != info.Branch {
		t.Fatalf("worktree on %q, want %q", got, info.Branch)
	}
}

func TestCreateNamedBranchNewAndExisting(t *testing.T) {
	repo := testrepo.Init(t)
	info, err := Create(ctx, repo, "feat/x", "")
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != filepath.Join(repo, ".agentgopher", "worktrees", "feat-x") || info.Branch != "feat/x" {
		t.Fatalf("info = %+v", info)
	}
	// Existing branch in another repo state: remove worktree, create again reuses the branch.
	info.Unlock()
	if err := Remove(ctx, info.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(ctx, repo, "feat/x", ""); err != nil {
		t.Fatal(err)
	}
}

func TestCreateFromBaseBranchOnlyWhenNew(t *testing.T) {
	repo := testrepo.Init(t)
	testrepo.Git(t, repo, "branch", "base")
	testrepo.Commit(t, repo, "later.txt", "x", "later on main")
	info, err := Create(ctx, repo, "from-base", "base")
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(info.Path, "later.txt")); statErr == nil {
		t.Fatal("branch must start from base, not main")
	}
	info.Unlock()
	Remove(ctx, info.Path)
	testrepo.Git(t, repo, "branch", "-f", "from-base", "main")
	info, err = Create(ctx, repo, "from-base", "base")
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(info.Path, "later.txt")); statErr != nil {
		t.Fatal("existing branch must be used as-is, baseBranch ignored")
	}
}

func TestCreateCollisionWithMainWorkingTree(t *testing.T) {
	repo := testrepo.Init(t)
	_, err := Create(ctx, repo, "main", "")
	var werr *Error
	if !errors.As(err, &werr) || !strings.Contains(werr.Message, "already checked out") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateReusesManagedWorktree(t *testing.T) {
	repo := testrepo.Init(t)
	first, err := Create(ctx, repo, "reuse-me", "")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(first.Path, "dirty.txt"), []byte("x"), 0o644)
	first.Unlock()
	second, err := Create(ctx, repo, "reuse-me", "")
	if err != nil {
		t.Fatal(err)
	}
	if second.Path != first.Path {
		t.Fatalf("got %q, want %q", second.Path, first.Path)
	}
}

func TestHasUncommittedChanges(t *testing.T) {
	repo := testrepo.Init(t)
	if dirty, _ := HasUncommittedChanges(ctx, repo); dirty {
		t.Fatal("fresh repo must be clean")
	}
	os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x"), 0o644)
	if dirty, _ := HasUncommittedChanges(ctx, repo); !dirty {
		t.Fatal("untracked file must count as dirty")
	}
}

func TestRemoveDeletesDirectoryAndMetadata(t *testing.T) {
	repo := testrepo.Init(t)
	info, _ := CreateTemp(ctx, repo, "")
	if err := Remove(ctx, info.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(info.Path); err == nil {
		t.Fatal("directory still exists")
	}
	if strings.Contains(testrepo.Git(t, repo, "worktree", "list", "--porcelain"), info.Path) {
		t.Fatal("metadata still lists the worktree")
	}
}

func TestListWorktreesParsesDetachedHead(t *testing.T) {
	repo := testrepo.Init(t)
	info, _ := CreateTemp(ctx, repo, "")
	testrepo.Git(t, info.Path, "checkout", "-q", "--detach")
	entries, err := ListWorktrees(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range entries {
		if samePath(e.Path, info.Path) {
			found = true
			if e.Branch != "" {
				t.Fatalf("detached HEAD must have empty Branch, got %q", e.Branch)
			}
		}
	}
	if !found {
		t.Fatalf("entries = %+v", entries)
	}
	if _, ok := FindCollidingWorktree(entries, "nope", info.Path); !ok {
		t.Fatal("path fallback must match a detached worktree")
	}
}

func TestNoUpstreamTrackingConfigWritten(t *testing.T) {
	repo := testrepo.Init(t)
	testrepo.Git(t, repo, "config", "branch.autoSetupMerge", "always")
	testrepo.Git(t, repo, "remote", "add", "origin", repo)
	if _, err := Create(ctx, repo, "tracked", ""); err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(filepath.Join(repo, ".git", "config"))
	if strings.Contains(string(cfg), `[branch "tracked"]`) {
		t.Fatal("worktree add must not write upstream tracking config")
	}
}

func cloneWithOrigin(t *testing.T) (origin, clone string) {
	t.Helper()
	origin = testrepo.Init(t)
	clone = t.TempDir()
	testrepo.Git(t, clone, "clone", "-q", origin, ".")
	testrepo.Git(t, clone, "config", "user.name", "Test User")
	testrepo.Git(t, clone, "config", "user.email", "test@example.com")
	return origin, clone
}

func captureReuseLog(t *testing.T) *[]string {
	t.Helper()
	var lines []string
	old := reuseLog
	reuseLog = func(s string) { lines = append(lines, s) }
	t.Cleanup(func() { reuseLog = old })
	return &lines
}

func TestReuseFastForwardsCleanWorktree(t *testing.T) {
	origin, clone := cloneWithOrigin(t)
	testrepo.Git(t, origin, "branch", "feat")
	info, err := Create(ctx, clone, "feat", "")
	if err != nil {
		t.Fatal(err)
	}
	info.Unlock()
	testrepo.Git(t, origin, "checkout", "-q", "feat")
	testrepo.Commit(t, origin, "new.txt", "x", "moved on origin")
	logs := captureReuseLog(t)
	info, err = Create(ctx, clone, "feat", "")
	if err != nil {
		t.Fatal(err)
	}
	defer info.Unlock()
	if _, err := os.Stat(filepath.Join(info.Path, "new.txt")); err != nil {
		t.Fatalf("not fast-forwarded; logs=%q", *logs)
	}
}

func TestReusePreservesDivergedBranch(t *testing.T) {
	origin, clone := cloneWithOrigin(t)
	testrepo.Git(t, origin, "branch", "feat")
	info, _ := Create(ctx, clone, "feat", "")
	info.Unlock()
	testrepo.Commit(t, info.Path, "local.txt", "x", "unpushed")
	testrepo.Git(t, origin, "checkout", "-q", "feat")
	testrepo.Commit(t, origin, "remote.txt", "y", "moved on origin")
	logs := captureReuseLog(t)
	info, err := Create(ctx, clone, "feat", "")
	if err != nil {
		t.Fatal(err)
	}
	defer info.Unlock()
	if _, err := os.Stat(filepath.Join(info.Path, "local.txt")); err != nil {
		t.Fatal("unpushed commit lost")
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "diverged") {
		t.Fatalf("logs = %q", *logs)
	}
}

func TestReuseWithoutOriginIsNonFatal(t *testing.T) {
	repo := testrepo.Init(t)
	info, _ := Create(ctx, repo, "feat", "")
	info.Unlock()
	logs := captureReuseLog(t)
	info, err := Create(ctx, repo, "feat", "")
	if err != nil {
		t.Fatal(err)
	}
	info.Unlock()
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "Could not fetch") {
		t.Fatalf("logs = %q", *logs)
	}
}

func TestReuseSkipsDetachedHead(t *testing.T) {
	origin, clone := cloneWithOrigin(t)
	testrepo.Git(t, origin, "branch", "feat")
	info, _ := Create(ctx, clone, "feat", "")
	info.Unlock()
	testrepo.Git(t, info.Path, "checkout", "-q", "--detach")
	logs := captureReuseLog(t)
	if _, err := Create(ctx, clone, "feat", ""); err != nil {
		t.Fatal(err)
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "HEAD is not on") {
		t.Fatalf("logs = %q", *logs)
	}
}

func TestPruneRemovesOrphansKeepsActive(t *testing.T) {
	repo := testrepo.Init(t)
	active, _ := CreateTemp(ctx, repo, "keep")
	orphan := filepath.Join(repo, Dir, "orphan")
	os.MkdirAll(orphan, 0o755)
	os.WriteFile(filepath.Join(orphan, "junk"), []byte("x"), 0o644)
	if err := Prune(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Fatal("orphan not removed")
	}
	if _, err := os.Stat(active.Path); err != nil {
		t.Fatal("active worktree removed")
	}
}

func TestPruneRemovesStaleMetadataAndLocks(t *testing.T) {
	repo := testrepo.Init(t)
	info, _ := CreateTemp(ctx, repo, "")
	os.RemoveAll(info.Path)
	os.MkdirAll(filepath.Join(repo, LocksDir), 0o755)
	dead, _ := json.Marshal(lockFile{PID: 999999999, Branch: "x"})
	os.WriteFile(filepath.Join(repo, LocksDir, "dead.lock"), dead, 0o644)
	live, _ := json.Marshal(lockFile{PID: os.Getpid(), Branch: "y"})
	os.MkdirAll(filepath.Join(repo, Dir, "live"), 0o755)
	os.WriteFile(filepath.Join(repo, LocksDir, "live.lock"), live, 0o644)
	if err := Prune(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(testrepo.Git(t, repo, "worktree", "list", "--porcelain"), info.Path) {
		t.Fatal("stale metadata not pruned")
	}
	if _, err := os.Stat(filepath.Join(repo, LocksDir, "dead.lock")); err == nil {
		t.Fatal("dead lock not removed")
	}
}

func TestPruneWhenWorktreesDirIsSymlink(t *testing.T) {
	repo := testrepo.Init(t)
	real := t.TempDir()
	os.MkdirAll(filepath.Join(repo, ".agentgopher"), 0o755)
	if err := os.Symlink(real, filepath.Join(repo, Dir)); err != nil {
		t.Skip("symlinks unsupported")
	}
	active, err := CreateTemp(ctx, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := Prune(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(active.Path); err != nil {
		t.Fatal("active worktree behind symlink removed")
	}
}
