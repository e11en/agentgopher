# agentgopher

Orchestrate sandboxed AI coding agents from Go. A port of
[sandcastle](https://github.com/mattpocock/sandcastle) with Jira and Windshift
as issue trackers and a Kubernetes sandbox provider.

Status: phases 1 to 4 of `docs/plan.md` are done. `Run` supports the head,
merge-to-head and branch strategies with the no-sandbox, Docker and Podman
providers, and `CreateWorktree` gives you a long-lived worktree. `Interactive`, `CreateSandbox` and the CLI follow in later phases.

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

See `docs/plan.md` for the roadmap.
