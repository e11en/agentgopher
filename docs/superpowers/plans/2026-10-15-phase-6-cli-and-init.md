# agentgopher Phase 6: CLI and Init Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `agentgopher` binary with `init`, `docker build-image`, `docker remove-image`, `podman build-image` and `podman remove-image`, where `init` scaffolds a self-contained `.agentgopher/` Go program from embedded templates, with registries for agents, sandbox providers and issue trackers including Jira and Windshift.

**Architecture:** `cmd/agentgopher` holds one cobra command per file and nothing else. All logic lives in `internal/initcmd`: registries (`registry.go`, `dockerfiles.go`), the embedded templates (`templates/`), the scaffold (`scaffold.go`), next steps (`nextsteps.go`) and the interactive flow with a `Prompter` interface (`run.go`). Image build and removal are functions in `sandbox/docker` and `sandbox/podman` that shell out to the CLI. Upstream's `IssueTrackerEntry` gains `Questions`, so trackers whose commands depend on per-workspace names (Jira JQL and done status, Windshift ready and done status) ask for them during init and substitute the answers into their template arguments before the prompt files are written.

**Tech Stack:** Go 1.25, standard library plus `github.com/spf13/cobra` for the CLI. Templates embedded with `embed.FS`. CI as in phase 1.

**Spec:** `docs/plan.md` (sections "Feature inventory and Go mapping" rows for CLI, templates and registries; "Issue trackers"; "Go design decisions" item "Templates in non-Go repos"; phase 6). Upstream reference: `mattpocock/sandcastle` at commit `7a4e7f7`, files `src/cli.ts`, `src/main.ts`, `src/InitService.ts`, `src/templates.ts`, `src/templates/**`, `src/DockerLifecycle.ts`, `src/PodmanLifecycle.ts`, `src/version.ts`, `docs/agents/adding-an-issue-tracker.md`, `docs/adr/0009-templates-no-shared-code.md`, `docs/adr/0014-docker-uid-alignment-via-build-arg.md`. Clone it when a task is unclear: `git clone --depth 1 https://github.com/mattpocock/sandcastle.git /tmp/sandcastle && git -C /tmp/sandcastle checkout 7a4e7f7`.

## Global Constraints

- Module path `github.com/e11en/agentgopher`, `go 1.25` in `go.mod`.
- Standard library plus `github.com/spf13/cobra` (CLI only). Nothing else.
- Config directory is `.agentgopher/`. Its `.gitignore` lists `.env`, `logs/`, `worktrees/`.
- Templates are self-contained (ADR 0009): each `internal/initcmd/templates/<name>/` directory imports only the public packages of `github.com/e11en/agentgopher`, never `internal/` and never another template. They are real Go packages, so `go build ./...` compiles them in CI.
- The scaffolded `.agentgopher/` is its own Go module (`go.mod` written by init) so `go run -C .agentgopher .` works in any repo, Go project or not.
- Public API additions in this plan: `sandbox/docker.BuildImage`, `sandbox/docker.RemoveImage`, `sandbox/podman.BuildImage`, `sandbox/podman.RemoveImage`. Everything else under `internal/` or `cmd/`.
- The GitHub label is `agentgopher` (upstream: `Sandcastle`). The closing comment is `Completed by agentgopher`.
- Code comments, test names and commit messages in English. No em or en dashes anywhere (use ` - ` or commas).
- Commit messages: imperative, no attribution footer, no `Co-Authored-By`.
- Tests must not require Docker, Podman, `gh`, `acli`, `ws` or network. Shell-outs go through a package-level `execCommand` variable that tests replace.
- Every prompt the interactive flow asks has a flag that pre-answers it; with no TTY and no flag, init fails naming the flag.

## Review Focus

1. Running `init` twice: the second run fails with `.agentgopher/ directory already exists` and leaves the first scaffold untouched. Test added in Task 5.
2. A tracker answer containing a double quote or `$` (a JQL with `"To Do"`): it lands in the prompt file verbatim, inside single quotes in the shell command, and is not expanded by `sh`. Test added in Task 5.
3. `--agent pi --sandbox podman` with the parallel-planner template: every `agent.ClaudeCode(` call and every `docker` reference in `main.go` is rewritten, so the scaffold compiles. Test added in Task 5.
4. `init --issue-tracker custom --build-image true`: the build flag is ignored and no `docker build` runs, because the Dockerfile still has the TODO block. Test added in Task 8.
5. `docker build-image` from a repo without `.agentgopher/`: fails with `No .agentgopher/ found. Run agentgopher init first.` before touching Docker. Test added in Task 9.

---

## File structure

```
cmd/agentgopher/main.go                     root cobra command, version flag
cmd/agentgopher/init.go                     init command and its flags
cmd/agentgopher/image.go                    docker and podman build-image, remove-image
cmd/agentgopher/main_test.go
internal/version/version.go                 Version variable, set by ldflags
internal/initcmd/registry.go                AgentEntry, SandboxProviderEntry, TemplateEntry, IssueTrackerEntry, Question, registries
internal/initcmd/registry_test.go
internal/initcmd/dockerfiles.go             Dockerfile template per agent, tracker tool snippets
internal/initcmd/templates.go               embed.FS and ListTemplates
internal/initcmd/templates/<name>/          main.go, go.mod.tmpl, template.json, prompt files
internal/initcmd/scaffold.go                Scaffold
internal/initcmd/scaffold_test.go
internal/initcmd/nextsteps.go               NextStepsLines
internal/initcmd/nextsteps_test.go
internal/initcmd/prompter.go                Prompter interface, stdin implementation
internal/initcmd/run.go                     Run: resolve choices, scaffold, label, build, next steps
internal/initcmd/run_test.go
sandbox/docker/image.go                     BuildImage, RemoveImage
sandbox/docker/image_test.go
sandbox/podman/image.go                     BuildImage, RemoveImage
sandbox/podman/image_test.go
```

Upstream behaviour not ported: `detectMainFilename` (`.ts` vs `.mts`), `detectPackageManager`, `hostHasDependency`, `addDependencyCommand` and the zod install offer. The Go templates need no host dependency (structured output uses `encoding/json`), so none of that has a Go counterpart. Upstream at `7a4e7f7` writes no CLAUDE.md or AGENTS.md during init; nothing to port there.

---

### Task 1: Version package and cobra root command

**Files:**
- Create: `internal/version/version.go`, `cmd/agentgopher/main.go`, `cmd/agentgopher/main_test.go`
- Modify: `go.mod` (cobra dependency), `.github/workflows/ci.yml` (build the binary)

**Interfaces:**
- Produces: `version.Version string` (default `"dev"`, overridden with `-ldflags "-X github.com/e11en/agentgopher/internal/version.Version=v1.2.3"`); `newRootCmd() *cobra.Command` in package `main` with `SilenceUsage` and `SilenceErrors` set, so errors print once as `error: <message>` and exit 1. Later tasks add subcommands with `root.AddCommand(...)` from their own file's `init()` function via a package-level `var subcommands []*cobra.Command`.

- [ ] **Step 1: Add cobra**

```bash
cd /path/to/agentgopher
go get github.com/spf13/cobra@v1.9.1
```

- [ ] **Step 2: Write the failing test**

`cmd/agentgopher/main_test.go`:
```go
package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/version"
)

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestRootPrintsVersion(t *testing.T) {
	version.Version = "v9.9.9"
	out, err := runCLI(t, "--version")
	if err != nil || !strings.Contains(out, "agentgopher version v9.9.9") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestRootWithoutArgsShowsHint(t *testing.T) {
	out, err := runCLI(t)
	if err != nil || !strings.Contains(out, "Use --help to see available commands.") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./cmd/agentgopher/`
Expected: FAIL, "undefined: newRootCmd".

- [ ] **Step 4: Write version.go and main.go**

`internal/version/version.go`:
```go
// Package version holds the build version of the agentgopher binary.
package version

// Version is set at build time with -ldflags "-X .../internal/version.Version=v1.2.3".
var Version = "dev"
```

`cmd/agentgopher/main.go`:
```go
// Command agentgopher scaffolds and manages agentgopher config directories.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/e11en/agentgopher/internal/version"
)

// configDir is the per-repo config directory that init creates.
const configDir = ".agentgopher"

// subcommands is filled by the init() functions of the other files in this package.
var subcommands []*cobra.Command

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "agentgopher",
		Short:         "Orchestrate sandboxed coding agents",
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "agentgopher %s\nUse --help to see available commands.\n", version.Version)
			return nil
		},
	}
	root.SetVersionTemplate("agentgopher version {{.Version}}\n")
	root.AddCommand(subcommands...)
	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./cmd/agentgopher/`
Expected: PASS.

- [ ] **Step 6: Build the binary in CI**

In `.github/workflows/ci.yml`, after the `go test -race ./...` step add:
```yaml
      - run: go build -o /tmp/agentgopher ./cmd/agentgopher && /tmp/agentgopher --version
```

- [ ] **Step 7: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/version/ cmd/agentgopher/ .github/workflows/ci.yml
git commit -m "Add agentgopher binary with cobra root command"
```

---

### Task 2: Agent, sandbox provider and template registries

Port of `AGENT_REGISTRY`, `SANDBOX_PROVIDER_REGISTRY` and `TEMPLATES` from `InitService.ts` and `templates.ts`. The Dockerfile text per agent lives in Task 3's `dockerfiles.go`; this task references the constants by name, so write Task 3's constants first if the compiler complains, or stub them as empty strings and fill them in Task 3.

**Files:**
- Create: `internal/initcmd/registry.go`, `internal/initcmd/registry_test.go`, `internal/initcmd/templates.go`, `internal/initcmd/templates/<name>/template.json` for the five templates

**Interfaces:**
- Produces, in package `initcmd`:
  ```go
  const SetupIssueTrackerDoc = "SETUP_ISSUE_TRACKER.md"
  const SetupIssueTrackerPath = ".agentgopher/" + SetupIssueTrackerDoc
  type AgentEntry struct {
      Name, Label, DefaultModel string
      Factory      string // Go constructor in package agent, e.g. "ClaudeCode"
      Dockerfile   string // template with {{ISSUE_TRACKER_TOOLS}}
      EnvExample   string
      SetupCommand string // host command that feeds SETUP_ISSUE_TRACKER.md to the agent
  }
  type SandboxProviderEntry struct { Name, Label, ContainerfileName, CLINamespace string }
  type TemplateEntry struct { Name, Description string }
  type Question struct { Key, Prompt, Default string } // answer substitutes {{Key}} inside TemplateArgs values
  type IssueTrackerEntry struct {
      Name, Label  string
      TemplateArgs map[string]string // LIST_TASKS_COMMAND, VIEW_TASK_COMMAND, CLOSE_TASK_COMMAND, ISSUE_TRACKER_TOOLS, ISSUE_TRACKER_LOGIN_COMMAND
      EnvExample   string
      Questions    []Question
  }
  func ListAgents() []AgentEntry;                 func GetAgent(name string) (AgentEntry, bool)
  func ListSandboxProviders() []SandboxProviderEntry; func GetSandboxProvider(name string) (SandboxProviderEntry, bool)
  func ListTemplates() []TemplateEntry;           func GetTemplate(name string) (TemplateEntry, bool)
  func ListIssueTrackers() []IssueTrackerEntry;   func GetIssueTracker(name string) (IssueTrackerEntry, bool)
  var templatesFS embed.FS // //go:embed all:templates
  ```
- `ListTemplates` reads each `templates/<name>/template.json` from the embedded FS, in the fixed order blank, simple-loop, sequential-reviewer, parallel-planner, parallel-planner-with-review.

- [ ] **Step 1: Write the failing tests**

`internal/initcmd/registry_test.go`:
```go
package initcmd

import (
	"strings"
	"testing"
)

func TestAgentRegistry(t *testing.T) {
	cases := []struct{ name, label, model, factory, envKey string }{
		{"claude-code", "Claude Code", "claude-opus-4-8", "ClaudeCode", "CLAUDE_CODE_OAUTH_TOKEN"},
		{"pi", "Pi", "claude-sonnet-4-6", "Pi", "ANTHROPIC_API_KEY"},
		{"codex", "Codex", "gpt-5.4", "Codex", "OPENAI_KEY"},
		{"cursor", "Cursor", "composer-2", "Cursor", "CURSOR_API_KEY"},
		{"opencode", "OpenCode", "opencode/big-pickle", "OpenCode", "OPENCODE_API_KEY"},
		{"copilot", "GitHub Copilot CLI", "claude-sonnet-4.5", "Copilot", "GITHUB_TOKEN"},
	}
	if len(ListAgents()) != len(cases) {
		t.Fatalf("got %d agents", len(ListAgents()))
	}
	for _, c := range cases {
		a, ok := GetAgent(c.name)
		if !ok || a.Label != c.label || a.DefaultModel != c.model || a.Factory != c.factory {
			t.Fatalf("%s: got %+v ok=%v", c.name, a, ok)
		}
		if !strings.Contains(a.EnvExample, c.envKey+"=") {
			t.Fatalf("%s: EnvExample lacks %s", c.name, c.envKey)
		}
		if !strings.Contains(a.Dockerfile, "{{ISSUE_TRACKER_TOOLS}}") || !strings.Contains(a.Dockerfile, "ARG AGENT_UID=1000") {
			t.Fatalf("%s: Dockerfile lacks tracker placeholder or UID arg", c.name)
		}
		if !strings.Contains(a.SetupCommand, SetupIssueTrackerPath) {
			t.Fatalf("%s: SetupCommand = %q", c.name, a.SetupCommand)
		}
	}
	if _, ok := GetAgent("nope"); ok {
		t.Fatal("unknown agent must not resolve")
	}
}

func TestSandboxProviderRegistry(t *testing.T) {
	ps := ListSandboxProviders()
	if len(ps) != 2 || ps[0].Name != "docker" || ps[1].Name != "podman" {
		t.Fatalf("got %+v", ps)
	}
	d, _ := GetSandboxProvider("docker")
	p, _ := GetSandboxProvider("podman")
	if d.ContainerfileName != "Dockerfile" || d.CLINamespace != "docker" || p.ContainerfileName != "Containerfile" || p.CLINamespace != "podman" {
		t.Fatalf("docker=%+v podman=%+v", d, p)
	}
	if _, ok := GetSandboxProvider("nope"); ok {
		t.Fatal("unknown provider must not resolve")
	}
}

func TestTemplateRegistryFromEmbeddedJSON(t *testing.T) {
	want := []string{"blank", "simple-loop", "sequential-reviewer", "parallel-planner", "parallel-planner-with-review"}
	got := ListTemplates()
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, name := range want {
		if got[i].Name != name || got[i].Description == "" {
			t.Fatalf("template %d = %+v, want %s", i, got[i], name)
		}
	}
	if _, ok := GetTemplate("nope"); ok {
		t.Fatal("unknown template must not resolve")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/initcmd/`
Expected: FAIL, "undefined: ListAgents".

- [ ] **Step 3: Write the template.json files**

Create these five files (the directories get their code in Task 4):

`internal/initcmd/templates/blank/template.json`:
```json
{"name": "blank", "description": "Bare scaffold - write your own prompt and orchestration"}
```
`internal/initcmd/templates/simple-loop/template.json`:
```json
{"name": "simple-loop", "description": "Picks issues one by one and closes them"}
```
`internal/initcmd/templates/sequential-reviewer/template.json`:
```json
{"name": "sequential-reviewer", "description": "Implements issues one by one, with a code review step after each"}
```
`internal/initcmd/templates/parallel-planner/template.json`:
```json
{"name": "parallel-planner", "description": "Plans parallelizable issues, executes on separate branches, merges"}
```
`internal/initcmd/templates/parallel-planner-with-review/template.json`:
```json
{"name": "parallel-planner-with-review", "description": "Plans parallelizable issues, executes with per-branch review, merges"}
```

- [ ] **Step 4: Write templates.go**

```go
package initcmd

import (
	"embed"
	"encoding/json"
)

// templatesFS holds every template directory verbatim. "all:" keeps dot
// files such as .env.example.
//
//go:embed all:templates
var templatesFS embed.FS

// templateOrder is the order init shows templates in.
var templateOrder = []string{"blank", "simple-loop", "sequential-reviewer", "parallel-planner", "parallel-planner-with-review"}

// ListTemplates returns the embedded templates in display order.
func ListTemplates() []TemplateEntry {
	out := make([]TemplateEntry, 0, len(templateOrder))
	for _, name := range templateOrder {
		b, err := templatesFS.ReadFile("templates/" + name + "/template.json")
		if err != nil {
			panic("template.json missing for " + name + ": " + err.Error())
		}
		var e TemplateEntry
		if err := json.Unmarshal(b, &e); err != nil {
			panic("template.json invalid for " + name + ": " + err.Error())
		}
		out = append(out, e)
	}
	return out
}

// GetTemplate returns the template with the given name.
func GetTemplate(name string) (TemplateEntry, bool) {
	for _, t := range ListTemplates() {
		if t.Name == name {
			return t, true
		}
	}
	return TemplateEntry{}, false
}
```

- [ ] **Step 5: Write registry.go**

```go
// Package initcmd scaffolds the .agentgopher config directory.
package initcmd

// SetupIssueTrackerDoc is the setup prompt written for the custom issue tracker.
const SetupIssueTrackerDoc = "SETUP_ISSUE_TRACKER.md"

// SetupIssueTrackerPath is the doc's path relative to the repo root.
const SetupIssueTrackerPath = ".agentgopher/" + SetupIssueTrackerDoc

// AgentEntry describes an agent init can scaffold.
type AgentEntry struct {
	Name         string
	Label        string
	DefaultModel string
	// Factory is the constructor in package agent, e.g. "ClaudeCode".
	Factory string
	// Dockerfile is the container file template with {{ISSUE_TRACKER_TOOLS}}.
	Dockerfile string
	EnvExample string
	// SetupCommand feeds SETUP_ISSUE_TRACKER.md to this agent on the host.
	SetupCommand string
}

// SandboxProviderEntry describes a container provider init can scaffold.
type SandboxProviderEntry struct {
	Name              string
	Label             string
	ContainerfileName string
	CLINamespace      string
}

// TemplateEntry is the metadata in a template's template.json.
type TemplateEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Question is asked during init; the answer replaces {{Key}} in the
// tracker's TemplateArgs values.
type Question struct {
	Key     string
	Prompt  string
	Default string
}

// IssueTrackerEntry describes a task source init can scaffold.
type IssueTrackerEntry struct {
	Name  string
	Label string
	// TemplateArgs are substituted into the scaffolded text files. Keys:
	// LIST_TASKS_COMMAND, VIEW_TASK_COMMAND, CLOSE_TASK_COMMAND,
	// ISSUE_TRACKER_TOOLS and the optional ISSUE_TRACKER_LOGIN_COMMAND, which
	// becomes an onSandboxReady hook in main.go.
	TemplateArgs map[string]string
	EnvExample   string
	Questions    []Question
}

var agentRegistry = []AgentEntry{
	{
		Name: "claude-code", Label: "Claude Code", DefaultModel: "claude-opus-4-8", Factory: "ClaudeCode",
		Dockerfile: claudeCodeDockerfile,
		EnvExample: "# Claude Code OAuth token - get one by running `claude setup-token` on your host.\n" +
			"# Lets the agent use your Claude subscription instead of an API key.\n" +
			"CLAUDE_CODE_OAUTH_TOKEN=\n" +
			"# Or use an Anthropic API key instead - uncomment and fill in:\n" +
			"# ANTHROPIC_API_KEY=",
		SetupCommand: `claude "$(cat ` + SetupIssueTrackerPath + `)"`,
	},
	{
		Name: "pi", Label: "Pi", DefaultModel: "claude-sonnet-4-6", Factory: "Pi",
		Dockerfile:   piDockerfile,
		EnvExample:   "# Anthropic API key\nANTHROPIC_API_KEY=",
		SetupCommand: `pi "$(cat ` + SetupIssueTrackerPath + `)"`,
	},
	{
		Name: "codex", Label: "Codex", DefaultModel: "gpt-5.4", Factory: "Codex",
		Dockerfile:   codexDockerfile,
		EnvExample:   "# OpenAI API key\nOPENAI_KEY=",
		SetupCommand: `codex "$(cat ` + SetupIssueTrackerPath + `)"`,
	},
	{
		Name: "cursor", Label: "Cursor", DefaultModel: "composer-2", Factory: "Cursor",
		Dockerfile:   cursorDockerfile,
		EnvExample:   "# Cursor API key (recommended)\n# You can also pass --api-key directly to the agent CLI.\nCURSOR_API_KEY=",
		SetupCommand: `agent "$(cat ` + SetupIssueTrackerPath + `)"`,
	},
	{
		Name: "opencode", Label: "OpenCode", DefaultModel: "opencode/big-pickle", Factory: "OpenCode",
		Dockerfile:   opencodeDockerfile,
		EnvExample:   "# OpenCode API key\nOPENCODE_API_KEY=",
		SetupCommand: `opencode --prompt "$(cat ` + SetupIssueTrackerPath + `)"`,
	},
	{
		Name: "copilot", Label: "GitHub Copilot CLI", DefaultModel: "claude-sonnet-4.5", Factory: "Copilot",
		Dockerfile: copilotDockerfile,
		EnvExample: "# GitHub token with the \"Copilot Requests\" permission\n" +
			"# (a fine-grained PAT, or any token from `gh auth login`).\n" +
			"# COPILOT_GITHUB_TOKEN takes precedence over GH_TOKEN and GITHUB_TOKEN.\n" +
			"GITHUB_TOKEN=",
		SetupCommand: `copilot -i "$(cat ` + SetupIssueTrackerPath + `)"`,
	},
}

var sandboxProviderRegistry = []SandboxProviderEntry{
	{Name: "docker", Label: "Docker", ContainerfileName: "Dockerfile", CLINamespace: "docker"},
	{Name: "podman", Label: "Podman", ContainerfileName: "Containerfile", CLINamespace: "podman"},
}

// ListAgents returns every agent init offers, Claude Code first.
func ListAgents() []AgentEntry { return agentRegistry }

// GetAgent returns the agent with the given name.
func GetAgent(name string) (AgentEntry, bool) {
	for _, a := range agentRegistry {
		if a.Name == name {
			return a, true
		}
	}
	return AgentEntry{}, false
}

// ListSandboxProviders returns the container providers init offers.
func ListSandboxProviders() []SandboxProviderEntry { return sandboxProviderRegistry }

// GetSandboxProvider returns the provider with the given name.
func GetSandboxProvider(name string) (SandboxProviderEntry, bool) {
	for _, p := range sandboxProviderRegistry {
		if p.Name == name {
			return p, true
		}
	}
	return SandboxProviderEntry{}, false
}

// ListIssueTrackers returns every issue tracker init offers, GitHub Issues first.
func ListIssueTrackers() []IssueTrackerEntry { return issueTrackerRegistry }

// GetIssueTracker returns the tracker with the given name.
func GetIssueTracker(name string) (IssueTrackerEntry, bool) {
	for _, t := range issueTrackerRegistry {
		if t.Name == name {
			return t, true
		}
	}
	return IssueTrackerEntry{}, false
}
```

The constants `claudeCodeDockerfile` and the others, plus `issueTrackerRegistry`, come from Task 3. Until then add a temporary `internal/initcmd/dockerfiles.go` with `const claudeCodeDockerfile, piDockerfile, codexDockerfile, cursorDockerfile, opencodeDockerfile, copilotDockerfile = "{{ISSUE_TRACKER_TOOLS}}\nARG AGENT_UID=1000", ...` (all six the same string) and `var issueTrackerRegistry []IssueTrackerEntry`; Task 3 replaces that file.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/initcmd/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/initcmd/
git commit -m "Add init registries for agents, sandbox providers and templates"
```

---

### Task 3: Dockerfile templates and the issue tracker registry (GitHub, Beads, custom, Jira, Windshift)

Port of the Dockerfile constants and `ISSUE_TRACKER_REGISTRY` from `InitService.ts`, plus the two new trackers from `docs/plan.md`.

Verified against the vendor documentation on 2026-10-15:

- `acli jira workitem search [flags]`: `-j, --jql string`, `--json`, `-f, --fields string` (default `issuetype,key,assignee,priority,status,summary`), `-l, --limit int`, `--paginate`.
- `acli jira workitem view [key] [flags]`: the key is positional, `--json`, `-f, --fields string` (default `key,issuetype,summary,status,assignee,description`; `*navigable` and `-field` exclusion accepted).
- `acli jira workitem transition [flags]`: `-k, --key string`, `-s, --status string`, `-y, --yes`, `--json`.
- `acli jira auth login [flags]`: `-s, --site string`, `-e, --email string`, `--token` (reads the token from stdin, no env var exists).
- acli Debian install: keyring at `https://acli.atlassian.com/gpg/public-key.asc`, apt source `https://acli.atlassian.com/linux/deb stable main`, package `acli`.
- Windshift `ws` (repo `Windshiftapp/core`, `internal/wscli`): `ws task ls` with `-s, --status` (accepts a status name, ID, or `~status` to exclude), `--all`, `-w, --workspace`; `ws task get <id|KEY-123>`; `ws task move <id|KEY-123> <status>`; global `-o, --output` defaults to `json`; config from `WS_URL`, `WS_TOKEN`, `WS_WORKSPACE`. Release assets are named `ws-v<version>-linux-<amd64|arm64>.tar.gz` (latest checked: v0.8.9).

Still to confirm at implementation time, against the installed versions: whether `acli jira workitem view --json` prints the comments when `comment` is in `--fields`; whether `ws task ls -s` matches the status name case-insensitively; the exact file name inside the `ws` release tarball (the snippet below assumes a single `ws` binary at the top level).

**Files:**
- Create: `internal/initcmd/dockerfiles.go` (replacing the Task 2 stub)
- Modify: `internal/initcmd/registry_test.go` (append tests)

**Interfaces:**
- Produces: the six Dockerfile constants, `issueTrackerRegistry`, and the exported sentinels `CustomListTasksSentinel`, `CustomViewTaskMarker`, `CustomCloseTaskMarker`, `CustomTrackerTools`, used by the setup doc in Task 5.

- [ ] **Step 1: Write the failing tests (append to registry_test.go)**

```go
func TestIssueTrackerRegistryOrderAndNames(t *testing.T) {
	want := []string{"github-issues", "beads", "custom", "jira", "windshift"}
	got := ListIssueTrackers()
	if len(got) != len(want) {
		t.Fatalf("got %d trackers", len(got))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("tracker %d = %s, want %s", i, got[i].Name, name)
		}
		for _, key := range []string{"LIST_TASKS_COMMAND", "VIEW_TASK_COMMAND", "CLOSE_TASK_COMMAND", "ISSUE_TRACKER_TOOLS"} {
			if _, ok := got[i].TemplateArgs[key]; !ok {
				t.Fatalf("%s lacks %s", name, key)
			}
		}
	}
}

func TestGitHubIssuesEntry(t *testing.T) {
	gh, _ := GetIssueTracker("github-issues")
	if !strings.Contains(gh.TemplateArgs["LIST_TASKS_COMMAND"], "gh issue list --state open --label agentgopher --limit 100 --json") {
		t.Fatalf("LIST = %q", gh.TemplateArgs["LIST_TASKS_COMMAND"])
	}
	if gh.TemplateArgs["VIEW_TASK_COMMAND"] != "gh issue view <ID>" || gh.TemplateArgs["CLOSE_TASK_COMMAND"] != `gh issue close <ID> --comment "Completed by agentgopher"` {
		t.Fatalf("VIEW=%q CLOSE=%q", gh.TemplateArgs["VIEW_TASK_COMMAND"], gh.TemplateArgs["CLOSE_TASK_COMMAND"])
	}
	if !strings.Contains(gh.EnvExample, "GH_TOKEN=") || !strings.Contains(gh.TemplateArgs["ISSUE_TRACKER_TOOLS"], "apt-get install -y gh") {
		t.Fatal("GH_TOKEN or gh install missing")
	}
}

func TestBeadsAndCustomEntries(t *testing.T) {
	bd, _ := GetIssueTracker("beads")
	if bd.TemplateArgs["LIST_TASKS_COMMAND"] != "bd ready --json" || bd.TemplateArgs["VIEW_TASK_COMMAND"] != "bd show <ID>" || bd.EnvExample != "" {
		t.Fatalf("beads = %+v", bd)
	}
	custom, _ := GetIssueTracker("custom")
	if custom.TemplateArgs["LIST_TASKS_COMMAND"] != CustomListTasksSentinel || !strings.Contains(CustomListTasksSentinel, "exit 1") {
		t.Fatalf("custom list = %q", custom.TemplateArgs["LIST_TASKS_COMMAND"])
	}
	if !strings.Contains(custom.TemplateArgs["ISSUE_TRACKER_TOOLS"], "TODO") || !strings.Contains(custom.EnvExample, "TODO") {
		t.Fatal("custom tracker must scaffold TODO markers")
	}
}

func TestJiraEntry(t *testing.T) {
	j, ok := GetIssueTracker("jira")
	if !ok || j.Label != "Jira" {
		t.Fatalf("jira = %+v ok=%v", j, ok)
	}
	wantList := `acli jira workitem search --jql '{{JIRA_JQL}}' --json --paginate --fields key,summary,description,status,labels,priority`
	if j.TemplateArgs["LIST_TASKS_COMMAND"] != wantList {
		t.Fatalf("LIST = %q", j.TemplateArgs["LIST_TASKS_COMMAND"])
	}
	if j.TemplateArgs["VIEW_TASK_COMMAND"] != `acli jira workitem view <ID> --json --fields '*navigable,comment'` {
		t.Fatalf("VIEW = %q", j.TemplateArgs["VIEW_TASK_COMMAND"])
	}
	if j.TemplateArgs["CLOSE_TASK_COMMAND"] != `acli jira workitem transition --key <ID> --status '{{JIRA_DONE_STATUS}}' --yes` {
		t.Fatalf("CLOSE = %q", j.TemplateArgs["CLOSE_TASK_COMMAND"])
	}
	login := j.TemplateArgs["ISSUE_TRACKER_LOGIN_COMMAND"]
	if !strings.Contains(login, `printf '%s' "$JIRA_API_TOKEN" | acli jira auth login --site "$JIRA_SITE" --email "$JIRA_EMAIL" --token`) {
		t.Fatalf("LOGIN = %q", login)
	}
	for _, key := range []string{"JIRA_SITE=", "JIRA_EMAIL=", "JIRA_API_TOKEN="} {
		if !strings.Contains(j.EnvExample, key) {
			t.Fatalf("EnvExample lacks %s", key)
		}
	}
	tools := j.TemplateArgs["ISSUE_TRACKER_TOOLS"]
	if !strings.Contains(tools, "https://acli.atlassian.com/linux/deb stable main") || !strings.Contains(tools, "apt-get install -y acli") || strings.Contains(tools, "sudo") {
		t.Fatalf("TOOLS = %q", tools)
	}
	if len(j.Questions) != 2 || j.Questions[0].Key != "JIRA_JQL" || j.Questions[1].Key != "JIRA_DONE_STATUS" || j.Questions[1].Default != "Done" {
		t.Fatalf("Questions = %+v", j.Questions)
	}
	if !strings.Contains(j.Questions[0].Default, "labels = agentgopher") {
		t.Fatalf("default JQL = %q", j.Questions[0].Default)
	}
}

func TestWindshiftEntry(t *testing.T) {
	w, ok := GetIssueTracker("windshift")
	if !ok || w.Label != "Windshift" {
		t.Fatalf("windshift = %+v ok=%v", w, ok)
	}
	if w.TemplateArgs["LIST_TASKS_COMMAND"] != `ws task ls --status '{{WS_READY_STATUS}}' --all` {
		t.Fatalf("LIST = %q", w.TemplateArgs["LIST_TASKS_COMMAND"])
	}
	if w.TemplateArgs["VIEW_TASK_COMMAND"] != "ws task get <ID>" || w.TemplateArgs["CLOSE_TASK_COMMAND"] != `ws task move <ID> '{{WS_DONE_STATUS}}'` {
		t.Fatalf("VIEW=%q CLOSE=%q", w.TemplateArgs["VIEW_TASK_COMMAND"], w.TemplateArgs["CLOSE_TASK_COMMAND"])
	}
	if _, has := w.TemplateArgs["ISSUE_TRACKER_LOGIN_COMMAND"]; has {
		t.Fatal("windshift authenticates through env vars, no login hook")
	}
	for _, key := range []string{"WS_URL=", "WS_TOKEN=", "WS_WORKSPACE="} {
		if !strings.Contains(w.EnvExample, key) {
			t.Fatalf("EnvExample lacks %s", key)
		}
	}
	tools := w.TemplateArgs["ISSUE_TRACKER_TOOLS"]
	if !strings.Contains(tools, "github.com/Windshiftapp/core/releases/download/v") || !strings.Contains(tools, "ws-v") || !strings.Contains(tools, "/usr/local/bin") {
		t.Fatalf("TOOLS = %q", tools)
	}
	if len(w.Questions) != 2 || w.Questions[0].Key != "WS_READY_STATUS" || w.Questions[0].Default != "To Do" || w.Questions[1].Key != "WS_DONE_STATUS" || w.Questions[1].Default != "Done" {
		t.Fatalf("Questions = %+v", w.Questions)
	}
}

func TestDockerfilesFollowADR0014(t *testing.T) {
	for _, a := range ListAgents() {
		d := a.Dockerfile
		for _, want := range []string{
			"FROM node:22-bookworm",
			"{{ISSUE_TRACKER_TOOLS}}",
			"ARG AGENT_UID=1000",
			"ARG AGENT_GID=1000",
			"RUN groupmod -o -g $AGENT_GID node && usermod -o -u $AGENT_UID -g $AGENT_GID -d /home/agent -m -l agent node",
			"USER ${AGENT_UID}:${AGENT_GID}",
			`ENTRYPOINT ["sleep", "infinity"]`,
			"/home/agent/workspace",
		} {
			if !strings.Contains(d, want) {
				t.Fatalf("%s Dockerfile lacks %q", a.Name, want)
			}
		}
		if strings.Index(d, "{{ISSUE_TRACKER_TOOLS}}") > strings.Index(d, "USER ${AGENT_UID}") {
			t.Fatalf("%s: tracker tools must install before USER switch", a.Name)
		}
	}
	cc, _ := GetAgent("claude-code")
	if strings.Contains(cc.Dockerfile, "corepack") || strings.Contains(cc.Dockerfile, "pnpm") {
		t.Fatal("claude-code Dockerfile must not enable corepack or install pnpm")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/initcmd/`
Expected: FAIL on `TestIssueTrackerRegistryOrderAndNames` (empty registry) and `TestDockerfilesFollowADR0014`.

- [ ] **Step 3: Write dockerfiles.go**

The file is long because it holds verbatim text. The six agent Dockerfiles share a head and tail; only the install block differs.

```go
package initcmd

import "strings"

// sandboxRepoDir is where bind-mount providers mount the worktree.
const sandboxRepoDir = "/home/agent/workspace"

const dockerfileHead = `FROM node:22-bookworm

# Install system dependencies
RUN apt-get update && apt-get install -y \
  git \
  curl \
  jq \
  && rm -rf /var/lib/apt/lists/*

{{ISSUE_TRACKER_TOOLS}}

# Build-args for UID/GID alignment: agentgopher docker build-image
# defaults these to the host user's UID/GID so image-built files
# and bind-mounted files share an owner without runtime chown.
ARG AGENT_UID=1000
ARG AGENT_GID=1000

# Rename the base image's "node" user to "agent" and align UID/GID.
RUN groupmod -o -g $AGENT_GID node && usermod -o -u $AGENT_UID -g $AGENT_GID -d /home/agent -m -l agent node
`

const dockerfileTail = `
WORKDIR /home/agent

# In worktree sandbox mode, agentgopher bind-mounts the git worktree at ` + sandboxRepoDir + `
# and overrides the working directory to ` + sandboxRepoDir + ` at container start.
# Structure your Dockerfile so that ` + sandboxRepoDir + ` can serve as the project root.
ENTRYPOINT ["sleep", "infinity"]
`

// userSwitch is the numeric USER directive from ADR 0014.
const userSwitch = "USER ${AGENT_UID}:${AGENT_GID}\n"

// Agents installed as the agent user (curl installers into ~/.local/bin).
func userInstallDockerfile(install string) string {
	return dockerfileHead + userSwitch + "\n" + install + "\n# Add the CLI to PATH\nENV PATH=\"/home/agent/.local/bin:$PATH\"\n" + dockerfileTail
}

// Agents installed as root with npm before the USER switch.
func rootInstallDockerfile(install string) string {
	return dockerfileHead + "\n" + install + "\n\n" + userSwitch + dockerfileTail
}

var (
	claudeCodeDockerfile = userInstallDockerfile("# Install Claude Code CLI\nRUN curl -fsSL https://claude.ai/install.sh | bash")
	cursorDockerfile     = userInstallDockerfile("# Install Cursor Agent CLI\nRUN curl https://cursor.com/install -fsS | bash")
	piDockerfile         = rootInstallDockerfile("# Install pi coding agent (run as root before USER agent)\nRUN npm install -g @mariozechner/pi-coding-agent")
	codexDockerfile      = rootInstallDockerfile("# Install Codex CLI (run as root before USER agent)\nRUN npm install -g @openai/codex")
	opencodeDockerfile   = rootInstallDockerfile("# Install OpenCode CLI (run as root before USER agent)\nRUN npm install -g opencode-ai@latest")
	copilotDockerfile    = rootInstallDockerfile("# Install GitHub Copilot CLI (run as root before USER agent)\nRUN npm install -g @github/copilot")
)

const githubCLITools = `# Install GitHub CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
  | dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg \
  && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
  | tee /etc/apt/sources.list.d/github-cli.list > /dev/null \
  && apt-get update && apt-get install -y gh \
  && rm -rf /var/lib/apt/lists/*`

const beadsTools = `# Install system dependencies for Beads
RUN apt-get update && apt-get install -y \
  dpkg-dev \
  libicu72 \
  && rm -rf /var/lib/apt/lists/* \
  && ARCH_DIR=$(dpkg-architecture -qDEB_HOST_MULTIARCH) \
  && for lib in /usr/lib/$ARCH_DIR/libicu*.so.72; do \
       ln -s "$lib" "${lib%.72}.74"; \
     done

RUN curl -fsSL https://raw.githubusercontent.com/steveyegge/beads/main/scripts/install.sh | bash

RUN corepack enable`

// Verified against https://developer.atlassian.com/cloud/acli/guides/install-linux/ (2026-10-15). Runs as root, so no sudo.
const acliTools = `# Install Atlassian CLI (acli)
RUN apt-get update && apt-get install -y wget gnupg2 \
  && mkdir -p -m 755 /etc/apt/keyrings \
  && wget -nv -O- https://acli.atlassian.com/gpg/public-key.asc | gpg --dearmor -o /etc/apt/keyrings/acli-archive-keyring.gpg \
  && chmod go+r /etc/apt/keyrings/acli-archive-keyring.gpg \
  && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/acli-archive-keyring.gpg] https://acli.atlassian.com/linux/deb stable main" \
  | tee /etc/apt/sources.list.d/acli.list > /dev/null \
  && apt-get update && apt-get install -y acli \
  && rm -rf /var/lib/apt/lists/*`

// windshiftVersion pins the ws CLI release. Bump deliberately: the CLI is pre-1.0.
const windshiftVersion = "v0.8.9"

var windshiftTools = `# Install Windshift CLI (ws) from the pinned GitHub release
RUN ARCH=$(dpkg --print-architecture) \
  && curl -fsSL "https://github.com/Windshiftapp/core/releases/download/` + windshiftVersion + `/ws-` + windshiftVersion + `-linux-${ARCH}.tar.gz" \
  | tar -xz -C /usr/local/bin ws \
  && chmod +x /usr/local/bin/ws`

// Sentinels for the custom tracker. The scaffold ships broken until the
// setup doc has been worked through; the list sentinel is the enforcement point.
const (
	CustomListTasksSentinel = `echo 'No issue tracker configured - run ` + SetupIssueTrackerPath + ` through your coding agent.' >&2; exit 1`
	CustomViewTaskMarker    = `<view command - see ` + SetupIssueTrackerPath + `>`
	CustomCloseTaskMarker   = `<close command - see ` + SetupIssueTrackerPath + `>`
	CustomTrackerTools      = `# TODO: install your issue tracker's CLI here. See ` + SetupIssueTrackerPath
	customEnvExample        = "# TODO: add any env vars your issue tracker needs (e.g. an API token).\n# See " + SetupIssueTrackerPath
)

const defaultJiraJQL = `labels = agentgopher AND statusCategory = "To Do" ORDER BY priority DESC, created ASC`

var issueTrackerRegistry = []IssueTrackerEntry{
	{
		Name: "github-issues", Label: "GitHub Issues",
		TemplateArgs: map[string]string{
			"LIST_TASKS_COMMAND":  `gh issue list --state open --label agentgopher --limit 100 --json number,title,body,labels,comments --jq '[.[] | {number, title, body, labels: [.labels[].name], comments: [.comments[].body]}]'`,
			"VIEW_TASK_COMMAND":   "gh issue view <ID>",
			"CLOSE_TASK_COMMAND":  `gh issue close <ID> --comment "Completed by agentgopher"`,
			"ISSUE_TRACKER_TOOLS": githubCLITools,
		},
		EnvExample: "# GitHub personal access token - the agent uses it to read and manage GitHub Issues\n" +
			"# Create a fine-grained token: https://github.com/settings/personal-access-tokens/new\n" +
			"# Required repository permissions: Issues (Read and write) and Metadata (Read)\n" +
			"GH_TOKEN=",
	},
	{
		Name: "beads", Label: "Beads",
		TemplateArgs: map[string]string{
			"LIST_TASKS_COMMAND":  "bd ready --json",
			"VIEW_TASK_COMMAND":   "bd show <ID>",
			"CLOSE_TASK_COMMAND":  `bd close <ID> --reason="Completed by agentgopher"`,
			"ISSUE_TRACKER_TOOLS": beadsTools,
		},
	},
	{
		Name: "custom", Label: "Custom",
		TemplateArgs: map[string]string{
			"LIST_TASKS_COMMAND":  CustomListTasksSentinel,
			"VIEW_TASK_COMMAND":   CustomViewTaskMarker,
			"CLOSE_TASK_COMMAND":  CustomCloseTaskMarker,
			"ISSUE_TRACKER_TOOLS": CustomTrackerTools,
		},
		EnvExample: customEnvExample,
	},
	{
		Name: "jira", Label: "Jira",
		TemplateArgs: map[string]string{
			"LIST_TASKS_COMMAND":  `acli jira workitem search --jql '{{JIRA_JQL}}' --json --paginate --fields key,summary,description,status,labels,priority`,
			"VIEW_TASK_COMMAND":   `acli jira workitem view <ID> --json --fields '*navigable,comment'`,
			"CLOSE_TASK_COMMAND":  `acli jira workitem transition --key <ID> --status '{{JIRA_DONE_STATUS}}' --yes`,
			"ISSUE_TRACKER_TOOLS": acliTools,
			// acli has no env-var auth; log in once per sandbox before the agent starts.
			"ISSUE_TRACKER_LOGIN_COMMAND": `printf '%s' "$JIRA_API_TOKEN" | acli jira auth login --site "$JIRA_SITE" --email "$JIRA_EMAIL" --token`,
		},
		EnvExample: "# Jira Cloud site, e.g. mycompany.atlassian.net\nJIRA_SITE=\n" +
			"# Email of the Atlassian account the token belongs to\nJIRA_EMAIL=\n" +
			"# API token: https://id.atlassian.com/manage-profile/security/api-tokens\nJIRA_API_TOKEN=",
		Questions: []Question{
			{Key: "JIRA_JQL", Prompt: "JQL that selects the work items ready for the agent:", Default: defaultJiraJQL},
			{Key: "JIRA_DONE_STATUS", Prompt: "Status to transition a finished work item to:", Default: "Done"},
		},
	},
	{
		Name: "windshift", Label: "Windshift",
		TemplateArgs: map[string]string{
			"LIST_TASKS_COMMAND":  `ws task ls --status '{{WS_READY_STATUS}}' --all`,
			"VIEW_TASK_COMMAND":   "ws task get <ID>",
			"CLOSE_TASK_COMMAND":  `ws task move <ID> '{{WS_DONE_STATUS}}'`,
			"ISSUE_TRACKER_TOOLS": windshiftTools,
		},
		EnvExample: "# Windshift server URL, e.g. https://windshift.example.com\nWS_URL=\n" +
			"# API token created in Windshift under Settings > API tokens\nWS_TOKEN=\n" +
			"# Workspace key, e.g. PROJ\nWS_WORKSPACE=",
		Questions: []Question{
			{Key: "WS_READY_STATUS", Prompt: "Windshift status of tasks ready for the agent:", Default: "To Do"},
			{Key: "WS_DONE_STATUS", Prompt: "Windshift status to move a finished task to:", Default: "Done"},
		},
	},
}

// shellSingleQuote wraps s in single quotes for sh, escaping embedded quotes.
// Used when a question answer is spliced into a command.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
```

Note on `shellSingleQuote`: the Jira and Windshift commands already carry the single quotes around the placeholder, so the scaffold (Task 5) substitutes the raw answer and only escapes embedded single quotes with `strings.ReplaceAll(answer, "'", `'\''`)`. Keep `shellSingleQuote` out if nothing uses it after Task 5; the linter reports unused functions.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/initcmd/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/initcmd/
git commit -m "Add Dockerfile templates and issue tracker registry with Jira and Windshift"
```

---

### Task 4: Embedded templates as Go programs

Port of `src/templates/**`. Each template becomes a `package main` directory under `internal/initcmd/templates/<name>/`, compiled by `go build ./...` in CI, plus a `go.mod.tmpl` that the scaffold renames to `go.mod`. Prompt files are copied from upstream with the product name and config directory renamed.

**Files:**
- Create: `internal/initcmd/templates/<name>/main.go`, `go.mod.tmpl` and prompt files for blank, simple-loop, sequential-reviewer, parallel-planner, parallel-planner-with-review
- Create: `internal/initcmd/templates_test.go`

**Interfaces:**
- Consumes (phases 1 to 5; adapt the names below to what those phases shipped, the compiler points at every mismatch):
  - `agentgopher.Run(ctx, agentgopher.RunOptions{Name, Sandbox, Agent, PromptFile, PromptArgs, MaxIterations, BranchStrategy, CopyToWorktree, Hooks, Output})` returning `agentgopher.RunResult` with `Commits` and `Output string`.
  - `agentgopher.OutputString(tag string)` for `RunOptions.Output`; the planner templates decode the `<plan>` JSON with `encoding/json`.
  - `agentgopher.CreateSandbox(ctx, agentgopher.CreateSandboxOptions{Branch, Sandbox, Hooks, CopyToWorktree})` returning `*agentgopher.Sandbox` with `Run(ctx, agentgopher.SandboxRunOptions{Name, MaxIterations, Agent, PromptFile, PromptArgs})` and `Close(ctx) error`.
  - `agent.ClaudeCode(model, nil)`, `docker.New(nil)` from `github.com/e11en/agentgopher/sandbox/docker`.
- Produces: the template directories. Every `main.go` uses exactly `agent.ClaudeCode("<model>", nil)` for agent construction, `docker.New(nil)` for the provider, the import path `github.com/e11en/agentgopher/sandbox/docker`, and declares hooks as `SandboxOnSandboxReady: []agentgopher.SandboxHook{` on one line. Task 5's rewrites depend on these four spellings.

- [ ] **Step 1: Write the compile and shape test**

`internal/initcmd/templates_test.go`:
```go
package initcmd

import (
	"io/fs"
	"strings"
	"testing"
)

func TestEveryTemplateHasRequiredFilesAndMarkers(t *testing.T) {
	for _, tmpl := range ListTemplates() {
		dir := "templates/" + tmpl.Name
		entries, err := fs.ReadDir(templatesFS, dir)
		if err != nil {
			t.Fatalf("%s: %v", tmpl.Name, err)
		}
		names := map[string]bool{}
		for _, e := range entries {
			names[e.Name()] = true
		}
		for _, want := range []string{"main.go", "go.mod.tmpl", "template.json"} {
			if !names[want] {
				t.Fatalf("%s lacks %s", tmpl.Name, want)
			}
		}
		main, _ := fs.ReadFile(templatesFS, dir+"/main.go")
		for _, want := range []string{
			`agent.ClaudeCode("`,
			`docker.New(nil)`,
			`"github.com/e11en/agentgopher/sandbox/docker"`,
			`SandboxOnSandboxReady: []agentgopher.SandboxHook{`,
		} {
			if !strings.Contains(string(main), want) {
				t.Fatalf("%s/main.go lacks %q", tmpl.Name, want)
			}
		}
		if strings.Contains(string(main), "/internal/") {
			t.Fatalf("%s/main.go imports internal packages (ADR 0009)", tmpl.Name)
		}
		gomod, _ := fs.ReadFile(templatesFS, dir+"/go.mod.tmpl")
		if !strings.Contains(string(gomod), "require github.com/e11en/agentgopher {{AGENTGOPHER_VERSION}}") {
			t.Fatalf("%s/go.mod.tmpl = %q", tmpl.Name, gomod)
		}
	}
}

func TestPromptFilesUseTrackerPlaceholders(t *testing.T) {
	cases := map[string][]string{
		"blank/prompt.md":                           {"{{LIST_TASKS_COMMAND}}"},
		"simple-loop/prompt.md":                     {"!`{{LIST_TASKS_COMMAND}}`", "{{CLOSE_TASK_COMMAND}}", "<promise>COMPLETE</promise>"},
		"sequential-reviewer/implement-prompt.md":   {"!`{{LIST_TASKS_COMMAND}}`", "{{CLOSE_TASK_COMMAND}}"},
		"sequential-reviewer/review-prompt.md":      {"{{BRANCH}}", "{{TARGET_BRANCH}}", "@.agentgopher/CODING_STANDARDS.md"},
		"parallel-planner/plan-prompt.md":           {"!`{{LIST_TASKS_COMMAND}}`", "<plan>"},
		"parallel-planner/implement-prompt.md":      {"{{TASK_ID}}", "{{ISSUE_TITLE}}", "{{BRANCH}}", "{{VIEW_TASK_COMMAND}}"},
		"parallel-planner/merge-prompt.md":          {"{{BRANCHES}}", "{{ISSUES}}", "{{CLOSE_TASK_COMMAND}}"},
		"parallel-planner-with-review/review-prompt.md": {"{{BRANCH}}", "@.agentgopher/CODING_STANDARDS.md"},
	}
	for file, wants := range cases {
		b, err := fs.ReadFile(templatesFS, "templates/"+file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		s := string(b)
		for _, w := range wants {
			if !strings.Contains(s, w) {
				t.Fatalf("%s lacks %q", file, w)
			}
		}
		if strings.Contains(s, "sandcastle") || strings.Contains(s, "Sandcastle") {
			t.Fatalf("%s still mentions sandcastle", file)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/initcmd/ -run Template`
Expected: FAIL, "blank lacks main.go".

- [ ] **Step 3: Copy the prompt files from upstream and rename**

```bash
U=/tmp/sandcastle/src/templates
T=internal/initcmd/templates
for name in blank simple-loop sequential-reviewer parallel-planner parallel-planner-with-review; do
  for f in "$U/$name"/*.md; do
    sed -e 's/\.sandcastle/.agentgopher/g' -e 's/sandcastle\//agentgopher\//g' -e 's/Sandcastle/agentgopher/g' -e 's/sandcastle/agentgopher/g' -e "s/ $(printf '\xe2\x80\x94') / - /g" -e "s/$(printf '\xe2\x80\x94')/-/g" "$f" > "$T/$name/$(basename "$f")"
  done
done
grep -rn "sandcastle\|$(printf '\xe2\x80\x94')" $T && echo "FIX THE LINES ABOVE" || echo clean
```

The replacements turn `--label Sandcastle` into `--label agentgopher` and `@.sandcastle/CODING_STANDARDS.md` into `@.agentgopher/CODING_STANDARDS.md`. The blank `prompt.md` ends up as:
```markdown
# Context

<!-- Use !`command` to pull in dynamic context. Commands run inside the sandbox. -->
<!-- Example: !`git log --oneline -10` or !`{{LIST_TASKS_COMMAND}}` -->

# Task

<!-- Describe what the agent should do. -->

# Done

<!-- When the task is complete, output <promise>COMPLETE</promise> to signal early termination. -->
```

- [ ] **Step 4: Write go.mod.tmpl (identical in all five directories)**

```
module agentgopher-config

go 1.25

require github.com/e11en/agentgopher {{AGENTGOPHER_VERSION}}
```

- [ ] **Step 5: Write blank/main.go**

```go
// Blank template: customize this to build your own orchestration.
// Run it from the repo root with: go run -C .agentgopher .
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/e11en/agentgopher"
	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox/docker"
)

func main() {
	_, err := agentgopher.Run(context.Background(), agentgopher.RunOptions{
		Agent:      agent.ClaudeCode("claude-opus-4-8", nil),
		Sandbox:    docker.New(nil),
		PromptFile: "./.agentgopher/prompt.md",
		Hooks:      agentgopher.Hooks{SandboxOnSandboxReady: []agentgopher.SandboxHook{}},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 6: Write simple-loop/main.go**

```go
// Simple loop: an agent that picks open issues one by one and closes them.
// Run it from the repo root with: go run -C .agentgopher .
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/e11en/agentgopher"
	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/docker"
)

func main() {
	_, err := agentgopher.Run(context.Background(), agentgopher.RunOptions{
		// Shown as a prefix in log output.
		Name: "worker",
		// Runs the agent inside an isolated container.
		Sandbox: docker.New(nil),
		// Sonnet balances capability and speed; switch to claude-opus-4-8 for harder problems.
		Agent: agent.ClaudeCode("claude-sonnet-4-6", nil),
		// Shell expressions in the prompt run inside the sandbox at the start of each iteration.
		PromptFile: "./.agentgopher/prompt.md",
		// Each iteration works on a single issue. Set to 1 for single-shot mode.
		MaxIterations: 3,
		// merge-to-head: the agent works on a temporary branch that is merged back to HEAD.
		// Required for CopyToWorktree, since head mode mounts the host directory directly.
		BranchStrategy: &sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead},
		// Copy node_modules into the worktree so the sandbox skips a cold install;
		// the hook below still runs npm install as a safety net.
		CopyToWorktree: []string{"node_modules"},
		Hooks: agentgopher.Hooks{SandboxOnSandboxReady: []agentgopher.SandboxHook{{Command: "npm install"}}},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 7: Write sequential-reviewer/main.go**

```go
// Sequential reviewer: implement one issue on its own branch, then review it
// in the same sandbox. Stops when the implementer makes no commits.
// Run it from the repo root with: go run -C .agentgopher .
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/e11en/agentgopher"
	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox/docker"
)

// maxIterations bounds the number of implement-then-review cycles.
const maxIterations = 10

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	hooks := agentgopher.Hooks{SandboxOnSandboxReady: []agentgopher.SandboxHook{{Command: "npm install"}}}
	copyToWorktree := []string{"node_modules"}

	for i := 1; i <= maxIterations; i++ {
		fmt.Printf("\n=== Iteration %d/%d ===\n\n", i, maxIterations)
		branch := fmt.Sprintf("agentgopher/sequential-reviewer/%d", time.Now().UnixMilli())

		// One sandbox for both phases, so the reviewer sees the implementer's branch.
		sb, err := agentgopher.CreateSandbox(ctx, agentgopher.CreateSandboxOptions{
			Branch:         branch,
			Sandbox:        docker.New(nil),
			Hooks:          hooks,
			CopyToWorktree: copyToWorktree,
		})
		if err != nil {
			return err
		}
		stop, err := cycle(ctx, sb, branch)
		closeErr := sb.Close(ctx)
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if stop {
			break
		}
	}
	fmt.Println("\nAll done.")
	return nil
}

// cycle runs implementer then reviewer; stop is true when there was nothing to implement.
func cycle(ctx context.Context, sb *agentgopher.Sandbox, branch string) (stop bool, err error) {
	// One iteration so each outer pass implements a single issue, then hands it to the reviewer.
	implement, err := sb.Run(ctx, agentgopher.SandboxRunOptions{
		Name:          "implementer",
		MaxIterations: 1,
		Agent:         agent.ClaudeCode("claude-sonnet-4-6", nil),
		PromptFile:    "./.agentgopher/implement-prompt.md",
	})
	if err != nil {
		return false, err
	}
	if len(implement.Commits) == 0 {
		fmt.Println("Implementation agent made no commits. Stopping.")
		return true, nil
	}
	fmt.Printf("\nImplementation complete on branch: %s (%d commits)\n", branch, len(implement.Commits))

	_, err = sb.Run(ctx, agentgopher.SandboxRunOptions{
		Name:          "reviewer",
		MaxIterations: 1,
		Agent:         agent.ClaudeCode("claude-sonnet-4-6", nil),
		PromptFile:    "./.agentgopher/review-prompt.md",
		PromptArgs:    map[string]string{"BRANCH": branch},
	})
	if err != nil {
		return false, err
	}
	fmt.Println("\nReview complete.")
	return false, nil
}
```

- [ ] **Step 8: Write parallel-planner/main.go**

```go
// Parallel planner: plan, execute one agent per unblocked issue in parallel,
// then merge the branches that produced commits.
// Run it from the repo root with: go run -C .agentgopher .
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/e11en/agentgopher"
	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/docker"
)

const maxIterations = 10

type issue struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Branch string `json:"branch"`
}

type plan struct {
	Issues []issue `json:"issues"`
}

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	hooks := agentgopher.Hooks{SandboxOnSandboxReady: []agentgopher.SandboxHook{{Command: "npm install"}}}
	copyToWorktree := []string{"node_modules"}

	for i := 1; i <= maxIterations; i++ {
		fmt.Printf("\n=== Iteration %d/%d ===\n\n", i, maxIterations)

		// Phase 1: plan. Opus reads the backlog and emits a <plan> JSON block.
		planned, err := agentgopher.Run(ctx, agentgopher.RunOptions{
			Hooks:         hooks,
			Sandbox:       docker.New(nil),
			Name:          "planner",
			MaxIterations: 1,
			Agent:         agent.ClaudeCode("claude-opus-4-8", nil),
			PromptFile:    "./.agentgopher/plan-prompt.md",
			Output:        agentgopher.OutputString("plan"),
		})
		if err != nil {
			return err
		}
		var p plan
		if err := json.Unmarshal([]byte(planned.Output), &p); err != nil {
			return fmt.Errorf("planner output is not valid JSON: %w", err)
		}
		if len(p.Issues) == 0 {
			fmt.Println("No unblocked issues to work on. Exiting.")
			break
		}
		fmt.Printf("Planning complete. %d issue(s) to work in parallel:\n", len(p.Issues))
		for _, is := range p.Issues {
			fmt.Printf("  %s: %s -> %s\n", is.ID, is.Title, is.Branch)
		}

		// Phase 2: execute, one agent per issue on its own branch. A failing
		// agent does not cancel the others.
		completed := execute(ctx, p.Issues, hooks, copyToWorktree)
		if len(completed) == 0 {
			fmt.Println("No commits produced. Nothing to merge.")
			continue
		}

		// Phase 3: merge every branch that produced commits.
		branches := make([]string, 0, len(completed))
		issues := make([]string, 0, len(completed))
		for _, is := range completed {
			branches = append(branches, "- "+is.Branch)
			issues = append(issues, "- "+is.ID+": "+is.Title)
		}
		if _, err := agentgopher.Run(ctx, agentgopher.RunOptions{
			Hooks:         hooks,
			Sandbox:       docker.New(nil),
			Name:          "merger",
			MaxIterations: 1,
			Agent:         agent.ClaudeCode("claude-sonnet-4-6", nil),
			PromptFile:    "./.agentgopher/merge-prompt.md",
			PromptArgs:    map[string]string{"BRANCHES": strings.Join(branches, "\n"), "ISSUES": strings.Join(issues, "\n")},
		}); err != nil {
			return err
		}
		fmt.Println("\nBranches merged.")
	}
	fmt.Println("\nAll done.")
	return nil
}

// execute runs one implementer per issue concurrently and returns the issues
// whose branch received commits.
func execute(ctx context.Context, issues []issue, hooks agentgopher.Hooks, copyToWorktree []string) []issue {
	var mu sync.Mutex
	var wg sync.WaitGroup
	var completed []issue
	for _, is := range issues {
		wg.Add(1)
		go func(is issue) {
			defer wg.Done()
			res, err := agentgopher.Run(ctx, agentgopher.RunOptions{
				Hooks:          hooks,
				CopyToWorktree: copyToWorktree,
				Sandbox:        docker.New(nil),
				BranchStrategy: &sandbox.BranchStrategy{Type: sandbox.BranchNamed, Branch: is.Branch},
				Name:           "implementer",
				MaxIterations:  100,
				Agent:          agent.ClaudeCode("claude-sonnet-4-6", nil),
				PromptFile:     "./.agentgopher/implement-prompt.md",
				PromptArgs:     map[string]string{"TASK_ID": is.ID, "ISSUE_TITLE": is.Title, "BRANCH": is.Branch},
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "  x %s (%s) failed: %v\n", is.ID, is.Branch, err)
				return
			}
			if len(res.Commits) == 0 {
				return
			}
			mu.Lock()
			completed = append(completed, is)
			mu.Unlock()
		}(is)
	}
	wg.Wait()
	fmt.Printf("\nExecution complete. %d branch(es) with commits.\n", len(completed))
	return completed
}
```

- [ ] **Step 9: Write parallel-planner-with-review/main.go**

Same as Step 8 except `execute`, which creates one sandbox per issue so implementer and reviewer share the branch:

```go
// execute runs implementer then reviewer per issue, each pair in its own
// sandbox, all issues concurrently. Returns the issues whose branch received commits.
func execute(ctx context.Context, issues []issue, hooks agentgopher.Hooks, copyToWorktree []string) []issue {
	var mu sync.Mutex
	var wg sync.WaitGroup
	var completed []issue
	for _, is := range issues {
		wg.Add(1)
		go func(is issue) {
			defer wg.Done()
			ok, err := implementAndReview(ctx, is, hooks, copyToWorktree)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  x %s (%s) failed: %v\n", is.ID, is.Branch, err)
				return
			}
			if ok {
				mu.Lock()
				completed = append(completed, is)
				mu.Unlock()
			}
		}(is)
	}
	wg.Wait()
	fmt.Printf("\nExecution complete. %d branch(es) with commits.\n", len(completed))
	return completed
}

func implementAndReview(ctx context.Context, is issue, hooks agentgopher.Hooks, copyToWorktree []string) (bool, error) {
	sb, err := agentgopher.CreateSandbox(ctx, agentgopher.CreateSandboxOptions{
		Branch: is.Branch, Sandbox: docker.New(nil), Hooks: hooks, CopyToWorktree: copyToWorktree,
	})
	if err != nil {
		return false, err
	}
	defer sb.Close(ctx)

	implement, err := sb.Run(ctx, agentgopher.SandboxRunOptions{
		Name: "implementer", MaxIterations: 100,
		Agent:      agent.ClaudeCode("claude-sonnet-4-6", nil),
		PromptFile: "./.agentgopher/implement-prompt.md",
		PromptArgs: map[string]string{"TASK_ID": is.ID, "ISSUE_TITLE": is.Title, "BRANCH": is.Branch},
	})
	if err != nil {
		return false, err
	}
	if len(implement.Commits) == 0 {
		return false, nil
	}
	review, err := sb.Run(ctx, agentgopher.SandboxRunOptions{
		Name: "reviewer", MaxIterations: 1,
		Agent:      agent.ClaudeCode("claude-sonnet-4-6", nil),
		PromptFile: "./.agentgopher/review-prompt.md",
		PromptArgs: map[string]string{"BRANCH": is.Branch},
	})
	if err != nil {
		return false, err
	}
	return len(implement.Commits)+len(review.Commits) > 0, nil
}
```

The file header comment reads "Parallel planner with review: plan, then per issue implement and review in a shared sandbox, all issues in parallel, then merge." The rest of the file (types, `main`, `run`) is copied verbatim from Step 8.

- [ ] **Step 10: Build and run the tests**

Run: `go build ./... && go vet ./... && go test ./internal/initcmd/ -run Template`
Expected: the templates compile against the library and the tests PASS. A compile error here means a phase 3 to 5 name differs from the assumption in Interfaces above; fix the template, not the library.

- [ ] **Step 11: Commit**

```bash
git add internal/initcmd/templates/ internal/initcmd/templates_test.go
git commit -m "Add embedded init templates as Go programs"
```

---

### Task 5: Scaffold

Port of `scaffold` and its helpers in `InitService.ts`: copy the template, write the container file, `.gitignore` and `.env.example`, rewrite `main.go` for the chosen agent, model, provider and tracker login hook, write `go.mod`, substitute tracker template arguments (with question answers applied first), strip the label when declined, and write the custom tracker's setup doc.

**Files:**
- Create: `internal/initcmd/scaffold.go`, `internal/initcmd/scaffold_test.go`

**Interfaces:**
- Consumes: registries (Tasks 2 and 3), `templatesFS` (Task 4), `version.Version`.
- Produces:
  ```go
  type ScaffoldOptions struct {
      Agent        AgentEntry
      Model        string
      Template     string            // default "blank"
      CreateLabel  *bool             // nil means true
      IssueTracker *IssueTrackerEntry // nil means github-issues
      Sandbox      *SandboxProviderEntry // nil means docker
      Answers      map[string]string // question key to answer; missing keys use Question.Default
  }
  func Scaffold(repoDir string, o ScaffoldOptions) error
  func ResolveTemplateArgs(t IssueTrackerEntry, answers map[string]string) map[string]string
  func BuildSetupIssueTrackerDoc(cliNamespace string) string
  ```
- `ResolveTemplateArgs` replaces `{{Key}}` in every value with the answer (default when absent), escaping single quotes as `'\''` because the registry commands wrap the placeholders in single quotes.

- [ ] **Step 1: Write the failing tests**

`internal/initcmd/scaffold_test.go`:
```go
package initcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/version"
)

func mustAgent(t *testing.T, name string) AgentEntry {
	t.Helper()
	a, ok := GetAgent(name)
	if !ok {
		t.Fatal(name)
	}
	return a
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func scaffoldTo(t *testing.T, o ScaffoldOptions) string {
	t.Helper()
	dir := t.TempDir()
	if o.Agent.Name == "" {
		o.Agent = mustAgent(t, "claude-code")
	}
	if o.Model == "" {
		o.Model = o.Agent.DefaultModel
	}
	if err := Scaffold(dir, o); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, ".agentgopher")
}

func TestScaffoldDefaultsToBlankGitHubDocker(t *testing.T) {
	version.Version = "v1.2.3"
	cfg := scaffoldTo(t, ScaffoldOptions{})
	for _, f := range []string{"main.go", "go.mod", "prompt.md", "Dockerfile", ".gitignore", ".env.example"} {
		if _, err := os.Stat(filepath.Join(cfg, f)); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg, "template.json")); err == nil {
		t.Fatal("template.json must not be copied")
	}
	if _, err := os.Stat(filepath.Join(cfg, "go.mod.tmpl")); err == nil {
		t.Fatal("go.mod.tmpl must be renamed")
	}
	if got := read(t, filepath.Join(cfg, ".gitignore")); got != ".env\nlogs/\nworktrees/\n" {
		t.Fatalf(".gitignore = %q", got)
	}
	gomod := read(t, filepath.Join(cfg, "go.mod"))
	if !strings.Contains(gomod, "require github.com/e11en/agentgopher v1.2.3") {
		t.Fatalf("go.mod = %q", gomod)
	}
	dockerfile := read(t, filepath.Join(cfg, "Dockerfile"))
	if strings.Contains(dockerfile, "{{ISSUE_TRACKER_TOOLS}}") || !strings.Contains(dockerfile, "apt-get install -y gh") {
		t.Fatal("tracker tools not substituted into Dockerfile")
	}
	env := read(t, filepath.Join(cfg, ".env.example"))
	if !strings.Contains(env, "CLAUDE_CODE_OAUTH_TOKEN=") || !strings.Contains(env, "GH_TOKEN=") || !strings.HasSuffix(env, "\n") {
		t.Fatalf(".env.example = %q", env)
	}
	prompt := read(t, filepath.Join(cfg, "prompt.md"))
	if !strings.Contains(prompt, "gh issue list --state open --label agentgopher") || strings.Contains(prompt, "{{LIST_TASKS_COMMAND}}") {
		t.Fatalf("prompt.md = %q", prompt)
	}
}

func TestScaffoldDevVersionUsesPlaceholderVersion(t *testing.T) {
	version.Version = "dev"
	cfg := scaffoldTo(t, ScaffoldOptions{})
	if !strings.Contains(read(t, filepath.Join(cfg, "go.mod")), "require github.com/e11en/agentgopher v0.0.0") {
		t.Fatal("dev builds must write v0.0.0 so go mod tidy can replace it")
	}
}

func TestScaffoldRefusesExistingDir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".agentgopher"), 0o755)
	os.WriteFile(filepath.Join(dir, ".agentgopher", "keep.txt"), []byte("x"), 0o644)
	err := Scaffold(dir, ScaffoldOptions{Agent: mustAgent(t, "claude-code"), Model: "m"})
	if err == nil || !strings.Contains(err.Error(), ".agentgopher/ directory already exists") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".agentgopher", "keep.txt")); statErr != nil {
		t.Fatal("existing directory must be left untouched")
	}
}

func TestScaffoldUnknownTemplate(t *testing.T) {
	err := Scaffold(t.TempDir(), ScaffoldOptions{Agent: mustAgent(t, "claude-code"), Model: "m", Template: "nope"})
	if err == nil || !strings.Contains(err.Error(), `unknown template "nope"`) || !strings.Contains(err.Error(), "blank, simple-loop") {
		t.Fatalf("err = %v", err)
	}
}

func TestScaffoldRewritesAgentModelAndProvider(t *testing.T) {
	podman, _ := GetSandboxProvider("podman")
	cfg := scaffoldTo(t, ScaffoldOptions{Agent: mustAgent(t, "pi"), Model: "my-model", Template: "parallel-planner", Sandbox: &podman})
	main := read(t, filepath.Join(cfg, "main.go"))
	if strings.Contains(main, "ClaudeCode(") || strings.Contains(main, "docker") {
		t.Fatalf("main.go not rewritten:\n%s", main)
	}
	if strings.Count(main, `agent.Pi("my-model", nil)`) != 3 {
		t.Fatalf("expected 3 rewritten factory calls:\n%s", main)
	}
	if !strings.Contains(main, `"github.com/e11en/agentgopher/sandbox/podman"`) || !strings.Contains(main, "podman.New(nil)") {
		t.Fatalf("provider not rewritten:\n%s", main)
	}
	if _, err := os.Stat(filepath.Join(cfg, "Containerfile")); err != nil {
		t.Fatal("podman writes a Containerfile")
	}
	if _, err := os.Stat(filepath.Join(cfg, "Dockerfile")); err == nil {
		t.Fatal("podman must not write a Dockerfile")
	}
	if !strings.Contains(read(t, filepath.Join(cfg, "Containerfile")), "npm install -g @mariozechner/pi-coding-agent") {
		t.Fatal("pi Dockerfile not used")
	}
}

func TestScaffoldStripsLabelWhenDeclined(t *testing.T) {
	no := false
	cfg := scaffoldTo(t, ScaffoldOptions{Template: "simple-loop", CreateLabel: &no})
	if strings.Contains(read(t, filepath.Join(cfg, "prompt.md")), " --label agentgopher") {
		t.Fatal("label filter must be stripped")
	}
	cfg = scaffoldTo(t, ScaffoldOptions{Template: "simple-loop"})
	if !strings.Contains(read(t, filepath.Join(cfg, "prompt.md")), " --label agentgopher") {
		t.Fatal("label filter must be kept by default")
	}
}

func TestScaffoldJiraAppliesAnswersAndLoginHook(t *testing.T) {
	jira, _ := GetIssueTracker("jira")
	cfg := scaffoldTo(t, ScaffoldOptions{Template: "simple-loop", IssueTracker: &jira, Answers: map[string]string{
		"JIRA_JQL": `project = DEV AND status = "To Do" AND labels = agentgopher`,
	}})
	prompt := read(t, filepath.Join(cfg, "prompt.md"))
	if !strings.Contains(prompt, `acli jira workitem search --jql 'project = DEV AND status = "To Do" AND labels = agentgopher' --json`) {
		t.Fatalf("JQL not substituted:\n%s", prompt)
	}
	if !strings.Contains(prompt, `--status 'Done' --yes`) {
		t.Fatal("default done status not applied")
	}
	if strings.Contains(prompt, "{{JIRA") {
		t.Fatal("question placeholders left behind")
	}
	main := read(t, filepath.Join(cfg, "main.go"))
	want := `SandboxOnSandboxReady: []agentgopher.SandboxHook{{Command: "printf '%s' \"$JIRA_API_TOKEN\" | acli jira auth login --site \"$JIRA_SITE\" --email \"$JIRA_EMAIL\" --token"}, {Command: "npm install"}}`
	if !strings.Contains(main, want) {
		t.Fatalf("login hook not inserted:\n%s", main)
	}
	env := read(t, filepath.Join(cfg, ".env.example"))
	for _, k := range []string{"JIRA_SITE=", "JIRA_EMAIL=", "JIRA_API_TOKEN="} {
		if !strings.Contains(env, k) {
			t.Fatalf(".env.example lacks %s", k)
		}
	}
	if !strings.Contains(read(t, filepath.Join(cfg, "Dockerfile")), "apt-get install -y acli") {
		t.Fatal("acli not installed in Dockerfile")
	}
}

func TestResolveTemplateArgsEscapesSingleQuotes(t *testing.T) {
	w, _ := GetIssueTracker("windshift")
	args := ResolveTemplateArgs(w, map[string]string{"WS_DONE_STATUS": "Klaar 'echt'", "WS_READY_STATUS": "$HOME"})
	if args["CLOSE_TASK_COMMAND"] != `ws task move <ID> 'Klaar '\''echt'\'''` {
		t.Fatalf("CLOSE = %q", args["CLOSE_TASK_COMMAND"])
	}
	if args["LIST_TASKS_COMMAND"] != `ws task ls --status '$HOME' --all` {
		t.Fatalf("LIST = %q", args["LIST_TASKS_COMMAND"])
	}
}

func TestScaffoldWindshiftBlankLeavesHooksUnchanged(t *testing.T) {
	w, _ := GetIssueTracker("windshift")
	cfg := scaffoldTo(t, ScaffoldOptions{IssueTracker: &w})
	main := read(t, filepath.Join(cfg, "main.go"))
	if !strings.Contains(main, "SandboxOnSandboxReady: []agentgopher.SandboxHook{}") {
		t.Fatalf("windshift has no login hook, main.go must be unchanged:\n%s", main)
	}
	if !strings.Contains(read(t, filepath.Join(cfg, "prompt.md")), `ws task ls --status 'To Do' --all`) {
		t.Fatal("windshift defaults not applied")
	}
}

func TestScaffoldCustomTrackerWritesSetupDoc(t *testing.T) {
	custom, _ := GetIssueTracker("custom")
	podman, _ := GetSandboxProvider("podman")
	cfg := scaffoldTo(t, ScaffoldOptions{Template: "simple-loop", IssueTracker: &custom, Sandbox: &podman})
	doc := read(t, filepath.Join(cfg, SetupIssueTrackerDoc))
	for _, want := range []string{CustomListTasksSentinel, CustomViewTaskMarker, CustomCloseTaskMarker, CustomTrackerTools, "agentgopher podman build-image"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("setup doc lacks %q", want)
		}
	}
	if !strings.Contains(read(t, filepath.Join(cfg, "prompt.md")), CustomListTasksSentinel) {
		t.Fatal("sentinel not in prompt")
	}
}

func TestScaffoldSequentialReviewerFiles(t *testing.T) {
	cfg := scaffoldTo(t, ScaffoldOptions{Template: "sequential-reviewer"})
	for _, f := range []string{"implement-prompt.md", "review-prompt.md", "CODING_STANDARDS.md"} {
		if _, err := os.Stat(filepath.Join(cfg, f)); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
	if strings.Contains(read(t, filepath.Join(cfg, "implement-prompt.md")), "{{TASK_ID}}") {
		t.Fatal("prompts without a runtime TASK_ID must not contain the placeholder")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/initcmd/ -run Scaffold`
Expected: FAIL, "undefined: Scaffold".

- [ ] **Step 3: Write scaffold.go**

```go
package initcmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/e11en/agentgopher/internal/version"
)

const configDirName = ".agentgopher"

const gitignore = ".env\nlogs/\nworktrees/\n"

// ScaffoldOptions configure Scaffold. Nil pointers select the defaults:
// template blank, GitHub Issues, Docker, label created.
type ScaffoldOptions struct {
	Agent        AgentEntry
	Model        string
	Template     string
	CreateLabel  *bool
	IssueTracker *IssueTrackerEntry
	Sandbox      *SandboxProviderEntry
	// Answers map a Question.Key to the user's answer.
	Answers map[string]string
}

// Scaffold writes <repoDir>/.agentgopher from the chosen template.
func Scaffold(repoDir string, o ScaffoldOptions) error {
	templateName := o.Template
	if templateName == "" {
		templateName = "blank"
	}
	if _, ok := GetTemplate(templateName); !ok {
		names := make([]string, 0, len(templateOrder))
		for _, t := range ListTemplates() {
			names = append(names, t.Name)
		}
		return fmt.Errorf("unknown template %q. Available: %s", templateName, strings.Join(names, ", "))
	}
	tracker := issueTrackerRegistry[0]
	if o.IssueTracker != nil {
		tracker = *o.IssueTracker
	}
	provider := sandboxProviderRegistry[0]
	if o.Sandbox != nil {
		provider = *o.Sandbox
	}
	createLabel := o.CreateLabel == nil || *o.CreateLabel

	configDir := filepath.Join(repoDir, configDirName)
	if _, err := os.Stat(configDir); err == nil {
		return errors.New(".agentgopher/ directory already exists. Remove it first if you want to re-initialize.")
	}
	if err := os.Mkdir(configDir, 0o755); err != nil {
		return err
	}

	// Copy the template verbatim, except metadata and the go.mod template.
	src := "templates/" + templateName
	entries, err := fs.ReadDir(templatesFS, src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "template.json" || e.Name() == "go.mod.tmpl" {
			continue
		}
		b, err := templatesFS.ReadFile(src + "/" + e.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(configDir, e.Name()), b, 0o644); err != nil {
			return err
		}
	}

	gomod, err := templatesFS.ReadFile(src + "/go.mod.tmpl")
	if err != nil {
		return err
	}
	moduleVersion := version.Version
	if moduleVersion == "dev" {
		// Not a valid module version; the next steps tell the user to run go get.
		moduleVersion = "v0.0.0"
	}
	if err := os.WriteFile(filepath.Join(configDir, "go.mod"), []byte(strings.ReplaceAll(string(gomod), "{{AGENTGOPHER_VERSION}}", moduleVersion)), 0o644); err != nil {
		return err
	}

	envExample := o.Agent.EnvExample
	if tracker.EnvExample != "" {
		envExample += "\n" + tracker.EnvExample
	}
	files := map[string]string{
		provider.ContainerfileName: o.Agent.Dockerfile,
		".gitignore":               gitignore,
		".env.example":             envExample + "\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(configDir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}

	args := ResolveTemplateArgs(tracker, o.Answers)
	if err := rewriteMain(filepath.Join(configDir, "main.go"), o.Agent, o.Model, provider, args["ISSUE_TRACKER_LOGIN_COMMAND"]); err != nil {
		return err
	}
	if err := substituteTemplateArgs(configDir, args); err != nil {
		return err
	}
	if !createLabel {
		if err := rewriteTextFiles(configDir, func(s string) string { return strings.ReplaceAll(s, " --label agentgopher", "") }); err != nil {
			return err
		}
	}
	if tracker.Name == "custom" {
		return os.WriteFile(filepath.Join(configDir, SetupIssueTrackerDoc), []byte(BuildSetupIssueTrackerDoc(provider.CLINamespace)), 0o644)
	}
	return nil
}

// ResolveTemplateArgs applies question answers (or defaults) to the tracker's
// template arguments. Answers are spliced inside single quotes in the
// registry commands, so embedded single quotes are escaped for sh.
func ResolveTemplateArgs(t IssueTrackerEntry, answers map[string]string) map[string]string {
	out := make(map[string]string, len(t.TemplateArgs))
	for k, v := range t.TemplateArgs {
		for _, q := range t.Questions {
			answer, ok := answers[q.Key]
			if !ok || answer == "" {
				answer = q.Default
			}
			v = strings.ReplaceAll(v, "{{"+q.Key+"}}", strings.ReplaceAll(answer, "'", `'\''`))
		}
		out[k] = v
	}
	return out
}

var modelCallPattern = regexp.MustCompile(`agent\.ClaudeCode\("[^"]*", nil\)`)
var dockerWordPattern = regexp.MustCompile(`\bdocker\b`)

// rewriteMain points main.go at the chosen agent, model and provider, and
// prepends the tracker login hook when the tracker has one. Templates always
// use agent.ClaudeCode and docker as placeholders.
func rewriteMain(path string, a AgentEntry, model string, p SandboxProviderEntry, loginCommand string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s := modelCallPattern.ReplaceAllString(string(b), fmt.Sprintf(`agent.%s(%q, nil)`, a.Factory, model))
	s = dockerWordPattern.ReplaceAllString(s, p.Name)
	if loginCommand != "" {
		s = strings.Replace(s, "SandboxOnSandboxReady: []agentgopher.SandboxHook{", fmt.Sprintf("SandboxOnSandboxReady: []agentgopher.SandboxHook{{Command: %q}, ", loginCommand), 1)
		s = strings.ReplaceAll(s, "}, }", "}}")
	}
	return os.WriteFile(path, []byte(s), 0o644)
}

func isTextFile(name string) bool {
	switch name {
	case "Dockerfile", "Containerfile", ".gitignore":
		return true
	}
	switch filepath.Ext(name) {
	case ".md", ".txt", ".env", ".example":
		return true
	}
	return false
}

func rewriteTextFiles(configDir string, fn func(string) string) error {
	entries, err := os.ReadDir(configDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !isTextFile(e.Name()) {
			continue
		}
		path := filepath.Join(configDir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		updated := fn(string(b))
		if updated == string(b) {
			continue
		}
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func substituteTemplateArgs(configDir string, args map[string]string) error {
	return rewriteTextFiles(configDir, func(s string) string {
		for k, v := range args {
			s = strings.ReplaceAll(s, "{{"+k+"}}", v)
		}
		return s
	})
}

// BuildSetupIssueTrackerDoc is the prompt the user feeds to their coding agent
// to wire up a custom tracker by editing the scaffold in place.
func BuildSetupIssueTrackerDoc(cliNamespace string) string {
	return `# Set up your custom issue tracker

You are a coding agent. Finish wiring up the **custom issue tracker** for this agentgopher project. It was scaffolded in a deliberately broken-until-configured state: until you complete the steps below, every agentgopher run hard-fails with a pointer back to this file.

## Goal

Wire up the issue tracker so the scaffolded prompts can **list**, **view**, and **close** tasks. There is no runtime abstraction to implement - the tracker commands are baked into the scaffolded files, so you edit those files **in place**.

## 1. Interview the user

Ask the user:

- Which issue tracker do they use (e.g. Linear, a GitHub repo other than this one, an internal API)?
- How should the sandbox authenticate - a CLI that is already logged in, or an API token? If a token, what is the environment variable name?

## 2. Produce three commands

Work out, together with the user, the shell commands for:

- **list** - print all open tasks **as JSON** (an array of objects, each with at least an id, title, and body). This is what the agent reads at the start of every iteration.
- **view** ` + "`<ID>`" + ` - show a single task by id.
- **close** ` + "`<ID>`" + ` - close a single task by id.

## 3. Edit the scaffolded files in place

- **Dockerfile / Containerfile** - replace the line

  ` + "```" + `
  ` + CustomTrackerTools + `
  ` + "```" + `

  with the install steps for your tracker's CLI (if it needs one).

- **Prompt files (` + "`.agentgopher/*.md`" + `)** - replace the sentinel

  ` + "```" + `
  ` + CustomListTasksSentinel + `
  ` + "```" + `

  with your **list** command. In the prompt file the sentinel sits inside an agentgopher **shell expression** - a leading ` + "`!`" + ` followed by the command in backticks - whose output is injected into the prompt before each run. Keep that ` + "`!`" + ` and the surrounding backticks; replace only the command between them, and **remove the ` + "`exit 1`" + `** (leaving it keeps every run hard-failing). Then replace the ` + "`" + CustomViewTaskMarker + "`" + ` and ` + "`" + CustomCloseTaskMarker + "`" + ` markers with your **view** and **close** commands.

- **` + "`.env.example`" + `** - replace the ` + "`# TODO`" + ` block with the real env var(s) your tracker needs, then tell the user to set them in ` + "`.agentgopher/.env`" + `.

## 4. Build the image

Once the files are wired up, build the sandbox image:

` + "```" + `
agentgopher ` + cliNamespace + ` build-image
` + "```" + `

## 5. Verify

Run your **list** command inside the built image and confirm it returns the open tasks as JSON. If it errors, fix the command or the auth and rebuild.
`
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/initcmd/`
Expected: PASS. If `TestScaffoldRewritesAgentModelAndProvider` reports a different count of factory calls, count them in the parallel-planner template from Task 4 and fix the test expectation to the real number.

- [ ] **Step 5: Commit**

```bash
git add internal/initcmd/scaffold.go internal/initcmd/scaffold_test.go
git commit -m "Add init scaffold with template rewrites and tracker answers"
```

---

### Task 6: Next steps

Port of `getNextStepsLines`. The npm-specific steps become Go steps.

**Files:**
- Create: `internal/initcmd/nextsteps.go`, `internal/initcmd/nextsteps_test.go`

**Interfaces:**
- Produces: `func NextStepsLines(template string, tracker IssueTrackerEntry, a AgentEntry) []string`. First line is `Next steps:`.

- [ ] **Step 1: Write the failing tests**

`internal/initcmd/nextsteps_test.go`:
```go
package initcmd

import (
	"strings"
	"testing"
)

func joined(lines []string) string { return strings.Join(lines, "\n") }

func TestNextStepsCustomTrackerWinsOverTemplate(t *testing.T) {
	custom, _ := GetIssueTracker("custom")
	pi, _ := GetAgent("pi")
	s := joined(NextStepsLines("parallel-planner", custom, pi))
	if !strings.Contains(s, pi.SetupCommand) || !strings.Contains(s, "Runs on the host") || !strings.Contains(s, SetupIssueTrackerDoc) {
		t.Fatalf("got:\n%s", s)
	}
	if strings.Contains(s, "go run -C") {
		t.Fatal("custom tracker steps must not include the normal run step")
	}
}

func TestNextStepsBlank(t *testing.T) {
	gh, _ := GetIssueTracker("github-issues")
	cc, _ := GetAgent("claude-code")
	s := joined(NextStepsLines("blank", gh, cc))
	for _, want := range []string{"Next steps:", ".agentgopher/.env", "claude setup-token", "prompt.md", ".agentgopher/main.go", "go run -C .agentgopher .", "go mod tidy"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "CODING_STANDARDS") {
		t.Fatal("blank must not mention coding standards")
	}
}

func TestNextStepsReviewerAndNonClaude(t *testing.T) {
	gh, _ := GetIssueTracker("github-issues")
	codex, _ := GetAgent("codex")
	s := joined(NextStepsLines("sequential-reviewer", gh, codex))
	if !strings.Contains(s, "CODING_STANDARDS.md") || !strings.Contains(s, "CopyToWorktree") || strings.Contains(s, "claude setup-token") {
		t.Fatalf("got:\n%s", s)
	}
	s = joined(NextStepsLines("simple-loop", gh, codex))
	if strings.Contains(s, "CODING_STANDARDS.md") {
		t.Fatal("simple-loop must not mention coding standards")
	}
}

func TestNextStepsJiraMentionsLogin(t *testing.T) {
	jira, _ := GetIssueTracker("jira")
	cc, _ := GetAgent("claude-code")
	s := joined(NextStepsLines("simple-loop", jira, cc))
	if !strings.Contains(s, "JIRA_API_TOKEN") {
		t.Fatalf("got:\n%s", s)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/initcmd/ -run NextSteps`
Expected: FAIL, "undefined: NextStepsLines".

- [ ] **Step 3: Write nextsteps.go**

```go
package initcmd

import (
	"fmt"
	"strings"
)

// NextStepsLines returns what the user should do after init.
func NextStepsLines(template string, tracker IssueTrackerEntry, a AgentEntry) []string {
	if tracker.Name == "custom" {
		return []string{
			"Next steps:",
			"1. Your custom issue tracker isn't wired up yet - runs hard-fail until you configure it.",
			fmt.Sprintf("2. Feed the setup prompt to %s on your host to finish wiring it up:", a.Label),
			"   " + a.SetupCommand,
			fmt.Sprintf("   (Runs on the host - you need the %s CLI installed locally, since the sandbox image isn't built yet.)", a.Label),
			fmt.Sprintf("3. Follow .agentgopher/%s to edit the scaffolded files in place, build the image, and verify.", SetupIssueTrackerDoc),
		}
	}
	step := 0
	next := func(text string) string {
		step++
		return fmt.Sprintf("%d. %s", step, text)
	}
	lines := []string{"Next steps:", next("Set the required env vars in .agentgopher/.env (see .agentgopher/.env.example)")}
	if a.Name == "claude-code" {
		lines = append(lines, "   To use your Claude subscription instead of an API key, run `claude setup-token` on your host and paste the result into CLAUDE_CODE_OAUTH_TOKEN.")
	}
	if _, ok := tracker.TemplateArgs["ISSUE_TRACKER_LOGIN_COMMAND"]; ok {
		lines = append(lines, "   The sandbox logs in to "+tracker.Label+" with these values before each run, so all of them are required (e.g. JIRA_API_TOKEN).")
	}
	lines = append(lines, next("Run `go mod tidy` inside .agentgopher/ once (use `go get github.com/e11en/agentgopher@latest` first when go.mod says v0.0.0)"))
	if template == "blank" {
		lines = append(lines,
			next("Read and customize .agentgopher/prompt.md to describe what you want the agent to do"),
			next("Customize .agentgopher/main.go - it calls agentgopher.Run to control how the agent runs"),
		)
	} else {
		lines = append(lines,
			next("Templates use `CopyToWorktree: []string{\"node_modules\"}` to copy your host node_modules into the sandbox for fast startup - the `npm install` in the onSandboxReady hook is a safety net. Adjust both if your project is not a Node project"),
			next("Read and customize the prompt files in .agentgopher/ - they shape what the agent does"),
		)
		if strings.Contains(template, "review") {
			lines = append(lines, next("Customize .agentgopher/CODING_STANDARDS.md with your project's standards - the reviewer agent loads it during review"))
		}
	}
	return append(lines, next("Run `go run -C .agentgopher .` from the repo root to start the agent"))
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/initcmd/ -run NextSteps`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/initcmd/nextsteps.go internal/initcmd/nextsteps_test.go
git commit -m "Add init next steps"
```

---

### Task 7: Image build and removal for Docker and Podman

Port of `buildImage` and `removeImage` from `DockerLifecycle.ts` and `PodmanLifecycle.ts`. Added to the provider packages that phase 3 created.

**Files:**
- Create: `sandbox/docker/image.go`, `sandbox/docker/image_test.go`, `sandbox/podman/image.go`, `sandbox/podman/image_test.go`

**Interfaces:**
- Consumes: packages `sandbox/docker` and `sandbox/podman` (phase 3). If phase 3 already defined `DefaultImageName` or an `execCommand` variable in these packages, reuse them instead of adding the ones below.
- Produces, in both packages (identical shapes):
  ```go
  type BuildImageOptions struct {
      ImageName  string
      ContextDir string            // build context and default location of the container file
      File       string            // optional explicit Dockerfile/Containerfile; context becomes the working directory
      BuildArgs  map[string]string // --build-arg KEY=VALUE, sorted by key
  }
  func BuildImage(ctx context.Context, o BuildImageOptions) error
  func RemoveImage(ctx context.Context, imageName string) error
  var execCommand = exec.CommandContext
  ```
- Only `sandbox/docker` gets `func DefaultImageName(repoDir string) string` returning `agentgopher:<lowercased dir name with [^a-z0-9_.-] replaced by ->`, `agentgopher:local` when empty. Podman reuses it through the CLI.
- Errors: `fmt.Errorf("docker build failed: %s", stderr)` (or `podman`), with stderr falling back to the exec error text.

- [ ] **Step 1: Write the failing tests**

`sandbox/docker/image_test.go`:
```go
package docker

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func recordExec(t *testing.T, fail bool) *[][]string {
	t.Helper()
	var calls [][]string
	old := execCommand
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		calls = append(calls, append([]string{name}, args...))
		if fail {
			return exec.CommandContext(ctx, "sh", "-c", "echo build broke 1>&2; exit 1")
		}
		return exec.CommandContext(ctx, "true")
	}
	t.Cleanup(func() { execCommand = old })
	return &calls
}

func TestBuildImageDefaultContext(t *testing.T) {
	calls := recordExec(t, false)
	err := BuildImage(context.Background(), BuildImageOptions{ImageName: "agentgopher:x", ContextDir: "/repo/.agentgopher", BuildArgs: map[string]string{"AGENT_UID": "501", "AGENT_GID": "20"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "docker build -t agentgopher:x --build-arg AGENT_GID=20 --build-arg AGENT_UID=501 /repo/.agentgopher"
	if got := strings.Join((*calls)[0], " "); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestBuildImageExplicitFileUsesCwdAsContext(t *testing.T) {
	calls := recordExec(t, false)
	if err := BuildImage(context.Background(), BuildImageOptions{ImageName: "i", ContextDir: "/repo/.agentgopher", File: "/elsewhere/Dockerfile"}); err != nil {
		t.Fatal(err)
	}
	got := strings.Join((*calls)[0], " ")
	if !strings.HasPrefix(got, "docker build -t i -f /elsewhere/Dockerfile ") || strings.HasSuffix(got, "/repo/.agentgopher") {
		t.Fatalf("got %q", got)
	}
}

func TestBuildImageSurfacesStderr(t *testing.T) {
	recordExec(t, true)
	err := BuildImage(context.Background(), BuildImageOptions{ImageName: "i", ContextDir: "."})
	if err == nil || !strings.Contains(err.Error(), "docker build failed: build broke") {
		t.Fatalf("err = %v", err)
	}
}

func TestRemoveImage(t *testing.T) {
	calls := recordExec(t, false)
	if err := RemoveImage(context.Background(), "agentgopher:x"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join((*calls)[0], " "); got != "docker rmi agentgopher:x" {
		t.Fatalf("got %q", got)
	}
}

func TestDefaultImageName(t *testing.T) {
	cases := map[string]string{"/Users/me/My Repo/": "agentgopher:my-repo", "/x/app_v2.0": "agentgopher:app_v2.0", "": "agentgopher:local"}
	for in, want := range cases {
		if got := DefaultImageName(in); got != want {
			t.Errorf("DefaultImageName(%q) = %q, want %q", in, got, want)
		}
	}
}
```

`sandbox/podman/image_test.go`: the same file with `package podman`, `docker` replaced by `podman` in the expected command strings and error text, and without `TestDefaultImageName`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./sandbox/docker/ ./sandbox/podman/`
Expected: FAIL, "undefined: BuildImage".

- [ ] **Step 3: Write sandbox/docker/image.go**

```go
package docker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// execCommand is replaced in tests.
var execCommand = exec.CommandContext

// BuildImageOptions configure BuildImage.
type BuildImageOptions struct {
	ImageName string
	// ContextDir is the build context and holds the Dockerfile unless File is set.
	ContextDir string
	// File is an explicit Dockerfile; the build context is then the working directory,
	// so COPY instructions resolve relative to it.
	File      string
	BuildArgs map[string]string
}

// BuildImage runs docker build.
func BuildImage(ctx context.Context, o BuildImageOptions) error {
	args := []string{"build", "-t", o.ImageName}
	keys := make([]string, 0, len(o.BuildArgs))
	for k := range o.BuildArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--build-arg", k+"="+o.BuildArgs[k])
	}
	if o.File != "" {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		args = append(args, "-f", abs(o.File), cwd)
	} else {
		args = append(args, abs(o.ContextDir))
	}
	return run(ctx, args...)
}

// RemoveImage runs docker rmi.
func RemoveImage(ctx context.Context, imageName string) error {
	return run(ctx, "rmi", imageName)
}

func run(ctx context.Context, args ...string) error {
	cmd := execCommand(ctx, "docker", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("docker %s failed: %s", args[0], detail)
	}
	return nil
}

func abs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

var imageNameUnsafe = regexp.MustCompile(`[^a-z0-9_.-]`)

// DefaultImageName derives the image name from the repo directory name.
func DefaultImageName(repoDir string) string {
	name := strings.ToLower(filepath.Base(strings.TrimRight(repoDir, `/\`)))
	if name == "." || name == "" || name == string(filepath.Separator) {
		name = "local"
	}
	name = imageNameUnsafe.ReplaceAllString(name, "-")
	if name == "" {
		name = "local"
	}
	return "agentgopher:" + name
}
```

`sandbox/podman/image.go` is the same file with `package podman`, every `docker` string replaced by `podman`, the `Containerfile` wording in the comments, and without `DefaultImageName` and the `regexp` import.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./sandbox/docker/ ./sandbox/podman/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sandbox/docker/image.go sandbox/docker/image_test.go sandbox/podman/image.go sandbox/podman/image_test.go
git commit -m "Add image build and removal for Docker and Podman providers"
```

---

### Task 8: The init flow

Port of the `init` command body in `cli.ts`: resolve every choice from a flag or a prompt, create the GitHub label, scaffold, build the image, print next steps. The terminal interaction sits behind a `Prompter` so the flow is testable.

**Files:**
- Create: `internal/initcmd/prompter.go`, `internal/initcmd/run.go`, `internal/initcmd/run_test.go`

**Interfaces:**
- Consumes: `Scaffold`, `NextStepsLines`, registries, `docker.BuildImage`, `podman.BuildImage`, `docker.DefaultImageName`.
- Produces:
  ```go
  type Choice struct { Value, Label, Hint string }
  type Prompter interface {
      Select(message string, choices []Choice, initial string) (string, error)
      Confirm(message string, initial bool) (bool, error)
      Input(message, initial string) (string, error)
  }
  func NewStdinPrompter(in io.Reader, out io.Writer) Prompter // numbered menu, reads one line per answer
  type Options struct {
      RepoDir      string
      ImageName    string            // "" means docker.DefaultImageName(RepoDir)
      Template, Agent, Model, Sandbox, IssueTracker string
      CreateLabel  *bool             // nil means ask
      BuildImage   *bool             // nil means ask
      Answers      map[string]string // pre-answered tracker questions
      Interactive  bool              // false: every unset choice is an error naming its flag
      Prompter     Prompter
      Out          io.Writer
  }
  func Run(ctx context.Context, o Options) error
  var execCommand = exec.CommandContext   // for gh label create
  var buildDockerImage = docker.BuildImage // replaced in tests
  var buildPodmanImage = podman.BuildImage
  var hostUIDGID = func() (string, string) // "501","20" on Unix, "" on Windows
  ```

- [ ] **Step 1: Write the failing tests**

`internal/initcmd/run_test.go`:
```go
package initcmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/sandbox/docker"
	"github.com/e11en/agentgopher/sandbox/podman"
)

// scriptedPrompter answers in order; any answer is a string, "y"/"n" for confirms.
type scriptedPrompter struct {
	answers []string
	asked   []string
}

func (p *scriptedPrompter) next(msg string) string {
	p.asked = append(p.asked, msg)
	if len(p.answers) == 0 {
		return ""
	}
	a := p.answers[0]
	p.answers = p.answers[1:]
	return a
}
func (p *scriptedPrompter) Select(msg string, _ []Choice, initial string) (string, error) {
	if a := p.next(msg); a != "" {
		return a, nil
	}
	return initial, nil
}
func (p *scriptedPrompter) Confirm(msg string, initial bool) (bool, error) {
	switch p.next(msg) {
	case "y":
		return true, nil
	case "n":
		return false, nil
	}
	return initial, nil
}
func (p *scriptedPrompter) Input(msg, initial string) (string, error) {
	if a := p.next(msg); a != "" {
		return a, nil
	}
	return initial, nil
}

type fakes struct {
	ghCalls      [][]string
	dockerBuilds []docker.BuildImageOptions
	podmanBuilds []podman.BuildImageOptions
}

func installFakes(t *testing.T) *fakes {
	t.Helper()
	f := &fakes{}
	oldExec, oldD, oldP, oldUID := execCommand, buildDockerImage, buildPodmanImage, hostUIDGID
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		f.ghCalls = append(f.ghCalls, append([]string{name}, args...))
		return exec.CommandContext(ctx, "true")
	}
	buildDockerImage = func(_ context.Context, o docker.BuildImageOptions) error { f.dockerBuilds = append(f.dockerBuilds, o); return nil }
	buildPodmanImage = func(_ context.Context, o podman.BuildImageOptions) error { f.podmanBuilds = append(f.podmanBuilds, o); return nil }
	hostUIDGID = func() (string, string) { return "501", "20" }
	t.Cleanup(func() { execCommand, buildDockerImage, buildPodmanImage, hostUIDGID = oldExec, oldD, oldP, oldUID })
	return f
}

func TestRunNonInteractiveWithAllFlags(t *testing.T) {
	f := installFakes(t)
	dir := t.TempDir()
	var out bytes.Buffer
	yes := true
	err := Run(context.Background(), Options{
		RepoDir: dir, Template: "simple-loop", Agent: "claude-code", Sandbox: "docker", IssueTracker: "github-issues",
		CreateLabel: &yes, BuildImage: &yes, Out: &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agentgopher", "main.go")); err != nil {
		t.Fatal("scaffold missing")
	}
	if len(f.ghCalls) != 1 || !strings.HasPrefix(strings.Join(f.ghCalls[0], " "), "gh label create agentgopher") {
		t.Fatalf("gh calls = %v", f.ghCalls)
	}
	if len(f.dockerBuilds) != 1 || f.dockerBuilds[0].ImageName != docker.DefaultImageName(dir) || f.dockerBuilds[0].BuildArgs["AGENT_UID"] != "501" || f.dockerBuilds[0].ContextDir != filepath.Join(dir, ".agentgopher") {
		t.Fatalf("docker builds = %+v", f.dockerBuilds)
	}
	s := out.String()
	for _, want := range []string{"Scaffolding .agentgopher/", "Init complete! Image built successfully.", "Next steps:"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestRunNonInteractiveMissingFlagFails(t *testing.T) {
	installFakes(t)
	err := Run(context.Background(), Options{RepoDir: t.TempDir(), Template: "blank", Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "--agent is required in non-interactive mode") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunRejectsUnknownFlagValuesBeforePrompting(t *testing.T) {
	installFakes(t)
	cases := map[string]Options{
		`Unknown template "x". Available: blank, simple-loop`: {Template: "x"},
		`Unknown sandbox provider "x". Available: docker, podman`: {Sandbox: "x"},
		`Unknown issue tracker "x". Available: github-issues, beads, custom, jira, windshift`: {IssueTracker: "x"},
		`Unknown agent "x". Available: claude-code, pi`: {Agent: "x"},
	}
	for want, o := range cases {
		o.RepoDir = t.TempDir()
		o.Out = &bytes.Buffer{}
		err := Run(context.Background(), o)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want %q, got %v", want, err)
		}
	}
}

func TestRunInteractiveAsksEverythingIncludingTrackerQuestions(t *testing.T) {
	f := installFakes(t)
	dir := t.TempDir()
	p := &scriptedPrompter{answers: []string{"pi", "podman", "jira", "simple-loop", "project = X", "Closed", "n"}}
	err := Run(context.Background(), Options{RepoDir: dir, Interactive: true, Prompter: p, Out: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	wantAsked := []string{"Select an agent:", "Select a sandbox provider:", "Select an issue tracker:", "Select a template:",
		"JQL that selects the work items ready for the agent:", "Status to transition a finished work item to:", "Build the default Podman image now?"}
	if strings.Join(p.asked, "|") != strings.Join(wantAsked, "|") {
		t.Fatalf("asked = %q", p.asked)
	}
	prompt, _ := os.ReadFile(filepath.Join(dir, ".agentgopher", "prompt.md"))
	if !strings.Contains(string(prompt), "--jql 'project = X'") || !strings.Contains(string(prompt), "--status 'Closed'") {
		t.Fatalf("answers not applied:\n%s", prompt)
	}
	if len(f.ghCalls) != 0 || len(f.podmanBuilds) != 0 || len(f.dockerBuilds) != 0 {
		t.Fatalf("no label for jira and no build when declined: %+v", f)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agentgopher", "Containerfile")); err != nil {
		t.Fatal("podman writes a Containerfile")
	}
}

func TestRunCustomTrackerSkipsBuildEvenWhenFlagSet(t *testing.T) {
	f := installFakes(t)
	yes := true
	var out bytes.Buffer
	err := Run(context.Background(), Options{RepoDir: t.TempDir(), Template: "blank", Agent: "claude-code", Sandbox: "docker", IssueTracker: "custom", BuildImage: &yes, Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.dockerBuilds) != 0 || !strings.Contains(out.String(), "custom issue tracker isn't configured yet") {
		t.Fatalf("builds=%d out=%s", len(f.dockerBuilds), out.String())
	}
}

func TestRunLabelOnlyAskedForGitHub(t *testing.T) {
	installFakes(t)
	p := &scriptedPrompter{answers: []string{"claude-code", "docker", "beads", "blank", "n"}}
	if err := Run(context.Background(), Options{RepoDir: t.TempDir(), Interactive: true, Prompter: p, Out: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	for _, q := range p.asked {
		if strings.Contains(q, "label") {
			t.Fatalf("label question asked for beads: %q", p.asked)
		}
	}
}

func TestStdinPrompter(t *testing.T) {
	in := strings.NewReader("2\n\nn\ncustom value\n")
	var out bytes.Buffer
	p := NewStdinPrompter(in, &out)
	v, err := p.Select("Pick:", []Choice{{Value: "a", Label: "A"}, {Value: "b", Label: "B", Hint: "second"}}, "a")
	if err != nil || v != "b" {
		t.Fatalf("select = %q, %v", v, err)
	}
	v, _ = p.Select("Pick:", []Choice{{Value: "a", Label: "A"}}, "a")
	if v != "a" {
		t.Fatalf("empty line must pick the initial value, got %q", v)
	}
	c, _ := p.Confirm("Sure?", true)
	if c {
		t.Fatal("n must be false")
	}
	s, _ := p.Input("Value:", "default")
	if s != "custom value" {
		t.Fatalf("input = %q", s)
	}
	if _, err := p.Input("Value:", "x"); err == nil {
		t.Fatal("EOF must be an error")
	}
	if !strings.Contains(out.String(), "2) B (second)") {
		t.Fatalf("menu = %q", out.String())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/initcmd/ -run 'TestRun|TestStdinPrompter'`
Expected: FAIL, "undefined: Run".

- [ ] **Step 3: Write prompter.go**

```go
package initcmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Choice is one option in a Select prompt.
type Choice struct {
	Value string
	Label string
	Hint  string
}

// Prompter asks the user questions.
type Prompter interface {
	Select(message string, choices []Choice, initial string) (string, error)
	Confirm(message string, initial bool) (bool, error)
	Input(message, initial string) (string, error)
}

type stdinPrompter struct {
	in  *bufio.Scanner
	out io.Writer
}

// NewStdinPrompter prompts on out and reads one line per answer from in.
func NewStdinPrompter(in io.Reader, out io.Writer) Prompter {
	return &stdinPrompter{in: bufio.NewScanner(in), out: out}
}

func (p *stdinPrompter) readLine() (string, error) {
	if !p.in.Scan() {
		if err := p.in.Err(); err != nil {
			return "", err
		}
		return "", errors.New("input closed")
	}
	return strings.TrimSpace(p.in.Text()), nil
}

func (p *stdinPrompter) Select(message string, choices []Choice, initial string) (string, error) {
	fmt.Fprintln(p.out, message)
	initialIdx := 1
	for i, c := range choices {
		line := fmt.Sprintf("  %d) %s", i+1, c.Label)
		if c.Hint != "" {
			line += " (" + c.Hint + ")"
		}
		fmt.Fprintln(p.out, line)
		if c.Value == initial {
			initialIdx = i + 1
		}
	}
	for {
		fmt.Fprintf(p.out, "Choice [%d]: ", initialIdx)
		line, err := p.readLine()
		if err != nil {
			return "", err
		}
		if line == "" {
			return choices[initialIdx-1].Value, nil
		}
		n, err := strconv.Atoi(line)
		if err == nil && n >= 1 && n <= len(choices) {
			return choices[n-1].Value, nil
		}
		for _, c := range choices {
			if c.Value == line {
				return c.Value, nil
			}
		}
		fmt.Fprintln(p.out, "Please enter a number from the list.")
	}
}

func (p *stdinPrompter) Confirm(message string, initial bool) (bool, error) {
	hint := "Y/n"
	if !initial {
		hint = "y/N"
	}
	fmt.Fprintf(p.out, "%s [%s] ", message, hint)
	line, err := p.readLine()
	if err != nil {
		return false, err
	}
	switch strings.ToLower(line) {
	case "":
		return initial, nil
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func (p *stdinPrompter) Input(message, initial string) (string, error) {
	fmt.Fprintf(p.out, "%s [%s] ", message, initial)
	line, err := p.readLine()
	if err != nil {
		return "", err
	}
	if line == "" {
		return initial, nil
	}
	return line, nil
}
```

- [ ] **Step 4: Write run.go**

```go
package initcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/e11en/agentgopher/sandbox/docker"
	"github.com/e11en/agentgopher/sandbox/podman"
)

// Replaced in tests.
var (
	execCommand      = exec.CommandContext
	buildDockerImage = docker.BuildImage
	buildPodmanImage = podman.BuildImage
	hostUIDGID       = func() (string, string) {
		if runtime.GOOS == "windows" {
			return "", ""
		}
		return strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid())
	}
)

// Options configure Run. Empty strings and nil pointers mean "ask", or fail
// in non-interactive mode.
type Options struct {
	RepoDir      string
	ImageName    string
	Template     string
	Agent        string
	Model        string
	Sandbox      string
	IssueTracker string
	CreateLabel  *bool
	BuildImage   *bool
	Answers      map[string]string
	Interactive  bool
	Prompter     Prompter
	Out          io.Writer
}

// Run performs init: resolve choices, scaffold, label, build, next steps.
func Run(ctx context.Context, o Options) error {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.Answers == nil {
		o.Answers = map[string]string{}
	}
	if err := validateFlags(o); err != nil {
		return err
	}

	agentName, err := resolveSelect(o, o.Agent, "--agent", "Select an agent:", agentChoices(), "claude-code")
	if err != nil {
		return err
	}
	ag, _ := GetAgent(agentName)
	model := o.Model
	if model == "" {
		model = ag.DefaultModel
	}
	sandboxName, err := resolveSelect(o, o.Sandbox, "--sandbox", "Select a sandbox provider:", providerChoices(), "")
	if err != nil {
		return err
	}
	provider, _ := GetSandboxProvider(sandboxName)
	trackerName, err := resolveSelect(o, o.IssueTracker, "--issue-tracker", "Select an issue tracker:", trackerChoices(), "github-issues")
	if err != nil {
		return err
	}
	tracker, _ := GetIssueTracker(trackerName)
	templateName, err := resolveSelect(o, o.Template, "--template", "Select a template:", templateChoices(), "blank")
	if err != nil {
		return err
	}
	for _, q := range tracker.Questions {
		if _, ok := o.Answers[q.Key]; ok {
			continue
		}
		if !o.Interactive {
			// Defaults keep non-interactive runs working; --answer KEY=VALUE overrides.
			o.Answers[q.Key] = q.Default
			continue
		}
		a, err := o.Prompter.Input(q.Prompt, q.Default)
		if err != nil {
			return err
		}
		o.Answers[q.Key] = a
	}

	createLabel := false
	if tracker.Name == "github-issues" {
		createLabel, err = resolveConfirm(o, o.CreateLabel, "--create-label", `Create an "agentgopher" GitHub label? (Templates filter issues by this label)`)
		if err != nil {
			return err
		}
		if createLabel {
			// Best effort, as upstream: an existing label or a missing gh is not an error.
			cmd := execCommand(ctx, "gh", "label", "create", "agentgopher", "--description", "Issues for agentgopher to work on", "--color", "F9A825")
			cmd.Dir = o.RepoDir
			_ = cmd.Run()
		}
	}

	fmt.Fprintln(o.Out, "Scaffolding .agentgopher/ config directory...")
	if err := Scaffold(o.RepoDir, ScaffoldOptions{
		Agent: ag, Model: model, Template: templateName, CreateLabel: &createLabel,
		IssueTracker: &tracker, Sandbox: &provider, Answers: o.Answers,
	}); err != nil {
		return err
	}

	if tracker.Name == "custom" {
		fmt.Fprintln(o.Out, "Init complete! Your custom issue tracker isn't configured yet - see the steps below before building.")
	} else {
		build, err := resolveConfirm(o, o.BuildImage, "--build-image", fmt.Sprintf("Build the default %s image now?", provider.Label))
		if err != nil {
			return err
		}
		if build {
			imageName := o.ImageName
			if imageName == "" {
				imageName = docker.DefaultImageName(o.RepoDir)
			}
			fmt.Fprintf(o.Out, "Building %s image '%s'...\n", provider.Label, imageName)
			contextDir := filepath.Join(o.RepoDir, configDirName)
			if provider.Name == "podman" {
				err = buildPodmanImage(ctx, podman.BuildImageOptions{ImageName: imageName, ContextDir: contextDir})
			} else {
				err = buildDockerImage(ctx, docker.BuildImageOptions{ImageName: imageName, ContextDir: contextDir, BuildArgs: uidBuildArgs()})
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(o.Out, "Init complete! Image built successfully.")
		} else {
			fmt.Fprintf(o.Out, "Init complete! Run `agentgopher %s build-image` to build the %s image later.\n", provider.CLINamespace, provider.Label)
		}
	}

	for _, line := range NextStepsLines(templateName, tracker, ag) {
		fmt.Fprintln(o.Out, line)
	}
	return nil
}

// uidBuildArgs aligns the image UID/GID with the host (ADR 0014). Empty on Windows.
func uidBuildArgs() map[string]string {
	uid, gid := hostUIDGID()
	args := map[string]string{}
	if uid != "" {
		args["AGENT_UID"] = uid
	}
	if gid != "" {
		args["AGENT_GID"] = gid
	}
	return args
}

func validateFlags(o Options) error {
	if o.Template != "" {
		if _, ok := GetTemplate(o.Template); !ok {
			return fmt.Errorf("Unknown template %q. Available: %s", o.Template, names(templateChoices()))
		}
	}
	if o.Sandbox != "" {
		if _, ok := GetSandboxProvider(o.Sandbox); !ok {
			return fmt.Errorf("Unknown sandbox provider %q. Available: %s", o.Sandbox, names(providerChoices()))
		}
	}
	if o.IssueTracker != "" {
		if _, ok := GetIssueTracker(o.IssueTracker); !ok {
			return fmt.Errorf("Unknown issue tracker %q. Available: %s", o.IssueTracker, names(trackerChoices()))
		}
	}
	if o.Agent != "" {
		if _, ok := GetAgent(o.Agent); !ok {
			return fmt.Errorf("Unknown agent %q. Available: %s", o.Agent, names(agentChoices()))
		}
	}
	return nil
}

func resolveSelect(o Options, flagValue, flag, message string, choices []Choice, initial string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if !o.Interactive {
		return "", fmt.Errorf("%s is required in non-interactive mode (no TTY detected).", flag)
	}
	return o.Prompter.Select(message, choices, initial)
}

func resolveConfirm(o Options, flagValue *bool, flag, message string) (bool, error) {
	if flagValue != nil {
		return *flagValue, nil
	}
	if !o.Interactive {
		return false, fmt.Errorf("%s is required in non-interactive mode (no TTY detected).", flag)
	}
	return o.Prompter.Confirm(message, true)
}

func names(cs []Choice) string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Value
	}
	return strings.Join(out, ", ")
}

func agentChoices() []Choice {
	var cs []Choice
	for _, a := range ListAgents() {
		cs = append(cs, Choice{Value: a.Name, Label: a.Label, Hint: "Default model: " + a.DefaultModel})
	}
	return cs
}

func providerChoices() []Choice {
	var cs []Choice
	for _, p := range ListSandboxProviders() {
		cs = append(cs, Choice{Value: p.Name, Label: p.Label})
	}
	return cs
}

func trackerChoices() []Choice {
	var cs []Choice
	for _, t := range ListIssueTrackers() {
		cs = append(cs, Choice{Value: t.Name, Label: t.Label})
	}
	return cs
}

func templateChoices() []Choice {
	var cs []Choice
	for _, t := range ListTemplates() {
		cs = append(cs, Choice{Value: t.Name, Label: t.Name, Hint: t.Description})
	}
	return cs
}
```

golangci-lint flags error strings that start with a capital letter (`ST1005`). The messages above keep upstream's wording; if the linter complains, lowercase the first word in `validateFlags`, `resolveSelect` and `resolveConfirm` and update the tests in Step 1 to match.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/initcmd/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/initcmd/prompter.go internal/initcmd/run.go internal/initcmd/run_test.go
git commit -m "Add interactive init flow with flag overrides"
```

---

### Task 9: Wire the cobra commands

Port of the command definitions in `cli.ts`: `init` with its flags, and the `docker` and `podman` namespaces with `build-image` and `remove-image`.

**Files:**
- Create: `cmd/agentgopher/init.go`, `cmd/agentgopher/image.go`
- Modify: `cmd/agentgopher/main_test.go` (append tests), `README.md`

**Interfaces:**
- Consumes: `initcmd.Run`, `initcmd.NewStdinPrompter`, `docker.BuildImage`, `docker.RemoveImage`, `docker.DefaultImageName`, `podman.BuildImage`, `podman.RemoveImage`.
- Produces: subcommands `init`, `docker build-image`, `docker remove-image`, `podman build-image`, `podman remove-image`. Flags on `init`: `--template`, `--agent`, `--model`, `--sandbox`, `--issue-tracker`, `--image-name`, `--create-label true|false`, `--build-image true|false`, `--answer KEY=VALUE` (repeatable), `--yes` (non-interactive; equivalent to `--create-label true --build-image true` for the confirms, selects still need their flags). Flags on `build-image`: `--image-name`, `--dockerfile` (docker) or `--containerfile` (podman). Flags on `remove-image`: `--image-name`.
- Package-level `var cliBuildDocker = docker.BuildImage` and the podman and remove counterparts, replaced in tests.

- [ ] **Step 1: Write the failing tests (append to main_test.go)**

```go
func TestInitNonInteractiveScaffolds(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var built []docker.BuildImageOptions
	old := cliBuildDocker
	cliBuildDocker = func(_ context.Context, o docker.BuildImageOptions) error { built = append(built, o); return nil }
	t.Cleanup(func() { cliBuildDocker = old })

	out, err := runCLI(t, "init", "--template", "blank", "--agent", "claude-code", "--sandbox", "docker", "--issue-tracker", "windshift",
		"--answer", "WS_READY_STATUS=Ready", "--create-label", "false", "--build-image", "true", "--image-name", "custom:tag")
	if err != nil {
		t.Fatalf("err=%v out=%s", err, out)
	}
	if len(built) != 1 || built[0].ImageName != "custom:tag" {
		t.Fatalf("built = %+v", built)
	}
	prompt, _ := os.ReadFile(filepath.Join(dir, ".agentgopher", "prompt.md"))
	if !strings.Contains(string(prompt), "--status 'Ready'") {
		t.Fatalf("answer not applied:\n%s", prompt)
	}
}

func TestInitMissingFlagWithoutTTY(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := runCLI(t, "init", "--template", "blank")
	if err == nil || !strings.Contains(err.Error(), "--agent is required in non-interactive mode") {
		t.Fatalf("err = %v", err)
	}
}

func TestInitRejectsBadAnswerFlag(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := runCLI(t, "init", "--answer", "novalue")
	if err == nil || !strings.Contains(err.Error(), "--answer expects KEY=VALUE") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildImageRequiresConfigDir(t *testing.T) {
	t.Chdir(t.TempDir())
	called := false
	old := cliBuildDocker
	cliBuildDocker = func(context.Context, docker.BuildImageOptions) error { called = true; return nil }
	t.Cleanup(func() { cliBuildDocker = old })
	_, err := runCLI(t, "docker", "build-image")
	if err == nil || !strings.Contains(err.Error(), "No .agentgopher/ found. Run agentgopher init first.") || called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestBuildAndRemoveImageDefaults(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, ".agentgopher"), 0o755)
	t.Chdir(dir)
	var built docker.BuildImageOptions
	var removed string
	oldB, oldR, oldP := cliBuildDocker, cliRemoveDocker, cliBuildPodman
	cliBuildDocker = func(_ context.Context, o docker.BuildImageOptions) error { built = o; return nil }
	cliRemoveDocker = func(_ context.Context, name string) error { removed = name; return nil }
	var podmanBuilt podman.BuildImageOptions
	cliBuildPodman = func(_ context.Context, o podman.BuildImageOptions) error { podmanBuilt = o; return nil }
	t.Cleanup(func() { cliBuildDocker, cliRemoveDocker, cliBuildPodman = oldB, oldR, oldP })

	out, err := runCLI(t, "docker", "build-image", "--dockerfile", "Other.Dockerfile")
	if err != nil || !strings.Contains(out, "Build complete!") {
		t.Fatalf("err=%v out=%s", err, out)
	}
	if built.ImageName != docker.DefaultImageName(dir) || built.File != "Other.Dockerfile" || built.ContextDir != filepath.Join(dir, ".agentgopher") || built.BuildArgs["AGENT_UID"] == "" {
		t.Fatalf("built = %+v", built)
	}
	if _, err := runCLI(t, "docker", "remove-image", "--image-name", "x:y"); err != nil || removed != "x:y" {
		t.Fatalf("removed=%q err=%v", removed, err)
	}
	if _, err := runCLI(t, "podman", "build-image", "--containerfile", "CF"); err != nil || podmanBuilt.File != "CF" || len(podmanBuilt.BuildArgs) != 0 {
		t.Fatalf("podman = %+v err=%v", podmanBuilt, err)
	}
}
```

Add to the test file imports: `"context"`, `"os"`, `"path/filepath"`, `"github.com/e11en/agentgopher/sandbox/docker"`, `"github.com/e11en/agentgopher/sandbox/podman"`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/agentgopher/`
Expected: FAIL, "undefined: cliBuildDocker".

- [ ] **Step 3: Write init.go**

```go
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/e11en/agentgopher/internal/initcmd"
)

func init() {
	subcommands = append(subcommands, newInitCmd())
}

// stdinIsTerminal reports whether prompts can be shown.
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// triBool parses a "true"/"false" flag that may be absent.
func triBool(cmd *cobra.Command, name string) (*bool, error) {
	if !cmd.Flags().Changed(name) {
		return nil, nil
	}
	v, _ := cmd.Flags().GetString(name)
	switch v {
	case "true":
		t := true
		return &t, nil
	case "false":
		f := false
		return &f, nil
	}
	return nil, fmt.Errorf("--%s must be true or false", name)
}

func newInitCmd() *cobra.Command {
	var answers []string
	var yes bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold the .agentgopher/ config directory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			f := cmd.Flags()
			get := func(name string) string { v, _ := f.GetString(name); return v }
			createLabel, err := triBool(cmd, "create-label")
			if err != nil {
				return err
			}
			buildImage, err := triBool(cmd, "build-image")
			if err != nil {
				return err
			}
			if yes {
				t := true
				if createLabel == nil {
					createLabel = &t
				}
				if buildImage == nil {
					buildImage = &t
				}
			}
			parsed := map[string]string{}
			for _, a := range answers {
				k, v, ok := strings.Cut(a, "=")
				if !ok || k == "" {
					return errors.New("--answer expects KEY=VALUE")
				}
				parsed[k] = v
			}
			interactive := stdinIsTerminal() && !yes
			return initcmd.Run(cmd.Context(), initcmd.Options{
				RepoDir:      cwd,
				ImageName:    get("image-name"),
				Template:     get("template"),
				Agent:        get("agent"),
				Model:        get("model"),
				Sandbox:      get("sandbox"),
				IssueTracker: get("issue-tracker"),
				CreateLabel:  createLabel,
				BuildImage:   buildImage,
				Answers:      parsed,
				Interactive:  interactive,
				Prompter:     initcmd.NewStdinPrompter(os.Stdin, cmd.OutOrStdout()),
				Out:          cmd.OutOrStdout(),
			})
		},
	}
	f := cmd.Flags()
	f.String("template", "", "Template to scaffold (blank, simple-loop, sequential-reviewer, parallel-planner, parallel-planner-with-review)")
	f.String("agent", "", "Agent to use (e.g. claude-code)")
	f.String("model", "", "Model for the agent; defaults to the agent's default model")
	f.String("sandbox", "", "Sandbox provider (docker, podman)")
	f.String("issue-tracker", "", "Issue tracker (github-issues, beads, custom, jira, windshift)")
	f.String("image-name", "", "Image name (default: agentgopher:<repo dir>)")
	f.String("create-label", "", "true|false: create the \"agentgopher\" GitHub label (github-issues only)")
	f.String("build-image", "", "true|false: build the sandbox image now (ignored for the custom issue tracker)")
	f.StringArrayVar(&answers, "answer", nil, "Pre-answer a tracker question as KEY=VALUE (repeatable), e.g. JIRA_JQL='project = X'")
	f.BoolVar(&yes, "yes", false, "Non-interactive: answer yes to confirmations and use defaults for tracker questions")
	return cmd
}
```

- [ ] **Step 4: Write image.go**

```go
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/e11en/agentgopher/sandbox/docker"
	"github.com/e11en/agentgopher/sandbox/podman"
)

// Replaced in tests.
var (
	cliBuildDocker  = docker.BuildImage
	cliRemoveDocker = docker.RemoveImage
	cliBuildPodman  = podman.BuildImage
	cliRemovePodman = podman.RemoveImage
)

func init() {
	subcommands = append(subcommands, newDockerCmd(), newPodmanCmd())
}

func requireConfigDir(cwd string) error {
	if _, err := os.Stat(filepath.Join(cwd, configDir)); err != nil {
		return errors.New("No .agentgopher/ found. Run agentgopher init first.")
	}
	return nil
}

func imageName(cmd *cobra.Command, cwd string) string {
	if v, _ := cmd.Flags().GetString("image-name"); v != "" {
		return v
	}
	return docker.DefaultImageName(cwd)
}

// uidBuildArgs aligns the image UID/GID with the host (ADR 0014). Empty on Windows.
func uidBuildArgs() map[string]string {
	if runtime.GOOS == "windows" {
		return nil
	}
	return map[string]string{"AGENT_UID": strconv.Itoa(os.Getuid()), "AGENT_GID": strconv.Itoa(os.Getgid())}
}

func newDockerCmd() *cobra.Command {
	root := &cobra.Command{Use: "docker", Short: "Docker sandbox commands"}
	build := &cobra.Command{
		Use:   "build-image",
		Short: "Build the sandbox image from .agentgopher/Dockerfile",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, _ := os.Getwd()
			if err := requireConfigDir(cwd); err != nil {
				return err
			}
			name := imageName(cmd, cwd)
			file, _ := cmd.Flags().GetString("dockerfile")
			fmt.Fprintf(cmd.OutOrStdout(), "Building Docker image '%s'...\n", name)
			if err := cliBuildDocker(cmd.Context(), docker.BuildImageOptions{ImageName: name, ContextDir: filepath.Join(cwd, configDir), File: file, BuildArgs: uidBuildArgs()}); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Build complete!")
			return nil
		},
	}
	build.Flags().String("image-name", "", "Docker image name")
	build.Flags().String("dockerfile", "", "Path to a custom Dockerfile (build context becomes the current directory)")
	remove := &cobra.Command{
		Use:   "remove-image",
		Short: "Remove the sandbox image",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, _ := os.Getwd()
			name := imageName(cmd, cwd)
			fmt.Fprintf(cmd.OutOrStdout(), "Removing Docker image '%s'...\n", name)
			if err := cliRemoveDocker(cmd.Context(), name); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Image removed.")
			return nil
		},
	}
	remove.Flags().String("image-name", "", "Docker image name")
	root.AddCommand(build, remove)
	return root
}

func newPodmanCmd() *cobra.Command {
	root := &cobra.Command{Use: "podman", Short: "Podman sandbox commands"}
	build := &cobra.Command{
		Use:   "build-image",
		Short: "Build the sandbox image from .agentgopher/Containerfile",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, _ := os.Getwd()
			if err := requireConfigDir(cwd); err != nil {
				return err
			}
			name := imageName(cmd, cwd)
			file, _ := cmd.Flags().GetString("containerfile")
			fmt.Fprintf(cmd.OutOrStdout(), "Building Podman image '%s'...\n", name)
			// Podman aligns UIDs at run time with --userns=keep-id, so no build args.
			if err := cliBuildPodman(cmd.Context(), podman.BuildImageOptions{ImageName: name, ContextDir: filepath.Join(cwd, configDir), File: file}); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Build complete!")
			return nil
		},
	}
	build.Flags().String("image-name", "", "Podman image name")
	build.Flags().String("containerfile", "", "Path to a custom Containerfile (build context becomes the current directory)")
	remove := &cobra.Command{
		Use:   "remove-image",
		Short: "Remove the sandbox image",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, _ := os.Getwd()
			name := imageName(cmd, cwd)
			fmt.Fprintf(cmd.OutOrStdout(), "Removing Podman image '%s'...\n", name)
			if err := cliRemovePodman(cmd.Context(), name); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Image removed.")
			return nil
		},
	}
	remove.Flags().String("image-name", "", "Podman image name")
	root.AddCommand(build, remove)
	return root
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./... && go vet ./... && golangci-lint run ./...`
Expected: PASS and no lint output. `TestInitNonInteractiveScaffolds` runs without a TTY, so `stdinIsTerminal()` is false and the flags carry every answer.

- [ ] **Step 6: Smoke test the binary by hand and update the README**

```bash
go build -o /tmp/agentgopher ./cmd/agentgopher
tmp=$(mktemp -d) && git -C "$tmp" init -q
(cd "$tmp" && /tmp/agentgopher init --template simple-loop --agent claude-code --sandbox docker --issue-tracker jira --create-label false --build-image false && cat .agentgopher/prompt.md | head -8 && cd .agentgopher && go mod tidy && go vet .)
```
Expected: the scaffold is written, the prompt shows the `acli jira workitem search` command with the default JQL, and `go vet` passes inside `.agentgopher` (this needs a published module version; with a `dev` build run `go mod edit -replace github.com/e11en/agentgopher=<path to this checkout>` first).

Add to `README.md` under the status line:
```markdown
## CLI

```bash
go install github.com/e11en/agentgopher/cmd/agentgopher@latest
agentgopher init            # scaffold .agentgopher/ (asks for agent, sandbox, issue tracker, template)
agentgopher docker build-image
go run -C .agentgopher .    # run the scaffolded orchestration
```

Issue trackers: GitHub Issues, Beads, Jira (`acli`), Windshift (`ws`), or custom.
```

- [ ] **Step 7: Commit and push**

```bash
git add cmd/agentgopher/ README.md
git commit -m "Add init, build-image and remove-image commands"
git push origin main
gh run watch --repo e11en/agentgopher --exit-status
```

---

## Self-review notes

- Spec coverage for phase 6: CLI with `init`, `docker`/`podman build-image` and `remove-image` (Tasks 1, 7, 9); five templates embedded with `embed.FS`, each self-contained with its own `go.mod` so `go run -C .agentgopher .` works in non-Go repos (Task 4, "Go design decisions"); registries for agents, issue trackers and sandbox providers (Tasks 2, 3); Jira and Windshift with the commands, env vars, `acli` login hook and the Windshift status questions from the "Issue trackers" section (Tasks 3, 5, 8); ADR 0014 UID alignment in the Dockerfiles and `build-image` (Tasks 3, 7, 9); `.env.example`, prompt files, `CODING_STANDARDS.md` and template argument substitution (Task 5).
- Deviations from upstream, on purpose: no `.ts`/`.mts` detection, no package manager detection, no zod install offer (the Go templates have no host dependency); the `custom` tracker setup doc drops Jira from its examples because Jira is now built in; tracker questions default silently in non-interactive mode instead of failing, so `--yes` plus the select flags is enough for CI; the planner templates decode `<plan>` JSON themselves via `OutputString` instead of a typed output helper, which keeps them independent of how phase 5 shaped generics.
- Cross-plan assumptions the compiler will check in Task 4: `agentgopher.CreateSandbox`, `CreateSandboxOptions`, `Sandbox.Run`, `SandboxRunOptions`, `OutputString`, `RunResult.Output`, `RunOptions.Output`, `docker.New(nil)`, `agent.Pi/Codex/Cursor/OpenCode/Copilot`. If phase 5 named any of these differently, change the template, then the `modelCallPattern` and marker strings in Task 5 if the agent constructor spelling changed.
- Type consistency: `Question.Key` placeholders live only inside `IssueTrackerEntry.TemplateArgs` values and are resolved by `ResolveTemplateArgs` before `substituteTemplateArgs`; `ISSUE_TRACKER_LOGIN_COMMAND` is read from the resolved args in `Scaffold` and inserted by `rewriteMain`; `initcmd.Options.Answers` flows unchanged into `ScaffoldOptions.Answers`.
- Review Focus: item 1 (double init) Task 5; item 2 (quotes and `$` in answers) Task 5 `TestResolveTemplateArgsEscapesSingleQuotes`; item 3 (pi plus podman rewrite) Task 5; item 4 (custom tracker ignores `--build-image`) Task 8; item 5 (`build-image` without config dir) Task 9.
- Not verified at implementation time, listed in Task 3: comments in `acli jira workitem view --json`, case handling of `ws task ls -s`, the layout of the `ws` release tarball.
