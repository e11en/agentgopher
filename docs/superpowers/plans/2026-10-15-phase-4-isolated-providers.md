# agentgopher Phase 4: Isolated Sandbox Providers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `Run` works with sandboxes that have their own filesystem: the repo is shipped in as a git bundle, the agent's commits come back as patches, and `vercel` and `daytona` are the first two built-in isolated providers.

**Architecture:** An `IsolatedHandle` adds `CopyIn` and `CopyFileOut` to the `Handle` from phase 1. `internal/sync` moves the repo in (`git bundle` plus `git clone` inside the sandbox) and the changes out (`git format-patch` inside, `git am --3way` on the host worktree, plus an uncommitted diff and untracked files). The base for sync-out is a sandbox-owned ref `refs/agentgopher/sync-base` (ADR 0017). The orchestrator gets a `KindIsolated` branch that creates a worktree (phase 3), syncs in, runs the lifecycle with `ApplyToHost` set to sync-out, and closes the sandbox. Vercel is driven through its official `sandbox` CLI, Daytona through its official Go SDK.

**Tech Stack:** Go 1.25, standard library, plus one dependency in `sandbox/daytona` only: the official Daytona Go SDK. Vercel's `sandbox` CLI must be installed on the host for the Vercel provider.

**Spec:** `docs/plan.md` (sections "Feature inventory and Go mapping": isolated providers, sync in/out, `IsolatedProvider` interface; "Go design decisions": Vercel has no Go SDK, Daytona to be checked; phase 4). Upstream reference: `mattpocock/sandcastle` at commit `7a4e7f7`, files `src/syncIn.ts`, `src/syncOut.ts`, `src/RecoveryMessage.ts`, `src/SandboxProvider.ts`, `src/startSandbox.ts`, `src/SandboxFactory.ts`, `src/sandboxes/vercel.ts`, `src/sandboxes/daytona.ts`, `src/sandboxes/test-isolated.ts`, `src/sandboxes/test-shared.ts`, `docs/adr/0017-sandbox-owned-sync-base-ref.md`. Clone to read: `git clone --depth 1 https://github.com/mattpocock/sandcastle.git /tmp/sandcastle && git -C /tmp/sandcastle checkout 7a4e7f7`.

## Global Constraints

- Module path `github.com/e11en/agentgopher`, `go 1.25` in `go.mod`.
- Standard library only, except `sandbox/daytona`, which imports the official Daytona Go SDK. No other dependency. The Vercel provider shells out to the `sandbox` CLI, as the Docker and Podman providers shell out to theirs.
- Config directory is `.agentgopher/`. Sync-out artifacts go to `.agentgopher/patches/<timestamp>/` inside the host repo (upstream: `.sandcastle/patches/`).
- Public API: root package, `agent`, `sandbox`, `sandbox/nosandbox`, `sandbox/docker`, `sandbox/podman` (phase 3), `sandbox/vercel`, `sandbox/daytona`. Everything else under `internal/`.
- Code comments, test names and commit messages in English. No em or en dashes anywhere (use ` - ` or commas).
- Commit messages: imperative, no attribution footer, no `Co-Authored-By`.
- Repo path inside an isolated sandbox: `sandbox.RepoDir = "/home/agent/workspace"` (upstream `SANDBOX_REPO_DIR`). Vercel uses `/vercel/sandbox/workspace`, Daytona its reported working directory; the handle's `WorktreePath()` is the source of truth and callers never assume `RepoDir`.
- Isolated providers reject the `head` branch strategy and default to `merge-to-head`.
- Every `Exec` implementation streams lines live through `OnLine` while the process runs.
- Tests must not require Vercel, Daytona or network. Sync and orchestration tests use `internal/sandboxtest.Isolated()`, a temp-directory provider that runs `sh -c`. Vercel and Daytona tests call `t.Skip` when `VERCEL_TOKEN` or `DAYTONA_API_KEY` is absent.

## Review Focus

1. A sandbox commit that is a merge commit produces a header-only patch: it is skipped, the other patches still apply, and sync-out succeeds. Test added in Task 5.
2. A second `Run` on the same sandbox handle after `git am` rewrote the host SHAs: the second sync-out ships only the new commits and does not fail with "Invalid revision range". Test added in Task 5.
3. An untracked file in a new subdirectory: the directory is created on the host before the file is written. Test added in Task 6.
4. A stale `git am` session left on the host worktree by an earlier failure: sync-out aborts it first and applies cleanly. Test added in Task 5.
5. `git am` fails on a conflict: the patch directory is preserved, the recovery message names the step and the directory, and the sync-base ref does NOT advance so the next run retries the same commits. Test added in Task 6.


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
sandbox/sandbox.go                       add RepoDir, IsolatedCreateOptions, IsolatedHandle, IsolatedProvider, IsolatedProviderConfig, NewIsolatedProvider
internal/sandboxtest/isolated.go         temp-directory isolated provider for tests
internal/sandboxtest/isolated_test.go
internal/sync/errors.go                  Error, InTimeoutError
internal/sync/in.go                      In
internal/sync/in_test.go
internal/sync/recovery.go                RecoveryMessage (pure)
internal/sync/recovery_test.go
internal/sync/out.go                     Out, CountCommitsToSync, BaseRef
internal/sync/out_test.go
internal/orchestrator/isolated.go        KindIsolated iteration: worktree, create, sync in, copy paths, lifecycle with ApplyToHost
internal/orchestrator/isolated_test.go
errors.go                                aliases: SyncError, SyncInTimeoutError, ContainerStartTimeoutError
agentgopher.go                           isolated strategy validation and defaults
agentgopher_isolated_test.go             end-to-end Run with sandboxtest.Isolated
sandbox/vercel/vercel.go                 provider over the `sandbox` CLI
sandbox/vercel/vercel_test.go
sandbox/daytona/daytona.go               provider over the Daytona Go SDK
sandbox/daytona/daytona_test.go
examples/isolated/main.go
README.md
```

Interfaces consumed from the phase 3 plan (adapt call sites in Task 7 if phase 3 landed with other names, nothing else depends on them):

- `internal/worktree`: see "Reconciliation with the phase 3 plan" below for the real names (`worktree.Create`, `worktree.CreateTemp`, `worktree.Info`, the `cleanup` closure in `startSession`).
- `internal/lifecycle.Options` has `HostWorktreePath string` and `ApplyToHost func(ctx context.Context) error`; `lifecycle.Run` calls `ApplyToHost` after `work` and before commit collection, and collects commits from `HostWorktreePath` when set.
- `internal/orchestrator.Options.Sandbox` is `sandbox.Provider`; the iteration loop switches on `Kind()`. `orchestrator.Options.CopyToWorktree []string` and `orchestrator.Options.BranchStrategy sandbox.BranchStrategy` exist.
- `RunOptions.Sandbox` is `sandbox.Provider`, `RunOptions.CopyToWorktree []string`, and `Run` resolves `resolvedBranch` (the source branch) and `currentHostBranch` (the target branch) for the merge-to-head and named strategies.
- `errors.go` already has `CopyToWorktreeTimeoutError{Timeout time.Duration; Paths []string}` and `WorktreeError{Message string}`; if `ContainerStartTimeoutError{Timeout}` exists already, reuse it instead of adding it in Task 7.

---

### Task 1: Isolated interfaces and the test provider

Port of the isolated parts of `SandboxProvider.ts`, `sandboxes/test-isolated.ts` and `sandboxes/test-shared.ts`.

**Files:**
- Modify: `sandbox/sandbox.go`
- Create: `internal/sandboxtest/isolated.go`, `internal/sandboxtest/isolated_test.go`

**Interfaces:**
- Produces, in package `sandbox`:
  ```go
  const RepoDir = "/home/agent/workspace"
  type IsolatedCreateOptions struct { Env map[string]string }
  type IsolatedHandle interface {
      Handle
      CopyIn(ctx context.Context, hostPath, sandboxPath string) error     // file or directory
      CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error // single file, creates parent dirs
  }
  type IsolatedProvider interface { Provider; Create(ctx context.Context, opts IsolatedCreateOptions) (IsolatedHandle, error) }
  type IsolatedProviderConfig struct { Name string; Env map[string]string; Create func(ctx context.Context, opts IsolatedCreateOptions) (IsolatedHandle, error) }
  func NewIsolatedProvider(cfg IsolatedProviderConfig) IsolatedProvider // Kind() == KindIsolated
  ```
- Produces, in package `sandboxtest`: `func Isolated() sandbox.IsolatedProvider`. Each `Create` makes a temp root with a `workspace/` subdirectory as `WorktreePath()`; `Exec` runs `sh -c` there; `Close` removes the root.

- [ ] **Step 1: Write the failing tests**

`internal/sandboxtest/isolated_test.go`:
```go
package sandboxtest

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/e11en/agentgopher/sandbox"
)

func create(t *testing.T) sandbox.IsolatedHandle {
	t.Helper()
	h, err := Isolated().Create(context.Background(), sandbox.IsolatedCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	return h
}

func TestProviderMetadata(t *testing.T) {
	p := Isolated()
	if p.Kind() != sandbox.KindIsolated || p.Name() != "test-isolated" {
		t.Fatalf("kind=%q name=%q", p.Kind(), p.Name())
	}
}

func TestExecRunsInWorktreeByDefaultAndRespectsCwd(t *testing.T) {
	h := create(t)
	res, err := h.Exec(context.Background(), "pwd", sandbox.ExecOptions{})
	if err != nil || res.Stdout != h.WorktreePath()+"\n" {
		t.Fatalf("got %+v, %v", res, err)
	}
	other := t.TempDir()
	res, err = h.Exec(context.Background(), "pwd", sandbox.ExecOptions{Cwd: other})
	if err != nil || res.Stdout != other+"\n" {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestExecExitCodeAndStreaming(t *testing.T) {
	h := create(t)
	res, err := h.Exec(context.Background(), "exit 4", sandbox.ExecOptions{})
	if err != nil || res.ExitCode != 4 {
		t.Fatalf("got %+v, %v", res, err)
	}
	var lines []string
	start := time.Now()
	var firstAt time.Duration
	_, err = h.Exec(context.Background(), "echo a; sleep 0.3; echo b", sandbox.ExecOptions{OnLine: func(l string) {
		if len(lines) == 0 {
			firstAt = time.Since(start)
		}
		lines = append(lines, l)
	}})
	if err != nil || len(lines) != 2 || firstAt > 200*time.Millisecond {
		t.Fatalf("lines=%v firstAt=%v err=%v", lines, firstAt, err)
	}
}

func TestCopyInFileAndDirectoryAndCopyFileOut(t *testing.T) {
	h := create(t)
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "one.txt"), []byte("1"), 0o644)
	os.MkdirAll(filepath.Join(src, "sub"), 0o755)
	os.WriteFile(filepath.Join(src, "sub", "two.txt"), []byte("2"), 0o644)

	if err := h.CopyIn(context.Background(), filepath.Join(src, "one.txt"), filepath.Join(h.WorktreePath(), "deep", "one.txt")); err != nil {
		t.Fatal(err)
	}
	if err := h.CopyIn(context.Background(), src, filepath.Join(h.WorktreePath(), "copied")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"deep/one.txt", "copied/one.txt", "copied/sub/two.txt"} {
		if _, err := os.Stat(filepath.Join(h.WorktreePath(), p)); err != nil {
			t.Fatalf("%s missing", p)
		}
	}

	out := filepath.Join(t.TempDir(), "nested", "two.txt")
	if err := h.CopyFileOut(context.Background(), filepath.Join(h.WorktreePath(), "copied", "sub", "two.txt"), out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "2" {
		t.Fatalf("got %q", b)
	}
}

func TestCloseRemovesTempDir(t *testing.T) {
	h, _ := Isolated().Create(context.Background(), sandbox.IsolatedCreateOptions{})
	root := filepath.Dir(h.WorktreePath())
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("root still exists: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sandboxtest/`
Expected: FAIL, "undefined: Isolated" (and `sandbox.IsolatedHandle`).

- [ ] **Step 3: Add the isolated types to sandbox/sandbox.go**

Append to `sandbox/sandbox.go`:
```go
// RepoDir is where isolated providers put the repo unless they report
// another working directory.
const RepoDir = "/home/agent/workspace"

// IsolatedCreateOptions are passed to an IsolatedProvider.
type IsolatedCreateOptions struct {
	Env map[string]string
}

// IsolatedHandle is a running sandbox with its own filesystem. Code moves in
// and out through CopyIn and CopyFileOut.
type IsolatedHandle interface {
	Handle
	// CopyIn copies a file or directory from the host into the sandbox.
	CopyIn(ctx context.Context, hostPath, sandboxPath string) error
	// CopyFileOut copies one file from the sandbox to the host, creating
	// parent directories.
	CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error
}

// IsolatedProvider creates sandboxes that do not share the host filesystem.
type IsolatedProvider interface {
	Provider
	Create(ctx context.Context, opts IsolatedCreateOptions) (IsolatedHandle, error)
}

// IsolatedProviderConfig builds a custom IsolatedProvider.
type IsolatedProviderConfig struct {
	Name   string
	Env    map[string]string
	Create func(ctx context.Context, opts IsolatedCreateOptions) (IsolatedHandle, error)
}

type isolatedProvider struct{ cfg IsolatedProviderConfig }

// NewIsolatedProvider wraps cfg into a provider accepted by Run.
func NewIsolatedProvider(cfg IsolatedProviderConfig) IsolatedProvider {
	return &isolatedProvider{cfg: cfg}
}

func (p *isolatedProvider) Kind() Kind              { return KindIsolated }
func (p *isolatedProvider) Name() string            { return p.cfg.Name }
func (p *isolatedProvider) Env() map[string]string  { return p.cfg.Env }
func (p *isolatedProvider) Create(ctx context.Context, opts IsolatedCreateOptions) (IsolatedHandle, error) {
	return p.cfg.Create(ctx, opts)
}
```

- [ ] **Step 4: Write internal/sandboxtest/isolated.go**

```go
// Package sandboxtest holds filesystem-backed providers for tests.
package sandboxtest

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

// Isolated returns a provider whose sandbox is a temp directory on the host.
// Exec runs `sh -c` in it, so sync in and out can be tested without a remote.
func Isolated() sandbox.IsolatedProvider {
	return sandbox.NewIsolatedProvider(sandbox.IsolatedProviderConfig{
		Name: "test-isolated",
		Create: func(ctx context.Context, opts sandbox.IsolatedCreateOptions) (sandbox.IsolatedHandle, error) {
			root, err := os.MkdirTemp("", "agentgopher-test-")
			if err != nil {
				return nil, err
			}
			worktree := filepath.Join(root, "workspace")
			if err := os.MkdirAll(worktree, 0o755); err != nil {
				return nil, err
			}
			inner, err := nosandbox.New(nil).Create(ctx, sandbox.NoSandboxCreateOptions{WorktreePath: worktree, Env: opts.Env})
			if err != nil {
				return nil, err
			}
			return &handle{Handle: inner, root: root}, nil
		},
	})
}

type handle struct {
	sandbox.Handle
	root string
}

func (h *handle) Close(context.Context) error { return os.RemoveAll(h.root) }

func (h *handle) CopyIn(_ context.Context, hostPath, sandboxPath string) error {
	info, err := os.Stat(hostPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.CopyFS(sandboxPath, os.DirFS(hostPath))
	}
	return copyFile(hostPath, sandboxPath)
}

func (h *handle) CopyFileOut(_ context.Context, sandboxPath, hostPath string) error {
	return copyFile(sandboxPath, hostPath)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./sandbox/... ./internal/sandboxtest/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add sandbox/sandbox.go internal/sandboxtest/
git commit -m "Add isolated sandbox interfaces and a temp-directory test provider"
```

---

### Task 2: Sync in

Port of `syncIn.ts`: bundle the host worktree, copy the bundle in, clone it inside the sandbox, check out the branch, verify HEAD.

**Files:**
- Create: `internal/sync/errors.go`, `internal/sync/in.go`, `internal/sync/in_test.go`
- Modify: `errors.go`

**Interfaces:**
- Consumes: `gitx.Run`, `gitx.Head`, `gitx.CurrentBranch` (phase 1), `sandbox.IsolatedHandle` (Task 1).
- Produces:
  ```go
  type Error struct{ Message string }                 // Error() returns Message
  type InTimeoutError struct{ Timeout time.Duration } // "sync-in timed out after <Timeout>"
  const DefaultInTimeout = 5 * time.Minute
  // In ships hostWorktreePath into the sandbox at sandboxRepoDir and checks out branch.
  func In(ctx context.Context, h sandbox.IsolatedHandle, hostWorktreePath, sandboxRepoDir, branch string, timeout time.Duration) error
  ```
- `errors.go` gains `type SyncError = sync.Error` and `type SyncInTimeoutError = sync.InTimeoutError`.

- [ ] **Step 1: Write the failing tests**

`internal/sync/in_test.go`:
```go
package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/internal/sandboxtest"
	"github.com/e11en/agentgopher/sandbox"
)

func newIsolated(t *testing.T) sandbox.IsolatedHandle {
	t.Helper()
	h, err := sandboxtest.Isolated().Create(context.Background(), sandbox.IsolatedCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(context.Background()) })
	return h
}

func commitFile(t *testing.T, dir, name, content, message string) {
	t.Helper()
	os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", message}} {
		if _, err := gitx.Run(context.Background(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInClonesFilesAndHistory(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	commitFile(t, repo, "a.txt", "a", "second")
	commitFile(t, repo, "b.txt", "b", "third")
	h := newIsolated(t)
	ctx := context.Background()
	if err := In(ctx, h, repo, h.WorktreePath(), "main", DefaultInTimeout); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"README.md", "a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(h.WorktreePath(), f)); err != nil {
			t.Fatalf("%s missing in sandbox", f)
		}
	}
	log, _ := gitx.Run(ctx, h.WorktreePath(), "log", "--oneline")
	if len(strings.Split(log, "\n")) != 3 {
		t.Fatalf("log = %q", log)
	}
	hostHead, _ := gitx.Head(ctx, repo)
	sandboxHead, _ := gitx.Head(ctx, h.WorktreePath())
	if hostHead != sandboxHead {
		t.Fatalf("head host=%s sandbox=%s", hostHead, sandboxHead)
	}
	if b, _ := gitx.CurrentBranch(ctx, h.WorktreePath()); b != "main" {
		t.Fatalf("branch = %q", b)
	}
}

func TestInChecksOutNonMainBranch(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	gitx.Run(context.Background(), repo, "checkout", "-q", "-b", "feature/x")
	commitFile(t, repo, "f.txt", "f", "feature")
	h := newIsolated(t)
	if err := In(context.Background(), h, repo, h.WorktreePath(), "feature/x", DefaultInTimeout); err != nil {
		t.Fatal(err)
	}
	if b, _ := gitx.CurrentBranch(context.Background(), h.WorktreePath()); b != "feature/x" {
		t.Fatalf("branch = %q", b)
	}
}

func TestInExcludesUncommittedChanges(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("x"), 0o644)
	h := newIsolated(t)
	if err := In(context.Background(), h, repo, h.WorktreePath(), "main", DefaultInTimeout); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.WorktreePath(), "dirty.txt")); !os.IsNotExist(err) {
		t.Fatal("uncommitted file must not be synced")
	}
}

func TestInTimesOut(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	h := newIsolated(t)
	err := In(context.Background(), h, repo, h.WorktreePath(), "main", time.Nanosecond)
	var terr *InTimeoutError
	if !errors.As(err, &terr) {
		t.Fatalf("err = %v", err)
	}
}

func TestInFailsOnNonRepo(t *testing.T) {
	h := newIsolated(t)
	err := In(context.Background(), h, t.TempDir(), h.WorktreePath(), "main", DefaultInTimeout)
	var serr *Error
	if !errors.As(err, &serr) {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sync/`
Expected: FAIL, "undefined: In".

- [ ] **Step 3: Write errors.go and in.go**

`internal/sync/errors.go`:
```go
// Package sync moves a repo into an isolated sandbox and its changes back out.
package sync

import (
	"fmt"
	"time"
)

// Error reports a failed sync step.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

// InTimeoutError reports a sync-in that ran longer than Timeout.
type InTimeoutError struct{ Timeout time.Duration }

func (e *InTimeoutError) Error() string { return fmt.Sprintf("sync-in timed out after %v", e.Timeout) }
```

`internal/sync/in.go`:
```go
package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/sandbox"
)

// DefaultInTimeout bounds a whole sync-in.
const DefaultInTimeout = 5 * time.Minute

// execOK runs command in the sandbox and fails with Error on a non-zero exit.
func execOK(ctx context.Context, h sandbox.Handle, command, cwd string) (sandbox.ExecResult, error) {
	res, err := h.Exec(ctx, command, sandbox.ExecOptions{Cwd: cwd})
	if err != nil {
		return res, &Error{Message: fmt.Sprintf("sandbox exec failed: %s\n%v", command, err)}
	}
	if res.ExitCode != 0 {
		return res, &Error{Message: fmt.Sprintf("sandbox command failed (exit %d): %s\n%s", res.ExitCode, command, res.Stderr)}
	}
	return res, nil
}

func hostGit(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := gitx.Run(ctx, dir, args...)
	if err != nil {
		return "", &Error{Message: "host command failed: " + err.Error()}
	}
	return out, nil
}

// In bundles hostWorktreePath, copies the bundle into the sandbox, clones it
// at sandboxRepoDir, checks out branch and verifies HEAD matches the host.
func In(ctx context.Context, h sandbox.IsolatedHandle, hostWorktreePath, sandboxRepoDir, branch string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := syncIn(ctx, h, hostWorktreePath, sandboxRepoDir, branch)
	if ctx.Err() == context.DeadlineExceeded {
		return &InTimeoutError{Timeout: timeout}
	}
	return err
}

func syncIn(ctx context.Context, h sandbox.IsolatedHandle, hostWorktreePath, sandboxRepoDir, branch string) error {
	bundleDir, err := os.MkdirTemp("", "agentgopher-bundle-")
	if err != nil {
		return &Error{Message: "failed to create temp dir: " + err.Error()}
	}
	defer os.RemoveAll(bundleDir)
	bundleHost := filepath.Join(bundleDir, "repo.bundle")
	if _, err := hostGit(ctx, hostWorktreePath, "bundle", "create", bundleHost, "--all"); err != nil {
		return err
	}

	tmp, err := execOK(ctx, h, "mktemp -d -t agentgopher-XXXXXX", "")
	if err != nil {
		return err
	}
	sandboxTmp := strings.TrimSpace(tmp.Stdout)
	bundleSandbox := sandboxTmp + "/repo.bundle"
	if err := h.CopyIn(ctx, bundleHost, bundleSandbox); err != nil {
		return &Error{Message: "failed to copy bundle into sandbox: " + err.Error()}
	}

	// git clone needs an empty target, so clone next to the repo dir and swap.
	steps := []string{
		fmt.Sprintf(`git clone -q "%s" "%s_clone"`, bundleSandbox, sandboxRepoDir),
		fmt.Sprintf(`rm -rf "%s" && mv "%s_clone" "%s"`, sandboxRepoDir, sandboxRepoDir, sandboxRepoDir),
	}
	for _, cmd := range steps {
		if _, err := execOK(ctx, h, cmd, ""); err != nil {
			return err
		}
	}
	if _, err := execOK(ctx, h, fmt.Sprintf(`git checkout -q "%s"`, branch), sandboxRepoDir); err != nil {
		return err
	}
	_, _ = h.Exec(ctx, fmt.Sprintf(`rm -rf "%s"`, sandboxTmp), sandbox.ExecOptions{})

	hostHead, err := hostGit(ctx, hostWorktreePath, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	res, err := execOK(ctx, h, "git rev-parse HEAD", sandboxRepoDir)
	if err != nil {
		return err
	}
	if sandboxHead := strings.TrimSpace(res.Stdout); sandboxHead != hostHead {
		return &Error{Message: fmt.Sprintf("HEAD mismatch after sync-in: host=%s sandbox=%s", hostHead, sandboxHead)}
	}
	return nil
}
```

- [ ] **Step 4: Alias the errors from the root package**

Append to `errors.go` (import `"github.com/e11en/agentgopher/internal/sync"`):
```go
// SyncError reports a failed sync-in or sync-out step.
type SyncError = sync.Error

// SyncInTimeoutError reports a sync-in that ran longer than its timeout.
type SyncInTimeoutError = sync.InTimeoutError
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/sync/ ./`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/sync/ errors.go
git commit -m "Add sync-in via git bundle for isolated sandboxes"
```

---

### Task 3: Recovery message

Port of `RecoveryMessage.ts` without the `branch` variant (upstream never passes it from `syncOut`).

**Files:**
- Create: `internal/sync/recovery.go`, `internal/sync/recovery_test.go`

**Interfaces:**
- Produces:
  ```go
  type FailedStep string // StepCommits = "commits", StepDiff = "diff", StepUntracked = "untracked"
  type RecoveryInput struct { PatchDir string; FailedStep FailedStep; HasCommits, HasDiff, HasUntracked bool }
  func RecoveryMessage(in RecoveryInput) string
  ```

- [ ] **Step 1: Write the failing tests**

`internal/sync/recovery_test.go`:
```go
package sync

import "testing"

func TestRecoveryMessage(t *testing.T) {
	cases := []struct {
		name string
		in   RecoveryInput
		want string
	}{
		{
			"am failure with remaining steps",
			RecoveryInput{PatchDir: ".agentgopher/patches/20261015-120000", FailedStep: StepCommits, HasCommits: true, HasDiff: true, HasUntracked: true},
			"Patch application failed at step 1 (committed changes).\n\nResolve conflicts, then continue with:\n  git am --continue\n\nAfter all commits are applied, run the remaining steps:\n  git apply .agentgopher/patches/20261015-120000/changes.patch && \\\n  cp -r .agentgopher/patches/20261015-120000/untracked/* .",
		},
		{
			"am failure alone",
			RecoveryInput{PatchDir: "p", FailedStep: StepCommits, HasCommits: true},
			"Patch application failed at step 1 (committed changes).\n\nResolve conflicts, then continue with:\n  git am --continue",
		},
		{
			"diff failure after commits",
			RecoveryInput{PatchDir: "p", FailedStep: StepDiff, HasCommits: true, HasDiff: true},
			"Patch application failed at step 2 (uncommitted changes).\n\nRun the remaining steps:\n  git apply p/changes.patch",
		},
		{
			"untracked failure only",
			RecoveryInput{PatchDir: "p", FailedStep: StepUntracked, HasUntracked: true},
			"Patch application failed at step 1 (untracked files).\n\nRun the remaining steps:\n  cp -r p/untracked/* .",
		},
	}
	for _, c := range cases {
		if got := RecoveryMessage(c.in); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sync/ -run TestRecoveryMessage`
Expected: FAIL, "undefined: RecoveryInput".

- [ ] **Step 3: Write recovery.go**

```go
package sync

import "strings"

// FailedStep names the sync-out step that failed.
type FailedStep string

const (
	StepCommits   FailedStep = "commits"
	StepDiff      FailedStep = "diff"
	StepUntracked FailedStep = "untracked"
)

// RecoveryInput describes a failed sync-out.
type RecoveryInput struct {
	PatchDir     string
	FailedStep   FailedStep
	HasCommits   bool
	HasDiff      bool
	HasUntracked bool
}

type step struct {
	key   FailedStep
	label string
	cmd   string
}

// RecoveryMessage builds copy-pastable commands to finish a failed sync-out
// by hand from the preserved patch directory.
func RecoveryMessage(in RecoveryInput) string {
	var steps []step
	if in.HasCommits {
		steps = append(steps, step{StepCommits, "committed changes", "git am --3way " + in.PatchDir + "/*.patch"})
	}
	if in.HasDiff {
		steps = append(steps, step{StepDiff, "uncommitted changes", "git apply " + in.PatchDir + "/changes.patch"})
	}
	if in.HasUntracked {
		steps = append(steps, step{StepUntracked, "untracked files", "cp -r " + in.PatchDir + "/untracked/* ."})
	}
	failed := 0
	for i, s := range steps {
		if s.key == in.FailedStep {
			failed = i
		}
	}
	var b strings.Builder
	b.WriteString("Patch application failed at step " + itoa(failed+1) + " (" + steps[failed].label + ").\n\n")
	if in.FailedStep == StepCommits {
		b.WriteString("Resolve conflicts, then continue with:\n  git am --continue")
		if rest := commands(steps[failed+1:]); rest != "" {
			b.WriteString("\n\nAfter all commits are applied, run the remaining steps:\n" + rest)
		}
		return b.String()
	}
	b.WriteString("Run the remaining steps:\n" + commands(steps[failed:]))
	return b.String()
}

func commands(steps []step) string {
	lines := make([]string, len(steps))
	for i, s := range steps {
		if i < len(steps)-1 {
			lines[i] = "  " + s.cmd + " && \\"
		} else {
			lines[i] = "  " + s.cmd
		}
	}
	return strings.Join(lines, "\n")
}

func itoa(n int) string { return strconv.Itoa(n) }
```

Add `"strconv"` to the imports of recovery.go.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sync/ -run TestRecoveryMessage`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sync/recovery.go internal/sync/recovery_test.go
git commit -m "Add sync-out recovery message"
```

---

### Task 4: Count commits and the sync-base ref

Port of `countCommitsToSync` and `SYNC_BASE_REF` from `syncOut.ts`. Small on its own, but it is the base resolution every later task shares (ADR 0017).

**Files:**
- Create: `internal/sync/out.go` (started here, extended in Tasks 5 and 6), `internal/sync/out_test.go`

**Interfaces:**
- Produces:
  ```go
  const BaseRef = "refs/agentgopher/sync-base"
  // resolveBase returns BaseRef's target when set in the sandbox repo, else hostHead.
  func resolveBase(ctx context.Context, h sandbox.Handle, sandboxRepoDir, hostHead string) string
  // CountCommitsToSync returns how many commits Out would ship; 0 on any git failure.
  func CountCommitsToSync(ctx context.Context, h sandbox.Handle, sandboxRepoDir, hostHead string) (int, error)
  ```
- `CountCommitsToSync` never returns a non-nil error in practice (it degrades to 0, as upstream); the error slot exists for the contract and future use.

- [ ] **Step 1: Write the failing tests**

`internal/sync/out_test.go`:
```go
package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/sandbox"
)

// synced returns a host repo and a sandbox handle holding a clone of it.
func synced(t *testing.T) (string, sandbox.IsolatedHandle) {
	t.Helper()
	repo := gitx.InitTestRepo(t)
	h := newIsolated(t)
	if err := In(context.Background(), h, repo, h.WorktreePath(), "main", DefaultInTimeout); err != nil {
		t.Fatal(err)
	}
	return repo, h
}

func hostLog(t *testing.T, repo string) []string {
	t.Helper()
	out, err := gitx.Run(context.Background(), repo, "log", "--format=%s")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(out, "\n")
}

func TestCountCommitsToSyncFallsBackToHostHead(t *testing.T) {
	repo, h := synced(t)
	ctx := context.Background()
	hostHead, _ := gitx.Head(ctx, repo)
	n, err := CountCommitsToSync(ctx, h, h.WorktreePath(), hostHead)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	commitFile(t, h.WorktreePath(), "one.txt", "1", "one")
	commitFile(t, h.WorktreePath(), "two.txt", "2", "two")
	if n, _ = CountCommitsToSync(ctx, h, h.WorktreePath(), hostHead); n != 2 {
		t.Fatalf("n = %d", n)
	}
}

func TestCountCommitsToSyncUsesBaseRefWhenSet(t *testing.T) {
	repo, h := synced(t)
	ctx := context.Background()
	hostHead, _ := gitx.Head(ctx, repo)
	commitFile(t, h.WorktreePath(), "one.txt", "1", "one")
	shipped, _ := gitx.Head(ctx, h.WorktreePath())
	gitx.Run(ctx, h.WorktreePath(), "update-ref", BaseRef, shipped)
	commitFile(t, h.WorktreePath(), "two.txt", "2", "two")
	// hostHead is stale on purpose: the ref must win.
	if n, _ := CountCommitsToSync(ctx, h, h.WorktreePath(), hostHead); n != 1 {
		t.Fatalf("n = %d", n)
	}
}

func TestCountCommitsToSyncDegradesToZero(t *testing.T) {
	_, h := synced(t)
	if n, err := CountCommitsToSync(context.Background(), h, h.WorktreePath(), "not-a-sha"); n != 0 || err != nil {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sync/ -run CountCommits`
Expected: FAIL, "undefined: CountCommitsToSync".

- [ ] **Step 3: Write out.go (first part)**

```go
package sync

import (
	"context"
	"strconv"
	"strings"

	"github.com/e11en/agentgopher/sandbox"
)

// BaseRef is a sandbox-owned ref pinning the last commit shipped to the host.
// `git am` rewrites SHAs on the host, so after the first sync-out the host
// HEAD is unknown inside the sandbox; the ref keeps a base that exists there.
// Absent exactly when no sync-out has shipped commits yet. See ADR 0017.
const BaseRef = "refs/agentgopher/sync-base"

func resolveBase(ctx context.Context, h sandbox.Handle, sandboxRepoDir, hostHead string) string {
	res, err := h.Exec(ctx, "git rev-parse --verify --quiet "+BaseRef, sandbox.ExecOptions{Cwd: sandboxRepoDir})
	if err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) != "" {
		return strings.TrimSpace(res.Stdout)
	}
	return hostHead
}

// CountCommitsToSync returns the number of sandbox commits Out would ship.
// It degrades to 0 when git fails, so a status label never breaks a run.
func CountCommitsToSync(ctx context.Context, h sandbox.Handle, sandboxRepoDir, hostHead string) (int, error) {
	base := resolveBase(ctx, h, sandboxRepoDir, hostHead)
	res, err := h.Exec(ctx, `git rev-list "`+base+`..HEAD" --count`, sandbox.ExecOptions{Cwd: sandboxRepoDir})
	if err != nil || res.ExitCode != 0 {
		return 0, nil
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if convErr != nil {
		return 0, nil
	}
	return n, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/sync/ -run CountCommits`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sync/out.go internal/sync/out_test.go
git commit -m "Add sync-base ref resolution and commit count for sync-out"
```

---

### Task 5: Sync out, committed changes

Port of the commit path of `syncOut.ts`: `git format-patch` in the sandbox, copy each patch out to `.agentgopher/patches/<timestamp>/`, skip header-only patches, `git am --abort` then `git am --3way` on the host worktree, advance `BaseRef`, clean up on success. The uncommitted diff and untracked files follow in Task 6.

**Files:**
- Modify: `internal/sync/out.go`, `internal/sync/out_test.go`

**Interfaces:**
- Produces: `func Out(ctx context.Context, h sandbox.IsolatedHandle, hostWorktreePath, sandboxRepoDir string) error`. Writes the recovery message to `os.Stderr` on failure and returns nil (upstream behaviour: a failed apply is reported, not raised; the preserved patch directory is the recovery path). Returns `*Error` only when saving artifacts fails.
- Produces: `const PatchesDir = ".agentgopher/patches"`.

- [ ] **Step 1: Write the failing tests (append to out_test.go)**

```go
func TestOutShipsOneCommit(t *testing.T) {
	repo, h := synced(t)
	commitFile(t, h.WorktreePath(), "new.txt", "hello", "add new file")
	if err := Out(context.Background(), h, repo, h.WorktreePath()); err != nil {
		t.Fatal(err)
	}
	if log := hostLog(t, repo); log[0] != "add new file" {
		t.Fatalf("log = %v", log)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "new.txt")); string(b) != "hello" {
		t.Fatalf("content = %q", b)
	}
	if _, err := os.Stat(filepath.Join(repo, PatchesDir)); !os.IsNotExist(err) {
		t.Fatal("patch artifacts must be removed after success")
	}
}

func TestOutShipsMultipleCommitsInOrderWithMetadata(t *testing.T) {
	repo, h := synced(t)
	ctx := context.Background()
	gitx.Run(ctx, h.WorktreePath(), "config", "user.name", "Agent Smith")
	gitx.Run(ctx, h.WorktreePath(), "config", "user.email", "agent@example.com")
	commitFile(t, h.WorktreePath(), "1.txt", "1", "first")
	commitFile(t, h.WorktreePath(), "2.txt", "2", "second")
	commitFile(t, h.WorktreePath(), "3.txt", "3", "third")
	if err := Out(ctx, h, repo, h.WorktreePath()); err != nil {
		t.Fatal(err)
	}
	log := hostLog(t, repo)
	if len(log) != 4 || log[0] != "third" || log[1] != "second" || log[2] != "first" {
		t.Fatalf("log = %v", log)
	}
	author, _ := gitx.Run(ctx, repo, "log", "-1", "--format=%an <%ae>")
	if author != "Agent Smith <agent@example.com>" {
		t.Fatalf("author = %q", author)
	}
}

func TestOutNoNewCommitsIsNoop(t *testing.T) {
	repo, h := synced(t)
	before := hostLog(t, repo)
	if err := Out(context.Background(), h, repo, h.WorktreePath()); err != nil {
		t.Fatal(err)
	}
	if after := hostLog(t, repo); len(after) != len(before) {
		t.Fatalf("log changed: %v", after)
	}
	if _, err := os.Stat(filepath.Join(repo, PatchesDir)); !os.IsNotExist(err) {
		t.Fatal("no patch dir expected")
	}
}

func TestOutSkipsHeaderOnlyMergeCommitPatches(t *testing.T) {
	repo, h := synced(t)
	ctx := context.Background()
	wt := h.WorktreePath()
	gitx.Run(ctx, wt, "checkout", "-q", "-b", "side")
	commitFile(t, wt, "side.txt", "s", "side work")
	gitx.Run(ctx, wt, "checkout", "-q", "main")
	commitFile(t, wt, "main.txt", "m", "main work")
	if _, err := gitx.Run(ctx, wt, "merge", "--no-ff", "-q", "-m", "merge side", "side"); err != nil {
		t.Fatal(err)
	}
	if err := Out(ctx, h, repo, wt); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"side.txt", "main.txt"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Fatalf("%s missing on host", f)
		}
	}
}

func TestOutAbortsStaleAmSession(t *testing.T) {
	repo, h := synced(t)
	ctx := context.Background()
	// Leave a half-applied am session behind on the host.
	os.MkdirAll(filepath.Join(repo, ".git", "rebase-apply"), 0o755)
	os.WriteFile(filepath.Join(repo, ".git", "rebase-apply", "applying"), nil, 0o644)
	commitFile(t, h.WorktreePath(), "a.txt", "a", "after stale")
	if err := Out(ctx, h, repo, h.WorktreePath()); err != nil {
		t.Fatal(err)
	}
	if log := hostLog(t, repo); log[0] != "after stale" {
		t.Fatalf("log = %v", log)
	}
}

func TestOutTwiceOnSameSandboxShipsOnlyNewCommits(t *testing.T) {
	repo, h := synced(t)
	ctx := context.Background()
	commitFile(t, h.WorktreePath(), "1.txt", "1", "first")
	if err := Out(ctx, h, repo, h.WorktreePath()); err != nil {
		t.Fatal(err)
	}
	commitFile(t, h.WorktreePath(), "2.txt", "2", "second")
	if err := Out(ctx, h, repo, h.WorktreePath()); err != nil {
		t.Fatal(err)
	}
	log := hostLog(t, repo)
	if len(log) != 3 || log[0] != "second" || log[1] != "first" {
		t.Fatalf("log = %v", log)
	}
	// Third call with nothing new: no-op, no artifacts.
	if err := Out(ctx, h, repo, h.WorktreePath()); err != nil {
		t.Fatal(err)
	}
	if after := hostLog(t, repo); len(after) != 3 {
		t.Fatalf("log = %v", after)
	}
	hostHead, _ := gitx.Head(ctx, repo)
	if n, _ := CountCommitsToSync(ctx, h, h.WorktreePath(), hostHead); n != 0 {
		t.Fatalf("count after sync = %d", n)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sync/ -run TestOut`
Expected: FAIL, "undefined: Out".

- [ ] **Step 3: Extend out.go**

Add to the imports: `"fmt"`, `"os"`, `"path/filepath"`, `"time"`, `"github.com/e11en/agentgopher/internal/gitx"`. Append:

```go
// PatchesDir holds preserved sync-out artifacts, relative to the host repo.
const PatchesDir = ".agentgopher/patches"

// outState is what Out found in the sandbox before applying anything.
type outState struct {
	base, sandboxHead string
	hasCommits        bool
	diff              string
	untracked         []string
}

func inspect(ctx context.Context, h sandbox.IsolatedHandle, hostWorktreePath, sandboxRepoDir string) (outState, error) {
	hostHead, err := hostGit(ctx, hostWorktreePath, "rev-parse", "HEAD")
	if err != nil {
		return outState{}, err
	}
	res, err := execOK(ctx, h, "git rev-parse HEAD", sandboxRepoDir)
	if err != nil {
		return outState{}, err
	}
	s := outState{sandboxHead: strings.TrimSpace(res.Stdout)}
	s.base = resolveBase(ctx, h, sandboxRepoDir, hostHead)
	s.hasCommits = s.base != s.sandboxHead

	if d, err := h.Exec(ctx, "git diff HEAD", sandbox.ExecOptions{Cwd: sandboxRepoDir}); err == nil && d.ExitCode == 0 && strings.TrimSpace(d.Stdout) != "" {
		s.diff = d.Stdout
	}
	if u, err := h.Exec(ctx, "git ls-files --others --exclude-standard", sandbox.ExecOptions{Cwd: sandboxRepoDir}); err == nil && u.ExitCode == 0 {
		for _, f := range strings.Split(strings.TrimSpace(u.Stdout), "\n") {
			if f != "" {
				s.untracked = append(s.untracked, f)
			}
		}
	}
	return s, nil
}

// createPatchDir makes <host>/.agentgopher/patches/YYYYMMDD-HHMMSS[-n].
func createPatchDir(hostWorktreePath string) (string, error) {
	root := filepath.Join(hostWorktreePath, PatchesDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", &Error{Message: "failed to create patch directory: " + err.Error()}
	}
	base := time.Now().Format("20060102-150405")
	name := base
	for i := 1; ; i++ {
		if _, err := os.Stat(filepath.Join(root, name)); os.IsNotExist(err) {
			break
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", &Error{Message: "failed to create patch directory: " + err.Error()}
	}
	return dir, nil
}

// savePatches runs format-patch in the sandbox and copies the non-empty
// patches into patchDir, returning their host paths in order.
func savePatches(ctx context.Context, h sandbox.IsolatedHandle, sandboxRepoDir, base, patchDir string) ([]string, error) {
	tmp, err := execOK(ctx, h, "mktemp -d -t agentgopher-patches-XXXXXX", "")
	if err != nil {
		return nil, err
	}
	sandboxPatchDir := strings.TrimSpace(tmp.Stdout)
	defer func() { _, _ = h.Exec(ctx, fmt.Sprintf(`rm -rf "%s"`, sandboxPatchDir), sandbox.ExecOptions{}) }()

	if _, err := execOK(ctx, h, fmt.Sprintf(`git format-patch "%s..HEAD" -o "%s"`, base, sandboxPatchDir), sandboxRepoDir); err != nil {
		return nil, err
	}
	ls, err := execOK(ctx, h, fmt.Sprintf(`ls -1 "%s"`, sandboxPatchDir), "")
	if err != nil {
		return nil, err
	}
	var patches []string
	for _, name := range strings.Split(strings.TrimSpace(ls.Stdout), "\n") {
		if name == "" {
			continue
		}
		hostPath := filepath.Join(patchDir, name)
		if err := h.CopyFileOut(ctx, sandboxPatchDir+"/"+name, hostPath); err != nil {
			return nil, &Error{Message: fmt.Sprintf("failed to copy patch %s: %v", name, err)}
		}
		b, err := os.ReadFile(hostPath)
		if err != nil {
			return nil, &Error{Message: fmt.Sprintf("failed to read patch %s: %v", name, err)}
		}
		// A merge commit yields a header-only patch that git am rejects.
		if strings.Contains(string(b), "diff --git") {
			patches = append(patches, hostPath)
		}
	}
	return patches, nil
}

// Out ships the sandbox's commits, uncommitted diff and untracked files to
// hostWorktreePath. Artifacts are saved under PatchesDir first, applied
// second; on an apply failure they are preserved and a recovery message is
// written to stderr. See ADR 0017 for the base ref.
func Out(ctx context.Context, h sandbox.IsolatedHandle, hostWorktreePath, sandboxRepoDir string) error {
	s, err := inspect(ctx, h, hostWorktreePath, sandboxRepoDir)
	if err != nil {
		return err
	}
	if !s.hasCommits && s.diff == "" && len(s.untracked) == 0 {
		return nil
	}

	patchDir, err := createPatchDir(hostWorktreePath)
	if err != nil {
		return err
	}
	relPatchDir := filepath.ToSlash(filepath.Join(PatchesDir, filepath.Base(patchDir)))

	var patches []string
	if s.hasCommits {
		if patches, err = savePatches(ctx, h, sandboxRepoDir, s.base, patchDir); err != nil {
			return err
		}
	}
	if err := saveDiffAndUntracked(ctx, h, sandboxRepoDir, patchDir, s); err != nil {
		return err
	}

	var failed FailedStep
	if len(patches) > 0 {
		_, _ = gitx.Run(ctx, hostWorktreePath, "am", "--abort")
		if _, err := gitx.Run(ctx, hostWorktreePath, append([]string{"am", "--3way"}, patches...)...); err != nil {
			failed = StepCommits
		}
	}
	if failed == "" {
		failed = applyDiffAndUntracked(hostWorktreePath, patchDir, s)
	}

	// Commits that landed must not be re-emitted next run, even when a later
	// step failed. Only a failed `git am` leaves the ref where it was.
	if s.hasCommits && failed != StepCommits {
		if _, err := execOK(ctx, h, fmt.Sprintf("git update-ref %s %s", BaseRef, s.sandboxHead), sandboxRepoDir); err != nil {
			return err
		}
	}

	if failed != "" {
		fmt.Fprintln(os.Stderr, "\n"+RecoveryMessage(RecoveryInput{
			PatchDir: relPatchDir, FailedStep: failed,
			HasCommits: len(patches) > 0, HasDiff: s.diff != "", HasUntracked: len(s.untracked) > 0,
		}))
		return nil
	}
	_ = os.RemoveAll(patchDir)
	if entries, err := os.ReadDir(filepath.Join(hostWorktreePath, PatchesDir)); err == nil && len(entries) == 0 {
		_ = os.RemoveAll(filepath.Join(hostWorktreePath, PatchesDir))
	}
	return nil
}

// saveDiffAndUntracked and applyDiffAndUntracked are filled in by Task 6.
func saveDiffAndUntracked(context.Context, sandbox.IsolatedHandle, string, string, outState) error { return nil }
func applyDiffAndUntracked(string, string, outState) FailedStep                                    { return "" }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/sync/`
Expected: PASS. If `TestOutAbortsStaleAmSession` fails because `git am --abort` refuses the fake session, replace the fake with a real stale session: commit a conflicting change on the host, run `Out` once to produce a failed `am` (left in progress), then make a clean commit in the sandbox and assert the second `Out` applies it. Keep whichever variant passes and delete the other.

- [ ] **Step 5: Commit**

```bash
git add internal/sync/out.go internal/sync/out_test.go
git commit -m "Add sync-out for committed changes with a sandbox-owned base ref"
```

---

### Task 6: Sync out, uncommitted diff, untracked files and failure preservation

Completes `syncOut.ts`: `git diff HEAD` applied with `git apply`, untracked files copied out file by file, and the preserve-and-report path when a step fails.

**Files:**
- Modify: `internal/sync/out.go`, `internal/sync/out_test.go`

- [ ] **Step 1: Write the failing tests (append to out_test.go)**

```go
func TestOutShipsUncommittedChanges(t *testing.T) {
	repo, h := synced(t)
	wt := h.WorktreePath()
	os.WriteFile(filepath.Join(wt, "README.md"), []byte("hi\nstaged\n"), 0o644)
	gitx.Run(context.Background(), wt, "add", "README.md")
	os.WriteFile(filepath.Join(wt, "README.md"), []byte("hi\nstaged\nunstaged\n"), 0o644)
	if err := Out(context.Background(), h, repo, wt); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "README.md")); string(b) != "hi\nstaged\nunstaged\n" {
		t.Fatalf("content = %q", b)
	}
	if log := hostLog(t, repo); len(log) != 1 {
		t.Fatalf("no commit expected, log = %v", log)
	}
}

func TestOutShipsUntrackedFilesIncludingNewDirectories(t *testing.T) {
	repo, h := synced(t)
	wt := h.WorktreePath()
	os.MkdirAll(filepath.Join(wt, "src", "deep"), 0o755)
	os.WriteFile(filepath.Join(wt, "src", "deep", "new.go"), []byte("package deep"), 0o644)
	os.WriteFile(filepath.Join(wt, "top.txt"), []byte("t"), 0o644)
	if err := Out(context.Background(), h, repo, wt); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"src/deep/new.go", "top.txt"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Fatalf("%s missing on host", f)
		}
	}
}

func TestOutShipsCommitsDiffAndUntrackedTogether(t *testing.T) {
	repo, h := synced(t)
	wt := h.WorktreePath()
	commitFile(t, wt, "c.txt", "c", "committed")
	os.WriteFile(filepath.Join(wt, "c.txt"), []byte("c-modified"), 0o644)
	os.WriteFile(filepath.Join(wt, "u.txt"), []byte("u"), 0o644)
	if err := Out(context.Background(), h, repo, wt); err != nil {
		t.Fatal(err)
	}
	if log := hostLog(t, repo); log[0] != "committed" {
		t.Fatalf("log = %v", log)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "c.txt")); string(b) != "c-modified" {
		t.Fatalf("c.txt = %q", b)
	}
	if _, err := os.Stat(filepath.Join(repo, "u.txt")); err != nil {
		t.Fatal("u.txt missing")
	}
	if _, err := os.Stat(filepath.Join(repo, PatchesDir)); !os.IsNotExist(err) {
		t.Fatal("artifacts must be cleaned up")
	}
}

func TestOutPreservesArtifactsWhenAmFails(t *testing.T) {
	repo, h := synced(t)
	ctx := context.Background()
	wt := h.WorktreePath()
	// Conflict: both sides change the same line of README.md.
	commitFile(t, repo, "README.md", "host version\n", "host change")
	commitFile(t, wt, "README.md", "sandbox version\n", "sandbox change")
	os.WriteFile(filepath.Join(wt, "extra.txt"), []byte("x"), 0o644)

	if err := Out(ctx, h, repo, wt); err != nil {
		t.Fatalf("a failed apply is reported, not returned: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(repo, PatchesDir))
	if err != nil || len(entries) != 1 {
		t.Fatalf("patch dir entries = %v, %v", entries, err)
	}
	patchDir := filepath.Join(repo, PatchesDir, entries[0].Name())
	if _, err := os.Stat(filepath.Join(patchDir, "untracked", "extra.txt")); err != nil {
		t.Fatal("untracked artifact missing")
	}
	// The base ref must not advance: the commit never landed.
	res, _ := h.Exec(ctx, "git rev-parse --verify --quiet "+BaseRef, sandbox.ExecOptions{Cwd: wt})
	if res.ExitCode == 0 {
		t.Fatal("base ref advanced after a failed git am")
	}
	// Leave the host usable for the next test run.
	gitx.Run(ctx, repo, "am", "--abort")
}

func TestOutPreservesDiffWhenApplyFails(t *testing.T) {
	repo, h := synced(t)
	ctx := context.Background()
	wt := h.WorktreePath()
	// Uncommitted change in the sandbox that cannot apply on the host.
	os.WriteFile(filepath.Join(wt, "README.md"), []byte("sandbox edit\n"), 0o644)
	commitFile(t, repo, "README.md", "host rewrote everything\n", "host change")

	if err := Out(ctx, h, repo, wt); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(repo, PatchesDir))
	if len(entries) != 1 {
		t.Fatalf("entries = %v", entries)
	}
	if _, err := os.Stat(filepath.Join(repo, PatchesDir, entries[0].Name(), "changes.patch")); err != nil {
		t.Fatal("changes.patch not preserved")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/sync/ -run 'Uncommitted|Untracked|Together|Preserves'`
Expected: FAIL on content assertions (the stubs from Task 5 do nothing).

- [ ] **Step 3: Replace the two stubs in out.go**

```go
func saveDiffAndUntracked(ctx context.Context, h sandbox.IsolatedHandle, sandboxRepoDir, patchDir string, s outState) error {
	if s.diff != "" {
		if err := os.WriteFile(filepath.Join(patchDir, "changes.patch"), []byte(s.diff), 0o644); err != nil {
			return &Error{Message: "failed to write diff patch: " + err.Error()}
		}
	}
	for _, rel := range s.untracked {
		if err := h.CopyFileOut(ctx, sandboxRepoDir+"/"+rel, filepath.Join(patchDir, "untracked", filepath.FromSlash(rel))); err != nil {
			return &Error{Message: fmt.Sprintf("failed to save untracked file %s: %v", rel, err)}
		}
	}
	return nil
}

func applyDiffAndUntracked(hostWorktreePath, patchDir string, s outState) FailedStep {
	ctx := context.Background()
	if s.diff != "" {
		if _, err := gitx.Run(ctx, hostWorktreePath, "apply", filepath.Join(patchDir, "changes.patch")); err != nil {
			return StepDiff
		}
	}
	for _, rel := range s.untracked {
		src := filepath.Join(patchDir, "untracked", filepath.FromSlash(rel))
		dst := filepath.Join(hostWorktreePath, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return StepUntracked
		}
		b, err := os.ReadFile(src)
		if err != nil {
			return StepUntracked
		}
		if err := os.WriteFile(dst, b, 0o644); err != nil {
			return StepUntracked
		}
	}
	return ""
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/sync/`
Expected: PASS, and the recovery message appears on stderr for the two failure tests.

- [ ] **Step 5: Commit**

```bash
git add internal/sync/out.go internal/sync/out_test.go
git commit -m "Ship uncommitted and untracked changes in sync-out and preserve artifacts on failure"
```

---

### Task 7: Orchestrator and Run support for isolated providers

Port of `startIsolatedSandbox` in `startSandbox.ts` and the `tag === "isolated"` branch of `SandboxFactory.ts`, plus the strategy validation in `run.ts`.

**Files:**
- Create: `internal/orchestrator/isolated.go`, `internal/orchestrator/isolated_test.go`, `agentgopher_isolated_test.go`
- Modify: `internal/orchestrator/orchestrator.go` (the `Kind()` switch), `agentgopher.go` (validation and default strategy), `errors.go`

**Interfaces:**
- Consumes: phase 3's `worktree.Create`, `lifecycle.Options.HostWorktreePath` and `ApplyToHost`, `orchestrator.Options.CopyToWorktree` and `BranchStrategy`; `sync.In`, `sync.Out`, `sync.CountCommitsToSync` (Tasks 2 to 6).
- Produces:
  ```go
  const ContainerStartTimeout = 2 * time.Minute          // upstream CONTAINER_START_TIMEOUT_MS
  const CopyPathsTimeout = 60 * time.Second              // upstream COPY_PATHS_TIMEOUT_MS
  type ContainerStartTimeoutError struct{ Timeout time.Duration }
  func runIsolatedIteration(ctx context.Context, o Options, p sandbox.IsolatedProvider, i int, signals []string, idle, completion time.Duration, label func(string) string) (lifecycle.Result[iterationOutcome], string, error)
  ```
  The second return value is the preserved worktree path, or "".
- `Run`: an isolated provider with `BranchStrategy.Type == BranchHead` returns `fmt.Errorf("head branch strategy is not supported with isolated providers")`; a nil `BranchStrategy` with an isolated provider means merge-to-head.
- `errors.go`: `type ContainerStartTimeoutError = orchestrator.ContainerStartTimeoutError` (skip when phase 3 already added it).

- [ ] **Step 1: Write the failing orchestrator test**

`internal/orchestrator/isolated_test.go`:
```go
package orchestrator

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/internal/sandboxtest"
	"github.com/e11en/agentgopher/sandbox"
)

// shellAgent runs its Command as the "agent" inside the sandbox repo.
type shellAgent struct{ command string }

func (shellAgent) Name() string           { return "shell-agent" }
func (shellAgent) Env() map[string]string { return nil }
func (shellAgent) CaptureSessions() bool  { return false }
func (a shellAgent) BuildPrintCommand(agent.CommandOptions) agent.PrintCommand {
	return agent.PrintCommand{Command: a.command}
}
func (shellAgent) ParseStreamLine(line string) []agent.StreamEvent {
	if strings.HasPrefix(line, "RESULT ") {
		return []agent.StreamEvent{{Type: agent.EventResult, Text: strings.TrimPrefix(line, "RESULT ")}}
	}
	return []agent.StreamEvent{{Type: agent.EventText, Text: line}}
}

func TestIsolatedIterationSyncsInRunsAgentAndMergesBack(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	log := &bytes.Buffer{}
	o := Options{
		HostRepoDir:    repo,
		Iterations:     1,
		Prompt:         "work",
		Provider:       shellAgent{command: `test -f README.md && echo made > made.txt && git add . && git commit -q -m "agent commit" && echo "RESULT <promise>COMPLETE</promise>"`},
		Sandbox:        sandboxtest.Isolated(),
		BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead},
		Display:        display.NewWriter(log),
		IdleTimeout:    10 * time.Second,
	}
	res, err := Orchestrate(context.Background(), o)
	if err != nil {
		t.Fatalf("err = %v\nlog:\n%s", err, log.String())
	}
	if res.CompletionSignal == "" || len(res.Commits) != 1 || res.Branch != "main" {
		t.Fatalf("res = %+v\nlog:\n%s", res, log.String())
	}
	if _, err := os.Stat(filepath.Join(repo, "made.txt")); err != nil {
		t.Fatal("agent commit did not reach the host")
	}
	for _, want := range []string{"Syncing repo into sandbox", "Syncing 1 commit to host"} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("missing %q in log:\n%s", want, log.String())
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(repo, ".agentgopher", "worktrees")); len(entries) != 0 {
		t.Fatalf("worktree not cleaned up: %v", entries)
	}
}

func TestIsolatedIterationCopiesPathsIntoSandbox(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	os.MkdirAll(filepath.Join(repo, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(repo, "node_modules", "x", "index.js"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("node_modules\n"), 0o644)
	gitx.Run(context.Background(), repo, "add", ".gitignore")
	gitx.Run(context.Background(), repo, "commit", "-q", "-m", "ignore")
	log := &bytes.Buffer{}
	o := Options{
		HostRepoDir:    repo,
		Iterations:     1,
		Prompt:         "work",
		Provider:       shellAgent{command: `test -f node_modules/x/index.js && echo "RESULT <promise>COMPLETE</promise>" || echo "RESULT missing"`},
		Sandbox:        sandboxtest.Isolated(),
		BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead},
		CopyToWorktree: []string{"node_modules", "does-not-exist"},
		Display:        display.NewWriter(log),
		IdleTimeout:    10 * time.Second,
	}
	res, err := Orchestrate(context.Background(), o)
	if err != nil || res.CompletionSignal == "" {
		t.Fatalf("res=%+v err=%v\nlog:\n%s", res, err, log.String())
	}
}

func TestIsolatedIterationNamedBranch(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	log := &bytes.Buffer{}
	o := Options{
		HostRepoDir:    repo,
		Iterations:     1,
		Prompt:         "work",
		Branch:         "feat/agent",
		Provider:       shellAgent{command: `echo x > x.txt && git add . && git commit -q -m "on branch" && echo "RESULT <promise>COMPLETE</promise>"`},
		Sandbox:        sandboxtest.Isolated(),
		BranchStrategy: sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: "feat/agent"},
		Display:        display.NewWriter(log),
		IdleTimeout:    10 * time.Second,
	}
	res, err := Orchestrate(context.Background(), o)
	if err != nil || len(res.Commits) != 1 || res.Branch != "feat/agent" {
		t.Fatalf("res=%+v err=%v\nlog:\n%s", res, err, log.String())
	}
	if subject, _ := gitx.Run(context.Background(), repo, "log", "-1", "--format=%s", "feat/agent"); subject != "on branch" {
		t.Fatalf("subject = %q", subject)
	}
	if b, _ := gitx.CurrentBranch(context.Background(), repo); b != "main" {
		t.Fatalf("host branch changed to %q", b)
	}
}
```

- [ ] **Step 2: Write the failing Run test**

`agentgopher_isolated_test.go`:
```go
package agentgopher

import (
	"context"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/internal/sandboxtest"
	"github.com/e11en/agentgopher/sandbox"
)

func TestRunRejectsHeadWithIsolatedProvider(t *testing.T) {
	_, err := Run(context.Background(), RunOptions{
		Agent:          echoAgent{script: "true"},
		Sandbox:        sandboxtest.Isolated(),
		Cwd:            gitx.InitTestRepo(t),
		Prompt:         "x",
		BranchStrategy: &sandbox.BranchStrategy{Type: sandbox.BranchHead},
	})
	if err == nil || !strings.Contains(err.Error(), "head branch strategy is not supported with isolated providers") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunDefaultsIsolatedToMergeToHead(t *testing.T) {
	repo := gitx.InitTestRepo(t)
	res, err := Run(context.Background(), RunOptions{
		Agent:   echoAgent{script: `echo "RESULT <promise>COMPLETE</promise>"`},
		Sandbox: sandboxtest.Isolated(),
		Cwd:     repo,
		Prompt:  "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "main" || res.CompletionSignal == "" {
		t.Fatalf("res = %+v", res)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/orchestrator/ ./ -run 'Isolated'`
Expected: FAIL (the isolated kind is not handled, or the head validation message is missing).

- [ ] **Step 4: Write internal/orchestrator/isolated.go**

```go
package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/internal/sync"
	"github.com/e11en/agentgopher/internal/worktree"
	"github.com/e11en/agentgopher/sandbox"
)

const (
	// ContainerStartTimeout bounds a provider's Create call.
	ContainerStartTimeout = 2 * time.Minute
	// CopyPathsTimeout bounds copying CopyToWorktree paths into the sandbox.
	CopyPathsTimeout = 60 * time.Second
)

// ContainerStartTimeoutError reports a sandbox that did not start within Timeout.
type ContainerStartTimeoutError struct{ Timeout time.Duration }

func (e *ContainerStartTimeoutError) Error() string {
	return fmt.Sprintf("isolated sandbox start timed out after %v", e.Timeout)
}

// runIsolatedIteration creates a worktree, ships it into a fresh isolated
// sandbox, runs one iteration and ships the commits back. It returns the
// preserved worktree path when the worktree was kept after a failure.
func runIsolatedIteration(ctx context.Context, o Options, p sandbox.IsolatedProvider, i int, signals []string, idle, completion time.Duration, label func(string) string) (lifecycle.Result[iterationOutcome], string, error) {
	var zero lifecycle.Result[iterationOutcome]
	wt, err := createWorktree(ctx, o.HostRepoDir, o.BranchStrategy, o.Name)
	if err != nil {
		return zero, "", err
	}
	defer wt.Unlock()
	res, err := isolatedIterationInWorktree(ctx, o, p, wt, i, signals, idle, completion, label)
	preserved, cleanupErr := cleanupWorktree(ctx, wt, err == nil)
	if err == nil {
		err = cleanupErr
	}
	return res, preserved, err
}

// cleanupWorktree mirrors the cleanup closure of startSession in start.go:
// a failed run or a dirty worktree is preserved and its path returned, a
// clean successful one is removed.
func cleanupWorktree(ctx context.Context, wt worktree.Info, succeeded bool) (string, error) {
	dirty, err := worktree.HasUncommittedChanges(ctx, wt.Path)
	if err != nil {
		return wt.Path, err
	}
	if !succeeded || dirty {
		return wt.Path, nil
	}
	return "", worktree.Remove(ctx, wt.Path)
}

func isolatedIterationInWorktree(ctx context.Context, o Options, p sandbox.IsolatedProvider, wt worktree.Info, i int, signals []string, idle, completion time.Duration, label func(string) string) (lifecycle.Result[iterationOutcome], error) {
	var zero lifecycle.Result[iterationOutcome]
	if err := lifecycle.RunHostHooks(ctx, o.Hooks.HostOnWorktreeReady, wt.Path); err != nil {
		return zero, err
	}

	cctx, cancel := context.WithTimeout(ctx, ContainerStartTimeout)
	h, err := p.Create(cctx, sandbox.IsolatedCreateOptions{Env: o.Env})
	cancel()
	if err != nil {
		if cctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			return zero, &ContainerStartTimeoutError{Timeout: ContainerStartTimeout}
		}
		return zero, fmt.Errorf("isolated provider '%s' setup failed: %w", p.Name(), err)
	}
	defer func() { _ = h.Close(ctx) }()

	repoDir := h.WorktreePath()
	err = o.Display.Task("Syncing repo into sandbox", func(func(string)) error {
		return sync.In(ctx, h, wt.Path, repoDir, wt.Branch, sync.DefaultInTimeout)
	})
	if err != nil {
		return zero, err
	}
	if err := copyPathsIn(ctx, h, o.HostRepoDir, repoDir, o.CopyToWorktree); err != nil {
		return zero, err
	}

	hostHead := ""
	applyToHost := func(ctx context.Context) error {
		n, _ := sync.CountCommitsToSync(ctx, h, repoDir, hostHead)
		title := "No commits to sync out"
		if n == 1 {
			title = "Syncing 1 commit to host"
		} else if n > 1 {
			title = fmt.Sprintf("Syncing %d commits to host", n)
		}
		return o.Display.Task(title, func(func(string)) error { return sync.Out(ctx, h, wt.Path, repoDir) })
	}

	return lifecycle.Run(ctx, lifecycle.Options{
		HostRepoDir:      o.HostRepoDir,
		HostWorktreePath: wt.Path,
		SandboxRepoDir:   repoDir,
		Branch:           o.Branch,
		Hooks:            o.Hooks,
		Timeouts:         o.Timeouts,
		Display:          o.Display,
		ApplyToHost:      applyToHost,
	}, h, func(lctx lifecycle.Context) (iterationOutcome, error) {
		hostHead = lctx.BaseHead
		return runIteration(ctx, o, lctx, i, signals, idle, completion, label)
	})
}

// copyPathsIn copies CopyToWorktree paths that exist on the host into the
// sandbox repo. Sandbox paths are POSIX regardless of the host platform.
func copyPathsIn(ctx context.Context, h sandbox.IsolatedHandle, hostRepoDir, repoDir string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, CopyPathsTimeout)
	defer cancel()
	for _, rel := range paths {
		hostPath := filepath.Join(hostRepoDir, rel)
		if _, err := os.Stat(hostPath); err != nil {
			continue
		}
		if err := h.CopyIn(cctx, hostPath, path.Join(repoDir, filepath.ToSlash(rel))); err != nil {
			if cctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
				return &CopyToWorktreeTimeoutError{Timeout: CopyPathsTimeout, Paths: paths}
			}
			return fmt.Errorf("failed to copy %s into sandbox: %w", rel, err)
		}
	}
	return nil
}

var _ display.Display = (display.Display)(nil)
```

Remove the last line (`var _ display.Display ...`) and the `display` import if phase 3's `Options.Display` is already typed; it is only there to keep the import list honest while editing.

`CopyToWorktreeTimeoutError` is phase 3's type; if it lives in another package, reference it there.

- [ ] **Step 5: Wire the Kind switch and the Run validation**

In `internal/orchestrator/orchestrator.go`, in the per-iteration `switch o.Sandbox.Kind()` that phase 3 introduced, add:
```go
	case sandbox.KindIsolated:
		lc, preserved, err = runIsolatedIteration(ctx, o, o.Sandbox.(sandbox.IsolatedProvider), i, signals, idle, completion, label)
```
using the same variables the bind-mount and none cases assign (`lc`, `preserved`, `err`). `result.PreservedWorktreePath` is set from `preserved` the way phase 3 does for bind-mount worktrees.

In `agentgopher.go`, after the branch strategy is resolved and before cwd resolution:
```go
	if o.Sandbox.Kind() == sandbox.KindIsolated {
		if o.BranchStrategy == nil {
			strategy = sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead}
		} else if o.BranchStrategy.Type == sandbox.BranchHead {
			return RunResult{}, fmt.Errorf("head branch strategy is not supported with isolated providers")
		}
	}
```
where `strategy` is the resolved `sandbox.BranchStrategy` variable phase 3 introduced (default `BranchHead` for the other kinds).

In `errors.go` add, unless phase 3 already did:
```go
// ContainerStartTimeoutError reports a sandbox that did not start in time.
type ContainerStartTimeoutError = orchestrator.ContainerStartTimeoutError
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./...`
Expected: PASS for every package.

- [ ] **Step 7: Lint and commit**

Run: `go vet ./... && golangci-lint run ./...`
Expected: no output.

```bash
git add internal/orchestrator/ agentgopher.go agentgopher_isolated_test.go errors.go
git commit -m "Run iterations in isolated sandboxes with sync in and out"
```

---

### Task 8: Vercel provider

Port of `sandboxes/vercel.ts`. Vercel ships no Go SDK and the sandbox exec channel is not a plain REST call, so the provider drives the official `sandbox` CLI (`pnpm i -g sandbox`), which authenticates with `--token` or a stored login. Each `Create` makes a non-persistent sandbox with a generated name; `Exec` is `sandbox exec`, `CopyIn` and `CopyFileOut` are `sandbox copy` (one file per call, directories are tarred first), `Close` is `sandbox remove`.

**Files:**
- Create: `sandbox/vercel/vercel.go`, `sandbox/vercel/vercel_test.go`

**Interfaces:**
- Produces:
  ```go
  type Options struct {
      Token, Project, Team string   // passed as --token, --project, --scope when set
      Timeout string                // sandbox create --timeout, e.g. "1h"; default "1h"
      Image, Snapshot string        // --image or --snapshot
      VCPUs int                     // --vcpus when > 0
      Env map[string]string         // provider env, merged at launch time
      MaxOutputTailChars int
      Bin string                    // CLI binary, default "sandbox"
  }
  func New(opts *Options) sandbox.IsolatedProvider // Name() == "vercel"
  const RepoPath = "/vercel/sandbox/workspace"
  func createArgs(name string, o Options, env map[string]string) []string // exported for tests as CreateArgs
  func execArgs(name string, o Options, command, cwd string, sudo bool) []string // ExecArgs
  ```

- [ ] **Step 1: Write the failing tests**

`sandbox/vercel/vercel_test.go`:
```go
package vercel

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/sandbox"
)

func TestCreateArgs(t *testing.T) {
	got := strings.Join(CreateArgs("agentgopher-1", Options{Token: "tok", Project: "p", Team: "t", VCPUs: 2, Image: "img"}, map[string]string{"A": "1"}), " ")
	want := "create --silent --non-persistent --name agentgopher-1 --timeout 1h --token tok --project p --scope t --vcpus 2 --image img --env A=1"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestExecArgs(t *testing.T) {
	got := strings.Join(ExecArgs("n", Options{Token: "tok"}, "echo hi", "/w", true), " ")
	want := "exec --token tok --workdir /w --sudo n -- sh -c echo hi"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestProviderMetadata(t *testing.T) {
	p := New(nil)
	if p.Kind() != sandbox.KindIsolated || p.Name() != "vercel" {
		t.Fatalf("kind=%q name=%q", p.Kind(), p.Name())
	}
}

func TestLiveRoundTrip(t *testing.T) {
	if os.Getenv("VERCEL_TOKEN") == "" {
		t.Skip("VERCEL_TOKEN not set")
	}
	h, err := New(&Options{Token: os.Getenv("VERCEL_TOKEN"), Timeout: "10m"}).Create(context.Background(), sandbox.IsolatedCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(context.Background())
	res, err := h.Exec(context.Background(), "echo hello; exit 3", sandbox.ExecOptions{})
	if err != nil || res.ExitCode != 3 || !strings.Contains(res.Stdout, "hello") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	src := t.TempDir() + "/in.txt"
	os.WriteFile(src, []byte("ping"), 0o644)
	if err := h.CopyIn(context.Background(), src, RepoPath+"/in.txt"); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir() + "/out.txt"
	if err := h.CopyFileOut(context.Background(), RepoPath+"/in.txt", out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "ping" {
		t.Fatalf("got %q", b)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./sandbox/vercel/`
Expected: FAIL, "undefined: CreateArgs".

- [ ] **Step 3: Write vercel.go**

```go
// Package vercel runs agents in Vercel Sandbox microVMs through the official
// `sandbox` CLI. Install it with `pnpm i -g sandbox` and log in with
// `sandbox login`, or pass Options.Token.
package vercel

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/sandbox"
)

// RepoPath is where the repo lives inside a Vercel sandbox.
const RepoPath = "/vercel/sandbox/workspace"

// Options configure the provider.
type Options struct {
	Token              string
	Project            string
	Team               string
	Timeout            string
	Image              string
	Snapshot           string
	VCPUs              int
	Env                map[string]string
	MaxOutputTailChars int
	Bin                string
}

type provider struct{ o Options }

// New returns an isolated provider backed by the `sandbox` CLI.
func New(opts *Options) sandbox.IsolatedProvider {
	p := &provider{}
	if opts != nil {
		p.o = *opts
	}
	if p.o.Bin == "" {
		p.o.Bin = "sandbox"
	}
	if p.o.Timeout == "" {
		p.o.Timeout = "1h"
	}
	if p.o.MaxOutputTailChars == 0 {
		p.o.MaxOutputTailChars = stream.MaxTailChars
	}
	return p
}

func (p *provider) Kind() sandbox.Kind     { return sandbox.KindIsolated }
func (p *provider) Name() string           { return "vercel" }
func (p *provider) Env() map[string]string { return p.o.Env }

func authArgs(o Options) []string {
	var a []string
	if o.Token != "" {
		a = append(a, "--token", o.Token)
	}
	if o.Project != "" {
		a = append(a, "--project", o.Project)
	}
	if o.Team != "" {
		a = append(a, "--scope", o.Team)
	}
	return a
}

// CreateArgs builds the `sandbox create` argument list.
func CreateArgs(name string, o Options, env map[string]string) []string {
	a := []string{"create", "--silent", "--non-persistent", "--name", name, "--timeout", o.Timeout}
	a = append(a, authArgs(o)...)
	if o.VCPUs > 0 {
		a = append(a, "--vcpus", fmt.Sprint(o.VCPUs))
	}
	if o.Image != "" {
		a = append(a, "--image", o.Image)
	}
	if o.Snapshot != "" {
		a = append(a, "--snapshot", o.Snapshot)
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a = append(a, "--env", k+"="+env[k])
	}
	return a
}

// ExecArgs builds the `sandbox exec` argument list.
func ExecArgs(name string, o Options, command, cwd string, sudo bool) []string {
	a := append([]string{"exec"}, authArgs(o)...)
	a = append(a, "--workdir", cwd)
	if sudo {
		a = append(a, "--sudo")
	}
	return append(a, name, "--", "sh", "-c", command)
}

func (p *provider) Create(ctx context.Context, opts sandbox.IsolatedCreateOptions) (sandbox.IsolatedHandle, error) {
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	name := "agentgopher-" + hex.EncodeToString(suffix)
	if out, err := exec.CommandContext(ctx, p.o.Bin, CreateArgs(name, p.o, opts.Env)...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("sandbox create: %w: %s", err, strings.TrimSpace(string(out)))
	}
	h := &handle{p: p, name: name}
	if _, err := h.Exec(ctx, "mkdir -p "+RepoPath, sandbox.ExecOptions{Cwd: "/"}); err != nil {
		_ = h.Close(ctx)
		return nil, err
	}
	return h, nil
}

type handle struct {
	p    *provider
	name string
}

func (h *handle) WorktreePath() string { return RepoPath }

func (h *handle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = RepoPath
	}
	cmd := exec.CommandContext(ctx, h.p.o.Bin, ExecArgs(h.name, h.p.o, command, cwd, opts.Sudo)...)
	if opts.Stdin != nil {
		cmd.Stdin = strings.NewReader(*opts.Stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdoutTail := stream.NewBoundedTail(h.p.o.MaxOutputTailChars, "\n")
	lw := &lineWriter{onLine: func(line string) {
		stdoutTail.Push(line)
		if opts.OnLine != nil {
			opts.OnLine(line)
		}
	}}
	cmd.Stdout = lw
	err := cmd.Run()
	lw.flush()
	if ctx.Err() != nil {
		return sandbox.ExecResult{}, ctx.Err()
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
	}
	errTail := stream.NewBoundedTail(h.p.o.MaxOutputTailChars, "")
	errTail.Push(stderr.String())
	return sandbox.ExecResult{Stdout: stdoutTail.String(), Stderr: errTail.String(), ExitCode: cmd.ProcessState.ExitCode()}, nil
}

func (h *handle) copy(ctx context.Context, src, dst string) error {
	a := append([]string{"copy"}, authArgs(h.p.o)...)
	out, err := exec.CommandContext(ctx, h.p.o.Bin, append(a, src, dst)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sandbox copy %s %s: %w: %s", src, dst, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (h *handle) CopyIn(ctx context.Context, hostPath, sandboxPath string) error {
	info, err := os.Stat(hostPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return h.copy(ctx, hostPath, h.name+":"+sandboxPath)
	}
	// The CLI copies one file per call, so a directory travels as a tarball.
	tar, err := os.CreateTemp("", "agentgopher-copyin-*.tar.gz")
	if err != nil {
		return err
	}
	tar.Close()
	defer os.Remove(tar.Name())
	if out, err := exec.CommandContext(ctx, "tar", "-czf", tar.Name(), "-C", hostPath, ".").CombinedOutput(); err != nil {
		return fmt.Errorf("tar: %w: %s", err, out)
	}
	remote := "/tmp/" + filepath.Base(tar.Name())
	if err := h.copy(ctx, tar.Name(), h.name+":"+remote); err != nil {
		return err
	}
	res, err := h.Exec(ctx, fmt.Sprintf(`mkdir -p "%s" && tar -xzf "%s" -C "%s" && rm -f "%s"`, sandboxPath, remote, sandboxPath, remote), sandbox.ExecOptions{Cwd: "/"})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("extract in sandbox failed: %s", res.Stderr)
	}
	return nil
}

func (h *handle) CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error {
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		return err
	}
	return h.copy(ctx, h.name+":"+sandboxPath, hostPath)
}

func (h *handle) Close(ctx context.Context) error {
	a := append([]string{"remove"}, authArgs(h.p.o)...)
	out, err := exec.CommandContext(ctx, h.p.o.Bin, append(a, h.name)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sandbox remove: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

type lineWriter struct {
	onLine func(string)
	buf    []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
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

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.onLine(string(w.buf))
		w.buf = nil
	}
}
```

`lineWriter` duplicates the one in `sandbox/nosandbox`. Move that type to `internal/stream` as `stream.NewLineWriter(onLine func(string)) *LineWriter` with `Write` and `Flush`, use it from both providers, and keep `nosandbox`'s tests green. Do that in this task so the duplicate never lands.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./sandbox/... `
Expected: PASS, with `TestLiveRoundTrip` skipped unless `VERCEL_TOKEN` is set. When it is set and the CLI is installed, run `VERCEL_TOKEN=... go test ./sandbox/vercel/ -run Live -v` once by hand and record the outcome in the commit message.

- [ ] **Step 5: Commit**

```bash
git add sandbox/vercel/ internal/stream/ sandbox/nosandbox/
git commit -m "Add Vercel isolated provider over the sandbox CLI"
```

---

### Task 9: Daytona provider

Port of `sandboxes/daytona.ts` on the official Daytona Go SDK. Streaming uses a session command plus the SDK's log stream, as upstream does.

**Files:**
- Create: `sandbox/daytona/daytona.go`, `sandbox/daytona/daytona_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Produces:
  ```go
  type Options struct {
      APIKey, APIURL, Target string      // fall back to DAYTONA_API_KEY, DAYTONA_API_URL, DAYTONA_TARGET
      Snapshot string                    // create from a snapshot when set
      Image string                       // else create from this image; SDK default when both empty
      Env map[string]string
      MaxOutputTailChars int
  }
  func New(opts *Options) sandbox.IsolatedProvider // Name() == "daytona"
  ```
- The SDK module: `go get github.com/daytonaio/daytona/libs/sdk-go@latest`; import path `github.com/daytonaio/daytona/libs/sdk-go/pkg/daytona` (the docs site also prints `github.com/daytona/clients/sdk-go/pkg/daytona`; use whichever `go get` resolves, and fix the import lines below to match). The identifiers used here come from the SDK reference: `daytona.NewClientWithConfig(*types.DaytonaConfig)`, `client.Create(ctx, params)`, `sandbox.Process.ExecuteCommand(ctx, cmd, opts...)`, `sandbox.Process.CreateSession`, `ExecuteSessionCommand(ctx, sessionID, cmd, runAsync, suppressInputEcho)`, `GetSessionCommandLogsStream(ctx, sessionID, cmdID, stdout, stderr chan<- string)`, `sandbox.FileSystem.UploadFile(ctx, source, dest)`, `DownloadFile(ctx, remote, localPath *string)`, `sandbox.GetWorkingDir(ctx)`, `sandbox.Delete(ctx)`. Confirm each against `go doc` after `go get` and adjust names; the structure of the provider does not change.

- [ ] **Step 1: Write the failing tests**

`sandbox/daytona/daytona_test.go`:
```go
package daytona

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/sandbox"
)

func TestProviderMetadata(t *testing.T) {
	p := New(nil)
	if p.Kind() != sandbox.KindIsolated || p.Name() != "daytona" {
		t.Fatalf("kind=%q name=%q", p.Kind(), p.Name())
	}
}

func TestLiveRoundTrip(t *testing.T) {
	if os.Getenv("DAYTONA_API_KEY") == "" {
		t.Skip("DAYTONA_API_KEY not set")
	}
	ctx := context.Background()
	h, err := New(nil).Create(ctx, sandbox.IsolatedCreateOptions{Env: map[string]string{"GREETING": "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(ctx)

	res, err := h.Exec(ctx, "echo $GREETING; exit 3", sandbox.ExecOptions{})
	if err != nil || res.ExitCode != 3 || !strings.Contains(res.Stdout, "hi") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	var lines []string
	res, err = h.Exec(ctx, "echo one; echo two", sandbox.ExecOptions{OnLine: func(l string) { lines = append(lines, l) }})
	if err != nil || res.ExitCode != 0 || len(lines) != 2 {
		t.Fatalf("lines=%v res=%+v err=%v", lines, res, err)
	}

	src := t.TempDir() + "/in.txt"
	os.WriteFile(src, []byte("ping"), 0o644)
	if err := h.CopyIn(ctx, src, h.WorktreePath()+"/in.txt"); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir() + "/nested/out.txt"
	if err := h.CopyFileOut(ctx, h.WorktreePath()+"/in.txt", out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); string(b) != "ping" {
		t.Fatalf("got %q", b)
	}
}
```

- [ ] **Step 2: Add the SDK and run the tests to verify they fail**

Run:
```bash
go get github.com/daytonaio/daytona/libs/sdk-go@latest
go doc github.com/daytonaio/daytona/libs/sdk-go/pkg/daytona | head -60
go test ./sandbox/daytona/
```
Expected: the `go doc` output lists `NewClientWithConfig` and `Client.Create`; the test fails with "undefined: New".

- [ ] **Step 3: Write daytona.go**

```go
// Package daytona runs agents in Daytona sandboxes through the official Go SDK.
package daytona

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/daytonaio/daytona/libs/sdk-go/pkg/daytona"
	"github.com/daytonaio/daytona/libs/sdk-go/pkg/types"

	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/sandbox"
)

// Options configure the provider. Empty auth fields fall back to the
// DAYTONA_API_KEY, DAYTONA_API_URL and DAYTONA_TARGET environment variables.
type Options struct {
	APIKey             string
	APIURL             string
	Target             string
	Snapshot           string
	Image              string
	Env                map[string]string
	MaxOutputTailChars int
}

type provider struct{ o Options }

// New returns an isolated provider that creates one Daytona sandbox per run.
func New(opts *Options) sandbox.IsolatedProvider {
	p := &provider{}
	if opts != nil {
		p.o = *opts
	}
	if p.o.MaxOutputTailChars == 0 {
		p.o.MaxOutputTailChars = stream.MaxTailChars
	}
	return p
}

func (p *provider) Kind() sandbox.Kind     { return sandbox.KindIsolated }
func (p *provider) Name() string           { return "daytona" }
func (p *provider) Env() map[string]string { return p.o.Env }

func (p *provider) Create(ctx context.Context, opts sandbox.IsolatedCreateOptions) (sandbox.IsolatedHandle, error) {
	client, err := daytona.NewClientWithConfig(&types.DaytonaConfig{APIKey: p.o.APIKey, APIUrl: p.o.APIURL, Target: p.o.Target})
	if err != nil {
		return nil, fmt.Errorf("daytona client: %w", err)
	}
	var params any
	switch {
	case p.o.Snapshot != "":
		params = types.SnapshotParams{Snapshot: p.o.Snapshot, SandboxBaseParams: types.SandboxBaseParams{EnvVars: opts.Env}}
	default:
		params = types.ImageParams{Image: p.o.Image, SandboxBaseParams: types.SandboxBaseParams{EnvVars: opts.Env}}
	}
	sb, err := client.Create(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("daytona create: %w", err)
	}
	workdir, err := sb.GetWorkingDir(ctx)
	if err != nil || workdir == "" {
		workdir = "/home/daytona"
	}
	return &handle{p: p, sb: sb, workdir: workdir}, nil
}

type handle struct {
	p       *provider
	sb      *daytona.Sandbox
	workdir string
}

func (h *handle) WorktreePath() string { return h.workdir }

func (h *handle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd = h.workdir
	}
	if opts.Sudo {
		command = "sudo " + command
	}
	// The SDK has no stdin pipe; deliver a prompt through a temp file.
	if opts.Stdin != nil {
		stdinPath := path.Join("/tmp", "agentgopher-stdin-"+randomSuffix())
		if err := h.sb.FileSystem.UploadFile(ctx, []byte(*opts.Stdin), stdinPath); err != nil {
			return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
		}
		command = fmt.Sprintf("(%s) < %s; rc=$?; rm -f %s; exit $rc", command, stdinPath, stdinPath)
	}
	full := fmt.Sprintf("cd %s && %s", shellQuote(cwd), command)

	if opts.OnLine == nil {
		res, err := h.sb.Process.ExecuteCommand(ctx, full)
		if err != nil {
			return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
		}
		return sandbox.ExecResult{Stdout: res.Result, ExitCode: res.ExitCode}, nil
	}

	session := "agentgopher-" + randomSuffix()
	if err := h.sb.Process.CreateSession(ctx, session); err != nil {
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
	}
	defer func() { _ = h.sb.Process.DeleteSession(context.Background(), session) }()

	started, err := h.sb.Process.ExecuteSessionCommand(ctx, session, full, true, false)
	if err != nil {
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
	}
	cmdID, _ := started["cmdId"].(string)

	stdoutTail := stream.NewBoundedTail(h.p.o.MaxOutputTailChars, "\n")
	stderrTail := stream.NewBoundedTail(h.p.o.MaxOutputTailChars, "")
	lw := stream.NewLineWriter(func(line string) {
		stdoutTail.Push(line)
		opts.OnLine(line)
	})
	stdoutCh, stderrCh := make(chan string), make(chan string)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for chunk := range stdoutCh {
			_, _ = lw.Write([]byte(chunk))
		}
		lw.Flush()
	}()
	go func() {
		defer wg.Done()
		for chunk := range stderrCh {
			stderrTail.Push(chunk)
		}
	}()
	streamErr := h.sb.Process.GetSessionCommandLogsStream(ctx, session, cmdID, stdoutCh, stderrCh)
	close(stdoutCh)
	close(stderrCh)
	wg.Wait()
	if ctx.Err() != nil {
		return sandbox.ExecResult{}, ctx.Err()
	}
	if streamErr != nil {
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: streamErr}
	}
	info, err := h.sb.Process.GetSessionCommand(ctx, session, cmdID)
	if err != nil {
		return sandbox.ExecResult{}, &sandbox.StartError{Command: command, Cause: err}
	}
	exit := 0
	if info.ExitCode != nil {
		exit = int(*info.ExitCode)
	}
	return sandbox.ExecResult{Stdout: stdoutTail.String(), Stderr: stderrTail.String(), ExitCode: exit}, nil
}

func (h *handle) CopyIn(ctx context.Context, hostPath, sandboxPath string) error {
	info, err := os.Stat(hostPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return h.sb.FileSystem.UploadFile(ctx, hostPath, sandboxPath)
	}
	return filepath.WalkDir(hostPath, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(hostPath, p)
		return h.sb.FileSystem.UploadFile(ctx, p, path.Join(sandboxPath, filepath.ToSlash(rel)))
	})
}

func (h *handle) CopyFileOut(ctx context.Context, sandboxPath, hostPath string) error {
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		return err
	}
	_, err := h.sb.FileSystem.DownloadFile(ctx, sandboxPath, &hostPath)
	return err
}

func (h *handle) Close(ctx context.Context) error { return h.sb.Delete(ctx) }

func randomSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
```

Adjust field and method names to what `go doc` shows (for example `EnvVars` on `SandboxBaseParams`, `ExitCode` on the session command info, the `cmdId` key of the session response). The upstream TypeScript uses exactly these concepts, so the mapping is one to one.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go build ./... && go test ./sandbox/daytona/`
Expected: builds; `TestLiveRoundTrip` skipped unless `DAYTONA_API_KEY` is set. When it is set, run `go test ./sandbox/daytona/ -run Live -v` once by hand and record the outcome in the commit message.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum sandbox/daytona/
git commit -m "Add Daytona isolated provider over the official Go SDK"
```

---

### Task 10: Example, README and CI

**Files:**
- Create: `examples/isolated/main.go`
- Modify: `README.md`, `.github/workflows/ci.yml`

- [ ] **Step 1: Write the example**

`examples/isolated/main.go`:
```go
// Command isolated runs Claude Code once in a Vercel or Daytona sandbox and
// merges the commits back into the current branch.
// Usage: go run ./examples/isolated [vercel|daytona] [path-to-repo]
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
	"github.com/e11en/agentgopher/sandbox/daytona"
	"github.com/e11en/agentgopher/sandbox/vercel"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	providerName, repo := "vercel", "."
	if len(os.Args) > 1 {
		providerName = os.Args[1]
	}
	if len(os.Args) > 2 {
		repo = os.Args[2]
	}
	var provider sandbox.Provider
	switch providerName {
	case "daytona":
		provider = daytona.New(nil)
	default:
		provider = vercel.New(nil)
	}
	promptFile, _ := filepath.Abs("examples/simple/prompt.md")

	res, err := agentgopher.Run(ctx, agentgopher.RunOptions{
		Name:       "worker",
		Agent:      agent.ClaudeCode(agent.DefaultModel, nil),
		Sandbox:    provider,
		Cwd:        repo,
		PromptFile: promptFile,
		Hooks: agentgopher.Hooks{SandboxOnSandboxReady: []agentgopher.SandboxHook{
			{Command: "command -v claude >/dev/null || npm install -g @anthropic-ai/claude-code", Timeout: 0},
		}},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "run failed:", err)
		os.Exit(1)
	}
	fmt.Printf("done: signal=%q commits=%d branch=%s log=%s\n", res.CompletionSignal, len(res.Commits), res.Branch, res.LogFilePath)
}
```

- [ ] **Step 2: Build and run the suite**

Run: `go build ./... && go vet ./... && go test -race ./... && golangci-lint run ./...`
Expected: all green, live tests skipped.

- [ ] **Step 3: Update README**

Add under the status paragraph:
```markdown
Isolated providers (phase 4): `sandbox/vercel` drives the official `sandbox`
CLI (`pnpm i -g sandbox`, then `sandbox login` or `VERCEL_TOKEN`), and
`sandbox/daytona` uses the official Daytona Go SDK (`DAYTONA_API_KEY`). The
repo is shipped in as a git bundle and the agent's commits come back as
patches; failed patch applications are preserved under
`.agentgopher/patches/` with recovery commands on stderr. Custom providers
implement `sandbox.IsolatedProviderConfig`.

Run the example: `go run ./examples/isolated vercel /path/to/repo`.
```

- [ ] **Step 4: Commit and push**

```bash
git add examples/isolated/ README.md
git commit -m "Add isolated provider example and document phase 4"
git push origin main
gh run watch --repo e11en/agentgopher --exit-status
```
Expected: the `ci` workflow passes.

---

## Self-review notes

- Spec coverage for phase 4: sync in and out (Tasks 2, 4 to 6), `IsolatedProvider` interface for custom providers (Task 1), `vercel` (Task 8), `daytona` (Task 9), orchestration and strategy rules (Task 7). ADR 0017 is implemented by `BaseRef` and tested in Task 4 and Task 5.
- Go SDK check: Daytona has an official Go SDK and Task 9 uses it; this is the one dependency this plan adds. Vercel has TypeScript and Python SDKs and a Docker-style CLI; the exec channel is not a documented REST endpoint, so Task 8 drives the official CLI, matching how Docker and Podman are driven in phase 3.
- Left out on purpose, with the reason: the `branch` variant of the recovery message (upstream never passes it from `syncOut`); Vercel pass-through options `ports`, `networkPolicy`, `runtime` and `source` (the CLI exposes `--publish-port`, `--network-policy`, `--image`; add fields when someone needs them); Daytona `Resources` on image params (same); `Interactive` on isolated handles (phase 5).
- Deviations from upstream: upstream's `syncOut` reads the diff and untracked list once and uses them for both save and apply; this plan does the same through `outState`. Upstream copies untracked files with `copyFileOut` into `patchDir/untracked`, then copies host to host; identical here. Upstream's `Out` prints the recovery message with `console.error`; here `fmt.Fprintln(os.Stderr, ...)`.
- Type consistency: `sync.In` takes `(h, hostWorktreePath, sandboxRepoDir, branch, timeout)` and `sync.Out` takes `(h, hostWorktreePath, sandboxRepoDir)` everywhere, including the orchestrator call sites in Task 7; `sandbox.IsolatedHandle` embeds `sandbox.Handle` so every `Exec`-based helper accepts it.
- Review Focus mapping: 1 and 2 and 4 in Task 5 (`TestOutSkipsHeaderOnlyMergeCommitPatches`, `TestOutTwiceOnSameSandboxShipsOnlyNewCommits`, `TestOutAbortsStaleAmSession`), 3 and 5 in Task 6 (`TestOutShipsUntrackedFilesIncludingNewDirectories`, `TestOutPreservesArtifactsWhenAmFails`).
