# agentgopher Phase 1 and 2: Skeleton and Core Loop Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go module where `agentgopher.Run` drives Claude Code for N iterations on the host (no sandbox, head branch strategy), with prompt templates, completion signal, idle and completion timeouts, a run log, and stream events.

**Architecture:** The public root package `agentgopher` exposes `Run`, `RunOptions` and `RunResult`. Pluggable parts live in public subpackages `agent` (agent CLIs) and `sandbox` (execution environments). Everything else is `internal/`: prompt handling, the iteration loop, git lifecycle, display and env. Upstream's Effect services become plain Go interfaces, `AbortSignal` becomes `context.Context`, tagged errors become exported error structs matched with `errors.As`.

**Tech Stack:** Go 1.25 (toolchain installed: go1.27.0), standard library only in this plan. CI on GitHub Actions with `go test`, `go vet` and `golangci-lint`.

**Spec:** `docs/plan.md` (sections "Feature inventory and Go mapping", "Go design decisions", phases 1 and 2). Upstream reference: `mattpocock/sandcastle` at commit `7a4e7f7`; file names below refer to its `src/` directory. Clone it to read the original when a task is unclear: `git clone --depth 1 https://github.com/mattpocock/sandcastle.git /tmp/sandcastle && git -C /tmp/sandcastle checkout 7a4e7f7`.

## Global Constraints

- Module path `github.com/e11en/agentgopher`, `go 1.25` in `go.mod`.
- Standard library only. No third-party dependency in this plan.
- Config directory is `.agentgopher/` (upstream: `.sandcastle/`). Env file `.agentgopher/.env`, logs in `.agentgopher/logs/`.
- Public API: root package, `agent`, `sandbox`, `sandbox/nosandbox`. Everything else under `internal/`.
- Code comments, test names and commit messages in English. No em or en dashes anywhere (use ` - ` or commas).
- Commit messages: imperative, no attribution footer, no `Co-Authored-By`.
- Default completion signal `<promise>COMPLETE</promise>`, default idle timeout 600 s, default completion timeout 60 s, default max iterations 1, default model `claude-opus-4-8`.
- Every `exec` implementation streams lines live through `OnLine` while the process runs. A buffered implementation breaks idle timeouts.
- Tests must not require Claude Code, Docker or network. Use a fake `agent.Provider` and the `nosandbox` provider running `sh`.

## Review Focus

1. A prompt file with CRLF line endings: `{{KEY}}` and `` !`cmd` `` still match and output contains no stray `\r`. Test added in Task 5.
2. A shell expression whose stdout is empty: the placeholder is replaced by an empty string, not left in place. Test added in Task 8.
3. The agent prints the completion signal split over two `text` events: still detected, because detection scans the accumulated output. Test added in Task 13.
4. The agent exits non-zero with an empty stderr: the error carries the last 20 non-empty stdout lines. Test added in Task 13.
5. `.agentgopher/.env` declares `KEY=` with an empty value and the process env has `KEY=x`: the resolved env uses `x`. Test added in Task 7.

---

## File structure

```
go.mod
README.md
.gitignore
.github/workflows/ci.yml
.golangci.yml
agentgopher.go                     Run, RunOptions, RunResult, IterationResult, Timeouts, Logging, Hooks
agentgopher_test.go
errors.go                          exported error types
agent/agent.go                     Provider, CommandOptions, PrintCommand, StreamEvent, Usage
agent/claudecode.go                ClaudeCode provider, stream-json parser
agent/claudecode_test.go
sandbox/sandbox.go                 ExecResult, ExecOptions, Handle, Provider, Kind, BranchStrategy
sandbox/nosandbox/nosandbox.go     host provider (sh -c)
sandbox/nosandbox/nosandbox_test.go
internal/stream/tail.go            BoundedTail
internal/stream/tail_test.go
internal/stream/delta.go           TextDeltaBuffer
internal/stream/delta_test.go
internal/prompt/resolve.go         Resolve (inline vs template)
internal/prompt/args.go            placeholder substitution
internal/prompt/expand.go          shell expression expansion
internal/prompt/prompt_test.go
internal/env/env.go                .env parsing, Resolve, MergeProviderEnv
internal/env/env_test.go
internal/display/display.go        Display interface, Severity, writer-backed implementation
internal/display/display_test.go
internal/gitx/gitx.go              host git helpers
internal/gitx/gitx_test.go
internal/testrepo/testrepo.go      Init(t): throwaway git repo for tests in any package
internal/lifecycle/lifecycle.go    git setup in sandbox, hooks, base HEAD, commit collection
internal/lifecycle/lifecycle_test.go
internal/orchestrator/invoke.go    one agent invocation with timers
internal/orchestrator/orchestrator.go  iteration loop
internal/orchestrator/orchestrator_test.go
examples/simple/main.go
examples/simple/prompt.md
```

Known simplification in this plan: `Logging{Type: "stdout"}` writes plain lines to stdout. Upstream's spinner UI (Clack) is deferred to the phase 5 plan.

---

### Task 1: Module skeleton and CI

**Files:**
- Create: `go.mod`, `.gitignore`, `.golangci.yml`, `.github/workflows/ci.yml`, `README.md`, `errors.go`, `errors_test.go`

**Interfaces:**
- Produces: error types used by every later task:
  - `type ExecError struct { Command string; ExitCode int; Stderr string }` (ExitCode -1 when the process did not start)
  - `type PromptError struct { Message string; ExitCode int }`
  - `type AgentError struct { Message string }`
  - `type AgentIdleTimeoutError struct { Timeout time.Duration }`
  - `type PromptExpansionTimeoutError struct { Expression string; Timeout, Elapsed time.Duration }`
  - `type HookTimeoutError struct { Command string; Timeout time.Duration }`
  - `type GitSetupTimeoutError struct { Command string; Timeout time.Duration }`
  - `type CommitCollectionTimeoutError struct { Timeout time.Duration }`
  - `type CwdError struct { Cwd string; Message string }`
  - all implement `error` with a pointer receiver.

- [ ] **Step 1: Create go.mod and .gitignore**

```bash
cd /path/to/agentgopher
go mod init github.com/e11en/agentgopher
```

Edit `go.mod` so the version line reads `go 1.25`.

`.gitignore`:
```
.agentgopher/.env
.agentgopher/logs/
.agentgopher/worktrees/
/dist/
```

- [ ] **Step 2: Write the failing error test**

`errors_test.go`:
```go
package agentgopher

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestErrorsMatchWithErrorsAs(t *testing.T) {
	wrapped := fmt.Errorf("iteration 1: %w", &AgentIdleTimeoutError{Timeout: 5 * time.Second})
	var idle *AgentIdleTimeoutError
	if !errors.As(wrapped, &idle) {
		t.Fatal("expected errors.As to find AgentIdleTimeoutError")
	}
	if idle.Timeout != 5*time.Second {
		t.Fatalf("Timeout = %v, want 5s", idle.Timeout)
	}
	if got := idle.Error(); got != "agent idle for 5s - no output received" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestExecErrorMessage(t *testing.T) {
	err := &ExecError{Command: "git status", ExitCode: 128, Stderr: "fatal: not a git repo"}
	want := "command failed (exit 128): git status\nfatal: not a git repo"
	if err.Error() != want {
		t.Fatalf("Error() = %q, want %q", err.Error(), want)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./... -run 'TestErrorsMatchWithErrorsAs|TestExecErrorMessage'`
Expected: FAIL, "undefined: AgentIdleTimeoutError".

- [ ] **Step 4: Write errors.go**

```go
// Package agentgopher orchestrates AI coding agents in sandboxes.
package agentgopher

import (
	"fmt"
	"time"
)

// ExecError reports a command that ran in a sandbox and exited non-zero,
// or could not be started (ExitCode -1).
type ExecError struct {
	Command  string
	ExitCode int
	Stderr   string
}

func (e *ExecError) Error() string {
	if e.ExitCode < 0 {
		return fmt.Sprintf("command could not be started: %s\n%s", e.Command, e.Stderr)
	}
	return fmt.Sprintf("command failed (exit %d): %s\n%s", e.ExitCode, e.Command, e.Stderr)
}

// PromptError reports a prompt that could not be resolved, substituted or expanded.
// ExitCode is set when a shell expression exited non-zero, otherwise 0.
type PromptError struct {
	Message  string
	ExitCode int
}

func (e *PromptError) Error() string { return e.Message }

// AgentError reports an agent process that exited non-zero.
type AgentError struct{ Message string }

func (e *AgentError) Error() string { return e.Message }

// AgentIdleTimeoutError reports an agent that produced no output for Timeout.
type AgentIdleTimeoutError struct{ Timeout time.Duration }

func (e *AgentIdleTimeoutError) Error() string {
	return fmt.Sprintf("agent idle for %v - no output received", e.Timeout)
}

// PromptExpansionTimeoutError reports a shell expression that ran longer than Timeout.
type PromptExpansionTimeoutError struct {
	Expression string
	Timeout    time.Duration
	Elapsed    time.Duration
}

func (e *PromptExpansionTimeoutError) Error() string {
	return fmt.Sprintf("shell expression `%s` timed out after %v", e.Expression, e.Elapsed)
}

// HookTimeoutError reports a lifecycle hook that ran longer than Timeout.
type HookTimeoutError struct {
	Command string
	Timeout time.Duration
}

func (e *HookTimeoutError) Error() string {
	return fmt.Sprintf("hook '%s' timed out after %v", e.Command, e.Timeout)
}

// GitSetupTimeoutError reports an in-sandbox git setup command that ran longer than Timeout.
type GitSetupTimeoutError struct {
	Command string
	Timeout time.Duration
}

func (e *GitSetupTimeoutError) Error() string {
	return fmt.Sprintf("git command timed out after %v: %s", e.Timeout, e.Command)
}

// CommitCollectionTimeoutError reports that listing the run's commits ran longer than Timeout.
type CommitCollectionTimeoutError struct{ Timeout time.Duration }

func (e *CommitCollectionTimeoutError) Error() string {
	return fmt.Sprintf("commit collection timed out after %v", e.Timeout)
}

// CwdError reports a Cwd option that is missing or not a directory.
type CwdError struct {
	Cwd     string
	Message string
}

func (e *CwdError) Error() string { return e.Message }
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./...`
Expected: `ok  github.com/e11en/agentgopher`

- [ ] **Step 6: Add lint config, CI and README**

`.golangci.yml`:
```yaml
version: "2"
linters:
  default: standard
```

`.github/workflows/ci.yml`:
```yaml
name: ci
on:
  push:
    branches: [main]
  pull_request:
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: go vet ./...
      - run: go test -race ./...
      - uses: golangci/golangci-lint-action@v7
        with:
          version: latest
```

`README.md`:
```markdown
# agentgopher

Orchestrate sandboxed AI coding agents from Go. A port of
[sandcastle](https://github.com/mattpocock/sandcastle) with Jira and Windshift
as issue trackers and a Kubernetes sandbox provider.

Status: phase 2 of `docs/plan.md` (core loop, no sandbox, head strategy).

```go
result, err := agentgopher.Run(ctx, agentgopher.RunOptions{
    Agent:      agent.ClaudeCode("claude-opus-4-8", nil),
    Sandbox:    nosandbox.New(nil),
    PromptFile: ".agentgopher/prompt.md",
})
```

See `docs/plan.md` for the roadmap.
```

- [ ] **Step 7: Verify lint and commit**

Run: `go vet ./... && golangci-lint run ./...`
Expected: no output.

```bash
git add go.mod .gitignore .golangci.yml .github/workflows/ci.yml README.md errors.go errors_test.go
git commit -m "Add module skeleton, error types and CI"
```

---

### Task 2: BoundedTail

Port of `boundedTail.ts`. Keeps only the last `maxChars` of streamed output so a long run cannot exhaust memory.

**Files:**
- Create: `internal/stream/tail.go`, `internal/stream/tail_test.go`

**Interfaces:**
- Produces: `stream.NewBoundedTail(maxChars int, separator string) *BoundedTail`, `(*BoundedTail).Push(item string)`, `(*BoundedTail).String() string`, `const stream.MaxTailChars = 64 * 1024`.

- [ ] **Step 1: Write the failing tests**

`internal/stream/tail_test.go`:
```go
package stream

import "testing"

func TestBoundedTailKeepsEverythingUnderBudget(t *testing.T) {
	tail := NewBoundedTail(100, "\n")
	tail.Push("a")
	tail.Push("b")
	if got := tail.String(); got != "a\nb" {
		t.Fatalf("String() = %q", got)
	}
}

func TestBoundedTailDropsOldestItems(t *testing.T) {
	tail := NewBoundedTail(5, "\n")
	tail.Push("aa")
	tail.Push("bb")
	tail.Push("cc")
	if got := tail.String(); got != "bb\ncc" {
		t.Fatalf("String() = %q, want %q", got, "bb\ncc")
	}
}

func TestBoundedTailTruncatesOversizedItem(t *testing.T) {
	tail := NewBoundedTail(3, "")
	tail.Push("abcdef")
	if got := tail.String(); got != "def" {
		t.Fatalf("String() = %q, want %q", got, "def")
	}
}

func TestBoundedTailNeverExceedsMax(t *testing.T) {
	tail := NewBoundedTail(10, "\n")
	for i := 0; i < 50; i++ {
		tail.Push("xxxx")
	}
	if l := len(tail.String()); l > 10 {
		t.Fatalf("len = %d, want <= 10", l)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/stream/`
Expected: FAIL, "undefined: NewBoundedTail".

- [ ] **Step 3: Write tail.go**

```go
// Package stream holds helpers for agent output streams.
package stream

import "strings"

// MaxTailChars is the default budget of a BoundedTail: well above any
// completion signal or structured output payload, far below memory limits.
const MaxTailChars = 64 * 1024

// BoundedTail is a rolling tail of strings bounded by total character length.
type BoundedTail struct {
	items     []string
	total     int
	maxChars  int
	separator string
}

// NewBoundedTail returns a tail that joins items with separator and keeps
// String() at most maxChars long.
func NewBoundedTail(maxChars int, separator string) *BoundedTail {
	return &BoundedTail{maxChars: maxChars, separator: separator}
}

// Push appends item, evicting the oldest items to stay within budget. An item
// longer than the budget is truncated to its own tail.
func (b *BoundedTail) Push(item string) {
	if len(item) > b.maxChars {
		item = item[len(item)-b.maxChars:]
	}
	if len(b.items) > 0 {
		b.total += len(b.separator)
	}
	b.total += len(item)
	b.items = append(b.items, item)
	for b.total > b.maxChars && len(b.items) > 1 {
		b.total -= len(b.items[0]) + len(b.separator)
		b.items = b.items[1:]
	}
}

// String joins the retained items.
func (b *BoundedTail) String() string { return strings.Join(b.items, b.separator) }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/stream/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/stream/tail.go internal/stream/tail_test.go
git commit -m "Add bounded output tail"
```

---

### Task 3: TextDeltaBuffer

Port of `TextDeltaBuffer.ts`. Collects streamed text deltas and flushes readable chunks: on newline, sentence boundary, 80 characters, or 50 ms of silence.

**Files:**
- Create: `internal/stream/delta.go`, `internal/stream/delta_test.go`

**Interfaces:**
- Produces: `stream.NewTextDeltaBuffer(onFlush func(string)) *TextDeltaBuffer`, methods `Write(text string)`, `Flush()`, `Close()`. Safe for use from one goroutine plus the internal timer.

- [ ] **Step 1: Write the failing tests**

`internal/stream/delta_test.go`:
```go
package stream

import (
	"sync"
	"testing"
	"time"
)

type flushRecorder struct {
	mu     sync.Mutex
	chunks []string
}

func (r *flushRecorder) record(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chunks = append(r.chunks, s)
}

func (r *flushRecorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.chunks...)
}

func TestTextDeltaBufferFlushesOnNewline(t *testing.T) {
	rec := &flushRecorder{}
	b := NewTextDeltaBuffer(rec.record)
	defer b.Close()
	b.Write("hello")
	b.Write(" world\n")
	if got := rec.get(); len(got) != 1 || got[0] != "hello world\n" {
		t.Fatalf("chunks = %q", got)
	}
}

func TestTextDeltaBufferFlushesOnSentenceBoundary(t *testing.T) {
	rec := &flushRecorder{}
	b := NewTextDeltaBuffer(rec.record)
	defer b.Close()
	b.Write("Done. ")
	if got := rec.get(); len(got) != 1 || got[0] != "Done. " {
		t.Fatalf("chunks = %q", got)
	}
}

func TestTextDeltaBufferFlushesAtLengthThreshold(t *testing.T) {
	rec := &flushRecorder{}
	b := NewTextDeltaBuffer(rec.record)
	defer b.Close()
	long := make([]byte, 80)
	for i := range long {
		long[i] = 'x'
	}
	b.Write(string(long))
	if got := rec.get(); len(got) != 1 {
		t.Fatalf("chunks = %q", got)
	}
}

func TestTextDeltaBufferFlushesAfterDebounce(t *testing.T) {
	rec := &flushRecorder{}
	b := NewTextDeltaBuffer(rec.record)
	defer b.Close()
	b.Write("partial")
	if got := rec.get(); len(got) != 0 {
		t.Fatalf("flushed too early: %q", got)
	}
	time.Sleep(120 * time.Millisecond)
	if got := rec.get(); len(got) != 1 || got[0] != "partial" {
		t.Fatalf("chunks = %q", got)
	}
}

func TestTextDeltaBufferCloseFlushesRemainder(t *testing.T) {
	rec := &flushRecorder{}
	b := NewTextDeltaBuffer(rec.record)
	b.Write("tail")
	b.Close()
	if got := rec.get(); len(got) != 1 || got[0] != "tail" {
		t.Fatalf("chunks = %q", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/stream/ -run TextDeltaBuffer`
Expected: FAIL, "undefined: NewTextDeltaBuffer".

- [ ] **Step 3: Write delta.go**

```go
package stream

import (
	"strings"
	"sync"
	"time"
)

const (
	deltaLengthThreshold = 80
	deltaDebounce        = 50 * time.Millisecond
)

// TextDeltaBuffer buffers streaming text deltas and flushes them as readable
// multi-word chunks: on a newline, a sentence boundary, 80 characters, or
// after 50 ms without new writes.
type TextDeltaBuffer struct {
	mu      sync.Mutex
	buf     strings.Builder
	timer   *time.Timer
	onFlush func(string)
}

// NewTextDeltaBuffer returns a buffer that calls onFlush with each chunk.
func NewTextDeltaBuffer(onFlush func(string)) *TextDeltaBuffer {
	return &TextDeltaBuffer{onFlush: onFlush}
}

// Write appends text and flushes when a boundary is reached.
func (b *TextDeltaBuffer) Write(text string) {
	if text == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.WriteString(text)
	b.stopTimer()
	if b.shouldFlush() {
		b.flushLocked()
		return
	}
	b.timer = time.AfterFunc(deltaDebounce, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.flushLocked()
	})
}

// Flush force-flushes buffered text.
func (b *TextDeltaBuffer) Flush() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopTimer()
	b.flushLocked()
}

// Close flushes the remainder and stops the timer.
func (b *TextDeltaBuffer) Close() { b.Flush() }

func (b *TextDeltaBuffer) shouldFlush() bool {
	s := b.buf.String()
	if strings.Contains(s, "\n") || len(s) >= deltaLengthThreshold {
		return true
	}
	return strings.HasSuffix(s, ". ") || strings.HasSuffix(s, "! ") || strings.HasSuffix(s, "? ")
}

func (b *TextDeltaBuffer) flushLocked() {
	if b.buf.Len() == 0 {
		return
	}
	text := b.buf.String()
	b.buf.Reset()
	b.onFlush(text)
}

func (b *TextDeltaBuffer) stopTimer() {
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/stream/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/stream/delta.go internal/stream/delta_test.go
git commit -m "Add text delta buffer"
```

---

### Task 4: Sandbox interfaces and the no-sandbox provider

Port of `SandboxProvider.ts` (the no-sandbox parts) and `sandboxes/no-sandbox.ts`. Bind-mount and isolated interfaces come in the phase 3 and 4 plans.

**Files:**
- Create: `sandbox/sandbox.go`, `sandbox/nosandbox/nosandbox.go`, `sandbox/nosandbox/nosandbox_test.go`

**Interfaces:**
- Produces, in package `sandbox`:
  ```go
  type ExecResult struct { Stdout, Stderr string; ExitCode int }
  type ExecOptions struct {
      OnLine func(line string) // called for each stdout line while the process runs
      Cwd    string            // defaults to the handle's worktree path
      Sudo   bool              // ignored by no-sandbox
      Stdin  *string           // piped to stdin and closed when non-nil
  }
  type Handle interface {
      WorktreePath() string
      Exec(ctx context.Context, command string, opts ExecOptions) (ExecResult, error)
      Close(ctx context.Context) error
  }
  type Kind string // KindNone = "none", KindBindMount = "bind-mount", KindIsolated = "isolated"
  type Provider interface { Kind() Kind; Name() string; Env() map[string]string }
  type NoSandboxCreateOptions struct { WorktreePath string; Env map[string]string }
  type NoSandboxProvider interface { Provider; Create(ctx context.Context, opts NoSandboxCreateOptions) (Handle, error) }
  type BranchType string // BranchHead = "head", BranchMergeToHead = "merge-to-head", BranchNamed = "branch"
  type BranchStrategy struct { Type BranchType; Branch string; BaseBranch string }
  ```
- Produces, in package `nosandbox`: `New(opts *Options) sandbox.NoSandboxProvider` with `Options{Env map[string]string; MaxOutputTailChars int}`.
- `Exec` returns `*agentgopher.ExecError` only when the process cannot start; a non-zero exit is reported through `ExecResult.ExitCode` with a nil error. Context cancellation kills the process and returns `ctx.Err()`.

Note on imports: `sandbox` must not import the root package (the root imports `sandbox`). The "cannot start" error is therefore `sandbox.StartError{Command, Cause}`; the root package maps it where needed.

- [ ] **Step 1: Write the failing tests**

`sandbox/nosandbox/nosandbox_test.go`:
```go
package nosandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/e11en/agentgopher/sandbox"
)

func newHandle(t *testing.T) sandbox.Handle {
	t.Helper()
	h, err := New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestExecCapturesStdoutStderrAndExitCode(t *testing.T) {
	h := newHandle(t)
	res, err := h.Exec(context.Background(), "echo out; echo err 1>&2; exit 3", sandbox.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "out\n" || res.Stderr != "err\n" || res.ExitCode != 3 {
		t.Fatalf("got %+v", res)
	}
}

func TestExecStreamsLinesWhileRunning(t *testing.T) {
	h := newHandle(t)
	var seen []time.Time
	start := time.Now()
	_, err := h.Exec(context.Background(), "echo one; sleep 0.3; echo two", sandbox.ExecOptions{
		OnLine: func(string) { seen = append(seen, time.Now()) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("got %d lines", len(seen))
	}
	if seen[0].Sub(start) > 200*time.Millisecond {
		t.Fatal("first line arrived only after the process finished; OnLine must stream")
	}
}

func TestExecPipesStdin(t *testing.T) {
	h := newHandle(t)
	in := "from stdin"
	res, err := h.Exec(context.Background(), "cat", sandbox.ExecOptions{Stdin: &in})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "from stdin" {
		t.Fatalf("Stdout = %q", res.Stdout)
	}
}

func TestExecUsesCwdAndEnv(t *testing.T) {
	dir := t.TempDir()
	p := New(&Options{Env: map[string]string{"GREETING": "hi"}})
	h, err := p.Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: dir, Env: map[string]string{"GREETING": "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.Exec(context.Background(), "pwd; echo $GREETING", sandbox.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != dir+"\nhi\n" {
		t.Fatalf("Stdout = %q", res.Stdout)
	}
}

func TestExecBoundsStreamedTail(t *testing.T) {
	p := New(&Options{MaxOutputTailChars: 10})
	h, _ := p.Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: t.TempDir()})
	res, err := h.Exec(context.Background(), "for i in 1 2 3 4 5 6 7 8 9; do echo line$i; done", sandbox.ExecOptions{OnLine: func(string) {}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stdout) > 10 {
		t.Fatalf("tail not bounded: %q", res.Stdout)
	}
}

func TestExecCancelledContextKillsProcess(t *testing.T) {
	h := newHandle(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := h.Exec(ctx, "sleep 5", sandbox.ExecOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
}

func TestProviderMetadata(t *testing.T) {
	p := New(nil)
	if p.Kind() != sandbox.KindNone || p.Name() != "no-sandbox" {
		t.Fatalf("kind=%q name=%q", p.Kind(), p.Name())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./sandbox/...`
Expected: FAIL, package `sandbox` not found.

- [ ] **Step 3: Write sandbox/sandbox.go**

```go
// Package sandbox defines the pluggable execution environment for an agent.
package sandbox

import (
	"context"
	"fmt"
)

// ExecResult is the outcome of a command run inside a sandbox.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// ExecOptions configures a single Exec call.
type ExecOptions struct {
	// OnLine receives each stdout line while the process runs. Implementations
	// must stream; a buffered implementation breaks idle timeouts.
	OnLine func(line string)
	// Cwd defaults to the handle's worktree path.
	Cwd string
	// Sudo runs the command as root where the provider supports it.
	Sudo bool
	// Stdin, when non-nil, is piped to the process and closed.
	Stdin *string
}

// Handle is a running sandbox.
type Handle interface {
	WorktreePath() string
	Exec(ctx context.Context, command string, opts ExecOptions) (ExecResult, error)
	Close(ctx context.Context) error
}

// Kind tells the orchestrator how a provider relates to the host filesystem.
type Kind string

const (
	KindNone      Kind = "none"
	KindBindMount Kind = "bind-mount"
	KindIsolated  Kind = "isolated"
)

// Provider is the common part of every sandbox provider.
type Provider interface {
	Kind() Kind
	Name() string
	Env() map[string]string
}

// NoSandboxCreateOptions are passed to a NoSandboxProvider.
type NoSandboxCreateOptions struct {
	WorktreePath string
	Env          map[string]string
}

// NoSandboxProvider runs the agent directly on the host.
type NoSandboxProvider interface {
	Provider
	Create(ctx context.Context, opts NoSandboxCreateOptions) (Handle, error)
}

// BranchType selects how the agent's commits relate to branches.
type BranchType string

const (
	BranchHead        BranchType = "head"
	BranchMergeToHead BranchType = "merge-to-head"
	BranchNamed       BranchType = "branch"
)

// BranchStrategy configures branching for a run. Branch and BaseBranch are
// used by BranchNamed only.
type BranchStrategy struct {
	Type       BranchType
	Branch     string
	BaseBranch string
}

// StartError reports a process that could not be started.
type StartError struct {
	Command string
	Cause   error
}

func (e *StartError) Error() string { return fmt.Sprintf("exec failed: %s: %v", e.Command, e.Cause) }
func (e *StartError) Unwrap() error { return e.Cause }
```

- [ ] **Step 4: Write sandbox/nosandbox/nosandbox.go**

```go
// Package nosandbox runs the agent directly on the host with no isolation.
package nosandbox

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/sandbox"
)

// Options configures the provider.
type Options struct {
	// Env is injected into every process. Merged at launch time.
	Env map[string]string
	// MaxOutputTailChars bounds the stdout and stderr returned from a streamed
	// Exec. Default: stream.MaxTailChars.
	MaxOutputTailChars int
}

type provider struct{ opts Options }

// New returns a provider that runs commands on the host through `sh -c`.
func New(opts *Options) sandbox.NoSandboxProvider {
	p := &provider{}
	if opts != nil {
		p.opts = *opts
	}
	if p.opts.MaxOutputTailChars == 0 {
		p.opts.MaxOutputTailChars = stream.MaxTailChars
	}
	return p
}

func (p *provider) Kind() sandbox.Kind      { return sandbox.KindNone }
func (p *provider) Name() string            { return "no-sandbox" }
func (p *provider) Env() map[string]string  { return p.opts.Env }

func (p *provider) Create(_ context.Context, opts sandbox.NoSandboxCreateOptions) (sandbox.Handle, error) {
	env := os.Environ()
	for k, v := range opts.Env {
		env = append(env, k+"="+v)
	}
	return &handle{worktree: opts.WorktreePath, env: env, maxTail: p.opts.MaxOutputTailChars}, nil
}

type handle struct {
	worktree string
	env      []string
	maxTail  int
}

func (h *handle) WorktreePath() string { return h.worktree }

func (h *handle) Close(context.Context) error { return nil }

func (h *handle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = opts.Cwd
	if cmd.Dir == "" {
		cmd.Dir = h.worktree
	}
	cmd.Env = h.env
	if opts.Stdin != nil {
		cmd.Stdin = strings.NewReader(*opts.Stdin)
	}

	// A child that inherits stdout (a hanging gh or MCP server) must not keep
	// Exec blocked after sh exits or is killed; see ADR 0019.
	cmd.WaitDelay = 2 * time.Second

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if opts.OnLine == nil {
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		err := cmd.Run()
		return h.result(ctx, stdout.String(), stderr.String(), cmd, err)
	}

	tail := stream.NewBoundedTail(h.maxTail, "\n")
	lw := &lineWriter{onLine: func(line string) {
		tail.Push(line)
		opts.OnLine(line)
	}}
	cmd.Stdout = lw
	err := cmd.Run()
	lw.flush()
	errTail := stream.NewBoundedTail(h.maxTail, "")
	errTail.Push(stderr.String())
	return h.result(ctx, tail.String(), errTail.String(), cmd, err)
}

// lineWriter splits a byte stream into lines and hands each complete line to
// onLine as soon as it arrives.
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

func (h *handle) result(ctx context.Context, stdout, stderr string, cmd *exec.Cmd, err error) (sandbox.ExecResult, error) {
	if ctx.Err() != nil {
		return sandbox.ExecResult{}, ctx.Err()
	}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
	default:
		return sandbox.ExecResult{}, &sandbox.StartError{Command: strings.Join(cmd.Args[2:], " "), Cause: err}
	}
	return sandbox.ExecResult{Stdout: stdout, Stderr: stderr, ExitCode: cmd.ProcessState.ExitCode()}, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./sandbox/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add sandbox/
git commit -m "Add sandbox interfaces and no-sandbox provider"
```

---

### Task 5: Prompt resolution and argument substitution

Port of `PromptResolver.ts` and `PromptArgumentSubstitution.ts`.

**Files:**
- Create: `internal/prompt/resolve.go`, `internal/prompt/args.go`, `internal/prompt/prompt_test.go`

**Interfaces:**
- Produces:
  ```go
  type Source string // SourceInline = "inline", SourceTemplate = "template"
  type Resolved struct { Text string; Source Source }
  func Resolve(inline, file string) (Resolved, error)            // exactly one must be set

  type Args map[string]string
  var BuiltInKeys = []string{"SOURCE_BRANCH", "TARGET_BRANCH"}
  func ValidateNoArgsWithInlinePrompt(args Args) error
  func ValidateNoBuiltInOverride(args Args) error
  func FindMissingKeys(prompt string, args Args) []string
  // Substitute replaces {{KEY}} placeholders. It marks shell blocks that were
  // in the raw template so Expand (Task 8) only runs those, never ones that
  // arrived through an argument value. unusedKeys receives keys provided but
  // not referenced, excluding silentKeys.
  func Substitute(prompt string, args Args, silentKeys map[string]bool, warn func(string)) (string, error)
  const ShellBlockMarker = "\x01"
  ```
- Errors are `*agentgopher.PromptError`. Because `internal/prompt` is imported by the root package, it cannot import the root. Define the error in `internal/prompt` as `prompt.Error{Message string; ExitCode int}` and alias it from the root: in `errors.go` replace the `PromptError` struct with `type PromptError = prompt.Error`. Do the same for `PromptExpansionTimeoutError = prompt.ExpansionTimeoutError` in Task 8. Update `errors.go` and its test accordingly in this task.

- [ ] **Step 1: Write the failing tests**

`internal/prompt/prompt_test.go`:
```go
package prompt

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveInline(t *testing.T) {
	r, err := Resolve("do it", "")
	if err != nil || r.Text != "do it" || r.Source != SourceInline {
		t.Fatalf("got %+v, %v", r, err)
	}
}

func TestResolveFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "prompt.md")
	os.WriteFile(p, []byte("from file"), 0o644)
	r, err := Resolve("", p)
	if err != nil || r.Text != "from file" || r.Source != SourceTemplate {
		t.Fatalf("got %+v, %v", r, err)
	}
}

func TestResolveRejectsBothAndNeither(t *testing.T) {
	var perr *Error
	if _, err := Resolve("a", "b"); !errors.As(err, &perr) {
		t.Fatalf("both: err = %v", err)
	}
	if _, err := Resolve("", ""); !errors.As(err, &perr) {
		t.Fatalf("neither: err = %v", err)
	}
}

func TestResolveMissingFile(t *testing.T) {
	var perr *Error
	_, err := Resolve("", "/nonexistent/prompt.md")
	if !errors.As(err, &perr) || !strings.Contains(perr.Message, "/nonexistent/prompt.md") {
		t.Fatalf("err = %v", err)
	}
}

func TestSubstituteReplacesPlaceholders(t *testing.T) {
	out, err := Substitute("Work on {{ BRANCH }} for {{OWNER}}", Args{"BRANCH": "main", "OWNER": "ellen"}, nil, nil)
	if err != nil || out != "Work on main for ellen" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestSubstituteFailsOnMissingKey(t *testing.T) {
	_, err := Substitute("{{MISSING}}", Args{}, nil, nil)
	var perr *Error
	if !errors.As(err, &perr) || !strings.Contains(perr.Message, "{{MISSING}}") {
		t.Fatalf("err = %v", err)
	}
}

func TestSubstituteWarnsOnUnusedKeyExceptSilent(t *testing.T) {
	var warnings []string
	_, err := Substitute("no placeholders", Args{"UNUSED": "x", "SOURCE_BRANCH": "y"},
		map[string]bool{"SOURCE_BRANCH": true}, func(m string) { warnings = append(warnings, m) })
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "UNUSED") {
		t.Fatalf("warnings = %q", warnings)
	}
}

func TestSubstituteMarksTemplateShellBlocksOnly(t *testing.T) {
	out, err := Substitute("!`ls` and {{CMD}}", Args{"CMD": "!`rm -rf /`"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "!"+ShellBlockMarker+"`ls` and !`rm -rf /`" {
		t.Fatalf("got %q", out)
	}
}

func TestSubstituteStripsForgedMarkers(t *testing.T) {
	out, err := Substitute("!"+ShellBlockMarker+"`forged`", Args{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The forged marker is stripped, then the block is marked again as a
	// genuine template block, which is the same result as a clean template.
	if strings.Count(out, ShellBlockMarker) != 1 {
		t.Fatalf("got %q", out)
	}
}

func TestSubstituteHandlesCRLF(t *testing.T) {
	out, err := Substitute("line1\r\n{{A}}\r\n", Args{"A": "x"}, nil, nil)
	if err != nil || strings.Contains(out, "\r") || out != "line1\nx\n" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestFindMissingKeys(t *testing.T) {
	got := FindMissingKeys("{{A}} {{B}} {{A}} {{SOURCE_BRANCH}}", Args{"A": "1"})
	if len(got) != 1 || got[0] != "B" {
		t.Fatalf("got %v", got)
	}
}

func TestValidateBuiltIns(t *testing.T) {
	if err := ValidateNoBuiltInOverride(Args{"TARGET_BRANCH": "x"}); err == nil {
		t.Fatal("expected error")
	}
	if err := ValidateNoArgsWithInlinePrompt(Args{"A": "x"}); err == nil {
		t.Fatal("expected error")
	}
	if err := ValidateNoArgsWithInlinePrompt(Args{}); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/prompt/`
Expected: FAIL, "undefined: Resolve".

- [ ] **Step 3: Write resolve.go**

```go
// Package prompt resolves, substitutes and expands agent prompts.
package prompt

import (
	"fmt"
	"os"
	"strings"
)

// Error reports a prompt that could not be resolved, substituted or expanded.
// ExitCode is set when a shell expression exited non-zero, otherwise 0.
type Error struct {
	Message  string
	ExitCode int
}

func (e *Error) Error() string { return e.Message }

// Source says where a prompt came from.
type Source string

const (
	// SourceInline prompts are delivered to the agent verbatim.
	SourceInline Source = "inline"
	// SourceTemplate prompts get placeholder substitution and shell expansion.
	SourceTemplate Source = "template"
)

// Resolved is a prompt text plus its source.
type Resolved struct {
	Text   string
	Source Source
}

// Resolve returns the inline prompt, or reads file. Exactly one must be set.
// Line endings are normalised to LF.
func Resolve(inline, file string) (Resolved, error) {
	switch {
	case inline != "" && file != "":
		return Resolved{}, &Error{Message: "cannot provide both Prompt and PromptFile"}
	case inline != "":
		return Resolved{Text: inline, Source: SourceInline}, nil
	case file == "":
		return Resolved{}, &Error{Message: "must provide either Prompt or PromptFile; pass Prompt: \"...\" or PromptFile: \"./.agentgopher/prompt.md\" to Run"}
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return Resolved{}, &Error{Message: fmt.Sprintf("failed to read prompt from %s: %v", file, err)}
	}
	return Resolved{Text: strings.ReplaceAll(string(b), "\r\n", "\n"), Source: SourceTemplate}, nil
}
```

- [ ] **Step 4: Write args.go**

```go
package prompt

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ShellBlockMarker sits between `!` and the backtick of shell blocks that
// were present in the raw template, so Expand only runs those.
const ShellBlockMarker = "\x01"

var (
	placeholderPattern = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)
	shellBlockPattern  = regexp.MustCompile("!`([^`]+)`")
)

// Args maps {{KEY}} placeholders to values.
type Args map[string]string

// BuiltInKeys are injected by the orchestrator and cannot be overridden.
var BuiltInKeys = []string{"SOURCE_BRANCH", "TARGET_BRANCH"}

// ValidateNoArgsWithInlinePrompt fails when args are passed with an inline
// prompt, which would silently ignore them.
func ValidateNoArgsWithInlinePrompt(args Args) error {
	if len(args) == 0 {
		return nil
	}
	return &Error{Message: "PromptArgs is only supported with PromptFile. Inline prompts are passed to the agent as-is; interpolate values in Go, or switch to PromptFile to use {{KEY}} substitution."}
}

// ValidateNoBuiltInOverride fails when args contains a built-in key.
func ValidateNoBuiltInOverride(args Args) error {
	for _, k := range BuiltInKeys {
		if _, ok := args[k]; ok {
			return &Error{Message: fmt.Sprintf("%q is a built-in prompt argument and cannot be overridden via PromptArgs", k)}
		}
	}
	return nil
}

// FindMissingKeys returns placeholder keys that are not in args, excluding
// built-ins, in order of first appearance.
func FindMissingKeys(prompt string, args Args) []string {
	seen := map[string]bool{}
	for _, k := range BuiltInKeys {
		seen[k] = true
	}
	var missing []string
	for _, m := range placeholderPattern.FindAllStringSubmatch(prompt, -1) {
		key := m[1]
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, ok := args[key]; !ok {
			missing = append(missing, key)
		}
	}
	return missing
}

// Substitute replaces {{KEY}} placeholders with args. Shell blocks written in
// the template are marked with ShellBlockMarker; markers in the input or in
// argument values are stripped so they cannot be forged. warn is called for
// every provided key that the prompt does not reference, except silentKeys.
func Substitute(prompt string, args Args, silentKeys map[string]bool, warn func(string)) (string, error) {
	prompt = strings.ReplaceAll(prompt, "\r\n", "\n")
	marked := strings.ReplaceAll(prompt, ShellBlockMarker, "")
	marked = shellBlockPattern.ReplaceAllString(marked, "!"+ShellBlockMarker+"`$1`")

	clean := make(Args, len(args))
	for k, v := range args {
		clean[k] = strings.ReplaceAll(v, ShellBlockMarker, "")
	}

	referenced := map[string]bool{}
	for _, m := range placeholderPattern.FindAllStringSubmatch(marked, -1) {
		referenced[m[1]] = true
	}
	for key := range referenced {
		if _, ok := clean[key]; !ok {
			return "", &Error{Message: fmt.Sprintf("prompt argument \"{{%s}}\" has no matching value in PromptArgs", key)}
		}
	}

	if warn != nil {
		unused := make([]string, 0)
		for k := range clean {
			if !referenced[k] && !silentKeys[k] {
				unused = append(unused, k)
			}
		}
		sort.Strings(unused)
		for _, k := range unused {
			warn(fmt.Sprintf("prompt argument %q was provided but not referenced in the prompt", k))
		}
	}

	return placeholderPattern.ReplaceAllStringFunc(marked, func(m string) string {
		return clean[placeholderPattern.FindStringSubmatch(m)[1]]
	}), nil
}
```

- [ ] **Step 5: Alias the error from the root package**

In `errors.go` replace the `PromptError` struct and its `Error` method with:
```go
// PromptError reports a prompt that could not be resolved, substituted or expanded.
type PromptError = prompt.Error
```
and add `"github.com/e11en/agentgopher/internal/prompt"` to the imports.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./...`
Expected: PASS for all packages.

- [ ] **Step 7: Commit**

```bash
git add internal/prompt/ errors.go
git commit -m "Add prompt resolution and argument substitution"
```

---

### Task 6: Agent provider interface and Claude Code provider

Port of the `AgentProvider` interface, `parseStreamJsonLine` and `claudeCode()` from `AgentProvider.ts`. Session storage, interactive args and the other five agents are deferred to the phase 5 plan.

**Files:**
- Create: `agent/agent.go`, `agent/claudecode.go`, `agent/claudecode_test.go`

**Interfaces:**
- Produces, in package `agent`:
  ```go
  type CommandOptions struct { Prompt string; DangerouslySkipPermissions bool; ResumeSession string; ForkSession bool }
  type PrintCommand struct { Command string; Stdin *string }
  type Usage struct { InputTokens, CacheCreationInputTokens, CacheReadInputTokens, OutputTokens int }
  type EventType string // EventText, EventResult, EventToolCall, EventSessionID, EventUsage
  type StreamEvent struct { Type EventType; Text string; Name string; Args string; SessionID string; Usage *Usage }
  type Provider interface {
      Name() string
      Env() map[string]string
      CaptureSessions() bool
      BuildPrintCommand(opts CommandOptions) PrintCommand
      ParseStreamLine(line string) []StreamEvent
  }
  type SessionUsageParser interface { ParseSessionUsage(content string) (Usage, bool) } // optional
  const DefaultModel = "claude-opus-4-8"
  type ClaudeCodeOptions struct { Env map[string]string; CaptureSessions *bool; PermissionMode string; Effort string }
  func ClaudeCode(model string, opts *ClaudeCodeOptions) Provider
  func ShellEscape(s string) string
  ```
- `EventText` carries `Text`; `EventResult` carries `Text` too (the final result string); `EventToolCall` carries `Name` and `Args`.

- [ ] **Step 1: Write the failing tests**

`agent/claudecode_test.go`:
```go
package agent

import (
	"strings"
	"testing"
)

func TestClaudeCodePrintCommandDefaults(t *testing.T) {
	p := ClaudeCode("claude-opus-4-8", nil)
	pc := p.BuildPrintCommand(CommandOptions{Prompt: "hello", DangerouslySkipPermissions: true})
	want := "claude --print --verbose --dangerously-skip-permissions --output-format stream-json --model 'claude-opus-4-8' -p -"
	if pc.Command != want {
		t.Fatalf("Command = %q\nwant      %q", pc.Command, want)
	}
	if pc.Stdin == nil || *pc.Stdin != "hello" {
		t.Fatal("prompt must be delivered over stdin")
	}
	if p.Name() != "claude-code" || !p.CaptureSessions() {
		t.Fatalf("name=%q capture=%v", p.Name(), p.CaptureSessions())
	}
}

func TestClaudeCodePrintCommandOptions(t *testing.T) {
	p := ClaudeCode("m", &ClaudeCodeOptions{PermissionMode: "plan", Effort: "high"})
	pc := p.BuildPrintCommand(CommandOptions{Prompt: "x", DangerouslySkipPermissions: true, ResumeSession: "abc'd", ForkSession: true})
	want := "claude --print --verbose --permission-mode plan --output-format stream-json --model 'm' --effort high --resume 'abc'\\''d' --fork-session -p -"
	if pc.Command != want {
		t.Fatalf("Command = %q\nwant      %q", pc.Command, want)
	}
}

func TestClaudeCodeForkWithoutResumeIsIgnored(t *testing.T) {
	pc := ClaudeCode("m", nil).BuildPrintCommand(CommandOptions{Prompt: "x", ForkSession: true})
	if strings.Contains(pc.Command, "--fork-session") || strings.Contains(pc.Command, "--dangerously") {
		t.Fatalf("Command = %q", pc.Command)
	}
}

func TestParseStreamLineAssistantTextAndToolCalls(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"Let me "},{"type":"text","text":"look."},{"type":"tool_use","name":"Bash","input":{"command":"ls -la"}},{"type":"tool_use","name":"Read","input":{"file_path":"a.go"}},{"type":"text","text":"done"}]}}`
	events := ClaudeCode("m", nil).ParseStreamLine(line)
	want := []StreamEvent{
		{Type: EventText, Text: "Let me look."},
		{Type: EventToolCall, Name: "Bash", Args: "ls -la"},
		{Type: EventText, Text: "done"},
	}
	if len(events) != len(want) {
		t.Fatalf("got %+v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v", i, events[i], want[i])
		}
	}
}

func TestParseStreamLineResultInitAndJunk(t *testing.T) {
	p := ClaudeCode("m", nil)
	if ev := p.ParseStreamLine(`{"type":"result","result":"all good"}`); len(ev) != 1 || ev[0].Type != EventResult || ev[0].Text != "all good" {
		t.Fatalf("result: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"system","subtype":"init","session_id":"s-1"}`); len(ev) != 1 || ev[0].Type != EventSessionID || ev[0].SessionID != "s-1" {
		t.Fatalf("init: %+v", ev)
	}
	if ev := p.ParseStreamLine("not json"); len(ev) != 0 {
		t.Fatalf("junk: %+v", ev)
	}
	if ev := p.ParseStreamLine(`{"type":"assistant","message":{"content":"string"}}`); len(ev) != 0 {
		t.Fatalf("non-array content: %+v", ev)
	}
}

func TestParseSessionUsageTakesLastAssistantMessage(t *testing.T) {
	content := `{"type":"assistant","message":{"usage":{"input_tokens":1,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"output_tokens":4}}}
{"type":"user"}
{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":20,"cache_read_input_tokens":30,"output_tokens":40}}}
`
	u, ok := ClaudeCode("m", nil).(SessionUsageParser).ParseSessionUsage(content)
	if !ok || u != (Usage{10, 20, 30, 40}) {
		t.Fatalf("got %+v ok=%v", u, ok)
	}
	if _, ok := ClaudeCode("m", nil).(SessionUsageParser).ParseSessionUsage("{}\n"); ok {
		t.Fatal("expected no usage")
	}
}

func TestShellEscape(t *testing.T) {
	if got := ShellEscape("it's"); got != `'it'\''s'` {
		t.Fatalf("got %s", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./agent/`
Expected: FAIL, "undefined: ClaudeCode".

- [ ] **Step 3: Write agent/agent.go**

```go
// Package agent defines the pluggable AI coding agent invoked inside a sandbox.
package agent

import "strings"

// CommandOptions are passed to BuildPrintCommand.
type CommandOptions struct {
	Prompt                     string
	DangerouslySkipPermissions bool
	// ResumeSession, when set, resumes that session instead of starting fresh.
	ResumeSession string
	// ForkSession, with ResumeSession, writes the continuation as a new session.
	ForkSession bool
}

// PrintCommand is the shell command for one non-interactive agent run. When
// Stdin is set the sandbox pipes it to the process, which avoids the Linux
// per-argument size limit for large prompts.
type PrintCommand struct {
	Command string
	Stdin   *string
}

// Usage is a token usage snapshot for one iteration.
type Usage struct {
	InputTokens              int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	OutputTokens             int
}

// EventType classifies a StreamEvent.
type EventType string

const (
	EventText      EventType = "text"
	EventResult    EventType = "result"
	EventToolCall  EventType = "tool_call"
	EventSessionID EventType = "session_id"
	EventUsage     EventType = "usage"
)

// StreamEvent is one parsed item from the agent's stdout. Text is set for
// EventText and EventResult, Name and Args for EventToolCall, SessionID for
// EventSessionID, Usage for EventUsage.
type StreamEvent struct {
	Type      EventType
	Text      string
	Name      string
	Args      string
	SessionID string
	Usage     *Usage
}

// Provider builds the command for an agent CLI and parses its output.
type Provider interface {
	Name() string
	// Env is injected into the sandbox. Merged at launch time.
	Env() map[string]string
	// CaptureSessions enables copying the agent session back to the host.
	CaptureSessions() bool
	BuildPrintCommand(opts CommandOptions) PrintCommand
	ParseStreamLine(line string) []StreamEvent
}

// SessionUsageParser is implemented by providers that can read token usage
// from a captured session file.
type SessionUsageParser interface {
	ParseSessionUsage(content string) (Usage, bool)
}

// ShellEscape single-quotes s for a POSIX shell.
func ShellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
```

- [ ] **Step 4: Write agent/claudecode.go**

```go
package agent

import (
	"encoding/json"
	"strings"
)

// DefaultModel is used by examples and init templates.
const DefaultModel = "claude-opus-4-8"

// ClaudeCodeOptions configures the Claude Code provider.
type ClaudeCodeOptions struct {
	Env map[string]string
	// CaptureSessions defaults to true.
	CaptureSessions *bool
	// PermissionMode replaces --dangerously-skip-permissions when set.
	PermissionMode string
	Effort         string
}

// toolArgFields maps allowlisted tool names to the input field shown as the
// tool call argument.
var toolArgFields = map[string]string{
	"Bash":      "command",
	"WebSearch": "query",
	"WebFetch":  "url",
	"Agent":     "description",
}

type claudeCode struct {
	model string
	opts  ClaudeCodeOptions
}

// ClaudeCode returns a provider for the `claude` CLI.
func ClaudeCode(model string, opts *ClaudeCodeOptions) Provider {
	p := &claudeCode{model: model}
	if opts != nil {
		p.opts = *opts
	}
	return p
}

func (p *claudeCode) Name() string           { return "claude-code" }
func (p *claudeCode) Env() map[string]string { return p.opts.Env }
func (p *claudeCode) CaptureSessions() bool {
	return p.opts.CaptureSessions == nil || *p.opts.CaptureSessions
}

func (p *claudeCode) BuildPrintCommand(o CommandOptions) PrintCommand {
	var b strings.Builder
	b.WriteString("claude --print --verbose")
	switch {
	case p.opts.PermissionMode != "":
		b.WriteString(" --permission-mode " + p.opts.PermissionMode)
	case o.DangerouslySkipPermissions:
		b.WriteString(" --dangerously-skip-permissions")
	}
	b.WriteString(" --output-format stream-json --model " + ShellEscape(p.model))
	if p.opts.Effort != "" {
		b.WriteString(" --effort " + p.opts.Effort)
	}
	if o.ResumeSession != "" {
		b.WriteString(" --resume " + ShellEscape(o.ResumeSession))
		if o.ForkSession {
			b.WriteString(" --fork-session")
		}
	}
	b.WriteString(" -p -")
	prompt := o.Prompt
	return PrintCommand{Command: b.String(), Stdin: &prompt}
}

func (p *claudeCode) ParseStreamLine(line string) []StreamEvent {
	return ParseStreamJSONLine(line)
}

func (p *claudeCode) ParseSessionUsage(content string) (Usage, bool) {
	lines := strings.Split(content, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.HasPrefix(lines[i], "{") {
			continue
		}
		var obj struct {
			Type    string `json:"type"`
			Message struct {
				Usage *struct {
					Input         *int `json:"input_tokens"`
					CacheCreation *int `json:"cache_creation_input_tokens"`
					CacheRead     *int `json:"cache_read_input_tokens"`
					Output        *int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(lines[i]), &obj) != nil || obj.Type != "assistant" || obj.Message.Usage == nil {
			continue
		}
		u := obj.Message.Usage
		if u.Input == nil || u.CacheCreation == nil || u.CacheRead == nil || u.Output == nil {
			continue
		}
		return Usage{*u.Input, *u.CacheCreation, *u.CacheRead, *u.Output}, true
	}
	return Usage{}, false
}

// ParseStreamJSONLine parses one line of Claude Code's stream-json output.
// Shared with the Cursor provider in a later phase.
func ParseStreamJSONLine(line string) []StreamEvent {
	if !strings.HasPrefix(line, "{") {
		return nil
	}
	var obj struct {
		Type      string          `json:"type"`
		Subtype   string          `json:"subtype"`
		Result    *string         `json:"result"`
		SessionID *string         `json:"session_id"`
		Message   json.RawMessage `json:"message"`
	}
	if json.Unmarshal([]byte(line), &obj) != nil {
		return nil
	}
	switch {
	case obj.Type == "assistant":
		var msg struct {
			Content []struct {
				Type  string                     `json:"type"`
				Text  *string                    `json:"text"`
				Name  *string                    `json:"name"`
				Input map[string]json.RawMessage `json:"input"`
			} `json:"content"`
		}
		if json.Unmarshal(obj.Message, &msg) != nil || msg.Content == nil {
			return nil
		}
		var events []StreamEvent
		var texts strings.Builder
		flush := func() {
			if texts.Len() > 0 {
				events = append(events, StreamEvent{Type: EventText, Text: texts.String()})
				texts.Reset()
			}
		}
		for _, block := range msg.Content {
			switch {
			case block.Type == "text" && block.Text != nil:
				texts.WriteString(*block.Text)
			case block.Type == "tool_use" && block.Name != nil && block.Input != nil:
				field, ok := toolArgFields[*block.Name]
				if !ok {
					continue
				}
				var arg string
				if json.Unmarshal(block.Input[field], &arg) != nil {
					continue
				}
				flush()
				events = append(events, StreamEvent{Type: EventToolCall, Name: *block.Name, Args: arg})
			}
		}
		flush()
		return events
	case obj.Type == "result" && obj.Result != nil:
		return []StreamEvent{{Type: EventResult, Text: *obj.Result}}
	case obj.Type == "system" && obj.Subtype == "init" && obj.SessionID != nil:
		return []StreamEvent{{Type: EventSessionID, SessionID: *obj.SessionID}}
	}
	return nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./agent/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add agent/
git commit -m "Add agent provider interface and Claude Code provider"
```

---

### Task 7: Env resolution

Port of `EnvResolver.ts` and `mergeProviderEnv.ts`. Reads `.agentgopher/.env`; only keys declared there are resolved, with the process env as fallback for empty values.

**Files:**
- Create: `internal/env/env.go`, `internal/env/env_test.go`

**Interfaces:**
- Produces: `env.ParseFile(path string) (map[string]string, error)` (missing file returns empty map, nil), `env.Resolve(repoDir string) (map[string]string, error)`, `env.MergeProviderEnv(resolved, agentEnv, sandboxEnv map[string]string) (map[string]string, error)` (error when agentEnv and sandboxEnv share a key).

- [ ] **Step 1: Write the failing tests**

`internal/env/env_test.go`:
```go
package env

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFileHandlesCommentsQuotesAndEscapes(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(p, []byte("# comment\n\nA=1\nB=\"two\\nlines\"\nC='raw\\n'\nnoequals\nD = spaced \n"), 0o644)
	got, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "1", "B": "two\nlines", "C": "raw\\n", "D": "spaced"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestParseFileMissingIsEmpty(t *testing.T) {
	got, err := ParseFile("/nonexistent/.env")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestResolveOnlyDeclaredKeysWithProcessFallback(t *testing.T) {
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, ".agentgopher"), 0o755)
	os.WriteFile(filepath.Join(repo, ".agentgopher", ".env"), []byte("TOKEN=\nFIXED=yes\nABSENT=\n"), 0o644)
	t.Setenv("TOKEN", "from-process")
	t.Setenv("UNDECLARED", "ignored")
	os.Unsetenv("ABSENT")
	got, err := Resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got["TOKEN"] != "from-process" || got["FIXED"] != "yes" {
		t.Fatalf("got %v", got)
	}
	if _, ok := got["UNDECLARED"]; ok {
		t.Fatal("undeclared key leaked")
	}
	if _, ok := got["ABSENT"]; ok {
		t.Fatal("empty key with no fallback must be dropped")
	}
}

func TestMergeProviderEnvPrecedenceAndOverlap(t *testing.T) {
	got, err := MergeProviderEnv(map[string]string{"A": "env", "B": "env"}, map[string]string{"A": "agent"}, map[string]string{"B": "sandbox"})
	if err != nil || got["A"] != "agent" || got["B"] != "sandbox" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := MergeProviderEnv(nil, map[string]string{"X": "1"}, map[string]string{"X": "2"}); err == nil {
		t.Fatal("expected overlap error")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/env/`
Expected: FAIL, "undefined: ParseFile".

- [ ] **Step 3: Write env.go**

```go
// Package env resolves the environment injected into the sandbox.
package env

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ParseFile reads a dotenv file. A missing file yields an empty map.
func ParseFile(path string) (map[string]string, error) {
	vars := map[string]string{}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return vars, nil
	}
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch {
		case len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"':
			value = unescape(value[1 : len(value)-1])
		case len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'':
			value = value[1 : len(value)-1]
		}
		vars[key] = value
	}
	return vars, nil
}

func unescape(s string) string {
	r := strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t", `\\`, `\`)
	return r.Replace(s)
}

// Resolve reads <repoDir>/.agentgopher/.env. Only keys declared there are
// returned; an empty value falls back to the process environment, and a key
// with no value anywhere is dropped.
func Resolve(repoDir string) (map[string]string, error) {
	declared, err := ParseFile(filepath.Join(repoDir, ".agentgopher", ".env"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for k, v := range declared {
		if v == "" {
			v = os.Getenv(k)
		}
		if v != "" {
			out[k] = v
		}
	}
	return out, nil
}

// MergeProviderEnv layers provider env over the resolved env. Agent and
// sandbox provider env must not share keys.
func MergeProviderEnv(resolved, agentEnv, sandboxEnv map[string]string) (map[string]string, error) {
	var overlap []string
	for k := range agentEnv {
		if _, ok := sandboxEnv[k]; ok {
			overlap = append(overlap, k)
		}
	}
	if len(overlap) > 0 {
		sort.Strings(overlap)
		return nil, fmt.Errorf("overlapping env keys between agent provider and sandbox provider: %s", strings.Join(overlap, ", "))
	}
	out := map[string]string{}
	for _, m := range []map[string]string{resolved, sandboxEnv, agentEnv} {
		for k, v := range m {
			out[k] = v
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/env/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/env/
git commit -m "Add env file resolution and provider env merge"
```

---

### Task 8: Shell expression expansion

Port of `PromptPreprocessor.ts`. Runs every marked `` !`command` `` in the sandbox in parallel with a 30 s timeout each, and replaces it with trimmed stdout.

**Files:**
- Create: `internal/prompt/expand.go`
- Modify: `internal/prompt/prompt_test.go` (append tests), `errors.go` (alias)

**Interfaces:**
- Consumes: `sandbox.Handle.Exec` (Task 4).
- Produces:
  ```go
  type Executor interface { Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) }
  type ExpansionTimeoutError struct { Expression string; Timeout, Elapsed time.Duration }
  // Expand runs marked shell blocks in cwd and strips all markers. log receives
  // one "cmd -> ~N tokens" line per expression.
  func Expand(ctx context.Context, prompt string, exec Executor, cwd string, log func(string)) (string, error)
  const ExpansionTimeout = 30 * time.Second
  ```
- `errors.go`: `type PromptExpansionTimeoutError = prompt.ExpansionTimeoutError`; remove the struct defined in Task 1.

- [ ] **Step 1: Write the failing tests (append to prompt_test.go)**

```go
func TestExpandRunsMarkedBlocksAndStripsMarkers(t *testing.T) {
	h := newHostHandle(t)
	in := "A: !" + ShellBlockMarker + "`echo one`\nB: !`echo untouched`\nC: !" + ShellBlockMarker + "`printf ''`."
	var logs []string
	out, err := Expand(context.Background(), in, h, h.WorktreePath(), func(m string) { logs = append(logs, m) })
	if err != nil {
		t.Fatal(err)
	}
	if out != "A: one\nB: !`echo untouched`\nC: ." {
		t.Fatalf("got %q", out)
	}
	if len(logs) != 2 {
		t.Fatalf("logs = %q", logs)
	}
}

func TestExpandNoBlocksStripsStrayMarkers(t *testing.T) {
	out, err := Expand(context.Background(), "plain "+ShellBlockMarker+"text", nil, "", nil)
	if err != nil || out != "plain text" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestExpandFailsOnNonZeroExit(t *testing.T) {
	h := newHostHandle(t)
	_, err := Expand(context.Background(), "!"+ShellBlockMarker+"`echo bad 1>&2; exit 2`", h, h.WorktreePath(), nil)
	var perr *Error
	if !errors.As(err, &perr) || perr.ExitCode != 2 || !strings.Contains(perr.Message, "bad") {
		t.Fatalf("err = %v", err)
	}
}

func TestExpandTimesOut(t *testing.T) {
	h := newHostHandle(t)
	old := expansionTimeout
	expansionTimeout = 100 * time.Millisecond
	defer func() { expansionTimeout = old }()
	_, err := Expand(context.Background(), "!"+ShellBlockMarker+"`sleep 2`", h, h.WorktreePath(), nil)
	var terr *ExpansionTimeoutError
	if !errors.As(err, &terr) || terr.Expression != "sleep 2" {
		t.Fatalf("err = %v", err)
	}
}

func newHostHandle(t *testing.T) sandbox.Handle {
	t.Helper()
	h, err := nosandbox.New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return h
}
```

Add to the test file imports: `"context"`, `"time"`, `"github.com/e11en/agentgopher/sandbox"`, `"github.com/e11en/agentgopher/sandbox/nosandbox"`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/prompt/`
Expected: FAIL, "undefined: Expand".

- [ ] **Step 3: Write expand.go**

```go
package prompt

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/e11en/agentgopher/sandbox"
)

// ExpansionTimeout bounds each shell expression.
const ExpansionTimeout = 30 * time.Second

// expansionTimeout is a variable so tests can shorten it.
var expansionTimeout = ExpansionTimeout

var markedShellBlockPattern = regexp.MustCompile("!" + ShellBlockMarker + "`([^`]+)`")

// Executor runs a command in the sandbox. sandbox.Handle satisfies it.
type Executor interface {
	Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error)
}

// ExpansionTimeoutError reports a shell expression that ran longer than Timeout.
type ExpansionTimeoutError struct {
	Expression string
	Timeout    time.Duration
	Elapsed    time.Duration
}

func (e *ExpansionTimeoutError) Error() string {
	return fmt.Sprintf("shell expression `%s` timed out after %v", e.Expression, e.Elapsed.Round(time.Millisecond))
}

type expansion struct {
	out string
	err error
}

// Expand runs every marked shell block in parallel inside cwd and replaces it
// with the command's stdout (trailing whitespace trimmed). All markers are
// stripped from the result. log, when set, receives one line per expression.
func Expand(ctx context.Context, prompt string, exec Executor, cwd string, log func(string)) (string, error) {
	matches := markedShellBlockPattern.FindAllStringSubmatchIndex(prompt, -1)
	if len(matches) == 0 {
		return strings.ReplaceAll(prompt, ShellBlockMarker, ""), nil
	}

	results := make([]expansion, len(matches))
	done := make(chan struct{})
	for i, m := range matches {
		go func(i int, command string) {
			results[i] = runExpression(ctx, exec, cwd, command)
			done <- struct{}{}
		}(i, prompt[m[2]:m[3]])
	}
	for range matches {
		<-done
	}

	for i, r := range results {
		if r.err != nil {
			return "", r.err
		}
		if log != nil {
			command := prompt[matches[i][2]:matches[i][3]]
			log(fmt.Sprintf("%s -> ~%d tokens", command, (len(r.out)+3)/4))
		}
	}

	var b strings.Builder
	last := 0
	for i, m := range matches {
		b.WriteString(prompt[last:m[0]])
		b.WriteString(results[i].out)
		last = m[1]
	}
	b.WriteString(prompt[last:])
	return strings.ReplaceAll(b.String(), ShellBlockMarker, ""), nil
}

func runExpression(ctx context.Context, exec Executor, cwd, command string) expansion {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, expansionTimeout)
	defer cancel()
	res, err := exec.Exec(ctx, command, sandbox.ExecOptions{Cwd: cwd})
	if ctx.Err() == context.DeadlineExceeded {
		return expansion{err: &ExpansionTimeoutError{Expression: command, Timeout: expansionTimeout, Elapsed: time.Since(start)}}
	}
	if err != nil {
		return expansion{err: &Error{Message: fmt.Sprintf("command `%s` failed: %v", command, err)}}
	}
	if res.ExitCode != 0 {
		return expansion{err: &Error{Message: fmt.Sprintf("command `%s` exited with code %d: %s", command, res.ExitCode, res.Stderr), ExitCode: res.ExitCode}}
	}
	return expansion{out: strings.TrimRight(res.Stdout, " \t\r\n")}
}
```

- [ ] **Step 4: Alias the timeout error in errors.go**

Replace the `PromptExpansionTimeoutError` struct and method with:
```go
// PromptExpansionTimeoutError reports a shell expression that ran longer than its timeout.
type PromptExpansionTimeoutError = prompt.ExpansionTimeoutError
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/prompt/ errors.go
git commit -m "Add shell expression expansion for prompt templates"
```

---

### Task 9: Display

Port of the `Display` service and `FileDisplay` from `Display.ts`. One writer-backed implementation serves both log-to-file mode and plain stdout mode.

**Files:**
- Create: `internal/display/display.go`, `internal/display/display_test.go`

**Interfaces:**
- Produces:
  ```go
  type Severity string // SeverityInfo, SeveritySuccess, SeverityWarn, SeverityError
  type Display interface {
      Intro(title string)
      Status(message string, severity Severity)
      Summary(title string, rows [][2]string)
      Task(title string, fn func(log func(string)) error) error
      Text(line string)
      TextChunk(chunk string)
      ToolCall(name, args string)
  }
  func NewWriter(w io.Writer) Display
  // OpenLogFile creates the parent directory, appends a "--- Run started: <RFC3339> ---" delimiter and returns the file.
  func OpenLogFile(path string) (*os.File, error)
  ```
- `Status` strips a leading `[name] ` label, as upstream's FileDisplay does. `Task` writes `title...` before and `title done (1.2s)` after, plus each log line indented by two spaces. `TextChunk` writes raw; any line-oriented write after a chunk without a trailing newline first writes `\n`.

- [ ] **Step 1: Write the failing tests**

`internal/display/display_test.go`:
```go
package display

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriterDisplayFormatsEntries(t *testing.T) {
	var buf bytes.Buffer
	d := NewWriter(&buf)
	d.Intro("agentgopher")
	d.Summary("agentgopher Run", [][2]string{{"Agent", "claude-code"}, {"Branch", "main"}})
	d.Status("[worker] Iteration 1/3", SeverityInfo)
	err := d.Task("Setting up", func(log func(string)) error { log("git config"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	d.TextChunk("streamed prose")
	d.ToolCall("Bash", "ls")
	d.Text("Context window: 12k")
	got := buf.String()
	for _, want := range []string{
		"agentgopher Run\n  Agent: claude-code\n  Branch: main\n",
		"Iteration 1/3\n",
		"Setting up...\n  git config\nSetting up done (",
		"streamed prose\n[Bash] ls\n",
		"Context window: 12k\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[worker]") {
		t.Fatal("label must be stripped")
	}
}

func TestTaskPropagatesError(t *testing.T) {
	d := NewWriter(&bytes.Buffer{})
	want := errors.New("boom")
	if err := d.Task("x", func(func(string)) error { return want }); !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenLogFileCreatesDirAndDelimiter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "run.log")
	f, err := OpenLogFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "\n--- Run started: ") {
		t.Fatalf("got %q", b)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/display/`
Expected: FAIL, "undefined: NewWriter".

- [ ] **Step 3: Write display.go**

```go
// Package display renders run progress to a log file or the terminal.
package display

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Severity classifies a status line.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeveritySuccess Severity = "success"
	SeverityWarn    Severity = "warn"
	SeverityError   Severity = "error"
)

// Display receives progress from the orchestrator.
type Display interface {
	Intro(title string)
	Status(message string, severity Severity)
	Summary(title string, rows [][2]string)
	Task(title string, fn func(log func(string)) error) error
	Text(line string)
	TextChunk(chunk string)
	ToolCall(name, args string)
}

var labelPrefix = regexp.MustCompile(`^\[[^\]]+\] `)

type writer struct {
	mu      sync.Mutex
	w       io.Writer
	midLine bool
}

// NewWriter returns a Display that appends plain lines to w.
func NewWriter(w io.Writer) Display { return &writer{w: w} }

func (d *writer) line(s string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.midLine {
		_, _ = io.WriteString(d.w, "\n")
		d.midLine = false
	}
	_, _ = io.WriteString(d.w, s+"\n")
}

func (d *writer) Intro(string) {}

func (d *writer) Status(message string, _ Severity) {
	d.line(labelPrefix.ReplaceAllString(message, ""))
}

func (d *writer) Summary(title string, rows [][2]string) {
	var b strings.Builder
	b.WriteString(title)
	for _, r := range rows {
		b.WriteString("\n  " + r[0] + ": " + r[1])
	}
	d.line(b.String())
}

func (d *writer) Task(title string, fn func(log func(string)) error) error {
	d.line(title + "...")
	start := time.Now()
	err := fn(func(m string) { d.line("  " + m) })
	if err != nil {
		return err
	}
	d.line(fmt.Sprintf("%s done (%.1fs)", title, time.Since(start).Seconds()))
	return nil
}

func (d *writer) Text(line string) { d.line(line) }

func (d *writer) TextChunk(chunk string) {
	if chunk == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, _ = io.WriteString(d.w, chunk)
	d.midLine = !strings.HasSuffix(chunk, "\n")
}

func (d *writer) ToolCall(name, args string) { d.line("[" + name + "] " + args) }

// OpenLogFile opens path for appending, creating parent directories, and
// writes a run delimiter.
func OpenLogFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(f, "\n--- Run started: %s ---\n", time.Now().UTC().Format(time.RFC3339)); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/display/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/display/
git commit -m "Add writer-backed display for run logs"
```

---

### Task 10: Host git helpers

Small wrappers around the `git` binary on the host, used by the lifecycle and by `Run`.

**Files:**
- Create: `internal/gitx/gitx.go`, `internal/gitx/gitx_test.go`, `internal/testrepo/testrepo.go`

**Interfaces:**
- Produces:
  ```go
  func Run(ctx context.Context, dir string, args ...string) (string, error) // trimmed stdout; error includes stderr
  func CurrentBranch(ctx context.Context, dir string) (string, error)       // git rev-parse --abbrev-ref HEAD
  func Head(ctx context.Context, dir string) (string, error)                // git rev-parse HEAD
  func RevList(ctx context.Context, dir, from, to string) ([]string, error) // git rev-list from..to --reverse; nil error, nil list when to does not exist
  func ConfigValue(ctx context.Context, dir, key string) string             // "" when unset
  func testrepo.Init(t testing.TB) string   // package internal/testrepo, a non-test file so every package's tests can import it
  ```

- [ ] **Step 1: Write the failing tests**

`internal/testrepo/testrepo.go` (a regular file, not `_test.go`, so other packages' tests can import it):
```go
// Package testrepo builds throwaway git repositories for tests.
package testrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Init creates a repo with one commit on branch main and returns its path.
func Init(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@example.com")
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "initial")
	return dir
}
```

`internal/gitx/gitx_test.go`:
```go
package gitx

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/e11en/agentgopher/internal/testrepo"
)

func TestCurrentBranchAndHead(t *testing.T) {
	dir := testrepo.Init(t)
	ctx := context.Background()
	if b, err := CurrentBranch(ctx, dir); err != nil || b != "main" {
		t.Fatalf("branch = %q, %v", b, err)
	}
	if h, err := Head(ctx, dir); err != nil || len(h) != 40 {
		t.Fatalf("head = %q, %v", h, err)
	}
}

func TestRevListAndConfig(t *testing.T) {
	dir := testrepo.Init(t)
	ctx := context.Background()
	base, _ := Head(ctx, dir)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	if _, err := Run(ctx, dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(ctx, dir, "commit", "-q", "-m", "second"); err != nil {
		t.Fatal(err)
	}
	shas, err := RevList(ctx, dir, base, "HEAD")
	if err != nil || len(shas) != 1 {
		t.Fatalf("shas = %v, %v", shas, err)
	}
	if shas, err := RevList(ctx, dir, base, "refs/heads/nope"); err != nil || shas != nil {
		t.Fatalf("missing ref: %v, %v", shas, err)
	}
	if got := ConfigValue(ctx, dir, "user.name"); got != "Test User" {
		t.Fatalf("user.name = %q", got)
	}
	if got := ConfigValue(ctx, dir, "nope.nope"); got != "" {
		t.Fatalf("unset = %q", got)
	}
}

func TestRunErrorIncludesStderr(t *testing.T) {
	_, err := Run(context.Background(), t.TempDir(), "rev-parse", "HEAD")
	if err == nil {
		t.Fatal("expected error outside a repo")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/gitx/`
Expected: FAIL, "undefined: CurrentBranch".

- [ ] **Step 3: Write gitx.go**

```go
// Package gitx wraps the git binary for host-side operations.
package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Run executes git with args in dir and returns trimmed stdout.
func Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// CurrentBranch returns the checked-out branch name.
func CurrentBranch(ctx context.Context, dir string) (string, error) {
	return Run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
}

// Head returns the HEAD commit SHA.
func Head(ctx context.Context, dir string) (string, error) {
	return Run(ctx, dir, "rev-parse", "HEAD")
}

// RevList returns the commits in from..to, oldest first. A missing `to` ref
// yields nil, nil: no commits were produced on it.
func RevList(ctx context.Context, dir, from, to string) ([]string, error) {
	out, err := Run(ctx, dir, "rev-list", from+".."+to, "--reverse")
	if err != nil {
		if strings.Contains(err.Error(), "unknown revision") || strings.Contains(err.Error(), "bad revision") {
			return nil, nil
		}
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// ConfigValue returns a git config value, or "" when unset.
func ConfigValue(ctx context.Context, dir, key string) string {
	out, err := Run(ctx, dir, "config", key)
	if err != nil {
		return ""
	}
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/gitx/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gitx/ internal/testrepo/
git commit -m "Add host git helpers and test repo fixture"
```

---

### Task 11: Sandbox lifecycle (head strategy)

Port of `SandboxLifecycle.ts` for the case `branch` is set (head strategy passes the host branch, so no merge happens). Merge-to-head and `applyToHost` come in the phase 3 plan. Does, in order: git safe.directory and identity in the sandbox, branch discovery, `onSandboxReady` hooks (sandbox and host, in parallel, each with a timeout), base HEAD, the caller's work, commit collection.

**Files:**
- Create: `internal/lifecycle/lifecycle.go`, `internal/lifecycle/lifecycle_test.go`

**Interfaces:**
- Consumes: `sandbox.Handle` (Task 4), `display.Display` (Task 9), `gitx` (Task 10).
- Produces:
  ```go
  type HostHook struct { Command string; Timeout time.Duration }
  type SandboxHook struct { Command string; Sudo bool; Timeout time.Duration }
  type Hooks struct {
      HostOnWorktreeReady  []HostHook
      HostOnSandboxReady   []HostHook
      SandboxOnSandboxReady []SandboxHook
  }
  type Timeouts struct { GitSetup, CommitCollection time.Duration } // zero means default (10 s, 30 s)
  type Options struct {
      HostRepoDir, SandboxRepoDir string
      Branch string              // the branch commits are collected from; required in this plan
      Hooks Hooks
      Timeouts Timeouts
      Display display.Display
  }
  type Context struct { Sandbox sandbox.Handle; SandboxRepoDir, BaseHead string }
  type Result[A any] struct { Value A; Branch string; Commits []string }
  func Run[A any](ctx context.Context, opts Options, h sandbox.Handle, work func(Context) (A, error)) (Result[A], error)
  func RunHostHooks(ctx context.Context, hooks []HostHook, cwd string) error
  const DefaultHookTimeout = 60 * time.Second
  ```
- Errors: `*lifecycle.ExecError{Command, ExitCode, Stderr}`, `*lifecycle.HookTimeoutError{Command, Timeout}`, `*lifecycle.GitSetupTimeoutError{Command, Timeout}`, `*lifecycle.CommitCollectionTimeoutError{Timeout}`. Alias the last three and `ExecError` from `errors.go` (replace the Task 1 structs with `type ExecError = lifecycle.ExecError` etc.) and update `errors_test.go` expectations if a message changed (keep the messages from Task 1).

- [ ] **Step 1: Write the failing tests**

`internal/lifecycle/lifecycle_test.go`:
```go
package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/testrepo"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

func setup(t *testing.T) (string, sandbox.Handle, *bytes.Buffer) {
	t.Helper()
	repo := testrepo.Init(t)
	h, err := nosandbox.New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: repo})
	if err != nil {
		t.Fatal(err)
	}
	return repo, h, &bytes.Buffer{}
}

func TestRunCollectsCommitsMadeByWork(t *testing.T) {
	repo, h, log := setup(t)
	opts := Options{HostRepoDir: repo, SandboxRepoDir: repo, Branch: "main", Display: display.NewWriter(log)}
	res, err := Run(context.Background(), opts, h, func(c Context) (string, error) {
		if len(c.BaseHead) != 40 {
			t.Fatalf("BaseHead = %q", c.BaseHead)
		}
		os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x"), 0o644)
		_, _ = c.Sandbox.Exec(context.Background(), "git add . && git commit -q -m 'agent change'", sandbox.ExecOptions{})
		return "work-result", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Value != "work-result" || res.Branch != "main" || len(res.Commits) != 1 {
		t.Fatalf("res = %+v", res)
	}
	if !strings.Contains(log.String(), "Setting up sandbox") {
		t.Fatalf("log:\n%s", log.String())
	}
}

func TestRunNoCommits(t *testing.T) {
	repo, h, log := setup(t)
	res, err := Run(context.Background(), Options{HostRepoDir: repo, SandboxRepoDir: repo, Branch: "main", Display: display.NewWriter(log)}, h,
		func(Context) (int, error) { return 1, nil })
	if err != nil || len(res.Commits) != 0 {
		t.Fatalf("res = %+v, %v", res, err)
	}
}

func TestRunExecutesHooksAndLogsThem(t *testing.T) {
	repo, h, log := setup(t)
	opts := Options{HostRepoDir: repo, SandboxRepoDir: repo, Branch: "main", Display: display.NewWriter(log), Hooks: Hooks{
		SandboxOnSandboxReady: []SandboxHook{{Command: "touch from-sandbox"}},
		HostOnSandboxReady:    []HostHook{{Command: "touch from-host"}},
	}}
	if _, err := Run(context.Background(), opts, h, func(Context) (int, error) { return 0, nil }); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"from-sandbox", "from-host"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Fatalf("%s not created", f)
		}
	}
	if !strings.Contains(log.String(), "  touch from-sandbox") || !strings.Contains(log.String(), "  [host] touch from-host") {
		t.Fatalf("log:\n%s", log.String())
	}
}

func TestRunHookFailureAndTimeout(t *testing.T) {
	repo, h, log := setup(t)
	base := Options{HostRepoDir: repo, SandboxRepoDir: repo, Branch: "main", Display: display.NewWriter(log)}

	failing := base
	failing.Hooks = Hooks{SandboxOnSandboxReady: []SandboxHook{{Command: "exit 7"}}}
	_, err := Run(context.Background(), failing, h, func(Context) (int, error) { return 0, nil })
	var execErr *ExecError
	if !errors.As(err, &execErr) || execErr.ExitCode != 7 {
		t.Fatalf("err = %v", err)
	}

	slow := base
	slow.Hooks = Hooks{HostOnSandboxReady: []HostHook{{Command: "sleep 2", Timeout: 100 * time.Millisecond}}}
	_, err = Run(context.Background(), slow, h, func(Context) (int, error) { return 0, nil })
	var hookErr *HookTimeoutError
	if !errors.As(err, &hookErr) || hookErr.Command != "sleep 2" {
		t.Fatalf("err = %v", err)
	}
}

func TestRunHostHooksSequentialFailFast(t *testing.T) {
	dir := t.TempDir()
	err := RunHostHooks(context.Background(), []HostHook{{Command: "touch one"}, {Command: "exit 1"}, {Command: "touch three"}}, dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "three")); statErr == nil {
		t.Fatal("hooks after a failure must not run")
	}
}

func TestRunPropagatesWorkError(t *testing.T) {
	repo, h, log := setup(t)
	want := errors.New("agent failed")
	_, err := Run(context.Background(), Options{HostRepoDir: repo, SandboxRepoDir: repo, Branch: "main", Display: display.NewWriter(log)}, h,
		func(Context) (int, error) { return 0, want })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/lifecycle/`
Expected: FAIL, "undefined: Options".

- [ ] **Step 3: Write lifecycle.go**

```go
// Package lifecycle prepares a sandbox for an agent and collects its commits.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/sandbox"
)

const (
	DefaultHookTimeout             = 60 * time.Second
	defaultGitSetupTimeout         = 10 * time.Second
	defaultCommitCollectionTimeout = 30 * time.Second
	gitSetupMaxRetries             = 2
	gitSetupRetryDelay             = 250 * time.Millisecond
)

// ExecError reports a command that exited non-zero in the sandbox or on the
// host, or could not be started (ExitCode -1).
type ExecError struct {
	Command  string
	ExitCode int
	Stderr   string
}

func (e *ExecError) Error() string {
	if e.ExitCode < 0 {
		return fmt.Sprintf("command could not be started: %s\n%s", e.Command, e.Stderr)
	}
	return fmt.Sprintf("command failed (exit %d): %s\n%s", e.ExitCode, e.Command, e.Stderr)
}

// HookTimeoutError reports a lifecycle hook that ran longer than Timeout.
type HookTimeoutError struct {
	Command string
	Timeout time.Duration
}

func (e *HookTimeoutError) Error() string {
	return fmt.Sprintf("hook '%s' timed out after %v", e.Command, e.Timeout)
}

// GitSetupTimeoutError reports an in-sandbox git setup command that ran longer than Timeout.
type GitSetupTimeoutError struct {
	Command string
	Timeout time.Duration
}

func (e *GitSetupTimeoutError) Error() string {
	return fmt.Sprintf("git command timed out after %v: %s", e.Timeout, e.Command)
}

// CommitCollectionTimeoutError reports that listing the run's commits ran longer than Timeout.
type CommitCollectionTimeoutError struct{ Timeout time.Duration }

func (e *CommitCollectionTimeoutError) Error() string {
	return fmt.Sprintf("commit collection timed out after %v", e.Timeout)
}

// HostHook runs on the host. Timeout zero means DefaultHookTimeout.
type HostHook struct {
	Command string
	Timeout time.Duration
}

// SandboxHook runs inside the sandbox. Timeout zero means DefaultHookTimeout.
type SandboxHook struct {
	Command string
	Sudo    bool
	Timeout time.Duration
}

// Hooks groups lifecycle hooks by location and moment.
type Hooks struct {
	HostOnWorktreeReady   []HostHook
	HostOnSandboxReady    []HostHook
	SandboxOnSandboxReady []SandboxHook
}

// Timeouts override built-in step timeouts. Zero keeps the default.
type Timeouts struct {
	GitSetup         time.Duration
	CommitCollection time.Duration
}

// Options configure Run.
type Options struct {
	HostRepoDir    string
	SandboxRepoDir string
	// Branch is the branch commits are collected from.
	Branch   string
	Hooks    Hooks
	Timeouts Timeouts
	Display  display.Display
}

// Context is handed to the caller's work function.
type Context struct {
	Sandbox        sandbox.Handle
	SandboxRepoDir string
	BaseHead       string
}

// Result wraps the work's value with the branch and the commits it produced.
type Result[A any] struct {
	Value   A
	Branch  string
	Commits []string
}

// Run sets up git inside the sandbox, runs the onSandboxReady hooks, runs
// work, and returns the commits on opts.Branch made since work started.
func Run[A any](ctx context.Context, opts Options, h sandbox.Handle, work func(Context) (A, error)) (Result[A], error) {
	var zero Result[A]
	gitTimeout := orDefault(opts.Timeouts.GitSetup, defaultGitSetupTimeout)
	collectTimeout := orDefault(opts.Timeouts.CommitCollection, defaultCommitCollectionTimeout)

	hostName := gitx.ConfigValue(ctx, opts.HostRepoDir, "user.name")
	hostEmail := gitx.ConfigValue(ctx, opts.HostRepoDir, "user.email")

	err := opts.Display.Task("Setting up sandbox", func(log func(string)) error {
		// The mounted worktree may be owned by another UID; mark it safe so git
		// does not reject it with "dubious ownership".
		setup := []string{fmt.Sprintf(`git config --global --add safe.directory "%s"`, opts.SandboxRepoDir)}
		if hostName != "" {
			setup = append(setup, fmt.Sprintf(`git config --global user.name "%s"`, strings.ReplaceAll(hostName, `"`, `\"`)))
		}
		if hostEmail != "" {
			setup = append(setup, fmt.Sprintf(`git config --global user.email "%s"`, strings.ReplaceAll(hostEmail, `"`, `\"`)))
		}
		for _, cmd := range setup {
			if _, err := execWithGitTimeout(ctx, h, cmd, "", gitTimeout); err != nil {
				return err
			}
		}
		return runOnSandboxReady(ctx, opts, h, log)
	})
	if err != nil {
		return zero, err
	}

	baseHead, err := gitx.Head(ctx, opts.HostRepoDir)
	if err != nil {
		return zero, err
	}

	value, err := work(Context{Sandbox: h, SandboxRepoDir: opts.SandboxRepoDir, BaseHead: baseHead})
	if err != nil {
		return zero, err
	}

	var commits []string
	err = opts.Display.Task("Collecting commits", func(func(string)) error {
		cctx, cancel := context.WithTimeout(ctx, collectTimeout)
		defer cancel()
		var listErr error
		commits, listErr = gitx.RevList(cctx, opts.HostRepoDir, baseHead, "refs/heads/"+opts.Branch)
		if cctx.Err() == context.DeadlineExceeded {
			return &CommitCollectionTimeoutError{Timeout: collectTimeout}
		}
		return listErr
	})
	if err != nil {
		return zero, err
	}
	return Result[A]{Value: value, Branch: opts.Branch, Commits: commits}, nil
}

func orDefault(d, def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return d
}

// execOK runs a command in the sandbox and converts a non-zero exit to ExecError.
func execOK(ctx context.Context, h sandbox.Handle, command, cwd string, sudo bool) (sandbox.ExecResult, error) {
	res, err := h.Exec(ctx, command, sandbox.ExecOptions{Cwd: cwd, Sudo: sudo})
	if err != nil {
		var start *sandbox.StartError
		if errors.As(err, &start) {
			return res, &ExecError{Command: command, ExitCode: -1, Stderr: start.Cause.Error()}
		}
		return res, err
	}
	if res.ExitCode != 0 {
		return res, &ExecError{Command: command, ExitCode: res.ExitCode, Stderr: res.Stderr}
	}
	return res, nil
}

// execWithGitTimeout bounds each attempt and retries exit codes 126 and 137,
// which signal a transient exec race under container load, not a git error.
func execWithGitTimeout(ctx context.Context, h sandbox.Handle, command, cwd string, timeout time.Duration) (sandbox.ExecResult, error) {
	var res sandbox.ExecResult
	var err error
	for attempt := 0; attempt <= gitSetupMaxRetries; attempt++ {
		actx, cancel := context.WithTimeout(ctx, timeout)
		res, err = execOK(actx, h, command, cwd, false)
		cancel()
		if actx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
			return res, &GitSetupTimeoutError{Command: command, Timeout: timeout}
		}
		var execErr *ExecError
		if errors.As(err, &execErr) && (execErr.ExitCode == 126 || execErr.ExitCode == 137) {
			time.Sleep(gitSetupRetryDelay)
			continue
		}
		return res, err
	}
	return res, err
}

func runOnSandboxReady(ctx context.Context, opts Options, h sandbox.Handle, log func(string)) error {
	for _, hook := range opts.Hooks.SandboxOnSandboxReady {
		log(hook.Command)
	}
	for _, hook := range opts.Hooks.HostOnSandboxReady {
		log("[host] " + hook.Command)
	}
	total := len(opts.Hooks.SandboxOnSandboxReady) + len(opts.Hooks.HostOnSandboxReady)
	if total == 0 {
		return nil
	}
	errs := make([]error, total)
	var wg sync.WaitGroup
	for i, hook := range opts.Hooks.SandboxOnSandboxReady {
		wg.Add(1)
		go func(i int, hook SandboxHook) {
			defer wg.Done()
			timeout := orDefault(hook.Timeout, DefaultHookTimeout)
			hctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			_, err := execOK(hctx, h, hook.Command, opts.SandboxRepoDir, hook.Sudo)
			if hctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
				err = &HookTimeoutError{Command: hook.Command, Timeout: timeout}
			}
			errs[i] = err
		}(i, hook)
	}
	offset := len(opts.Hooks.SandboxOnSandboxReady)
	for i, hook := range opts.Hooks.HostOnSandboxReady {
		wg.Add(1)
		go func(i int, hook HostHook) {
			defer wg.Done()
			errs[offset+i] = runHostHook(ctx, hook, opts.HostRepoDir)
		}(i, hook)
	}
	wg.Wait()
	return errors.Join(errs...)
}

// RunHostHooks runs hooks sequentially on the host in cwd, failing fast.
func RunHostHooks(ctx context.Context, hooks []HostHook, cwd string) error {
	for _, hook := range hooks {
		if err := runHostHook(ctx, hook, cwd); err != nil {
			return err
		}
	}
	return nil
}

func runHostHook(ctx context.Context, hook HostHook, cwd string) error {
	timeout := orDefault(hook.Timeout, DefaultHookTimeout)
	hctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(hctx, "sh", "-c", hook.Command)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if hctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		return &HookTimeoutError{Command: hook.Command, Timeout: timeout}
	}
	if err != nil {
		code := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		return &ExecError{Command: hook.Command, ExitCode: code, Stderr: "host hook failed: " + strings.TrimSpace(string(out))}
	}
	return nil
}
```

- [ ] **Step 4: Alias the errors from the root package**

In `errors.go`, delete the `ExecError`, `HookTimeoutError`, `GitSetupTimeoutError` and `CommitCollectionTimeoutError` structs and methods and replace them with:
```go
type ExecError = lifecycle.ExecError
type HookTimeoutError = lifecycle.HookTimeoutError
type GitSetupTimeoutError = lifecycle.GitSetupTimeoutError
type CommitCollectionTimeoutError = lifecycle.CommitCollectionTimeoutError
```
with a one-line doc comment each and the import `"github.com/e11en/agentgopher/internal/lifecycle"`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./...`
Expected: PASS. `TestExecErrorMessage` in the root still passes because the message format is unchanged.

- [ ] **Step 6: Commit**

```bash
git add internal/lifecycle/ errors.go
git commit -m "Add sandbox lifecycle with git setup, hooks and commit collection"
```

---

### Task 12: One agent invocation with idle and completion timers

Port of `invokeAgent` in `Orchestrator.ts`. Runs the agent's print command in the sandbox, parses every stdout line, and enforces two timers: an idle timeout before the completion signal (expiry fails the iteration) and a completion grace window after it (expiry force-completes with the buffered output, ADR 0019). Both reset on every output line.

**Files:**
- Create: `internal/orchestrator/invoke.go`, `internal/orchestrator/fakes_test.go`, `internal/orchestrator/invoke_test.go`

**Interfaces:**
- Consumes: `agent.Provider` (Task 6), `sandbox.Handle` (Task 4).
- Produces (package-private, used by Task 13):
  ```go
  type invokeOptions struct {
      Prompt              string
      Provider            agent.Provider
      SkipPermissions     bool
      IdleTimeout         time.Duration
      CompletionTimeout   time.Duration
      IdleWarningInterval time.Duration   // zero means 1 minute
      CompletionSignals   []string
      ResumeSession       string
      ForkSession         bool
      OnText              func(text string)
      OnToolCall          func(name, args string)
      OnRawLine           func(line string)
      OnIdleWarning       func(minutes int)
      OnCompletionTimeout func(grace time.Duration)
  }
  type invokeResult struct { Output, SessionID string; Usage *agent.Usage }
  func invokeAgent(ctx context.Context, h sandbox.Handle, repoDir string, o invokeOptions) (invokeResult, error)
  type AgentError struct { Message string }            // alias target for the root package
  type AgentIdleTimeoutError struct { Timeout time.Duration }
  ```
- `invokeResult.Output` is the `result` event text when one was seen, else the raw stdout tail.
- Alias `AgentError` and `AgentIdleTimeoutError` from `errors.go` (replace the Task 1 structs with `type AgentError = orchestrator.AgentError` and `type AgentIdleTimeoutError = orchestrator.AgentIdleTimeoutError`; messages stay identical so `errors_test.go` keeps passing).

- [ ] **Step 1: Write the test fakes**

`internal/orchestrator/fakes_test.go`:
```go
package orchestrator

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox"
)

// scriptedLine is one stdout line the fake handle emits after a delay.
type scriptedLine struct {
	after time.Duration
	line  string
}

// fakeHandle plays scripted stdout lines through OnLine. exitCode and stderr
// are returned when the script ends; hang keeps the process open after the
// last line until the context is cancelled.
type fakeHandle struct {
	mu       sync.Mutex
	script   []scriptedLine
	exitCode int
	stderr   string
	hang     bool
	commands []string
	stdins   []string
}

func (f *fakeHandle) WorktreePath() string         { return "/fake" }
func (f *fakeHandle) Close(context.Context) error { return nil }

func (f *fakeHandle) Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error) {
	f.mu.Lock()
	f.commands = append(f.commands, command)
	if opts.Stdin != nil {
		f.stdins = append(f.stdins, *opts.Stdin)
	}
	f.mu.Unlock()
	var out []string
	for _, s := range f.script {
		select {
		case <-time.After(s.after):
		case <-ctx.Done():
			return sandbox.ExecResult{}, ctx.Err()
		}
		out = append(out, s.line)
		if opts.OnLine != nil {
			opts.OnLine(s.line)
		}
	}
	if f.hang {
		<-ctx.Done()
		return sandbox.ExecResult{}, ctx.Err()
	}
	return sandbox.ExecResult{Stdout: strings.Join(out, "\n"), Stderr: f.stderr, ExitCode: f.exitCode}, nil
}

// fakeProvider treats every line as text, except lines starting with
// "TOOL " (tool call), "RESULT " (result) and "SESSION " (session id).
type fakeProvider struct{}

func (fakeProvider) Name() string           { return "fake" }
func (fakeProvider) Env() map[string]string { return nil }
func (fakeProvider) CaptureSessions() bool  { return false }
func (fakeProvider) BuildPrintCommand(o agent.CommandOptions) agent.PrintCommand {
	p := o.Prompt
	cmd := "fake-agent"
	if o.DangerouslySkipPermissions {
		cmd += " --yolo"
	}
	if o.ResumeSession != "" {
		cmd += " --resume " + o.ResumeSession
	}
	return agent.PrintCommand{Command: cmd, Stdin: &p}
}
func (fakeProvider) ParseStreamLine(line string) []agent.StreamEvent {
	switch {
	case strings.HasPrefix(line, "TOOL "):
		name, args, _ := strings.Cut(strings.TrimPrefix(line, "TOOL "), " ")
		return []agent.StreamEvent{{Type: agent.EventToolCall, Name: name, Args: args}}
	case strings.HasPrefix(line, "RESULT "):
		return []agent.StreamEvent{{Type: agent.EventResult, Text: strings.TrimPrefix(line, "RESULT ")}}
	case strings.HasPrefix(line, "SESSION "):
		return []agent.StreamEvent{{Type: agent.EventSessionID, SessionID: strings.TrimPrefix(line, "SESSION ")}}
	case line == "":
		return nil
	}
	return []agent.StreamEvent{{Type: agent.EventText, Text: line}}
}
```

- [ ] **Step 2: Write the failing tests**

`internal/orchestrator/invoke_test.go`:
```go
package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const signal = "<promise>COMPLETE</promise>"

func baseInvoke(h *fakeHandle) (invokeOptions, *[]string) {
	var texts []string
	return invokeOptions{
		Prompt:            "do the thing",
		Provider:          fakeProvider{},
		SkipPermissions:   true,
		IdleTimeout:       time.Second,
		CompletionTimeout: 300 * time.Millisecond,
		CompletionSignals: []string{signal},
		OnText:            func(t string) { texts = append(texts, t) },
	}, &texts
}

func TestInvokeParsesEventsAndPipesPrompt(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{0, "hello"}, {0, "TOOL Bash ls"}, {0, "SESSION s-9"}, {0, "RESULT " + signal}}}
	o, texts := baseInvoke(h)
	var tools []string
	o.OnToolCall = func(n, a string) { tools = append(tools, n+":"+a) }
	res, err := invokeAgent(context.Background(), h, "/repo", o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != signal || res.SessionID != "s-9" {
		t.Fatalf("res = %+v", res)
	}
	if len(*texts) != 1 || (*texts)[0] != "hello" || len(tools) != 1 || tools[0] != "Bash:ls" {
		t.Fatalf("texts=%v tools=%v", *texts, tools)
	}
	if h.commands[0] != "fake-agent --yolo" || h.stdins[0] != "do the thing" {
		t.Fatalf("commands=%v stdins=%v", h.commands, h.stdins)
	}
}

func TestInvokeIdleTimeoutFails(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{0, "working"}, {2 * time.Second, "late"}}}
	o, _ := baseInvoke(h)
	o.IdleTimeout = 150 * time.Millisecond
	_, err := invokeAgent(context.Background(), h, "/repo", o)
	var idle *AgentIdleTimeoutError
	if !errors.As(err, &idle) || idle.Timeout != 150*time.Millisecond {
		t.Fatalf("err = %v", err)
	}
}

func TestInvokeIdleTimerResetsOnOutput(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{100 * time.Millisecond, "a"}, {100 * time.Millisecond, "b"}, {100 * time.Millisecond, "c"}}}
	o, _ := baseInvoke(h)
	o.IdleTimeout = 200 * time.Millisecond
	if _, err := invokeAgent(context.Background(), h, "/repo", o); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestInvokeCompletionSignalSplitAcrossLinesForceCompletesHangingProcess(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{0, "<promise>COMP"}, {0, "LETE</promise>"}}, hang: true}
	o, _ := baseInvoke(h)
	var graceSeen time.Duration
	o.OnCompletionTimeout = func(d time.Duration) { graceSeen = d }
	start := time.Now()
	res, err := invokeAgent(context.Background(), h, "/repo", o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, signal) {
		t.Fatalf("Output = %q", res.Output)
	}
	if graceSeen != 300*time.Millisecond || time.Since(start) > 900*time.Millisecond {
		t.Fatalf("grace=%v elapsed=%v", graceSeen, time.Since(start))
	}
}

func TestInvokeNonZeroExitUsesStderrThenStdoutTail(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{0, "l1"}, {0, "l2"}}, exitCode: 2, stderr: "boom"}
	o, _ := baseInvoke(h)
	_, err := invokeAgent(context.Background(), h, "/repo", o)
	var aerr *AgentError
	if !errors.As(err, &aerr) || !strings.Contains(aerr.Message, "exited with code 2") || !strings.Contains(aerr.Message, "boom") {
		t.Fatalf("err = %v", err)
	}

	var lines []scriptedLine
	for i := 0; i < 25; i++ {
		lines = append(lines, scriptedLine{0, "line" + strings.Repeat("x", i)})
	}
	h = &fakeHandle{script: lines, exitCode: 1}
	o, _ = baseInvoke(h)
	_, err = invokeAgent(context.Background(), h, "/repo", o)
	if !errors.As(err, &aerr) || strings.Contains(aerr.Message, "line\n") || !strings.Contains(aerr.Message, "line"+strings.Repeat("x", 24)) {
		t.Fatalf("err = %v", err)
	}
	if strings.Count(aerr.Message, "\n") > 21 {
		t.Fatalf("more than 20 tail lines: %q", aerr.Message)
	}
}

func TestInvokeIdleWarnings(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{350 * time.Millisecond, "RESULT " + signal}}}
	o, _ := baseInvoke(h)
	o.IdleWarningInterval = 100 * time.Millisecond
	var warnings []int
	o.OnIdleWarning = func(m int) { warnings = append(warnings, m) }
	if _, err := invokeAgent(context.Background(), h, "/repo", o); err != nil {
		t.Fatal(err)
	}
	if len(warnings) < 2 || warnings[0] != 1 || warnings[1] != 2 {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestInvokeCancelledContext(t *testing.T) {
	h := &fakeHandle{hang: true}
	o, _ := baseInvoke(h)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := invokeAgent(ctx, h, "/repo", o)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/orchestrator/`
Expected: FAIL, "undefined: invokeOptions".

- [ ] **Step 4: Write invoke.go**

```go
// Package orchestrator runs the agent iteration loop.
package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox"
)

const defaultIdleWarningInterval = time.Minute

// AgentError reports an agent process that exited non-zero.
type AgentError struct{ Message string }

func (e *AgentError) Error() string { return e.Message }

// AgentIdleTimeoutError reports an agent that produced no output for Timeout.
type AgentIdleTimeoutError struct{ Timeout time.Duration }

func (e *AgentIdleTimeoutError) Error() string {
	return fmt.Sprintf("agent idle for %v - no output received", e.Timeout)
}

type invokeOptions struct {
	Prompt              string
	Provider            agent.Provider
	SkipPermissions     bool
	IdleTimeout         time.Duration
	CompletionTimeout   time.Duration
	IdleWarningInterval time.Duration
	CompletionSignals   []string
	ResumeSession       string
	ForkSession         bool
	OnText              func(text string)
	OnToolCall          func(name, args string)
	OnRawLine           func(line string)
	OnIdleWarning       func(minutes int)
	OnCompletionTimeout func(grace time.Duration)
}

type invokeResult struct {
	Output    string
	SessionID string
	Usage     *agent.Usage
}

// streamState is shared between the exec goroutine (writer) and the watchdog (reader).
type streamState struct {
	mu          sync.Mutex
	result      string
	accumulated string
	sessionID   string
	usage       *agent.Usage
	completed   bool
}

type execOutcome struct {
	res sandbox.ExecResult
	err error
}

// invokeAgent runs one agent iteration and returns its output. It fails with
// AgentIdleTimeoutError when no output arrives for IdleTimeout before the
// completion signal, and force-completes successfully when the process hangs
// for CompletionTimeout after the signal (ADR 0019).
func invokeAgent(ctx context.Context, h sandbox.Handle, repoDir string, o invokeOptions) (invokeResult, error) {
	warnInterval := o.IdleWarningInterval
	if warnInterval == 0 {
		warnInterval = defaultIdleWarningInterval
	}
	execCtx, cancelExec := context.WithCancel(ctx)
	defer cancelExec()

	state := &streamState{}
	activity := make(chan struct{}, 1)
	done := make(chan execOutcome, 1)

	printCmd := o.Provider.BuildPrintCommand(agent.CommandOptions{
		Prompt:                     o.Prompt,
		DangerouslySkipPermissions: o.SkipPermissions,
		ResumeSession:              o.ResumeSession,
		ForkSession:                o.ForkSession,
	})

	go func() {
		res, err := h.Exec(execCtx, printCmd.Command, sandbox.ExecOptions{
			Cwd:   repoDir,
			Stdin: printCmd.Stdin,
			OnLine: func(line string) {
				if o.OnRawLine != nil {
					o.OnRawLine(line)
				}
				state.mu.Lock()
				for _, ev := range o.Provider.ParseStreamLine(line) {
					switch ev.Type {
					case agent.EventText:
						state.accumulated += ev.Text
						state.mu.Unlock()
						if o.OnText != nil {
							o.OnText(ev.Text)
						}
						state.mu.Lock()
					case agent.EventResult:
						state.result = ev.Text
						state.accumulated += ev.Text
					case agent.EventToolCall:
						state.mu.Unlock()
						if o.OnToolCall != nil {
							o.OnToolCall(ev.Name, ev.Args)
						}
						state.mu.Lock()
					case agent.EventSessionID:
						state.sessionID = ev.SessionID
					case agent.EventUsage:
						state.usage = ev.Usage
					}
				}
				if !state.completed {
					for _, sig := range o.CompletionSignals {
						if strings.Contains(state.accumulated, sig) {
							state.completed = true
							break
						}
					}
				}
				state.mu.Unlock()
				select {
				case activity <- struct{}{}:
				default:
				}
			},
		})
		done <- execOutcome{res: res, err: err}
	}()

	snapshot := func() invokeResult {
		state.mu.Lock()
		defer state.mu.Unlock()
		out := state.result
		if out == "" {
			out = state.accumulated
		}
		return invokeResult{Output: out, SessionID: state.sessionID, Usage: state.usage}
	}
	isCompleted := func() bool {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.completed
	}

	timer := time.NewTimer(o.IdleTimeout)
	defer timer.Stop()
	warn := time.NewTicker(warnInterval)
	defer warn.Stop()
	idleMinutes := 0

	for {
		select {
		case <-activity:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if isCompleted() {
				timer.Reset(o.CompletionTimeout)
			} else {
				timer.Reset(o.IdleTimeout)
				warn.Reset(warnInterval)
				idleMinutes = 0
			}
		case <-warn.C:
			if !isCompleted() {
				idleMinutes++
				if o.OnIdleWarning != nil {
					o.OnIdleWarning(idleMinutes)
				}
			}
		case <-timer.C:
			if isCompleted() {
				if o.OnCompletionTimeout != nil {
					o.OnCompletionTimeout(o.CompletionTimeout)
				}
				cancelExec()
				<-done
				return snapshot(), nil
			}
			cancelExec()
			<-done
			return invokeResult{}, &AgentIdleTimeoutError{Timeout: o.IdleTimeout}
		case out := <-done:
			if ctx.Err() != nil {
				return invokeResult{}, ctx.Err()
			}
			if out.err != nil {
				return invokeResult{}, out.err
			}
			if out.res.ExitCode != 0 {
				state.mu.Lock()
				parsedResult := state.result
				state.mu.Unlock()
				return invokeResult{}, &AgentError{Message: fmt.Sprintf("%s exited with code %d:\n%s", o.Provider.Name(), out.res.ExitCode, errorDetail(out.res, parsedResult))}
			}
			r := snapshot()
			if r.Output == "" {
				r.Output = out.res.Stdout
			}
			return r, nil
		}
	}
}

// errorDetail prefers stderr, then the parsed result text, then the last 20
// non-empty stdout lines.
func errorDetail(res sandbox.ExecResult, parsed string) string {
	if strings.TrimSpace(res.Stderr) != "" {
		return res.Stderr
	}
	if strings.TrimSpace(parsed) != "" {
		return parsed
	}
	var lines []string
	for _, l := range strings.Split(res.Stdout, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	return strings.Join(lines, "\n")
}
```

- [ ] **Step 5: Alias the errors from the root package**

In `errors.go` replace the `AgentError` and `AgentIdleTimeoutError` structs and methods with:
```go
// AgentError reports an agent process that exited non-zero.
type AgentError = orchestrator.AgentError
// AgentIdleTimeoutError reports an agent that produced no output for Timeout.
type AgentIdleTimeoutError = orchestrator.AgentIdleTimeoutError
```
and import `"github.com/e11en/agentgopher/internal/orchestrator"`.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test -race ./internal/orchestrator/ ./`
Expected: PASS. If `TestInvokeIdleWarnings` is flaky on a loaded machine, raise its script delay to 450 ms.

- [ ] **Step 7: Commit**

```bash
git add internal/orchestrator/ errors.go
git commit -m "Add agent invocation with idle and completion timers"
```

---

### Task 13: Iteration loop

Port of `orchestrate` in `Orchestrator.ts` for the head strategy with a no-sandbox provider: per iteration create a handle, run the lifecycle, expand the prompt, invoke the agent, stop on the completion signal. Session resume and capture are left as no-ops until the phase 5 plan.

**Files:**
- Create: `internal/orchestrator/orchestrator.go`, `internal/orchestrator/orchestrator_test.go`
- Modify: `internal/orchestrator/fakes_test.go` (add `fakeNoSandboxProvider`)

**Interfaces:**
- Consumes: `lifecycle.Run` (Task 11), `prompt.Expand` (Task 8), `stream.NewTextDeltaBuffer` (Task 3), `display.Display` (Task 9), `invokeAgent` (Task 12).
- Produces:
  ```go
  type StreamEventType string // StreamText, StreamToolCall, StreamRaw
  type StreamEvent struct { Type StreamEventType; Message, Name, FormattedArgs, Line string; Iteration int; Timestamp time.Time }
  type Options struct {
      HostRepoDir         string
      Iterations          int
      Hooks               lifecycle.Hooks
      Prompt              string
      Branch              string            // head strategy: the host branch
      Provider            agent.Provider
      Sandbox             sandbox.NoSandboxProvider
      Env                 map[string]string
      CompletionSignals   []string          // empty means the default
      IdleTimeout         time.Duration     // zero means 10 min
      CompletionTimeout   time.Duration     // zero means 60 s
      IdleWarningInterval time.Duration     // tests only
      Name                string
      SkipPromptExpansion bool
      Timeouts            lifecycle.Timeouts
      Display             display.Display
      OnStreamEvent       func(StreamEvent) // may be nil
  }
  type IterationResult struct { SessionID string; Usage *agent.Usage }
  type Result struct { Iterations []IterationResult; CompletionSignal string; Stdout string; Commits []string; Branch string }
  func Orchestrate(ctx context.Context, o Options) (Result, error)
  const DefaultCompletionSignal = "<promise>COMPLETE</promise>"
  const DefaultIdleTimeout = 10 * time.Minute
  const DefaultCompletionTimeout = 60 * time.Second
  ```
- No-sandbox is the only provider kind in this plan; `Options.Sandbox` widens to `sandbox.Provider` in the phase 3 plan.

- [ ] **Step 1: Add a fake provider to fakes_test.go**

```go
// fakeNoSandboxProvider hands out one scripted handle per Create call, in order.
type fakeNoSandboxProvider struct {
	mu      sync.Mutex
	handles []*fakeHandle
	created int
}

func (p *fakeNoSandboxProvider) Kind() sandbox.Kind      { return sandbox.KindNone }
func (p *fakeNoSandboxProvider) Name() string            { return "fake-sandbox" }
func (p *fakeNoSandboxProvider) Env() map[string]string  { return nil }
func (p *fakeNoSandboxProvider) Create(context.Context, sandbox.NoSandboxCreateOptions) (sandbox.Handle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h := p.handles[p.created]
	p.created++
	return h, nil
}
```

The fake handle must also answer the lifecycle's git commands. Extend `fakeHandle.Exec`: when `command` starts with `git ` return `sandbox.ExecResult{Stdout: "main", ExitCode: 0}` immediately without touching the script. Add this at the top of `Exec`, after recording the command:
```go
	if strings.HasPrefix(command, "git ") {
		return sandbox.ExecResult{Stdout: "main"}, nil
	}
```

- [ ] **Step 2: Write the failing tests**

`internal/orchestrator/orchestrator_test.go`:
```go
package orchestrator

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/testrepo"
)

func baseOptions(t *testing.T, handles ...*fakeHandle) (Options, *bytes.Buffer) {
	t.Helper()
	repo := testrepo.Init(t)
	log := &bytes.Buffer{}
	return Options{
		HostRepoDir: repo,
		Iterations:  len(handles),
		Prompt:      "work",
		Branch:      "main",
		Provider:    fakeProvider{},
		Sandbox:     &fakeNoSandboxProvider{handles: handles},
		Display:     display.NewWriter(log),
		IdleTimeout: time.Second,
	}, log
}

func TestOrchestrateStopsOnCompletionSignal(t *testing.T) {
	h1 := &fakeHandle{script: []scriptedLine{{0, "iteration one"}}}
	h2 := &fakeHandle{script: []scriptedLine{{0, "RESULT " + DefaultCompletionSignal}}}
	h3 := &fakeHandle{script: []scriptedLine{{0, "never"}}}
	o, log := baseOptions(t, h1, h2, h3)
	res, err := Orchestrate(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Iterations) != 2 || res.CompletionSignal != DefaultCompletionSignal || res.Branch != "main" {
		t.Fatalf("res = %+v", res)
	}
	if len(h3.commands) != 0 {
		t.Fatal("third iteration must not run")
	}
	for _, want := range []string{"Iteration 1/3", "Agent started", "Agent stopped", "Agent signaled completion after 2 iteration(s)."} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("missing %q in log:\n%s", want, log.String())
		}
	}
}

func TestOrchestrateReachesMaxIterations(t *testing.T) {
	o, log := baseOptions(t, &fakeHandle{script: []scriptedLine{{0, "a"}}}, &fakeHandle{script: []scriptedLine{{0, "b"}}})
	res, err := Orchestrate(context.Background(), o)
	if err != nil || res.CompletionSignal != "" || len(res.Iterations) != 2 || res.Stdout != "ab" {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if !strings.Contains(log.String(), "Reached max iterations (2).") {
		t.Fatalf("log:\n%s", log.String())
	}
}

func TestOrchestrateExpandsPromptInSandboxUnlessSkipped(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{0, "RESULT " + DefaultCompletionSignal}}}
	o, _ := baseOptions(t, h)
	o.Prompt = "Branch: !\x01`git branch --show-current`"
	if _, err := Orchestrate(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if h.stdins[0] != "Branch: main" {
		t.Fatalf("prompt sent = %q", h.stdins[0])
	}

	h = &fakeHandle{script: []scriptedLine{{0, "RESULT " + DefaultCompletionSignal}}}
	o, _ = baseOptions(t, h)
	o.Prompt = "literal !`echo x`"
	o.SkipPromptExpansion = true
	if _, err := Orchestrate(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if h.stdins[0] != "literal !`echo x`" {
		t.Fatalf("prompt sent = %q", h.stdins[0])
	}
}

func TestOrchestrateEmitsStreamEventsAndLabels(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{0, "hi there\n"}, {0, "TOOL Bash ls"}, {0, "RESULT " + DefaultCompletionSignal}}}
	o, log := baseOptions(t, h)
	o.Name = "worker"
	var events []StreamEvent
	o.OnStreamEvent = func(e StreamEvent) { events = append(events, e) }
	if _, err := Orchestrate(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range events {
		if e.Iteration != 1 || e.Timestamp.IsZero() {
			t.Fatalf("bad event %+v", e)
		}
		kinds = append(kinds, string(e.Type))
	}
	joined := strings.Join(kinds, ",")
	if !strings.Contains(joined, "raw") || !strings.Contains(joined, "text") || !strings.Contains(joined, "toolCall") {
		t.Fatalf("kinds = %v", kinds)
	}
	if !strings.Contains(log.String(), "[Bash] ls") || strings.Contains(log.String(), "[worker]") {
		t.Fatalf("log:\n%s", log.String())
	}
}

func TestOrchestratePropagatesAgentFailure(t *testing.T) {
	o, log := baseOptions(t, &fakeHandle{exitCode: 3, stderr: "crash"})
	_, err := Orchestrate(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "crash") {
		t.Fatalf("err = %v", err)
	}
	_ = log
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/orchestrator/`
Expected: FAIL, "undefined: Options".

- [ ] **Step 4: Write orchestrator.go**

```go
package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/internal/prompt"
	"github.com/e11en/agentgopher/internal/stream"
	"github.com/e11en/agentgopher/sandbox"
)

const (
	DefaultCompletionSignal  = "<promise>COMPLETE</promise>"
	DefaultIdleTimeout       = 10 * time.Minute
	DefaultCompletionTimeout = 60 * time.Second
)

// StreamEventType classifies a StreamEvent.
type StreamEventType string

const (
	StreamText     StreamEventType = "text"
	StreamToolCall StreamEventType = "toolCall"
	StreamRaw      StreamEventType = "raw"
)

// StreamEvent is one item of the agent's output, surfaced to the caller.
// Message is set for StreamText, Name and FormattedArgs for StreamToolCall,
// Line for StreamRaw.
type StreamEvent struct {
	Type          StreamEventType
	Message       string
	Name          string
	FormattedArgs string
	Line          string
	Iteration     int
	Timestamp     time.Time
}

// Options configure Orchestrate.
type Options struct {
	HostRepoDir         string
	Iterations          int
	Hooks               lifecycle.Hooks
	Prompt              string
	Branch              string
	Provider            agent.Provider
	Sandbox             sandbox.NoSandboxProvider
	Env                 map[string]string
	CompletionSignals   []string
	IdleTimeout         time.Duration
	CompletionTimeout   time.Duration
	IdleWarningInterval time.Duration
	Name                string
	SkipPromptExpansion bool
	Timeouts            lifecycle.Timeouts
	Display             display.Display
	OnStreamEvent       func(StreamEvent)
}

// IterationResult is the outcome of one agent invocation.
type IterationResult struct {
	SessionID string
	Usage     *agent.Usage
}

// Result is the outcome of the whole loop.
type Result struct {
	Iterations       []IterationResult
	CompletionSignal string
	Stdout           string
	Commits          []string
	Branch           string
}

type iterationOutcome struct {
	completionSignal string
	stdout           string
	sessionID        string
	usage            *agent.Usage
}

// Orchestrate runs up to o.Iterations agent invocations, stopping early when
// the agent emits a completion signal.
func Orchestrate(ctx context.Context, o Options) (Result, error) {
	signals := o.CompletionSignals
	if len(signals) == 0 {
		signals = []string{DefaultCompletionSignal}
	}
	idle, completion := o.IdleTimeout, o.CompletionTimeout
	if idle == 0 {
		idle = DefaultIdleTimeout
	}
	if completion == 0 {
		completion = DefaultCompletionTimeout
	}
	label := func(msg string) string {
		if o.Name != "" {
			return "[" + o.Name + "] " + msg
		}
		return msg
	}

	var result Result
	for i := 1; i <= o.Iterations; i++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		o.Display.Status(label(fmt.Sprintf("Iteration %d/%d", i, o.Iterations)), display.SeverityInfo)

		if err := lifecycle.RunHostHooks(ctx, o.Hooks.HostOnWorktreeReady, o.HostRepoDir); err != nil {
			return result, err
		}
		h, err := o.Sandbox.Create(ctx, sandbox.NoSandboxCreateOptions{WorktreePath: o.HostRepoDir, Env: o.Env})
		if err != nil {
			return result, fmt.Errorf("provider '%s' create failed: %w", o.Sandbox.Name(), err)
		}

		lc, err := lifecycle.Run(ctx, lifecycle.Options{
			HostRepoDir:    o.HostRepoDir,
			SandboxRepoDir: h.WorktreePath(),
			Branch:         o.Branch,
			Hooks:          o.Hooks,
			Timeouts:       o.Timeouts,
			Display:        o.Display,
		}, h, func(lctx lifecycle.Context) (iterationOutcome, error) {
			return runIteration(ctx, o, lctx, i, signals, idle, completion, label)
		})
		_ = h.Close(ctx)
		if err != nil {
			return result, err
		}

		result.Commits = append(result.Commits, lc.Commits...)
		result.Stdout += lc.Value.stdout
		result.Branch = lc.Branch
		result.Iterations = append(result.Iterations, IterationResult{SessionID: lc.Value.sessionID, Usage: lc.Value.usage})

		if lc.Value.completionSignal != "" {
			o.Display.Status(label(fmt.Sprintf("Agent signaled completion after %d iteration(s).", i)), display.SeveritySuccess)
			result.CompletionSignal = lc.Value.completionSignal
			return result, nil
		}
	}
	o.Display.Status(label(fmt.Sprintf("Reached max iterations (%d).", o.Iterations)), display.SeverityInfo)
	return result, nil
}

func runIteration(ctx context.Context, o Options, lctx lifecycle.Context, iteration int, signals []string, idle, completion time.Duration, label func(string) string) (iterationOutcome, error) {
	fullPrompt := o.Prompt
	if !o.SkipPromptExpansion {
		var err error
		err = o.Display.Task("Expanding shell expressions", func(log func(string)) error {
			var expandErr error
			fullPrompt, expandErr = prompt.Expand(ctx, o.Prompt, lctx.Sandbox, lctx.SandboxRepoDir, log)
			return expandErr
		})
		if err != nil {
			return iterationOutcome{}, err
		}
	}

	o.Display.Status(label("Agent started"), display.SeveritySuccess)

	emit := func(ev StreamEvent) {
		if o.OnStreamEvent == nil {
			return
		}
		ev.Iteration = iteration
		ev.Timestamp = time.Now()
		// A broken forwarder must not kill the run.
		defer func() { _ = recover() }()
		o.OnStreamEvent(ev)
	}
	textBuffer := stream.NewTextDeltaBuffer(func(chunk string) {
		o.Display.TextChunk(chunk)
		emit(StreamEvent{Type: StreamText, Message: chunk})
	})

	res, err := invokeAgent(ctx, lctx.Sandbox, lctx.SandboxRepoDir, invokeOptions{
		Prompt:              fullPrompt,
		Provider:            o.Provider,
		SkipPermissions:     true,
		IdleTimeout:         idle,
		CompletionTimeout:   completion,
		IdleWarningInterval: o.IdleWarningInterval,
		CompletionSignals:   signals,
		OnText:              textBuffer.Write,
		OnToolCall: func(name, args string) {
			textBuffer.Flush()
			o.Display.ToolCall(name, args)
			emit(StreamEvent{Type: StreamToolCall, Name: name, FormattedArgs: args})
		},
		OnRawLine: func(line string) { emit(StreamEvent{Type: StreamRaw, Line: line}) },
		OnIdleWarning: func(minutes int) {
			msg := "Agent idle for 1 minute"
			if minutes != 1 {
				msg = fmt.Sprintf("Agent idle for %d minutes", minutes)
			}
			o.Display.Status(label(msg), display.SeverityWarn)
		},
		OnCompletionTimeout: func(grace time.Duration) {
			o.Display.Status(label(fmt.Sprintf("Completion signal seen but agent process is hanging - force-completing after %v grace window.", grace)), display.SeverityWarn)
		},
	})
	textBuffer.Close()
	if err != nil {
		return iterationOutcome{}, err
	}
	o.Display.Status(label("Agent stopped"), display.SeverityInfo)

	out := iterationOutcome{stdout: res.Output, sessionID: res.SessionID, usage: res.Usage}
	for _, sig := range signals {
		if strings.Contains(res.Output, sig) {
			out.completionSignal = sig
			break
		}
	}
	return out, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./internal/orchestrator/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/orchestrator/
git commit -m "Add iteration loop with prompt expansion and stream events"
```

---

### Task 14: Public `Run`

Port of `run()` in `run.ts` for the no-sandbox provider and head strategy: validate options, resolve cwd, prompt, env and logging, print the run summary, substitute built-in prompt arguments, orchestrate, return `RunResult`. Structured output, resume and fork, branch strategies other than head, and the stdout spinner UI are deferred to later plans.

**Files:**
- Create: `agentgopher.go`, `agentgopher_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces:
  ```go
  type HostHook = lifecycle.HostHook
  type SandboxHook = lifecycle.SandboxHook
  type Hooks = lifecycle.Hooks
  type Timeouts = lifecycle.Timeouts
  type StreamEvent = orchestrator.StreamEvent
  type IterationResult = orchestrator.IterationResult
  type LoggingType string // LoggingFile = "file", LoggingStdout = "stdout"
  type Logging struct {
      Type               LoggingType
      Path               string              // file mode; default .agentgopher/logs/<branch>[-<name>].log
      OnAgentStreamEvent func(StreamEvent)   // file mode only
      Verbose            bool                // append every raw line to the log (file) or stdout
  }
  type RunOptions struct {
      Agent             agent.Provider
      Sandbox           sandbox.NoSandboxProvider
      Cwd               string
      Prompt            string
      PromptFile        string
      MaxIterations     int                // default 1
      Hooks             Hooks
      PromptArgs        map[string]string
      Logging           *Logging           // nil means file mode with the default path
      CompletionSignal  []string
      IdleTimeout       time.Duration
      CompletionTimeout time.Duration
      Name              string
      BranchStrategy    *sandbox.BranchStrategy // nil means head; only head is accepted in this plan
      Timeouts          Timeouts
  }
  type Commit struct{ SHA string }
  type RunResult struct {
      Iterations       []IterationResult
      CompletionSignal string
      Stdout           string
      Commits          []Commit
      Branch           string
      LogFilePath      string
  }
  func Run(ctx context.Context, o RunOptions) (RunResult, error)
  const DefaultMaxIterations = 1
  func SanitizeBranchForFilename(branch string) string
  func BuildLogFilename(resolvedBranch, targetBranch, name string) string
  ```
- Validation errors are plain `fmt.Errorf` values (upstream throws `Error`). `Cwd` problems return `*CwdError`.

- [ ] **Step 1: Write the failing tests**

`agentgopher_test.go`:
```go
package agentgopher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/testrepo"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

// echoAgent is a Provider whose "CLI" is a shell script: it prints the prompt
// it received over stdin, then the completion signal as a result event.
type echoAgent struct{ script string }

func (echoAgent) Name() string           { return "echo-agent" }
func (echoAgent) Env() map[string]string { return map[string]string{"AGENT_ENV": "1"} }
func (echoAgent) CaptureSessions() bool  { return false }
func (a echoAgent) BuildPrintCommand(o agent.CommandOptions) agent.PrintCommand {
	p := o.Prompt
	return agent.PrintCommand{Command: a.script, Stdin: &p}
}
func (echoAgent) ParseStreamLine(line string) []agent.StreamEvent {
	if strings.HasPrefix(line, "RESULT ") {
		return []agent.StreamEvent{{Type: agent.EventResult, Text: strings.TrimPrefix(line, "RESULT ")}}
	}
	return []agent.StreamEvent{{Type: agent.EventText, Text: line + "\n"}}
}

func TestRunEndToEndOnHost(t *testing.T) {
	repo := testrepo.Init(t)
	os.MkdirAll(filepath.Join(repo, ".agentgopher"), 0o755)
	os.WriteFile(filepath.Join(repo, ".agentgopher", "prompt.md"), []byte("Hello {{WHO}} on {{SOURCE_BRANCH}}\nFiles: !`ls`\n"), 0o644)
	os.WriteFile(filepath.Join(repo, ".agentgopher", ".env"), []byte("FROM_ENV=yes\n"), 0o644)

	script := `cat; echo "env=$FROM_ENV agent=$AGENT_ENV"; echo "RESULT <promise>COMPLETE</promise>"`
	res, err := Run(context.Background(), RunOptions{
		Agent:         echoAgent{script: script},
		Sandbox:       nosandbox.New(nil),
		Cwd:           repo,
		PromptFile:    filepath.Join(repo, ".agentgopher", "prompt.md"),
		PromptArgs:    map[string]string{"WHO": "world"},
		MaxIterations: 3,
		Name:          "worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.CompletionSignal != "<promise>COMPLETE</promise>" || len(res.Iterations) != 1 || res.Branch != "main" {
		t.Fatalf("res = %+v", res)
	}
	wantLog := filepath.Join(repo, ".agentgopher", "logs", "main-worker.log")
	if res.LogFilePath != wantLog {
		t.Fatalf("LogFilePath = %q, want %q", res.LogFilePath, wantLog)
	}
	logBytes, err := os.ReadFile(wantLog)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBytes)
	for _, want := range []string{"--- Run started: ", "agentgopher Run\n  Agent: worker\n  Sandbox: no-sandbox\n  Max iterations: 3\n  Branch: main", "Hello world on main", "Files: README.md", "env=yes agent=1", "Run complete: agent finished after 1 iteration(s)."} {
		if !strings.Contains(log, want) {
			t.Fatalf("missing %q in log:\n%s", want, log)
		}
	}
}

func TestRunInlinePromptIsVerbatim(t *testing.T) {
	repo := testrepo.Init(t)
	res, err := Run(context.Background(), RunOptions{
		Agent:   echoAgent{script: `cat; echo; echo "RESULT <promise>COMPLETE</promise>"`},
		Sandbox: nosandbox.New(nil),
		Cwd:     repo,
		Prompt:  "keep {{THIS}} and !`this`",
	})
	if err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(res.LogFilePath)
	if !strings.Contains(string(log), "keep {{THIS}} and !`this`") {
		t.Fatalf("log:\n%s", log)
	}
}

func TestRunValidation(t *testing.T) {
	repo := testrepo.Init(t)
	base := RunOptions{Agent: echoAgent{script: "true"}, Sandbox: nosandbox.New(nil), Cwd: repo, Prompt: "x"}

	o := base
	o.PromptArgs = map[string]string{"A": "1"}
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "PromptArgs is only supported with PromptFile") {
		t.Fatalf("inline+args: %v", err)
	}

	o = base
	o.BranchStrategy = &sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead}
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("strategy: %v", err)
	}

	o = base
	o.Cwd = filepath.Join(repo, "missing")
	var cwdErr *CwdError
	if _, err := Run(context.Background(), o); !errors.As(err, &cwdErr) {
		t.Fatalf("cwd: %v", err)
	}

	o = base
	o.Prompt = ""
	var perr *PromptError
	if _, err := Run(context.Background(), o); !errors.As(err, &perr) {
		t.Fatalf("no prompt: %v", err)
	}
}

func TestRunRejectsBuiltInOverrideAndMissingArg(t *testing.T) {
	repo := testrepo.Init(t)
	pf := filepath.Join(repo, "p.md")
	os.WriteFile(pf, []byte("{{NEEDED}}"), 0o644)
	base := RunOptions{Agent: echoAgent{script: "true"}, Sandbox: nosandbox.New(nil), Cwd: repo, PromptFile: pf}

	o := base
	o.PromptArgs = map[string]string{"TARGET_BRANCH": "x", "NEEDED": "y"}
	var perr *PromptError
	if _, err := Run(context.Background(), o); !errors.As(err, &perr) || !strings.Contains(perr.Message, "built-in") {
		t.Fatalf("override: %v", err)
	}

	o = base
	if _, err := Run(context.Background(), o); !errors.As(err, &perr) || !strings.Contains(perr.Message, "{{NEEDED}}") {
		t.Fatalf("missing: %v", err)
	}
}

func TestRunAlreadyCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, RunOptions{Agent: echoAgent{script: "true"}, Sandbox: nosandbox.New(nil), Cwd: testrepo.Init(t), Prompt: "x"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunStdoutLoggingAndStreamEvents(t *testing.T) {
	repo := testrepo.Init(t)
	var events []StreamEvent
	res, err := Run(context.Background(), RunOptions{
		Agent:   echoAgent{script: `echo "RESULT <promise>COMPLETE</promise>"`},
		Sandbox: nosandbox.New(nil),
		Cwd:     repo,
		Prompt:  "x",
		Logging: &Logging{Type: LoggingFile, Path: filepath.Join(repo, "custom.log"), OnAgentStreamEvent: func(e StreamEvent) { events = append(events, e) }},
	})
	if err != nil || res.LogFilePath != filepath.Join(repo, "custom.log") {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if len(events) == 0 {
		t.Fatal("expected stream events")
	}

	res, err = Run(context.Background(), RunOptions{
		Agent:   echoAgent{script: `echo "RESULT <promise>COMPLETE</promise>"`},
		Sandbox: nosandbox.New(nil),
		Cwd:     repo,
		Prompt:  "x",
		Logging: &Logging{Type: LoggingStdout},
	})
	if err != nil || res.LogFilePath != "" {
		t.Fatalf("stdout mode: res = %+v, %v", res, err)
	}
}

func TestBuildLogFilename(t *testing.T) {
	cases := []struct{ branch, target, name, want string }{
		{"feat/x", "", "", "feat-x.log"},
		{"tmp-1", "main", "", "main-tmp-1.log"},
		{"main", "", "My Worker!", "main-my-worker-.log"},
	}
	for _, c := range cases {
		if got := BuildLogFilename(c.branch, c.target, c.name); got != c.want {
			t.Errorf("BuildLogFilename(%q,%q,%q) = %q, want %q", c.branch, c.target, c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./`
Expected: FAIL, "undefined: Run".

- [ ] **Step 3: Write agentgopher.go**

```go
package agentgopher

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/env"
	"github.com/e11en/agentgopher/internal/gitx"
	"github.com/e11en/agentgopher/internal/lifecycle"
	"github.com/e11en/agentgopher/internal/orchestrator"
	"github.com/e11en/agentgopher/internal/prompt"
	"github.com/e11en/agentgopher/sandbox"
)

// DefaultMaxIterations is used when RunOptions.MaxIterations is zero.
const DefaultMaxIterations = 1

type (
	// HostHook runs on the host. Timeout zero means 60 s.
	HostHook = lifecycle.HostHook
	// SandboxHook runs inside the sandbox. Timeout zero means 60 s.
	SandboxHook = lifecycle.SandboxHook
	// Hooks groups lifecycle hooks by location and moment.
	Hooks = lifecycle.Hooks
	// Timeouts override built-in step timeouts. Zero keeps the default.
	Timeouts = lifecycle.Timeouts
	// StreamEvent is one item of the agent's output stream.
	StreamEvent = orchestrator.StreamEvent
	// IterationResult is the outcome of one agent invocation.
	IterationResult = orchestrator.IterationResult
)

// LoggingType selects where progress is written.
type LoggingType string

const (
	// LoggingFile writes progress and agent output to a log file.
	LoggingFile LoggingType = "file"
	// LoggingStdout writes progress and agent output to stdout.
	LoggingStdout LoggingType = "stdout"
)

// Logging configures where progress is written.
type Logging struct {
	Type LoggingType
	// Path of the log file in file mode. Default: .agentgopher/logs/<branch>[-<name>].log.
	Path string
	// OnAgentStreamEvent receives every stream event (file mode only).
	OnAgentStreamEvent func(StreamEvent)
	// Verbose also writes every raw stdout line of the agent, in real time.
	Verbose bool
}

// RunOptions configure Run.
type RunOptions struct {
	Agent   agent.Provider
	Sandbox sandbox.NoSandboxProvider
	// Cwd is the host repo directory. Relative paths resolve against the
	// process working directory. Default: the process working directory.
	Cwd string
	// Prompt is an inline prompt, delivered verbatim. Mutually exclusive with PromptFile.
	Prompt string
	// PromptFile is a prompt template with {{KEY}} and !`cmd` support.
	PromptFile string
	// MaxIterations defaults to DefaultMaxIterations.
	MaxIterations int
	Hooks         Hooks
	// PromptArgs fill {{KEY}} placeholders in PromptFile.
	PromptArgs map[string]string
	// Logging defaults to file mode under .agentgopher/logs/.
	Logging *Logging
	// CompletionSignal substrings stop the loop early. Default: <promise>COMPLETE</promise>.
	CompletionSignal []string
	// IdleTimeout fails the run when the agent is silent this long. Default: 10 min.
	IdleTimeout time.Duration
	// CompletionTimeout is the grace window after the completion signal. Default: 60 s.
	CompletionTimeout time.Duration
	// Name prefixes status messages and the log filename.
	Name string
	// BranchStrategy defaults to head. Only head is supported in this phase.
	BranchStrategy *sandbox.BranchStrategy
	Timeouts       Timeouts
}

// Commit identifies a commit the agent made.
type Commit struct{ SHA string }

// RunResult is the outcome of Run.
type RunResult struct {
	Iterations       []IterationResult
	CompletionSignal string
	Stdout           string
	Commits          []Commit
	Branch           string
	// LogFilePath is set in file logging mode.
	LogFilePath string
}

var invalidFilenameChars = regexp.MustCompile(`[/\\:*?"<>|]`)

// SanitizeBranchForFilename replaces path-unsafe characters with dashes.
func SanitizeBranchForFilename(branch string) string {
	return invalidFilenameChars.ReplaceAllString(branch, "-")
}

var nonFilenameRunes = regexp.MustCompile(`[^a-z0-9_.-]`)

// BuildLogFilename returns "<target>-<branch>[-<name>].log", or
// "<branch>[-<name>].log" when targetBranch is empty.
func BuildLogFilename(resolvedBranch, targetBranch, name string) string {
	base := SanitizeBranchForFilename(resolvedBranch)
	if name != "" {
		base += "-" + nonFilenameRunes.ReplaceAllString(strings.ToLower(name), "-")
	}
	if targetBranch != "" {
		return SanitizeBranchForFilename(targetBranch) + "-" + base + ".log"
	}
	return base + ".log"
}

func resolveCwd(cwd string) (string, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", &CwdError{Cwd: cwd, Message: err.Error()}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", &CwdError{Cwd: abs, Message: "cwd does not exist: " + abs}
	}
	if !info.IsDir() {
		return "", &CwdError{Cwd: abs, Message: "cwd is not a directory: " + abs}
	}
	return abs, nil
}

// Run drives the agent for up to MaxIterations iterations and returns what it produced.
func Run(ctx context.Context, o RunOptions) (RunResult, error) {
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}
	maxIterations := o.MaxIterations
	if maxIterations == 0 {
		maxIterations = DefaultMaxIterations
	}
	if o.BranchStrategy != nil && o.BranchStrategy.Type != sandbox.BranchHead {
		return RunResult{}, fmt.Errorf("branch strategy %q is not supported yet; only head is available in this phase", o.BranchStrategy.Type)
	}

	hostRepoDir, err := resolveCwd(o.Cwd)
	if err != nil {
		return RunResult{}, err
	}

	resolved, err := prompt.Resolve(o.Prompt, o.PromptFile)
	if err != nil {
		return RunResult{}, err
	}
	isInline := resolved.Source == prompt.SourceInline

	resolvedEnv, err := env.Resolve(hostRepoDir)
	if err != nil {
		return RunResult{}, err
	}
	merged, err := env.MergeProviderEnv(resolvedEnv, o.Agent.Env(), o.Sandbox.Env())
	if err != nil {
		return RunResult{}, err
	}

	currentBranch, err := gitx.CurrentBranch(ctx, hostRepoDir)
	if err != nil {
		return RunResult{}, err
	}

	logging := Logging{Type: LoggingFile}
	if o.Logging != nil {
		logging = *o.Logging
	}
	if logging.Type == LoggingFile && logging.Path == "" {
		logging.Path = filepath.Join(hostRepoDir, ".agentgopher", "logs", BuildLogFilename(currentBranch, "", o.Name))
	}

	var out io.Writer = os.Stdout
	logFilePath := ""
	if logging.Type == LoggingFile {
		f, err := display.OpenLogFile(logging.Path)
		if err != nil {
			return RunResult{}, err
		}
		defer f.Close()
		out = f
		logFilePath = logging.Path
		printFileStartup(o.Name, currentBranch, logging.Path, hostRepoDir)
	}
	d := display.NewWriter(out)

	d.Intro(orDefaultName(o.Name))
	d.Summary("agentgopher Run", [][2]string{
		{"Agent", orDefault(o.Name, o.Agent.Name())},
		{"Sandbox", o.Sandbox.Name()},
		{"Max iterations", fmt.Sprint(maxIterations)},
		{"Branch", currentBranch},
	})

	userArgs := prompt.Args(o.PromptArgs)
	resolvedPrompt := resolved.Text
	if isInline {
		if err := prompt.ValidateNoArgsWithInlinePrompt(userArgs); err != nil {
			return RunResult{}, logError(d, err)
		}
	} else {
		if err := prompt.ValidateNoBuiltInOverride(userArgs); err != nil {
			return RunResult{}, logError(d, err)
		}
		args := prompt.Args{"SOURCE_BRANCH": currentBranch, "TARGET_BRANCH": currentBranch}
		for k, v := range userArgs {
			args[k] = v
		}
		silent := map[string]bool{}
		for _, k := range prompt.BuiltInKeys {
			silent[k] = true
		}
		resolvedPrompt, err = prompt.Substitute(resolved.Text, args, silent, func(m string) { d.Status(m, display.SeverityWarn) })
		if err != nil {
			return RunResult{}, logError(d, err)
		}
	}

	result, err := orchestrator.Orchestrate(ctx, orchestrator.Options{
		HostRepoDir:         hostRepoDir,
		Iterations:          maxIterations,
		Hooks:               o.Hooks,
		Prompt:              resolvedPrompt,
		Branch:              currentBranch,
		Provider:            o.Agent,
		Sandbox:             o.Sandbox,
		Env:                 merged,
		CompletionSignals:   o.CompletionSignal,
		IdleTimeout:         o.IdleTimeout,
		CompletionTimeout:   o.CompletionTimeout,
		Name:                o.Name,
		SkipPromptExpansion: isInline,
		Timeouts:            o.Timeouts,
		Display:             d,
		OnStreamEvent:       buildStreamHandler(logging, out),
	})
	if err != nil {
		if ctx.Err() != nil {
			return RunResult{}, ctx.Err()
		}
		return RunResult{}, logError(d, err)
	}

	if result.CompletionSignal != "" {
		d.Status(fmt.Sprintf("Run complete: agent finished after %d iteration(s).", len(result.Iterations)), display.SeveritySuccess)
	} else {
		d.Status(fmt.Sprintf("Run complete: reached %d iteration(s) without completion signal.", len(result.Iterations)), display.SeverityWarn)
	}
	for _, it := range result.Iterations {
		if it.Usage != nil {
			total := it.Usage.InputTokens + it.Usage.CacheCreationInputTokens + it.Usage.CacheReadInputTokens
			d.Text(fmt.Sprintf("Context window: %dk", (total+999)/1000))
		}
	}

	commits := make([]Commit, len(result.Commits))
	for i, sha := range result.Commits {
		commits[i] = Commit{SHA: sha}
	}
	return RunResult{
		Iterations:       result.Iterations,
		CompletionSignal: result.CompletionSignal,
		Stdout:           result.Stdout,
		Commits:          commits,
		Branch:           result.Branch,
		LogFilePath:      logFilePath,
	}, nil
}

// buildStreamHandler composes the user's callback (file mode only) with the
// verbose raw-line sink. Returns nil when neither is configured.
func buildStreamHandler(logging Logging, out io.Writer) func(StreamEvent) {
	var user func(StreamEvent)
	if logging.Type == LoggingFile {
		user = logging.OnAgentStreamEvent
	}
	if user == nil && !logging.Verbose {
		return nil
	}
	return func(ev StreamEvent) {
		if user != nil {
			user(ev)
		}
		if logging.Verbose && ev.Type == orchestrator.StreamRaw {
			_, _ = io.WriteString(out, ev.Line+"\n")
		}
	}
}

// logError writes err to the display so a file log records why the run
// stopped, then returns it unchanged.
func logError(d display.Display, err error) error {
	d.Status(err.Error(), display.SeverityError)
	return err
}

func printFileStartup(name, branch, logPath, hostRepoDir string) {
	if name == "" {
		name = "Agent"
	}
	shown := logPath
	if wd, err := os.Getwd(); err == nil && wd == hostRepoDir {
		if rel, err := filepath.Rel(wd, logPath); err == nil {
			shown = rel
		}
	}
	fmt.Fprintf(os.Stdout, "[%s] Started on branch %s\n  tail -f %s\n", name, branch, shown)
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func orDefaultName(name string) string { return orDefault(name, "agentgopher") }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./...`
Expected: PASS for every package.

- [ ] **Step 5: Lint and commit**

Run: `go vet ./... && golangci-lint run ./...`
Expected: no output. Fix anything reported before committing.

```bash
git add agentgopher.go agentgopher_test.go
git commit -m "Add public Run with prompt arguments, logging and run summary"
```

---

### Task 15: Example, README and a real smoke run

**Files:**
- Create: `examples/simple/main.go`, `examples/simple/prompt.md`
- Modify: `README.md`

- [ ] **Step 1: Write the example**

`examples/simple/main.go`:
```go
// Command simple runs Claude Code once on the current repo, on the host.
// Usage: go run ./examples/simple [path-to-repo]
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/e11en/agentgopher"
	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
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
		Name:          "worker",
		Agent:         agent.ClaudeCode(agent.DefaultModel, nil),
		Sandbox:       nosandbox.New(nil),
		Cwd:           repo,
		PromptFile:    promptFile,
		MaxIterations: 1,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "run failed:", err)
		os.Exit(1)
	}
	fmt.Printf("done: signal=%q commits=%d log=%s\n", res.CompletionSignal, len(res.Commits), res.LogFilePath)
}
```

`examples/simple/prompt.md`:
```markdown
# Context

You are working on branch {{SOURCE_BRANCH}}.

Recent commits:

!`git log --oneline -5`

# Task

Add one line to README.md that says the project was touched by agentgopher, commit it with the message "Touched by agentgopher", then output the completion signal:

<promise>COMPLETE</promise>
```

- [ ] **Step 2: Verify it builds**

Run: `go build ./... && go vet ./...`
Expected: no output.

- [ ] **Step 3: Smoke run against the real Claude Code CLI (manual, skipped in CI)**

Only when `claude` is installed and logged in on the machine:
```bash
tmp=$(mktemp -d) && git -C "$tmp" init -q -b main && echo "# demo" > "$tmp/README.md" && git -C "$tmp" add . && git -C "$tmp" commit -q -m init
go run ./examples/simple "$tmp"
git -C "$tmp" log --oneline
```
Expected: a second commit "Touched by agentgopher" and `done: signal="<promise>COMPLETE</promise>" commits=1`. Record the result (or that it was skipped) in the commit message of Step 4.

- [ ] **Step 4: Update README and commit**

Replace the "Status" line in `README.md` with:
```markdown
Status: phases 1 and 2 of `docs/plan.md` are done. `Run` works on the host with
the head branch strategy. Docker, Podman, worktrees and isolated providers follow
in the next phases.

Run the example: `go run ./examples/simple /path/to/repo`. Progress lands in
`.agentgopher/logs/main-worker.log` inside that repo.
```

```bash
git add examples/ README.md
git commit -m "Add host example and document phase 2 status"
```

- [ ] **Step 5: Push and confirm CI**

```bash
git push origin main
gh run watch --repo e11en/agentgopher --exit-status
```
Expected: the `ci` workflow passes.

---

## Self-review notes

- Spec coverage for phases 1 and 2: skeleton and CI (Task 1), core loop (Tasks 12 to 14), prompt handling (5, 8), run log and stream events (9, 13, 14), no-sandbox provider (4), ClaudeCode agent (6), ADRs: the plan references ADR 0019 (completion timeout) and 0020 (fail-fast expansion) in code comments; copying `docs/adr/` from upstream is a documentation step without code and is left to the phase 6 plan together with the init templates.
- Deferred on purpose: merge-to-head and named branches (phase 3 plan), isolated providers (phase 4), `CreateSandbox`, `Interactive`, structured output, resume and fork, session store, spinner terminal UI (phase 5), CLI and init (phase 6), Kubernetes and GKE (phase 7).
- Type consistency: `sandbox.ExecOptions.Stdin` is `*string` everywhere; `agent.StreamEvent.Text` carries both text and result payloads; `lifecycle.Hooks` is the single hooks type reused by the root package.
