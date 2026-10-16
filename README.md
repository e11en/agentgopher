# agentgopher

Orchestrate sandboxed AI coding agents from Go. A port of
[sandcastle](https://github.com/mattpocock/sandcastle) with Jira and Windshift
as issue trackers and a Kubernetes sandbox provider.

Status: phases 1 to 6 of `docs/plan.md` are done. `Run`, `CreateSandbox`,
`CreateWorktree` and `Interactive` work with the no-sandbox, Docker, Podman,
Vercel and Daytona providers; Claude Code, Codex and Pi sessions can be
resumed and forked; structured output is extracted with `OutputObject` and
`OutputString`. Kubernetes and GKE follow in phase 7.

Examples: `go run ./examples/simple /path/to/repo` (host) and
`go run ./examples/docker /path/to/repo` (Docker, see the file header for the
image build).

Isolated providers (phase 4): `sandbox/vercel` drives the official `sandbox`
CLI (`pnpm i -g sandbox`, then `sandbox login` or `VERCEL_TOKEN`), and
`sandbox/daytona` uses the official Daytona Go SDK (`DAYTONA_API_KEY`). The
repo is shipped in as a git bundle and the agent's commits come back as
patches; failed patch applications are preserved under
`.agentgopher/patches/` with recovery commands on stderr. Custom providers
implement `sandbox.IsolatedProviderConfig`.

Run the example: `go run ./examples/isolated vercel /path/to/repo`.

## CLI

```bash
export GOPRIVATE=github.com/e11en   # the module is private, go needs git auth for it
go install github.com/e11en/agentgopher/cmd/agentgopher@latest
agentgopher init            # scaffold .agentgopher/ (asks for agent, sandbox, issue tracker, template)
agentgopher docker build-image
go run -C .agentgopher .    # run the scaffolded orchestration
```

Issue trackers: GitHub Issues, Beads, Jira (`acli`), Windshift (`ws`), or custom.

See `docs/plan.md` for the roadmap.
