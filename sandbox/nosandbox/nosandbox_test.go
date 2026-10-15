package nosandbox

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/e11en/agentgopher/sandbox"
)

func newHandle(t *testing.T) sandbox.Handle {
	t.Helper()
	h, err := New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestExecCapturesStdoutStderrAndExitCode(t *testing.T) {
	h := newHandle(t)
	res, err := h.Exec(context.Background(), "echo out; echo err 1>&2; exit 3", sandbox.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "out\n" || res.Stderr != "err\n" || res.ExitCode != 3 {
		t.Fatalf("got %+v", res)
	}
}

func TestExecStreamsLinesWhileRunning(t *testing.T) {
	h := newHandle(t)
	var seen []time.Time
	start := time.Now()
	_, err := h.Exec(context.Background(), "echo one; sleep 0.3; echo two", sandbox.ExecOptions{
		OnLine: func(string) { seen = append(seen, time.Now()) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("got %d lines", len(seen))
	}
	if seen[0].Sub(start) > 200*time.Millisecond {
		t.Fatal("first line arrived only after the process finished; OnLine must stream")
	}
}

func TestExecPipesStdin(t *testing.T) {
	h := newHandle(t)
	in := "from stdin"
	res, err := h.Exec(context.Background(), "cat", sandbox.ExecOptions{Stdin: &in})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "from stdin" {
		t.Fatalf("Stdout = %q", res.Stdout)
	}
}

func TestExecUsesCwdAndEnv(t *testing.T) {
	dir := t.TempDir()
	p := New(&Options{Env: map[string]string{"GREETING": "hi"}})
	h, err := p.Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: dir, Env: map[string]string{"GREETING": "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.Exec(context.Background(), "pwd; echo $GREETING", sandbox.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// pwd prints the resolved path, and macOS puts temp dirs behind a symlink.
	resolved, _ := filepath.EvalSymlinks(dir)
	if res.Stdout != resolved+"\nhi\n" {
		t.Fatalf("Stdout = %q", res.Stdout)
	}
}

func TestExecBoundsStreamedTail(t *testing.T) {
	p := New(&Options{MaxOutputTailChars: 10})
	h, _ := p.Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: t.TempDir()})
	res, err := h.Exec(context.Background(), "for i in 1 2 3 4 5 6 7 8 9; do echo line$i; done", sandbox.ExecOptions{OnLine: func(string) {}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stdout) > 10 {
		t.Fatalf("tail not bounded: %q", res.Stdout)
	}
}

func TestExecCancelledContextKillsProcess(t *testing.T) {
	h := newHandle(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := h.Exec(ctx, "sleep 5", sandbox.ExecOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestProviderMetadata(t *testing.T) {
	p := New(nil)
	if p.Kind() != sandbox.KindNone || p.Name() != "no-sandbox" {
		t.Fatalf("kind=%q name=%q", p.Kind(), p.Name())
	}
}

func TestExecSurvivesChildHoldingStdoutOpen(t *testing.T) {
	h := newHandle(t)
	var lines []string
	res, err := h.Exec(context.Background(), "sleep 4 & echo hi", sandbox.ExecOptions{OnLine: func(l string) { lines = append(lines, l) }})
	if err != nil {
		t.Fatalf("err = %v, want nil after WaitDelay", err)
	}
	if res.ExitCode != 0 || len(lines) != 1 || lines[0] != "hi" {
		t.Fatalf("res = %+v lines = %q", res, lines)
	}
}
