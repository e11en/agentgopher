package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/e11en/agentgopher/internal/testrepo"
)

func TestCurrentBranchAndHead(t *testing.T) {
	dir := testrepo.Init(t)
	ctx := context.Background()
	if b, err := CurrentBranch(ctx, dir); err != nil || b != "main" {
		t.Fatalf("branch = %q, %v", b, err)
	}
	if h, err := Head(ctx, dir); err != nil || len(h) != 40 {
		t.Fatalf("head = %q, %v", h, err)
	}
}

func TestRevListAndConfig(t *testing.T) {
	dir := testrepo.Init(t)
	ctx := context.Background()
	base, _ := Head(ctx, dir)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	if _, err := Run(ctx, dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, dir, "commit", "-q", "-m", "second"); err != nil {
		t.Fatal(err)
	}
	shas, err := RevList(ctx, dir, base, "HEAD")
	if err != nil || len(shas) != 1 {
		t.Fatalf("shas = %v, %v", shas, err)
	}
	if shas, err := RevList(ctx, dir, base, "refs/heads/nope"); err != nil || shas != nil {
		t.Fatalf("missing ref: %v, %v", shas, err)
	}
	if got := ConfigValue(ctx, dir, "user.name"); got != "Test User" {
		t.Fatalf("user.name = %q", got)
	}
	if got := ConfigValue(ctx, dir, "nope.nope"); got != "" {
		t.Fatalf("unset = %q", got)
	}
}

func TestRunErrorIncludesStderr(t *testing.T) {
	_, err := Run(context.Background(), t.TempDir(), "rev-parse", "HEAD")
	if err == nil {
		t.Fatal("expected error outside a repo")
	}
}
