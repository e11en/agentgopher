package initcmd

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestEveryTemplateHasRequiredFilesAndMarkers(t *testing.T) {
	for _, tmpl := range ListTemplates() {
		dir := "templates/" + tmpl.Name
		entries, err := fs.ReadDir(templatesFS, dir)
		if err != nil {
			t.Fatalf("%s: %v", tmpl.Name, err)
		}
		names := map[string]bool{}
		for _, e := range entries {
			names[e.Name()] = true
		}
		for _, want := range []string{"main.go", "go.mod.tmpl", "template.json"} {
			if !names[want] {
				t.Fatalf("%s lacks %s", tmpl.Name, want)
			}
		}
		main, _ := fs.ReadFile(templatesFS, dir+"/main.go")
		for _, want := range []string{
			`agent.ClaudeCode("`,
			`docker.New(nil)`,
			`"github.com/e11en/agentgopher/sandbox/docker"`,
			`SandboxOnSandboxReady: []agentgopher.SandboxHook{`,
		} {
			if !strings.Contains(string(main), want) {
				t.Fatalf("%s/main.go lacks %q", tmpl.Name, want)
			}
		}
		if strings.Contains(string(main), "/internal/") {
			t.Fatalf("%s/main.go imports internal packages (ADR 0009)", tmpl.Name)
		}
		gomod, _ := fs.ReadFile(templatesFS, dir+"/go.mod.tmpl")
		if !strings.Contains(string(gomod), "require github.com/e11en/agentgopher {{AGENTGOPHER_VERSION}}") {
			t.Fatalf("%s/go.mod.tmpl = %q", tmpl.Name, gomod)
		}
	}
}

func TestPromptFilesUseTrackerPlaceholders(t *testing.T) {
	cases := map[string][]string{
		"blank/prompt.md":                               {"{{LIST_TASKS_COMMAND}}"},
		"simple-loop/prompt.md":                         {"!`{{LIST_TASKS_COMMAND}}`", "{{CLOSE_TASK_COMMAND}}", "<promise>COMPLETE</promise>"},
		"sequential-reviewer/implement-prompt.md":       {"!`{{LIST_TASKS_COMMAND}}`", "{{CLOSE_TASK_COMMAND}}"},
		"sequential-reviewer/review-prompt.md":          {"{{BRANCH}}", "{{TARGET_BRANCH}}", "@.agentgopher/CODING_STANDARDS.md"},
		"parallel-planner/plan-prompt.md":               {"!`{{LIST_TASKS_COMMAND}}`", "<plan>"},
		"parallel-planner/implement-prompt.md":          {"{{TASK_ID}}", "{{ISSUE_TITLE}}", "{{BRANCH}}", "{{VIEW_TASK_COMMAND}}"},
		"parallel-planner/merge-prompt.md":              {"{{BRANCHES}}", "{{ISSUES}}", "{{CLOSE_TASK_COMMAND}}"},
		"parallel-planner-with-review/review-prompt.md": {"{{BRANCH}}", "@.agentgopher/CODING_STANDARDS.md"},
	}
	for file, wants := range cases {
		b, err := fs.ReadFile(templatesFS, "templates/"+file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		s := string(b)
		for _, w := range wants {
			if !strings.Contains(s, w) {
				t.Fatalf("%s lacks %q", file, w)
			}
		}
		if strings.Contains(s, "sandcastle") || strings.Contains(s, "Sandcastle") {
			t.Fatalf("%s still mentions sandcastle", file)
		}
	}
}

// The templates run with `go run -C .agentgopher .`, so every Run and
// CreateSandbox must point Cwd at the repo root and prompt paths must be
// relative to the config dir.
var (
	cwdField     = regexp.MustCompile(`Cwd:\s+"\.\.",`)
	sandboxField = regexp.MustCompile(`Sandbox:\s+docker\.New`)
)

func TestTemplatesResolvePathsFromConfigDir(t *testing.T) {
	for _, tpl := range ListTemplates() {
		b, err := templatesFS.ReadFile("templates/" + tpl.Name + "/main.go")
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		literals := strings.Count(src, "agentgopher.RunOptions{") + strings.Count(src, "agentgopher.CreateSandboxOptions{")
		if got := len(cwdField.FindAllString(src, -1)); got != literals {
			t.Errorf("%s: %d option literals but %d Cwd: \"..\" fields", tpl.Name, literals, got)
		}
		if !strings.Contains(src, "KUBERNETES_SERVICE_HOST") || !strings.Contains(src, "agentsandbox.New(nil)") || sandboxField.MatchString(src) {
			t.Errorf("%s: must pick the sandbox provider via sandboxProvider() so remote runs work", tpl.Name)
		}
		if strings.Contains(src, `"./.agentgopher/`) {
			t.Errorf("%s: prompt paths must be relative to the config dir, not ./.agentgopher/", tpl.Name)
		}
	}
}
