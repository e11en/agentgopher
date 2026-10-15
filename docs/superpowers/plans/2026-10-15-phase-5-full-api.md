# agentgopher Phase 5: Full API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring agentgopher to API parity with sandcastle: `CreateSandbox`, `Interactive`, structured output with retries, session resume and fork for Claude Code, Codex and Pi, the five remaining agent providers, a terminal display with spinners, and process-wide shutdown cleanup.

**Architecture:** Session storage is owned by each agent provider (ADR 0012) through an optional `agent.SessionStorage` interface backed by the public `session` package of pure transfer functions. Structured output is a post-run extraction pass in the root package, orthogonal to the completion signal (ADR 0010). `CreateSandbox` reuses the phase 3 worktree and start-sandbox helpers and feeds the orchestrator an already-running handle. `Interactive` attaches the caller's stdio to the agent CLI through an optional `sandbox.Interactive` capability on handles.

**Tech Stack:** Go 1.25, standard library only. `golang.org/x/term` is allowed for PTY work but is not needed: the host process inherits the terminal file descriptors, and "is stdin a terminal" is answered with `os.File.Stat` and `os.ModeCharDevice`.

**Spec:** `docs/plan.md` (feature inventory rows for `createSandbox`, `interactive`, `RunResult.resume/fork`, `Output`, the agent providers and the session store, plus phase 5). Upstream reference: `mattpocock/sandcastle` at commit `7a4e7f7`, files `createSandbox.ts`, `interactive.ts`, `Output.ts`, `extractStructuredOutput.ts`, `run.ts`, `SessionStore.ts`, `resumePrecheck.ts`, `AgentProvider.ts`, `Display.ts`, `shutdownRegistry.ts`, `terminalCleanup.ts`, and ADRs 0004, 0010, 0011, 0012, 0016, 0018, 0019. Clone with `git clone --depth 1 https://github.com/mattpocock/sandcastle.git /tmp/sandcastle && git -C /tmp/sandcastle checkout 7a4e7f7`.

## Global Constraints

- Module path `github.com/e11en/agentgopher`, `go 1.25` in `go.mod`.
- Standard library only. `golang.org/x/term` is the single permitted exception and this plan does not use it.
- Config directory is `.agentgopher/`. Logs in `.agentgopher/logs/`, worktrees in `.agentgopher/worktrees/`.
- Public API: root package, `agent`, `sandbox`, `sandbox/*`, `session`. Everything else under `internal/`.
- Code comments, test names and commit messages in English. No em or en dashes anywhere (use ` - ` or commas).
- Commit messages: imperative, no attribution footer, no `Co-Authored-By`.
- Every new exported type or function keeps the names given in the Interfaces blocks below; later phases import them.
- Tests must not require Claude Code, Codex, Pi, Docker or network. Use fake providers and handles backed by temp directories.
- Default sandbox session directories: Claude `/home/agent/.claude/projects`, Codex `/home/agent/.codex/sessions`, Pi `/home/agent/.pi/agent/sessions`. Host defaults: `$HOME/.claude/projects`, `$HOME/.codex/sessions`, `$HOME/.pi/agent/sessions`.
- Resume is one iteration (ADR 0011). Fork isolates the session only (ADR 0018). Resume needs filesystem-backed sessions (ADR 0016).

## Assumed from phases 3 and 4

This plan builds on names introduced by the phase 3 and 4 plans. If those plans landed with different names, adapt the calls in this plan to them; do not redefine the types.

```go
// package sandbox (phase 3 and 4)
type BindMountHandle interface {
    Handle
    CopyFileIn(ctx context.Context, hostPath, sandboxPath string) error
    CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error
}
type IsolatedHandle interface {
    Handle
    CopyIn(ctx context.Context, hostPath, sandboxPath string) error
    CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error
}
// sandbox.Provider has Kind(), Name(), Env(); RunOptions.Sandbox is sandbox.Provider.
// sandbox.BranchStrategy and the three BranchType constants exist since phase 1.

// package worktree, package start and package copy: see "Reconciliation with the phase 3 plan" below.

// package lifecycle (phase 3 additions to Options)
//   HostWorktreePath string; ApplyToHost func(ctx) error; KeepSourceBranch bool
//   Branch == "" means merge-to-head (lifecycle merges into the host branch).

// package orchestrator (phase 3 and 4): Options.Sandbox is sandbox.Provider,
// Options.BranchStrategy sandbox.BranchStrategy, Options.CopyToWorktree []string,
// Result.PreservedWorktreePath string. The per-iteration factory is
// internal/orchestrator/start.go: withSandbox(ctx, o Options, fn func(info sandboxInfo, h sandbox.Handle) (T, error)).

// root package (phase 3): CreateWorktree, *Worktree with Run, Close, Branch, Path;
// RunOptions.CopyToWorktree; RunResult.PreservedWorktreePath; resumeOptions pattern not yet present.
```

## Review Focus

1. A session JSONL whose last line is torn (writer killed mid-flush): transfer keeps that line verbatim and rewrites the rest. Test added in Task 1.
2. `Resume` on a run whose last iteration captured no session id: a clear error, not a nil dereference. Test added in Task 6.
3. Structured output where the agent emits the tag twice, the second time corrected: the last match wins. Test added in Task 7.
4. `Interactive` with a prompt file that still has a `{{KEY}}` after `PromptArgs`: the value is read from the terminal, and an empty answer is rejected and asked again. Test added in Task 11.
5. Two `CreateSandbox` handles open when SIGINT arrives: both teardown messages print, exactly once, and the process exits 1. Test added in Task 8.


## Reconciliation with the phase 3 plan

This plan was written in parallel with the phase 3 plan and assumed names that phase 3 did not choose. The phase 3 plan is authoritative. Use these names wherever this plan says otherwise:

| This plan says | Phase 3 actually defines |
|---|---|
| `worktree.Create(ctx, hostRepoDir, worktree.CreateOptions{Branch, BaseBranch, Name})` or `worktree.Create(ctx, worktree.Options{...})` returning `*worktree.Worktree` | `worktree.Create(ctx, repoDir, branch, baseBranch string) (worktree.Info, error)` for a named branch and `worktree.CreateTemp(ctx, repoDir, name string) (worktree.Info, error)` for merge-to-head; `worktree.Info{Path, Branch string; Unlock func()}` is a value, call `Unlock()` when done |
| `(*Worktree).Cleanup(ctx, succeeded)` | the `cleanup(ctx, failed bool) string` closure built in `internal/orchestrator/start.go` (`startSession`): dirty or failed worktrees are preserved and their path returned, clean successful ones are removed with `worktree.Remove` |
| `worktree.PruneStale` | `worktree.Prune(ctx, repoDir) error` |
| `copy.ToWorktree(...)` (package `internal/copy`) | `worktree.CopyToWorktree(ctx, paths []string, hostRepoDir, worktreePath string, timeout time.Duration) error` |
| `start.Sandbox(ctx, start.Options{...}) (start.Result{Handle, RepoDir}, error)` (package `internal/start`) | `orchestrator.startSession(ctx, Options) (*session, error)` in `internal/orchestrator/start.go`, with `session{handle sandbox.Handle; hostWorktreePath, sandboxRepoDir, lifecycleBranch string; cleanup func(ctx, failed bool) string}`; `Options.Worktree *worktree.Info` reuses an existing worktree. Where this plan needs it from the root package, export it as `orchestrator.StartSession` and `orchestrator.Session` with fields `Handle`, `HostWorktreePath`, `SandboxRepoDir`, `LifecycleBranch`, `Cleanup` (one rename task, no behaviour change) |
| `internal/orchestrator/factory.go` and `withSandbox` | `internal/orchestrator/start.go`, `startSession` plus `startHandle(ctx, Options, worktreeOrRepoPath) (sandbox.Handle, error)`, which switches on the provider type and returns an error for isolated providers until phase 4 fills that case in |
| `orchestrator.Options.BranchStrategy sandbox.BranchStrategy`, `Options.CopyToWorktree`, `Result.PreservedWorktreePath` | the same, already present in phase 3 |

A helper used below to pick the right constructor:

```go
// createWorktree creates the worktree a non-head strategy needs.
func createWorktree(ctx context.Context, hostRepoDir string, strategy sandbox.BranchStrategy, name string) (worktree.Info, error) {
	if strategy.Type == sandbox.BranchNamed {
		return worktree.Create(ctx, hostRepoDir, strategy.Branch, strategy.BaseBranch)
	}
	return worktree.CreateTemp(ctx, hostRepoDir, name)
}
```

---

## File structure

```
session/session.go                 HostSessionLookup, EncodeProjectPath, Claude paths, FindClaudeOnHost
session/transfer.go                TransferClaude, TransferCodex, TransferPi (pure JSONL rewrites)
session/codex.go                   FindCodexOnHost, LocateCodexOnHost, codex filename matching
session/pi.go                      EncodePiSessionDir, PiSessionDirPath, FindPiOnHost, LocatePiOnHost
session/session_test.go
agent/agent.go                     + InteractiveArgsBuilder, SessionStorage, SessionStorageProvider, SessionTransfer, HostSessionLookup alias
agent/storage.go                   readSandboxFile, writeSandboxFile helpers shared by the three storages
agent/claudecode.go                + SessionStorage(), BuildInteractiveArgs, ClaudeCodeOptions.SessionStorage
agent/claudecode_session.go        claudeStorage
agent/codex.go                     Codex provider and storage
agent/pi.go                        Pi provider and storage
agent/cursor.go, agent/opencode.go, agent/copilot.go
agent/*_test.go
agent/fakes_test.go                fake bind-mount handle backed by a temp dir
internal/orchestrator/orchestrator.go   + resume into sandbox, capture to host, SessionFilePath, Existing sandbox mode
internal/orchestrator/resume.go    AssertResumeSessionExists (port of resumePrecheck.ts)
agentgopher.go                     + ResumeSession, forkSession, Resume, Fork, Output wiring
output.go                          OutputDefinition, OutputObject, OutputString, WithMaxRetries, StructuredOutputError, extractStructuredOutput, retry feedback
output_test.go
internal/shutdown/shutdown.go      Register, process-wide signal fan-out, terminal cleanup
internal/shutdown/shutdown_test.go
internal/display/terminal.go       Terminal display with spinner and task log
internal/display/terminal_test.go
sandbox/sandbox.go                 + InteractiveExecOptions, Interactive
sandbox/nosandbox/nosandbox.go     + InteractiveExec
sandbox/docker/docker.go           + InteractiveExec (and podman)
interactive.go                     Interactive, InteractiveOptions, InteractiveResult, prompt arg collection
interactive_test.go
createsandbox.go                   CreateSandbox, Sandbox, SandboxRunOptions, SandboxRunResult, SandboxInteractiveOptions, SandboxInteractiveResult, SandboxExecOptions, CloseResult
createsandbox_test.go
worktree.go                        + (*Worktree).Interactive, (*Worktree).CreateSandbox
```

---

### Task 1: Public `session` package (pure path and transfer functions)

Port of the pure parts of `SessionStore.ts`: cwd encoding, host and sandbox paths, JSONL cwd rewriting, and host lookups by session id.

**Files:**
- Create: `session/session.go`, `session/transfer.go`, `session/codex.go`, `session/pi.go`, `session/session_test.go`

**Interfaces:**
- Produces:
  ```go
  type HostSessionLookup struct { Path string; SearchedRoot string } // Path "" when not found
  func EncodeProjectPath(cwd string) string
  func ClaudeHostPath(cwd, id, projectsDir string) string            // projectsDir "" means $HOME/.claude/projects
  func ClaudeSandboxPath(cwd, id, projectsDir string) string         // POSIX join
  func ClaudeSubagentsDirOnHost(cwd, id, projectsDir string) string
  func ClaudeSubagentsDirInSandbox(cwd, id, projectsDir string) string
  func FindClaudeOnHost(id, projectsDir string) HostSessionLookup
  func TransferClaude(jsonl, fromCwd, toCwd string) string
  func TransferCodex(jsonl, fromCwd, toCwd string) string
  func TransferPi(jsonl, fromCwd, toCwd string) string
  func FindCodexOnHost(id, sessionsDir string) HostSessionLookup
  type Location struct { Path, RelativePath string }
  func LocateCodexOnHost(id, sessionsDir string) (Location, error)
  func IsCodexSessionFilename(name, id string) bool
  func EncodePiSessionDir(cwd string) string
  func PiSessionDirPath(cwd, sessionsDir string) string
  func FindPiOnHost(id, sessionsDir string) HostSessionLookup
  func LocatePiOnHost(id, sessionsDir string) (Location, error)
  func IsPiSessionFilename(name, id string) bool
  func DefaultClaudeProjectsDir() string
  func DefaultCodexSessionsDir() string
  func DefaultPiSessionsDir() string
  ```

- [ ] **Step 1: Write the failing tests**

`session/session_test.go`:
```go
package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncodeProjectPath(t *testing.T) {
	cases := map[string]string{
		"/Users/ellen/repo":  "-Users-ellen-repo",
		"/Users/ellen/repo/": "-Users-ellen-repo",
		"/":                  "-",
		`C:\work\repo`:       "C-work-repo",
	}
	for in, want := range cases {
		if got := EncodeProjectPath(in); got != want {
			t.Errorf("EncodeProjectPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClaudePaths(t *testing.T) {
	if got := ClaudeHostPath("/r", "abc", "/p"); got != filepath.Join("/p", "-r", "abc.jsonl") {
		t.Fatalf("host = %q", got)
	}
	if got := ClaudeSandboxPath("/home/agent/workspace", "abc", "/home/agent/.claude/projects"); got != "/home/agent/.claude/projects/-home-agent-workspace/abc.jsonl" {
		t.Fatalf("sandbox = %q", got)
	}
	if got := ClaudeSubagentsDirInSandbox("/w", "abc", "/p"); got != "/p/-w/abc/subagents" {
		t.Fatalf("subagents = %q", got)
	}
}

func TestTransferClaudeRewritesCwdAndKeepsTornLine(t *testing.T) {
	in := `{"type":"user","cwd":"/sandbox/repo","text":"hi"}
{"type":"session_meta","payload":{"cwd":"/sandbox/repo"}}
{"type":"other","cwd":"/elsewhere"}
{"type":"user","cwd":"/sandbox/re`
	out := TransferClaude(in, "/sandbox/repo", "/host/repo")
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[0], `"cwd":"/host/repo"`) || !strings.Contains(lines[1], `"cwd":"/host/repo"`) {
		t.Fatalf("not rewritten:\n%s", out)
	}
	if !strings.Contains(lines[2], "/elsewhere") {
		t.Fatalf("unrelated cwd changed:\n%s", out)
	}
	if lines[3] != `{"type":"user","cwd":"/sandbox/re` {
		t.Fatalf("torn line altered: %q", lines[3])
	}
	if TransferClaude("", "/a", "/b") != "" {
		t.Fatal("empty input must stay empty")
	}
}

func TestTransferPiRewritesOnlySessionHeader(t *testing.T) {
	in := `{"type":"session","id":"x","cwd":"/s"}
{"type":"message","cwd":"/s"}`
	out := TransferPi(in, "/s", "/h")
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[0], `"cwd":"/h"`) || !strings.Contains(lines[1], `"cwd":"/s"`) {
		t.Fatalf("got:\n%s", out)
	}
}

func TestFindClaudeOnHost(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "-some-dir"), 0o755)
	os.WriteFile(filepath.Join(root, "-some-dir", "sess-1.jsonl"), []byte("{}"), 0o644)
	found := FindClaudeOnHost("sess-1", root)
	if found.Path != filepath.Join(root, "-some-dir", "sess-1.jsonl") || found.SearchedRoot != root {
		t.Fatalf("got %+v", found)
	}
	if FindClaudeOnHost("nope", root).Path != "" {
		t.Fatal("expected not found")
	}
	if FindClaudeOnHost("x", filepath.Join(root, "missing")).Path != "" {
		t.Fatal("missing root must not fail")
	}
}

func TestCodexLookupScansDateDirs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "10", "15")
	os.MkdirAll(dir, 0o755)
	file := filepath.Join(dir, "rollout-2026-10-15T10-00-00-thread-9.jsonl")
	os.WriteFile(file, []byte("{}"), 0o644)
	if !IsCodexSessionFilename("rollout-x-thread-9.jsonl", "thread-9") || IsCodexSessionFilename("other-thread-9.jsonl", "thread-9") {
		t.Fatal("filename matching wrong")
	}
	loc, err := LocateCodexOnHost("thread-9", root)
	if err != nil || loc.Path != file || loc.RelativePath != filepath.Join("2026", "10", "15", "rollout-2026-10-15T10-00-00-thread-9.jsonl") {
		t.Fatalf("loc = %+v, %v", loc, err)
	}
	if _, err := LocateCodexOnHost("missing", root); err == nil {
		t.Fatal("expected error")
	}
	if FindCodexOnHost("thread-9", root).Path != file {
		t.Fatal("find failed")
	}
}

func TestPiEncodingAndLookup(t *testing.T) {
	if got := EncodePiSessionDir("/Users/ellen/repo"); got != "--Users-ellen-repo--" {
		t.Fatalf("encode = %q", got)
	}
	if got := EncodePiSessionDir(`C:\work`); got != "--C-work--" {
		t.Fatalf("encode windows = %q", got)
	}
	root := t.TempDir()
	dir := PiSessionDirPath("/r", root)
	os.MkdirAll(dir, 0o755)
	file := filepath.Join(dir, "2026-10-15T10-00-00_sess-7.jsonl")
	os.WriteFile(file, []byte("{}"), 0o644)
	loc, err := LocatePiOnHost("sess-7", root)
	if err != nil || loc.Path != file || loc.RelativePath != filepath.Join("--r--", "2026-10-15T10-00-00_sess-7.jsonl") {
		t.Fatalf("loc = %+v, %v", loc, err)
	}
	if FindPiOnHost("nope", root).Path != "" {
		t.Fatal("expected not found")
	}
}

func TestDefaultDirsUseHome(t *testing.T) {
	t.Setenv("HOME", "/home/x")
	if DefaultClaudeProjectsDir() != filepath.Join("/home/x", ".claude", "projects") {
		t.Fatal(DefaultClaudeProjectsDir())
	}
	if DefaultCodexSessionsDir() != filepath.Join("/home/x", ".codex", "sessions") {
		t.Fatal(DefaultCodexSessionsDir())
	}
	if DefaultPiSessionsDir() != filepath.Join("/home/x", ".pi", "agent", "sessions") {
		t.Fatal(DefaultPiSessionsDir())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./session/`
Expected: FAIL, package not found.

- [ ] **Step 3: Write session/session.go**

```go
// Package session holds the pure path and transfer primitives for agent
// sessions. File I/O lives in the agent providers (ADR 0012).
package session

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
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

func hasSuffixFold(name, suffix string) bool { return strings.HasSuffix(name, suffix) }
```

- [ ] **Step 4: Write session/transfer.go**

```go
package session

import (
	"encoding/json"
	"strings"
)

// rewriteCwd replaces top-level "cwd" and session_meta.payload.cwd fields that
// equal fromCwd. A line that is not valid JSON (a torn final line) is kept
// verbatim so the rest of the session survives.
func rewriteCwd(jsonl, fromCwd, toCwd string) string {
	if jsonl == "" {
		return ""
	}
	lines := strings.Split(jsonl, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		changed := false
		if cwd, ok := entry["cwd"].(string); ok && cwd == fromCwd {
			entry["cwd"] = toCwd
			changed = true
		}
		if entry["type"] == "session_meta" {
			if payload, ok := entry["payload"].(map[string]any); ok {
				if cwd, ok := payload["cwd"].(string); ok && cwd == fromCwd {
					payload["cwd"] = toCwd
					changed = true
				}
			}
		}
		if changed {
			b, _ := json.Marshal(entry)
			lines[i] = string(b)
		}
	}
	return strings.Join(lines, "\n")
}

// TransferClaude rewrites cwd fields of a Claude Code session JSONL.
func TransferClaude(jsonl, fromCwd, toCwd string) string { return rewriteCwd(jsonl, fromCwd, toCwd) }

// TransferCodex rewrites cwd fields of a Codex rollout JSONL.
func TransferCodex(jsonl, fromCwd, toCwd string) string { return rewriteCwd(jsonl, fromCwd, toCwd) }

// TransferPi rewrites the cwd of the "session" header entry only. Pi refuses
// to load a session whose cwd does not exist, so this rewrite is load-bearing.
func TransferPi(jsonl, fromCwd, toCwd string) string {
	if jsonl == "" {
		return ""
	}
	lines := strings.Split(jsonl, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if cwd, ok := entry["cwd"].(string); entry["type"] == "session" && ok && cwd == fromCwd {
			entry["cwd"] = toCwd
			b, _ := json.Marshal(entry)
			lines[i] = string(b)
		}
	}
	return strings.Join(lines, "\n")
}
```

Note: `encoding/json` marshals maps with sorted keys, so rewritten lines may reorder fields. Every reader of these files parses JSON, so order does not matter. Non-rewritten lines are untouched.

- [ ] **Step 5: Write session/codex.go and session/pi.go**

`session/codex.go`:
```go
package session

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// IsCodexSessionFilename reports whether name is rollout-*-<id>.jsonl.
func IsCodexSessionFilename(name, id string) bool {
	return strings.HasPrefix(name, "rollout-") && strings.HasSuffix(name, "-"+id+".jsonl")
}

func findCodexPath(root, id string) string {
	var found string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && IsCodexSessionFilename(d.Name(), id) {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// FindCodexOnHost locates a Codex rollout file by id under the date-nested sessions dir.
func FindCodexOnHost(id, sessionsDir string) HostSessionLookup {
	root := orDefault(sessionsDir, DefaultCodexSessionsDir)
	return HostSessionLookup{Path: findCodexPath(root, id), SearchedRoot: root}
}

// LocateCodexOnHost is FindCodexOnHost plus the relative path, failing when absent.
func LocateCodexOnHost(id, sessionsDir string) (Location, error) {
	root := orDefault(sessionsDir, DefaultCodexSessionsDir)
	p := findCodexPath(root, id)
	if p == "" {
		return Location{}, fmt.Errorf("session %s not found in %s", id, root)
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return Location{}, err
	}
	return Location{Path: p, RelativePath: rel}, nil
}

var _ = os.ReadDir
```

`session/pi.go`:
```go
package session

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var piLeadingSep = regexp.MustCompile(`^[/\\]`)
var piReplace = regexp.MustCompile(`[/\\:]`)

// EncodePiSessionDir encodes a cwd the way pi names its session directory:
// leading separator stripped, separators and drive colons replaced by "-",
// wrapped in "--" markers. Verified against pi 0.73.1.
func EncodePiSessionDir(cwd string) string {
	return "--" + piReplace.ReplaceAllString(piLeadingSep.ReplaceAllString(cwd, ""), "-") + "--"
}

// PiSessionDirPath is the host session directory for cwd.
func PiSessionDirPath(cwd, sessionsDir string) string {
	return filepath.Join(orDefault(sessionsDir, DefaultPiSessionsDir), EncodePiSessionDir(cwd))
}

// IsPiSessionFilename reports whether name is *_<id>.jsonl.
func IsPiSessionFilename(name, id string) bool { return strings.HasSuffix(name, "_"+id+".jsonl") }

func findPi(root, id string) (Location, bool) {
	dirs, err := os.ReadDir(root)
	if err != nil {
		return Location{}, false
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, d.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if IsPiSessionFilename(f.Name(), id) {
				return Location{Path: filepath.Join(root, d.Name(), f.Name()), RelativePath: filepath.Join(d.Name(), f.Name())}, true
			}
		}
	}
	return Location{}, false
}

// FindPiOnHost locates a pi session by id across all encoded cwd directories.
func FindPiOnHost(id, sessionsDir string) HostSessionLookup {
	root := orDefault(sessionsDir, DefaultPiSessionsDir)
	loc, ok := findPi(root, id)
	if !ok {
		return HostSessionLookup{SearchedRoot: root}
	}
	return HostSessionLookup{Path: loc.Path, SearchedRoot: root}
}

// LocatePiOnHost is FindPiOnHost plus the relative path, failing when absent.
func LocatePiOnHost(id, sessionsDir string) (Location, error) {
	root := orDefault(sessionsDir, DefaultPiSessionsDir)
	loc, ok := findPi(root, id)
	if !ok {
		return Location{}, fmt.Errorf("session %s not found in %s", id, root)
	}
	return loc, nil
}
```

Remove the unused `hasSuffixFold` helper and the `var _ = os.ReadDir` line before committing; they are not needed.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./session/ && go vet ./session/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add session/
git commit -m "Add session package with path encoding and JSONL transfer"
```

---

### Task 2: Session storage interface and the Claude Code storage

Port of `AgentSessionStorage` and `makeClaudeSessionStorage` from `AgentProvider.ts`.

**Files:**
- Modify: `agent/agent.go`, `agent/claudecode.go`
- Create: `agent/storage.go`, `agent/claudecode_session.go`, `agent/fakes_test.go`, `agent/claudecode_session_test.go`

**Interfaces:**
- Produces, in package `agent`:
  ```go
  type HostSessionLookup = session.HostSessionLookup
  type SessionTransfer struct { HostCwd, SandboxCwd, SessionID string; Handle sandbox.BindMountHandle }
  type SessionStorage interface {
      CaptureToHost(ctx context.Context, t SessionTransfer) error
      ResumeIntoSandbox(ctx context.Context, t SessionTransfer) error
      ReadHostSession(cwd, sessionID string) (content string, ok bool, err error)
      ExistsOnHost(cwd, sessionID string) (bool, error)
      HostSessionFilePath(cwd, sessionID string) string   // "" when not derivable
      FindByIDOnHost(sessionID string) (HostSessionLookup, error)
  }
  type SessionStorageProvider interface { SessionStorage() SessionStorage }
  type InteractiveArgsBuilder interface { BuildInteractiveArgs(opts CommandOptions) []string }
  type SessionDirs struct { Host, Sandbox string }   // overrides for tests and non-standard installs
  // ClaudeCodeOptions gains: SessionStorage *SessionDirs
  ```
- `ClaudeCode(...)` now returns a value that also implements `SessionStorageProvider`, `SessionUsageParser` and `InteractiveArgsBuilder`.

- [ ] **Step 1: Add the interfaces to agent/agent.go**

Append:
```go
// HostSessionLookup is re-exported for providers implementing SessionStorage.
type HostSessionLookup = session.HostSessionLookup

// SessionTransfer names the two sides of a session move.
type SessionTransfer struct {
	HostCwd    string
	SandboxCwd string
	SessionID  string
	Handle     sandbox.BindMountHandle
}

// SessionStorage is implemented by providers whose sessions can be captured
// and resumed. Storage layout and transfer rules are provider-owned (ADR 0012).
type SessionStorage interface {
	CaptureToHost(ctx context.Context, t SessionTransfer) error
	ResumeIntoSandbox(ctx context.Context, t SessionTransfer) error
	// ReadHostSession returns the captured content, ok=false when absent.
	ReadHostSession(cwd, sessionID string) (string, bool, error)
	ExistsOnHost(cwd, sessionID string) (bool, error)
	// HostSessionFilePath is the host path a session would have, or "" when
	// the layout does not allow deriving it from (cwd, id).
	HostSessionFilePath(cwd, sessionID string) string
	FindByIDOnHost(sessionID string) (HostSessionLookup, error)
}

// SessionStorageProvider is implemented by resumable providers.
type SessionStorageProvider interface {
	SessionStorage() SessionStorage
}

// InteractiveArgsBuilder is implemented by providers that support Interactive.
type InteractiveArgsBuilder interface {
	BuildInteractiveArgs(opts CommandOptions) []string
}

// SessionDirs overrides a provider's session directories.
type SessionDirs struct {
	Host    string
	Sandbox string
}
```
Add the imports `"context"`, `"github.com/e11en/agentgopher/sandbox"`, `"github.com/e11en/agentgopher/session"`.

- [ ] **Step 2: Write the test fake and the failing tests**

`agent/fakes_test.go`:
```go
package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/e11en/agentgopher/sandbox"
)

// dirHandle is a bind-mount handle whose "sandbox filesystem" is a host temp
// dir. Sandbox paths are mapped under root; exec runs sh on the host with
// the same mapping applied to absolute paths in the command.
type dirHandle struct {
	root string
}

func (h *dirHandle) WorktreePath() string          { return filepath.Join(h.root, "home/agent/workspace") }
func (h *dirHandle) Close(context.Context) error  { return nil }
func (h *dirHandle) mapPath(p string) string       { return filepath.Join(h.root, p) }

func (h *dirHandle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", strings.ReplaceAll(command, "/home/agent", h.mapPath("/home/agent")))
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = 1
	}
	return sandbox.ExecResult{Stdout: string(out), ExitCode: code}, nil
}

func (h *dirHandle) CopyFileIn(_ context.Context, hostPath, sandboxPath string) error {
	b, err := os.ReadFile(hostPath)
	if err != nil {
		return err
	}
	dst := h.mapPath(sandboxPath)
	os.MkdirAll(filepath.Dir(dst), 0o755)
	return os.WriteFile(dst, b, 0o644)
}

func (h *dirHandle) CopyFileOut(_ context.Context, sandboxPath, hostPath string) error {
	b, err := os.ReadFile(h.mapPath(sandboxPath))
	if err != nil {
		return err
	}
	os.MkdirAll(filepath.Dir(hostPath), 0o755)
	return os.WriteFile(hostPath, b, 0o644)
}
```

`agent/claudecode_session_test.go`:
```go
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/session"
)

func TestClaudeStorageCaptureRewritesCwdAndCopiesSubagents(t *testing.T) {
	root := t.TempDir()
	hostProjects := filepath.Join(t.TempDir(), "projects")
	h := &dirHandle{root: root}
	p := ClaudeCode("m", &ClaudeCodeOptions{SessionStorage: &SessionDirs{Host: hostProjects, Sandbox: "/home/agent/.claude/projects"}})
	st := p.(SessionStorageProvider).SessionStorage()

	sandboxCwd, hostCwd := "/home/agent/workspace", "/Users/ellen/repo"
	sessPath := h.mapPath(session.ClaudeSandboxPath(sandboxCwd, "s1", "/home/agent/.claude/projects"))
	os.MkdirAll(filepath.Dir(sessPath), 0o755)
	os.WriteFile(sessPath, []byte(`{"cwd":"/home/agent/workspace","type":"user"}`+"\n"), 0o644)
	subDir := h.mapPath(session.ClaudeSubagentsDirInSandbox(sandboxCwd, "s1", "/home/agent/.claude/projects"))
	os.MkdirAll(subDir, 0o755)
	os.WriteFile(filepath.Join(subDir, "agent-a.jsonl"), []byte(`{"cwd":"/home/agent/workspace"}`), 0o644)

	if err := st.CaptureToHost(context.Background(), SessionTransfer{HostCwd: hostCwd, SandboxCwd: sandboxCwd, SessionID: "s1", Handle: h}); err != nil {
		t.Fatal(err)
	}
	content, ok, err := st.ReadHostSession(hostCwd, "s1")
	if err != nil || !ok || !strings.Contains(content, `"cwd":"/Users/ellen/repo"`) {
		t.Fatalf("content=%q ok=%v err=%v", content, ok, err)
	}
	if st.HostSessionFilePath(hostCwd, "s1") != session.ClaudeHostPath(hostCwd, "s1", hostProjects) {
		t.Fatal("HostSessionFilePath mismatch")
	}
	sub, err := os.ReadFile(filepath.Join(session.ClaudeSubagentsDirOnHost(hostCwd, "s1", hostProjects), "agent-a.jsonl"))
	if err != nil || !strings.Contains(string(sub), "/Users/ellen/repo") {
		t.Fatalf("subagent: %q %v", sub, err)
	}
	exists, _ := st.ExistsOnHost(hostCwd, "s1")
	if !exists {
		t.Fatal("ExistsOnHost false")
	}
	if lookup, _ := st.FindByIDOnHost("s1"); lookup.Path == "" {
		t.Fatal("FindByIDOnHost failed")
	}
}

func TestClaudeStorageResumeIntoSandbox(t *testing.T) {
	root := t.TempDir()
	hostProjects := t.TempDir()
	h := &dirHandle{root: root}
	st := ClaudeCode("m", &ClaudeCodeOptions{SessionStorage: &SessionDirs{Host: hostProjects, Sandbox: "/home/agent/.claude/projects"}}).(SessionStorageProvider).SessionStorage()
	hostCwd, sandboxCwd := "/Users/ellen/repo", "/home/agent/workspace"
	hostPath := session.ClaudeHostPath(hostCwd, "s2", hostProjects)
	os.MkdirAll(filepath.Dir(hostPath), 0o755)
	os.WriteFile(hostPath, []byte(`{"cwd":"/Users/ellen/repo"}`), 0o644)

	if err := st.ResumeIntoSandbox(context.Background(), SessionTransfer{HostCwd: hostCwd, SandboxCwd: sandboxCwd, SessionID: "s2", Handle: h}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(h.mapPath(session.ClaudeSandboxPath(sandboxCwd, "s2", "/home/agent/.claude/projects")))
	if err != nil || !strings.Contains(string(b), `"cwd":"/home/agent/workspace"`) {
		t.Fatalf("got %q, %v", b, err)
	}
}

func TestClaudeStorageMissingSessionIsFatal(t *testing.T) {
	st := ClaudeCode("m", &ClaudeCodeOptions{SessionStorage: &SessionDirs{Host: t.TempDir(), Sandbox: "/home/agent/.claude/projects"}}).(SessionStorageProvider).SessionStorage()
	err := st.CaptureToHost(context.Background(), SessionTransfer{HostCwd: "/h", SandboxCwd: "/s", SessionID: "none", Handle: &dirHandle{root: t.TempDir()}})
	if err == nil {
		t.Fatal("expected error when the main session file is missing")
	}
	if _, ok, _ := st.ReadHostSession("/h", "none"); ok {
		t.Fatal("expected ok=false")
	}
}

func TestClaudeInteractiveArgs(t *testing.T) {
	args := ClaudeCode("m", &ClaudeCodeOptions{Effort: "high"}).(InteractiveArgsBuilder).BuildInteractiveArgs(CommandOptions{Prompt: "hi", DangerouslySkipPermissions: true})
	want := []string{"claude", "--dangerously-skip-permissions", "--model", "m", "--effort", "high", "hi"}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v", args)
	}
	args = ClaudeCode("m", &ClaudeCodeOptions{PermissionMode: "plan"}).(InteractiveArgsBuilder).BuildInteractiveArgs(CommandOptions{DangerouslySkipPermissions: true})
	if strings.Join(args, " ") != "claude --permission-mode plan --model m" {
		t.Fatalf("args = %v", args)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./agent/`
Expected: FAIL, "undefined: SessionDirs" or missing method.

- [ ] **Step 4: Write agent/storage.go**

```go
package agent

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/e11en/agentgopher/sandbox"
)

// readSandboxFile copies a sandbox file to a temp file on the host and returns its content.
func readSandboxFile(ctx context.Context, h sandbox.BindMountHandle, sandboxPath string) (string, error) {
	tmp, err := os.CreateTemp("", "agentgopher-session-*.jsonl")
	if err != nil {
		return "", err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if err := h.CopyFileOut(ctx, sandboxPath, tmp.Name()); err != nil {
		return "", err
	}
	b, err := os.ReadFile(tmp.Name())
	return string(b), err
}

// writeSandboxFile writes content to a sandbox path, creating its directory.
func writeSandboxFile(ctx context.Context, h sandbox.BindMountHandle, sandboxPath, content string) error {
	tmp, err := os.CreateTemp("", "agentgopher-session-*.jsonl")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	if _, err := h.Exec(ctx, fmt.Sprintf("mkdir -p %s", ShellEscape(path.Dir(sandboxPath))), sandbox.ExecOptions{}); err != nil {
		return err
	}
	return h.CopyFileIn(ctx, tmp.Name(), sandboxPath)
}

func writeHostFile(p, content string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func readHostFile(p string) (string, bool, error) {
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

func fileExists(p string) (bool, error) {
	_, err := os.Stat(p)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}
```

- [ ] **Step 5: Write agent/claudecode_session.go and extend claudecode.go**

`agent/claudecode_session.go`:
```go
package agent

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/session"
)

const defaultClaudeSandboxProjectsDir = "/home/agent/.claude/projects"

type claudeStorage struct {
	hostDir    string // "" means the default
	sandboxDir string
}

func newClaudeStorage(dirs *SessionDirs) *claudeStorage {
	s := &claudeStorage{sandboxDir: defaultClaudeSandboxProjectsDir}
	if dirs != nil {
		s.hostDir = dirs.Host
		if dirs.Sandbox != "" {
			s.sandboxDir = dirs.Sandbox
		}
	}
	return s
}

func (s *claudeStorage) HostSessionFilePath(cwd, id string) string {
	return session.ClaudeHostPath(cwd, id, s.hostDir)
}

func (s *claudeStorage) ExistsOnHost(cwd, id string) (bool, error) {
	return fileExists(s.HostSessionFilePath(cwd, id))
}

func (s *claudeStorage) ReadHostSession(cwd, id string) (string, bool, error) {
	return readHostFile(s.HostSessionFilePath(cwd, id))
}

func (s *claudeStorage) FindByIDOnHost(id string) (HostSessionLookup, error) {
	return session.FindClaudeOnHost(id, s.hostDir), nil
}

func (s *claudeStorage) copyOut(ctx context.Context, h sandbox.BindMountHandle, src, fromCwd, toCwd, dst string) error {
	jsonl, err := readSandboxFile(ctx, h, src)
	if err != nil {
		return err
	}
	return writeHostFile(dst, session.TransferClaude(jsonl, fromCwd, toCwd))
}

func (s *claudeStorage) CaptureToHost(ctx context.Context, t SessionTransfer) error {
	// The main session is fatal when missing; the user expects their session.
	main := session.ClaudeSandboxPath(t.SandboxCwd, t.SessionID, s.sandboxDir)
	if err := s.copyOut(ctx, t.Handle, main, t.SandboxCwd, t.HostCwd, s.HostSessionFilePath(t.HostCwd, t.SessionID)); err != nil {
		return fmt.Errorf("capture session %s: %w", t.SessionID, err)
	}
	// Subagent transcripts are best-effort: a missing directory is the normal case.
	subDir := session.ClaudeSubagentsDirInSandbox(t.SandboxCwd, t.SessionID, s.sandboxDir)
	res, err := t.Handle.Exec(ctx, fmt.Sprintf("find %s -type f -name 'agent-*.jsonl' 2>/dev/null", ShellEscape(subDir)), sandbox.ExecOptions{})
	if err != nil || res.ExitCode != 0 || strings.TrimSpace(res.Stdout) == "" {
		return nil
	}
	hostSubDir := session.ClaudeSubagentsDirOnHost(t.HostCwd, t.SessionID, s.hostDir)
	for _, p := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if err := s.copyOut(ctx, t.Handle, p, t.SandboxCwd, t.HostCwd, filepath.Join(hostSubDir, path.Base(p))); err != nil {
			fmt.Fprintf(os.Stderr, "agentgopher: failed to capture Claude subagent transcript %s: %v\n", p, err)
		}
	}
	return nil
}

func (s *claudeStorage) ResumeIntoSandbox(ctx context.Context, t SessionTransfer) error {
	b, err := os.ReadFile(s.HostSessionFilePath(t.HostCwd, t.SessionID))
	if err != nil {
		return err
	}
	dst := session.ClaudeSandboxPath(t.SandboxCwd, t.SessionID, s.sandboxDir)
	return writeSandboxFile(ctx, t.Handle, dst, session.TransferClaude(string(b), t.HostCwd, t.SandboxCwd))
}
```

In `agent/claudecode.go`:
- Add `SessionStorage *SessionDirs` to `ClaudeCodeOptions`.
- Add a field `storage *claudeStorage` to `claudeCode`, set in `ClaudeCode`: `p.storage = newClaudeStorage(p.opts.SessionStorage)`.
- Add:
```go
func (p *claudeCode) SessionStorage() SessionStorage { return p.storage }

func (p *claudeCode) BuildInteractiveArgs(o CommandOptions) []string {
	args := []string{"claude"}
	switch {
	case p.opts.PermissionMode != "":
		args = append(args, "--permission-mode", p.opts.PermissionMode)
	case o.DangerouslySkipPermissions:
		args = append(args, "--dangerously-skip-permissions")
	}
	args = append(args, "--model", p.model)
	if p.opts.Effort != "" {
		args = append(args, "--effort", p.opts.Effort)
	}
	if o.Prompt != "" {
		args = append(args, o.Prompt)
	}
	return args
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./agent/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add agent/
git commit -m "Add session storage interface and Claude Code session capture and resume"
```

---

### Task 3: Codex provider with session storage

Port of `codex()`, `parseCodexStreamLine`, `parseCodexUsage` and `makeCodexSessionStorage`.

**Files:**
- Create: `agent/codex.go`, `agent/codex_test.go`

**Interfaces:**
- Produces:
  ```go
  type CodexOptions struct {
      Effort            string            // low, medium, high, xhigh
      Env               map[string]string
      CaptureSessions   *bool
      SessionStorage    *SessionDirs
      ApprovalsReviewer string            // "" or "user" or "auto_review"
  }
  func Codex(model string, opts *CodexOptions) Provider  // also SessionStorageProvider, InteractiveArgsBuilder
  ```

- [ ] **Step 1: Write the failing tests**

`agent/codex_test.go`:
```go
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/session"
)

func TestCodexPrintCommand(t *testing.T) {
	p := Codex("gpt-5", nil)
	pc := p.BuildPrintCommand(CommandOptions{Prompt: "x", DangerouslySkipPermissions: true})
	if pc.Command != "codex exec --json --dangerously-bypass-approvals-and-sandbox -m 'gpt-5'" || pc.Stdin == nil || *pc.Stdin != "x" {
		t.Fatalf("got %q", pc.Command)
	}
	pc = Codex("m", &CodexOptions{Effort: "high", ApprovalsReviewer: "auto_review"}).BuildPrintCommand(CommandOptions{Prompt: "x", ResumeSession: "t1"})
	want := `codex exec resume 't1' --json -a on-request -s danger-full-access -c 'approvals_reviewer="auto_review"' -m 'm' -c 'model_reasoning_effort="high"' -`
	if pc.Command != want {
		t.Fatalf("got  %q\nwant %q", pc.Command, want)
	}
	pc = Codex("m", nil).BuildPrintCommand(CommandOptions{Prompt: "x", ResumeSession: "t1", ForkSession: true})
	if !strings.HasPrefix(pc.Command, "codex exec fork 't1' --json") || !strings.HasSuffix(pc.Command, " -") {
		t.Fatalf("got %q", pc.Command)
	}
}

func TestCodexParseStreamLine(t *testing.T) {
	p := Codex("m", nil)
	cases := []struct {
		line string
		want []StreamEvent
	}{
		{`{"type":"thread.started","thread_id":"t1"}`, []StreamEvent{{Type: EventSessionID, SessionID: "t1"}}},
		{`{"type":"item.completed","item":{"type":"agent_message","text":"done"}}`, []StreamEvent{{Type: EventText, Text: "done"}, {Type: EventResult, Text: "done"}}},
		{`{"type":"item.started","item":{"type":"command_execution","command":"ls"}}`, []StreamEvent{{Type: EventToolCall, Name: "Bash", Args: "ls"}}},
		{`{"type":"error","message":"rate limited"}`, []StreamEvent{{Type: EventResult, Text: "rate limited"}}},
		{`{"type":"error","error":{"message":"nested"}}`, []StreamEvent{{Type: EventResult, Text: "nested"}}},
		{`{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":7}}`, []StreamEvent{{Type: EventUsage, Usage: &Usage{InputTokens: 60, CacheReadInputTokens: 40, OutputTokens: 7}}}},
		{`{"type":"turn.completed","usage":{"input_tokens":"x"}}`, nil},
		{`garbage`, nil},
	}
	for _, c := range cases {
		got := p.ParseStreamLine(c.line)
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %+v", c.line, got)
		}
		for i := range got {
			if got[i].Usage != nil {
				if *got[i].Usage != *c.want[i].Usage {
					t.Fatalf("%s: usage %+v", c.line, *got[i].Usage)
				}
				continue
			}
			if got[i] != c.want[i] {
				t.Fatalf("%s: event %d = %+v, want %+v", c.line, i, got[i], c.want[i])
			}
		}
	}
}

func TestCodexStorageRoundTrip(t *testing.T) {
	root := t.TempDir()
	hostSessions := t.TempDir()
	h := &dirHandle{root: root}
	st := Codex("m", &CodexOptions{SessionStorage: &SessionDirs{Host: hostSessions, Sandbox: "/home/agent/.codex/sessions"}}).(SessionStorageProvider).SessionStorage()

	sandboxFile := h.mapPath("/home/agent/.codex/sessions/2026/10/15/rollout-2026-10-15T10-00-00-t1.jsonl")
	os.MkdirAll(filepath.Dir(sandboxFile), 0o755)
	os.WriteFile(sandboxFile, []byte(`{"type":"session_meta","payload":{"cwd":"/home/agent/workspace"}}`), 0o644)

	tr := SessionTransfer{HostCwd: "/h", SandboxCwd: "/home/agent/workspace", SessionID: "t1", Handle: h}
	if err := st.CaptureToHost(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	hostFile := filepath.Join(hostSessions, "2026", "10", "15", "rollout-2026-10-15T10-00-00-t1.jsonl")
	b, err := os.ReadFile(hostFile)
	if err != nil || !strings.Contains(string(b), `"cwd":"/h"`) {
		t.Fatalf("host file: %q %v", b, err)
	}
	if st.HostSessionFilePath("/h", "t1") != hostFile {
		t.Fatalf("HostSessionFilePath = %q", st.HostSessionFilePath("/h", "t1"))
	}
	if st.HostSessionFilePath("/h", "unknown") != "" {
		t.Fatal("unknown id must yield empty path")
	}
	if ok, _ := st.ExistsOnHost("/h", "t1"); !ok {
		t.Fatal("ExistsOnHost")
	}

	os.Remove(sandboxFile)
	if err := st.ResumeIntoSandbox(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(sandboxFile)
	if err != nil || !strings.Contains(string(b), `"cwd":"/home/agent/workspace"`) {
		t.Fatalf("sandbox file: %q %v", b, err)
	}
	if _, err := LocateCodexSandbox(context.Background(), h, "missing", "/home/agent/.codex/sessions"); err == nil {
		t.Fatal("expected error for missing sandbox session")
	}
	_ = session.IsCodexSessionFilename
}

func TestCodexInteractiveArgs(t *testing.T) {
	args := Codex("m", nil).(InteractiveArgsBuilder).BuildInteractiveArgs(CommandOptions{Prompt: "go"})
	if strings.Join(args, " ") != "codex --model m go" {
		t.Fatalf("args = %v", args)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./agent/ -run Codex`
Expected: FAIL, "undefined: Codex".

- [ ] **Step 3: Write agent/codex.go**

```go
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/session"
)

const defaultCodexSandboxSessionsDir = "/home/agent/.codex/sessions"

// CodexOptions configures the Codex provider.
type CodexOptions struct {
	Effort          string
	Env             map[string]string
	CaptureSessions *bool
	SessionStorage  *SessionDirs
	// ApprovalsReviewer "auto_review" swaps the bypass flag for an interactive
	// approval policy reviewed by Codex's own reviewer agent.
	ApprovalsReviewer string
}

type codex struct {
	model   string
	opts    CodexOptions
	storage *codexStorage
}

// Codex returns a provider for the `codex` CLI.
func Codex(model string, opts *CodexOptions) Provider {
	p := &codex{model: model}
	if opts != nil {
		p.opts = *opts
	}
	p.storage = newCodexStorage(p.opts.SessionStorage)
	return p
}

func (p *codex) Name() string                   { return "codex" }
func (p *codex) Env() map[string]string         { return p.opts.Env }
func (p *codex) CaptureSessions() bool          { return p.opts.CaptureSessions == nil || *p.opts.CaptureSessions }
func (p *codex) SessionStorage() SessionStorage { return p.storage }

func (p *codex) BuildPrintCommand(o CommandOptions) PrintCommand {
	var base string
	switch {
	case o.ResumeSession != "" && o.ForkSession:
		// Codex separates fork from resume at the verb level (ADR 0018).
		base = "codex exec fork " + ShellEscape(o.ResumeSession)
	case o.ResumeSession != "":
		base = "codex exec resume " + ShellEscape(o.ResumeSession)
	default:
		base = "codex exec"
	}
	approvals := " --dangerously-bypass-approvals-and-sandbox"
	if p.opts.ApprovalsReviewer == "auto_review" {
		approvals = " -a on-request -s danger-full-access -c " + ShellEscape(`approvals_reviewer="auto_review"`)
	}
	effort := ""
	if p.opts.Effort != "" {
		effort = " -c " + ShellEscape(fmt.Sprintf(`model_reasoning_effort="%s"`, p.opts.Effort))
	}
	stdinArg := ""
	if o.ResumeSession != "" {
		stdinArg = " -"
	}
	prompt := o.Prompt
	return PrintCommand{Command: base + " --json" + approvals + " -m " + ShellEscape(p.model) + effort + stdinArg, Stdin: &prompt}
}

func (p *codex) BuildInteractiveArgs(o CommandOptions) []string {
	args := []string{"codex", "--model", p.model}
	if o.Prompt != "" {
		args = append(args, o.Prompt)
	}
	return args
}

// extractErrorMessage reads { error: "s" }, { error: { message } },
// { error: { data: { message } } } or { message }.
func extractErrorMessage(obj map[string]any) string {
	switch e := obj["error"].(type) {
	case string:
		return e
	case map[string]any:
		if m, ok := e["message"].(string); ok {
			return m
		}
		if d, ok := e["data"].(map[string]any); ok {
			if m, ok := d["message"].(string); ok {
				return m
			}
		}
	}
	if m, ok := obj["message"].(string); ok {
		return m
	}
	return ""
}

func parseJSONObject(line string) (map[string]any, bool) {
	if !strings.HasPrefix(line, "{") {
		return nil, false
	}
	var obj map[string]any
	if json.Unmarshal([]byte(line), &obj) != nil {
		return nil, false
	}
	return obj, true
}

func (p *codex) ParseStreamLine(line string) []StreamEvent {
	obj, ok := parseJSONObject(line)
	if !ok {
		return nil
	}
	item, _ := obj["item"].(map[string]any)
	switch obj["type"] {
	case "thread.started":
		if id, ok := obj["thread_id"].(string); ok {
			return []StreamEvent{{Type: EventSessionID, SessionID: id}}
		}
	case "item.completed":
		if text, ok := item["text"].(string); ok && item["type"] == "agent_message" {
			return []StreamEvent{{Type: EventText, Text: text}, {Type: EventResult, Text: text}}
		}
	case "item.started":
		if cmd, ok := item["command"].(string); ok && item["type"] == "command_execution" {
			return []StreamEvent{{Type: EventToolCall, Name: "Bash", Args: cmd}}
		}
	case "error":
		if msg := extractErrorMessage(obj); msg != "" {
			return []StreamEvent{{Type: EventResult, Text: msg}}
		}
	case "turn.completed":
		if u, ok := parseCodexUsage(obj["usage"]); ok {
			return []StreamEvent{{Type: EventUsage, Usage: &u}}
		}
	}
	return nil
}

// parseCodexUsage maps { input_tokens, cached_input_tokens, output_tokens } to
// Usage. input_tokens is the total, so the cached part moves to
// CacheReadInputTokens and the remainder to InputTokens.
func parseCodexUsage(v any) (Usage, bool) {
	u, ok := v.(map[string]any)
	if !ok {
		return Usage{}, false
	}
	in, ok1 := u["input_tokens"].(float64)
	cached, ok2 := u["cached_input_tokens"].(float64)
	out, ok3 := u["output_tokens"].(float64)
	if !ok1 || !ok2 || !ok3 {
		return Usage{}, false
	}
	return Usage{InputTokens: int(in - cached), CacheReadInputTokens: int(cached), OutputTokens: int(out)}, true
}

// codexStorage: sessions live at YYYY/MM/DD/rollout-*-<id>.jsonl, so the host
// path is not derivable from (cwd, id). Captured paths are cached per id.
type codexStorage struct {
	hostDir    string
	sandboxDir string
	mu         sync.Mutex
	captured   map[string]string
}

func newCodexStorage(dirs *SessionDirs) *codexStorage {
	s := &codexStorage{sandboxDir: defaultCodexSandboxSessionsDir, captured: map[string]string{}}
	if dirs != nil {
		s.hostDir = dirs.Host
		if dirs.Sandbox != "" {
			s.sandboxDir = dirs.Sandbox
		}
	}
	return s
}

func (s *codexStorage) hostRoot() string {
	if s.hostDir != "" {
		return s.hostDir
	}
	return session.DefaultCodexSessionsDir()
}

func (s *codexStorage) HostSessionFilePath(_, id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.captured[id]
}

func (s *codexStorage) ExistsOnHost(_, id string) (bool, error) {
	return session.FindCodexOnHost(id, s.hostDir).Path != "", nil
}

func (s *codexStorage) ReadHostSession(_, id string) (string, bool, error) {
	found := session.FindCodexOnHost(id, s.hostDir)
	if found.Path == "" {
		return "", false, nil
	}
	return readHostFile(found.Path)
}

func (s *codexStorage) FindByIDOnHost(id string) (HostSessionLookup, error) {
	return session.FindCodexOnHost(id, s.hostDir), nil
}

// LocateCodexSandbox finds rollout-*-<id>.jsonl inside the sandbox.
func LocateCodexSandbox(ctx context.Context, h sandbox.BindMountHandle, id, sessionsDir string) (session.Location, error) {
	res, err := h.Exec(ctx, fmt.Sprintf("find %s -type f -name %s -print -quit", ShellEscape(sessionsDir), ShellEscape("rollout-*-"+id+".jsonl")), sandbox.ExecOptions{})
	if err != nil {
		return session.Location{}, err
	}
	p := strings.SplitN(strings.TrimSpace(res.Stdout), "\n", 2)[0]
	if res.ExitCode != 0 || p == "" {
		return session.Location{}, fmt.Errorf("session %s not found in %s", id, sessionsDir)
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(p, sessionsDir), "/")
	return session.Location{Path: p, RelativePath: rel}, nil
}

func (s *codexStorage) CaptureToHost(ctx context.Context, t SessionTransfer) error {
	loc, err := LocateCodexSandbox(ctx, t.Handle, t.SessionID, s.sandboxDir)
	if err != nil {
		return err
	}
	jsonl, err := readSandboxFile(ctx, t.Handle, loc.Path)
	if err != nil {
		return err
	}
	target := filepath.Join(s.hostRoot(), filepath.FromSlash(loc.RelativePath))
	if err := writeHostFile(target, session.TransferCodex(jsonl, t.SandboxCwd, t.HostCwd)); err != nil {
		return err
	}
	s.mu.Lock()
	s.captured[t.SessionID] = target
	s.mu.Unlock()
	return nil
}

func (s *codexStorage) ResumeIntoSandbox(ctx context.Context, t SessionTransfer) error {
	loc, err := session.LocateCodexOnHost(t.SessionID, s.hostDir)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(loc.Path)
	if err != nil {
		return err
	}
	target := path.Join(s.sandboxDir, filepath.ToSlash(loc.RelativePath))
	return writeSandboxFile(ctx, t.Handle, target, session.TransferCodex(string(b), t.HostCwd, t.SandboxCwd))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./agent/ -run Codex`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add agent/codex.go agent/codex_test.go
git commit -m "Add Codex provider with session storage"
```

---

### Task 4: Pi provider with session storage

Port of `pi()`, `parsePiStreamLine` and `makePiSessionStorage`.

**Files:**
- Create: `agent/pi.go`, `agent/pi_test.go`

**Interfaces:**
- Produces:
  ```go
  type PiOptions struct { Thinking string; Env map[string]string; CaptureSessions *bool; SessionStorage *SessionDirs }
  func Pi(model string, opts *PiOptions) Provider   // also SessionStorageProvider, InteractiveArgsBuilder
  ```

- [ ] **Step 1: Write the failing tests**

`agent/pi_test.go`:
```go
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/session"
)

func TestPiPrintCommand(t *testing.T) {
	pc := Pi("m", &PiOptions{Thinking: "high"}).BuildPrintCommand(CommandOptions{Prompt: "x", ResumeSession: "s"})
	if pc.Command != "pi -p --mode json --model 'm' --thinking high --session 's'" || *pc.Stdin != "x" {
		t.Fatalf("got %q", pc.Command)
	}
	if pc := Pi("m", nil).BuildPrintCommand(CommandOptions{Prompt: "x"}); pc.Command != "pi -p --mode json --model 'm'" {
		t.Fatalf("got %q", pc.Command)
	}
}

func TestPiParseStreamLine(t *testing.T) {
	p := Pi("m", nil)
	cases := []struct {
		line string
		want []StreamEvent
	}{
		{`{"type":"session","id":"s1"}`, []StreamEvent{{Type: EventSessionID, SessionID: "s1"}}},
		{`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hi"}}`, []StreamEvent{{Type: EventText, Text: "hi"}}},
		{`{"type":"message_update","assistantMessageEvent":{"type":"other"}}`, nil},
		{`{"type":"tool_execution_start","toolName":"Bash","args":{"command":"ls"}}`, []StreamEvent{{Type: EventToolCall, Name: "Bash", Args: "ls"}}},
		{`{"type":"tool_execution_start","toolName":"Read","args":{"path":"x"}}`, nil},
		{`{"type":"agent_error","error":"boom"}`, []StreamEvent{{Type: EventResult, Text: "boom"}}},
		{`{"type":"agent_end","messages":[{"role":"user","content":[{"type":"text","text":"q"}]},{"role":"assistant","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}]}`, []StreamEvent{{Type: EventResult, Text: "ab"}}},
	}
	for _, c := range cases {
		got := p.ParseStreamLine(c.line)
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %+v", c.line, got)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: %+v want %+v", c.line, got[i], c.want[i])
			}
		}
	}
}

func TestPiStorageRoundTrip(t *testing.T) {
	root := t.TempDir()
	hostSessions := t.TempDir()
	h := &dirHandle{root: root}
	st := Pi("m", &PiOptions{SessionStorage: &SessionDirs{Host: hostSessions, Sandbox: "/home/agent/.pi/agent/sessions"}}).(SessionStorageProvider).SessionStorage()
	hostCwd, sandboxCwd := "/Users/ellen/repo", "/home/agent/workspace"

	sandboxFile := h.mapPath("/home/agent/.pi/agent/sessions/" + session.EncodePiSessionDir(sandboxCwd) + "/2026-10-15_s9.jsonl")
	os.MkdirAll(filepath.Dir(sandboxFile), 0o755)
	os.WriteFile(sandboxFile, []byte(`{"type":"session","id":"s9","cwd":"/home/agent/workspace"}`), 0o644)

	tr := SessionTransfer{HostCwd: hostCwd, SandboxCwd: sandboxCwd, SessionID: "s9", Handle: h}
	if err := st.CaptureToHost(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	// Landed under the host cwd's encoded dir, not the sandbox's (pi resolves --session against the current project first).
	hostFile := filepath.Join(session.PiSessionDirPath(hostCwd, hostSessions), "2026-10-15_s9.jsonl")
	b, err := os.ReadFile(hostFile)
	if err != nil || !strings.Contains(string(b), `"cwd":"/Users/ellen/repo"`) {
		t.Fatalf("host: %q %v", b, err)
	}
	if st.HostSessionFilePath(hostCwd, "s9") != session.PiSessionDirPath(hostCwd, hostSessions) {
		t.Fatal("HostSessionFilePath must be the session directory")
	}

	os.Remove(sandboxFile)
	if err := st.ResumeIntoSandbox(context.Background(), tr); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(sandboxFile)
	if err != nil || !strings.Contains(string(b), `"cwd":"/home/agent/workspace"`) {
		t.Fatalf("sandbox: %q %v", b, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./agent/ -run Pi`
Expected: FAIL, "undefined: Pi".

- [ ] **Step 3: Write agent/pi.go**

```go
package agent

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/session"
)

const defaultPiSandboxSessionsDir = "/home/agent/.pi/agent/sessions"

// PiOptions configures the pi provider.
type PiOptions struct {
	// Thinking maps to --thinking: off, minimal, low, medium, high, xhigh.
	Thinking        string
	Env             map[string]string
	CaptureSessions *bool
	SessionStorage  *SessionDirs
}

type pi struct {
	model   string
	opts    PiOptions
	storage *piStorage
}

// Pi returns a provider for the `pi` CLI.
func Pi(model string, opts *PiOptions) Provider {
	p := &pi{model: model}
	if opts != nil {
		p.opts = *opts
	}
	p.storage = newPiStorage(p.opts.SessionStorage)
	return p
}

func (p *pi) Name() string                   { return "pi" }
func (p *pi) Env() map[string]string         { return p.opts.Env }
func (p *pi) CaptureSessions() bool          { return p.opts.CaptureSessions == nil || *p.opts.CaptureSessions }
func (p *pi) SessionStorage() SessionStorage { return p.storage }

func (p *pi) BuildPrintCommand(o CommandOptions) PrintCommand {
	cmd := "pi -p --mode json --model " + ShellEscape(p.model)
	if p.opts.Thinking != "" {
		cmd += " --thinking " + p.opts.Thinking
	}
	// --session appends to an existing session in place; pi has no fork verb.
	if o.ResumeSession != "" {
		cmd += " --session " + ShellEscape(o.ResumeSession)
	}
	prompt := o.Prompt
	return PrintCommand{Command: cmd, Stdin: &prompt}
}

func (p *pi) BuildInteractiveArgs(o CommandOptions) []string {
	args := []string{"pi", "--model", p.model}
	if o.Prompt != "" {
		args = append(args, o.Prompt)
	}
	return args
}

func (p *pi) ParseStreamLine(line string) []StreamEvent {
	obj, ok := parseJSONObject(line)
	if !ok {
		return nil
	}
	switch obj["type"] {
	case "session":
		if id, ok := obj["id"].(string); ok {
			return []StreamEvent{{Type: EventSessionID, SessionID: id}}
		}
	case "message_update":
		if evt, ok := obj["assistantMessageEvent"].(map[string]any); ok && evt["type"] == "text_delta" {
			if delta, ok := evt["delta"].(string); ok {
				return []StreamEvent{{Type: EventText, Text: delta}}
			}
		}
	case "tool_execution_start":
		name, _ := obj["toolName"].(string)
		field, ok := toolArgFields[name]
		if !ok {
			return nil
		}
		if args, ok := obj["args"].(map[string]any); ok {
			if v, ok := args[field].(string); ok {
				return []StreamEvent{{Type: EventToolCall, Name: name, Args: v}}
			}
		}
	case "agent_error", "error":
		if msg := extractErrorMessage(obj); msg != "" {
			return []StreamEvent{{Type: EventResult, Text: msg}}
		}
	case "agent_end":
		msgs, _ := obj["messages"].([]any)
		for i := len(msgs) - 1; i >= 0; i-- {
			m, _ := msgs[i].(map[string]any)
			if m["role"] != "assistant" {
				continue
			}
			var b strings.Builder
			blocks, _ := m["content"].([]any)
			for _, blk := range blocks {
				if bm, ok := blk.(map[string]any); ok && bm["type"] == "text" {
					if s, ok := bm["text"].(string); ok {
						b.WriteString(s)
					}
				}
			}
			if b.Len() > 0 {
				return []StreamEvent{{Type: EventResult, Text: b.String()}}
			}
			break
		}
	}
	return nil
}

type piStorage struct {
	hostDir    string
	sandboxDir string
}

func newPiStorage(dirs *SessionDirs) *piStorage {
	s := &piStorage{sandboxDir: defaultPiSandboxSessionsDir}
	if dirs != nil {
		s.hostDir = dirs.Host
		if dirs.Sandbox != "" {
			s.sandboxDir = dirs.Sandbox
		}
	}
	return s
}

func (s *piStorage) HostSessionFilePath(cwd, _ string) string {
	return session.PiSessionDirPath(cwd, s.hostDir)
}

func (s *piStorage) ExistsOnHost(_, id string) (bool, error) {
	return session.FindPiOnHost(id, s.hostDir).Path != "", nil
}

func (s *piStorage) ReadHostSession(_, id string) (string, bool, error) {
	found := session.FindPiOnHost(id, s.hostDir)
	if found.Path == "" {
		return "", false, nil
	}
	return readHostFile(found.Path)
}

func (s *piStorage) FindByIDOnHost(id string) (HostSessionLookup, error) {
	return session.FindPiOnHost(id, s.hostDir), nil
}

func locatePiSandbox(ctx context.Context, h sandbox.BindMountHandle, id, sessionsDir string) (string, error) {
	res, err := h.Exec(ctx, fmt.Sprintf("find %s -type f -name %s -print -quit", ShellEscape(sessionsDir), ShellEscape("*_"+id+".jsonl")), sandbox.ExecOptions{})
	if err != nil {
		return "", err
	}
	p := strings.SplitN(strings.TrimSpace(res.Stdout), "\n", 2)[0]
	if res.ExitCode != 0 || p == "" {
		return "", fmt.Errorf("session %s not found in %s", id, sessionsDir)
	}
	return p, nil
}

func (s *piStorage) CaptureToHost(ctx context.Context, t SessionTransfer) error {
	src, err := locatePiSandbox(ctx, t.Handle, t.SessionID, s.sandboxDir)
	if err != nil {
		return err
	}
	jsonl, err := readSandboxFile(ctx, t.Handle, src)
	if err != nil {
		return err
	}
	// Pi resolves --session against the current project's encoded dir first,
	// so the file lands under the host cwd's dir, not the sandbox's.
	target := filepath.Join(session.PiSessionDirPath(t.HostCwd, s.hostDir), path.Base(src))
	return writeHostFile(target, session.TransferPi(jsonl, t.SandboxCwd, t.HostCwd))
}

func (s *piStorage) ResumeIntoSandbox(ctx context.Context, t SessionTransfer) error {
	loc, err := session.LocatePiOnHost(t.SessionID, s.hostDir)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(loc.Path)
	if err != nil {
		return err
	}
	target := path.Join(s.sandboxDir, session.EncodePiSessionDir(t.SandboxCwd), filepath.Base(loc.Path))
	return writeSandboxFile(ctx, t.Handle, target, session.TransferPi(string(b), t.HostCwd, t.SandboxCwd))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./agent/ -run Pi`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add agent/pi.go agent/pi_test.go
git commit -m "Add pi provider with session storage"
```

---

### Task 5: Cursor, OpenCode and Copilot providers

Non-resumable providers (ADR 0016). Cursor and Copilot pass the prompt as an argv argument and guard its size at 120 KiB.

**Files:**
- Create: `agent/cursor.go`, `agent/opencode.go`, `agent/copilot.go`, `agent/others_test.go`

**Interfaces:**
- Produces:
  ```go
  type CursorOptions struct { Env map[string]string }
  func Cursor(model string, opts *CursorOptions) Provider
  type OpenCodeOptions struct { Variant, Agent string; Env map[string]string }
  func OpenCode(model string, opts *OpenCodeOptions) Provider
  type CopilotOptions struct { Effort string; Env map[string]string }
  func Copilot(model string, opts *CopilotOptions) Provider
  const ArgvPromptMaxBytes = 120 * 1024
  type PromptTooLargeError struct { Provider string; Bytes int }
  ```
- `BuildPrintCommand` cannot return an error in the `Provider` interface. The two argv providers therefore return `PrintCommand{Err: err}`: add the field `Err error` to `agent.PrintCommand`; the orchestrator's `invokeAgent` returns `printCmd.Err` when set (one `if` before `h.Exec`). Add that check in `internal/orchestrator/invoke.go` in this task.

- [ ] **Step 1: Write the failing tests**

`agent/others_test.go`:
```go
package agent

import (
	"errors"
	"strings"
	"testing"
)

func TestCursorCommands(t *testing.T) {
	pc := Cursor("m", nil).BuildPrintCommand(CommandOptions{Prompt: "do it", DangerouslySkipPermissions: true})
	if pc.Command != "agent --print --output-format stream-json --model 'm' --force 'do it'" || pc.Stdin != nil || pc.Err != nil {
		t.Fatalf("got %+v", pc)
	}
	big := strings.Repeat("x", ArgvPromptMaxBytes+1)
	var tooLarge *PromptTooLargeError
	if pc := Cursor("m", nil).BuildPrintCommand(CommandOptions{Prompt: big}); !errors.As(pc.Err, &tooLarge) || tooLarge.Provider != "cursor" {
		t.Fatalf("err = %v", pc.Err)
	}
	args := Cursor("m", nil).(InteractiveArgsBuilder).BuildInteractiveArgs(CommandOptions{Prompt: "p", DangerouslySkipPermissions: true})
	if strings.Join(args, " ") != "agent --model m --force p" {
		t.Fatalf("args = %v", args)
	}
	ev := Cursor("m", nil).ParseStreamLine(`{"type":"tool_call","subtype":"started","tool_call":{"readToolCall":{"args":{"path":"a.go"}}}}`)
	if len(ev) != 1 || ev[0] != (StreamEvent{Type: EventToolCall, Name: "Read", Args: "a.go"}) {
		t.Fatalf("read: %+v", ev)
	}
	ev = Cursor("m", nil).ParseStreamLine(`{"type":"tool_call","subtype":"started","tool_call":{"function":{"name":"shell","arguments":"{\"command\":\"ls\"}"}}}`)
	if len(ev) != 1 || ev[0] != (StreamEvent{Type: EventToolCall, Name: "Bash", Args: "ls"}) {
		t.Fatalf("function: %+v", ev)
	}
	ev = Cursor("m", nil).ParseStreamLine(`{"type":"result","result":"r"}`)
	if len(ev) != 1 || ev[0].Type != EventResult {
		t.Fatalf("fallback to stream-json: %+v", ev)
	}
}

func TestOpenCodeCommands(t *testing.T) {
	pc := OpenCode("m", &OpenCodeOptions{Variant: "high", Agent: "plan"}).BuildPrintCommand(CommandOptions{Prompt: "p", DangerouslySkipPermissions: true})
	if pc.Command != "opencode run --format json --model 'm' --variant 'high' --agent 'plan' --dangerously-skip-permissions 'p'" {
		t.Fatalf("got %q", pc.Command)
	}
	args := OpenCode("m", &OpenCodeOptions{Agent: "build"}).(InteractiveArgsBuilder).BuildInteractiveArgs(CommandOptions{Prompt: "p"})
	if strings.Join(args, " ") != "opencode --model m --agent build --prompt p" {
		t.Fatalf("args = %v", args)
	}
	p := OpenCode("m", nil)
	if ev := p.ParseStreamLine(`{"type":"step_start","sessionID":"s"}`); len(ev) != 1 || ev[0].SessionID != "s" {
		t.Fatalf("session: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"text","part":{"type":"text","text":"t"}}`); len(ev) != 2 || ev[1].Type != EventResult {
		t.Fatalf("text: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"tool_use","part":{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"ls"}}}}`); len(ev) != 1 || ev[0].Args != "ls" || ev[0].Name != "bash" {
		t.Fatalf("tool: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"tool_use","part":{"type":"tool","tool":"bash","state":{"status":"running","input":{"command":"ls"}}}}`); len(ev) != 0 {
		t.Fatalf("running must be skipped: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"tool_use","part":{"type":"tool","tool":"edit","state":{"status":"completed","input":{"file":"a"}}}}`); len(ev) != 1 || ev[0].Args != `{"file":"a"}` {
		t.Fatalf("json fallback: %+v", ev)
	}
}

func TestCopilotCommands(t *testing.T) {
	pc := Copilot("m", &CopilotOptions{Effort: "high"}).BuildPrintCommand(CommandOptions{Prompt: "p", DangerouslySkipPermissions: true})
	if pc.Command != "copilot -p 'p' --output-format json --model 'm' --allow-all-tools --effort high" {
		t.Fatalf("got %q", pc.Command)
	}
	args := Copilot("m", nil).(InteractiveArgsBuilder).BuildInteractiveArgs(CommandOptions{Prompt: "p"})
	if strings.Join(args, " ") != "copilot --model m -i p" {
		t.Fatalf("args = %v", args)
	}
	p := Copilot("m", nil)
	if ev := p.ParseStreamLine(`{"type":"assistant.message_delta","data":{"deltaContent":"d"}}`); len(ev) != 1 || ev[0].Text != "d" {
		t.Fatalf("delta: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"tool.execution_start","data":{"toolName":"bash","arguments":{"command":"ls"}}}`); len(ev) != 1 || ev[0].Name != "Bash" {
		t.Fatalf("tool: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"assistant.message","data":{"content":"final"}}`); len(ev) != 1 || ev[0].Type != EventResult {
		t.Fatalf("message: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"result","sessionId":"s"}`); len(ev) != 1 || ev[0].SessionID != "s" {
		t.Fatalf("result: %+v", ev)
	}
	for _, p := range []Provider{Cursor("m", nil), OpenCode("m", nil), Copilot("m", nil)} {
		if p.CaptureSessions() {
			t.Fatalf("%s must not capture sessions", p.Name())
		}
		if _, ok := p.(SessionStorageProvider); ok {
			t.Fatalf("%s must not have session storage", p.Name())
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./agent/ -run 'Cursor|OpenCode|Copilot'`
Expected: FAIL, "undefined: Cursor".

- [ ] **Step 3: Add `Err` to PrintCommand and the argv guard**

In `agent/agent.go`:
```go
// PrintCommand ... Err is set when the command could not be built (for
// example a prompt too large for argv); the orchestrator fails the iteration.
type PrintCommand struct {
	Command string
	Stdin   *string
	Err     error
}

// ArgvPromptMaxBytes bounds prompts passed as a command-line argument.
// Linux limits a single argument to about 128 KiB.
const ArgvPromptMaxBytes = 120 * 1024

// PromptTooLargeError reports a prompt that does not fit in argv.
type PromptTooLargeError struct {
	Provider string
	Bytes    int
}

func (e *PromptTooLargeError) Error() string {
	return fmt.Sprintf("%s print-mode prompt is %d bytes (max %d bytes). This provider passes the prompt as a command-line argument; shorten the prompt or split the work. Other providers use stdin for large prompts.", e.Provider, e.Bytes, ArgvPromptMaxBytes)
}

func argvPromptError(provider, prompt string) error {
	if len(prompt) > ArgvPromptMaxBytes {
		return &PromptTooLargeError{Provider: provider, Bytes: len(prompt)}
	}
	return nil
}
```

In `internal/orchestrator/invoke.go`, directly after `printCmd := o.Provider.BuildPrintCommand(...)`:
```go
	if printCmd.Err != nil {
		return invokeResult{}, printCmd.Err
	}
```

- [ ] **Step 4: Write cursor.go**

```go
package agent

import "encoding/json"

// CursorOptions configures the Cursor provider.
type CursorOptions struct{ Env map[string]string }

type cursor struct {
	model string
	opts  CursorOptions
}

// Cursor returns a provider for the Cursor `agent` CLI. Not resumable (ADR 0016).
func Cursor(model string, opts *CursorOptions) Provider {
	p := &cursor{model: model}
	if opts != nil {
		p.opts = *opts
	}
	return p
}

func (p *cursor) Name() string           { return "cursor" }
func (p *cursor) Env() map[string]string { return p.opts.Env }
func (p *cursor) CaptureSessions() bool  { return false }

func (p *cursor) BuildPrintCommand(o CommandOptions) PrintCommand {
	if err := argvPromptError("cursor", o.Prompt); err != nil {
		return PrintCommand{Err: err}
	}
	force := ""
	if o.DangerouslySkipPermissions {
		force = " --force"
	}
	return PrintCommand{Command: "agent --print --output-format stream-json --model " + ShellEscape(p.model) + force + " " + ShellEscape(o.Prompt)}
}

func (p *cursor) BuildInteractiveArgs(o CommandOptions) []string {
	args := []string{"agent", "--model", p.model}
	if o.DangerouslySkipPermissions {
		args = append(args, "--force")
	}
	if o.Prompt != "" {
		args = append(args, o.Prompt)
	}
	return args
}

// ParseStreamLine handles Cursor's top-level tool_call events and falls back
// to the Claude-compatible stream-json parser for everything else.
func (p *cursor) ParseStreamLine(line string) []StreamEvent {
	obj, ok := parseJSONObject(line)
	if !ok {
		return nil
	}
	if obj["type"] != "tool_call" {
		return ParseStreamJSONLine(line)
	}
	if obj["subtype"] != "started" {
		return nil
	}
	tc, _ := obj["tool_call"].(map[string]any)
	for name, key := range map[string]string{"Read": "readToolCall", "Write": "writeToolCall"} {
		if call, ok := tc[key].(map[string]any); ok {
			if args, ok := call["args"].(map[string]any); ok {
				if path, ok := args["path"].(string); ok {
					return []StreamEvent{{Type: EventToolCall, Name: name, Args: path}}
				}
			}
		}
	}
	fn, _ := tc["function"].(map[string]any)
	name, ok := fn["name"].(string)
	if !ok {
		return nil
	}
	raw, _ := fn["arguments"].(string)
	if raw == "" {
		return []StreamEvent{{Type: EventToolCall, Name: name, Args: ""}}
	}
	var parsed map[string]any
	if json.Unmarshal([]byte(raw), &parsed) == nil {
		if cmd, ok := parsed["command"].(string); ok {
			return []StreamEvent{{Type: EventToolCall, Name: "Bash", Args: cmd}}
		}
	}
	return []StreamEvent{{Type: EventToolCall, Name: name, Args: raw}}
}
```

Note: iterating a map for Read/Write makes the order undefined when both keys are present; upstream checks `readToolCall` first. Replace the map loop with two explicit `if` blocks (Read first, then Write) when writing the file.

- [ ] **Step 5: Write opencode.go**

```go
package agent

import "encoding/json"

// OpenCodeOptions configures the OpenCode provider.
type OpenCodeOptions struct {
	// Variant is the provider-specific reasoning effort (for example "high").
	Variant string
	// Agent selects a named OpenCode agent such as "build" or "plan".
	Agent string
	Env   map[string]string
}

var openCodeToolArgFields = map[string]string{"bash": "command", "webfetch": "url", "task": "description"}

type opencode struct {
	model string
	opts  OpenCodeOptions
}

// OpenCode returns a provider for the `opencode` CLI. Not resumable: its
// sessions live in SQLite (ADR 0016).
func OpenCode(model string, opts *OpenCodeOptions) Provider {
	p := &opencode{model: model}
	if opts != nil {
		p.opts = *opts
	}
	return p
}

func (p *opencode) Name() string           { return "opencode" }
func (p *opencode) Env() map[string]string { return p.opts.Env }
func (p *opencode) CaptureSessions() bool  { return false }

func (p *opencode) BuildPrintCommand(o CommandOptions) PrintCommand {
	cmd := "opencode run --format json --model " + ShellEscape(p.model)
	if p.opts.Variant != "" {
		cmd += " --variant " + ShellEscape(p.opts.Variant)
	}
	if p.opts.Agent != "" {
		cmd += " --agent " + ShellEscape(p.opts.Agent)
	}
	if o.DangerouslySkipPermissions {
		cmd += " --dangerously-skip-permissions"
	}
	return PrintCommand{Command: cmd + " " + ShellEscape(o.Prompt)}
}

func (p *opencode) BuildInteractiveArgs(o CommandOptions) []string {
	args := []string{"opencode", "--model", p.model}
	if p.opts.Agent != "" {
		args = append(args, "--agent", p.opts.Agent)
	}
	// --prompt pre-fills the TUI textbox; -p is the basic-auth password flag.
	if o.Prompt != "" {
		args = append(args, "--prompt", o.Prompt)
	}
	return args
}

func (p *opencode) ParseStreamLine(line string) []StreamEvent {
	obj, ok := parseJSONObject(line)
	if !ok {
		return nil
	}
	part, _ := obj["part"].(map[string]any)
	switch obj["type"] {
	case "step_start":
		if id, ok := obj["sessionID"].(string); ok {
			return []StreamEvent{{Type: EventSessionID, SessionID: id}}
		}
	case "text":
		if text, ok := part["text"].(string); ok && part["type"] == "text" {
			return []StreamEvent{{Type: EventText, Text: text}, {Type: EventResult, Text: text}}
		}
	case "tool_use":
		tool, ok := part["tool"].(string)
		if !ok || part["type"] != "tool" {
			return nil
		}
		state, _ := part["state"].(map[string]any)
		if state["status"] != "completed" {
			return nil
		}
		input, ok := state["input"].(map[string]any)
		if !ok {
			return nil
		}
		if v, ok := input[openCodeToolArgFields[tool]].(string); ok {
			return []StreamEvent{{Type: EventToolCall, Name: tool, Args: v}}
		}
		b, _ := json.Marshal(input)
		return []StreamEvent{{Type: EventToolCall, Name: tool, Args: string(b)}}
	case "error":
		if msg := extractErrorMessage(obj); msg != "" {
			return []StreamEvent{{Type: EventResult, Text: msg}}
		}
	}
	return nil
}
```

- [ ] **Step 6: Write copilot.go**

```go
package agent

// CopilotOptions configures the GitHub Copilot CLI provider.
type CopilotOptions struct {
	Effort string // low, medium, high
	Env    map[string]string
}

type copilot struct {
	model string
	opts  CopilotOptions
}

// Copilot returns a provider for the `copilot` CLI. Not resumable: its
// session state is indexed by SQLite (ADR 0016).
func Copilot(model string, opts *CopilotOptions) Provider {
	p := &copilot{model: model}
	if opts != nil {
		p.opts = *opts
	}
	return p
}

func (p *copilot) Name() string           { return "copilot" }
func (p *copilot) Env() map[string]string { return p.opts.Env }
func (p *copilot) CaptureSessions() bool  { return false }

func (p *copilot) BuildPrintCommand(o CommandOptions) PrintCommand {
	if err := argvPromptError("copilot", o.Prompt); err != nil {
		return PrintCommand{Err: err}
	}
	cmd := "copilot -p " + ShellEscape(o.Prompt) + " --output-format json --model " + ShellEscape(p.model)
	if o.DangerouslySkipPermissions {
		cmd += " --allow-all-tools"
	}
	if p.opts.Effort != "" {
		cmd += " --effort " + p.opts.Effort
	}
	return PrintCommand{Command: cmd}
}

func (p *copilot) BuildInteractiveArgs(o CommandOptions) []string {
	args := []string{"copilot", "--model", p.model}
	// -i starts an interactive session seeded with the prompt; -p would print and exit.
	if o.Prompt != "" {
		args = append(args, "-i", o.Prompt)
	}
	return args
}

func (p *copilot) ParseStreamLine(line string) []StreamEvent {
	obj, ok := parseJSONObject(line)
	if !ok {
		return nil
	}
	data, _ := obj["data"].(map[string]any)
	switch obj["type"] {
	case "assistant.message_delta":
		if d, ok := data["deltaContent"].(string); ok {
			return []StreamEvent{{Type: EventText, Text: d}}
		}
	case "tool.execution_start":
		name, _ := data["toolName"].(string)
		if name == "bash" {
			name = "Bash"
		}
		field, ok := toolArgFields[name]
		if !ok {
			return nil
		}
		if args, ok := data["arguments"].(map[string]any); ok {
			if v, ok := args[field].(string); ok {
				return []StreamEvent{{Type: EventToolCall, Name: name, Args: v}}
			}
		}
	case "assistant.message":
		if c, ok := data["content"].(string); ok && c != "" {
			return []StreamEvent{{Type: EventResult, Text: c}}
		}
	case "result":
		if id, ok := obj["sessionId"].(string); ok {
			return []StreamEvent{{Type: EventSessionID, SessionID: id}}
		}
	case "error", "agent_error":
		if msg := extractErrorMessage(obj); msg != "" {
			return []StreamEvent{{Type: EventResult, Text: msg}}
		}
	}
	return nil
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test -race ./agent/ ./internal/orchestrator/`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add agent/ internal/orchestrator/invoke.go
git commit -m "Add Cursor, OpenCode and Copilot providers"
```

---

### Task 6: Session resume and fork in the orchestrator and `Run`

Port of the resume paths in `Orchestrator.ts` (resume into the sandbox before iteration 1, capture after each iteration, session usage parsing), `resumePrecheck.ts`, and `RunResult.resume()` / `.fork()` from `run.ts`.

**Files:**
- Create: `internal/orchestrator/resume.go`, `internal/orchestrator/resume_test.go`
- Modify: `internal/orchestrator/orchestrator.go`, `internal/orchestrator/orchestrator_test.go`, `agentgopher.go`, `agentgopher_test.go`

**Interfaces:**
- Produces, in `orchestrator`:
  ```go
  // Options gains:
  ResumeSession string
  ForkSession   bool
  // IterationResult gains:
  SessionFilePath string
  func AssertResumeSessionExists(provider agent.Provider, kind sandbox.Kind, hostRepoDir, sessionID string) error
  ```
- Produces, in the root package:
  ```go
  // RunOptions gains:
  ResumeSession string   // resume this session on iteration 1; requires MaxIterations <= 1
  forkSession   bool     // unexported, set by Fork
  // RunResult gains:
  func (r RunResult) Resume(ctx context.Context, prompt string, o *ResumeOptions) (RunResult, error)
  func (r RunResult) Fork(ctx context.Context, prompt string, o *ResumeOptions) (RunResult, error)
  type ResumeOptions struct {
      PromptArgs map[string]string; Logging *Logging; CompletionSignal []string
      IdleTimeout, CompletionTimeout time.Duration; Name string; Timeouts Timeouts
      Hooks *Hooks; CopyToWorktree []string; BranchStrategy *sandbox.BranchStrategy
  }
  ```
  `RunResult` keeps an unexported copy of the `RunOptions` it came from (`opts RunOptions`) so `Resume` can re-run.
- Capture and resume only happen when the per-iteration handle implements `sandbox.BindMountHandle` and the provider implements `agent.SessionStorageProvider` and `CaptureSessions()` is true. With the no-sandbox provider the agent writes its session on the host already; nothing is transferred and `SessionFilePath` stays empty.

- [ ] **Step 1: Write the failing precheck tests**

`internal/orchestrator/resume_test.go`:
```go
package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox"
)

// storageProvider wraps fakeProvider with a scripted SessionStorage.
type storageProvider struct {
	fakeProvider
	st *fakeStorage
}

func (p storageProvider) SessionStorage() agent.SessionStorage { return p.st }
func (p storageProvider) CaptureSessions() bool               { return true }

type fakeStorage struct {
	hostFiles map[string]string // id -> path
	captured  []string
	resumed   []string
	hostDir   string
}

func (s *fakeStorage) CaptureToHost(_ context.Context, t agent.SessionTransfer) error {
	s.captured = append(s.captured, t.SessionID)
	p := filepath.Join(s.hostDir, t.SessionID+".jsonl")
	os.WriteFile(p, []byte(`{"type":"assistant","message":{"usage":{"input_tokens":5,"cache_creation_input_tokens":1,"cache_read_input_tokens":2,"output_tokens":3}}}`), 0o644)
	s.hostFiles[t.SessionID] = p
	return nil
}
func (s *fakeStorage) ResumeIntoSandbox(_ context.Context, t agent.SessionTransfer) error {
	s.resumed = append(s.resumed, t.SessionID)
	return nil
}
func (s *fakeStorage) ReadHostSession(_, id string) (string, bool, error) {
	p, ok := s.hostFiles[id]
	if !ok {
		return "", false, nil
	}
	b, err := os.ReadFile(p)
	return string(b), true, err
}
func (s *fakeStorage) ExistsOnHost(_, id string) (bool, error) { _, ok := s.hostFiles[id]; return ok, nil }
func (s *fakeStorage) HostSessionFilePath(_, id string) string  { return s.hostFiles[id] }
func (s *fakeStorage) FindByIDOnHost(id string) (agent.HostSessionLookup, error) {
	return agent.HostSessionLookup{Path: s.hostFiles[id], SearchedRoot: s.hostDir}, nil
}

func newStorageProvider(t *testing.T) storageProvider {
	t.Helper()
	return storageProvider{st: &fakeStorage{hostFiles: map[string]string{}, hostDir: t.TempDir()}}
}

func TestAssertResumeSessionExists(t *testing.T) {
	p := newStorageProvider(t)
	if err := AssertResumeSessionExists(fakeProvider{}, sandbox.KindBindMount, "/r", "x"); err == nil || !strings.Contains(err.Error(), "does not support ResumeSession") {
		t.Fatalf("no storage: %v", err)
	}
	if err := AssertResumeSessionExists(p, sandbox.KindBindMount, "/r", "x"); err == nil || !strings.Contains(err.Error(), `"x" not found`) {
		t.Fatalf("bind-mount missing: %v", err)
	}
	if err := AssertResumeSessionExists(p, sandbox.KindNone, "/r", "x"); err == nil || !strings.Contains(err.Error(), p.st.hostDir) {
		t.Fatalf("none missing: %v", err)
	}
	p.st.hostFiles["x"] = filepath.Join(p.st.hostDir, "x.jsonl")
	if err := AssertResumeSessionExists(p, sandbox.KindBindMount, "/r", "x"); err != nil {
		t.Fatal(err)
	}
	if err := AssertResumeSessionExists(p, sandbox.KindNone, "/r", "x"); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Write the failing orchestrator tests (append to orchestrator_test.go)**

The fake handle from phase 1 is a plain `sandbox.Handle`. Add a bind-mount variant to `fakes_test.go`:
```go
// fakeBindMountHandle is fakeHandle plus the copy methods, so the orchestrator
// treats it as a bind-mount sandbox and runs session capture.
type fakeBindMountHandle struct{ *fakeHandle }

func (fakeBindMountHandle) CopyFileIn(context.Context, string, string) error  { return nil }
func (fakeBindMountHandle) CopyFileOut(context.Context, string, string) error { return nil }
```
and let `fakeNoSandboxProvider` accept it: change `handles []*fakeHandle` to `handles []sandbox.Handle` and update `baseOptions` callers to wrap: `baseOptions(t, h1)` keeps working if `baseOptions` takes `...sandbox.Handle` and the tests pass `*fakeHandle` values (they satisfy the interface).

Append:
```go
func TestOrchestrateCapturesSessionAndParsesUsage(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{0, "SESSION s-1"}, {0, "RESULT " + DefaultCompletionSignal}}}
	o, _ := baseOptions(t, fakeBindMountHandle{h})
	p := newStorageProvider(t)
	o.Provider = p
	res, err := Orchestrate(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	it := res.Iterations[0]
	if it.SessionID != "s-1" || it.SessionFilePath != p.st.hostFiles["s-1"] || len(p.st.captured) != 1 {
		t.Fatalf("iteration = %+v captured=%v", it, p.st.captured)
	}
	if it.Usage == nil || it.Usage.InputTokens != 5 {
		t.Fatalf("usage = %+v", it.Usage)
	}
}

func TestOrchestrateResumesIntoSandboxOnIterationOne(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{0, "RESULT " + DefaultCompletionSignal}}}
	o, _ := baseOptions(t, fakeBindMountHandle{h})
	p := newStorageProvider(t)
	o.Provider = p
	o.ResumeSession = "old"
	if _, err := Orchestrate(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if len(p.st.resumed) != 1 || p.st.resumed[0] != "old" || !strings.Contains(h.commands[len(h.commands)-1], "--resume old") {
		t.Fatalf("resumed=%v cmd=%v", p.st.resumed, h.commands)
	}
}

func TestOrchestrateNoCaptureWithoutBindMount(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{0, "SESSION s-1"}, {0, "RESULT " + DefaultCompletionSignal}}}
	o, _ := baseOptions(t, h)
	p := newStorageProvider(t)
	o.Provider = p
	res, err := Orchestrate(context.Background(), o)
	if err != nil || len(p.st.captured) != 0 || res.Iterations[0].SessionID != "s-1" {
		t.Fatalf("res=%+v captured=%v err=%v", res, p.st.captured, err)
	}
}
```
The storage provider's `ParseSessionUsage` comes from the Claude provider upstream; for the test, make `storageProvider` also implement `agent.SessionUsageParser` by delegating to `agent.ClaudeCode("m", nil).(agent.SessionUsageParser)`.

- [ ] **Step 3: Write the failing root tests (append to agentgopher_test.go)**

```go
func TestRunResumeAndForkRequireCapturedSession(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	res, err := Run(context.Background(), RunOptions{Agent: echoAgent{script: `echo "RESULT <promise>COMPLETE</promise>"`}, Sandbox: nosandbox.New(nil), Cwd: repo, Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := res.Resume(context.Background(), "again", nil); err == nil || !strings.Contains(err.Error(), "no session") {
		t.Fatalf("resume: %v", err)
	}
	if _, err := res.Fork(context.Background(), "again", nil); err == nil || !strings.Contains(err.Error(), "no session") {
		t.Fatalf("fork: %v", err)
	}
}

func TestRunResumeValidation(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	base := RunOptions{Agent: echoAgent{script: "true"}, Sandbox: nosandbox.New(nil), Cwd: repo, Prompt: "x"}
	o := base
	o.ResumeSession = "s"
	o.MaxIterations = 2
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "MaxIterations > 1") {
		t.Fatalf("got %v", err)
	}
	o = base
	o.ResumeSession = "s"
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "does not support ResumeSession") {
		t.Fatalf("got %v", err)
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./internal/orchestrator/ ./`
Expected: FAIL, "undefined: AssertResumeSessionExists" and unknown field `ResumeSession`.

- [ ] **Step 5: Write internal/orchestrator/resume.go**

```go
package orchestrator

import (
	"fmt"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox"
)

// AssertResumeSessionExists fails fast when the session to resume is not on
// the host. With no sandbox the agent wrote the session in place under a
// cwd-derived directory we cannot reliably reconstruct, so it is located by
// id; with a sandbox, capture put it at the host-cwd-derived path.
func AssertResumeSessionExists(provider agent.Provider, kind sandbox.Kind, hostRepoDir, sessionID string) error {
	sp, ok := provider.(agent.SessionStorageProvider)
	if !ok || sp.SessionStorage() == nil {
		return fmt.Errorf("%s does not support ResumeSession", provider.Name())
	}
	st := sp.SessionStorage()
	if kind == sandbox.KindNone {
		found, err := st.FindByIDOnHost(sessionID)
		if err != nil {
			return err
		}
		if found.Path == "" {
			return fmt.Errorf("ResumeSession %q not found under %s", sessionID, found.SearchedRoot)
		}
		return nil
	}
	exists, err := st.ExistsOnHost(hostRepoDir, sessionID)
	if err != nil {
		return err
	}
	if !exists {
		if p := st.HostSessionFilePath(hostRepoDir, sessionID); p != "" {
			return fmt.Errorf("ResumeSession %q not found: expected session file at %s", sessionID, p)
		}
		return fmt.Errorf("ResumeSession %q not found", sessionID)
	}
	return nil
}
```

- [ ] **Step 6: Wire resume and capture into orchestrator.go**

Add `ResumeSession string` and `ForkSession bool` to `Options`, `SessionFilePath string` to `IterationResult`, and `sessionFilePath string` to `iterationOutcome`. In `runIteration`, before prompt expansion:
```go
	bm, _ := lctx.Sandbox.(sandbox.BindMountHandle)
	storage := sessionStorageOf(o.Provider)
	resume, fork := "", false
	if iteration == 1 {
		resume, fork = o.ResumeSession, o.ForkSession
	}
	if resume != "" && bm != nil && storage != nil {
		o.Display.Status(label("Resuming session"), display.SeverityInfo)
		if err := storage.ResumeIntoSandbox(ctx, agent.SessionTransfer{HostCwd: o.HostRepoDir, SandboxCwd: lctx.SandboxRepoDir, SessionID: resume, Handle: bm}); err != nil {
			return iterationOutcome{}, fmt.Errorf("session resume failed: %w", err)
		}
	}
```
Pass `ResumeSession: resume, ForkSession: fork` into `invokeOptions`. After "Agent stopped":
```go
	out := iterationOutcome{stdout: res.Output, sessionID: res.SessionID, usage: res.Usage}
	if o.Provider.CaptureSessions() && storage != nil && res.SessionID != "" && bm != nil {
		o.Display.Status(label("Capturing session"), display.SeverityInfo)
		if err := storage.CaptureToHost(ctx, agent.SessionTransfer{HostCwd: o.HostRepoDir, SandboxCwd: lctx.SandboxRepoDir, SessionID: res.SessionID, Handle: bm}); err != nil {
			return iterationOutcome{}, fmt.Errorf("session capture failed: %w", err)
		}
		out.sessionFilePath = storage.HostSessionFilePath(o.HostRepoDir, res.SessionID)
		if parser, ok := o.Provider.(agent.SessionUsageParser); ok {
			if content, found, _ := storage.ReadHostSession(o.HostRepoDir, res.SessionID); found {
				if u, ok := parser.ParseSessionUsage(content); ok {
					out.usage = &u
				}
			}
		}
	}
```
and the helper:
```go
func sessionStorageOf(p agent.Provider) agent.SessionStorage {
	if sp, ok := p.(agent.SessionStorageProvider); ok {
		return sp.SessionStorage()
	}
	return nil
}
```
Copy `sessionFilePath` into `IterationResult` in `Orchestrate`.

- [ ] **Step 7: Wire Resume and Fork into agentgopher.go**

Add to `RunOptions`: `ResumeSession string` and `forkSession bool`. Add `ResumeOptions` as in the Interfaces block. In `Run`, after the branch-strategy validation:
```go
	if o.ResumeSession != "" && maxIterations > 1 {
		return RunResult{}, errors.New("ResumeSession cannot be combined with MaxIterations > 1. Resume applies to iteration 1 only (ADR 0011)")
	}
	if o.forkSession && o.ResumeSession == "" {
		return RunResult{}, errors.New("forkSession requires ResumeSession; use RunResult.Fork")
	}
```
After `hostRepoDir` is resolved:
```go
	if o.ResumeSession != "" {
		if err := orchestrator.AssertResumeSessionExists(o.Agent, o.Sandbox.Kind(), hostRepoDir, o.ResumeSession); err != nil {
			return RunResult{}, err
		}
	}
```
Pass `ResumeSession: o.ResumeSession, ForkSession: o.forkSession` to `orchestrator.Options`. Store `opts: o` in the returned `RunResult` (add the unexported field `opts RunOptions`). Then:
```go
// ResumeOptions override run options for Resume and Fork. Nil pointers and
// zero values keep the original run's values.
type ResumeOptions struct {
	PromptArgs        map[string]string
	Logging           *Logging
	CompletionSignal  []string
	IdleTimeout       time.Duration
	CompletionTimeout time.Duration
	Name              string
	Timeouts          Timeouts
	Hooks             *Hooks
	CopyToWorktree    []string
	BranchStrategy    *sandbox.BranchStrategy
}

func (r RunResult) continueRun(ctx context.Context, prompt string, o *ResumeOptions, fork bool) (RunResult, error) {
	if len(r.Iterations) == 0 || r.Iterations[len(r.Iterations)-1].SessionID == "" {
		return RunResult{}, errors.New("cannot resume: no session id was captured")
	}
	if _, ok := r.opts.Agent.(agent.SessionStorageProvider); !ok {
		return RunResult{}, fmt.Errorf("cannot resume: %s does not support session storage", r.opts.Agent.Name())
	}
	next := r.opts
	next.Prompt, next.PromptFile, next.PromptArgs = prompt, "", nil
	next.MaxIterations = 1
	next.ResumeSession = r.Iterations[len(r.Iterations)-1].SessionID
	next.forkSession = fork
	next.Output = nil
	if o != nil {
		if o.PromptArgs != nil {
			next.PromptArgs = o.PromptArgs
		}
		if o.Logging != nil {
			next.Logging = o.Logging
		}
		if o.CompletionSignal != nil {
			next.CompletionSignal = o.CompletionSignal
		}
		if o.IdleTimeout != 0 {
			next.IdleTimeout = o.IdleTimeout
		}
		if o.CompletionTimeout != 0 {
			next.CompletionTimeout = o.CompletionTimeout
		}
		if o.Name != "" {
			next.Name = o.Name
		}
		if o.Timeouts != (Timeouts{}) {
			next.Timeouts = o.Timeouts
		}
		if o.Hooks != nil {
			next.Hooks = *o.Hooks
		}
		if o.CopyToWorktree != nil {
			next.CopyToWorktree = o.CopyToWorktree
		}
		if o.BranchStrategy != nil {
			next.BranchStrategy = o.BranchStrategy
		}
	}
	return Run(ctx, next)
}

// Resume continues the last captured session for exactly one iteration (ADR 0011).
func (r RunResult) Resume(ctx context.Context, prompt string, o *ResumeOptions) (RunResult, error) {
	return r.continueRun(ctx, prompt, o, false)
}

// Fork continues the last captured session as a new session, leaving the
// parent record intact. It isolates the session only, not the branch or
// sandbox (ADR 0018): concurrent forks need distinct named branches.
func (r RunResult) Fork(ctx context.Context, prompt string, o *ResumeOptions) (RunResult, error) {
	return r.continueRun(ctx, prompt, o, true)
}
```
`next.Output = nil` refers to the field added in Task 7; when implementing this task before Task 7, omit that line and add it in Task 7.

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/orchestrator/ agentgopher.go agentgopher_test.go
git commit -m "Add session resume, fork and capture to the run loop"
```

---

### Task 7: Structured output with retries

Port of `Output.ts`, `extractStructuredOutput.ts` and the output handling in `run.ts`. Standard Schema becomes `encoding/json` into `T` plus an optional `validate func(T) error`.

**Files:**
- Create: `output.go`, `output_test.go`
- Modify: `agentgopher.go`, `agentgopher_test.go`

**Interfaces:**
- Produces:
  ```go
  type OutputDefinition interface { Tag() string; MaxRetries() int; extract(raw string) (any, error) }
  type OutputOption func(*outputConfig)
  func WithMaxRetries(n int) OutputOption
  func OutputObject[T any](tag string, validate func(T) error, opts ...OutputOption) OutputDefinition
  func OutputString(tag string, opts ...OutputOption) OutputDefinition
  type StructuredOutputError struct {
      Tag, Message, RawMatched string   // RawMatched "" and Found false when the tag was absent
      Found bool
      Cause error
      Commits []Commit; Branch, PreservedWorktreePath, SessionID, SessionFilePath string
  }
  // RunOptions gains: Output OutputDefinition
  // RunResult gains:  Output any   (T for OutputObject, string for OutputString, nil otherwise)
  func BuildStructuredOutputRetryFeedback(err *StructuredOutputError, retriesRemaining int) string
  ```

- [ ] **Step 1: Write the failing tests**

`output_test.go`:
```go
package agentgopher

import (
	"errors"
	"strings"
	"testing"
)

type answer struct {
	Answer int `json:"answer"`
}

func TestOutputObjectLastMatchWinsAndUnwrapsFence(t *testing.T) {
	def := OutputObject[answer]("result", nil)
	stdout := "draft <result>{\"answer\":1}</result> fixed <result>\n```json\n{\"answer\": 42}\n```\n</result>"
	got, err := extractStructuredOutput(stdout, def, extractionContext{})
	if err != nil || got.(answer).Answer != 42 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestOutputObjectErrors(t *testing.T) {
	def := OutputObject[answer]("result", func(a answer) error {
		if a.Answer < 0 {
			return errors.New("must be positive")
		}
		return nil
	})
	var soe *StructuredOutputError

	_, err := extractStructuredOutput("no tag here", def, extractionContext{Branch: "b", Commits: []Commit{{SHA: "x"}}})
	if !errors.As(err, &soe) || soe.Found || soe.Branch != "b" || len(soe.Commits) != 1 || !strings.Contains(soe.Message, "not found") {
		t.Fatalf("missing: %+v", err)
	}
	_, err = extractStructuredOutput("<result>{nope</result>", def, extractionContext{})
	if !errors.As(err, &soe) || !soe.Found || soe.RawMatched != "{nope" || soe.Cause == nil || !strings.Contains(soe.Message, "invalid JSON") {
		t.Fatalf("json: %+v", err)
	}
	_, err = extractStructuredOutput(`<result>{"answer":-1}</result>`, def, extractionContext{SessionID: "s"})
	if !errors.As(err, &soe) || soe.SessionID != "s" || !strings.Contains(soe.Message, "failed validation") || soe.Cause.Error() != "must be positive" {
		t.Fatalf("validate: %+v", err)
	}
}

func TestOutputStringTrims(t *testing.T) {
	got, err := extractStructuredOutput("<summary>\n  hello \n</summary>", OutputString("summary"), extractionContext{})
	if err != nil || got != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
	if OutputString("s", WithMaxRetries(2)).MaxRetries() != 2 || OutputString("s").MaxRetries() != 0 {
		t.Fatal("MaxRetries")
	}
}

func TestRetryFeedbackMentionsEverything(t *testing.T) {
	msg := BuildStructuredOutputRetryFeedback(&StructuredOutputError{Tag: "result", Message: "bad", RawMatched: "{x", Found: true, Cause: errors.New("unexpected x")}, 1)
	for _, want := range []string{"Retries remaining after this attempt: 1", "bad", "unexpected x", "{x", "<result>"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missing %q in %q", want, msg)
		}
	}
	msg = BuildStructuredOutputRetryFeedback(&StructuredOutputError{Tag: "r"}, 0)
	if !strings.Contains(msg, "(no matching tag was emitted)") || !strings.Contains(msg, "(no parser detail)") {
		t.Fatalf("got %q", msg)
	}
}
```

Append to `agentgopher_test.go`:
```go
func TestRunWithStructuredOutput(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	res, err := Run(context.Background(), RunOptions{
		Agent:   echoAgent{script: `echo 'RESULT <out>{"answer":7}</out>'`},
		Sandbox: nosandbox.New(nil),
		Cwd:     repo,
		Prompt:  "Reply inside <out></out>",
		Output:  OutputObject[answer]("out", nil),
	})
	if err != nil || res.Output.(answer).Answer != 7 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRunStructuredOutputValidation(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	base := RunOptions{Agent: echoAgent{script: "true"}, Sandbox: nosandbox.New(nil), Cwd: repo, Prompt: "x <out>", Output: OutputString("out")}
	o := base
	o.MaxIterations = 2
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "MaxIterations to be 1") {
		t.Fatalf("iterations: %v", err)
	}
	o = base
	o.Prompt = "no tag"
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "<out> not found in the resolved prompt") {
		t.Fatalf("tag: %v", err)
	}
	o = base
	o.Output = OutputString("out", WithMaxRetries(1))
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "requires an agent provider that supports session resumption") {
		t.Fatalf("retries: %v", err)
	}
	o = base
	o.Agent = echoAgent{script: `echo "RESULT nothing"`}
	var soe *StructuredOutputError
	if _, err := Run(context.Background(), o); !errors.As(err, &soe) || soe.Branch != "main" {
		t.Fatalf("missing tag: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./ -run 'Output|StructuredOutput|RetryFeedback'`
Expected: FAIL, "undefined: OutputObject".

- [ ] **Step 3: Write output.go**

```go
package agentgopher

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// OutputDefinition declares structured output extracted from an XML tag in
// the agent's stdout after the run (ADR 0010).
type OutputDefinition interface {
	Tag() string
	MaxRetries() int
	extract(raw string) (any, error)
}

type outputConfig struct {
	tag        string
	maxRetries int
}

func (c outputConfig) Tag() string     { return c.tag }
func (c outputConfig) MaxRetries() int { return c.maxRetries }

// OutputOption configures an OutputDefinition.
type OutputOption func(*outputConfig)

// WithMaxRetries lets Run resume the failed session up to n times with a
// feedback prompt when extraction or validation fails. Requires a provider
// with session storage.
func WithMaxRetries(n int) OutputOption { return func(c *outputConfig) { c.maxRetries = n } }

type outputObject[T any] struct {
	outputConfig
	validate func(T) error
}

// OutputObject declares JSON output decoded into T. validate, when set, runs
// after decoding and its error becomes the StructuredOutputError cause.
func OutputObject[T any](tag string, validate func(T) error, opts ...OutputOption) OutputDefinition {
	c := outputConfig{tag: tag}
	for _, o := range opts {
		o(&c)
	}
	return outputObject[T]{outputConfig: c, validate: validate}
}

type parseError struct{ cause error }

func (e parseError) Error() string { return e.cause.Error() }

type validationError struct{ cause error }

func (e validationError) Error() string { return e.cause.Error() }

func (d outputObject[T]) extract(raw string) (any, error) {
	var v T
	if err := json.Unmarshal([]byte(unwrapFences(strings.TrimSpace(raw))), &v); err != nil {
		return nil, parseError{err}
	}
	if d.validate != nil {
		if err := d.validate(v); err != nil {
			return nil, validationError{err}
		}
	}
	return v, nil
}

type outputString struct{ outputConfig }

// OutputString declares output returned as a trimmed string, without parsing.
func OutputString(tag string, opts ...OutputOption) OutputDefinition {
	c := outputConfig{tag: tag}
	for _, o := range opts {
		o(&c)
	}
	return outputString{c}
}

func (outputString) extract(raw string) (any, error) { return strings.TrimSpace(raw), nil }

// StructuredOutputError reports a missing tag, invalid JSON or failed
// validation. It carries the run's side effects so the caller can recover.
type StructuredOutputError struct {
	Tag                  string
	Message              string
	RawMatched           string
	Found                bool
	Cause                error
	Commits              []Commit
	Branch               string
	PreservedWorktreePath string
	SessionID            string
	SessionFilePath      string
}

func (e *StructuredOutputError) Error() string { return e.Message }
func (e *StructuredOutputError) Unwrap() error { return e.Cause }

type extractionContext struct {
	Commits               []Commit
	Branch                string
	PreservedWorktreePath string
	SessionID             string
	SessionFilePath       string
}

// extractStructuredOutput finds the last <tag>...</tag> in stdout and decodes it.
func extractStructuredOutput(stdout string, def OutputDefinition, ctx extractionContext) (any, error) {
	fail := func(msg, raw string, found bool, cause error) error {
		return &StructuredOutputError{Tag: def.Tag(), Message: msg, RawMatched: raw, Found: found, Cause: cause,
			Commits: ctx.Commits, Branch: ctx.Branch, PreservedWorktreePath: ctx.PreservedWorktreePath, SessionID: ctx.SessionID, SessionFilePath: ctx.SessionFilePath}
	}
	raw, found := findLastTagContent(stdout, def.Tag())
	if !found {
		return nil, fail(fmt.Sprintf("structured output tag <%s> not found in agent output", def.Tag()), "", false, nil)
	}
	v, err := def.extract(raw)
	switch e := err.(type) {
	case nil:
		return v, nil
	case parseError:
		return nil, fail(fmt.Sprintf("structured output tag <%s> contains invalid JSON", def.Tag()), raw, true, e.cause)
	case validationError:
		return nil, fail(fmt.Sprintf("structured output tag <%s> failed validation", def.Tag()), raw, true, e.cause)
	default:
		return nil, fail(err.Error(), raw, true, err)
	}
}

// findLastTagContent returns the content of the last complete <tag>...</tag> pair.
func findLastTagContent(text, tag string) (string, bool) {
	open, closeTag := "<"+tag+">", "</"+tag+">"
	var last string
	found := false
	from := 0
	for {
		i := strings.Index(text[from:], open)
		if i < 0 {
			break
		}
		start := from + i + len(open)
		j := strings.Index(text[start:], closeTag)
		if j < 0 {
			break
		}
		last, found = text[start:start+j], true
		from = start + j + len(closeTag)
	}
	return last, found
}

var fencePattern = regexp.MustCompile("(?s)^```(?:json)?[ \t]*\n(.*?)\n[ \t]*```[ \t]*$")

// unwrapFences strips one optional ```json fence around the payload.
func unwrapFences(text string) string {
	if m := fencePattern.FindStringSubmatch(text); m != nil {
		return strings.TrimSpace(m[1])
	}
	return text
}

// BuildStructuredOutputRetryFeedback is the prompt sent on a retry: the agent
// already did the work in the resumed session, it only re-emits the tag.
func BuildStructuredOutputRetryFeedback(e *StructuredOutputError, retriesRemaining int) string {
	raw := "(no matching tag was emitted)"
	if e.Found {
		raw = e.RawMatched
	}
	cause := "(no parser detail)"
	if e.Cause != nil {
		cause = e.Cause.Error()
	}
	return fmt.Sprintf(`Your previous response did not produce valid structured output.

Retries remaining after this attempt: %d.

Problem:
%s

Parser detail:
%s

Previous matched output:
%s

Emit only a corrected <%s> block. Do not change files or run commands.`, retriesRemaining, e.Message, cause, raw, e.Tag)
}
```

- [ ] **Step 4: Wire Output into Run**

Add `Output OutputDefinition` to `RunOptions` and `Output any` to `RunResult`. Validation after the resume checks in `Run`:
```go
	outputRetries := 0
	if o.Output != nil {
		if maxIterations != 1 {
			return RunResult{}, errors.New("Output requires MaxIterations to be 1. Structured output is only supported for single-iteration runs")
		}
		outputRetries = o.Output.MaxRetries()
		if outputRetries < 0 {
			return RunResult{}, fmt.Errorf("Output max retries must be non-negative, got %d", outputRetries)
		}
		if _, ok := o.Agent.(agent.SessionStorageProvider); outputRetries > 0 && !ok {
			return RunResult{}, fmt.Errorf("Output retries requires an agent provider that supports session resumption; %q does not. Use ClaudeCode, Codex or Pi, or set retries to 0", o.Agent.Name())
		}
	}
```
After the prompt is resolved:
```go
	if o.Output != nil && !strings.Contains(resolved.Text, "<"+o.Output.Tag()+">") {
		return RunResult{}, fmt.Errorf("Output tag <%s> not found in the resolved prompt. The caller must instruct the agent to emit the configured tag", o.Output.Tag())
	}
```
At the end of `Run`, after building `runResult` (the value currently returned):
```go
	if o.Output == nil {
		return runResult, nil
	}
	var last IterationResult
	if n := len(runResult.Iterations); n > 0 {
		last = runResult.Iterations[n-1]
	}
	value, err := extractStructuredOutput(runResult.Stdout, o.Output, extractionContext{
		Commits: runResult.Commits, Branch: runResult.Branch, PreservedWorktreePath: runResult.PreservedWorktreePath,
		SessionID: last.SessionID, SessionFilePath: last.SessionFilePath,
	})
	if err == nil {
		runResult.Output = value
		return runResult, nil
	}
	var soe *StructuredOutputError
	if errors.As(err, &soe) && outputRetries > 0 && soe.SessionID != "" {
		retry := o
		retry.Prompt = BuildStructuredOutputRetryFeedback(soe, outputRetries-1)
		retry.PromptFile, retry.PromptArgs = "", nil
		retry.ResumeSession = soe.SessionID
		retry.forkSession = false
		retry.Output = withRetries(o.Output, outputRetries-1)
		return Run(ctx, retry)
	}
	return RunResult{}, err
```
with:
```go
// withRetries returns a copy of def with a new retry budget.
func withRetries(def OutputDefinition, n int) OutputDefinition {
	switch d := def.(type) {
	case outputString:
		d.maxRetries = n
		return d
	default:
		return def.(interface{ withRetries(int) OutputDefinition }).withRetries(n)
	}
}

func (d outputObject[T]) withRetries(n int) OutputDefinition { d.maxRetries = n; return d }
```
Make `RunResult.PreservedWorktreePath` exist (phase 3) or leave the field out of `extractionContext` if phase 3 named it differently.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add output.go output_test.go agentgopher.go agentgopher_test.go
git commit -m "Add structured output extraction with session-resume retries"
```

---

### Task 8: Shutdown registry and terminal cleanup

Port of `shutdownRegistry.ts` and `terminalCleanup.ts`: one signal listener for the whole process that fans out to registered teardowns, exits 1 on SIGINT or SIGTERM, and restores the cursor.

**Files:**
- Create: `internal/shutdown/shutdown.go`, `internal/shutdown/shutdown_test.go`

**Interfaces:**
- Produces:
  ```go
  func Register(teardown func()) (unregister func())
  const ShowCursor = "\x1b[?25h"
  func TerminalCleanup(stdout io.Writer) func()   // teardown that writes ShowCursor
  // test seam:
  var exit = os.Exit
  func runTeardowns()
  ```

- [ ] **Step 1: Write the failing tests**

`internal/shutdown/shutdown_test.go`:
```go
package shutdown

import (
	"bytes"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestRegisterFanOutRunsEachTeardownOnceAndExits(t *testing.T) {
	var code int
	exited := make(chan struct{})
	exit = func(c int) { code = c; close(exited) }
	defer func() { exit = os.Exit }()

	calls := map[string]int{}
	unregA := Register(func() { calls["a"]++ })
	unregB := Register(func() { calls["b"]++; panic("teardown panic must not block the others") })
	Register(func() { calls["c"]++ })
	defer unregA()
	defer unregB()

	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("exit was not called")
	}
	if code != 1 || calls["a"] != 1 || calls["b"] != 1 || calls["c"] != 1 {
		t.Fatalf("code=%d calls=%v", code, calls)
	}
}

func TestUnregisterIsIdempotentAndStopsCallbacks(t *testing.T) {
	n := 0
	unreg := Register(func() { n++ })
	unreg()
	unreg()
	runTeardowns()
	if n != 0 {
		t.Fatalf("teardown ran after unregister: %d", n)
	}
}

func TestTerminalCleanupShowsCursor(t *testing.T) {
	var buf bytes.Buffer
	TerminalCleanup(&buf)()
	if buf.String() != ShowCursor {
		t.Fatalf("got %q", buf.String())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/shutdown/`
Expected: FAIL, package not found.

- [ ] **Step 3: Write shutdown.go**

```go
// Package shutdown runs sandbox teardowns when the process is interrupted.
// One signal listener serves every live sandbox.
package shutdown

import (
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// ShowCursor is the DECTCEM escape that makes the cursor visible again.
const ShowCursor = "\x1b[?25h"

var (
	mu        sync.Mutex
	teardowns = map[int]func(){}
	nextID    int
	sigCh     chan os.Signal
	exit      = os.Exit
)

// Register adds a teardown to run on SIGINT or SIGTERM. The returned function
// unregisters it and is idempotent. The first registration installs the
// signal listener; the last unregistration removes it.
func Register(teardown func()) func() {
	mu.Lock()
	defer mu.Unlock()
	id := nextID
	nextID++
	teardowns[id] = teardown
	if sigCh == nil {
		sigCh = make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func(ch chan os.Signal) {
			if _, ok := <-ch; !ok {
				return
			}
			detach()
			runTeardowns()
			exit(1)
		}(sigCh)
	}
	done := false
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if done {
			return
		}
		done = true
		delete(teardowns, id)
		if len(teardowns) == 0 {
			detach()
		}
	}
}

// detach stops signal delivery; callers hold mu or run from the listener.
func detach() {
	if sigCh == nil {
		return
	}
	signal.Stop(sigCh)
	close(sigCh)
	sigCh = nil
}

// runTeardowns calls every registered teardown once, in registration order.
// A panic in one teardown must not block the others.
func runTeardowns() {
	mu.Lock()
	ids := make([]int, 0, len(teardowns))
	for id := range teardowns {
		ids = append(ids, id)
	}
	fns := make([]func(), 0, len(ids))
	for _, id := range sortedInts(ids) {
		fns = append(fns, teardowns[id])
	}
	mu.Unlock()
	for _, fn := range fns {
		func() {
			defer func() { _ = recover() }()
			fn()
		}()
	}
}

func sortedInts(a []int) []int {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
	return a
}

// TerminalCleanup returns a teardown that shows the cursor again, for use
// after a spinner was interrupted.
func TerminalCleanup(stdout io.Writer) func() {
	return func() { _, _ = io.WriteString(stdout, ShowCursor) }
}
```
The listener goroutine calls `detach`, which locks nothing; wrap the `detach()` call inside the goroutine with `mu.Lock(); detach(); mu.Unlock()`. Replace `sortedInts` with `sort.Ints` from the standard library.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/shutdown/`
Expected: PASS. The signal test sends SIGTERM to the test process itself; the stubbed `exit` keeps the process alive.

- [ ] **Step 5: Commit**

```bash
git add internal/shutdown/
git commit -m "Add process-wide shutdown registry and terminal cleanup"
```

---

### Task 9: Terminal display with spinner

Port of `ClackDisplay` from `Display.ts` with a small ANSI implementation: an animated spinner for `Task`, bold status lines, an indented summary box. Replaces the plain writer for `LoggingStdout` and is used by `Interactive`.

**Files:**
- Create: `internal/display/terminal.go`, `internal/display/terminal_test.go`
- Modify: `agentgopher.go` (use `display.NewTerminal(os.Stdout)` for `LoggingStdout`)

**Interfaces:**
- Produces: `display.NewTerminal(w io.Writer) Display`, `display.NewTerminalWithTicker(w io.Writer, interval time.Duration) Display` (test seam), `display.IsTerminal(f *os.File) bool`.
- When `w` is not a terminal (`IsTerminal` false, or `w` is not an `*os.File`), the spinner animation is skipped and `Task` prints `title...` and `title done` like the writer display. That keeps CI logs clean.

- [ ] **Step 1: Write the failing tests**

`internal/display/terminal_test.go`:
```go
package display

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTerminalFormatsEntries(t *testing.T) {
	var buf bytes.Buffer
	d := NewTerminal(&buf)
	d.Intro("agentgopher")
	d.Status("Iteration 1/3", SeverityInfo)
	d.Status("Oops", SeverityError)
	d.Summary("Run", [][2]string{{"Agent", "claude-code"}})
	d.ToolCall("Bash", "ls")
	d.Text("line")
	d.TextChunk("chunk\n")
	got := buf.String()
	for _, want := range []string{"\x1b[7m agentgopher \x1b[0m", "\x1b[1mIteration 1/3\x1b[0m", "\x1b[31m", "Run", "  \x1b[1mAgent\x1b[0m: \x1b[2mclaude-code\x1b[0m", "\x1b[2mBash(ls)\x1b[0m", "line\n", "chunk\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestTerminalTaskWithoutTTYPrintsPlainLines(t *testing.T) {
	var buf bytes.Buffer
	d := NewTerminal(&buf)
	err := d.Task("Setting up", func(log func(string)) error { log("step"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.Contains(got, "Setting up...\n  step\nSetting up done") {
		t.Fatalf("got %q", got)
	}
	want := errors.New("x")
	if err := d.Task("Fails", func(func(string)) error { return want }); !errors.Is(err, want) || !strings.Contains(buf.String(), "Fails (failed)") {
		t.Fatalf("err=%v out=%q", err, buf.String())
	}
}

func TestTerminalSpinnerAnimatesWhenEnabled(t *testing.T) {
	var buf bytes.Buffer
	d := NewTerminalWithTicker(&buf, 5*time.Millisecond).(*terminal)
	d.animate = true
	_ = d.Task("Working", func(func(string)) error { time.Sleep(30 * time.Millisecond); return nil })
	out := buf.String()
	if strings.Count(out, "\r") < 2 || !strings.Contains(out, "\x1b[?25l") || !strings.HasSuffix(out, "Working done\n") || !strings.Contains(out, "\x1b[?25h") {
		t.Fatalf("got %q", out)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/display/ -run Terminal`
Expected: FAIL, "undefined: NewTerminal".

- [ ] **Step 3: Write terminal.go**

```go
package display

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiInverse = "\x1b[7m"
	ansiRed     = "\x1b[31m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiBlue    = "\x1b[34m"
	hideCursor  = "\x1b[?25l"
	showCursor  = "\x1b[?25h"
)

var spinnerFrames = []string{"|", "/", "-", "\\"}

// IsTerminal reports whether f is a character device (an interactive terminal).
func IsTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

type terminal struct {
	mu       sync.Mutex
	w        io.Writer
	animate  bool
	interval time.Duration
}

// NewTerminal returns a Display that renders styled lines and, on a real
// terminal, an animated spinner for tasks.
func NewTerminal(w io.Writer) Display { return NewTerminalWithTicker(w, 80*time.Millisecond) }

// NewTerminalWithTicker is NewTerminal with a custom spinner interval.
func NewTerminalWithTicker(w io.Writer, interval time.Duration) Display {
	t := &terminal{w: w, interval: interval}
	if f, ok := w.(*os.File); ok {
		t.animate = IsTerminal(f)
	}
	return t
}

func (t *terminal) print(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = io.WriteString(t.w, s)
}

func (t *terminal) Intro(title string) { t.print(ansiInverse + " " + title + " " + ansiReset + "\n") }

func (t *terminal) Status(message string, sev Severity) {
	color := map[Severity]string{SeverityInfo: ansiBlue, SeveritySuccess: ansiGreen, SeverityWarn: ansiYellow, SeverityError: ansiRed}[sev]
	t.print(color + "*" + ansiReset + " " + ansiBold + message + ansiReset + "\n")
}

func (t *terminal) Summary(title string, rows [][2]string) {
	var b strings.Builder
	b.WriteString(ansiBold + title + ansiReset + "\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "  %s%s%s: %s%s%s\n", ansiBold, r[0], ansiReset, ansiDim, r[1], ansiReset)
	}
	t.print(b.String())
}

func (t *terminal) Task(title string, fn func(log func(string)) error) error {
	var logs []string
	var logMu sync.Mutex
	logFn := func(m string) {
		logMu.Lock()
		logs = append(logs, m)
		logMu.Unlock()
		if !t.animate {
			t.print("  " + m + "\n")
		}
	}
	if !t.animate {
		t.print(title + "...\n")
		err := fn(logFn)
		if err != nil {
			t.print(title + " (failed)\n")
			return err
		}
		t.print(title + " done\n")
		return nil
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t.print(hideCursor)
		i := 0
		ticker := time.NewTicker(t.interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				t.print("\r" + spinnerFrames[i%len(spinnerFrames)] + " " + title)
				i++
			}
		}
	}()
	err := fn(logFn)
	close(stop)
	<-done
	var b strings.Builder
	b.WriteString("\r\x1b[2K")
	if err != nil {
		b.WriteString(ansiRed + "x" + ansiReset + " " + title + " (failed)\n")
	} else {
		b.WriteString(ansiGreen + "+" + ansiReset + " " + title + " done\n")
	}
	logMu.Lock()
	for _, m := range logs {
		b.WriteString("  " + ansiDim + m + ansiReset + "\n")
	}
	logMu.Unlock()
	b.WriteString(showCursor)
	t.print(b.String())
	return err
}

func (t *terminal) Text(line string)       { t.print(line + "\n") }
func (t *terminal) TextChunk(chunk string) { t.print(chunk) }
func (t *terminal) ToolCall(name, args string) {
	t.print(ansiDim + name + "(" + args + ")" + ansiReset + "\n")
}
```

`TestTerminalSpinnerAnimatesWhenEnabled` expects the output to end with `Working done\n`; move the `showCursor` write before the status line so the final bytes are the done line. Adjust the order in `Task`: write `"\r\x1b[2K" + showCursor` first, then the status line and logs.

- [ ] **Step 4: Use the terminal display in Run**

In `agentgopher.go`, where `d := display.NewWriter(out)` is created: use `display.NewTerminal(os.Stdout)` when `logging.Type == LoggingStdout`, and keep `display.NewWriter(f)` for file mode.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/display/ ./`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/display/ agentgopher.go
git commit -m "Add terminal display with spinner"
```

---

### Task 10: Interactive exec on sandbox handles

Port of `interactiveExec` from `no-sandbox.ts`, `docker.ts` and `podman.ts`. The host process passes its own stdio to the child, so no PTY code is needed; Docker and Podman get `-it` when stdin is a terminal.

**Files:**
- Modify: `sandbox/sandbox.go`, `sandbox/nosandbox/nosandbox.go`, `sandbox/nosandbox/nosandbox_test.go`, `sandbox/docker/docker.go` (and the podman file from phase 3; both share the container-exec code)
- Create: `sandbox/docker/interactive_test.go`

**Interfaces:**
- Produces, in `sandbox`:
  ```go
  type InteractiveExecOptions struct { Stdin io.Reader; Stdout, Stderr io.Writer; Cwd string }
  type Interactive interface { InteractiveExec(ctx context.Context, args []string, opts InteractiveExecOptions) (exitCode int, err error) }
  func StdinIsTerminal(r io.Reader) bool   // true when r is an *os.File character device
  ```
- Produces, in `docker`: `func ContainerExecArgs(tool, container string, opts sandbox.InteractiveExecOptions, args []string) []string` (pure, tested) used by both docker and podman handles.

- [ ] **Step 1: Write the failing tests**

Append to `sandbox/nosandbox/nosandbox_test.go`:
```go
func TestInteractiveExecWiresStdio(t *testing.T) {
	h := newHandle(t)
	var out, errBuf bytes.Buffer
	code, err := h.(sandbox.Interactive).InteractiveExec(context.Background(), []string{"sh", "-c", "cat; echo err 1>&2; exit 4"},
		sandbox.InteractiveExecOptions{Stdin: strings.NewReader("typed"), Stdout: &out, Stderr: &errBuf})
	if err != nil || code != 4 || out.String() != "typed" || errBuf.String() != "err\n" {
		t.Fatalf("code=%d err=%v out=%q errbuf=%q", code, err, out.String(), errBuf.String())
	}
}

func TestInteractiveExecMissingBinary(t *testing.T) {
	h := newHandle(t)
	_, err := h.(sandbox.Interactive).InteractiveExec(context.Background(), []string{"definitely-not-a-binary-xyz"}, sandbox.InteractiveExecOptions{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard})
	var start *sandbox.StartError
	if !errors.As(err, &start) {
		t.Fatalf("err = %v", err)
	}
}
```
(add imports `bytes`, `io`, `strings`).

`sandbox/docker/interactive_test.go`:
```go
package docker

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/sandbox"
)

func TestContainerExecArgs(t *testing.T) {
	opts := sandbox.InteractiveExecOptions{Stdin: strings.NewReader(""), Cwd: "/home/agent/workspace"}
	got := ContainerExecArgs("docker", "c1", opts, []string{"claude", "--model", "m"})
	if strings.Join(got, " ") != "exec -i -w /home/agent/workspace c1 claude --model m" {
		t.Fatalf("got %v", got)
	}
	// A character device stdin gets a pseudo-terminal.
	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		t.Skip("no /dev/tty")
	}
	defer tty.Close()
	got = ContainerExecArgs("docker", "c1", sandbox.InteractiveExecOptions{Stdin: tty}, []string{"sh"})
	if strings.Join(got, " ") != "exec -it c1 sh" {
		t.Fatalf("tty: %v", got)
	}
	_ = io.Discard
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./sandbox/...`
Expected: FAIL, "undefined: sandbox.Interactive".

- [ ] **Step 3: Add the interface to sandbox/sandbox.go**

```go
// InteractiveExecOptions wire the caller's streams to an interactive process.
type InteractiveExecOptions struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Cwd defaults to the handle's worktree path.
	Cwd string
}

// Interactive is implemented by handles that can attach a terminal to a
// process inside the sandbox.
type Interactive interface {
	InteractiveExec(ctx context.Context, args []string, opts InteractiveExecOptions) (int, error)
}

// StdinIsTerminal reports whether r is a terminal, which decides whether a
// container exec allocates a pseudo-terminal.
func StdinIsTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
```

- [ ] **Step 4: Implement InteractiveExec on the no-sandbox handle**

In `sandbox/nosandbox/nosandbox.go`:
```go
// InteractiveExec runs args on the host with the caller's streams. When the
// streams are *os.File values the child inherits the descriptors directly.
func (h *handle) InteractiveExec(ctx context.Context, args []string, opts sandbox.InteractiveExecOptions) (int, error) {
	if len(args) == 0 {
		return 0, &sandbox.StartError{Command: "", Cause: errors.New("no command")}
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = opts.Cwd
	if cmd.Dir == "" {
		cmd.Dir = h.worktree
	}
	cmd.Env = h.env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = opts.Stdin, opts.Stdout, opts.Stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), nil
	case ctx.Err() != nil:
		return 0, ctx.Err()
	default:
		return 0, &sandbox.StartError{Command: strings.Join(args, " "), Cause: err}
	}
}
```

- [ ] **Step 5: Implement InteractiveExec on the docker and podman handles**

In `sandbox/docker/docker.go` (the podman handle from phase 3 reuses it with `tool = "podman"`):
```go
// ContainerExecArgs builds the `<tool> exec` argument list for an interactive
// process: -it with a terminal on stdin, -i otherwise, -w for the cwd.
func ContainerExecArgs(tool, container string, opts sandbox.InteractiveExecOptions, args []string) []string {
	out := []string{"exec"}
	if sandbox.StdinIsTerminal(opts.Stdin) {
		out = append(out, "-it")
	} else {
		out = append(out, "-i")
	}
	if opts.Cwd != "" {
		out = append(out, "-w", opts.Cwd)
	}
	_ = tool
	return append(append(out, container), args...)
}

func (h *handle) InteractiveExec(ctx context.Context, args []string, opts sandbox.InteractiveExecOptions) (int, error) {
	cmd := exec.CommandContext(ctx, h.tool, ContainerExecArgs(h.tool, h.container, opts, args)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = opts.Stdin, opts.Stdout, opts.Stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), nil
	case ctx.Err() != nil:
		return 0, ctx.Err()
	default:
		return 0, &sandbox.StartError{Command: h.tool + " exec", Cause: err}
	}
}
```
`h.tool` and `h.container` are the field names from the phase 3 docker handle (`"docker"` or `"podman"`, and the container name); adapt to the phase 3 names. Drop the `tool` parameter from `ContainerExecArgs` if it stays unused.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./sandbox/...`
Expected: PASS (the `/dev/tty` branch skips on CI).

- [ ] **Step 7: Commit**

```bash
git add sandbox/
git commit -m "Add interactive exec to no-sandbox, docker and podman handles"
```

---

### Task 11: Top-level `Interactive`

Port of `interactive.ts`: resolve prompt and args, ask the terminal for missing `{{KEY}}` values, create a worktree unless head mode, start the sandbox, run the lifecycle around the agent's TUI, report commits.

**Files:**
- Create: `interactive.go`, `interactive_test.go`

**Interfaces:**
- Produces:
  ```go
  type InteractiveOptions struct {
      Agent          agent.Provider
      Sandbox        sandbox.Provider          // nil means nosandbox.New(nil)
      Prompt, PromptFile string                // both empty: launch the agent without a prompt
      Name           string
      BranchStrategy *sandbox.BranchStrategy
      Hooks          Hooks
      CopyToWorktree []string
      PromptArgs     map[string]string
      Env            map[string]string
      Cwd            string
      Timeouts       Timeouts
      // Stdio defaults to the process streams; tests inject pipes.
      Stdin  io.Reader
      Stdout io.Writer
      Stderr io.Writer
  }
  type InteractiveResult struct { Commits []Commit; Branch, PreservedWorktreePath string; ExitCode int }
  func Interactive(ctx context.Context, o InteractiveOptions) (InteractiveResult, error)
  func collectMissingArgs(prompt string, args prompt.Args, in io.Reader, out io.Writer) (prompt.Args, error)  // unexported, tested
  ```
- `DangerouslySkipPermissions` is true for every sandbox kind except `KindNone` (the user manages host permissions themselves), matching upstream.

- [ ] **Step 1: Write the failing tests**

`interactive_test.go`:
```go
package agentgopher

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/internal/prompt"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

// tuiAgent "launches" a TUI that writes its args to a file and commits.
type tuiAgent struct{ echoAgent }

func (tuiAgent) BuildInteractiveArgs(o agent.CommandOptions) []string {
	return []string{"sh", "-c", `printf '%s' "$1" > tui-args.txt; git add -A; git commit -q -m "from tui"; exit 3`, "sh", o.Prompt}
}

func TestInteractiveRunsAgentAndCollectsCommits(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	pf := filepath.Join(repo, "p.md")
	os.WriteFile(pf, []byte("Hello {{NAME}} on {{SOURCE_BRANCH}}"), 0o644)
	var out bytes.Buffer
	res, err := Interactive(context.Background(), InteractiveOptions{
		Agent:      tuiAgent{},
		Sandbox:    nosandbox.New(nil),
		Cwd:        repo,
		PromptFile: pf,
		PromptArgs: map[string]string{"NAME": "ellen"},
		Stdin:      strings.NewReader(""),
		Stdout:     &out,
		Stderr:     &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.Branch != "main" || len(res.Commits) != 1 {
		t.Fatalf("res = %+v", res)
	}
	args, _ := os.ReadFile(filepath.Join(repo, "tui-args.txt"))
	if string(args) != "Hello ellen on main" {
		t.Fatalf("args = %q", args)
	}
	if !strings.Contains(out.String(), "Session Complete") {
		t.Fatalf("out:\n%s", out.String())
	}
}

func TestInteractiveRejectsProviderWithoutInteractiveArgs(t *testing.T) {
	_, err := Interactive(context.Background(), InteractiveOptions{Agent: echoAgent{}, Cwd: gitx.InitTestRepo(t), Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "does not support interactive sessions") {
		t.Fatalf("err = %v", err)
	}
}

func TestCollectMissingArgsAsksAndRejectsEmpty(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("\nvalue-a\nvalue-b\n")
	got, err := collectMissingArgs("{{A}} {{B}} {{A}}", prompt.Args{}, in, &out)
	if err != nil || got["A"] != "value-a" || got["B"] != "value-b" {
		t.Fatalf("got %v, %v", got, err)
	}
	if strings.Count(out.String(), "Enter value for {{A}}") != 2 || !strings.Contains(out.String(), "A value is required") {
		t.Fatalf("out = %q", out.String())
	}
	if _, err := collectMissingArgs("{{A}}", prompt.Args{}, strings.NewReader(""), &out); err == nil {
		t.Fatal("EOF must fail")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./ -run 'Interactive|CollectMissing'`
Expected: FAIL, "undefined: Interactive".

- [ ] **Step 3: Write interactive.go**

```go
package agentgopher

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/copy"
	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/env"
	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/internal/prompt"
	"github.com/e11en/agentgopher/internal/start"
	"github.com/e11en/agentgopher/internal/sync"
	"github.com/e11en/agentgopher/internal/worktree"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

// InteractiveOptions configure Interactive.
type InteractiveOptions struct {
	Agent          agent.Provider
	Sandbox        sandbox.Provider
	Prompt         string
	PromptFile     string
	Name           string
	BranchStrategy *sandbox.BranchStrategy
	Hooks          Hooks
	CopyToWorktree []string
	PromptArgs     map[string]string
	Env            map[string]string
	Cwd            string
	Timeouts       Timeouts
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
}

// InteractiveResult is the outcome of an interactive session.
type InteractiveResult struct {
	Commits               []Commit
	Branch                string
	PreservedWorktreePath string
	ExitCode              int
}

// collectMissingArgs asks the terminal for every {{KEY}} that args does not
// provide. An empty answer is rejected and asked again; EOF fails.
func collectMissingArgs(text string, args prompt.Args, in io.Reader, out io.Writer) (prompt.Args, error) {
	merged := prompt.Args{}
	for k, v := range args {
		merged[k] = v
	}
	reader := bufio.NewReader(in)
	for _, key := range prompt.FindMissingKeys(text, args) {
		for {
			fmt.Fprintf(out, "Enter value for {{%s}}: ", key)
			line, err := reader.ReadString('\n')
			if err != nil && line == "" {
				return nil, errors.New("prompt arg collection cancelled")
			}
			line = strings.TrimSpace(line)
			if line != "" {
				merged[key] = line
				break
			}
			fmt.Fprintf(out, "A value is required for {{%s}}\n", key)
		}
	}
	return merged, nil
}

// Interactive launches the agent's own TUI inside a sandbox and collects the
// commits it made when the session ends.
func Interactive(ctx context.Context, o InteractiveOptions) (InteractiveResult, error) {
	if err := ctx.Err(); err != nil {
		return InteractiveResult{}, err
	}
	if o.Sandbox == nil {
		o.Sandbox = nosandbox.New(nil)
	}
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	builder, ok := o.Agent.(agent.InteractiveArgsBuilder)
	if !ok {
		return InteractiveResult{}, fmt.Errorf("agent provider %q does not support interactive sessions", o.Agent.Name())
	}

	strategy := sandbox.BranchStrategy{Type: sandbox.BranchHead}
	if o.Sandbox.Kind() == sandbox.KindIsolated {
		strategy.Type = sandbox.BranchMergeToHead
	}
	if o.BranchStrategy != nil {
		strategy = *o.BranchStrategy
	}
	if strategy.Type == sandbox.BranchHead && o.Sandbox.Kind() == sandbox.KindIsolated {
		return InteractiveResult{}, errors.New("head branch strategy is not supported with isolated providers")
	}
	if strategy.Type == sandbox.BranchHead && len(o.CopyToWorktree) > 0 {
		return InteractiveResult{}, errors.New("CopyToWorktree is not supported with the head branch strategy")
	}
	headMode := strategy.Type == sandbox.BranchHead

	hostRepoDir, err := resolveCwd(o.Cwd)
	if err != nil {
		return InteractiveResult{}, err
	}
	d := display.NewTerminal(o.Stdout)

	hasPrompt := o.Prompt != "" || o.PromptFile != ""
	resolvedText, inline := "", false
	if hasPrompt {
		r, err := prompt.Resolve(o.Prompt, o.PromptFile)
		if err != nil {
			return InteractiveResult{}, err
		}
		resolvedText, inline = r.Text, r.Source == prompt.SourceInline
	}

	resolvedEnv, err := env.Resolve(hostRepoDir)
	if err != nil {
		return InteractiveResult{}, err
	}
	merged, err := env.MergeProviderEnv(resolvedEnv, o.Agent.Env(), o.Sandbox.Env())
	if err != nil {
		return InteractiveResult{}, err
	}
	for k, v := range o.Env {
		merged[k] = v
	}

	currentBranch, err := gitx.CurrentBranch(ctx, hostRepoDir)
	if err != nil {
		return InteractiveResult{}, err
	}
	resolvedBranch := currentBranch
	switch strategy.Type {
	case sandbox.BranchNamed:
		resolvedBranch = strategy.Branch
	case sandbox.BranchMergeToHead:
		resolvedBranch = worktree.GenerateTempBranchName(o.Name)
	}

	userArgs := prompt.Args(o.PromptArgs)
	substituted := resolvedText
	switch {
	case hasPrompt && !inline:
		if err := prompt.ValidateNoBuiltInOverride(userArgs); err != nil {
			return InteractiveResult{}, err
		}
		collected, err := collectMissingArgs(resolvedText, userArgs, o.Stdin, o.Stdout)
		if err != nil {
			return InteractiveResult{}, err
		}
		args := prompt.Args{"SOURCE_BRANCH": resolvedBranch, "TARGET_BRANCH": currentBranch}
		for k, v := range collected {
			args[k] = v
		}
		silent := map[string]bool{"SOURCE_BRANCH": true, "TARGET_BRANCH": true}
		substituted, err = prompt.Substitute(resolvedText, args, silent, func(m string) { d.Status(m, display.SeverityWarn) })
		if err != nil {
			return InteractiveResult{}, err
		}
	case inline:
		if err := prompt.ValidateNoArgsWithInlinePrompt(userArgs); err != nil {
			return InteractiveResult{}, err
		}
	}

	d.Intro(orDefault(o.Name, "agentgopher interactive"))
	d.Summary("Interactive Session", [][2]string{{"Agent", orDefault(o.Name, o.Agent.Name())}, {"Sandbox", o.Sandbox.Name()}, {"Branch", resolvedBranch}})

	var wt *worktree.Info
	if !headMode {
		var info worktree.Info
		err := d.Task("Creating worktree", func(func(string)) error {
			_ = worktree.Prune(ctx, hostRepoDir)
			var err error
			info, err = createWorktree(ctx, hostRepoDir, sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: strategy.Branch, BaseBranch: strategy.BaseBranch}, o.Name)
			return err
		})
		if err != nil {
			return InteractiveResult{}, err
		}
		wt = &info
	}
	removeWorktree := func() {
		if wt != nil {
			_ = worktree.Remove(ctx, wt.Path)
		}
	}

	worktreeOrRepo := hostRepoDir
	if wt != nil {
		worktreeOrRepo = wt.Path
	}
	if wt != nil && len(o.CopyToWorktree) > 0 && o.Sandbox.Kind() != sandbox.KindIsolated {
		if err := d.Task("Copying files to worktree", func(func(string)) error {
			return worktree.CopyToWorktree(ctx, o.CopyToWorktree, hostRepoDir, wt.Path, 0)
		}); err != nil {
			removeWorktree()
			return InteractiveResult{}, err
		}
	}
	if err := lifecycle.RunHostHooks(ctx, o.Hooks.HostOnWorktreeReady, worktreeOrRepo); err != nil {
		removeWorktree()
		return InteractiveResult{}, err
	}

	var started *orchestrator.Session
	err = d.Task("Starting sandbox", func(func(string)) error {
		var err error
		// wt is nil in head mode; startSession then works on hostRepoDir directly.
		started, err = orchestrator.StartSession(ctx, orchestrator.Options{Sandbox: o.Sandbox, HostRepoDir: hostRepoDir, Worktree: wt, BranchStrategy: strategy, Env: merged, CopyToWorktree: o.CopyToWorktree, Hooks: o.Hooks, Timeouts: o.Timeouts, Display: d, Name: o.Name})
		return err
	})
	if err != nil {
		removeWorktree()
		return InteractiveResult{}, err
	}
	defer started.Handle.Close(ctx)

	ix, ok := started.Handle.(sandbox.Interactive)
	if !ok {
		removeWorktree()
		return InteractiveResult{}, fmt.Errorf("sandbox provider %q does not support interactive sessions", o.Sandbox.Name())
	}

	lcOpts := lifecycle.Options{
		HostRepoDir:      hostRepoDir,
		SandboxRepoDir:   started.SandboxRepoDir,
		HostWorktreePath: worktreeOrRepo,
		Hooks:            o.Hooks,
		Timeouts:         o.Timeouts,
		Display:          d,
	}
	switch strategy.Type {
	case sandbox.BranchHead:
		lcOpts.Branch = currentBranch
	case sandbox.BranchNamed:
		lcOpts.Branch = strategy.Branch
	}
	if iso, ok := started.Handle.(sandbox.IsolatedHandle); ok && wt != nil {
		lcOpts.ApplyToHost = func(ctx context.Context) error { return sync.Out(ctx, wt.Path, iso) }
	}

	lc, err := lifecycle.Run(ctx, lcOpts, started.Handle, func(lctx lifecycle.Context) (int, error) {
		full := substituted
		if hasPrompt && !inline {
			var err error
			err = d.Task("Expanding shell expressions", func(log func(string)) error {
				var expandErr error
				full, expandErr = prompt.Expand(ctx, substituted, lctx.Sandbox, lctx.SandboxRepoDir, log)
				return expandErr
			})
			if err != nil {
				return 0, err
			}
		}
		args := builder.BuildInteractiveArgs(agent.CommandOptions{Prompt: full, DangerouslySkipPermissions: o.Sandbox.Kind() != sandbox.KindNone})
		return ix.InteractiveExec(ctx, args, sandbox.InteractiveExecOptions{Stdin: o.Stdin, Stdout: o.Stdout, Stderr: o.Stderr, Cwd: lctx.SandboxRepoDir})
	})
	if err != nil {
		removeWorktree()
		return InteractiveResult{}, err
	}

	preserved := ""
	if wt != nil {
		if dirty, _ := worktree.HasUncommittedChanges(ctx, wt.Path); dirty {
			preserved = wt.Path
		} else {
			removeWorktree()
		}
	}
	rows := [][2]string{{"Commits", fmt.Sprint(len(lc.Commits))}, {"Branch", lc.Branch}, {"Exit code", fmt.Sprint(lc.Value)}}
	if preserved != "" {
		rows = append(rows, [2]string{"Preserved worktree", preserved})
	}
	d.Summary("Session Complete", rows)

	commits := make([]Commit, len(lc.Commits))
	for i, sha := range lc.Commits {
		commits[i] = Commit{SHA: sha}
	}
	return InteractiveResult{Commits: commits, Branch: lc.Branch, PreservedWorktreePath: preserved, ExitCode: lc.Value}, nil
}
```
The test agent commits as part of its "TUI"; because the test uses the head strategy on the no-sandbox provider, the lifecycle's commit collection on `refs/heads/main` returns that commit.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add interactive.go interactive_test.go
git commit -m "Add Interactive for attaching a terminal to the agent"
```

---

### Task 12: `CreateSandbox` and the worktree-backed sandbox

Port of `createSandbox.ts`: a long-lived worktree plus sandbox handle on an explicit branch, reused across `Run`, `Interactive` and `Exec` calls, torn down by `Close`. `(*Worktree).CreateSandbox` and `(*Worktree).Interactive` reuse the same handle builder with container-only close.

**Files:**
- Create: `createsandbox.go`, `createsandbox_test.go`
- Modify: `internal/orchestrator/orchestrator.go` (existing-sandbox mode), `worktree.go` (phase 3 file; add two methods)

**Interfaces:**
- Produces, in `orchestrator`:
  ```go
  // Existing makes Orchestrate reuse an already-running sandbox instead of
  // creating one per iteration. Set by CreateSandbox.
  type Existing struct {
      Handle           sandbox.Handle
      SandboxRepoDir   string
      HostWorktreePath string
      ApplyToHost      func(ctx context.Context) error // nil for bind-mount and no-sandbox
      KeepSourceBranch bool
  }
  // Options gains: Existing *Existing
  ```
- Produces, in the root package:
  ```go
  type CreateSandboxOptions struct {
      Branch, BaseBranch string           // Branch required
      Sandbox        sandbox.Provider
      Cwd            string
      Hooks          Hooks
      CopyToWorktree []string
      Timeouts       Timeouts
  }
  type SandboxRunOptions struct {
      Agent agent.Provider; Prompt, PromptFile string; MaxIterations int
      PromptArgs map[string]string; CompletionSignal []string; IdleTimeout, CompletionTimeout time.Duration
      Name string; Logging *Logging; ResumeSession string; forkSession bool
  }
  type SandboxRunResult struct {
      Iterations []IterationResult; CompletionSignal, Stdout string; Commits []Commit; LogFilePath string
      sb *Sandbox; opts SandboxRunOptions
  }
  func (r SandboxRunResult) Resume(ctx, prompt string, o *ResumeOptions) (SandboxRunResult, error)
  func (r SandboxRunResult) Fork(ctx, prompt string, o *ResumeOptions) (SandboxRunResult, error)
  type SandboxInteractiveOptions struct { Agent agent.Provider; Prompt, PromptFile string; PromptArgs map[string]string; Name string; Stdin io.Reader; Stdout, Stderr io.Writer }
  type SandboxInteractiveResult struct { Commits []Commit; ExitCode int }
  type SandboxExecOptions = sandbox.ExecOptions
  type CloseResult struct { PreservedWorktreePath string }
  type Sandbox struct { Branch, WorktreePath string; /* unexported: hostRepoDir, repoDir, handle, strategy, applyToHost, timeouts, closeFn */ }
  func CreateSandbox(ctx context.Context, o CreateSandboxOptions) (*Sandbox, error)
  func (s *Sandbox) Run(ctx context.Context, o SandboxRunOptions) (SandboxRunResult, error)
  func (s *Sandbox) Interactive(ctx context.Context, o SandboxInteractiveOptions) (SandboxInteractiveResult, error)
  func (s *Sandbox) Exec(ctx context.Context, command string, o SandboxExecOptions) (sandbox.ExecResult, error)
  func (s *Sandbox) Close(ctx context.Context) (CloseResult, error)
  func (w *Worktree) CreateSandbox(ctx context.Context, o WorktreeCreateSandboxOptions) (*Sandbox, error)
  func (w *Worktree) Interactive(ctx context.Context, o WorktreeInteractiveOptions) (InteractiveResult, error)
  type WorktreeCreateSandboxOptions struct { Sandbox sandbox.Provider; Hooks Hooks; CopyToWorktree []string; Timeouts Timeouts }
  type WorktreeInteractiveOptions struct { Agent agent.Provider; Sandbox sandbox.Provider; Prompt, PromptFile string; PromptArgs map[string]string; Name string; Hooks Hooks; CopyToWorktree []string; Stdin io.Reader; Stdout, Stderr io.Writer }
  ```

- [ ] **Step 1: Add existing-sandbox mode to the orchestrator**

In `internal/orchestrator/orchestrator.go`, add the `Existing` type and the `Existing *Existing` field. In `Orchestrate`, where the per-iteration factory is called (`withSandbox` from phase 3), branch first:
```go
		if o.Existing != nil {
			lc, err := lifecycle.Run(ctx, lifecycle.Options{
				HostRepoDir:      o.HostRepoDir,
				SandboxRepoDir:   o.Existing.SandboxRepoDir,
				HostWorktreePath: o.Existing.HostWorktreePath,
				Branch:           o.Branch,
				Hooks:            lifecycle.Hooks{}, // hooks ran once at CreateSandbox time
				Timeouts:         o.Timeouts,
				Display:          o.Display,
				ApplyToHost:      o.Existing.ApplyToHost,
				KeepSourceBranch: o.Existing.KeepSourceBranch,
			}, o.Existing.Handle, func(lctx lifecycle.Context) (iterationOutcome, error) {
				return runIteration(ctx, o, lctx, i, signals, idle, completion, label)
			})
			// then the same result bookkeeping as the per-iteration path
		}
```
Refactor the bookkeeping after the lifecycle call into a helper `func (r *Result) absorb(lc lifecycle.Result[iterationOutcome])` so both paths share it. Add a test to `orchestrator_test.go`:
```go
func TestOrchestrateExistingSandboxDoesNotCreateHandles(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{0, "RESULT " + DefaultCompletionSignal}}}
	o, _ := baseOptions(t)
	o.Iterations = 1
	o.Sandbox = &fakeNoSandboxProvider{}
	o.Existing = &Existing{Handle: h, SandboxRepoDir: o.HostRepoDir, HostWorktreePath: o.HostRepoDir}
	res, err := Orchestrate(context.Background(), o)
	if err != nil || res.CompletionSignal == "" || o.Sandbox.(*fakeNoSandboxProvider).created != 0 {
		t.Fatalf("res=%+v err=%v created=%d", res, err, o.Sandbox.(*fakeNoSandboxProvider).created)
	}
}
```

- [ ] **Step 2: Write the failing root tests**

`createsandbox_test.go`:
```go
package agentgopher

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

func TestCreateSandboxRunsTwiceOnTheSameWorktreeAndCloses(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	sb, err := CreateSandbox(context.Background(), CreateSandboxOptions{Branch: "feature/x", Sandbox: nosandbox.New(nil), Cwd: repo,
		Hooks: Hooks{SandboxOnSandboxReady: []SandboxHook{{Command: "touch hooked"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if sb.Branch != "feature/x" || !strings.Contains(sb.WorktreePath, filepath.Join(".agentgopher", "worktrees")) {
		t.Fatalf("sb = %+v", sb)
	}
	if _, err := os.Stat(filepath.Join(sb.WorktreePath, "hooked")); err != nil {
		t.Fatal("onSandboxReady hook did not run in the worktree")
	}

	script := `echo "a line" >> notes.txt; git add -A; git commit -q -m "agent"; echo "RESULT <promise>COMPLETE</promise>"`
	r1, err := sb.Run(context.Background(), SandboxRunOptions{Agent: echoAgent{script: script}, Prompt: "one", Logging: &Logging{Type: LoggingFile, Path: filepath.Join(repo, "r1.log")}})
	if err != nil || len(r1.Commits) != 1 {
		t.Fatalf("r1 = %+v, %v", r1, err)
	}
	r2, err := sb.Run(context.Background(), SandboxRunOptions{Agent: echoAgent{script: script}, Prompt: "two"})
	if err != nil || len(r2.Commits) != 1 {
		t.Fatalf("r2 = %+v, %v", r2, err)
	}
	if res, err := sb.Exec(context.Background(), "wc -l < notes.txt", SandboxExecOptions{}); err != nil || strings.TrimSpace(res.Stdout) != "2" {
		t.Fatalf("exec = %+v, %v", res, err)
	}
	shas, _ := gitx.RevList(context.Background(), repo, "main", "refs/heads/feature/x")
	if len(shas) != 2 {
		t.Fatalf("branch has %d commits", len(shas))
	}

	cr, err := sb.Close(context.Background())
	if err != nil || cr.PreservedWorktreePath != "" {
		t.Fatalf("close = %+v, %v", cr, err)
	}
	if _, err := os.Stat(sb.WorktreePath); !os.IsNotExist(err) {
		t.Fatal("worktree not removed")
	}
	if _, err := sb.Close(context.Background()); err != nil {
		t.Fatal("second close must be a no-op")
	}
}

func TestCreateSandboxClosePreservesDirtyWorktree(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	sb, err := CreateSandbox(context.Background(), CreateSandboxOptions{Branch: "dirty", Sandbox: nosandbox.New(nil), Cwd: repo})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(sb.WorktreePath, "untracked.txt"), []byte("x"), 0o644)
	cr, err := sb.Close(context.Background())
	if err != nil || cr.PreservedWorktreePath != sb.WorktreePath {
		t.Fatalf("close = %+v, %v", cr, err)
	}
}

func TestCreateSandboxInteractiveAndResumeGuards(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	sb, err := CreateSandbox(context.Background(), CreateSandboxOptions{Branch: "tui", Sandbox: nosandbox.New(nil), Cwd: repo})
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close(context.Background())
	var out bytes.Buffer
	ir, err := sb.Interactive(context.Background(), SandboxInteractiveOptions{Agent: tuiAgent{}, Prompt: "hello", Stdin: strings.NewReader(""), Stdout: &out, Stderr: &out})
	if err != nil || ir.ExitCode != 3 || len(ir.Commits) != 1 {
		t.Fatalf("ir = %+v, %v", ir, err)
	}
	r, err := sb.Run(context.Background(), SandboxRunOptions{Agent: echoAgent{script: `echo "RESULT <promise>COMPLETE</promise>"`}, Prompt: "x", Logging: &Logging{Type: LoggingFile, Path: filepath.Join(repo, "r.log")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resume(context.Background(), "again", nil); err == nil || !strings.Contains(err.Error(), "no session") {
		t.Fatalf("resume: %v", err)
	}
	_, err = sb.Run(context.Background(), SandboxRunOptions{Agent: echoAgent{script: "true"}, Prompt: "x", ResumeSession: "s", MaxIterations: 2})
	if err == nil || !strings.Contains(err.Error(), "MaxIterations > 1") {
		t.Fatalf("validation: %v", err)
	}
	_ = sandbox.KindNone
}

func TestWorktreeCreateSandboxLeavesWorktreeOnClose(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	wt, err := CreateWorktree(context.Background(), CreateWorktreeOptions{Cwd: repo, BranchStrategy: &sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: "wt-branch"}})
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close(context.Background())
	sb, err := wt.CreateSandbox(context.Background(), WorktreeCreateSandboxOptions{Sandbox: nosandbox.New(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if sb.WorktreePath != wt.Path {
		t.Fatalf("sandbox must reuse the worktree: %q vs %q", sb.WorktreePath, wt.Path)
	}
	if _, err := sb.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatal("worktree-backed sandbox close must keep the worktree")
	}
}
```
`CreateWorktreeOptions` and its `BranchStrategy`/`Cwd` fields come from phase 3; adapt the field names if they differ.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./ -run 'CreateSandbox|WorktreeCreateSandbox'`
Expected: FAIL, "undefined: CreateSandbox".

- [ ] **Step 4: Write createsandbox.go**

```go
package agentgopher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/copy"
	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/env"
	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/internal/orchestrator"
	"github.com/e11en/agentgopher/internal/prompt"
	"github.com/e11en/agentgopher/internal/shutdown"
	"github.com/e11en/agentgopher/internal/start"
	gitsync "github.com/e11en/agentgopher/internal/sync"
	"github.com/e11en/agentgopher/internal/worktree"
	"github.com/e11en/agentgopher/sandbox"
)

// CreateSandboxOptions configure CreateSandbox.
type CreateSandboxOptions struct {
	// Branch is the explicit branch for the worktree (required).
	Branch string
	// BaseBranch is forked from when Branch does not exist yet. Default HEAD.
	BaseBranch     string
	Sandbox        sandbox.Provider
	Cwd            string
	Hooks          Hooks
	CopyToWorktree []string
	Timeouts       Timeouts
}

// SandboxRunOptions configure (*Sandbox).Run.
type SandboxRunOptions struct {
	Agent             agent.Provider
	Prompt            string
	PromptFile        string
	MaxIterations     int
	PromptArgs        map[string]string
	CompletionSignal  []string
	IdleTimeout       time.Duration
	CompletionTimeout time.Duration
	Name              string
	Logging           *Logging
	ResumeSession     string
	forkSession       bool
}

// SandboxRunResult is the outcome of (*Sandbox).Run.
type SandboxRunResult struct {
	Iterations       []IterationResult
	CompletionSignal string
	Stdout           string
	Commits          []Commit
	LogFilePath      string
	sb               *Sandbox
	opts             SandboxRunOptions
}

// SandboxInteractiveOptions configure (*Sandbox).Interactive.
type SandboxInteractiveOptions struct {
	Agent      agent.Provider
	Prompt     string
	PromptFile string
	PromptArgs map[string]string
	Name       string
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
}

// SandboxInteractiveResult is the outcome of (*Sandbox).Interactive.
type SandboxInteractiveResult struct {
	Commits  []Commit
	ExitCode int
}

// SandboxExecOptions mirror the provider handle's exec options.
type SandboxExecOptions = sandbox.ExecOptions

// CloseResult reports a worktree kept because it had uncommitted changes.
type CloseResult struct{ PreservedWorktreePath string }

// Sandbox is a long-lived worktree plus sandbox reused across runs.
type Sandbox struct {
	Branch       string
	WorktreePath string

	hostRepoDir string
	repoDir     string
	handle      sandbox.Handle
	provider    sandbox.Provider
	strategy    sandbox.BranchStrategy
	applyToHost func(context.Context) error
	timeouts    Timeouts
	closeMu     sync.Mutex
	closed      bool
	closeFn     func(ctx context.Context) (CloseResult, error)
}

func toCommits(shas []string) []Commit {
	out := make([]Commit, len(shas))
	for i, s := range shas {
		out[i] = Commit{SHA: s}
	}
	return out
}

// startOnWorktree starts the provider sandbox on an existing worktree and runs
// the onSandboxReady hooks once.
func startOnWorktree(ctx context.Context, provider sandbox.Provider, hostRepoDir, worktreePath string, hooks Hooks, copyPaths []string, timeouts Timeouts) (*orchestrator.Session, error) {
	if len(copyPaths) > 0 && provider.Kind() != sandbox.KindIsolated {
		if err := worktree.CopyToWorktree(ctx, copyPaths, hostRepoDir, worktreePath, 0); err != nil {
			return nil, err
		}
	}
	if err := lifecycle.RunHostHooks(ctx, hooks.HostOnWorktreeReady, worktreePath); err != nil {
		return nil, err
	}
	resolvedEnv, err := env.Resolve(hostRepoDir)
	if err != nil {
		return nil, err
	}
	merged, err := env.MergeProviderEnv(resolvedEnv, nil, provider.Env())
	if err != nil {
		return nil, err
	}
	started, err := orchestrator.StartSession(ctx, orchestrator.Options{Sandbox: provider, HostRepoDir: hostRepoDir, Worktree: &info, Env: merged, CopyToWorktree: copyPaths, Hooks: hooks, Timeouts: timeouts})
	if err != nil {
		return nil, err
	}
	if len(hooks.SandboxOnSandboxReady) > 0 || len(hooks.HostOnSandboxReady) > 0 {
		// Hooks run through the lifecycle's setup step with no work and no
		// commit collection; the Branch is the worktree's own so nothing merges.
		_, err := lifecycle.Run(ctx, lifecycle.Options{
			HostRepoDir: hostRepoDir, SandboxRepoDir: started.SandboxRepoDir, HostWorktreePath: worktreePath,
			Branch: "HEAD", Hooks: Hooks{SandboxOnSandboxReady: hooks.SandboxOnSandboxReady, HostOnSandboxReady: hooks.HostOnSandboxReady},
			Timeouts: timeouts, Display: display.NewWriter(io.Discard),
		}, started.Handle, func(lifecycle.Context) (struct{}, error) { return struct{}{}, nil })
		if err != nil {
			_ = started.Handle.Close(ctx)
			return nil, err
		}
	}
	return started, nil
}

// CreateSandbox creates a worktree on an explicit branch and starts a sandbox
// on it. The returned Sandbox is reused across Run, Interactive and Exec
// calls until Close.
func CreateSandbox(ctx context.Context, o CreateSandboxOptions) (*Sandbox, error) {
	if o.Branch == "" {
		return nil, errors.New("CreateSandbox requires Branch")
	}
	hostRepoDir, err := resolveCwd(o.Cwd)
	if err != nil {
		return nil, err
	}
	_ = worktree.Prune(ctx, hostRepoDir)
	info, err := createWorktree(ctx, hostRepoDir, sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: o.Branch, BaseBranch: o.BaseBranch}, \"\")
	if err != nil {
		return nil, err
	}
	started, err := startOnWorktree(ctx, o.Sandbox, hostRepoDir, info.Path, o.Hooks, o.CopyToWorktree, o.Timeouts)
	if err != nil {
		_ = worktree.Remove(ctx, info.Path)
		return nil, err
	}

	sb := newSandbox(hostRepoDir, info.Path, o.Branch, sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: o.Branch}, started, o.Sandbox, o.Timeouts)
	unregister := shutdown.Register(func() {
		fmt.Fprintf(os.Stderr, "\nWorktree preserved at %s\n  To review: cd %s\n  To clean up: git worktree remove --force %s\n", info.Path, info.Path, info.Path)
	})
	sb.closeFn = func(ctx context.Context) (CloseResult, error) {
		unregister()
		_ = started.Handle.Close(ctx)
		if dirty, _ := worktree.HasUncommittedChanges(ctx, info.Path); dirty {
			return CloseResult{PreservedWorktreePath: info.Path}, nil
		}
		_ = worktree.Remove(ctx, info.Path)
		return CloseResult{}, nil
	}
	return sb, nil
}

func newSandbox(hostRepoDir, worktreePath, branch string, strategy sandbox.BranchStrategy, started *orchestrator.Session, provider sandbox.Provider, timeouts Timeouts) *Sandbox {
	sb := &Sandbox{Branch: branch, WorktreePath: worktreePath, hostRepoDir: hostRepoDir, repoDir: started.SandboxRepoDir, handle: started.Handle, provider: provider, strategy: strategy, timeouts: timeouts}
	if iso, ok := started.Handle.(sandbox.IsolatedHandle); ok {
		sb.applyToHost = func(ctx context.Context) error { return gitsync.Out(ctx, worktreePath, iso) }
	}
	return sb
}

// lifecycleBranch is "" in merge-to-head mode (the lifecycle merges into the
// host branch and keeps the worktree's source branch), else the branch itself.
func (s *Sandbox) lifecycleBranch() string {
	if s.strategy.Type == sandbox.BranchMergeToHead {
		return ""
	}
	return s.Branch
}

func (s *Sandbox) existing() *orchestrator.Existing {
	return &orchestrator.Existing{Handle: s.handle, SandboxRepoDir: s.repoDir, HostWorktreePath: s.WorktreePath, ApplyToHost: s.applyToHost, KeepSourceBranch: s.strategy.Type == sandbox.BranchMergeToHead}
}

func (s *Sandbox) resolvePrompt(ctx context.Context, inline, file string, args map[string]string, warn func(string)) (text string, isInline bool, err error) {
	r, err := prompt.Resolve(inline, file)
	if err != nil {
		return "", false, err
	}
	userArgs := prompt.Args(args)
	if r.Source == prompt.SourceInline {
		return r.Text, true, prompt.ValidateNoArgsWithInlinePrompt(userArgs)
	}
	if err := prompt.ValidateNoBuiltInOverride(userArgs); err != nil {
		return "", false, err
	}
	currentBranch, err := gitx.CurrentBranch(ctx, s.hostRepoDir)
	if err != nil {
		return "", false, err
	}
	all := prompt.Args{"SOURCE_BRANCH": s.Branch, "TARGET_BRANCH": currentBranch}
	for k, v := range userArgs {
		all[k] = v
	}
	text, err = prompt.Substitute(r.Text, all, map[string]bool{"SOURCE_BRANCH": true, "TARGET_BRANCH": true}, warn)
	return text, false, err
}

// Run invokes an agent inside the existing sandbox.
func (s *Sandbox) Run(ctx context.Context, o SandboxRunOptions) (SandboxRunResult, error) {
	if err := ctx.Err(); err != nil {
		return SandboxRunResult{}, err
	}
	maxIterations := o.MaxIterations
	if maxIterations == 0 {
		maxIterations = DefaultMaxIterations
	}
	if o.ResumeSession != "" && maxIterations > 1 {
		return SandboxRunResult{}, errors.New("ResumeSession cannot be combined with MaxIterations > 1. Resume applies to iteration 1 only (ADR 0011)")
	}
	if o.forkSession && o.ResumeSession == "" {
		return SandboxRunResult{}, errors.New("forkSession requires ResumeSession; use SandboxRunResult.Fork")
	}
	if o.ResumeSession != "" {
		if err := orchestrator.AssertResumeSessionExists(o.Agent, s.provider.Kind(), s.hostRepoDir, o.ResumeSession); err != nil {
			return SandboxRunResult{}, err
		}
	}

	logging := Logging{Type: LoggingFile}
	if o.Logging != nil {
		logging = *o.Logging
	}
	if logging.Type == LoggingFile && logging.Path == "" {
		logging.Path = filepath.Join(s.hostRepoDir, ".agentgopher", "logs", BuildLogFilename(s.Branch, "", o.Name))
	}
	var d display.Display
	var out io.Writer = io.Discard
	logFilePath := ""
	if logging.Type == LoggingFile {
		f, err := display.OpenLogFile(logging.Path)
		if err != nil {
			return SandboxRunResult{}, err
		}
		defer f.Close()
		out, logFilePath = f, logging.Path
		printFileStartup(o.Name, s.Branch, logging.Path, s.hostRepoDir)
		d = display.NewWriter(f)
	} else {
		// Upstream keeps sandbox runs silent in stdout mode; the plain writer on
		// stdout is the closest useful behaviour.
		out = os.Stdout
		d = display.NewWriter(out)
	}

	text, inline, err := s.resolvePrompt(ctx, o.Prompt, o.PromptFile, o.PromptArgs, func(m string) { d.Status(m, display.SeverityWarn) })
	if err != nil {
		return SandboxRunResult{}, logError(d, err)
	}
	d.Intro(orDefault(o.Name, "agentgopher"))

	result, err := orchestrator.Orchestrate(ctx, orchestrator.Options{
		HostRepoDir:         s.hostRepoDir,
		Iterations:          maxIterations,
		Prompt:              text,
		Branch:              s.lifecycleBranch(),
		Provider:            o.Agent,
		Sandbox:             s.provider,
		CompletionSignals:   o.CompletionSignal,
		IdleTimeout:         o.IdleTimeout,
		CompletionTimeout:   o.CompletionTimeout,
		Name:                o.Name,
		ResumeSession:       o.ResumeSession,
		ForkSession:         o.forkSession,
		SkipPromptExpansion: inline,
		Timeouts:            s.timeouts,
		Display:             d,
		OnStreamEvent:       buildStreamHandler(logging, out),
		Existing:            s.existing(),
	})
	if err != nil {
		if ctx.Err() != nil {
			return SandboxRunResult{}, ctx.Err()
		}
		return SandboxRunResult{}, logError(d, err)
	}
	if result.CompletionSignal != "" {
		d.Status(fmt.Sprintf("Run complete: agent finished after %d iteration(s).", len(result.Iterations)), display.SeveritySuccess)
	} else {
		d.Status(fmt.Sprintf("Run complete: reached %d iteration(s) without completion signal.", len(result.Iterations)), display.SeverityWarn)
	}
	return SandboxRunResult{Iterations: result.Iterations, CompletionSignal: result.CompletionSignal, Stdout: result.Stdout, Commits: toCommits(result.Commits), LogFilePath: logFilePath, sb: s, opts: o}, nil
}

func (r SandboxRunResult) continueRun(ctx context.Context, p string, o *ResumeOptions, fork bool) (SandboxRunResult, error) {
	if r.sb == nil || len(r.Iterations) == 0 || r.Iterations[len(r.Iterations)-1].SessionID == "" {
		return SandboxRunResult{}, errors.New("cannot resume: no session id was captured")
	}
	next := r.opts
	next.Prompt, next.PromptFile, next.PromptArgs = p, "", nil
	next.MaxIterations = 1
	next.ResumeSession = r.Iterations[len(r.Iterations)-1].SessionID
	next.forkSession = fork
	if o != nil {
		if o.PromptArgs != nil {
			next.PromptArgs = o.PromptArgs
		}
		if o.Logging != nil {
			next.Logging = o.Logging
		}
		if o.CompletionSignal != nil {
			next.CompletionSignal = o.CompletionSignal
		}
		if o.IdleTimeout != 0 {
			next.IdleTimeout = o.IdleTimeout
		}
		if o.CompletionTimeout != 0 {
			next.CompletionTimeout = o.CompletionTimeout
		}
		if o.Name != "" {
			next.Name = o.Name
		}
	}
	return r.sb.Run(ctx, next)
}

// Resume continues the last captured session for one iteration in the same sandbox.
func (r SandboxRunResult) Resume(ctx context.Context, p string, o *ResumeOptions) (SandboxRunResult, error) {
	return r.continueRun(ctx, p, o, false)
}

// Fork continues the last captured session as a new session (ADR 0018).
func (r SandboxRunResult) Fork(ctx context.Context, p string, o *ResumeOptions) (SandboxRunResult, error) {
	return r.continueRun(ctx, p, o, true)
}

// Interactive launches the agent's TUI inside the existing sandbox.
func (s *Sandbox) Interactive(ctx context.Context, o SandboxInteractiveOptions) (SandboxInteractiveResult, error) {
	if err := ctx.Err(); err != nil {
		return SandboxInteractiveResult{}, err
	}
	builder, ok := o.Agent.(agent.InteractiveArgsBuilder)
	if !ok {
		return SandboxInteractiveResult{}, fmt.Errorf("agent provider %q does not support interactive sessions", o.Agent.Name())
	}
	ix, ok := s.handle.(sandbox.Interactive)
	if !ok {
		return SandboxInteractiveResult{}, fmt.Errorf("sandbox provider %q does not support interactive sessions", s.provider.Name())
	}
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	d := display.NewTerminal(o.Stdout)
	text, inline, err := s.resolvePrompt(ctx, o.Prompt, o.PromptFile, o.PromptArgs, func(m string) { d.Status(m, display.SeverityWarn) })
	if err != nil {
		return SandboxInteractiveResult{}, err
	}
	lc, err := lifecycle.Run(ctx, lifecycle.Options{
		HostRepoDir: s.hostRepoDir, SandboxRepoDir: s.repoDir, HostWorktreePath: s.WorktreePath,
		Branch: s.lifecycleBranch(), Timeouts: s.timeouts, Display: d,
		ApplyToHost: s.applyToHost, KeepSourceBranch: s.strategy.Type == sandbox.BranchMergeToHead,
	}, s.handle, func(lctx lifecycle.Context) (int, error) {
		full := text
		if !inline {
			var err error
			full, err = prompt.Expand(ctx, text, lctx.Sandbox, lctx.SandboxRepoDir, nil)
			if err != nil {
				return 0, err
			}
		}
		args := builder.BuildInteractiveArgs(agent.CommandOptions{Prompt: full, DangerouslySkipPermissions: true})
		return ix.InteractiveExec(ctx, args, sandbox.InteractiveExecOptions{Stdin: o.Stdin, Stdout: o.Stdout, Stderr: o.Stderr, Cwd: s.repoDir})
	})
	if err != nil {
		return SandboxInteractiveResult{}, err
	}
	return SandboxInteractiveResult{Commits: toCommits(lc.Commits), ExitCode: lc.Value}, nil
}

// Exec runs a command inside the sandbox. Cwd defaults to the repo path.
// A non-zero exit code is returned in the result, not as an error.
func (s *Sandbox) Exec(ctx context.Context, command string, o SandboxExecOptions) (sandbox.ExecResult, error) {
	if o.Cwd == "" {
		o.Cwd = s.repoDir
	}
	return s.handle.Exec(ctx, command, o)
}

// Close tears the sandbox down. For CreateSandbox it also removes the
// worktree unless it has uncommitted changes. Safe to call twice.
func (s *Sandbox) Close(ctx context.Context) (CloseResult, error) {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return CloseResult{}, nil
	}
	s.closed = true
	return s.closeFn(ctx)
}
```

- [ ] **Step 5: Add the worktree methods to worktree.go**

```go
// WorktreeCreateSandboxOptions configure (*Worktree).CreateSandbox.
type WorktreeCreateSandboxOptions struct {
	Sandbox        sandbox.Provider
	Hooks          Hooks
	CopyToWorktree []string
	Timeouts       Timeouts
}

// CreateSandbox starts a sandbox on this worktree. Close tears down the
// sandbox only; the worktree belongs to the Worktree handle.
func (w *Worktree) CreateSandbox(ctx context.Context, o WorktreeCreateSandboxOptions) (*Sandbox, error) {
	started, err := startOnWorktree(ctx, o.Sandbox, w.hostRepoDir, w.Path, o.Hooks, o.CopyToWorktree, o.Timeouts)
	if err != nil {
		return nil, err
	}
	sb := newSandbox(w.hostRepoDir, w.Path, w.Branch, w.strategy, started, o.Sandbox, o.Timeouts)
	sb.closeFn = func(ctx context.Context) (CloseResult, error) {
		return CloseResult{}, started.Handle.Close(ctx)
	}
	return sb, nil
}

// WorktreeInteractiveOptions configure (*Worktree).Interactive.
type WorktreeInteractiveOptions struct {
	Agent          agent.Provider
	Sandbox        sandbox.Provider // nil means nosandbox.New(nil)
	Prompt         string
	PromptFile     string
	PromptArgs     map[string]string
	Name           string
	Hooks          Hooks
	CopyToWorktree []string
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
}

// Interactive runs an interactive session on this worktree.
func (w *Worktree) Interactive(ctx context.Context, o WorktreeInteractiveOptions) (InteractiveResult, error) {
	if o.Sandbox == nil {
		o.Sandbox = nosandbox.New(nil)
	}
	sb, err := w.CreateSandbox(ctx, WorktreeCreateSandboxOptions{Sandbox: o.Sandbox, Hooks: o.Hooks, CopyToWorktree: o.CopyToWorktree})
	if err != nil {
		return InteractiveResult{}, err
	}
	defer sb.Close(ctx)
	r, err := sb.Interactive(ctx, SandboxInteractiveOptions{Agent: o.Agent, Prompt: o.Prompt, PromptFile: o.PromptFile, PromptArgs: o.PromptArgs, Name: o.Name, Stdin: o.Stdin, Stdout: o.Stdout, Stderr: o.Stderr})
	if err != nil {
		return InteractiveResult{}, err
	}
	return InteractiveResult{Commits: r.Commits, Branch: w.Branch, ExitCode: r.ExitCode}, nil
}
```
`w.hostRepoDir`, `w.Path`, `w.Branch` and `w.strategy` are the phase 3 `Worktree` fields; adapt to the actual names.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 7: Lint, update README and commit**

Run: `go vet ./... && golangci-lint run ./...`
Expected: no output.

Replace the "Status" paragraph in `README.md` with:
```markdown
Status: phases 1 to 5 of `docs/plan.md` are done. `Run`, `CreateSandbox`,
`CreateWorktree` and `Interactive` work with the no-sandbox, Docker, Podman,
Vercel and Daytona providers; Claude Code, Codex and Pi sessions can be
resumed and forked; structured output is extracted with `OutputObject` and
`OutputString`. The CLI and `init` follow in phase 6.
```

```bash
git add createsandbox.go createsandbox_test.go worktree.go internal/orchestrator/ README.md
git commit -m "Add CreateSandbox and worktree-backed sandboxes"
```

---

## Self-review notes

- Spec coverage for phase 5: `CreateSandbox` (Task 12), `Interactive` (Tasks 10 and 11), structured output (Task 7), resume and fork with session store (Tasks 1, 2, 6), remaining agents codex, copilot, cursor, opencode, pi (Tasks 3, 4, 5), terminal mode with spinners (Task 9), shutdown registry and terminal cleanup (Task 8).
- Deliberately not ported: `ErrorHandler.ts` (`withFriendlyErrors` exits the process; that is CLI behaviour for the phase 6 plan), `RecoveryMessage.ts` (belongs to sync-out, phase 4), `raceAbortSignal.ts` (replaced by `context.Context` everywhere), the `_test.buildSandbox` hooks on `CreateSandboxOptions` (Go tests use the real no-sandbox provider), `Symbol.asyncDispose` (no Go equivalent; `defer sb.Close(ctx)`), the `SilentDisplay` layer (Go passes `display.NewWriter(io.Discard)`), and `resumeSession` on `interactive()` (upstream does not support it either).
- Behaviour differences recorded in code comments: `(*Sandbox).Run` in stdout logging mode writes plain lines instead of staying silent; the Pi provider ignores `ForkSession` because pi has no fork verb, as upstream does.
- Type consistency: `agent.SessionTransfer.Handle` is `sandbox.BindMountHandle` everywhere; `IterationResult.SessionFilePath` is set only by the orchestrator capture path; `ResumeOptions` is shared by `RunResult` and `SandboxRunResult`; `Commit`, `Hooks`, `Timeouts` and `Logging` are the phase 1 types.
- Review Focus mapping: 1 torn line (Task 1 `TestTransferClaudeRewritesCwdAndKeepsTornLine`), 2 resume without session (Task 6 `TestRunResumeAndForkRequireCapturedSession`), 3 last tag wins (Task 7 `TestOutputObjectLastMatchWinsAndUnwrapsFence`), 4 empty answer re-asked (Task 11 `TestCollectMissingArgsAsksAndRejectsEmpty`), 5 SIGINT with two sandboxes (Task 8 `TestRegisterFanOutRunsEachTeardownOnceAndExits`, three teardowns).
