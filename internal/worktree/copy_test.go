package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCopyOnWriteFlags(t *testing.T) {
	if got := CopyOnWriteFlags("darwin"); len(got) != 1 || got[0] != "-cR" {
		t.Fatalf("darwin: %v", got)
	}
	if got := CopyOnWriteFlags("linux"); len(got) != 2 || got[1] != "--reflink=auto" {
		t.Fatalf("linux: %v", got)
	}
}

func TestCopyToWorktreeCopiesAndSkipsMissing(t *testing.T) {
	host, wt := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(host, "node_modules", "pkg"), 0o755)
	os.WriteFile(filepath.Join(host, "node_modules", "pkg", "index.js"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(host, "single.txt"), []byte("y"), 0o644)
	err := CopyToWorktree(context.Background(), []string{"node_modules", "single.txt", "missing"}, host, wt, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"node_modules/pkg/index.js", "single.txt"} {
		if _, err := os.Stat(filepath.Join(wt, f)); err != nil {
			t.Fatalf("%s not copied", f)
		}
	}
}

func TestCopyToWorktreeFailsWithCopyError(t *testing.T) {
	host, wt := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(host, "a.txt"), []byte("x"), 0o644)
	// Destination is a directory without write permission, so cp cannot place the file in it.
	os.MkdirAll(filepath.Join(wt, "a.txt"), 0o755)
	os.Chmod(filepath.Join(wt, "a.txt"), 0o555)
	t.Cleanup(func() { os.Chmod(filepath.Join(wt, "a.txt"), 0o755) })
	err := CopyToWorktree(context.Background(), []string{"a.txt"}, host, wt, 0)
	var cerr *CopyError
	if !errors.As(err, &cerr) || cerr.Path != "a.txt" {
		t.Fatalf("err = %v", err)
	}
}

func TestCopyToWorktreeTimeout(t *testing.T) {
	host, wt := t.TempDir(), t.TempDir()
	big := filepath.Join(host, "big")
	os.MkdirAll(big, 0o755)
	for i := 0; i < 2000; i++ {
		os.WriteFile(filepath.Join(big, "f"+string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676))), make([]byte, 4096), 0o644)
	}
	err := CopyToWorktree(context.Background(), []string{"big"}, host, wt, time.Nanosecond)
	var terr *CopyTimeoutError
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v", err)
	}
}
