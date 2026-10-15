# agentgopher

Orchestrate sandboxed AI coding agents from Go. A port of
[sandcastle](https://github.com/mattpocock/sandcastle) with Jira and Windshift
as issue trackers and a Kubernetes sandbox provider.

Status: phases 1 and 2 of `docs/plan.md` are done. `Run` works on the host with
the head branch strategy. Docker, Podman, worktrees and isolated providers follow
in the next phases.

Run the example: `go run ./examples/simple /path/to/repo`. Progress lands in
`.agentgopher/logs/main-worker.log` inside that repo.

```go
result, err := agentgopher.Run(ctx, agentgopher.RunOptions{
    Agent:      agent.ClaudeCode("claude-opus-4-8", nil),
    Sandbox:    nosandbox.New(nil),
    PromptFile: ".agentgopher/prompt.md",
})
```

See `docs/plan.md` for the roadmap.
