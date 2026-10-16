package sandboxtest

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/e11en/agentgopher/sandbox"
)

func create(t *testing.T) sandbox.IsolatedHandle {
	t.Helper()
	h, err := Isolated().Create(context.Background(), sandbox.IsolatedCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	return h
}

func TestProviderMetadata(t *testing.T) {
	p := Isolated()
	if p.Kind() != sandbox.KindIsolated || p.Name() != "test-isolated" {
		t.Fatalf("kind=%q name=%q", p.Kind(), p.Name())
	}
}

func TestExecRunsInWorktreeByDefaultAndRespectsCwd(t *testing.T) {
	h := create(t)
	res, err := h.Exec(context.Background(), "pwd", sandbox.ExecOptions{})
	if err != nil || res.Stdout != h.WorktreePath()+"\n" {
		t.Fatalf("got %+v, %v", res, err)
	}
	other, _ := filepath.EvalSymlinks(t.TempDir())
	res, err = h.Exec(context.Background(), "pwd", sandbox.ExecOptions{Cwd: other})
	if err != nil || res.Stdout != other+"\n" {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestExecExitCodeAndStreaming(t *testing.T) {
	h := create(t)
	res, err := h.Exec(context.Background(), "exit 4", sandbox.ExecOptions{})
	if err != nil || res.ExitCode != 4 {
		t.Fatalf("got %+v, %v", res, err)
	}
	var lines []string
	start := time.Now()
	var firstAt time.Duration
	_, err = h.Exec(context.Background(), "echo a; sleep 0.3; echo b", sandbox.ExecOptions{OnLine: func(l string) {
		if len(lines) == 0 {
			firstAt = time.Since(start)
		}
		lines = append(lines, l)
	}})
	if err != nil || len(lines) != 2 || firstAt > 200*time.Millisecond {
		t.Fatalf("lines=%v firstAt=%v err=%v", lines, firstAt, err)
	}
}

func TestCopyInFileAndDirectoryAndCopyFileOut(t *testing.T) {
	h := create(t)
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "one.txt"), []byte("1"), 0o644)
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "sub", "two.txt"), []byte("2"), 0o644)

	if err := h.CopyIn(context.Background(), filepath.Join(src, "one.txt"), filepath.Join(h.WorktreePath(), "deep", "one.txt")); err != nil {
		t.Fatal(err)
	}
	if err := h.CopyIn(context.Background(), src, filepath.Join(h.WorktreePath(), "copied")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"deep/one.txt", "copied/one.txt", "copied/sub/two.txt"} {
		if _, err := os.Stat(filepath.Join(h.WorktreePath(), p)); err != nil {
			t.Fatalf("%s missing", p)
		}
	}

	out := filepath.Join(t.TempDir(), "nested", "two.txt")
	if err := h.CopyFileOut(context.Background(), filepath.Join(h.WorktreePath(), "copied", "sub", "two.txt"), out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "2" {
		t.Fatalf("got %q", b)
	}
}

func TestCloseRemovesTempDir(t *testing.T) {
	h, _ := Isolated().Create(context.Background(), sandbox.IsolatedCreateOptions{})
	root := filepath.Dir(h.WorktreePath())
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("root still exists: %v", err)
	}
}
