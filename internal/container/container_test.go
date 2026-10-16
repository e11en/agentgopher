package container

import (
	"context"
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
    while [ "$1" != "sh" ]; do shift; done
    shift; shift
    # The fake has no root; drop the sudo prefix the handle adds.
    exec sh -c "${1#sudo }" ;;
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
	if !strings.Contains(string(argv), "exec -i -w /w c1 sh -c sudo cat; echo; echo done 1>&2; exit 4") {
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
