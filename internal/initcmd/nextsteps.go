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
