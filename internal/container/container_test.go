package container

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/sandbox"
)

// fakeBinary installs a shell script named `fakectl` on PATH that records its
// argv to a file and emulates `exec`, `cp` and `rm`.
func fakeBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	script := `#!/bin/sh
echo "$@" >> "` + log + `"
case "$1" in
  exec)
    shift
    # The fake has no root; skip everything (including sudo) up to the shell.
    while [ "$1" != "sh" ]; do shift; done
    shift; shift
    exec sh -c "$1" ;;
  *) exit 0 ;;
esac
`
	os.WriteFile(filepath.Join(dir, "fakectl"), []byte(script), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestHandleExecBuildsArgsAndStreams(t *testing.T) {
	log := fakeBinary(t)
	h := NewHandle("fakectl", "c1", "/home/agent/workspace", 1024)
	defer h.Close(context.Background())
	in := "piped"
	var lines []string
	res, err := h.Exec(context.Background(), "cat; echo; echo done 1>&2; exit 4", sandbox.ExecOptions{
		Stdin: &in, Cwd: "/w", Sudo: true, OnLine: func(l string) { lines = append(lines, l) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 4 || res.Stdout != "piped" || !strings.Contains(res.Stderr, "done") || len(lines) != 1 {
		t.Fatalf("res=%+v lines=%v", res, lines)
	}
	argv, _ := os.ReadFile(log)
	if !strings.Contains(string(argv), "exec -i -w /w c1 sudo sh -c cat; echo; echo done 1>&2; exit 4") {
		t.Fatalf("argv = %s", argv)
	}
}

func TestHandleCopyAndClose(t *testing.T) {
	log := fakeBinary(t)
	h := NewHandle("fakectl", "c1", "/w", 1024)
	ctx := context.Background()
	if err := h.CopyFileIn(ctx, "/h/a", "/s/a"); err != nil {
		t.Fatal(err)
	}
	if err := h.CopyFileOut(ctx, "/s/b", "/h/b"); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(ctx); err != nil {
		t.Fatal(err)
	}
	argv, _ := os.ReadFile(log)
	for _, want := range []string{"cp /h/a c1:/s/a", "cp c1:/s/b /h/b", "rm -f c1"} {
		if !strings.Contains(string(argv), want) {
			t.Fatalf("missing %q in %s", want, argv)
		}
	}
}

func TestRunReportsStderr(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh required")
	}
	_, err := Run(context.Background(), "sh", "-c", "echo bad 1>&2; exit 1")
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("err = %v", err)
	}
}

func TestHandleExecSurvivesChildHoldingStdoutOpen(t *testing.T) {
	fakeBinary(t)
	h := NewHandle("fakectl", "c1", "/w", 1024)
	defer h.Close(context.Background())
	res, err := h.Exec(context.Background(), "sleep 4 & echo hi", sandbox.ExecOptions{OnLine: func(string) {}})
	if err != nil || res.ExitCode != 0 || res.Stdout != "hi" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestInteractiveExecArgs(t *testing.T) {
	opts := sandbox.InteractiveExecOptions{Stdin: strings.NewReader(""), Cwd: "/home/agent/workspace"}
	got := InteractiveExecArgs("c1", opts, []string{"claude", "--model", "m"})
	if strings.Join(got, " ") != "exec -i -w /home/agent/workspace c1 claude --model m" {
		t.Fatalf("got %v", got)
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		t.Skip("no /dev/tty")
	}
	defer tty.Close()
	if got := InteractiveExecArgs("c1", sandbox.InteractiveExecOptions{Stdin: tty}, []string{"sh"}); strings.Join(got, " ") != "exec -it c1 sh" {
		t.Fatalf("tty: %v", got)
	}
}

func TestHandleInteractiveExecWiresStdio(t *testing.T) {
	fakeBinary(t)
	h := NewHandle("fakectl", "c1", "/w", 1024)
	defer h.Close(context.Background())
	var out bytes.Buffer
	code, err := h.InteractiveExec(context.Background(), []string{"sh", "-c", "cat; exit 2"}, sandbox.InteractiveExecOptions{Stdin: strings.NewReader("typed"), Stdout: &out, Stderr: io.Discard})
	if err != nil || code != 2 || out.String() != "typed" {
		t.Fatalf("code=%d err=%v out=%q", code, err, out.String())
	}
}
