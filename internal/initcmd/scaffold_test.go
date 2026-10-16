package initcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/version"
)

func mustAgent(t *testing.T, name string) AgentEntry {
	t.Helper()
	a, ok := GetAgent(name)
	if !ok {
		t.Fatal(name)
	}
	return a
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func scaffoldTo(t *testing.T, o ScaffoldOptions) string {
	t.Helper()
	dir := t.TempDir()
	if o.Agent.Name == "" {
		o.Agent = mustAgent(t, "claude-code")
	}
	if o.Model == "" {
		o.Model = o.Agent.DefaultModel
	}
	if err := Scaffold(dir, o); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, ".agentgopher")
}

func TestScaffoldDefaultsToBlankGitHubDocker(t *testing.T) {
	version.Version = "v1.2.3"
	cfg := scaffoldTo(t, ScaffoldOptions{})
	for _, f := range []string{"main.go", "go.mod", "prompt.md", "Dockerfile", ".gitignore", ".env.example"} {
		if _, err := os.Stat(filepath.Join(cfg, f)); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg, "template.json")); err == nil {
		t.Fatal("template.json must not be copied")
	}
	if _, err := os.Stat(filepath.Join(cfg, "go.mod.tmpl")); err == nil {
		t.Fatal("go.mod.tmpl must be renamed")
	}
	if got := read(t, filepath.Join(cfg, ".gitignore")); got != ".env\nlogs/\nworktrees/\n" {
		t.Fatalf(".gitignore = %q", got)
	}
	gomod := read(t, filepath.Join(cfg, "go.mod"))
	if !strings.Contains(gomod, "require github.com/e11en/agentgopher v1.2.3") {
		t.Fatalf("go.mod = %q", gomod)
	}
	dockerfile := read(t, filepath.Join(cfg, "Dockerfile"))
	if strings.Contains(dockerfile, "{{ISSUE_TRACKER_TOOLS}}") || !strings.Contains(dockerfile, "apt-get install -y gh") {
		t.Fatal("tracker tools not substituted into Dockerfile")
	}
	env := read(t, filepath.Join(cfg, ".env.example"))
	if !strings.Contains(env, "CLAUDE_CODE_OAUTH_TOKEN=") || !strings.Contains(env, "GH_TOKEN=") || !strings.HasSuffix(env, "\n") {
		t.Fatalf(".env.example = %q", env)
	}
	prompt := read(t, filepath.Join(cfg, "prompt.md"))
	if !strings.Contains(prompt, "gh issue list --state open --label agentgopher") || strings.Contains(prompt, "{{LIST_TASKS_COMMAND}}") {
		t.Fatalf("prompt.md = %q", prompt)
	}
}

func TestScaffoldDevVersionUsesPlaceholderVersion(t *testing.T) {
	version.Version = "dev"
	cfg := scaffoldTo(t, ScaffoldOptions{})
	if !strings.Contains(read(t, filepath.Join(cfg, "go.mod")), "require github.com/e11en/agentgopher v0.0.0") {
		t.Fatal("dev builds must write v0.0.0 so go mod tidy can replace it")
	}
}

func TestScaffoldRefusesExistingDir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".agentgopher"), 0o755)
	os.WriteFile(filepath.Join(dir, ".agentgopher", "keep.txt"), []byte("x"), 0o644)
	err := Scaffold(dir, ScaffoldOptions{Agent: mustAgent(t, "claude-code"), Model: "m"})
	if err == nil || !strings.Contains(err.Error(), ".agentgopher/ directory already exists") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".agentgopher", "keep.txt")); statErr != nil {
		t.Fatal("existing directory must be left untouched")
	}
}

func TestScaffoldUnknownTemplate(t *testing.T) {
	err := Scaffold(t.TempDir(), ScaffoldOptions{Agent: mustAgent(t, "claude-code"), Model: "m", Template: "nope"})
	if err == nil || !strings.Contains(err.Error(), `unknown template "nope"`) || !strings.Contains(err.Error(), "blank, simple-loop") {
		t.Fatalf("err = %v", err)
	}
}

func TestScaffoldRewritesAgentModelAndProvider(t *testing.T) {
	podman, _ := GetSandboxProvider("podman")
	cfg := scaffoldTo(t, ScaffoldOptions{Agent: mustAgent(t, "pi"), Model: "my-model", Template: "parallel-planner", Sandbox: &podman})
	main := read(t, filepath.Join(cfg, "main.go"))
	if strings.Contains(main, "ClaudeCode(") || strings.Contains(main, "docker") {
		t.Fatalf("main.go not rewritten:\n%s", main)
	}
	if strings.Count(main, `agent.Pi("my-model", nil)`) != 3 {
		t.Fatalf("expected 3 rewritten factory calls:\n%s", main)
	}
	if !strings.Contains(main, `"github.com/e11en/agentgopher/sandbox/podman"`) || !strings.Contains(main, "podman.New(nil)") {
		t.Fatalf("provider not rewritten:\n%s", main)
	}
	if _, err := os.Stat(filepath.Join(cfg, "Containerfile")); err != nil {
		t.Fatal("podman writes a Containerfile")
	}
	if _, err := os.Stat(filepath.Join(cfg, "Dockerfile")); err == nil {
		t.Fatal("podman must not write a Dockerfile")
	}
	if !strings.Contains(read(t, filepath.Join(cfg, "Containerfile")), "npm install -g @mariozechner/pi-coding-agent") {
		t.Fatal("pi Dockerfile not used")
	}
}

func TestScaffoldStripsLabelWhenDeclined(t *testing.T) {
	no := false
	cfg := scaffoldTo(t, ScaffoldOptions{Template: "simple-loop", CreateLabel: &no})
	if strings.Contains(read(t, filepath.Join(cfg, "prompt.md")), " --label agentgopher") {
		t.Fatal("label filter must be stripped")
	}
	cfg = scaffoldTo(t, ScaffoldOptions{Template: "simple-loop"})
	if !strings.Contains(read(t, filepath.Join(cfg, "prompt.md")), " --label agentgopher") {
		t.Fatal("label filter must be kept by default")
	}
}

func TestScaffoldJiraAppliesAnswersAndLoginHook(t *testing.T) {
	jira, _ := GetIssueTracker("jira")
	cfg := scaffoldTo(t, ScaffoldOptions{Template: "simple-loop", IssueTracker: &jira, Answers: map[string]string{
		"JIRA_JQL": `project = DEV AND status = "To Do" AND labels = agentgopher`,
	}})
	prompt := read(t, filepath.Join(cfg, "prompt.md"))
	if !strings.Contains(prompt, `acli jira workitem search --jql 'project = DEV AND status = "To Do" AND labels = agentgopher' --json`) {
		t.Fatalf("JQL not substituted:\n%s", prompt)
	}
	if !strings.Contains(prompt, `--status 'Done' --yes`) {
		t.Fatal("default done status not applied")
	}
	if strings.Contains(prompt, "{{JIRA") {
		t.Fatal("question placeholders left behind")
	}
	main := read(t, filepath.Join(cfg, "main.go"))
	want := `SandboxOnSandboxReady: []agentgopher.SandboxHook{{Command: "printf '%s' \"$JIRA_API_TOKEN\" | acli jira auth login --site \"$JIRA_SITE\" --email \"$JIRA_EMAIL\" --token"}, {Command: "npm install"}}`
	if !strings.Contains(main, want) {
		t.Fatalf("login hook not inserted:\n%s", main)
	}
	env := read(t, filepath.Join(cfg, ".env.example"))
	for _, k := range []string{"JIRA_SITE=", "JIRA_EMAIL=", "JIRA_API_TOKEN="} {
		if !strings.Contains(env, k) {
			t.Fatalf(".env.example lacks %s", k)
		}
	}
	if !strings.Contains(read(t, filepath.Join(cfg, "Dockerfile")), "apt-get install -y acli") {
		t.Fatal("acli not installed in Dockerfile")
	}
}

func TestResolveTemplateArgsEscapesSingleQuotes(t *testing.T) {
	w, _ := GetIssueTracker("windshift")
	args := ResolveTemplateArgs(w, map[string]string{"WS_DONE_STATUS": "Klaar 'echt'", "WS_READY_STATUS": "$HOME"})
	if args["CLOSE_TASK_COMMAND"] != `ws task move <ID> 'Klaar '\''echt'\'''` {
		t.Fatalf("CLOSE = %q", args["CLOSE_TASK_COMMAND"])
	}
	if args["LIST_TASKS_COMMAND"] != `ws task ls --status '$HOME' --all` {
		t.Fatalf("LIST = %q", args["LIST_TASKS_COMMAND"])
	}
}

func TestScaffoldWindshiftBlankLeavesHooksUnchanged(t *testing.T) {
	w, _ := GetIssueTracker("windshift")
	cfg := scaffoldTo(t, ScaffoldOptions{IssueTracker: &w})
	main := read(t, filepath.Join(cfg, "main.go"))
	if !strings.Contains(main, "SandboxOnSandboxReady: []agentgopher.SandboxHook{}") {
		t.Fatalf("windshift has no login hook, main.go must be unchanged:\n%s", main)
	}
	if !strings.Contains(read(t, filepath.Join(cfg, "prompt.md")), `ws task ls --status 'To Do' --all`) {
		t.Fatal("windshift defaults not applied")
	}
}

func TestScaffoldCustomTrackerWritesSetupDoc(t *testing.T) {
	custom, _ := GetIssueTracker("custom")
	podman, _ := GetSandboxProvider("podman")
	cfg := scaffoldTo(t, ScaffoldOptions{Template: "simple-loop", IssueTracker: &custom, Sandbox: &podman})
	doc := read(t, filepath.Join(cfg, SetupIssueTrackerDoc))
	for _, want := range []string{CustomListTasksSentinel, CustomViewTaskMarker, CustomCloseTaskMarker, CustomTrackerTools, "agentgopher podman build-image"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("setup doc lacks %q", want)
		}
	}
	if !strings.Contains(read(t, filepath.Join(cfg, "prompt.md")), CustomListTasksSentinel) {
		t.Fatal("sentinel not in prompt")
	}
}

func TestScaffoldSequentialReviewerFiles(t *testing.T) {
	cfg := scaffoldTo(t, ScaffoldOptions{Template: "sequential-reviewer"})
	for _, f := range []string{"implement-prompt.md", "review-prompt.md", "CODING_STANDARDS.md"} {
		if _, err := os.Stat(filepath.Join(cfg, f)); err != nil {
			t.Fatalf("missing %s", f)
		}
	}
	if strings.Contains(read(t, filepath.Join(cfg, "implement-prompt.md")), "{{TASK_ID}}") {
		t.Fatal("prompts without a runtime TASK_ID must not contain the placeholder")
	}
}
