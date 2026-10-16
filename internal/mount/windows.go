package mount

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/e11en/agentgopher/sandbox"
)

// ParentGitSandboxDir is where a worktree's parent .git directory is mounted
// inside the container on Windows hosts (ADR 0006).
const ParentGitSandboxDir = "/.agentgopher-parent-git"

var windowsDrive = regexp.MustCompile(`^[A-Za-z]:[/\\]`)

func slash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// NormalizeMounts rewrites Windows host paths to forward slashes and remaps
// Windows-style sandbox paths under the worktree to POSIX paths under
// sandboxRepoDir. Other platforms get the mounts back unchanged.
func NormalizeMounts(mounts []sandbox.Mount, worktreeHostPath, sandboxRepoDir, goos string) []sandbox.Mount {
	if goos != "windows" {
		return mounts
	}
	wt := slash(worktreeHostPath)
	out := make([]sandbox.Mount, len(mounts))
	for i, m := range mounts {
		m.HostPath = slash(m.HostPath)
		if windowsDrive.MatchString(m.SandboxPath) || strings.Contains(m.SandboxPath, `\`) {
			sp := slash(m.SandboxPath)
			switch {
			case sp == wt:
				m.SandboxPath = sandboxRepoDir
			case strings.HasPrefix(sp, wt+"/"):
				m.SandboxPath = sandboxRepoDir + sp[len(wt):]
			default:
				m.SandboxPath = sp
			}
		}
		out[i] = m
	}
	return out
}

// ParseGitdirPath splits <parent .git>/worktrees/<name> on either separator.
func ParseGitdirPath(gitdir string) (parentGitDir, worktreeName string) {
	parts := strings.Split(strings.TrimRight(slash(gitdir), "/"), "/")
	if len(parts) < 3 {
		return strings.Join(parts, "/"), ""
	}
	return strings.Join(parts[:len(parts)-2], "/"), parts[len(parts)-1]
}

// FS is the file access PatchGitMountsForWindows needs; OSFS is the real one.
type FS interface {
	ReadFile(name string) ([]byte, error)
	Stat(name string) (fs.FileInfo, error)
}

// OSFS reads the real filesystem.
type OSFS struct{}

func (OSFS) ReadFile(name string) ([]byte, error)  { return os.ReadFile(name) }
func (OSFS) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) }

// PatchGitMountsForWindows makes a worktree's gitdir pointer resolve inside a
// Linux container on a Windows host: the parent .git directory is mounted at
// ParentGitSandboxDir and a corrected .git file overlays <sandboxRepoDir>/.git.
func PatchGitMountsForWindows(gitMounts []sandbox.Mount, worktreeHostPath, sandboxRepoDir string, fsys FS, goos string) ([]sandbox.Mount, error) {
	if goos != "windows" {
		return gitMounts, nil
	}
	gitEntry := worktreeHostPath + `\.git`
	info, err := fsys.Stat(gitEntry)
	if err != nil || info.IsDir() {
		return gitMounts, nil
	}
	content, err := fsys.ReadFile(gitEntry)
	if err != nil {
		return gitMounts, nil
	}
	m := gitdirLine.FindStringSubmatch(strings.TrimSpace(string(content)))
	if m == nil {
		return gitMounts, nil
	}
	parentGitDir, worktreeName := ParseGitdirPath(m[1])

	tmp, err := os.MkdirTemp("", "agentgopher-git-")
	if err != nil {
		return nil, err
	}
	override := filepath.Join(tmp, "git-override")
	if err := os.WriteFile(override, []byte("gitdir: "+ParentGitSandboxDir+"/worktrees/"+worktreeName+"\n"), 0o644); err != nil {
		return nil, err
	}

	out := make([]sandbox.Mount, 0, len(gitMounts)+1)
	replaced := false
	for _, gm := range gitMounts {
		switch slash(gm.HostPath) {
		case parentGitDir:
			gm.SandboxPath = ParentGitSandboxDir
		case slash(gitEntry):
			gm.HostPath = override
			gm.SandboxPath = sandboxRepoDir + "/.git"
			replaced = true
		}
		out = append(out, gm)
	}
	if !replaced {
		out = append(out, sandbox.Mount{HostPath: override, SandboxPath: sandboxRepoDir + "/.git"})
	}
	return out, nil
}
