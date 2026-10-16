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
