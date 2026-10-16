// Package session holds the pure path and transfer primitives for agent
// sessions. File I/O lives in the agent providers (ADR 0012).
package session

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
)

// HostSessionLookup is the result of locating a session on the host by id.
// Path is empty when nothing was found; SearchedRoot says where we looked.
type HostSessionLookup struct {
	Path         string
	SearchedRoot string
}

func home() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return "~"
}

// DefaultClaudeProjectsDir is $HOME/.claude/projects.
func DefaultClaudeProjectsDir() string { return filepath.Join(home(), ".claude", "projects") }

// DefaultCodexSessionsDir is $HOME/.codex/sessions.
func DefaultCodexSessionsDir() string { return filepath.Join(home(), ".codex", "sessions") }

// DefaultPiSessionsDir is $HOME/.pi/agent/sessions.
func DefaultPiSessionsDir() string { return filepath.Join(home(), ".pi", "agent", "sessions") }

var (
	windowsRoot  = regexp.MustCompile(`^[A-Za-z]:[\\/]?$`)
	driveLetter  = regexp.MustCompile(`^([A-Za-z]):`)
	separators   = regexp.MustCompile(`[\\/]`)
	trailingSeps = regexp.MustCompile(`[\\/]+$`)
)

// EncodeProjectPath encodes a cwd the way Claude Code names its
// ~/.claude/projects/<encoded>/ directory: separators become hyphens.
func EncodeProjectPath(cwd string) string {
	normalized := cwd
	if cwd != "/" && !windowsRoot.MatchString(cwd) {
		normalized = trailingSeps.ReplaceAllString(cwd, "")
	}
	return separators.ReplaceAllString(driveLetter.ReplaceAllString(normalized, "$1"), "-")
}

func orDefault(dir string, def func() string) string {
	if dir != "" {
		return dir
	}
	return def()
}

// ClaudeHostPath is the host path of a Claude session file.
func ClaudeHostPath(cwd, id, projectsDir string) string {
	return filepath.Join(orDefault(projectsDir, DefaultClaudeProjectsDir), EncodeProjectPath(cwd), id+".jsonl")
}

// ClaudeSandboxPath is the sandbox path of a Claude session file (POSIX).
func ClaudeSandboxPath(cwd, id, projectsDir string) string {
	return path.Join(projectsDir, EncodeProjectPath(cwd), id+".jsonl")
}

// ClaudeSubagentsDirOnHost is where subagent transcripts of a session live on the host.
func ClaudeSubagentsDirOnHost(cwd, id, projectsDir string) string {
	return filepath.Join(orDefault(projectsDir, DefaultClaudeProjectsDir), EncodeProjectPath(cwd), id, "subagents")
}

// ClaudeSubagentsDirInSandbox is the sandbox counterpart of ClaudeSubagentsDirOnHost (POSIX).
func ClaudeSubagentsDirInSandbox(cwd, id, projectsDir string) string {
	return path.Join(projectsDir, EncodeProjectPath(cwd), id, "subagents")
}

// FindClaudeOnHost scans every project directory for <id>.jsonl. The id is
// globally unique, so the first match wins.
func FindClaudeOnHost(id, projectsDir string) HostSessionLookup {
	root := orDefault(projectsDir, DefaultClaudeProjectsDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		return HostSessionLookup{SearchedRoot: root}
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(root, e.Name(), id+".jsonl")
		if _, err := os.Stat(candidate); err == nil {
			return HostSessionLookup{Path: candidate, SearchedRoot: root}
		}
	}
	return HostSessionLookup{SearchedRoot: root}
}

// Location is a found session file with its path relative to the sessions root.
type Location struct {
	Path         string
	RelativePath string
}
