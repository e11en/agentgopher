# agentgopher Phase 3: Worktrees and Bind-Mount Providers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `Run` supports the merge-to-head and named-branch strategies through git worktrees, copies host paths into the worktree, and runs the agent inside Docker or Podman containers that bind-mount the worktree; `CreateWorktree` gives callers a long-lived worktree to run agents in.

**Architecture:** Worktree management (create, reuse, lock, prune, copy) lives in `internal/worktree`. Mount resolution, including the Windows gitdir patch, lives in `internal/mount`. Docker and Podman share one CLI-driven handle in `internal/container`; the public `sandbox/docker` and `sandbox/podman` packages add container start, image checks and options. The orchestrator gains a per-iteration "start" step that picks head or worktree mode and no-sandbox or bind-mount kind, and the lifecycle gains the merge-to-head path. The phase 2 public API keeps working unchanged.

**Tech Stack:** Go 1.25, standard library only. `docker` and `podman` binaries on PATH at runtime; tests that need a daemon skip when `docker info` or `podman info` fails.

**Spec:** `docs/plan.md` (phase 3 and the feature inventory rows for worktrees, branch strategies, Docker, Podman, Windows mounts). Upstream reference: `mattpocock/sandcastle` at commit `7a4e7f7`, files `src/WorktreeManager.ts`, `src/createWorktree.ts`, `src/CopyToWorktree.ts`, `src/SandboxFactory.ts`, `src/startSandbox.ts`, `src/SandboxLifecycle.ts`, `src/DockerLifecycle.ts`, `src/PodmanLifecycle.ts`, `src/sandboxes/docker.ts`, `src/sandboxes/podman.ts`, `src/mountUtils.ts`, `src/MountConfig.ts`, `src/shutdownRegistry.ts`, and ADRs 0003, 0006, 0007, 0014. Clone it to read the original when a task is unclear: `git clone --depth 1 https://github.com/mattpocock/sandcastle.git /tmp/sandcastle && git -C /tmp/sandcastle checkout 7a4e7f7`.

## Global Constraints

- Module path `github.com/e11en/agentgopher`, `go 1.25`. Standard library only.
- Config directory `.agentgopher/`: worktrees in `.agentgopher/worktrees/<name>/`, locks in `.agentgopher/locks/<name>.lock`, logs in `.agentgopher/logs/`.
- Temp branches are named `agentgopher/<YYYYMMDD-HHMMSS>-<6 hex>` or `agentgopher/<sanitized name>/<YYYYMMDD-HHMMSS>-<6 hex>`.
- The worktree is mounted at `/home/agent/workspace` inside a container (`mount.SandboxRepoDir`); the sandbox home directory is `/home/agent`.
- Container names are `agentgopher-<32 hex>`; the default image name is `agentgopher:<repo dir name>`.
- Every `git` call made by `internal/worktree` runs with `LC_ALL=C` and `-c branch.autoSetupMerge=false -c push.autoSetupRemote=false`.
- Tests must not require Docker, Podman, Claude Code or network, except the tests marked "needs daemon", which call `t.Skip` when the daemon is unreachable. Windows-only behaviour is tested through a `goos` parameter, never through `runtime.GOOS`.
- Code comments, test names and commit messages in English. No em or en dashes. No attribution footers in commits.
- Public packages: root, `agent`, `sandbox`, `sandbox/nosandbox`, `sandbox/docker`, `sandbox/podman`. Everything else under `internal/`.
- `interactive`, `CreateSandbox`, isolated providers and session resume are out of scope (phases 4 and 5).

## Review Focus

1. A named branch that is checked out in the main working tree: `Create` fails with a clear message instead of a confusing git error. Test added in Task 2.
2. A second `Run` on the same named branch while the first is still running: the second fails fast with `LockedError` naming the PID, and a lock left behind by a dead PID is reclaimed. Test added in Task 3.
3. `copyToWorktree` paths that do not exist on the host are skipped silently; a path that cannot be copied fails with `CopyError` carrying stderr. Test added in Task 4.
4. A merge-to-head run whose host branch moved while the agent worked: the merge is a real merge, not a fast-forward, and on conflict the temp branch is preserved and the error tells the user how to finish. Test added in Task 11.
5. A worktree-mode run whose agent leaves uncommitted changes: the worktree is kept, its path is reported on `RunResult.PreservedWorktreePath` (success) or on `AgentError.PreservedWorktreePath` (failure), and the next run on a different branch is unaffected. Test added in Task 12.

---

## File structure

```
sandbox/sandbox.go                      ADD: Mount, MountConfig, BindMountCreateOptions, BindMountHandle, BindMountProvider, NewBindMountProvider
sandbox/docker/docker.go                Docker provider: New, Options, BuildImage, RemoveImage
sandbox/docker/docker_test.go
sandbox/podman/podman.go                Podman provider: New, Options, BuildImage, RemoveImage
sandbox/podman/podman_test.go
sandbox/nosandbox/nosandbox.go          MODIFY: use stream.LineWriter
internal/stream/linewriter.go           LineWriter moved out of nosandbox
internal/testrepo/testrepo.go           Init(t) shared git fixture (replaces gitx_test.go's InitTestRepo)
internal/worktree/worktree.go           names, git exec, list, collision, create, remove, dirty check
internal/worktree/reuse.go              fast-forward from origin on reuse (ADR 0003)
internal/worktree/prune.go              prune stale metadata, orphaned dirs and locks
internal/worktree/lock.go               file lock per worktree (ADR 0007)
internal/worktree/lock_unix.go, lock_windows.go   process liveness
internal/worktree/copy.go               copy host paths into the worktree
internal/worktree/*_test.go
internal/mount/mount.go                 image name, tilde, host/sandbox path, user mounts, volume format, file-mount parents, git mounts
internal/mount/windows.go               NormalizeMounts, ParseGitdirPath, PatchGitMountsForWindows (ADR 0006)
internal/mount/*_test.go
internal/shutdown/shutdown.go           one SIGINT/SIGTERM listener fanning out to teardowns
internal/shutdown/shutdown_test.go
internal/container/container.go         exec, cp, rm through a container CLI; shared by docker and podman
internal/container/container_test.go
internal/lifecycle/lifecycle.go         MODIFY: merge-to-head path, ApplyToHost, KeepSourceBranch, new timeouts
internal/orchestrator/start.go          per-iteration sandbox start: head or worktree mode, none or bind-mount kind
internal/orchestrator/orchestrator.go   MODIFY: Sandbox is sandbox.Provider, BranchStrategy, CopyToWorktree, Worktree reuse, preserved path
agentgopher.go                          MODIFY: BranchStrategy, CopyToWorktree, PreservedWorktreePath, Sandbox is sandbox.Provider
worktree.go                             CreateWorktree, Worktree.Run, Worktree.Close
errors.go                               MODIFY: new aliases
examples/docker/main.go                 merge-to-head run in Docker
```

---

### Task 1: Shared test fixture and LineWriter

Two small moves the rest of the phase builds on. The phase 1+2 plan was corrected after this plan was written and already creates `internal/testrepo.Init`; if it exists, skip the testrepo part of this task and only move `lineWriter`. Otherwise `InitTestRepo` becomes `internal/testrepo.Init`. `lineWriter` moves from `nosandbox` to `internal/stream` so the container handle can reuse it.

**Files:**
- Create: `internal/testrepo/testrepo.go`, `internal/stream/linewriter.go`, `internal/stream/linewriter_test.go`
- Modify: `internal/gitx/gitx_test.go` (delete `InitTestRepo`, call `testrepo.Init`), `internal/lifecycle/lifecycle_test.go`, `internal/orchestrator/orchestrator_test.go`, `agentgopher_test.go` (replace `gitx.InitTestRepo(t)` with `testrepo.Init(t)`), `sandbox/nosandbox/nosandbox.go` (use `stream.LineWriter`)

**Interfaces:**
- Produces: `testrepo.Init(t testing.TB) string` (repo with one commit on `main`, user configured), `testrepo.Commit(t testing.TB, dir, file, content, message string)` (writes a file and commits it), `stream.NewLineWriter(onLine func(string)) *LineWriter` with `Write([]byte) (int, error)` and `Flush()`.

- [ ] **Step 1: Write the failing LineWriter test**

`internal/stream/linewriter_test.go`:
```go
package stream

import "testing"

func TestLineWriterSplitsAcrossWrites(t *testing.T) {
	var lines []string
	w := NewLineWriter(func(l string) { lines = append(lines, l) })
	w.Write([]byte("ab"))
	w.Write([]byte("c\nde\nf"))
	if len(lines) != 2 || lines[0] != "abc" || lines[1] != "de" {
		t.Fatalf("lines = %q", lines)
	}
	w.Flush()
	if len(lines) != 3 || lines[2] != "f" {
		t.Fatalf("after flush: %q", lines)
	}
	w.Flush()
	if len(lines) != 3 {
		t.Fatal("second flush must not emit")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/stream/ -run LineWriter`
Expected: FAIL, "undefined: NewLineWriter".

- [ ] **Step 3: Write linewriter.go and testrepo.go**

`internal/stream/linewriter.go`:
```go
package stream

import "bytes"

// LineWriter splits a byte stream into lines and hands each complete line to
// onLine as soon as it arrives.
type LineWriter struct {
	onLine func(string)
	buf    []byte
}

// NewLineWriter returns a LineWriter that calls onLine per line, without the newline.
func NewLineWriter(onLine func(string)) *LineWriter { return &LineWriter{onLine: onLine} }

func (w *LineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		w.onLine(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
}

// Flush emits a trailing partial line, if any.
func (w *LineWriter) Flush() {
	if len(w.buf) > 0 {
		w.onLine(string(w.buf))
		w.buf = nil
	}
}
```

`internal/testrepo/testrepo.go`:
```go
// Package testrepo builds throwaway git repositories for tests.
package testrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Git runs git in dir and fails the test on error.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// Init creates a repo with one commit on branch main and returns its path.
func Init(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	Git(t, dir, "init", "-q", "-b", "main")
	Git(t, dir, "config", "user.name", "Test User")
	Git(t, dir, "config", "user.email", "test@example.com")
	Commit(t, dir, "README.md", "hi\n", "initial")
	return dir
}

// Commit writes file with content inside dir and commits it.
func Commit(t testing.TB, dir, file, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	Git(t, dir, "add", file)
	Git(t, dir, "commit", "-q", "-m", message)
}
```

- [ ] **Step 4: Replace the old helper and callers**

In `internal/gitx/gitx_test.go` delete `InitTestRepo` and its `os/exec` and `path/filepath` imports if unused; replace every `InitTestRepo(t)` with `testrepo.Init(t)` and import `"github.com/e11en/agentgopher/internal/testrepo"`. Do the same for `gitx.InitTestRepo(t)` in `internal/lifecycle/lifecycle_test.go`, `internal/orchestrator/orchestrator_test.go` and `agentgopher_test.go`.

In `sandbox/nosandbox/nosandbox.go` delete the `lineWriter` type and its methods, replace `lw := &lineWriter{onLine: ...}` with `lw := stream.NewLineWriter(func(line string) { ... })` and `lw.flush()` with `lw.Flush()`; drop the now unused `bytes` import if `result()` no longer needs it (it still uses `bytes.Buffer` for stderr, so keep it).

- [ ] **Step 5: Run all tests**

Run: `go test -race ./...`
Expected: PASS for every package.

- [ ] **Step 6: Commit**

```bash
git add internal/stream/ internal/testrepo/ internal/gitx/ internal/lifecycle/ internal/orchestrator/ agentgopher_test.go sandbox/nosandbox/
git commit -m "Share the git test fixture and the line writer across packages"
```

---

### Task 2: Worktree create, remove and dirty check

Port of `WorktreeManager.ts` minus the origin refresh (Task 5), prune (Task 6) and lock (Task 3).

**Files:**
- Create: `internal/worktree/worktree.go`, `internal/worktree/worktree_test.go`

**Interfaces:**
- Produces:
  ```go
  const Dir = ".agentgopher/worktrees"
  const Timeout = 30 * time.Second
  type Error struct{ Message string }                      // Error() returns Message
  type TimeoutError struct { Path, Operation string; Timeout time.Duration }
  type Info struct { Path, Branch string; Unlock func() } // Unlock is a no-op until Task 3
  type Entry struct { Path, Branch string }               // Branch is "" for a detached HEAD
  func SanitizeName(name string) string
  func GenerateTempBranchName(name string) string
  func WorktreeName(branch string) string                // "/" replaced by "-"
  func ListWorktrees(ctx context.Context, repoDir string) ([]Entry, error)
  func FindCollidingWorktree(entries []Entry, branch, worktreePath string) (Entry, bool)
  func IsManagedWorktreePath(worktreePath, worktreesDir string) bool
  func HasUncommittedChanges(ctx context.Context, worktreePath string) (bool, error)
  func Remove(ctx context.Context, worktreePath string) error
  // Create checks out branch in <repoDir>/.agentgopher/worktrees/<WorktreeName(branch)>.
  // A missing branch is created from baseBranch (default HEAD). An empty branch
  // creates a temp branch from GenerateTempBranchName(name).
  func Create(ctx context.Context, repoDir, branch, baseBranch string) (Info, error)
  func CreateTemp(ctx context.Context, repoDir, name string) (Info, error)
  ```
- Internal: `gitRun(ctx, dir string, args ...string) (string, error)` adds `LC_ALL=C` and the no-config-lock flags, returns `*Error` with trimmed stderr on failure.

- [ ] **Step 1: Write the failing tests**

`internal/worktree/worktree_test.go`:
```go
package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/testrepo"
)

var ctx = context.Background()

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{"My Agent": "my-agent", "a.b/c": "a-b-c", "worker": "worker"}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Errorf("SanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGenerateTempBranchName(t *testing.T) {
	plain := regexp.MustCompile(`^agentgopher/\d{8}-\d{6}-[0-9a-f]{6}$`)
	named := regexp.MustCompile(`^agentgopher/my-worker/\d{8}-\d{6}-[0-9a-f]{6}$`)
	if n := GenerateTempBranchName(""); !plain.MatchString(n) {
		t.Fatalf("got %q", n)
	}
	if n := GenerateTempBranchName("My Worker"); !named.MatchString(n) {
		t.Fatalf("got %q", n)
	}
	if GenerateTempBranchName("") == GenerateTempBranchName("") {
		t.Fatal("two names in the same second must differ")
	}
}

func TestCreateTempWorktree(t *testing.T) {
	repo := testrepo.Init(t)
	info, err := CreateTemp(ctx, repo, "worker")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(info.Path, filepath.Join(repo, ".agentgopher", "worktrees", "agentgopher-worker-")) {
		t.Fatalf("Path = %q", info.Path)
	}
	if got := strings.TrimSpace(testrepo.Git(t, info.Path, "rev-parse", "--abbrev-ref", "HEAD")); got != info.Branch {
		t.Fatalf("worktree on %q, want %q", got, info.Branch)
	}
}

func TestCreateNamedBranchNewAndExisting(t *testing.T) {
	repo := testrepo.Init(t)
	info, err := Create(ctx, repo, "feat/x", "")
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != filepath.Join(repo, ".agentgopher", "worktrees", "feat-x") || info.Branch != "feat/x" {
		t.Fatalf("info = %+v", info)
	}
	// Existing branch in another repo state: remove worktree, create again reuses the branch.
	if err := Remove(ctx, info.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(ctx, repo, "feat/x", ""); err != nil {
		t.Fatal(err)
	}
}

func TestCreateFromBaseBranchOnlyWhenNew(t *testing.T) {
	repo := testrepo.Init(t)
	testrepo.Git(t, repo, "branch", "base")
	testrepo.Commit(t, repo, "later.txt", "x", "later on main")
	info, err := Create(ctx, repo, "from-base", "base")
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(info.Path, "later.txt")); statErr == nil {
		t.Fatal("branch must start from base, not main")
	}
	Remove(ctx, info.Path)
	testrepo.Git(t, repo, "branch", "-f", "from-base", "main")
	info, err = Create(ctx, repo, "from-base", "base")
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(info.Path, "later.txt")); statErr != nil {
		t.Fatal("existing branch must be used as-is, baseBranch ignored")
	}
}

func TestCreateCollisionWithMainWorkingTree(t *testing.T) {
	repo := testrepo.Init(t)
	_, err := Create(ctx, repo, "main", "")
	var werr *Error
	if !errors.As(err, &werr) || !strings.Contains(werr.Message, "already checked out") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateReusesManagedWorktree(t *testing.T) {
	repo := testrepo.Init(t)
	first, err := Create(ctx, repo, "reuse-me", "")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(first.Path, "dirty.txt"), []byte("x"), 0o644)
	second, err := Create(ctx, repo, "reuse-me", "")
	if err != nil {
		t.Fatal(err)
	}
	if second.Path != first.Path {
		t.Fatalf("got %q, want %q", second.Path, first.Path)
	}
}

func TestHasUncommittedChanges(t *testing.T) {
	repo := testrepo.Init(t)
	if dirty, _ := HasUncommittedChanges(ctx, repo); dirty {
		t.Fatal("fresh repo must be clean")
	}
	os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x"), 0o644)
	if dirty, _ := HasUncommittedChanges(ctx, repo); !dirty {
		t.Fatal("untracked file must count as dirty")
	}
}

func TestRemoveDeletesDirectoryAndMetadata(t *testing.T) {
	repo := testrepo.Init(t)
	info, _ := CreateTemp(ctx, repo, "")
	if err := Remove(ctx, info.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(info.Path); err == nil {
		t.Fatal("directory still exists")
	}
	if strings.Contains(testrepo.Git(t, repo, "worktree", "list", "--porcelain"), info.Path) {
		t.Fatal("metadata still lists the worktree")
	}
}

func TestListWorktreesParsesDetachedHead(t *testing.T) {
	repo := testrepo.Init(t)
	info, _ := CreateTemp(ctx, repo, "")
	testrepo.Git(t, info.Path, "checkout", "-q", "--detach")
	entries, err := ListWorktrees(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range entries {
		if filepath.Clean(e.Path) == filepath.Clean(info.Path) {
			found = true
			if e.Branch != "" {
				t.Fatalf("detached HEAD must have empty Branch, got %q", e.Branch)
			}
		}
	}
	if !found {
		t.Fatalf("entries = %+v", entries)
	}
	if _, ok := FindCollidingWorktree(entries, "nope", info.Path); !ok {
		t.Fatal("path fallback must match a detached worktree")
	}
}

func TestNoUpstreamTrackingConfigWritten(t *testing.T) {
	repo := testrepo.Init(t)
	testrepo.Git(t, repo, "config", "branch.autoSetupMerge", "always")
	testrepo.Git(t, repo, "remote", "add", "origin", repo)
	if _, err := Create(ctx, repo, "tracked", ""); err != nil {
		t.Fatal(err)
	}
	cfg, _ := os.ReadFile(filepath.Join(repo, ".git", "config"))
	if strings.Contains(string(cfg), `[branch "tracked"]`) {
		t.Fatal("worktree add must not write upstream tracking config")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/worktree/`
Expected: FAIL, "undefined: SanitizeName".

- [ ] **Step 3: Write worktree.go**

```go
// Package worktree manages the git worktrees agentgopher runs agents in.
package worktree

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Dir is the worktree directory relative to the host repo.
const Dir = ".agentgopher/worktrees"

// Timeout bounds worktree create and prune.
const Timeout = 30 * time.Second

// Error reports a failed worktree operation.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

// TimeoutError reports a worktree operation that ran longer than Timeout.
type TimeoutError struct {
	Path      string
	Operation string
	Timeout   time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("worktree %s timed out after %v", e.Operation, e.Timeout)
}

// Info describes a worktree handed out by Create. Unlock releases the lock
// taken on it (a no-op when no lock was taken).
type Info struct {
	Path   string
	Branch string
	Unlock func()
}

// Entry is one line group of `git worktree list --porcelain`.
type Entry struct {
	Path   string
	Branch string
}

// noConfigLockFlags stop `git worktree add -b` from writing upstream tracking
// config, which races with other processes holding .git/config.lock.
var noConfigLockFlags = []string{"-c", "branch.autoSetupMerge=false", "-c", "push.autoSetupRemote=false"}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]`)

// SanitizeName lowercases name and replaces every non-alphanumeric run with a dash.
func SanitizeName(name string) string {
	return nonAlnum.ReplaceAllString(strings.ToLower(name), "-")
}

func timestamp() string { return time.Now().Format("20060102-150405") }

func randomSuffix() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// GenerateTempBranchName returns agentgopher/[<name>/]<timestamp>-<random>.
func GenerateTempBranchName(name string) string {
	if name != "" {
		return "agentgopher/" + SanitizeName(name) + "/" + timestamp() + "-" + randomSuffix()
	}
	return "agentgopher/" + timestamp() + "-" + randomSuffix()
}

// WorktreeName is the directory name used for branch.
func WorktreeName(branch string) string { return strings.ReplaceAll(branch, "/", "-") }

// gitRun runs git in dir with the C locale so stderr matching is stable.
func gitRun(ctx context.Context, dir string, args ...string) (string, error) {
	full := append(append([]string{}, noConfigLockFlags...), args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", &Error{Message: msg}
	}
	return stdout.String(), nil
}

func toSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// ListWorktrees parses `git worktree list --porcelain`.
func ListWorktrees(ctx context.Context, repoDir string) ([]Entry, error) {
	out, err := gitRun(ctx, repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var entries []Entry
	var cur *Entry
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			entries = append(entries, Entry{Path: strings.TrimSpace(strings.TrimPrefix(line, "worktree "))})
			cur = &entries[len(entries)-1]
		case strings.HasPrefix(line, "branch refs/heads/") && cur != nil:
			cur.Branch = strings.TrimSpace(strings.TrimPrefix(line, "branch refs/heads/"))
		}
	}
	return entries, nil
}

// FindCollidingWorktree matches by branch first, then by path, which covers a
// detached HEAD mid-rebase.
func FindCollidingWorktree(entries []Entry, branch, worktreePath string) (Entry, bool) {
	for _, e := range entries {
		if e.Branch == branch {
			return e, true
		}
	}
	for _, e := range entries {
		if toSlash(e.Path) == toSlash(worktreePath) {
			return e, true
		}
	}
	return Entry{}, false
}

// IsManagedWorktreePath reports whether worktreePath lives under worktreesDir.
func IsManagedWorktreePath(worktreePath, worktreesDir string) bool {
	return strings.HasPrefix(toSlash(worktreePath), toSlash(worktreesDir))
}

// HasUncommittedChanges reports staged, unstaged or untracked changes.
func HasUncommittedChanges(ctx context.Context, worktreePath string) (bool, error) {
	out, err := gitRun(ctx, worktreePath, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// Remove deletes a managed worktree and its git metadata. worktreePath must be
// <repo>/.agentgopher/worktrees/<name>.
func Remove(ctx context.Context, worktreePath string) error {
	repoDir := filepath.Join(worktreePath, "..", "..", "..")
	_, err := gitRun(ctx, repoDir, "worktree", "remove", "--force", worktreePath)
	return err
}

// CreateTemp creates a worktree on a fresh temp branch.
func CreateTemp(ctx context.Context, repoDir, name string) (Info, error) {
	branch := GenerateTempBranchName(name)
	worktreeName := "agentgopher-" + strings.TrimPrefix(strings.ReplaceAll(branch, "/", "-"), "agentgopher-")
	return create(ctx, repoDir, branch, "HEAD", worktreeName, false)
}

// Create checks out branch in a managed worktree, creating the branch from
// baseBranch (default HEAD) when it does not exist. A clean managed worktree
// for the same branch is reused and refreshed from origin (ADR 0003); a dirty
// one is reused with a warning.
func Create(ctx context.Context, repoDir, branch, baseBranch string) (Info, error) {
	if branch == "" {
		return CreateTemp(ctx, repoDir, "")
	}
	if baseBranch == "" {
		baseBranch = "HEAD"
	}
	return create(ctx, repoDir, branch, baseBranch, WorktreeName(branch), true)
}

func create(ctx context.Context, repoDir, branch, baseBranch, worktreeName string, reusable bool) (Info, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	info, err := createLocked(ctx, repoDir, branch, baseBranch, worktreeName, reusable)
	if ctx.Err() == context.DeadlineExceeded {
		return Info{}, &TimeoutError{Path: repoDir, Operation: "create", Timeout: Timeout}
	}
	return info, err
}

func createLocked(ctx context.Context, repoDir, branch, baseBranch, worktreeName string, reusable bool) (Info, error) {
	worktreesDir := filepath.Join(repoDir, Dir)
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		return Info{}, &Error{Message: err.Error()}
	}
	worktreePath := filepath.Join(worktreesDir, worktreeName)
	noop := func() {}

	if reusable {
		entries, err := ListWorktrees(ctx, repoDir)
		if err != nil {
			return Info{}, err
		}
		if hit, ok := FindCollidingWorktree(entries, branch, worktreePath); ok {
			if !IsManagedWorktreePath(hit.Path, worktreesDir) {
				return Info{}, &Error{Message: fmt.Sprintf("Branch '%s' is already checked out in worktree at '%s'. agentgopher's branch and merge-to-head strategies run the agent in a git worktree under %s, and git refuses to check out the same branch in two worktrees at once. Pick a different branch, or switch the main working tree to a different branch before re-running.", branch, hit.Path, Dir)}
			}
			dirty, err := HasUncommittedChanges(ctx, hit.Path)
			if err != nil {
				return Info{}, err
			}
			if dirty {
				fmt.Fprintf(os.Stderr, "Reusing worktree at %s (branch '%s') - worktree has uncommitted changes\n", hit.Path, branch)
			} else {
				fastForwardFromOrigin(ctx, hit.Path, branch)
			}
			return Info{Path: filepath.Clean(hit.Path), Branch: branch, Unlock: noop}, nil
		}
		_, err = gitRun(ctx, repoDir, "worktree", "add", worktreePath, branch)
		if err != nil && strings.Contains(err.Error(), "invalid reference") {
			_, err = gitRun(ctx, repoDir, "worktree", "add", "-b", branch, worktreePath, baseBranch)
		}
		if err != nil {
			return Info{}, err
		}
		return Info{Path: worktreePath, Branch: branch, Unlock: noop}, nil
	}

	if _, err := gitRun(ctx, repoDir, "worktree", "add", "-b", branch, worktreePath, "HEAD"); err != nil {
		if strings.Contains(err.Error(), "already checked out") || strings.Contains(err.Error(), "already exists") {
			return Info{}, &Error{Message: fmt.Sprintf("Branch '%s' is already checked out in another worktree. Use a different branch name, or wait for the other run to finish.", branch)}
		}
		return Info{}, err
	}
	return Info{Path: worktreePath, Branch: branch, Unlock: noop}, nil
}
```

Add a stub for the refresh so the package compiles before Task 5; it is replaced there:
```go
// fastForwardFromOrigin is implemented in reuse.go (Task 5).
func fastForwardFromOrigin(ctx context.Context, worktreePath, branch string) {
	fmt.Fprintf(os.Stderr, "Reusing existing worktree at %s (branch '%s')\n", worktreePath, branch)
}
```
Put it in `internal/worktree/reuse.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/worktree/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worktree/
git commit -m "Add worktree creation, reuse and removal"
```

---

### Task 3: Worktree lock

Port of ADR 0007. One lock file per managed worktree; a live PID fails fast, a dead PID is reclaimed.

**Files:**
- Create: `internal/worktree/lock.go`, `internal/worktree/lock_unix.go`, `internal/worktree/lock_windows.go`, `internal/worktree/lock_test.go`
- Modify: `internal/worktree/worktree.go` (`createLocked` takes the lock before returning `Info` and stores the release in `Info.Unlock`)

**Interfaces:**
- Produces:
  ```go
  const LocksDir = ".agentgopher/locks"
  type LockedError struct { WorktreeName string; PID int }
  type lockFile struct { PID int `json:"pid"`; Branch string `json:"branch"`; AcquiredAt time.Time `json:"acquiredAt"` }
  func Acquire(repoDir, worktreeName, branch string) (release func(), err error)
  func processAlive(pid int) bool // per OS
  ```

- [ ] **Step 1: Write the failing tests**

`internal/worktree/lock_test.go`:
```go
package worktree

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/e11en/agentgopher/internal/testrepo"
)

func TestAcquireCreatesAndReleasesLock(t *testing.T) {
	repo := testrepo.Init(t)
	release, err := Acquire(repo, "feat-x", "feat/x")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, LocksDir, "feat-x.lock")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lf lockFile
	if json.Unmarshal(b, &lf) != nil || lf.PID != os.Getpid() || lf.Branch != "feat/x" {
		t.Fatalf("lock = %s", b)
	}
	release()
	release()
	if _, err := os.Stat(path); err == nil {
		t.Fatal("lock not removed")
	}
}

func TestAcquireFailsWhenHeldByLiveProcess(t *testing.T) {
	repo := testrepo.Init(t)
	release, _ := Acquire(repo, "feat-x", "feat/x")
	defer release()
	_, err := Acquire(repo, "feat-x", "feat/x")
	var locked *LockedError
	if !errors.As(err, &locked) || locked.PID != os.Getpid() {
		t.Fatalf("err = %v", err)
	}
}

func TestAcquireReclaimsStaleLock(t *testing.T) {
	repo := testrepo.Init(t)
	os.MkdirAll(filepath.Join(repo, LocksDir), 0o755)
	stale, _ := json.Marshal(lockFile{PID: 999999999, Branch: "x", AcquiredAt: time.Now()})
	os.WriteFile(filepath.Join(repo, LocksDir, "feat-x.lock"), stale, 0o644)
	release, err := Acquire(repo, "feat-x", "feat/x")
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestCreateTakesLockAndSecondCreateFails(t *testing.T) {
	repo := testrepo.Init(t)
	info, err := Create(ctx, repo, "locked-branch", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Create(ctx, repo, "locked-branch", "")
	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("err = %v", err)
	}
	info.Unlock()
	if _, err := Create(ctx, repo, "locked-branch", ""); err != nil {
		t.Fatalf("after unlock: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/worktree/ -run 'Lock|Acquire'`
Expected: FAIL, "undefined: Acquire".

- [ ] **Step 3: Write lock.go and the OS files**

`internal/worktree/lock.go`:
```go
package worktree

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LocksDir holds one lock file per managed worktree.
const LocksDir = ".agentgopher/locks"

// LockedError reports a worktree in use by another live process.
type LockedError struct {
	WorktreeName string
	PID          int
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("worktree '%s' is in use by process %d", e.WorktreeName, e.PID)
}

type lockFile struct {
	PID        int       `json:"pid"`
	Branch     string    `json:"branch"`
	AcquiredAt time.Time `json:"acquiredAt"`
}

// Acquire takes the lock for worktreeName. A lock held by a dead process is
// reclaimed; one held by a live process fails with LockedError. release is
// idempotent.
func Acquire(repoDir, worktreeName, branch string) (func(), error) {
	dir := filepath.Join(repoDir, LocksDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, &Error{Message: err.Error()}
	}
	path := filepath.Join(dir, worktreeName+".lock")
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			content, _ := json.Marshal(lockFile{PID: os.Getpid(), Branch: branch, AcquiredAt: time.Now().UTC()})
			_, werr := f.Write(content)
			f.Close()
			if werr != nil {
				os.Remove(path)
				return nil, &Error{Message: werr.Error()}
			}
			var once sync.Once
			return func() { once.Do(func() { os.Remove(path) }) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, &Error{Message: err.Error()}
		}
		existing, readErr := os.ReadFile(path)
		var lf lockFile
		if readErr == nil && json.Unmarshal(existing, &lf) == nil && lf.PID > 0 && processAlive(lf.PID) {
			return nil, &LockedError{WorktreeName: worktreeName, PID: lf.PID}
		}
		// Stale or unreadable lock: remove and retry once.
		os.Remove(path)
	}
	return nil, &Error{Message: "could not acquire worktree lock at " + path}
}
```

`internal/worktree/lock_unix.go`:
```go
//go:build !windows

package worktree

import (
	"os"
	"syscall"
)

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
```

`internal/worktree/lock_windows.go`:
```go
//go:build windows

package worktree

import "os"

// shortcut: FindProcess on Windows fails only for a missing PID, which is the
// stale case we need; a PID that was recycled by another program still counts as live.
func processAlive(pid int) bool {
	_, err := os.FindProcess(pid)
	return err == nil
}
```

- [ ] **Step 4: Take the lock in createLocked**

In `worktree.go`, at the start of `createLocked` after computing `worktreePath`, add:
```go
	unlock := noop
	if reusable {
		var err error
		if unlock, err = Acquire(repoDir, worktreeName, branch); err != nil {
			return Info{}, err
		}
	}
```
Replace every `Unlock: noop` in the function with `Unlock: unlock`, and call `unlock()` before each `return Info{}, err` that follows the acquisition. Temp worktrees stay unlocked: their names are unique per run.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/worktree/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/worktree/
git commit -m "Lock managed worktrees per ADR 0007"
```

---

### Task 4: Copy host paths into the worktree

Port of `CopyToWorktree.ts`: copy-on-write first (`cp -cR` on macOS, `cp -R --reflink=auto` elsewhere), plain `cp -R` as fallback, missing sources skipped.

**Files:**
- Create: `internal/worktree/copy.go`, `internal/worktree/copy_test.go`

**Interfaces:**
- Produces:
  ```go
  const DefaultCopyTimeout = 60 * time.Second
  type CopyError struct { Path, Stderr string; ExitCode int }
  type CopyTimeoutError struct { Paths []string; Timeout time.Duration }
  func CopyOnWriteFlags(goos string) []string
  func CopyToWorktree(ctx context.Context, paths []string, hostRepoDir, worktreePath string, timeout time.Duration) error // zero timeout means default
  ```

- [ ] **Step 1: Write the failing tests**

`internal/worktree/copy_test.go`:
```go
package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCopyOnWriteFlags(t *testing.T) {
	if got := CopyOnWriteFlags("darwin"); len(got) != 1 || got[0] != "-cR" {
		t.Fatalf("darwin: %v", got)
	}
	if got := CopyOnWriteFlags("linux"); len(got) != 2 || got[1] != "--reflink=auto" {
		t.Fatalf("linux: %v", got)
	}
}

func TestCopyToWorktreeCopiesAndSkipsMissing(t *testing.T) {
	host, wt := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(host, "node_modules", "pkg"), 0o755)
	os.WriteFile(filepath.Join(host, "node_modules", "pkg", "index.js"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(host, "single.txt"), []byte("y"), 0o644)
	err := CopyToWorktree(context.Background(), []string{"node_modules", "single.txt", "missing"}, host, wt, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"node_modules/pkg/index.js", "single.txt"} {
		if _, err := os.Stat(filepath.Join(wt, f)); err != nil {
			t.Fatalf("%s not copied", f)
		}
	}
}

func TestCopyToWorktreeFailsWithCopyError(t *testing.T) {
	host, wt := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(host, "a.txt"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(wt, "a.txt", "dir"), 0o755) // destination is a non-empty directory, cp fails
	err := CopyToWorktree(context.Background(), []string{"a.txt"}, host, wt, 0)
	var cerr *CopyError
	if !errors.As(err, &cerr) || cerr.Path != "a.txt" {
		t.Fatalf("err = %v", err)
	}
}

func TestCopyToWorktreeTimeout(t *testing.T) {
	host, wt := t.TempDir(), t.TempDir()
	big := filepath.Join(host, "big")
	os.MkdirAll(big, 0o755)
	for i := 0; i < 2000; i++ {
		os.WriteFile(filepath.Join(big, "f"+string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676))), make([]byte, 4096), 0o644)
	}
	err := CopyToWorktree(context.Background(), []string{"big"}, host, wt, time.Nanosecond)
	var terr *CopyTimeoutError
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/worktree/ -run Copy`
Expected: FAIL, "undefined: CopyOnWriteFlags".

- [ ] **Step 3: Write copy.go**

```go
package worktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultCopyTimeout bounds CopyToWorktree when no timeout is given.
const DefaultCopyTimeout = 60 * time.Second

// CopyError reports a path that could not be copied, after the plain cp fallback.
type CopyError struct {
	Path     string
	Stderr   string
	ExitCode int
}

func (e *CopyError) Error() string {
	return fmt.Sprintf("failed to copy %s to worktree: %s", e.Path, e.Stderr)
}

// CopyTimeoutError reports that copying ran longer than Timeout.
type CopyTimeoutError struct {
	Paths   []string
	Timeout time.Duration
}

func (e *CopyTimeoutError) Error() string {
	return fmt.Sprintf("copying files to worktree timed out after %v", e.Timeout)
}

// CopyOnWriteFlags returns the cp flags that clone instead of copy where the
// filesystem supports it: APFS clonefile on macOS, reflink elsewhere.
func CopyOnWriteFlags(goos string) []string {
	if goos == "darwin" {
		return []string{"-cR"}
	}
	return []string{"-R", "--reflink=auto"}
}

// CopyToWorktree copies each path (relative to hostRepoDir) to the same
// relative location under worktreePath. Missing sources are skipped.
func CopyToWorktree(ctx context.Context, paths []string, hostRepoDir, worktreePath string, timeout time.Duration) error {
	if timeout == 0 {
		timeout = DefaultCopyTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for _, rel := range paths {
		src := filepath.Join(hostRepoDir, rel)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dest := filepath.Join(worktreePath, rel)
		if err := cp(ctx, append(CopyOnWriteFlags(runtime.GOOS), src, dest)); err == nil {
			continue
		}
		if err := cp(ctx, []string{"-R", src, dest}); err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return &CopyTimeoutError{Paths: paths, Timeout: timeout}
			}
			var exitErr *exec.ExitError
			code := -1
			stderr := err.Error()
			if errors.As(err, &exitErr) {
				code = exitErr.ExitCode()
				stderr = strings.TrimSpace(string(exitErr.Stderr))
			}
			return &CopyError{Path: rel, Stderr: stderr, ExitCode: code}
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		return &CopyTimeoutError{Paths: paths, Timeout: timeout}
	}
	return nil
}

func cp(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, "cp", args...)
	_, err := cmd.Output() // Output captures stderr into ExitError.Stderr
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/worktree/ -run Copy`
Expected: PASS. If `TestCopyToWorktreeTimeout` does not time out on a fast disk, it is acceptable to replace the 2000-file tree with a `cp` wrapper script on PATH that sleeps; keep whichever version fails deterministically before the fix and passes after.

- [ ] **Step 5: Commit**

```bash
git add internal/worktree/copy.go internal/worktree/copy_test.go
git commit -m "Copy host paths into the worktree with copy-on-write"
```

---

### Task 5: Reuse refresh from origin

Port of `fastForwardFromOrigin` (ADR 0003): on clean reuse, fetch `origin/<branch>` and fast-forward; skip when HEAD is detached, the fetch fails or the branch diverged. Every outcome is logged, none is fatal.

**Files:**
- Modify: `internal/worktree/reuse.go` (replace the Task 2 stub), `internal/worktree/worktree_test.go` (append tests)

**Interfaces:**
- Produces: `fastForwardFromOrigin(ctx context.Context, worktreePath, branch string)` (unexported) and `var reuseLog = func(string)` that writes to stderr; tests swap it.

- [ ] **Step 1: Write the failing tests (append)**

```go
func cloneWithOrigin(t *testing.T) (origin, clone string) {
	t.Helper()
	origin = testrepo.Init(t)
	clone = t.TempDir()
	testrepo.Git(t, clone, "clone", "-q", origin, ".")
	testrepo.Git(t, clone, "config", "user.name", "Test User")
	testrepo.Git(t, clone, "config", "user.email", "test@example.com")
	return origin, clone
}

func captureReuseLog(t *testing.T) *[]string {
	t.Helper()
	var lines []string
	old := reuseLog
	reuseLog = func(s string) { lines = append(lines, s) }
	t.Cleanup(func() { reuseLog = old })
	return &lines
}

func TestReuseFastForwardsCleanWorktree(t *testing.T) {
	origin, clone := cloneWithOrigin(t)
	testrepo.Git(t, origin, "branch", "feat")
	info, err := Create(ctx, clone, "feat", "")
	if err != nil {
		t.Fatal(err)
	}
	info.Unlock()
	testrepo.Git(t, origin, "checkout", "-q", "feat")
	testrepo.Commit(t, origin, "new.txt", "x", "moved on origin")
	logs := captureReuseLog(t)
	info, err = Create(ctx, clone, "feat", "")
	if err != nil {
		t.Fatal(err)
	}
	defer info.Unlock()
	if _, err := os.Stat(filepath.Join(info.Path, "new.txt")); err != nil {
		t.Fatalf("not fast-forwarded; logs=%q", *logs)
	}
}

func TestReusePreservesDivergedBranch(t *testing.T) {
	origin, clone := cloneWithOrigin(t)
	testrepo.Git(t, origin, "branch", "feat")
	info, _ := Create(ctx, clone, "feat", "")
	info.Unlock()
	testrepo.Commit(t, info.Path, "local.txt", "x", "unpushed")
	testrepo.Git(t, origin, "checkout", "-q", "feat")
	testrepo.Commit(t, origin, "remote.txt", "y", "moved on origin")
	logs := captureReuseLog(t)
	info, err := Create(ctx, clone, "feat", "")
	if err != nil {
		t.Fatal(err)
	}
	defer info.Unlock()
	if _, err := os.Stat(filepath.Join(info.Path, "local.txt")); err != nil {
		t.Fatal("unpushed commit lost")
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "diverged") {
		t.Fatalf("logs = %q", *logs)
	}
}

func TestReuseWithoutOriginIsNonFatal(t *testing.T) {
	repo := testrepo.Init(t)
	info, _ := Create(ctx, repo, "feat", "")
	info.Unlock()
	logs := captureReuseLog(t)
	info, err := Create(ctx, repo, "feat", "")
	if err != nil {
		t.Fatal(err)
	}
	info.Unlock()
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "Could not fetch") {
		t.Fatalf("logs = %q", *logs)
	}
}

func TestReuseSkipsDetachedHead(t *testing.T) {
	origin, clone := cloneWithOrigin(t)
	testrepo.Git(t, origin, "branch", "feat")
	info, _ := Create(ctx, clone, "feat", "")
	info.Unlock()
	testrepo.Git(t, info.Path, "checkout", "-q", "--detach")
	logs := captureReuseLog(t)
	if _, err := Create(ctx, clone, "feat", ""); err != nil {
		t.Fatal(err)
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "HEAD is not on") {
		t.Fatalf("logs = %q", *logs)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/worktree/ -run Reuse`
Expected: FAIL: `TestReuseFastForwardsCleanWorktree` (not fast-forwarded) and compile error for `reuseLog`.

- [ ] **Step 3: Replace reuse.go**

```go
package worktree

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// reuseLog reports why a reused worktree was or was not refreshed.
var reuseLog = func(line string) { fmt.Fprintln(os.Stderr, line) }

// fastForwardFromOrigin refreshes a clean reused worktree from origin/<branch>
// when that is a fast-forward. Detached HEAD, a failed fetch and a diverged
// branch all skip the refresh; nothing here is fatal (ADR 0003).
func fastForwardFromOrigin(ctx context.Context, worktreePath, branch string) {
	head, err := gitRun(ctx, worktreePath, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || strings.TrimSpace(head) != "refs/heads/"+branch {
		reuseLog(fmt.Sprintf("Reusing worktree at %s (branch '%s') - HEAD is not on '%s', skipping origin refresh", worktreePath, branch, branch))
		return
	}
	if _, err := gitRun(ctx, worktreePath, "fetch", "origin", branch); err != nil {
		reuseLog(fmt.Sprintf("Could not fetch from origin (reusing worktree at %s as-is, branch '%s')", worktreePath, branch))
		return
	}
	before, _ := gitRun(ctx, worktreePath, "rev-parse", "HEAD")
	if _, err := gitRun(ctx, worktreePath, "merge", "--ff-only", "origin/"+branch); err != nil {
		reuseLog(fmt.Sprintf("Branch '%s' has diverged from origin (reusing worktree at %s as-is)", branch, worktreePath))
		return
	}
	after, _ := gitRun(ctx, worktreePath, "rev-parse", "HEAD")
	if strings.TrimSpace(before) != strings.TrimSpace(after) {
		reuseLog(fmt.Sprintf("Fast-forwarded worktree at %s (branch '%s') to origin/%s", worktreePath, branch, branch))
		return
	}
	reuseLog(fmt.Sprintf("Reusing existing worktree at %s (branch '%s')", worktreePath, branch))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/worktree/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worktree/
git commit -m "Fast-forward reused worktrees from origin when safe"
```

---

### Task 6: Prune stale worktrees and locks

Port of `pruneStale` plus the lock cleanup from ADR 0007.

**Files:**
- Create: `internal/worktree/prune.go`
- Modify: `internal/worktree/worktree_test.go` (append tests)

**Interfaces:**
- Produces: `func Prune(ctx context.Context, repoDir string) error`: runs `git worktree prune`, removes directories under `.agentgopher/worktrees/` that git no longer lists, removes lock files whose worktree directory is gone or whose PID is dead. Bounded by `Timeout` (`TimeoutError{Operation: "prune"}`).

- [ ] **Step 1: Write the failing tests (append)**

```go
func TestPruneRemovesOrphansKeepsActive(t *testing.T) {
	repo := testrepo.Init(t)
	active, _ := CreateTemp(ctx, repo, "keep")
	orphan := filepath.Join(repo, Dir, "orphan")
	os.MkdirAll(orphan, 0o755)
	os.WriteFile(filepath.Join(orphan, "junk"), []byte("x"), 0o644)
	if err := Prune(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Fatal("orphan not removed")
	}
	if _, err := os.Stat(active.Path); err != nil {
		t.Fatal("active worktree removed")
	}
}

func TestPruneRemovesStaleMetadataAndLocks(t *testing.T) {
	repo := testrepo.Init(t)
	info, _ := CreateTemp(ctx, repo, "")
	os.RemoveAll(info.Path)
	os.MkdirAll(filepath.Join(repo, LocksDir), 0o755)
	dead, _ := json.Marshal(lockFile{PID: 999999999, Branch: "x"})
	os.WriteFile(filepath.Join(repo, LocksDir, "dead.lock"), dead, 0o644)
	live, _ := json.Marshal(lockFile{PID: os.Getpid(), Branch: "y"})
	os.MkdirAll(filepath.Join(repo, Dir, "live"), 0o755)
	os.WriteFile(filepath.Join(repo, LocksDir, "live.lock"), live, 0o644)
	if err := Prune(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(testrepo.Git(t, repo, "worktree", "list", "--porcelain"), info.Path) {
		t.Fatal("stale metadata not pruned")
	}
	if _, err := os.Stat(filepath.Join(repo, LocksDir, "dead.lock")); err == nil {
		t.Fatal("dead lock not removed")
	}
}

func TestPruneWhenWorktreesDirIsSymlink(t *testing.T) {
	repo := testrepo.Init(t)
	real := t.TempDir()
	os.MkdirAll(filepath.Join(repo, ".agentgopher"), 0o755)
	if err := os.Symlink(real, filepath.Join(repo, Dir)); err != nil {
		t.Skip("symlinks unsupported")
	}
	active, err := CreateTemp(ctx, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := Prune(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(active.Path); err != nil {
		t.Fatal("active worktree behind symlink removed")
	}
}
```

Add `"encoding/json"` to the test imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/worktree/ -run Prune`
Expected: FAIL, "undefined: Prune".

- [ ] **Step 3: Write prune.go**

```go
package worktree

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Prune removes stale git worktree metadata, orphaned directories under
// .agentgopher/worktrees/ and lock files that are dead or have no worktree.
func Prune(ctx context.Context, repoDir string) error {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	err := prune(ctx, repoDir)
	if ctx.Err() == context.DeadlineExceeded {
		return &TimeoutError{Path: repoDir, Operation: "prune", Timeout: Timeout}
	}
	return err
}

func prune(ctx context.Context, repoDir string) error {
	if _, err := gitRun(ctx, repoDir, "worktree", "prune"); err != nil {
		return err
	}
	worktreesDir := filepath.Join(repoDir, Dir)
	entries, err := os.ReadDir(worktreesDir)
	if errors.Is(err, os.ErrNotExist) {
		return pruneLocks(repoDir, worktreesDir)
	}
	if err != nil {
		return &Error{Message: err.Error()}
	}
	// git reports realpaths; resolve the prefix once so symlinked repos match.
	realDir, err := filepath.EvalSymlinks(worktreesDir)
	if err != nil {
		realDir = worktreesDir
	}
	list, err := ListWorktrees(ctx, repoDir)
	if err != nil {
		return err
	}
	active := map[string]bool{}
	for _, e := range list {
		active[toSlash(e.Path)] = true
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(realDir, e.Name())
		if !active[toSlash(p)] {
			if err := os.RemoveAll(p); err != nil {
				return &Error{Message: "failed to remove " + p + ": " + err.Error()}
			}
		}
	}
	return pruneLocks(repoDir, worktreesDir)
}

func pruneLocks(repoDir, worktreesDir string) error {
	locksDir := filepath.Join(repoDir, LocksDir)
	locks, err := os.ReadDir(locksDir)
	if err != nil {
		return nil
	}
	for _, l := range locks {
		name := strings.TrimSuffix(l.Name(), ".lock")
		if name == l.Name() {
			continue
		}
		path := filepath.Join(locksDir, l.Name())
		_, dirErr := os.Stat(filepath.Join(worktreesDir, name))
		var lf lockFile
		b, _ := os.ReadFile(path)
		alive := json.Unmarshal(b, &lf) == nil && lf.PID > 0 && processAlive(lf.PID)
		if dirErr != nil || !alive {
			os.Remove(path)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/worktree/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worktree/
git commit -m "Prune stale worktrees and locks"
```

---

### Task 7: Bind-mount interfaces and mount resolution

Port of the bind-mount parts of `SandboxProvider.ts`, `MountConfig.ts`, the non-Windows half of `mountUtils.ts`, and `resolveGitMounts` from `SandboxFactory.ts`.

**Files:**
- Modify: `sandbox/sandbox.go` (append types)
- Create: `internal/mount/mount.go`, `internal/mount/mount_test.go`

**Interfaces:**
- Produces, in package `sandbox`:
  ```go
  type Mount struct { HostPath, SandboxPath string; ReadOnly bool }
  type MountConfig struct { HostPath, SandboxPath string; ReadOnly bool } // user-facing: supports ~, relative paths
  type BindMountCreateOptions struct { WorktreePath, HostRepoPath string; Mounts []Mount; Env map[string]string }
  type BindMountHandle interface { Handle; CopyFileIn(ctx context.Context, hostPath, sandboxPath string) error; CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error }
  type BindMountProvider interface { Provider; SandboxHomedir() string; Create(ctx context.Context, opts BindMountCreateOptions) (BindMountHandle, error) }
  type BindMountProviderConfig struct { Name string; Env map[string]string; SandboxHomedir string; Create func(ctx context.Context, opts BindMountCreateOptions) (BindMountHandle, error) }
  func NewBindMountProvider(cfg BindMountProviderConfig) BindMountProvider
  ```
- Produces, in package `mount`:
  ```go
  const SandboxRepoDir = "/home/agent/workspace"
  const SandboxHomedir = "/home/agent"
  func DefaultImageName(repoDir string) string
  func ExpandTilde(p, home string) string
  func ResolveHostPath(p string) string
  func ResolveSandboxPath(p, sandboxHomedir string) (string, error)
  func ResolveUserMounts(mounts []sandbox.MountConfig, sandboxHomedir string) ([]sandbox.Mount, error)
  func FormatVolumeMount(m sandbox.Mount, selinuxLabel string) string // label "", "z" or "Z"
  func FileMountParents(mounts []sandbox.Mount, sandboxHomedir string, stat func(string) (fs.FileInfo, error)) ([]string, error)
  func ResolveGitMounts(gitPath string) ([]sandbox.Mount, error)
  ```

- [ ] **Step 1: Append the types to sandbox/sandbox.go**

```go
// Mount is a resolved bind mount.
type Mount struct {
	HostPath    string
	SandboxPath string
	ReadOnly    bool
}

// MountConfig is a user-facing bind mount. HostPath may use ~ and relative
// paths (resolved from the working directory); SandboxPath may use ~ (the
// sandbox home) and relative paths (resolved from the workspace).
type MountConfig struct {
	HostPath    string
	SandboxPath string
	ReadOnly    bool
}

// BindMountCreateOptions are passed to a BindMountProvider.
type BindMountCreateOptions struct {
	WorktreePath string
	HostRepoPath string
	Mounts       []Mount
	Env          map[string]string
}

// BindMountHandle is a running container that shares the host filesystem.
type BindMountHandle interface {
	Handle
	CopyFileIn(ctx context.Context, hostPath, sandboxPath string) error
	CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error
}

// BindMountProvider creates containers that bind-mount the worktree.
type BindMountProvider interface {
	Provider
	// SandboxHomedir is the home directory inside the sandbox, or "" when unknown.
	SandboxHomedir() string
	Create(ctx context.Context, opts BindMountCreateOptions) (BindMountHandle, error)
}

// BindMountProviderConfig builds a BindMountProvider from a Create function.
type BindMountProviderConfig struct {
	Name           string
	Env            map[string]string
	SandboxHomedir string
	Create         func(ctx context.Context, opts BindMountCreateOptions) (BindMountHandle, error)
}

type bindMountProvider struct{ cfg BindMountProviderConfig }

// NewBindMountProvider wraps cfg as a provider usable by Run.
func NewBindMountProvider(cfg BindMountProviderConfig) BindMountProvider {
	return &bindMountProvider{cfg: cfg}
}

func (p *bindMountProvider) Kind() Kind              { return KindBindMount }
func (p *bindMountProvider) Name() string            { return p.cfg.Name }
func (p *bindMountProvider) Env() map[string]string  { return p.cfg.Env }
func (p *bindMountProvider) SandboxHomedir() string  { return p.cfg.SandboxHomedir }
func (p *bindMountProvider) Create(ctx context.Context, opts BindMountCreateOptions) (BindMountHandle, error) {
	return p.cfg.Create(ctx, opts)
}
```

- [ ] **Step 2: Write the failing mount tests**

`internal/mount/mount_test.go`:
```go
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
		"/home/u/My Repo":   "agentgopher:my-repo",
		"/home/u/repo/":     "agentgopher:repo",
		`C:\Users\p\Proj\`:  "agentgopher:proj",
		"":                  "agentgopher:local",
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
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/mount/`
Expected: FAIL, package not found.

- [ ] **Step 4: Write mount.go**

```go
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
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./sandbox/ ./internal/mount/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add sandbox/sandbox.go internal/mount/
git commit -m "Add bind-mount provider interfaces and mount resolution"
```

---

### Task 8: Windows mount normalization and gitdir patch

Port of `normalizeMounts`, `parseGitdirPath` and `patchGitMountsForWindows` (ADR 0006). All functions take a `goos` argument so the Windows paths are tested on every platform.

**Files:**
- Create: `internal/mount/windows.go`, `internal/mount/windows_test.go`

**Interfaces:**
- Produces:
  ```go
  const ParentGitSandboxDir = "/.agentgopher-parent-git"
  func NormalizeMounts(mounts []sandbox.Mount, worktreeHostPath, sandboxRepoDir, goos string) []sandbox.Mount
  func ParseGitdirPath(gitdir string) (parentGitDir, worktreeName string)
  type FS interface { ReadFile(name string) ([]byte, error); Stat(name string) (fs.FileInfo, error) }
  func PatchGitMountsForWindows(gitMounts []sandbox.Mount, worktreeHostPath, sandboxRepoDir string, fsys FS, goos string) ([]sandbox.Mount, error)
  type OSFS struct{} // ReadFile and Stat via package os
  ```

- [ ] **Step 1: Write the failing tests**

`internal/mount/windows_test.go`:
```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/mount/ -run 'Normalize|Gitdir|Patch'`
Expected: FAIL, "undefined: NormalizeMounts".

- [ ] **Step 3: Write windows.go**

```go
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

func (OSFS) ReadFile(name string) ([]byte, error)   { return os.ReadFile(name) }
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/mount/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mount/
git commit -m "Normalize mounts and patch gitdir pointers for Windows hosts"
```

---

### Task 9: Shutdown registry and container handle

Port of `shutdownRegistry.ts` and the shared exec/cp/rm code duplicated in `sandboxes/docker.ts` and `sandboxes/podman.ts`.

**Files:**
- Create: `internal/shutdown/shutdown.go`, `internal/shutdown/shutdown_test.go`, `internal/container/container.go`, `internal/container/container_test.go`

**Interfaces:**
- Produces, in `shutdown`: `func Register(teardown func()) (unregister func())`. The first registration installs one `signal.Notify` for SIGINT and SIGTERM; on a signal every teardown runs, then `os.Exit(1)`. The last unregistration calls `signal.Stop`. `var exit = os.Exit` so tests can intercept.
- Produces, in `container`:
  ```go
  type Error struct { Binary, Message string }                // "docker run failed: ..."
  func Run(ctx context.Context, binary string, args ...string) (string, error) // trimmed stdout, Error with stderr
  type Handle struct { Binary, Name, Worktree string; MaxTail int; unregister func() }
  func NewHandle(binary, name, worktree string, maxTail int) *Handle // registers `rm -f` with shutdown
  func (h *Handle) WorktreePath() string
  func (h *Handle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error)
  func (h *Handle) CopyFileIn(ctx context.Context, hostPath, sandboxPath string) error
  func (h *Handle) CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error
  func (h *Handle) Close(ctx context.Context) error  // unregisters, then `<binary> rm -f <name>`
  ```
  `Exec` builds `<binary> exec [-i] [-w cwd] <name> sh -c <command>`, prefixing `sudo ` when `opts.Sudo`, and streams through `stream.NewLineWriter` with a `stream.BoundedTail` exactly like `nosandbox`.

- [ ] **Step 1: Write the failing tests**

`internal/shutdown/shutdown_test.go`:
```go
package shutdown

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestSignalRunsTeardownsOnceAndExits(t *testing.T) {
	exited := make(chan int, 1)
	exit = func(code int) { exited <- code }
	t.Cleanup(func() { exit = os.Exit })
	calls := 0
	un1 := Register(func() { calls++ })
	un2 := Register(func() { calls++ })
	defer un1()
	defer un2()
	syscall.Kill(os.Getpid(), syscall.SIGTERM)
	select {
	case code := <-exited:
		if code != 1 || calls != 2 {
			t.Fatalf("code=%d calls=%d", code, calls)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exit not called")
	}
}

func TestUnregisterIsIdempotent(t *testing.T) {
	un := Register(func() {})
	un()
	un()
	mu.Lock()
	n := len(teardowns)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("teardowns = %d", n)
	}
}
```

`internal/container/container_test.go`:
```go
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
    exec sh -c "$1" ;;
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/shutdown/ ./internal/container/`
Expected: FAIL, packages not found.

- [ ] **Step 3: Write shutdown.go**

```go
// Package shutdown runs container teardowns when the process is interrupted.
package shutdown

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var (
	mu        sync.Mutex
	teardowns = map[int]func(){}
	nextID    int
	sigCh     chan os.Signal
	exit      = os.Exit
)

// Register adds a teardown that runs on SIGINT or SIGTERM, after which the
// process exits with code 1. The returned function removes it; calling it
// twice is safe.
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
			mu.Lock()
			all := make([]func(), 0, len(teardowns))
			for _, f := range teardowns {
				all = append(all, f)
			}
			mu.Unlock()
			for _, f := range all {
				func() {
					defer func() { _ = recover() }()
					f()
				}()
			}
			exit(1)
		}(sigCh)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			mu.Lock()
			defer mu.Unlock()
			delete(teardowns, id)
			if len(teardowns) == 0 && sigCh != nil {
				signal.Stop(sigCh)
				close(sigCh)
				sigCh = nil
			}
		})
	}
}
```

- [ ] **Step 4: Write container.go**

```go
// Package container drives a container through its CLI (docker or podman).
package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/e11en/agentgopher/internal/shutdown"
	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/sandbox"
)

// Error reports a failed container CLI call.
type Error struct {
	Binary  string
	Message string
}

func (e *Error) Error() string { return e.Binary + " " + e.Message }

// Run executes the container binary and returns trimmed stdout.
func Run(ctx context.Context, binary string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		verb := ""
		if len(args) > 0 {
			verb = args[0]
		}
		return "", &Error{Binary: binary, Message: fmt.Sprintf("%s failed: %s", verb, msg)}
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Handle is a running container addressed by name.
type Handle struct {
	Binary     string
	Name       string
	Worktree   string
	MaxTail    int
	unregister func()
}

// NewHandle wraps a started container and registers its removal on shutdown.
func NewHandle(binary, name, worktree string, maxTail int) *Handle {
	h := &Handle{Binary: binary, Name: name, Worktree: worktree, MaxTail: maxTail}
	h.unregister = shutdown.Register(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = Run(ctx, binary, "rm", "-f", name)
	})
	return h
}

func (h *Handle) WorktreePath() string { return h.Worktree }

func (h *Handle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) {
	if opts.Sudo {
		command = "sudo " + command
	}
	args := []string{"exec"}
	if opts.Stdin != nil {
		args = append(args, "-i")
	}
	if opts.Cwd != "" {
		args = append(args, "-w", opts.Cwd)
	}
	args = append(args, h.Name, "sh", "-c", command)
	cmd := exec.CommandContext(ctx, h.Binary, args...)
	cmd.WaitDelay = 2 * time.Second
	if opts.Stdin != nil {
		cmd.Stdin = strings.NewReader(*opts.Stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	var stdout string
	var err error
	if opts.OnLine == nil {
		var out bytes.Buffer
		cmd.Stdout = &out
		err = cmd.Run()
		stdout = out.String()
	} else {
		tail := stream.NewBoundedTail(h.MaxTail, "\n")
		lw := stream.NewLineWriter(func(line string) {
			tail.Push(line)
			opts.OnLine(line)
		})
		cmd.Stdout = lw
		err = cmd.Run()
		lw.Flush()
		stdout = tail.String()
	}
	if ctx.Err() != nil {
		return sandbox.ExecResult{}, ctx.Err()
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
	}
	errTail := stream.NewBoundedTail(h.MaxTail, "")
	errTail.Push(stderr.String())
	return sandbox.ExecResult{Stdout: stdout, Stderr: errTail.String(), ExitCode: cmd.ProcessState.ExitCode()}, nil
}

func (h *Handle) CopyFileIn(ctx context.Context, hostPath, sandboxPath string) error {
	_, err := Run(ctx, h.Binary, "cp", hostPath, h.Name+":"+sandboxPath)
	return err
}

func (h *Handle) CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error {
	_, err := Run(ctx, h.Binary, "cp", h.Name+":"+sandboxPath, hostPath)
	return err
}

// Close removes the container and drops the shutdown registration.
func (h *Handle) Close(ctx context.Context) error {
	if h.unregister != nil {
		h.unregister()
	}
	_, err := Run(ctx, h.Binary, "rm", "-f", h.Name)
	return err
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/shutdown/ ./internal/container/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/shutdown/ internal/container/
git commit -m "Add shutdown registry and shared container handle"
```

---

### Task 10: Docker provider

Port of `sandboxes/docker.ts` and `DockerLifecycle.ts`. Pure parts (argument building, UID parsing) are unit tested; `docker run` itself is covered by a test that skips without a daemon.

**Files:**
- Create: `sandbox/docker/docker.go`, `sandbox/docker/docker_test.go`

**Interfaces:**
- Produces:
  ```go
  type Options struct {
      ImageName          string               // default mount.DefaultImageName(hostRepoPath)
      ContainerUID       int                  // 0 means os.Getuid() (1000 on Windows)
      ContainerGID       int                  // 0 means os.Getgid() (1000 on Windows)
      SelinuxLabel       string               // "" means "z"; "none" disables; "Z" private
      Mounts             []sandbox.MountConfig
      Env                map[string]string
      Network            []string
      Groups             []string
      Devices            []string
      MaxOutputTailChars int
      CPUs               float64              // 0 means unlimited
  }
  func New(opts *Options) sandbox.BindMountProvider  // Name "docker", SandboxHomedir "/home/agent"
  func BuildImage(ctx context.Context, imageName, dockerfileDir, dockerfile string, buildArgs map[string]string) error
  func RemoveImage(ctx context.Context, imageName string) error
  func RunArgs(name, image, workdir, user string, env map[string]string, mounts []sandbox.Mount, o Options) []string // pure; docker run -d ... image
  func CheckImageUID(user string, expected int, image string) error // pure parse of `{{.Config.User}}`
  ```
- Mount validation errors (missing host path, file mount outside home) surface from `Create`, not from `New`, because `New` returns no error.

- [ ] **Step 1: Write the failing tests**

`sandbox/docker/docker_test.go`:
```go
package docker

import (
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/sandbox"
)

func TestRunArgs(t *testing.T) {
	got := RunArgs("c1", "img", "/home/agent/workspace", "1000:1000",
		map[string]string{"A": "1"},
		[]sandbox.Mount{{HostPath: "/h", SandboxPath: "/s"}, {HostPath: "/r", SandboxPath: "/t", ReadOnly: true}},
		Options{Network: []string{"n1"}, Groups: []string{"docker", "999"}, Devices: []string{"/dev/kvm"}, CPUs: 1.5})
	want := []string{"run", "-d", "--name", "c1", "-e", "A=1", "-v", "/h:/s:z", "-v", "/r:/t:ro,z",
		"-w", "/home/agent/workspace", "--user", "1000:1000", "--network", "n1",
		"--group-add", "docker", "--group-add", "999", "--device", "/dev/kvm", "--cpus", "1.5", "img"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	got = RunArgs("c1", "img", "/w", "1:1", nil, nil, Options{SelinuxLabel: "none"})
	if strings.Contains(strings.Join(got, " "), ":z") {
		t.Fatalf("label not disabled: %v", got)
	}
}

func TestCheckImageUID(t *testing.T) {
	if err := CheckImageUID("", 1000, "img"); err != nil {
		t.Fatal("no USER must pass")
	}
	if err := CheckImageUID("agent", 1000, "img"); err != nil {
		t.Fatal("non-numeric USER must pass")
	}
	if err := CheckImageUID("1000:1000", 1000, "img"); err != nil {
		t.Fatal(err)
	}
	err := CheckImageUID("1001:1001", 1000, "img")
	if err == nil || !strings.Contains(err.Error(), "UID mismatch") || !strings.Contains(err.Error(), "containerUid: 1001") {
		t.Fatalf("err = %v", err)
	}
}

func TestProviderMetadata(t *testing.T) {
	p := New(nil)
	if p.Kind() != sandbox.KindBindMount || p.Name() != "docker" || p.SandboxHomedir() != "/home/agent" {
		t.Fatalf("kind=%q name=%q home=%q", p.Kind(), p.Name(), p.SandboxHomedir())
	}
}

func TestCreateRejectsMissingMountHostPath(t *testing.T) {
	p := New(&Options{Mounts: []sandbox.MountConfig{{HostPath: "/definitely/missing", SandboxPath: "/x"}}})
	_, err := p.Create(context.Background(), sandbox.BindMountCreateOptions{WorktreePath: "/w", HostRepoPath: "/r"})
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v", err)
	}
}

// needs daemon
func TestCreateExecCloseAgainstDaemon(t *testing.T) {
	if exec.Command("docker", "info").Run() != nil {
		t.Skip("docker daemon not available")
	}
	if exec.Command("docker", "image", "inspect", "alpine:3.20").Run() != nil {
		if exec.Command("docker", "pull", "alpine:3.20").Run() != nil {
			t.Skip("cannot pull alpine:3.20")
		}
	}
	wt := t.TempDir()
	p := New(&Options{ImageName: "alpine:3.20", ContainerUID: 0, ContainerGID: 0})
	h, err := p.Create(context.Background(), sandbox.BindMountCreateOptions{
		WorktreePath: wt, HostRepoPath: wt,
		Mounts: []sandbox.Mount{{HostPath: wt, SandboxPath: "/home/agent/workspace"}},
		Env:    map[string]string{"HELLO": "world"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	res, err := h.Exec(context.Background(), "echo $HELLO; pwd", sandbox.ExecOptions{})
	if err != nil || res.Stdout != "world\n/home/agent/workspace\n" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
```

Note: alpine has no `USER`, so `CheckImageUID` passes, and `--user 0:0` is required because `ContainerUID: 0` means "host UID" in `New`; set the default resolution so that an explicit zero is treated as "use host UID", and the test runs as the host user. If the host UID is not 0 the `pwd` still works because the mount is world-readable. Keep the assertion on `pwd` only if the echo line is flaky.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./sandbox/docker/`
Expected: FAIL, package not found.

- [ ] **Step 3: Write docker.go**

```go
// Package docker runs the agent in a Docker container that bind-mounts the worktree.
package docker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/e11en/agentgopher/internal/container"
	"github.com/e11en/agentgopher/internal/mount"
	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/sandbox"
)

// Options configure the Docker provider.
type Options struct {
	// ImageName defaults to agentgopher:<repo dir name>.
	ImageName string
	// ContainerUID and ContainerGID must match the UID and GID baked into the
	// image. Zero means the host UID and GID (1000 on Windows).
	ContainerUID int
	ContainerGID int
	// SelinuxLabel is "z" (shared, default), "Z" (private) or "none".
	SelinuxLabel string
	// Mounts are extra host directories to bind-mount.
	Mounts []sandbox.MountConfig
	// Env is injected into the container. Merged at launch time.
	Env map[string]string
	// Network, Groups and Devices map to --network, --group-add and --device.
	Network []string
	Groups  []string
	Devices []string
	// MaxOutputTailChars bounds streamed exec output. Default: stream.MaxTailChars.
	MaxOutputTailChars int
	// CPUs maps to --cpus. Zero leaves the container unconstrained.
	CPUs float64
}

const binary = "docker"

// New returns a Docker provider.
func New(opts *Options) sandbox.BindMountProvider {
	var o Options
	if opts != nil {
		o = *opts
	}
	if o.MaxOutputTailChars == 0 {
		o.MaxOutputTailChars = stream.MaxTailChars
	}
	return sandbox.NewBindMountProvider(sandbox.BindMountProviderConfig{
		Name:           binary,
		Env:            o.Env,
		SandboxHomedir: mount.SandboxHomedir,
		Create: func(ctx context.Context, c sandbox.BindMountCreateOptions) (sandbox.BindMountHandle, error) {
			return create(ctx, o, c)
		},
	})
}

func hostID(configured int, get func() int) int {
	if configured != 0 {
		return configured
	}
	if runtime.GOOS == "windows" {
		return 1000
	}
	return get()
}

func containerName() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "agentgopher-" + hex.EncodeToString(b)
}

func create(ctx context.Context, o Options, c sandbox.BindMountCreateOptions) (sandbox.BindMountHandle, error) {
	userMounts, err := mount.ResolveUserMounts(o.Mounts, mount.SandboxHomedir)
	if err != nil {
		return nil, err
	}
	parentDirs, err := mount.FileMountParents(userMounts, mount.SandboxHomedir, nil)
	if err != nil {
		return nil, err
	}

	workdir := mount.SandboxRepoDir
	for _, m := range c.Mounts {
		if m.HostPath == c.WorktreePath {
			workdir = m.SandboxPath
		}
	}
	image := o.ImageName
	if image == "" {
		image = mount.DefaultImageName(c.HostRepoPath)
	}
	uid := hostID(o.ContainerUID, os.Getuid)
	gid := hostID(o.ContainerGID, os.Getgid)

	user, err := container.Run(ctx, binary, "image", "inspect", image, "--format", "{{.Config.User}}")
	if err != nil {
		return nil, fmt.Errorf("image '%s' not found locally. Build it first with 'agentgopher docker build-image'", image)
	}
	if err := CheckImageUID(user, uid, image); err != nil {
		return nil, err
	}

	name := containerName()
	env := map[string]string{}
	for k, v := range c.Env {
		env[k] = v
	}
	env["HOME"] = mount.SandboxHomedir
	userSpec := fmt.Sprintf("%d:%d", uid, gid)
	if _, err := container.Run(ctx, binary, RunArgs(name, image, workdir, userSpec, env, append(append([]sandbox.Mount{}, c.Mounts...), userMounts...), o)...); err != nil {
		return nil, err
	}
	for _, dir := range parentDirs {
		if _, err := container.Run(ctx, binary, "exec", "--user", "0:0", name, "sh", "-c", `mkdir -p "$1" && chown "$2" "$1"`, "sh", dir, userSpec); err != nil {
			_, _ = container.Run(ctx, binary, "rm", "-f", name)
			return nil, fmt.Errorf("failed to create parent directory '%s' in container: %w", dir, err)
		}
	}
	return container.NewHandle(binary, name, workdir, o.MaxOutputTailChars), nil
}

// RunArgs builds the `docker run` argument list.
func RunArgs(name, image, workdir, user string, env map[string]string, mounts []sandbox.Mount, o Options) []string {
	label := o.SelinuxLabel
	switch label {
	case "":
		label = "z"
	case "none":
		label = ""
	}
	args := []string{"run", "-d", "--name", name}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}
	for _, m := range mounts {
		args = append(args, "-v", mount.FormatVolumeMount(m, label))
	}
	if workdir != "" {
		args = append(args, "-w", workdir)
	}
	if user != "" {
		args = append(args, "--user", user)
	}
	for _, n := range o.Network {
		args = append(args, "--network", n)
	}
	for _, g := range o.Groups {
		args = append(args, "--group-add", g)
	}
	for _, d := range o.Devices {
		args = append(args, "--device", d)
	}
	if o.CPUs > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(o.CPUs, 'f', -1, 64))
	}
	return append(args, image)
}

// CheckImageUID compares the image's numeric USER with the expected UID.
// Images without USER or with a named user skip the check.
func CheckImageUID(user string, expected int, image string) error {
	user = strings.TrimSpace(user)
	if user == "" {
		return nil
	}
	uid, err := strconv.Atoi(strings.SplitN(user, ":", 2)[0])
	if err != nil {
		return nil
	}
	if uid != expected {
		return fmt.Errorf("UID mismatch: image '%s' was built with UID %d, but the expected UID is %d. Rebuild the image with 'agentgopher docker build-image', or pass containerUid: %d to docker.New to match the image", image, uid, expected, uid)
	}
	return nil
}

// BuildImage builds imageName from dockerfileDir, or from dockerfile with the
// working directory as context when dockerfile is set.
func BuildImage(ctx context.Context, imageName, dockerfileDir, dockerfile string, buildArgs map[string]string) error {
	args := []string{"build", "-t", imageName}
	keys := make([]string, 0, len(buildArgs))
	for k := range buildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--build-arg", k+"="+buildArgs[k])
	}
	if dockerfile != "" {
		abs, _ := filepath.Abs(dockerfile)
		wd, _ := os.Getwd()
		args = append(args, "-f", abs, wd)
	} else {
		abs, _ := filepath.Abs(dockerfileDir)
		args = append(args, abs)
	}
	_, err := container.Run(ctx, binary, args...)
	return err
}

// RemoveImage removes imageName.
func RemoveImage(ctx context.Context, imageName string) error {
	_, err := container.Run(ctx, binary, "rmi", imageName)
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./sandbox/docker/`
Expected: PASS (the daemon test skips without Docker).

- [ ] **Step 5: Commit**

```bash
git add sandbox/docker/
git commit -m "Add Docker bind-mount provider"
```

---

### Task 11: Podman provider

Port of `sandboxes/podman.ts` and `PodmanLifecycle.ts`. Differences from Docker: `--userns=keep-id:uid=N,gid=N` by default, `--entrypoint sleep ... infinity`, a Podman Machine check on macOS and Windows, and no UID pre-flight (only "image exists").

**Files:**
- Create: `sandbox/podman/podman.go`, `sandbox/podman/podman_test.go`

**Interfaces:**
- Produces:
  ```go
  type Options struct {
      ImageName          string
      SelinuxLabel       string          // "" means "z"; "none" disables
      KeepID             *bool           // nil means true: --userns=keep-id
      ContainerUID       int             // 0 means 1000
      ContainerGID       int             // 0 means 1000
      Mounts             []sandbox.MountConfig
      Env                map[string]string
      Network, Groups, Devices []string
      MaxOutputTailChars int
      CPUs               float64
  }
  func New(opts *Options) sandbox.BindMountProvider  // Name "podman"
  func BuildImage(ctx context.Context, imageName, containerfileDir, containerfile string) error
  func RemoveImage(ctx context.Context, imageName string) error
  func RunArgs(name, image, workdir string, env map[string]string, mounts []sandbox.Mount, o Options) []string
  func MachineRunning(listJSON string) bool // parses `podman machine list --format json`
  ```

- [ ] **Step 1: Write the failing tests**

`sandbox/podman/podman_test.go`:
```go
package podman

import (
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/sandbox"
)

func TestRunArgsKeepIDDefault(t *testing.T) {
	got := RunArgs("c1", "img", "/w", map[string]string{"A": "1"}, []sandbox.Mount{{HostPath: "/h", SandboxPath: "/s"}}, Options{CPUs: 2})
	want := []string{"run", "-d", "--name", "c1", "--user", "1000:1000", "--userns=keep-id:uid=1000,gid=1000",
		"--cpus", "2", "-w", "/w", "-e", "A=1", "-v", "/h:/s:z", "--entrypoint", "sleep", "img", "infinity"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

func TestRunArgsRootful(t *testing.T) {
	f := false
	got := RunArgs("c1", "img", "/w", nil, nil, Options{KeepID: &f, ContainerUID: 2000, ContainerGID: 2000, SelinuxLabel: "none"})
	joined := strings.Join(got, " ")
	if strings.Contains(joined, "userns") || !strings.Contains(joined, "--user 2000:2000") {
		t.Fatalf("got %v", got)
	}
}

func TestMachineRunning(t *testing.T) {
	if !MachineRunning(`[{"Name":"m","Running":true}]`) {
		t.Fatal("running machine not detected")
	}
	if MachineRunning(`[{"Name":"m","Running":false}]`) || MachineRunning("garbage") || MachineRunning("[]") {
		t.Fatal("non-running machine reported as running")
	}
}

func TestProviderMetadata(t *testing.T) {
	p := New(nil)
	if p.Kind() != sandbox.KindBindMount || p.Name() != "podman" || p.SandboxHomedir() != "/home/agent" {
		t.Fatalf("kind=%q name=%q", p.Kind(), p.Name())
	}
}

// needs daemon
func TestCreateExecCloseAgainstPodman(t *testing.T) {
	if exec.Command("podman", "info").Run() != nil {
		t.Skip("podman not available")
	}
	if exec.Command("podman", "image", "inspect", "alpine:3.20").Run() != nil && exec.Command("podman", "pull", "alpine:3.20").Run() != nil {
		t.Skip("cannot pull alpine:3.20")
	}
	wt := t.TempDir()
	f := false
	p := New(&Options{ImageName: "alpine:3.20", KeepID: &f})
	h, err := p.Create(context.Background(), sandbox.BindMountCreateOptions{
		WorktreePath: wt, HostRepoPath: wt, Mounts: []sandbox.Mount{{HostPath: wt, SandboxPath: "/home/agent/workspace"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	res, err := h.Exec(context.Background(), "pwd", sandbox.ExecOptions{})
	if err != nil || res.Stdout != "/home/agent/workspace\n" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./sandbox/podman/`
Expected: FAIL, package not found.

- [ ] **Step 3: Write podman.go**

```go
// Package podman runs the agent in a Podman container that bind-mounts the worktree.
package podman

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"

	"github.com/e11en/agentgopher/internal/container"
	"github.com/e11en/agentgopher/internal/mount"
	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/sandbox"
)

// Options configure the Podman provider.
type Options struct {
	// ImageName defaults to agentgopher:<repo dir name>.
	ImageName string
	// SelinuxLabel is "z" (shared, default), "Z" (private) or "none".
	SelinuxLabel string
	// KeepID maps the host user to ContainerUID with --userns=keep-id. nil means true.
	KeepID *bool
	// ContainerUID and ContainerGID must match the Containerfile. Zero means 1000.
	ContainerUID int
	ContainerGID int
	Mounts       []sandbox.MountConfig
	Env          map[string]string
	Network      []string
	Groups       []string
	Devices      []string
	// MaxOutputTailChars bounds streamed exec output. Default: stream.MaxTailChars.
	MaxOutputTailChars int
	// CPUs maps to --cpus. Zero leaves the container unconstrained.
	CPUs float64
}

const binary = "podman"

// New returns a Podman provider.
func New(opts *Options) sandbox.BindMountProvider {
	var o Options
	if opts != nil {
		o = *opts
	}
	if o.MaxOutputTailChars == 0 {
		o.MaxOutputTailChars = stream.MaxTailChars
	}
	if o.ContainerUID == 0 {
		o.ContainerUID = 1000
	}
	if o.ContainerGID == 0 {
		o.ContainerGID = 1000
	}
	return sandbox.NewBindMountProvider(sandbox.BindMountProviderConfig{
		Name:           binary,
		Env:            o.Env,
		SandboxHomedir: mount.SandboxHomedir,
		Create: func(ctx context.Context, c sandbox.BindMountCreateOptions) (sandbox.BindMountHandle, error) {
			return create(ctx, o, c)
		},
	})
}

func containerName() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "agentgopher-" + hex.EncodeToString(b)
}

// MachineRunning parses `podman machine list --format json`.
func MachineRunning(listJSON string) bool {
	var machines []struct {
		Running bool `json:"Running"`
	}
	if json.Unmarshal([]byte(listJSON), &machines) != nil {
		return false
	}
	for _, m := range machines {
		if m.Running {
			return true
		}
	}
	return false
}

func create(ctx context.Context, o Options, c sandbox.BindMountCreateOptions) (sandbox.BindMountHandle, error) {
	userMounts, err := mount.ResolveUserMounts(o.Mounts, mount.SandboxHomedir)
	if err != nil {
		return nil, err
	}
	parentDirs, err := mount.FileMountParents(userMounts, mount.SandboxHomedir, nil)
	if err != nil {
		return nil, err
	}
	workdir := mount.SandboxRepoDir
	for _, m := range c.Mounts {
		if m.HostPath == c.WorktreePath {
			workdir = m.SandboxPath
		}
	}
	image := o.ImageName
	if image == "" {
		image = mount.DefaultImageName(c.HostRepoPath)
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		list, err := container.Run(ctx, binary, "machine", "list", "--format", "json")
		if err != nil || !MachineRunning(list) {
			return nil, fmt.Errorf("Podman Machine is not running. Run 'podman machine init && podman machine start' first")
		}
	}
	if _, err := container.Run(ctx, binary, "image", "inspect", image); err != nil {
		return nil, fmt.Errorf("image '%s' not found locally. Build it first with 'podman build -t %s .'", image, image)
	}
	env := map[string]string{}
	for k, v := range c.Env {
		env[k] = v
	}
	env["HOME"] = mount.SandboxHomedir
	name := containerName()
	if _, err := container.Run(ctx, binary, RunArgs(name, image, workdir, env, append(append([]sandbox.Mount{}, c.Mounts...), userMounts...), o)...); err != nil {
		return nil, err
	}
	userSpec := fmt.Sprintf("%d:%d", o.ContainerUID, o.ContainerGID)
	for _, dir := range parentDirs {
		if _, err := container.Run(ctx, binary, "exec", "--user", "0:0", name, "sh", "-c", `mkdir -p "$1" && chown "$2" "$1"`, "sh", dir, userSpec); err != nil {
			_, _ = container.Run(ctx, binary, "rm", "-f", name)
			return nil, fmt.Errorf("failed to create parent directory '%s' in container: %w", dir, err)
		}
	}
	return container.NewHandle(binary, name, workdir, o.MaxOutputTailChars), nil
}

// RunArgs builds the `podman run` argument list.
func RunArgs(name, image, workdir string, env map[string]string, mounts []sandbox.Mount, o Options) []string {
	uid, gid := o.ContainerUID, o.ContainerGID
	if uid == 0 {
		uid = 1000
	}
	if gid == 0 {
		gid = 1000
	}
	label := o.SelinuxLabel
	switch label {
	case "":
		label = "z"
	case "none":
		label = ""
	}
	args := []string{"run", "-d", "--name", name, "--user", fmt.Sprintf("%d:%d", uid, gid)}
	if o.KeepID == nil || *o.KeepID {
		args = append(args, fmt.Sprintf("--userns=keep-id:uid=%d,gid=%d", uid, gid))
	}
	for _, n := range o.Network {
		args = append(args, "--network", n)
	}
	for _, g := range o.Groups {
		args = append(args, "--group-add", g)
	}
	for _, d := range o.Devices {
		args = append(args, "--device", d)
	}
	if o.CPUs > 0 {
		args = append(args, "--cpus", strconv.FormatFloat(o.CPUs, 'f', -1, 64))
	}
	args = append(args, "-w", workdir)
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+env[k])
	}
	for _, m := range mounts {
		args = append(args, "-v", mount.FormatVolumeMount(m, label))
	}
	return append(args, "--entrypoint", "sleep", image, "infinity")
}

// BuildImage builds imageName from containerfileDir, or from containerfile
// with the working directory as context when containerfile is set.
func BuildImage(ctx context.Context, imageName, containerfileDir, containerfile string) error {
	args := []string{"build", "-t", imageName}
	if containerfile != "" {
		abs, _ := filepath.Abs(containerfile)
		wd, _ := os.Getwd()
		args = append(args, "-f", abs, wd)
	} else {
		abs, _ := filepath.Abs(containerfileDir)
		args = append(args, abs)
	}
	_, err := container.Run(ctx, binary, args...)
	return err
}

// RemoveImage removes imageName.
func RemoveImage(ctx context.Context, imageName string) error {
	_, err := container.Run(ctx, binary, "rmi", imageName)
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./sandbox/podman/`
Expected: PASS (the daemon test skips without Podman).

- [ ] **Step 5: Commit**

```bash
git add sandbox/podman/
git commit -m "Add Podman bind-mount provider"
```

---

### Task 12: Lifecycle merge-to-head path

Port of the remaining half of `SandboxLifecycle.ts`: when `Branch` is empty the lifecycle records the host's current branch, discovers the worktree's branch inside the sandbox, and after the work merges that branch into the host branch and deletes it. `ApplyToHost` runs before the merge (used by phase 4), `KeepSourceBranch` keeps the source branch for `Worktree.Run`.

**Files:**
- Modify: `internal/lifecycle/lifecycle.go`, `internal/lifecycle/lifecycle_test.go`

**Interfaces:**
- `lifecycle.Options` gains:
  ```go
  HostWorktreePath string                        // host path of the directory mounted at SandboxRepoDir; default SandboxRepoDir
  ApplyToHost      func(ctx context.Context) error // nil for bind-mount and no-sandbox
  KeepSourceBranch bool
  ```
- `lifecycle.Timeouts` gains `MergeToHost, CopyToWorktree time.Duration` (defaults 30 s and 60 s; `CopyToWorktree` is consumed by the orchestrator in Task 13).
- New errors: `MergeToHostTimeoutError{SourceBranch, TargetBranch string; Timeout time.Duration}`, `MergeError{Message string}` (the merge failed and the temp branch was preserved).
- `Run` semantics with `Branch == ""`: `Result.Branch` is the host branch; `Result.Commits` are the commits now on the host branch since `BaseHead`.

- [ ] **Step 1: Write the failing tests (append)**

```go
func TestRunMergeToHeadMergesAndDeletesTempBranch(t *testing.T) {
	repo := testrepo.Init(t)
	wt, err := worktree.CreateTemp(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	h, _ := nosandbox.New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: wt.Path})
	log := &bytes.Buffer{}
	res, err := Run(context.Background(), Options{
		HostRepoDir: repo, SandboxRepoDir: wt.Path, HostWorktreePath: wt.Path, Display: display.NewWriter(log),
	}, h, func(c Context) (int, error) {
		testrepo.Commit(t, wt.Path, "agent.txt", "x", "agent change")
		return 1, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "main" || len(res.Commits) != 1 {
		t.Fatalf("res = %+v", res)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "agent.txt")); statErr != nil {
		t.Fatal("commit not merged into main")
	}
	if strings.Contains(testrepo.Git(t, repo, "branch", "--list", wt.Branch), wt.Branch) {
		t.Fatal("temp branch not deleted")
	}
	if !strings.Contains(log.String(), "Merging to main") {
		t.Fatalf("log:\n%s", log.String())
	}
}

func TestRunMergeToHeadWithDivergedHostIsRealMerge(t *testing.T) {
	repo := testrepo.Init(t)
	wt, _ := worktree.CreateTemp(context.Background(), repo, "")
	h, _ := nosandbox.New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: wt.Path})
	res, err := Run(context.Background(), Options{HostRepoDir: repo, SandboxRepoDir: wt.Path, HostWorktreePath: wt.Path, Display: display.NewWriter(&bytes.Buffer{})}, h,
		func(Context) (int, error) {
			testrepo.Commit(t, repo, "host.txt", "h", "host moved")
			testrepo.Commit(t, wt.Path, "agent.txt", "a", "agent change")
			return 1, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	// host commit, agent commit and the merge commit
	if len(res.Commits) != 3 {
		t.Fatalf("commits = %v", res.Commits)
	}
}

func TestRunMergeConflictPreservesTempBranch(t *testing.T) {
	repo := testrepo.Init(t)
	wt, _ := worktree.CreateTemp(context.Background(), repo, "")
	h, _ := nosandbox.New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: wt.Path})
	_, err := Run(context.Background(), Options{HostRepoDir: repo, SandboxRepoDir: wt.Path, HostWorktreePath: wt.Path, Display: display.NewWriter(&bytes.Buffer{})}, h,
		func(Context) (int, error) {
			testrepo.Commit(t, repo, "README.md", "host version\n", "host edit")
			testrepo.Commit(t, wt.Path, "README.md", "agent version\n", "agent edit")
			return 1, nil
		})
	var merr *MergeError
	if !errors.As(err, &merr) || !strings.Contains(merr.Message, "git merge "+wt.Branch) {
		t.Fatalf("err = %v", err)
	}
	testrepo.Git(t, repo, "merge", "--abort")
	if !strings.Contains(testrepo.Git(t, repo, "branch", "--list", wt.Branch), wt.Branch) {
		t.Fatal("temp branch must be preserved on conflict")
	}
}

func TestRunMergeToHeadNoCommitsSkipsMerge(t *testing.T) {
	repo := testrepo.Init(t)
	wt, _ := worktree.CreateTemp(context.Background(), repo, "")
	h, _ := nosandbox.New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: wt.Path})
	log := &bytes.Buffer{}
	res, err := Run(context.Background(), Options{HostRepoDir: repo, SandboxRepoDir: wt.Path, HostWorktreePath: wt.Path, Display: display.NewWriter(log)}, h,
		func(Context) (int, error) { return 1, nil })
	if err != nil || len(res.Commits) != 0 || strings.Contains(log.String(), "Merging") {
		t.Fatalf("res=%+v err=%v log=%s", res, err, log.String())
	}
}

func TestRunKeepSourceBranch(t *testing.T) {
	repo := testrepo.Init(t)
	wt, _ := worktree.CreateTemp(context.Background(), repo, "")
	h, _ := nosandbox.New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: wt.Path})
	_, err := Run(context.Background(), Options{HostRepoDir: repo, SandboxRepoDir: wt.Path, HostWorktreePath: wt.Path, KeepSourceBranch: true, Display: display.NewWriter(&bytes.Buffer{})}, h,
		func(Context) (int, error) { testrepo.Commit(t, wt.Path, "a.txt", "a", "agent"); return 1, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(testrepo.Git(t, wt.Path, "rev-parse", "--abbrev-ref", "HEAD")); got != wt.Branch {
		t.Fatalf("worktree detached or moved: %q", got)
	}
}

func TestRunCallsApplyToHostBeforeMerge(t *testing.T) {
	repo := testrepo.Init(t)
	wt, _ := worktree.CreateTemp(context.Background(), repo, "")
	h, _ := nosandbox.New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: wt.Path})
	applied := false
	res, err := Run(context.Background(), Options{HostRepoDir: repo, SandboxRepoDir: wt.Path, HostWorktreePath: wt.Path, Display: display.NewWriter(&bytes.Buffer{}),
		ApplyToHost: func(context.Context) error { applied = true; testrepo.Commit(t, wt.Path, "synced.txt", "s", "synced"); return nil },
	}, h, func(Context) (int, error) { return 1, nil })
	if err != nil || !applied || len(res.Commits) != 1 {
		t.Fatalf("res=%+v err=%v applied=%v", res, err, applied)
	}
}
```

Add `"github.com/e11en/agentgopher/internal/worktree"` to the test imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/lifecycle/`
Expected: FAIL, unknown fields `HostWorktreePath`, `KeepSourceBranch`, `ApplyToHost`.

- [ ] **Step 3: Extend lifecycle.go**

Add the errors and fields:
```go
const defaultMergeToHostTimeout = 30 * time.Second

// MergeToHostTimeoutError reports a merge-to-head that ran longer than Timeout.
type MergeToHostTimeoutError struct {
	SourceBranch string
	TargetBranch string
	Timeout      time.Duration
}

func (e *MergeToHostTimeoutError) Error() string {
	return fmt.Sprintf("merge of '%s' to '%s' timed out after %v", e.SourceBranch, e.TargetBranch, e.Timeout)
}

// MergeError reports a failed merge-to-head; the temp branch is preserved.
type MergeError struct{ Message string }

func (e *MergeError) Error() string { return e.Message }
```
In `Timeouts` add `MergeToHost time.Duration` and `CopyToWorktree time.Duration`. In `Options` add `HostWorktreePath string`, `ApplyToHost func(ctx context.Context) error`, `KeepSourceBranch bool`, and change the `Branch` doc to "the branch commits are collected from; empty selects merge-to-head".

Replace the body of `Run` from `baseHead, err := gitx.Head(...)` to the end with:
```go
	hostWorktree := opts.HostWorktreePath
	if hostWorktree == "" {
		hostWorktree = opts.SandboxRepoDir
	}
	mergeTimeout := orDefault(opts.Timeouts.MergeToHost, defaultMergeToHostTimeout)

	hostCurrentBranch := ""
	if opts.Branch == "" {
		hostCurrentBranch, err = gitx.CurrentBranch(ctx, opts.HostRepoDir)
		if err != nil {
			return zero, err
		}
	}
	// Base HEAD comes from the host worktree: for isolated providers the host
	// SHAs are the ones commit collection must count from.
	baseHead, err := gitx.Head(ctx, hostWorktree)
	if err != nil {
		return zero, err
	}

	value, err := work(Context{Sandbox: h, SandboxRepoDir: opts.SandboxRepoDir, BaseHead: baseHead})
	if err != nil {
		return zero, err
	}

	if opts.ApplyToHost != nil {
		if err := opts.Display.Task("Syncing commits to host", func(func(string)) error { return opts.ApplyToHost(ctx) }); err != nil {
			return zero, err
		}
	}

	if opts.Branch != "" {
		commits, err := collectCommits(ctx, opts, baseHead, "refs/heads/"+opts.Branch, collectTimeout)
		if err != nil {
			return zero, err
		}
		return Result[A]{Value: value, Branch: opts.Branch, Commits: commits}, nil
	}

	// Merge-to-head: merge the worktree's branch into the host branch. A merge
	// (not cherry-pick) handles both fast-forward and diverged host branches,
	// and temp branches that themselves contain merge commits.
	newCommits, _ := gitx.RevList(ctx, hostWorktree, baseHead, "HEAD")
	if !opts.KeepSourceBranch {
		if _, err := execOK(ctx, h, "git checkout --detach", opts.SandboxRepoDir, false); err != nil {
			return zero, err
		}
	}
	if len(newCommits) > 0 {
		err := opts.Display.Task("Merging to "+hostCurrentBranch, func(func(string)) error {
			mctx, cancel := context.WithTimeout(ctx, mergeTimeout)
			defer cancel()
			if _, err := gitx.Run(mctx, opts.HostRepoDir, "merge", resolvedBranch); err != nil {
				if mctx.Err() == context.DeadlineExceeded {
					return &MergeToHostTimeoutError{SourceBranch: resolvedBranch, TargetBranch: hostCurrentBranch, Timeout: mergeTimeout}
				}
				return &MergeError{Message: fmt.Sprintf("Merge of '%s' onto '%s' failed. The temporary branch '%s' has been preserved. To retry: git merge %s, then clean up: git branch -D %s", resolvedBranch, hostCurrentBranch, resolvedBranch, resolvedBranch, resolvedBranch)}
			}
			return nil
		})
		if err != nil {
			return zero, err
		}
	}
	if !opts.KeepSourceBranch {
		_, _ = gitx.Run(ctx, opts.HostRepoDir, "branch", "-D", resolvedBranch)
	}
	commits, err := collectCommits(ctx, opts, baseHead, "HEAD", collectTimeout)
	if err != nil {
		return zero, err
	}
	return Result[A]{Value: value, Branch: hostCurrentBranch, Commits: commits}, nil
}

func collectCommits(ctx context.Context, opts Options, baseHead, to string, timeout time.Duration) ([]string, error) {
	var commits []string
	err := opts.Display.Task("Collecting commits", func(func(string)) error {
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var listErr error
		commits, listErr = gitx.RevList(cctx, opts.HostRepoDir, baseHead, to)
		if cctx.Err() == context.DeadlineExceeded {
			return &CommitCollectionTimeoutError{Timeout: timeout}
		}
		return listErr
	})
	return commits, err
}
```
`resolvedBranch` is the branch checked out inside the sandbox. Inside the "Setting up sandbox" task, after the git config commands and before the hooks, add:
```go
		out, err := execWithGitTimeout(ctx, h, "git rev-parse --abbrev-ref HEAD", opts.SandboxRepoDir, gitTimeout)
		if err != nil {
			return err
		}
		resolvedBranch = strings.TrimSpace(out.Stdout)
```
with `var resolvedBranch string` declared before the task.

- [ ] **Step 4: Alias the new errors from the root**

In `errors.go` add:
```go
// MergeToHostTimeoutError reports a merge-to-head that ran longer than its timeout.
type MergeToHostTimeoutError = lifecycle.MergeToHostTimeoutError
// MergeError reports a failed merge-to-head; the temp branch is preserved.
type MergeError = lifecycle.MergeError
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/lifecycle/ ./`
Expected: PASS, including the phase 2 tests (they pass `Branch: "main"`).

- [ ] **Step 6: Commit**

```bash
git add internal/lifecycle/ errors.go
git commit -m "Add merge-to-head path to the sandbox lifecycle"
```

---

### Task 13: Orchestrator sandbox start, worktree mode and preserved worktrees

Port of `SandboxFactory.ts` (no-sandbox and bind-mount paths) and `startSandbox.ts`. Each iteration now: optionally creates a worktree, copies paths, runs `HostOnWorktreeReady` hooks, resolves git mounts, starts the sandbox, runs the lifecycle, closes the sandbox, and removes or preserves the worktree.

**Files:**
- Create: `internal/orchestrator/start.go`, `internal/orchestrator/start_test.go`
- Modify: `internal/orchestrator/orchestrator.go`, `internal/orchestrator/invoke.go` (preserved path on errors), `internal/orchestrator/fakes_test.go`, `internal/orchestrator/orchestrator_test.go`

**Interfaces:**
- `orchestrator.Options` changes: `Sandbox sandbox.Provider` (was `NoSandboxProvider`); adds `BranchStrategy sandbox.BranchStrategy` (`Type` empty means head), `CopyToWorktree []string`, `Worktree *worktree.Info` (reuse an existing worktree instead of creating one per iteration; set by `Worktree.Run`), `KeepSourceBranch bool`.
- `orchestrator.Result` adds `PreservedWorktreePath string`.
- `AgentError` and `AgentIdleTimeoutError` gain `PreservedWorktreePath string`.
- New errors: `ContainerStartTimeoutError{Timeout time.Duration}` (120 s), `WorktreeError = worktree.Error` alias in root later.
- Internal:
  ```go
  type session struct {
      handle           sandbox.Handle
      hostWorktreePath string
      sandboxRepoDir   string
      lifecycleBranch  string // "" selects merge-to-head
      cleanup          func(ctx context.Context, failed bool) (preserved string)
  }
  func startSession(ctx context.Context, o Options) (*session, error)
  func startBindMount(ctx context.Context, p sandbox.BindMountProvider, hostRepoDir, worktreeOrRepoPath string, env map[string]string) (sandbox.BindMountHandle, error)
  const SandboxRepoDir = mount.SandboxRepoDir
  ```

- [ ] **Step 1: Extend the fakes**

In `fakes_test.go` add a fake bind-mount provider that records the mounts it was given and returns a `fakeHandle` whose `WorktreePath()` is `/home/agent/workspace`:
```go
type fakeBindMountProvider struct {
	mu      sync.Mutex
	handles []*fakeHandle
	created int
	opts    []sandbox.BindMountCreateOptions
}

func (p *fakeBindMountProvider) Kind() sandbox.Kind     { return sandbox.KindBindMount }
func (p *fakeBindMountProvider) Name() string           { return "fake-bind" }
func (p *fakeBindMountProvider) Env() map[string]string { return nil }
func (p *fakeBindMountProvider) SandboxHomedir() string { return "/home/agent" }
func (p *fakeBindMountProvider) Create(_ context.Context, o sandbox.BindMountCreateOptions) (sandbox.BindMountHandle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.opts = append(p.opts, o)
	h := p.handles[p.created]
	p.created++
	h.worktree = "/home/agent/workspace"
	return h, nil
}

func (f *fakeHandle) CopyFileIn(context.Context, string, string) error  { return nil }
func (f *fakeHandle) CopyFileOut(context.Context, string, string) error { return nil }
```
Give `fakeHandle` a `worktree string` field and make `WorktreePath()` return it when set, else `/fake`. Because the fake answers `git ` commands with "main", a merge-to-head run with the fake sandbox resolves the branch as "main"; the merge-to-head orchestration tests therefore use the real `nosandbox` provider.

- [ ] **Step 2: Write the failing tests**

`internal/orchestrator/start_test.go`:
```go
package orchestrator

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/testrepo"
	"github.com/e11en/agentgopher/internal/worktree"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

func TestHeadModeBindMountPassesWorktreeAndGitMounts(t *testing.T) {
	repo := testrepo.Init(t)
	p := &fakeBindMountProvider{handles: []*fakeHandle{{script: []scriptedLine{{0, "RESULT " + DefaultCompletionSignal}}}}}
	o, _ := baseOptions(t)
	o.HostRepoDir = repo
	o.Sandbox = p
	o.Iterations = 1
	if _, err := Orchestrate(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	got := p.opts[0]
	if got.WorktreePath != repo || got.HostRepoPath != repo || len(got.Mounts) != 2 {
		t.Fatalf("opts = %+v", got)
	}
	if got.Mounts[0].SandboxPath != SandboxRepoDir || got.Mounts[1].HostPath != filepath.Join(repo, ".git") {
		t.Fatalf("mounts = %+v", got.Mounts)
	}
}

func TestMergeToHeadCreatesWorktreeMergesAndRemovesIt(t *testing.T) {
	repo := testrepo.Init(t)
	o := Options{
		HostRepoDir: repo, Iterations: 1, Prompt: "work", Provider: fakeProvider{}, Sandbox: nosandbox.New(nil),
		BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead}, Display: display.NewWriter(&bytes.Buffer{}),
	}
	// A shell "agent": commits a file in its cwd, then prints the signal.
	o.Provider = shellProvider{script: `echo agent > agent.txt && git add agent.txt && git commit -q -m "agent" && echo "RESULT ` + DefaultCompletionSignal + `"`}
	res, err := Orchestrate(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "main" || len(res.Commits) != 1 || res.PreservedWorktreePath != "" {
		t.Fatalf("res = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(repo, "agent.txt")); err != nil {
		t.Fatal("not merged into main")
	}
	entries, _ := os.ReadDir(filepath.Join(repo, worktree.Dir))
	if len(entries) != 0 {
		t.Fatalf("worktree not removed: %v", entries)
	}
}

func TestNamedBranchKeepsCommitsOnBranch(t *testing.T) {
	repo := testrepo.Init(t)
	o := Options{
		HostRepoDir: repo, Iterations: 1, Prompt: "work", Sandbox: nosandbox.New(nil), Display: display.NewWriter(&bytes.Buffer{}),
		BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: "feat/agent"},
		Provider:       shellProvider{script: `echo x > f.txt && git add f.txt && git commit -q -m "agent" && echo "RESULT ` + DefaultCompletionSignal + `"`},
	}
	res, err := Orchestrate(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "feat/agent" || len(res.Commits) != 1 {
		t.Fatalf("res = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(repo, "f.txt")); err == nil {
		t.Fatal("main must not change")
	}
}

func TestDirtyWorktreeIsPreservedOnSuccessAndFailure(t *testing.T) {
	repo := testrepo.Init(t)
	base := Options{
		HostRepoDir: repo, Iterations: 1, Prompt: "work", Sandbox: nosandbox.New(nil), Display: display.NewWriter(&bytes.Buffer{}),
		BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead},
	}
	o := base
	o.Provider = shellProvider{script: `echo dirty > dirty.txt && echo "RESULT ` + DefaultCompletionSignal + `"`}
	res, err := Orchestrate(context.Background(), o)
	if err != nil || res.PreservedWorktreePath == "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, statErr := os.Stat(filepath.Join(res.PreservedWorktreePath, "dirty.txt")); statErr != nil {
		t.Fatal("preserved worktree missing dirty file")
	}

	o = base
	o.Provider = shellProvider{script: `echo dirty > dirty.txt && exit 3`}
	_, err = Orchestrate(context.Background(), o)
	var aerr *AgentError
	if !errors.As(err, &aerr) || aerr.PreservedWorktreePath == "" {
		t.Fatalf("err = %v", err)
	}
}

func TestCopyToWorktreeAndWorktreeReadyHook(t *testing.T) {
	repo := testrepo.Init(t)
	os.MkdirAll(filepath.Join(repo, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(repo, "node_modules", "x", "i.js"), []byte("1"), 0o644)
	o := Options{
		HostRepoDir: repo, Iterations: 1, Prompt: "work", Sandbox: nosandbox.New(nil), Display: display.NewWriter(&bytes.Buffer{}),
		BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead}, CopyToWorktree: []string{"node_modules"},
		Provider: shellProvider{script: `test -f node_modules/x/i.js && test -f hook.txt && echo "RESULT ` + DefaultCompletionSignal + `"`},
	}
	o.Hooks.HostOnWorktreeReady = []lifecycle.HostHook{{Command: "touch hook.txt"}}
	res, err := Orchestrate(context.Background(), o)
	if err != nil || res.CompletionSignal == "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestIsolatedProviderRejected(t *testing.T) {
	o, _ := baseOptions(t)
	o.Sandbox = isolatedStub{}
	_, err := Orchestrate(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "phase 4") {
		t.Fatalf("err = %v", err)
	}
}
```

Add to `fakes_test.go`:
```go
// shellProvider runs a shell script as the "agent" so tests can commit files.
type shellProvider struct{ script string }

func (shellProvider) Name() string           { return "shell" }
func (shellProvider) Env() map[string]string { return nil }
func (shellProvider) CaptureSessions() bool  { return false }
func (p shellProvider) BuildPrintCommand(agent.CommandOptions) agent.PrintCommand {
	return agent.PrintCommand{Command: p.script}
}
func (shellProvider) ParseStreamLine(line string) []agent.StreamEvent {
	return fakeProvider{}.ParseStreamLine(line)
}

type isolatedStub struct{}

func (isolatedStub) Kind() sandbox.Kind     { return sandbox.KindIsolated }
func (isolatedStub) Name() string           { return "iso" }
func (isolatedStub) Env() map[string]string { return nil }
```
Add `"github.com/e11en/agentgopher/internal/lifecycle"` to the start test imports. `baseOptions` in `orchestrator_test.go` keeps working: `&fakeNoSandboxProvider{}` satisfies `sandbox.Provider`.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/orchestrator/`
Expected: FAIL: unknown fields `BranchStrategy`, `CopyToWorktree`, `PreservedWorktreePath`.

- [ ] **Step 4: Write start.go**

```go
package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/internal/mount"
	"github.com/e11en/agentgopher/internal/worktree"
	"github.com/e11en/agentgopher/sandbox"
)

// SandboxRepoDir is where the worktree is mounted inside a container.
const SandboxRepoDir = mount.SandboxRepoDir

const containerStartTimeout = 120 * time.Second

// ContainerStartTimeoutError reports a provider Create that ran longer than Timeout.
type ContainerStartTimeoutError struct{ Timeout time.Duration }

func (e *ContainerStartTimeoutError) Error() string {
	return fmt.Sprintf("sandbox container start timed out after %v", e.Timeout)
}

type session struct {
	handle           sandbox.Handle
	hostWorktreePath string
	sandboxRepoDir   string
	lifecycleBranch  string
	cleanup          func(ctx context.Context, failed bool) string
}

// startSession prepares the place the agent works in for one iteration.
func startSession(ctx context.Context, o Options) (*session, error) {
	headMode := o.BranchStrategy.Type == "" || o.BranchStrategy.Type == sandbox.BranchHead
	if headMode {
		if err := lifecycle.RunHostHooks(ctx, o.Hooks.HostOnWorktreeReady, o.HostRepoDir); err != nil {
			return nil, err
		}
		h, err := startHandle(ctx, o, o.HostRepoDir)
		if err != nil {
			return nil, err
		}
		return &session{
			handle: h, hostWorktreePath: o.HostRepoDir, sandboxRepoDir: h.WorktreePath(), lifecycleBranch: o.Branch,
			cleanup: func(ctx context.Context, _ bool) string { _ = h.Close(ctx); return "" },
		}, nil
	}

	wt := o.Worktree
	owned := wt == nil
	if owned {
		if err := worktree.Prune(ctx, o.HostRepoDir); err != nil {
			fmt.Fprintln(os.Stderr, "[agentgopher] Warning: failed to prune stale worktrees:", err)
		}
		var info worktree.Info
		var err error
		if o.BranchStrategy.Type == sandbox.BranchNamed {
			info, err = worktree.Create(ctx, o.HostRepoDir, o.BranchStrategy.Branch, o.BranchStrategy.BaseBranch)
		} else {
			info, err = worktree.CreateTemp(ctx, o.HostRepoDir, o.Name)
		}
		if err != nil {
			return nil, err
		}
		wt = &info
	}
	removeWorktree := func(ctx context.Context, failed bool) string {
		if !owned {
			return ""
		}
		defer wt.Unlock()
		dirty, _ := worktree.HasUncommittedChanges(ctx, wt.Path)
		if dirty {
			reason := "Worktree preserved at " + wt.Path
			if !failed {
				reason = "Run succeeded but worktree has uncommitted changes at " + wt.Path
			}
			fmt.Fprintf(os.Stderr, "\n%s\n  To review: cd %s\n  To clean up: git worktree remove --force %s\n", reason, wt.Path, wt.Path)
			return wt.Path
		}
		if failed {
			fmt.Fprintln(os.Stderr, "\nWorktree removed (no uncommitted changes)")
		}
		_ = worktree.Remove(ctx, wt.Path)
		return ""
	}

	fail := func(err error) (*session, error) {
		removeWorktree(ctx, true)
		return nil, err
	}
	if owned && len(o.CopyToWorktree) > 0 {
		err := o.Display.Task("Copying to worktree", func(func(string)) error {
			return worktree.CopyToWorktree(ctx, o.CopyToWorktree, o.HostRepoDir, wt.Path, o.Timeouts.CopyToWorktree)
		})
		if err != nil {
			return fail(err)
		}
	}
	if owned {
		if err := lifecycle.RunHostHooks(ctx, o.Hooks.HostOnWorktreeReady, wt.Path); err != nil {
			return fail(err)
		}
	}
	h, err := startHandle(ctx, o, wt.Path)
	if err != nil {
		return fail(err)
	}
	lifecycleBranch := ""
	if o.BranchStrategy.Type == sandbox.BranchNamed {
		lifecycleBranch = wt.Branch
	}
	return &session{
		handle: h, hostWorktreePath: wt.Path, sandboxRepoDir: h.WorktreePath(), lifecycleBranch: lifecycleBranch,
		cleanup: func(ctx context.Context, failed bool) string {
			_ = h.Close(ctx)
			return removeWorktree(ctx, failed)
		},
	}, nil
}

// startHandle creates the provider handle for worktreeOrRepoPath.
func startHandle(ctx context.Context, o Options, worktreeOrRepoPath string) (sandbox.Handle, error) {
	switch p := o.Sandbox.(type) {
	case sandbox.NoSandboxProvider:
		h, err := p.Create(ctx, sandbox.NoSandboxCreateOptions{WorktreePath: worktreeOrRepoPath, Env: o.Env})
		if err != nil {
			return nil, fmt.Errorf("provider '%s' create failed: %w", p.Name(), err)
		}
		return h, nil
	case sandbox.BindMountProvider:
		return startBindMount(ctx, p, o.HostRepoDir, worktreeOrRepoPath, o.Env)
	default:
		return nil, fmt.Errorf("sandbox provider kind %q is not supported yet; isolated providers arrive in phase 4", o.Sandbox.Kind())
	}
}

// startBindMount resolves the worktree and git mounts, patches them for
// Windows, and asks the provider for a container.
func startBindMount(ctx context.Context, p sandbox.BindMountProvider, hostRepoDir, worktreeOrRepoPath string, env map[string]string) (sandbox.BindMountHandle, error) {
	gitMounts, err := mount.ResolveGitMounts(filepath.Join(hostRepoDir, ".git"))
	if err != nil {
		return nil, &worktree.Error{Message: "failed to resolve git mounts: " + err.Error()}
	}
	gitMounts, err = mount.PatchGitMountsForWindows(gitMounts, worktreeOrRepoPath, SandboxRepoDir, mount.OSFS{}, runtime.GOOS)
	if err != nil {
		return nil, &worktree.Error{Message: err.Error()}
	}
	raw := append([]sandbox.Mount{{HostPath: worktreeOrRepoPath, SandboxPath: SandboxRepoDir}}, gitMounts...)
	mounts := mount.NormalizeMounts(raw, worktreeOrRepoPath, SandboxRepoDir, runtime.GOOS)
	wtPath := worktreeOrRepoPath
	if runtime.GOOS == "windows" {
		wtPath = strings.ReplaceAll(wtPath, `\`, "/")
	}
	cctx, cancel := context.WithTimeout(ctx, containerStartTimeout)
	defer cancel()
	h, err := p.Create(cctx, sandbox.BindMountCreateOptions{WorktreePath: wtPath, HostRepoPath: hostRepoDir, Mounts: mounts, Env: env})
	if cctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		return nil, &ContainerStartTimeoutError{Timeout: containerStartTimeout}
	}
	if err != nil {
		return nil, &worktree.Error{Message: fmt.Sprintf("provider '%s' create failed: %v", p.Name(), err)}
	}
	return h, nil
}
```

- [ ] **Step 5: Wire it into orchestrator.go and invoke.go**

In `invoke.go` add `PreservedWorktreePath string` to both `AgentError` and `AgentIdleTimeoutError` (messages unchanged).

In `orchestrator.go`:
- Change `Sandbox sandbox.NoSandboxProvider` to `Sandbox sandbox.Provider`; add `BranchStrategy sandbox.BranchStrategy`, `CopyToWorktree []string`, `Worktree *worktree.Info`, `KeepSourceBranch bool` to `Options`; add `PreservedWorktreePath string` to `Result`.
- Replace the block from `if err := lifecycle.RunHostHooks(...)` through `_ = h.Close(ctx)` with:
```go
		s, err := startSession(ctx, o)
		if err != nil {
			return result, err
		}
		lc, err := lifecycle.Run(ctx, lifecycle.Options{
			HostRepoDir:      o.HostRepoDir,
			SandboxRepoDir:   s.sandboxRepoDir,
			HostWorktreePath: s.hostWorktreePath,
			Branch:           s.lifecycleBranch,
			KeepSourceBranch: o.KeepSourceBranch,
			Hooks:            o.Hooks,
			Timeouts:         o.Timeouts,
			Display:          o.Display,
		}, s.handle, func(lctx lifecycle.Context) (iterationOutcome, error) {
			return runIteration(ctx, o, lctx, i, signals, idle, completion, label)
		})
		preserved := s.cleanup(ctx, err != nil)
		result.PreservedWorktreePath = preserved
		if err != nil {
			return result, attachPreserved(err, preserved)
		}
```
- Add:
```go
// attachPreserved copies the preserved worktree path onto the agent errors so
// callers can build on the preserved worktree.
func attachPreserved(err error, path string) error {
	if path == "" {
		return err
	}
	var aerr *AgentError
	if errors.As(err, &aerr) {
		aerr.PreservedWorktreePath = path
	}
	var ierr *AgentIdleTimeoutError
	if errors.As(err, &ierr) {
		ierr.PreservedWorktreePath = path
	}
	return err
}
```
- In head mode `s.lifecycleBranch` is `o.Branch` (the host branch, as before), so the phase 2 tests keep passing.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./internal/orchestrator/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/orchestrator/
git commit -m "Start sandboxes in head or worktree mode with bind-mount providers"
```

---

### Task 14: Public `Run` with branch strategies

Port of the remaining `run()` logic: strategy defaults and validation, temp branch name for `SOURCE_BRANCH` and the log filename, `CopyToWorktree`, `PreservedWorktreePath`, `Sandbox` as `sandbox.Provider`.

**Files:**
- Modify: `agentgopher.go`, `agentgopher_test.go`, `errors.go`

**Interfaces:**
- `RunOptions.Sandbox` is `sandbox.Provider`; `RunOptions` gains `CopyToWorktree []string`; `BranchStrategy *sandbox.BranchStrategy` now accepts all three types (nil means head for no-sandbox and bind-mount providers; isolated providers are rejected until phase 4).
- `RunResult` gains `PreservedWorktreePath string`.
- `errors.go` gains aliases: `WorktreeError = worktree.Error`, `WorktreeTimeoutError = worktree.TimeoutError`, `WorktreeLockedError = worktree.LockedError`, `CopyToWorktreeError = worktree.CopyError`, `CopyToWorktreeTimeoutError = worktree.CopyTimeoutError`, `ContainerStartTimeoutError = orchestrator.ContainerStartTimeoutError`, `ContainerError = container.Error`.
- Internal: `runWith(ctx, o RunOptions, wt *worktree.Info, keepSourceBranch bool) (RunResult, error)`; `Run` calls `runWith(ctx, o, nil, false)`. Task 15 reuses it.

- [ ] **Step 1: Write the failing tests (append to agentgopher_test.go)**

```go
func TestRunMergeToHeadEndToEnd(t *testing.T) {
	repo := testrepo.Init(t)
	pf := filepath.Join(repo, "p.md")
	os.WriteFile(pf, []byte("source={{SOURCE_BRANCH}} target={{TARGET_BRANCH}}\n"), 0o644)
	res, err := Run(context.Background(), RunOptions{
		Agent:          echoAgent{script: `cat > prompt.txt; git add prompt.txt; git commit -q -m "agent"; echo "RESULT <promise>COMPLETE</promise>"`},
		Sandbox:        nosandbox.New(nil),
		Cwd:            repo,
		PromptFile:     pf,
		Name:           "worker",
		BranchStrategy: &sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "main" || len(res.Commits) != 1 || res.PreservedWorktreePath != "" {
		t.Fatalf("res = %+v", res)
	}
	b, _ := os.ReadFile(filepath.Join(repo, "prompt.txt"))
	if !strings.HasPrefix(string(b), "source=agentgopher/worker/") || !strings.Contains(string(b), "target=main") {
		t.Fatalf("prompt seen by agent: %q", b)
	}
	if !strings.HasPrefix(filepath.Base(res.LogFilePath), "main-agentgopher-worker-") {
		t.Fatalf("LogFilePath = %q", res.LogFilePath)
	}
}

func TestRunStrategyValidation(t *testing.T) {
	repo := testrepo.Init(t)
	base := RunOptions{Agent: echoAgent{script: "true"}, Sandbox: nosandbox.New(nil), Cwd: repo, Prompt: "x"}
	o := base
	o.CopyToWorktree = []string{"node_modules"}
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "copyToWorktree is not supported with head") {
		t.Fatalf("head+copy: %v", err)
	}
	o = base
	o.Sandbox = isolatedStub{}
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "phase 4") {
		t.Fatalf("isolated: %v", err)
	}
}

type isolatedStub struct{}

func (isolatedStub) Kind() sandbox.Kind     { return sandbox.KindIsolated }
func (isolatedStub) Name() string           { return "iso" }
func (isolatedStub) Env() map[string]string { return nil }
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./`
Expected: FAIL: unknown field `CopyToWorktree`, `isolatedStub` does not satisfy `NoSandboxProvider`.

- [ ] **Step 3: Update agentgopher.go**

Change the field types and add fields:
```go
	Sandbox sandbox.Provider
	// CopyToWorktree lists paths relative to the repo root copied into the
	// worktree before the sandbox starts. Not allowed with the head strategy.
	CopyToWorktree []string
	// BranchStrategy defaults to head. Isolated providers (phase 4) default to merge-to-head.
	BranchStrategy *sandbox.BranchStrategy
```
and `PreservedWorktreePath string` on `RunResult`.

Rename the body of `Run` to `runWith` and replace the strategy handling at the top:
```go
// Run drives the agent for up to MaxIterations iterations and returns what it produced.
func Run(ctx context.Context, o RunOptions) (RunResult, error) { return runWith(ctx, o, nil, false) }

func runWith(ctx context.Context, o RunOptions, wt *worktree.Info, keepSourceBranch bool) (RunResult, error) {
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}
	maxIterations := o.MaxIterations
	if maxIterations == 0 {
		maxIterations = DefaultMaxIterations
	}
	if o.Sandbox.Kind() == sandbox.KindIsolated {
		return RunResult{}, fmt.Errorf("sandbox provider kind %q is not supported yet; isolated providers arrive in phase 4", o.Sandbox.Kind())
	}
	strategy := sandbox.BranchStrategy{Type: sandbox.BranchHead}
	if o.BranchStrategy != nil {
		strategy = *o.BranchStrategy
	}
	if wt != nil {
		strategy.Type = sandbox.BranchNamed
		strategy.Branch = wt.Branch
		if keepSourceBranch {
			strategy.Type = sandbox.BranchMergeToHead
		}
	}
	if strategy.Type == sandbox.BranchHead && len(o.CopyToWorktree) > 0 {
		return RunResult{}, fmt.Errorf("copyToWorktree is not supported with head branch strategy. In head mode the host working directory is bind-mounted directly")
	}
```
After `currentBranch` is known, compute the branch names:
```go
	resolvedBranch := currentBranch
	targetBranch := ""
	switch {
	case wt != nil:
		resolvedBranch = wt.Branch
	case strategy.Type == sandbox.BranchNamed:
		resolvedBranch = strategy.Branch
	case strategy.Type == sandbox.BranchMergeToHead:
		// shortcut: this name is only for SOURCE_BRANCH and the log filename; the
		// orchestrator generates its own temp branch per iteration, as upstream does.
		resolvedBranch = worktree.GenerateTempBranchName(o.Name)
		targetBranch = currentBranch
	}
```
Use `resolvedBranch` for the default log path (`BuildLogFilename(resolvedBranch, targetBranch, o.Name)`), the "Branch" summary row, `printFileStartup` and `SOURCE_BRANCH`; keep `TARGET_BRANCH: currentBranch`. In the `orchestrator.Options` literal set:
```go
		Branch:         currentBranch,
		BranchStrategy: strategy,
		CopyToWorktree: o.CopyToWorktree,
		Worktree:       wt,
		KeepSourceBranch: keepSourceBranch,
```
and copy `result.PreservedWorktreePath` into `RunResult`. Add the imports for `worktree`.

- [ ] **Step 4: Add the error aliases to errors.go**

```go
type WorktreeError = worktree.Error
type WorktreeTimeoutError = worktree.TimeoutError
type WorktreeLockedError = worktree.LockedError
type CopyToWorktreeError = worktree.CopyError
type CopyToWorktreeTimeoutError = worktree.CopyTimeoutError
type ContainerStartTimeoutError = orchestrator.ContainerStartTimeoutError
type ContainerError = container.Error
```
each with a one-line doc comment.

- [ ] **Step 5: Run everything**

Run: `go test -race ./... && go vet ./... && golangci-lint run ./...`
Expected: PASS, no lint output.

- [ ] **Step 6: Commit**

```bash
git add agentgopher.go agentgopher_test.go errors.go
git commit -m "Support merge-to-head and named branches in Run"
```

---

### Task 15: `CreateWorktree`

Port of `createWorktree.ts` without `interactive` and `createSandbox` (phase 5).

**Files:**
- Create: `worktree.go`, `worktree_test.go`

**Interfaces:**
- Produces:
  ```go
  type CreateWorktreeOptions struct {
      BranchStrategy sandbox.BranchStrategy // BranchMergeToHead or BranchNamed; head is rejected
      Cwd            string
      CopyToWorktree []string
      Hooks          Hooks                  // only HostOnWorktreeReady runs here
      Timeouts       Timeouts
  }
  type WorktreeRunOptions struct {
      Agent agent.Provider; Sandbox sandbox.Provider; Prompt, PromptFile string; PromptArgs map[string]string
      MaxIterations int; CompletionSignal []string; IdleTimeout, CompletionTimeout time.Duration
      Name string; Logging *Logging; Hooks Hooks
  }
  type Worktree struct { Branch, Path string; /* unexported: hostRepoDir, info, mergeToHead, closed */ }
  func CreateWorktree(ctx context.Context, o CreateWorktreeOptions) (*Worktree, error)
  func (w *Worktree) Run(ctx context.Context, o WorktreeRunOptions) (RunResult, error)
  func (w *Worktree) Close(ctx context.Context) error // removes a clean worktree, keeps a dirty one; idempotent
  ```
- `Worktree.Run` with merge-to-head merges into the host branch after every run and keeps the worktree's branch (`KeepSourceBranch`); with a named branch commits stay on it.

- [ ] **Step 1: Write the failing tests**

`worktree_test.go`:
```go
package agentgopher

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/testrepo"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

func TestCreateWorktreeNamedBranchRunTwiceAndClose(t *testing.T) {
	repo := testrepo.Init(t)
	wt, err := CreateWorktree(context.Background(), CreateWorktreeOptions{
		BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: "feat/wt"},
		Cwd:            repo,
		Hooks:          Hooks{HostOnWorktreeReady: []HostHook{{Command: "touch ready.txt"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if wt.Branch != "feat/wt" || !strings.HasSuffix(wt.Path, filepath.Join(".agentgopher", "worktrees", "feat-wt")) {
		t.Fatalf("wt = %+v", wt)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "ready.txt")); err != nil {
		t.Fatal("onWorktreeReady hook did not run")
	}
	script := `n=$(ls *.step 2>/dev/null | wc -l); touch "$n.step"; git add .; git commit -q -m step; echo "RESULT <promise>COMPLETE</promise>"`
	for i := 0; i < 2; i++ {
		res, err := wt.Run(context.Background(), WorktreeRunOptions{Agent: echoAgent{script: script}, Sandbox: nosandbox.New(nil), Prompt: "go"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Branch != "feat/wt" || len(res.Commits) != 1 {
			t.Fatalf("run %d: %+v", i, res)
		}
	}
	if err := wt.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt.Path); err == nil {
		t.Fatal("clean worktree must be removed on Close")
	}
	if err := wt.Close(context.Background()); err != nil {
		t.Fatal("Close must be idempotent")
	}
}

func TestCreateWorktreeMergeToHeadKeepsBranchAcrossRuns(t *testing.T) {
	repo := testrepo.Init(t)
	wt, err := CreateWorktree(context.Background(), CreateWorktreeOptions{BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead}, Cwd: repo})
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Close(context.Background())
	script := `echo x >> log.txt; git add log.txt; git commit -q -m step; echo "RESULT <promise>COMPLETE</promise>"`
	for i := 0; i < 2; i++ {
		res, err := wt.Run(context.Background(), WorktreeRunOptions{Agent: echoAgent{script: script}, Sandbox: nosandbox.New(nil), Prompt: "go"})
		if err != nil || res.Branch != "main" {
			t.Fatalf("run %d: %+v, %v", i, res, err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(repo, "log.txt"))
	if strings.Count(string(b), "x") != 2 {
		t.Fatalf("main log.txt = %q", b)
	}
	if got := strings.TrimSpace(testrepo.Git(t, wt.Path, "rev-parse", "--abbrev-ref", "HEAD")); got != wt.Branch {
		t.Fatalf("worktree left on %q", got)
	}
}

func TestCreateWorktreeRejectsHead(t *testing.T) {
	_, err := CreateWorktree(context.Background(), CreateWorktreeOptions{BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchHead}, Cwd: testrepo.Init(t)})
	if err == nil || !strings.Contains(err.Error(), "head") {
		t.Fatalf("err = %v", err)
	}
}

func TestCloseKeepsDirtyWorktree(t *testing.T) {
	repo := testrepo.Init(t)
	wt, _ := CreateWorktree(context.Background(), CreateWorktreeOptions{BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: "dirty"}, Cwd: repo})
	os.WriteFile(filepath.Join(wt.Path, "wip.txt"), []byte("x"), 0o644)
	if err := wt.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatal("dirty worktree must be preserved")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./ -run Worktree`
Expected: FAIL, "undefined: CreateWorktree".

- [ ] **Step 3: Write worktree.go**

```go
package agentgopher

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/internal/worktree"
	"github.com/e11en/agentgopher/sandbox"
)

// CreateWorktreeOptions configure CreateWorktree.
type CreateWorktreeOptions struct {
	// BranchStrategy must be BranchMergeToHead or BranchNamed.
	BranchStrategy sandbox.BranchStrategy
	Cwd            string
	CopyToWorktree []string
	// Hooks: only HostOnWorktreeReady runs here; the rest are passed to Run.
	Hooks    Hooks
	Timeouts Timeouts
}

// WorktreeRunOptions configure Worktree.Run. Sandbox is required.
type WorktreeRunOptions struct {
	Agent             agent.Provider
	Sandbox           sandbox.Provider
	Prompt            string
	PromptFile        string
	PromptArgs        map[string]string
	MaxIterations     int
	CompletionSignal  []string
	IdleTimeout       time.Duration
	CompletionTimeout time.Duration
	Name              string
	Logging           *Logging
	Hooks             Hooks
}

// Worktree is a long-lived git worktree to run agents in.
type Worktree struct {
	Branch string
	Path   string

	hostRepoDir string
	info        worktree.Info
	mergeToHead bool
	closeOnce   sync.Once
}

// CreateWorktree creates (or reuses) a worktree for the strategy, copies
// CopyToWorktree into it and runs HostOnWorktreeReady hooks.
func CreateWorktree(ctx context.Context, o CreateWorktreeOptions) (*Worktree, error) {
	if o.BranchStrategy.Type != sandbox.BranchMergeToHead && o.BranchStrategy.Type != sandbox.BranchNamed {
		return nil, fmt.Errorf("CreateWorktree requires the merge-to-head or branch strategy, got %q (head has no worktree)", o.BranchStrategy.Type)
	}
	hostRepoDir, err := resolveCwd(o.Cwd)
	if err != nil {
		return nil, err
	}
	_ = worktree.Prune(ctx, hostRepoDir)
	var info worktree.Info
	if o.BranchStrategy.Type == sandbox.BranchNamed {
		info, err = worktree.Create(ctx, hostRepoDir, o.BranchStrategy.Branch, o.BranchStrategy.BaseBranch)
	} else {
		info, err = worktree.CreateTemp(ctx, hostRepoDir, "")
	}
	if err != nil {
		return nil, err
	}
	if len(o.CopyToWorktree) > 0 {
		if err := worktree.CopyToWorktree(ctx, o.CopyToWorktree, hostRepoDir, info.Path, o.Timeouts.CopyToWorktree); err != nil {
			info.Unlock()
			return nil, err
		}
	}
	if err := lifecycle.RunHostHooks(ctx, o.Hooks.HostOnWorktreeReady, info.Path); err != nil {
		info.Unlock()
		return nil, err
	}
	return &Worktree{Branch: info.Branch, Path: info.Path, hostRepoDir: hostRepoDir, info: info, mergeToHead: o.BranchStrategy.Type == sandbox.BranchMergeToHead}, nil
}

// Run runs an agent in this worktree. With merge-to-head the commits are
// merged into the host branch after the run and the worktree stays on its
// branch for the next Run.
func (w *Worktree) Run(ctx context.Context, o WorktreeRunOptions) (RunResult, error) {
	if o.Sandbox == nil {
		return RunResult{}, fmt.Errorf("Worktree.Run requires a Sandbox")
	}
	return runWith(ctx, RunOptions{
		Agent: o.Agent, Sandbox: o.Sandbox, Cwd: w.hostRepoDir, Prompt: o.Prompt, PromptFile: o.PromptFile,
		PromptArgs: o.PromptArgs, MaxIterations: o.MaxIterations, CompletionSignal: o.CompletionSignal,
		IdleTimeout: o.IdleTimeout, CompletionTimeout: o.CompletionTimeout, Name: o.Name, Logging: o.Logging, Hooks: o.Hooks,
	}, &w.info, w.mergeToHead)
}

// Close removes the worktree when it is clean and keeps it when it has
// uncommitted changes. Calling Close twice is safe.
func (w *Worktree) Close(ctx context.Context) error {
	var err error
	w.closeOnce.Do(func() {
		defer w.info.Unlock()
		dirty, derr := worktree.HasUncommittedChanges(ctx, w.Path)
		if derr == nil && dirty {
			return
		}
		err = worktree.Remove(ctx, w.Path)
	})
	return err
}
```

`runWith` (Task 14) with `wt != nil` passes `Worktree: wt` and `KeepSourceBranch: keepSourceBranch` to the orchestrator; with `keepSourceBranch` the strategy is merge-to-head so the lifecycle branch is empty, and the orchestrator does not create or remove the worktree because `Options.Worktree` is set. `SOURCE_BRANCH` and `TARGET_BRANCH` both equal the worktree's branch for an existing worktree, as upstream does; adjust the `resolvedBranch` switch in `runWith` so that `wt != nil` also sets `TARGET_BRANCH` to `wt.Branch`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add worktree.go worktree_test.go agentgopher.go
git commit -m "Add CreateWorktree with Run and Close"
```

---

### Task 16: Docker example, README and CI

**Files:**
- Create: `examples/docker/main.go`, `examples/docker/Dockerfile`
- Modify: `README.md`, `.github/workflows/ci.yml`

- [ ] **Step 1: Write the example and Dockerfile**

`examples/docker/Dockerfile` (the phase 6 init templates will supersede this):
```dockerfile
FROM node:22-bookworm
ARG AGENT_UID=1000
ARG AGENT_GID=1000
RUN apt-get update && apt-get install -y --no-install-recommends git sudo && rm -rf /var/lib/apt/lists/* \
  && groupmod -o -g $AGENT_GID node && usermod -o -u $AGENT_UID -g $AGENT_GID -d /home/agent -m -l agent node \
  && echo "agent ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/agent \
  && npm install -g @anthropic-ai/claude-code
USER ${AGENT_UID}:${AGENT_GID}
WORKDIR /home/agent/workspace
```

`examples/docker/main.go`:
```go
// Command docker runs Claude Code in a Docker container on a temp branch and
// merges the result back. Build the image first:
//
//	docker build -t agentgopher:example --build-arg AGENT_UID=$(id -u) --build-arg AGENT_GID=$(id -g) examples/docker
//
// Usage: go run ./examples/docker [path-to-repo]
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/e11en/agentgopher"
	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/docker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	repo := "."
	if len(os.Args) > 1 {
		repo = os.Args[1]
	}
	promptFile, _ := filepath.Abs("examples/simple/prompt.md")
	res, err := agentgopher.Run(ctx, agentgopher.RunOptions{
		Name:           "worker",
		Agent:          agent.ClaudeCode(agent.DefaultModel, nil),
		Sandbox:        docker.New(&docker.Options{ImageName: "agentgopher:example"}),
		Cwd:            repo,
		PromptFile:     promptFile,
		BranchStrategy: &sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "run failed:", err)
		os.Exit(1)
	}
	fmt.Printf("done: branch=%s commits=%d preserved=%q log=%s\n", res.Branch, len(res.Commits), res.PreservedWorktreePath, res.LogFilePath)
}
```

- [ ] **Step 2: Build and run the daemon tests when Docker is available**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: PASS; the daemon tests report `SKIP` without Docker or Podman and `PASS` with them.

- [ ] **Step 3: Manual smoke run (only with Docker and a logged-in Claude Code)**

```bash
docker build -t agentgopher:example --build-arg AGENT_UID=$(id -u) --build-arg AGENT_GID=$(id -g) examples/docker
tmp=$(mktemp -d) && git -C "$tmp" init -q -b main && echo "# demo" > "$tmp/README.md" && git -C "$tmp" add . && git -C "$tmp" commit -q -m init
mkdir -p "$tmp/.agentgopher" && printf 'CLAUDE_CODE_OAUTH_TOKEN=%s\n' "$(claude setup-token 2>/dev/null | tail -1)" > "$tmp/.agentgopher/.env"
go run ./examples/docker "$tmp"
git -C "$tmp" log --oneline
```
Expected: `done: branch=main commits=1 preserved=""` and a "Touched by agentgopher" commit on `main`. Record the outcome (or that it was skipped) in the Step 5 commit message.

- [ ] **Step 4: Update README and CI**

Replace the README status paragraph with:
```markdown
Status: phases 1 to 3 of `docs/plan.md` are done. `Run` supports the head,
merge-to-head and branch strategies with the no-sandbox, Docker and Podman
providers, and `CreateWorktree` gives you a long-lived worktree. Isolated
providers, `Interactive`, `CreateSandbox` and the CLI follow in later phases.

Examples: `go run ./examples/simple /path/to/repo` (host) and
`go run ./examples/docker /path/to/repo` (Docker, see the file header for the
image build).
```

In `.github/workflows/ci.yml` add a job that runs the daemon tests on a runner that has Docker:
```yaml
  docker:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go test -race -run 'AgainstDaemon' ./sandbox/docker/
```

- [ ] **Step 5: Commit and push**

```bash
git add examples/docker README.md .github/workflows/ci.yml
git commit -m "Add Docker example and run daemon tests in CI"
git push origin main
gh run watch --repo e11en/agentgopher --exit-status
```
Expected: both CI jobs pass.

---

## Self-review notes

- Spec coverage for phase 3: worktrees and both non-head strategies (Tasks 2, 3, 5, 6, 12, 13, 14), `copyToWorktree` (Task 4, wired in 13), Docker and Podman providers (Tasks 9, 10, 11), Windows mount handling (Task 8, applied in 13), `CreateWorktree` (Task 15). Hooks already existed in phase 2 and now run in worktree mode (Task 13).
- Fix carried into phase 2 code: `InitTestRepo` lived in a `_test.go` file and was imported across packages, which does not compile; Task 1 moves it to `internal/testrepo`. Apply that change to the phase 1+2 plan's Tasks 10, 11, 13 and 14 before executing them, or run this plan's Task 1 first.
- Inherited upstream quirk, kept on purpose: in merge-to-head mode `SOURCE_BRANCH` and the log filename use a temp branch name generated by `Run`, while each iteration's worktree gets its own generated name.
- Deferred to phase 4: isolated providers (`sandbox.IsolatedProvider`, sync in and out, `ApplyToHost` is already wired in the lifecycle), `CopyToWorktree` for isolated sandboxes via `copyIn`, Vercel and Daytona.
- Deferred to phase 5: `Interactive`, `Worktree.Interactive`, `CreateSandbox`, `Worktree.CreateSandbox`, `interactiveExec` on the container handle, session capture through `BindMountHandle.CopyFileOut`, structured output, resume and fork, the spinner terminal UI, terminal cleanup on exit.
- Deferred to phase 6: `agentgopher docker build-image` and `remove-image` CLI commands (the `BuildImage` and `RemoveImage` functions exist), the Dockerfile templates with `AGENT_UID`/`AGENT_GID` build args (ADR 0014), `init`.
- Deferred to phase 7: the Kubernetes provider, which is an isolated provider and builds on phase 4.
- Left out of the port: `createSandbox-windowsMounts` and `interactive-windowsMounts` tests (their subjects are phase 5), the Node `exit` event in the shutdown registry (Go has no equivalent; `Close` and the signal handler cover the cases), `terminalCleanup` (belongs to the spinner UI in phase 5).
- Type consistency: `sandbox.Mount` is the resolved mount everywhere (`mount`, `docker`, `podman`, orchestrator); `sandbox.MountConfig` is only in provider options; `worktree.Info.Unlock` is the single lock release path used by the orchestrator and `Worktree.Close`; `lifecycle.Options.Branch == ""` is the single merge-to-head switch used by Tasks 12, 13 and 15.
