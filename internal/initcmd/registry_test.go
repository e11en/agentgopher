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
