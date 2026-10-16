package mount

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/sandbox"
)

func TestNormalizeMountsNonWindowsUnchanged(t *testing.T) {
	in := []sandbox.Mount{{HostPath: "/r/.git", SandboxPath: "/r/.git"}}
	out := NormalizeMounts(in, "/r", SandboxRepoDir, "linux")
	if out[0] != in[0] {
		t.Fatalf("got %+v", out)
	}
}

func TestNormalizeMountsWindows(t *testing.T) {
	wt := `C:\Users\p\.agentgopher\worktrees\x`
	in := []sandbox.Mount{
		{HostPath: wt, SandboxPath: wt},
		{HostPath: wt + `\.git`, SandboxPath: wt + `\.git`},
		{HostPath: `C:\Users\p\.git`, SandboxPath: `C:\Users\p\.git`},
		{HostPath: `C:\data`, SandboxPath: "/mnt/data", ReadOnly: true},
	}
	out := NormalizeMounts(in, wt, SandboxRepoDir, "windows")
	want := []sandbox.Mount{
		{HostPath: "C:/Users/p/.agentgopher/worktrees/x", SandboxPath: SandboxRepoDir},
		{HostPath: "C:/Users/p/.agentgopher/worktrees/x/.git", SandboxPath: SandboxRepoDir + "/.git"},
		{HostPath: "C:/Users/p/.git", SandboxPath: "C:/Users/p/.git"},
		{HostPath: "C:/data", SandboxPath: "/mnt/data", ReadOnly: true},
	}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("mount %d = %+v, want %+v", i, out[i], want[i])
		}
	}
}

func TestParseGitdirPath(t *testing.T) {
	cases := []struct{ in, parent, name string }{
		{"/r/.git/worktrees/abc", "/r/.git", "abc"},
		{`C:\r\.git\worktrees\abc`, "C:/r/.git", "abc"},
		{"C:/r/.git/worktrees/abc/", "C:/r/.git", "abc"},
	}
	for _, c := range cases {
		p, n := ParseGitdirPath(c.in)
		if p != c.parent || n != c.name {
			t.Errorf("ParseGitdirPath(%q) = %q,%q", c.in, p, n)
		}
	}
}

type memFS struct {
	files map[string]string
	dirs  map[string]bool
}

type memInfo struct {
	fs.FileInfo
	dir bool
}

func (m memInfo) IsDir() bool { return m.dir }

func (m memFS) ReadFile(name string) ([]byte, error) {
	if s, ok := m.files[name]; ok {
		return []byte(s), nil
	}
	return nil, os.ErrNotExist
}

func (m memFS) Stat(name string) (fs.FileInfo, error) {
	if m.dirs[name] {
		return memInfo{dir: true}, nil
	}
	if _, ok := m.files[name]; ok {
		return memInfo{}, nil
	}
	return nil, os.ErrNotExist
}

func TestPatchGitMountsNonWindowsUnchanged(t *testing.T) {
	in := []sandbox.Mount{{HostPath: "/r/.git", SandboxPath: "/r/.git"}}
	out, err := PatchGitMountsForWindows(in, "/wt", SandboxRepoDir, memFS{}, "linux")
	if err != nil || len(out) != 1 || out[0] != in[0] {
		t.Fatalf("got %+v, %v", out, err)
	}
}

func TestPatchGitMountsWindowsDirectoryUnchanged(t *testing.T) {
	fsys := memFS{dirs: map[string]bool{`C:\r\.git`: true}}
	in := []sandbox.Mount{{HostPath: `C:\r\.git`, SandboxPath: `C:\r\.git`}}
	out, err := PatchGitMountsForWindows(in, `C:\r`, SandboxRepoDir, fsys, "windows")
	if err != nil || len(out) != 1 {
		t.Fatalf("got %+v, %v", out, err)
	}
}

func TestPatchGitMountsWindowsAgentgopherWorktree(t *testing.T) {
	wt := `C:\r\.agentgopher\worktrees\abc`
	fsys := memFS{files: map[string]string{wt + `\.git`: "gitdir: C:\\r\\.git\\worktrees\\abc\n"}}
	in := []sandbox.Mount{{HostPath: `C:\r\.git`, SandboxPath: `C:\r\.git`}}
	out, err := PatchGitMountsForWindows(in, wt, SandboxRepoDir, fsys, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].SandboxPath != ParentGitSandboxDir || out[1].SandboxPath != SandboxRepoDir+"/.git" {
		t.Fatalf("got %+v", out)
	}
	b, err := os.ReadFile(out[1].HostPath)
	if err != nil || strings.TrimSpace(string(b)) != "gitdir: "+ParentGitSandboxDir+"/worktrees/abc" {
		t.Fatalf("override = %q, %v", b, err)
	}
}

func TestPatchGitMountsWindowsHostRepoIsWorktree(t *testing.T) {
	wt := `C:\wt`
	fsys := memFS{files: map[string]string{wt + `\.git`: "gitdir: C:/r/.git/worktrees/wt\n"}}
	in := []sandbox.Mount{
		{HostPath: wt + `\.git`, SandboxPath: wt + `\.git`},
		{HostPath: `C:\r\.git`, SandboxPath: `C:\r\.git`},
	}
	out, err := PatchGitMountsForWindows(in, wt, SandboxRepoDir, fsys, "windows")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].SandboxPath != SandboxRepoDir+"/.git" || out[1].SandboxPath != ParentGitSandboxDir {
		t.Fatalf("got %+v", out)
	}
}

func TestPatchGitMountsWindowsNoGitdirLine(t *testing.T) {
	fsys := memFS{files: map[string]string{`C:\wt\.git`: "nonsense"}}
	in := []sandbox.Mount{{HostPath: `C:\r\.git`, SandboxPath: `C:\r\.git`}}
	out, err := PatchGitMountsForWindows(in, `C:\wt`, SandboxRepoDir, fsys, "windows")
	if err != nil || len(out) != 1 || out[0].SandboxPath != `C:\r\.git` {
		t.Fatalf("got %+v, %v", out, err)
	}
}
