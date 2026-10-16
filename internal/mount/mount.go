// Package mount resolves bind mounts for container sandbox providers.
package mount

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/e11en/agentgopher/sandbox"
)

// SandboxRepoDir is where the worktree is mounted inside a container.
const SandboxRepoDir = "/home/agent/workspace"

// SandboxHomedir is the agent user's home inside the built-in images.
const SandboxHomedir = "/home/agent"

var imageTagChars = regexp.MustCompile(`[^a-z0-9_.-]`)

// DefaultImageName returns agentgopher:<last path segment>, sanitized for image tags.
func DefaultImageName(repoDir string) string {
	trimmed := strings.TrimRight(repoDir, `/\`)
	parts := strings.FieldsFunc(trimmed, func(r rune) bool { return r == '/' || r == '\\' })
	name := ""
	if len(parts) > 0 {
		name = imageTagChars.ReplaceAllString(strings.ToLower(parts[len(parts)-1]), "-")
	}
	if name == "" {
		name = "local"
	}
	return "agentgopher:" + name
}

func hasTilde(p string) bool {
	return p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`)
}

// ExpandTilde replaces a leading ~ with home. An empty home means os.UserHomeDir.
func ExpandTilde(p, home string) string {
	if !hasTilde(p) {
		return p
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if p == "~" {
		return home
	}
	return home + "/" + p[2:]
}

// ResolveHostPath expands ~ and makes relative paths absolute from the working directory.
func ResolveHostPath(p string) string {
	expanded := ExpandTilde(p, "")
	if filepath.IsAbs(expanded) {
		return expanded
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return expanded
	}
	return abs
}

// ResolveSandboxPath expands ~ with sandboxHomedir and resolves relative paths from SandboxRepoDir.
func ResolveSandboxPath(p, sandboxHomedir string) (string, error) {
	if hasTilde(p) {
		if sandboxHomedir == "" {
			return "", fmt.Errorf("sandboxPath %q contains a tilde but the provider has no sandbox home directory", p)
		}
		p = ExpandTilde(p, sandboxHomedir)
	}
	if path.IsAbs(p) {
		return p, nil
	}
	return path.Join(SandboxRepoDir, p), nil
}

// ResolveUserMounts validates and resolves user-facing mount configs.
func ResolveUserMounts(mounts []sandbox.MountConfig, sandboxHomedir string) ([]sandbox.Mount, error) {
	out := make([]sandbox.Mount, 0, len(mounts))
	for _, m := range mounts {
		host := ResolveHostPath(m.HostPath)
		if _, err := os.Stat(host); err != nil {
			msg := "mount hostPath does not exist: " + m.HostPath
			if host != m.HostPath {
				msg += " (resolved to " + host + ")"
			}
			return nil, fmt.Errorf("%s", msg)
		}
		sb, err := ResolveSandboxPath(m.SandboxPath, sandboxHomedir)
		if err != nil {
			return nil, err
		}
		out = append(out, sandbox.Mount{HostPath: host, SandboxPath: sb, ReadOnly: m.ReadOnly})
	}
	return out, nil
}

// FormatVolumeMount renders host:sandbox[:ro][,label] for a -v flag.
func FormatVolumeMount(m sandbox.Mount, selinuxLabel string) string {
	var opts []string
	if m.ReadOnly {
		opts = append(opts, "ro")
	}
	if selinuxLabel != "" {
		opts = append(opts, selinuxLabel)
	}
	s := m.HostPath + ":" + m.SandboxPath
	if len(opts) > 0 {
		s += ":" + strings.Join(opts, ",")
	}
	return s
}

// FileMountParents returns the sandbox-side parent directories that file
// mounts need created at container start. A file mount outside the sandbox
// home fails, because the image may not contain its parent.
func FileMountParents(mounts []sandbox.Mount, sandboxHomedir string, stat func(string) (fs.FileInfo, error)) ([]string, error) {
	if stat == nil {
		stat = os.Stat
	}
	seen := map[string]bool{}
	var parents []string
	for _, m := range mounts {
		info, err := stat(m.HostPath)
		if err != nil || info.IsDir() {
			continue
		}
		parent := path.Dir(m.SandboxPath)
		if parent == sandboxHomedir {
			continue
		}
		if !strings.HasPrefix(parent, sandboxHomedir+"/") {
			return nil, fmt.Errorf("cannot mount file to '%s': parent directory '%s' is outside the sandbox home directory ('%s'). Mount the parent directory instead, or rebuild the image with '%s' pre-created", m.SandboxPath, parent, sandboxHomedir, parent)
		}
		if !seen[parent] {
			seen[parent] = true
			parents = append(parents, parent)
		}
	}
	return parents, nil
}

var gitdirLine = regexp.MustCompile(`^gitdir:\s*(.+)$`)

// ResolveGitMounts returns the mounts git needs: the .git directory, or for a
// worktree the .git file plus the parent repo's .git directory, each mounted
// at its own host path so gitdir pointers resolve inside the container.
func ResolveGitMounts(gitPath string) ([]sandbox.Mount, error) {
	info, err := os.Stat(gitPath)
	if err != nil {
		return nil, err
	}
	same := sandbox.Mount{HostPath: gitPath, SandboxPath: gitPath}
	if info.IsDir() {
		return []sandbox.Mount{same}, nil
	}
	b, err := os.ReadFile(gitPath)
	if err != nil {
		return nil, err
	}
	m := gitdirLine.FindStringSubmatch(strings.TrimSpace(string(b)))
	if m == nil {
		return []sandbox.Mount{same}, nil
	}
	parent := filepath.Clean(filepath.Join(m[1], "..", ".."))
	return []sandbox.Mount{same, {HostPath: parent, SandboxPath: parent}}, nil
}
