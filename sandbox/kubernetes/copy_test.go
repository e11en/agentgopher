package kubernetes

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTarPathReproducesTreeWithSymlinksAndEmptyDirs(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	os.MkdirAll(filepath.Join(src, "empty"), 0o755)
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "sub", "a.txt"), []byte("A"), 0o644)
	os.Symlink("sub/a.txt", filepath.Join(src, "link"))

	var buf bytes.Buffer
	if err := tarPath(&buf, src, "dest"); err != nil {
		t.Fatal(err)
	}
	entries := map[string]*tar.Header{}
	tr := tar.NewReader(&buf)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		entries[h.Name] = h
	}
	if entries["dest/empty"] == nil || entries["dest/empty"].Typeflag != tar.TypeDir {
		t.Fatalf("empty dir missing: %v", keys(entries))
	}
	if entries["dest/sub/a.txt"] == nil || entries["dest/sub/a.txt"].Size != 1 {
		t.Fatalf("file missing: %v", keys(entries))
	}
	if l := entries["dest/link"]; l == nil || l.Typeflag != tar.TypeSymlink || l.Linkname != "sub/a.txt" {
		t.Fatalf("symlink wrong: %+v", l)
	}
}

func TestTarPathSingleFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "only.txt")
	os.WriteFile(f, []byte("x"), 0o644)
	var buf bytes.Buffer
	if err := tarPath(&buf, f, "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	h, err := tar.NewReader(&buf).Next()
	if err != nil || h.Name != "renamed.txt" || h.Size != 1 {
		t.Fatalf("h = %+v, %v", h, err)
	}
}

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

func keys(m map[string]*tar.Header) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
