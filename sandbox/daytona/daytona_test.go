package daytona

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/sandbox"
)

func TestProviderMetadata(t *testing.T) {
	p := New(nil)
	if p.Kind() != sandbox.KindIsolated || p.Name() != "daytona" {
		t.Fatalf("kind=%q name=%q", p.Kind(), p.Name())
	}
}

func TestLiveRoundTrip(t *testing.T) {
	if os.Getenv("DAYTONA_API_KEY") == "" {
		t.Skip("DAYTONA_API_KEY not set")
	}
	ctx := context.Background()
	h, err := New(nil).Create(ctx, sandbox.IsolatedCreateOptions{Env: map[string]string{"GREETING": "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(ctx)

	res, err := h.Exec(ctx, "echo $GREETING; exit 3", sandbox.ExecOptions{})
	if err != nil || res.ExitCode != 3 || !strings.Contains(res.Stdout, "hi") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	var lines []string
	res, err = h.Exec(ctx, "echo one; echo two", sandbox.ExecOptions{OnLine: func(l string) { lines = append(lines, l) }})
	if err != nil || res.ExitCode != 0 || len(lines) != 2 {
		t.Fatalf("lines=%v res=%+v err=%v", lines, res, err)
	}

	src := t.TempDir() + "/in.txt"
	os.WriteFile(src, []byte("ping"), 0o644)
	if err := h.CopyIn(ctx, src, h.WorktreePath()+"/in.txt"); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir() + "/nested/out.txt"
	if err := h.CopyFileOut(ctx, h.WorktreePath()+"/in.txt", out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "ping" {
		t.Fatalf("got %q", b)
	}
}
