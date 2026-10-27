package kubernetes

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyInSendsTarToExtractCommand(t *testing.T) {
	fe := &fakeExecutor{script: map[string]scriptedExec{}}
	h := newTestHandle(t, fe)
	f := filepath.Join(t.TempDir(), "node_modules")
	os.MkdirAll(f, 0o755)
	if err := h.CopyIn(context.Background(), f, "/home/agent/workspace/node_modules"); err != nil {
		t.Fatal(err)
	}
	call := fe.calls[len(fe.calls)-1]
	if call.command[2] != "mkdir -p '/home/agent/workspace' && tar -xf - -C '/home/agent/workspace'" {
		t.Fatalf("command = %q", call.command[2])
	}
	hdr, err := tar.NewReader(strings.NewReader(call.stdin)).Next()
	if err != nil || hdr.Name != "node_modules" {
		t.Fatalf("hdr = %+v, %v", hdr, err)
	}
}

func TestCopyInFailsOnNonZeroExit(t *testing.T) {
	fe := &fakeExecutor{script: map[string]scriptedExec{"mkdir -p '/x' && tar -xf - -C '/x'": {stderr: "read-only", exitCode: 2}}}
	h := newTestHandle(t, fe)
	f := filepath.Join(t.TempDir(), "a")
	os.WriteFile(f, []byte("1"), 0o644)
	err := h.CopyIn(context.Background(), f, "/x/a")
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("err = %v", err)
	}
}

func TestCopyFileOutWritesStdoutToHost(t *testing.T) {
	fe := &fakeExecutor{script: map[string]scriptedExec{"cat '/home/agent/out.bin'": {stdout: "bin|ary\x00data"}}}
	h := newTestHandle(t, fe)
	dest := filepath.Join(t.TempDir(), "nested", "out.bin")
	if err := h.CopyFileOut(context.Background(), "/home/agent/out.bin", dest); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dest)
	if string(b) != "binary\x00data" {
		t.Fatalf("got %q", b)
	}
}
