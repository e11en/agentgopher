package mount

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/sandbox"
)

func TestDefaultImageName(t *testing.T) {
	cases := map[string]string{
		"/home/u/My Repo":  "agentgopher:my-repo",
		"/home/u/repo/":    "agentgopher:repo",
		`C:\Users\p\Proj\`: "agentgopher:proj",
		"":                 "agentgopher:local",
	}
	for in, want := range cases {
		if got := DefaultImageName(in); got != want {
			t.Errorf("DefaultImageName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExpandTildeAndPaths(t *testing.T) {
	if got := ExpandTilde("~", "/h"); got != "/h" {
		t.Fatal(got)
	}
	if got := ExpandTilde("~/x", "/h"); got != "/h/x" {
		t.Fatal(got)
	}
	if got := ExpandTilde(`~\x`, "/h"); got != "/h/x" {
		t.Fatal(got)
	}
	if got := ExpandTilde("rel", "/h"); got != "rel" {
		t.Fatal(got)
	}
	if got := ResolveHostPath("/abs"); got != "/abs" {
		t.Fatal(got)
	}
	wd, _ := os.Getwd()
	if got := ResolveHostPath("rel"); got != filepath.Join(wd, "rel") {
		t.Fatal(got)
	}
	if got, _ := ResolveSandboxPath("data", "/home/agent"); got != SandboxRepoDir+"/data" {
		t.Fatal(got)
	}
	if got, _ := ResolveSandboxPath("~/.npm", "/home/agent"); got != "/home/agent/.npm" {
		t.Fatal(got)
	}
	if _, err := ResolveSandboxPath("~/.npm", ""); err == nil {
		t.Fatal("tilde without homedir must fail")
	}
}

func TestResolveUserMounts(t *testing.T) {
	dir := t.TempDir()
	got, err := ResolveUserMounts([]sandbox.MountConfig{{HostPath: dir, SandboxPath: "/mnt/d", ReadOnly: true}}, "/home/agent")
	if err != nil || len(got) != 1 || got[0].HostPath != dir || got[0].SandboxPath != "/mnt/d" || !got[0].ReadOnly {
		t.Fatalf("got %+v, %v", got, err)
	}
	_, err = ResolveUserMounts([]sandbox.MountConfig{{HostPath: filepath.Join(dir, "nope"), SandboxPath: "/x"}}, "")
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v", err)
	}
}

func TestFormatVolumeMount(t *testing.T) {
	m := sandbox.Mount{HostPath: "/h", SandboxPath: "/s"}
	cases := []struct {
		ro    bool
		label string
		want  string
	}{
		{false, "", "/h:/s"}, {false, "z", "/h:/s:z"}, {true, "", "/h:/s:ro"}, {true, "Z", "/h:/s:ro,Z"},
	}
	for _, c := range cases {
		m.ReadOnly = c.ro
		if got := FormatVolumeMount(m, c.label); got != c.want {
			t.Errorf("ro=%v label=%q: got %q, want %q", c.ro, c.label, got, c.want)
		}
	}
}

type fakeInfo struct {
	fs.FileInfo
	file bool
}

func (f fakeInfo) IsDir() bool { return !f.file }

func TestFileMountParents(t *testing.T) {
	stat := func(p string) (fs.FileInfo, error) {
		if strings.HasSuffix(p, "dir") {
			return fakeInfo{file: false}, nil
		}
		if strings.HasSuffix(p, "missing") {
			return nil, os.ErrNotExist
		}
		return fakeInfo{file: true}, nil
	}
	got, err := FileMountParents([]sandbox.Mount{
		{HostPath: "/a", SandboxPath: "/home/agent/.config/x/f"},
		{HostPath: "/b", SandboxPath: "/home/agent/.config/x/g"},
		{HostPath: "/c", SandboxPath: "/home/agent/f"},
		{HostPath: "/dir", SandboxPath: "/etc/d"},
		{HostPath: "/missing", SandboxPath: "/etc/m"},
	}, "/home/agent", stat)
	if err != nil || len(got) != 1 || got[0] != "/home/agent/.config/x" {
		t.Fatalf("got %v, %v", got, err)
	}
	_, err = FileMountParents([]sandbox.Mount{{HostPath: "/a", SandboxPath: "/etc/x/f"}}, "/home/agent", stat)
	if err == nil || !strings.Contains(err.Error(), "outside the sandbox home directory") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveGitMounts(t *testing.T) {
	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")
	os.MkdirAll(gitDir, 0o755)
	got, err := ResolveGitMounts(gitDir)
	if err != nil || len(got) != 1 || got[0].HostPath != gitDir || got[0].SandboxPath != gitDir {
		t.Fatalf("dir: %+v, %v", got, err)
	}
	wt := t.TempDir()
	gitFile := filepath.Join(wt, ".git")
	os.WriteFile(gitFile, []byte("gitdir: "+filepath.Join(gitDir, "worktrees", "abc")+"\n"), 0o644)
	got, err = ResolveGitMounts(gitFile)
	if err != nil || len(got) != 2 || got[0].HostPath != gitFile || got[1].HostPath != gitDir {
		t.Fatalf("worktree: %+v, %v", got, err)
	}
	os.WriteFile(gitFile, []byte("garbage"), 0o644)
	got, err = ResolveGitMounts(gitFile)
	if err != nil || len(got) != 1 {
		t.Fatalf("fallback: %+v, %v", got, err)
	}
}
