package initcmd

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
