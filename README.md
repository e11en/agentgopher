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
