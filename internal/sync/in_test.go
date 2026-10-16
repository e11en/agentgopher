package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/internal/sandboxtest"
	"github.com/e11en/agentgopher/internal/testrepo"
	"github.com/e11en/agentgopher/sandbox"
)

func newIsolated(t *testing.T) sandbox.IsolatedHandle {
	t.Helper()
	h, err := sandboxtest.Isolated().Create(context.Background(), sandbox.IsolatedCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	return h
}

func commitFile(t *testing.T, dir, name, content, message string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", message}} {
		if _, err := gitx.Run(context.Background(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInClonesFilesAndHistory(t *testing.T) {
	repo := testrepo.Init(t)
	commitFile(t, repo, "a.txt", "a", "second")
	commitFile(t, repo, "b.txt", "b", "third")
	h := newIsolated(t)
	ctx := context.Background()
	if err := In(ctx, h, repo, h.WorktreePath(), "main", DefaultInTimeout); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"README.md", "a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(h.WorktreePath(), f)); err != nil {
			t.Fatalf("%s missing in sandbox", f)
		}
	}
	log, _ := gitx.Run(ctx, h.WorktreePath(), "log", "--oneline")
	if len(strings.Split(log, "\n")) != 3 {
		t.Fatalf("log = %q", log)
	}
	hostHead, _ := gitx.Head(ctx, repo)
	sandboxHead, _ := gitx.Head(ctx, h.WorktreePath())
	if hostHead != sandboxHead {
		t.Fatalf("head host=%s sandbox=%s", hostHead, sandboxHead)
	}
	if b, _ := gitx.CurrentBranch(ctx, h.WorktreePath()); b != "main" {
		t.Fatalf("branch = %q", b)
	}
}

func TestInChecksOutNonMainBranch(t *testing.T) {
	repo := testrepo.Init(t)
	gitx.Run(context.Background(), repo, "checkout", "-q", "-b", "feature/x")
	commitFile(t, repo, "f.txt", "f", "feature")
	h := newIsolated(t)
	if err := In(context.Background(), h, repo, h.WorktreePath(), "feature/x", DefaultInTimeout); err != nil {
		t.Fatal(err)
	}
	if b, _ := gitx.CurrentBranch(context.Background(), h.WorktreePath()); b != "feature/x" {
		t.Fatalf("branch = %q", b)
	}
}

func TestInExcludesUncommittedChanges(t *testing.T) {
	repo := testrepo.Init(t)
	os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("x"), 0o644)
	h := newIsolated(t)
	if err := In(context.Background(), h, repo, h.WorktreePath(), "main", DefaultInTimeout); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.WorktreePath(), "dirty.txt")); !os.IsNotExist(err) {
		t.Fatal("uncommitted file must not be synced")
	}
}

func TestInTimesOut(t *testing.T) {
	repo := testrepo.Init(t)
	h := newIsolated(t)
	err := In(context.Background(), h, repo, h.WorktreePath(), "main", time.Nanosecond)
	var terr *InTimeoutError
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v", err)
	}
}

func TestInFailsOnNonRepo(t *testing.T) {
	h := newIsolated(t)
	err := In(context.Background(), h, t.TempDir(), h.WorktreePath(), "main", DefaultInTimeout)
	var serr *Error
	if !errors.As(err, &serr) {
		t.Fatalf("err = %v", err)
	}
}
