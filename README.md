# agentgopher

Orchestrate sandboxed AI coding agents from Go. A port of
[sandcastle](https://github.com/mattpocock/sandcastle) with Jira and Windshift
as issue trackers and a Kubernetes sandbox provider.

Status: phases 1 to 3 of `docs/plan.md` are done. `Run` supports the head,
merge-to-head and branch strategies with the no-sandbox, Docker and Podman
providers, and `CreateWorktree` gives you a long-lived worktree. Isolated
providers, `Interactive`, `CreateSandbox` and the CLI follow in later phases.

Examples: `go run ./examples/simple /path/to/repo` (host) and
`go run ./examples/docker /path/to/repo` (Docker, see the file header for the
image build).

See `docs/plan.md` for the roadmap.
