# agentgopher: port plan

agentgopher is a Go port of [mattpocock/sandcastle](https://github.com/mattpocock/sandcastle), pinned to commit `7a4e7f7` (npm `@ai-hero/sandcastle` 0.12.0). The port keeps feature parity with sandcastle and adds two things:

1. Jira and Windshift as issue trackers, next to GitHub Issues and Beads.
2. A Kubernetes sandbox provider, plus a container image and manifests to run agentgopher itself on GKE.

## What sandcastle is

A library plus a small CLI. The caller writes a script (`.sandcastle/main.mts`) that calls `run()`. sandcastle then:

1. Starts a sandbox (container, VM, or the host itself).
2. Puts the repo in the sandbox, on a branch picked by the branch strategy (head, merge-to-head, or named branch, via git worktrees in `.sandcastle/worktrees/`).
3. Runs an agent CLI (Claude Code, Codex, ...) for up to N iterations, with a prompt that can hold `{{KEY}}` arguments and `` !`cmd` `` shell expressions.
4. Stops on a completion signal (`<promise>COMPLETE</promise>`) or a timeout, and optionally parses structured output from an XML tag.
5. Collects the commits and merges them back to the host branch.

Issue trackers are not a runtime component. `sandcastle init` writes three shell commands (`LIST_TASKS_COMMAND`, `VIEW_TASK_COMMAND`, `CLOSE_TASK_COMMAND`) into the generated prompts and installs the tracker CLI into the sandbox Dockerfile. The agent runs those commands itself. Adding Jira and Windshift is therefore an `init` change, not a core change.

## Feature inventory and Go mapping

| sandcastle (TS) | agentgopher (Go) | Notes |
|---|---|---|
| `run()` | `agentgopher.Run(ctx, RunOptions)` | Iterations, completion signal, idle and completion timeouts, per-step `Timeouts`, logging, `copyToWorktree`, hooks |
| `interactive()` | `agentgopher.Interactive(ctx, InteractiveOptions)` | Attaches the terminal to the agent in the sandbox |
| `createSandbox()` | `agentgopher.CreateSandbox(ctx, ...)` returning `*Sandbox` with `Run`, `Interactive`, `Exec`, `Close` | Long-lived sandbox reused across runs |
| `createWorktree()` | `agentgopher.CreateWorktree(ctx, ...)` returning `*Worktree` | Worktree locking and reuse (ADR 0003, 0007) |
| `RunResult.resume()` / `fork()` | `(*RunResult).Resume(ctx, ...)` / `Fork(ctx, ...)` | Session resume and fork, one iteration each (ADR 0011, 0018) |
| `Output.object({tag, schema})` / `Output.string` | `agentgopher.OutputObject[T](tag, validate)` / `OutputString(tag)` | Standard Schema becomes `json.Unmarshal` into `T` plus an optional `func(T) error` |
| `AbortSignal` | `context.Context` | Cancellation everywhere |
| Effect services and typed errors | Plain interfaces and `errors.Is`/`errors.As` on exported error types | `CwdError`, `StructuredOutputError`, the errors in `errors.ts` |
| `onAgentStreamEvent` | `Logging.OnAgentStreamEvent func(AgentStreamEvent)` | Text and tool-call events, log-to-file mode only |
| Agent providers: `claudeCode`, `codex`, `copilot`, `cursor`, `opencode`, `pi` | package `agent`: `agent.ClaudeCode(model, opts)`, ... | Each builds the CLI command and parses its stream-json output |
| Session store (`transferClaudeSession`, ...) | package `session` | Copies agent sessions between host and sandbox |
| Sandbox providers: docker, podman (bind-mount), vercel, daytona (isolated), no-sandbox | package `sandbox/...`: `docker`, `podman`, `vercel`, `daytona`, `nosandbox` | Docker and Podman shell out to the CLI, as sandcastle does |
| `createBindMountSandboxProvider`, `createIsolatedSandboxProvider` | `sandbox.BindMountProvider` and `sandbox.IsolatedProvider` interfaces | For custom providers |
| Sync in/out for isolated providers | `internal/sync` | git bundle in, commits out |
| Prompt substitution and expansion | `internal/prompt` | `{{KEY}}`, built-in arguments, `` !`cmd` `` evaluated in the sandbox, fail fast (ADR 0020) |
| Display (terminal mode, spinners) and run logs | `internal/display` | Terminal mode and log-to-file mode in `.agentgopher/logs/` |
| CLI: `init`, `docker build-image/remove-image`, `podman build-image/remove-image` | `cmd/agentgopher` with the same subcommands | |
| `init` templates: blank, simple-loop, sequential-reviewer, parallel-planner, parallel-planner-with-review | `templates/` via `embed.FS`, each with a `main.go` | Templates stay self-contained (ADR 0009) |
| `init` registries: agents, issue trackers (github-issues, beads, custom), sandbox providers | Same registries in `internal/initcmd` | Plus `jira` and `windshift` |
| Config directory `.sandcastle/` | `.agentgopher/` | Same layout: `.env`, `Dockerfile`, prompts, `worktrees/`, `logs/` |

## Go design decisions

- **Module**: `github.com/e11en/agentgopher`, Go 1.25. The root package holds the public API; `agent`, `sandbox/...` and `session` are public subpackages; everything else lives under `internal/`.
- **Dependencies**: standard library first. `cobra` for the CLI, `client-go` for the Kubernetes provider, and nothing else unless a provider needs an SDK. Vercel has no Go SDK, so that provider calls its REST API. Daytona has to be checked: Go SDK if one exists, REST otherwise.
- **Templates in non-Go repos**: the target repo is often not a Go project. `init` therefore writes `.agentgopher/go.mod` next to `main.go`, so `go run -C .agentgopher .` works in any repo (the templates pass `Cwd: ".."` so the repo root stays the host repo dir). Phase 5 checks whether the dot directory is a problem.
- **Tests**: port the vitest tests per module as Go table tests, including the test sandbox providers (`test-bind-mount`, `test-isolated`), so parity is measurable per module.
- **Docs**: the ADRs move along as `docs/adr/` with the same numbers. Port-specific decisions continue from 0021.

## Issue trackers

All four meet sandcastle's criteria from `docs/agents/adding-an-issue-tracker.md`: official CLI, non-interactive auth, JSON output, installable in a Debian container.

| Tracker | CLI | List | View | Close | Auth |
|---|---|---|---|---|---|
| GitHub Issues | `gh` | `gh issue list --label agentgopher --json ...` | `gh issue view <ID>` | `gh issue close <ID> --comment ...` | `GH_TOKEN` |
| Beads | `bd` | `bd ready --json` | `bd show <ID>` | `bd close <ID>` | none |
| Jira | `acli` (Atlassian CLI) | `acli jira workitem search --jql "<JQL>" --json` | `acli jira workitem view <ID> --json` | `acli jira workitem transition --key <ID> --status Done` | `JIRA_SITE`, `JIRA_EMAIL`, `JIRA_API_TOKEN`, piped into `acli jira auth login` from an `onSandboxReady` hook |
| Windshift | `ws` | `ws task ls --status <ready status> --all` | `ws task get <ID>` | `ws task move <ID> <done status>` | `WS_URL`, `WS_TOKEN`, `WS_WORKSPACE` |

Open points, to verify against `--help` of the installed versions in phase 5:

- Jira: the default JQL (label `agentgopher` and status `To Do`?), and whether the done status is called the same on every board.
- Jira: `acli` has no plain env-var auth, hence the login hook. Its flags must be confirmed.
- Windshift: there is no `close` command, so close means `move` to a done status. The status names are configured per workspace, so `init` asks for them. The CLI is pre-1.0 and its API can change between releases.

## Kubernetes and GKE

There are two parts, and they can be built separately.

**A. Kubernetes sandbox provider** (`sandbox/kubernetes`). This is an isolated provider: one Pod per sandbox, commands run through the exec API (`client-go`, SPDY/WebSocket), code goes in and commits come out through the same sync as Vercel and Daytona. Options: namespace, image, resources, service account, node selector, and Pod TTL as a safety net. Works on any cluster, GKE included. Upstream rejects new built-in providers (`.out-of-scope/built-in-sandbox-providers.md`), so this is a deliberate departure from upstream.

**A2. GKE Agent Sandbox provider** (`sandbox/agentsandbox`). The same isolated contract on top of the `kubernetes-sigs/agent-sandbox` Go SDK: a `SandboxClaim` against a warm pool gives a gVisor-isolated Pod in under a second, commands go through the `sandboxd` gRPC process service and files through its REST filesystem. The sandbox image is the runner image with `sandboxd` injected by an initContainer (`deploy/k8s/sandboxtemplate.yaml`). This is the provider the scaffolded templates use inside a cluster.

**B. agentgopher itself on GKE**. A container image (distroless or Debian with `git` and `go`) that runs a script as a Kubernetes Job, started with `agentgopher remote run` (see Decisions). In the cluster there is no host repo with a checked-out branch, so this mode works as follows:

1. Clone the repo into the Job's ephemeral volume.
2. Use the `branch` strategy, not `merge-to-head`.
3. Push the branch and open a pull request through `gh`.

That flow is new compared to sandcastle. It needs a small `PushAndOpenPR` step after `Run`, not a change in the core.

Infrastructure (a new GKE cluster, Artifact Registry, Workload Identity, Secret Manager for the agent and tracker tokens) is managed with Terraform under `deploy/terraform/`. Kubernetes manifests (Job template, RBAC that lets the Job create sandbox Pods) live under `deploy/k8s/`.

## Phases

Each phase ends with green tests and a working example.

1. **Skeleton**: `go.mod`, the package layout from this plan, CI on GitHub Actions (`go test`, `go vet`, `golangci-lint`), README, and the `docs/adr/` from upstream.
2. **Core loop**: `Run` with the `nosandbox` provider, the `head` strategy, and the `ClaudeCode` agent. Iterations, completion signal, idle and completion timeouts, prompt substitution and expansion, run log, stream events.
3. **Worktrees and bind-mount**: `merge-to-head` and `branch` strategies, `CreateWorktree`, worktree locking, `copyToWorktree`, host and sandbox hooks, `docker` and `podman` providers, Windows mount handling.
4. **Isolated providers**: sync in and out, `vercel`, `daytona`, the `IsolatedProvider` interface for custom providers.
5. **Full API**: `CreateSandbox`, `Interactive`, structured output, session store with resume and fork, and the remaining agents (codex, copilot, cursor, opencode, pi).
6. **CLI and init**: `init` with all templates and registries, issue trackers including Jira and Windshift, `docker`/`podman build-image` and `remove-image`.
7. **Kubernetes**: the `kubernetes` sandbox provider (part A), then the image, Job template, `agentgopher remote run`, push-and-PR flow and Terraform for GKE (part B).
8. **Agent Sandbox**: gVisor by default for the `kubernetes` provider, the `agentsandbox` provider on the GKE Agent Sandbox SDK (part A2), warm pool manifests, the managed add-on in Terraform.

Phases 2 to 5 follow the upstream layering. Phase 7 can start after phase 4, because it only needs the isolated sync.

## Decisions

- **Trigger on GKE**: on demand, through a CLI command: `agentgopher remote run --repo <url> --script <path>`. The command uses the caller's kubeconfig (`gcloud container clusters get-credentials`) to create a Kubernetes Job in the cluster, then streams its logs and returns the PR links. Each Job lists the ready tasks, works through them, opens PRs and exits, which is what running the script by hand does today. The same command works from a laptop, GitHub Actions or any other script. Nothing keeps running in the cluster between runs. A schedule (CronJob) and an HTTP or webhook trigger are out of scope for now.
- **Cluster**: a new GKE cluster, created with Terraform in `deploy/terraform/`.
- **gVisor by default**: every sandbox Pod the `kubernetes` provider creates gets `runtimeClassName: gvisor`; `Options.RuntimeClass = kubernetes.NoRuntimeClass` opts out explicitly. The agent runs code it did not write, so kernel isolation is the default, not an option.
- **GKE Agent Sandbox in the cluster**: inside a `remote run` Job the templates use `sandbox/agentsandbox`, because it adds kernel isolation, warm pools and a managed network policy on top of plain Pods. `sandbox/kubernetes` stays as the fallback for clusters without the add-on.
- **Upstream**: frozen at `7a4e7f7`. New upstream features get ported by hand when they are worth it. The two codebases are expected to drift apart.
