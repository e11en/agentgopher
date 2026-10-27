package tarx

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPackReproducesTreeWithSymlinksAndEmptyDirs(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	os.MkdirAll(filepath.Join(src, "empty"), 0o755)
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "sub", "a.txt"), []byte("A"), 0o644)
	os.Symlink("sub/a.txt", filepath.Join(src, "link"))

	var buf bytes.Buffer
	if err := Pack(&buf, src, "dest"); err != nil {
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

func TestPackSingleFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "only.txt")
	os.WriteFile(f, []byte("x"), 0o644)
	var buf bytes.Buffer
	if err := Pack(&buf, f, "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	h, err := tar.NewReader(&buf).Next()
	if err != nil || h.Name != "renamed.txt" || h.Size != 1 {
		t.Fatalf("h = %+v, %v", h, err)
	}
}

func keys(m map[string]*tar.Header) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
