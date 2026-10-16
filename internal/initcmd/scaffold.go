package initcmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/e11en/agentgopher/internal/version"
)

const configDirName = ".agentgopher"

const gitignore = ".env\nlogs/\nworktrees/\n"

// ScaffoldOptions configure Scaffold. Nil pointers select the defaults:
// template blank, GitHub Issues, Docker, label created.
type ScaffoldOptions struct {
	Agent        AgentEntry
	Model        string
	Template     string
	CreateLabel  *bool
	IssueTracker *IssueTrackerEntry
	Sandbox      *SandboxProviderEntry
	// Answers map a Question.Key to the user's answer.
	Answers map[string]string
}

// Scaffold writes <repoDir>/.agentgopher from the chosen template.
func Scaffold(repoDir string, o ScaffoldOptions) error {
	templateName := o.Template
	if templateName == "" {
		templateName = "blank"
	}
	if _, ok := GetTemplate(templateName); !ok {
		names := make([]string, 0, len(templateOrder))
		for _, t := range ListTemplates() {
			names = append(names, t.Name)
		}
		return fmt.Errorf("unknown template %q. Available: %s", templateName, strings.Join(names, ", "))
	}
	tracker := issueTrackerRegistry[0]
	if o.IssueTracker != nil {
		tracker = *o.IssueTracker
	}
	provider := sandboxProviderRegistry[0]
	if o.Sandbox != nil {
		provider = *o.Sandbox
	}
	createLabel := o.CreateLabel == nil || *o.CreateLabel

	configDir := filepath.Join(repoDir, configDirName)
	if _, err := os.Stat(configDir); err == nil {
		return errors.New(".agentgopher/ directory already exists; remove it first if you want to re-initialize")
	}
	if err := os.Mkdir(configDir, 0o755); err != nil {
		return err
	}

	// Copy the template verbatim, except metadata and the go.mod template.
	src := "templates/" + templateName
	entries, err := fs.ReadDir(templatesFS, src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "template.json" || e.Name() == "go.mod.tmpl" {
			continue
		}
		b, err := templatesFS.ReadFile(src + "/" + e.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(configDir, e.Name()), b, 0o644); err != nil {
			return err
		}
	}

	gomod, err := templatesFS.ReadFile(src + "/go.mod.tmpl")
	if err != nil {
		return err
	}
	moduleVersion := version.Version
	if moduleVersion == "dev" {
		// Not a valid module version; the next steps tell the user to run go get.
		moduleVersion = "v0.0.0"
	}
	if err := os.WriteFile(filepath.Join(configDir, "go.mod"), []byte(strings.ReplaceAll(string(gomod), "{{AGENTGOPHER_VERSION}}", moduleVersion)), 0o644); err != nil {
		return err
	}

	envExample := o.Agent.EnvExample
	if tracker.EnvExample != "" {
		envExample += "\n" + tracker.EnvExample
	}
	files := map[string]string{
		provider.ContainerfileName: o.Agent.Dockerfile,
		".gitignore":               gitignore,
		".env.example":             envExample + "\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(configDir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}

	args := ResolveTemplateArgs(tracker, o.Answers)
	if err := rewriteMain(filepath.Join(configDir, "main.go"), o.Agent, o.Model, provider, args["ISSUE_TRACKER_LOGIN_COMMAND"]); err != nil {
		return err
	}
	if err := substituteTemplateArgs(configDir, args); err != nil {
		return err
	}
	if !createLabel {
		if err := rewriteTextFiles(configDir, func(s string) string { return strings.ReplaceAll(s, " --label agentgopher", "") }); err != nil {
			return err
		}
	}
	if tracker.Name == "custom" {
		return os.WriteFile(filepath.Join(configDir, SetupIssueTrackerDoc), []byte(BuildSetupIssueTrackerDoc(provider.CLINamespace)), 0o644)
	}
	return nil
}

// ResolveTemplateArgs applies question answers (or defaults) to the tracker's
// template arguments. Answers are spliced inside single quotes in the
// registry commands, so embedded single quotes are escaped for sh.
func ResolveTemplateArgs(t IssueTrackerEntry, answers map[string]string) map[string]string {
	out := make(map[string]string, len(t.TemplateArgs))
	for k, v := range t.TemplateArgs {
		for _, q := range t.Questions {
			answer, ok := answers[q.Key]
			if !ok || answer == "" {
				answer = q.Default
			}
			v = strings.ReplaceAll(v, "{{"+q.Key+"}}", strings.ReplaceAll(answer, "'", `'\''`))
		}
		out[k] = v
	}
	return out
}

var modelCallPattern = regexp.MustCompile(`agent\.ClaudeCode\("[^"]*", nil\)`)
var dockerWordPattern = regexp.MustCompile(`\bdocker\b`)

// rewriteMain points main.go at the chosen agent, model and provider, and
// prepends the tracker login hook when the tracker has one. Templates always
// use agent.ClaudeCode and docker as placeholders.
func rewriteMain(path string, a AgentEntry, model string, p SandboxProviderEntry, loginCommand string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s := modelCallPattern.ReplaceAllString(string(b), fmt.Sprintf(`agent.%s(%q, nil)`, a.Factory, model))
	s = dockerWordPattern.ReplaceAllString(s, p.Name)
	if loginCommand != "" {
		s = strings.Replace(s, "SandboxOnSandboxReady: []agentgopher.SandboxHook{", fmt.Sprintf("SandboxOnSandboxReady: []agentgopher.SandboxHook{{Command: %q}, ", loginCommand), 1)
		s = strings.ReplaceAll(s, "}, }", "}}")
	}
	return os.WriteFile(path, []byte(s), 0o644)
}

func isTextFile(name string) bool {
	switch name {
	case "Dockerfile", "Containerfile", ".gitignore":
		return true
	}
	switch filepath.Ext(name) {
	case ".md", ".txt", ".env", ".example":
		return true
	}
	return false
}

func rewriteTextFiles(configDir string, fn func(string) string) error {
	entries, err := os.ReadDir(configDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !isTextFile(e.Name()) {
			continue
		}
		path := filepath.Join(configDir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		updated := fn(string(b))
		if updated == string(b) {
			continue
		}
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func substituteTemplateArgs(configDir string, args map[string]string) error {
	return rewriteTextFiles(configDir, func(s string) string {
		for k, v := range args {
			s = strings.ReplaceAll(s, "{{"+k+"}}", v)
		}
		return s
	})
}

// BuildSetupIssueTrackerDoc is the prompt the user feeds to their coding agent
// to wire up a custom tracker by editing the scaffold in place.
func BuildSetupIssueTrackerDoc(cliNamespace string) string {
	return `# Set up your custom issue tracker

You are a coding agent. Finish wiring up the **custom issue tracker** for this agentgopher project. It was scaffolded in a deliberately broken-until-configured state: until you complete the steps below, every agentgopher run hard-fails with a pointer back to this file.

## Goal

Wire up the issue tracker so the scaffolded prompts can **list**, **view**, and **close** tasks. There is no runtime abstraction to implement - the tracker commands are baked into the scaffolded files, so you edit those files **in place**.

## 1. Interview the user

Ask the user:

- Which issue tracker do they use (e.g. Linear, a GitHub repo other than this one, an internal API)?
- How should the sandbox authenticate - a CLI that is already logged in, or an API token? If a token, what is the environment variable name?

## 2. Produce three commands

Work out, together with the user, the shell commands for:

- **list** - print all open tasks **as JSON** (an array of objects, each with at least an id, title, and body). This is what the agent reads at the start of every iteration.
- **view** ` + "`<ID>`" + ` - show a single task by id.
- **close** ` + "`<ID>`" + ` - close a single task by id.

## 3. Edit the scaffolded files in place

- **Dockerfile / Containerfile** - replace the line

  ` + "```" + `
  ` + CustomTrackerTools + `
  ` + "```" + `

  with the install steps for your tracker's CLI (if it needs one).

- **Prompt files (` + "`.agentgopher/*.md`" + `)** - replace the sentinel

  ` + "```" + `
  ` + CustomListTasksSentinel + `
  ` + "```" + `

  with your **list** command. In the prompt file the sentinel sits inside an agentgopher **shell expression** - a leading ` + "`!`" + ` followed by the command in backticks - whose output is injected into the prompt before each run. Keep that ` + "`!`" + ` and the surrounding backticks; replace only the command between them, and **remove the ` + "`exit 1`" + `** (leaving it keeps every run hard-failing). Then replace the ` + "`" + CustomViewTaskMarker + "`" + ` and ` + "`" + CustomCloseTaskMarker + "`" + ` markers with your **view** and **close** commands.

- **` + "`.env.example`" + `** - replace the ` + "`# TODO`" + ` block with the real env var(s) your tracker needs, then tell the user to set them in ` + "`.agentgopher/.env`" + `.

## 4. Build the image

Once the files are wired up, build the sandbox image:

` + "```" + `
agentgopher ` + cliNamespace + ` build-image
` + "```" + `

## 5. Verify

Run your **list** command inside the built image and confirm it returns the open tasks as JSON. If it errors, fix the command or the auth and rebuild.
`
}
