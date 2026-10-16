package initcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/e11en/agentgopher/sandbox/docker"
	"github.com/e11en/agentgopher/sandbox/podman"
)

// Replaced in tests.
var (
	execCommand      = exec.CommandContext
	buildDockerImage = docker.BuildImage
	buildPodmanImage = podman.BuildImage
	hostUIDGID       = func() (string, string) {
		if runtime.GOOS == "windows" {
			return "", ""
		}
		return strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid())
	}
)

// Options configure Run. Empty strings and nil pointers mean "ask", or fail
// in non-interactive mode.
type Options struct {
	RepoDir      string
	ImageName    string
	Template     string
	Agent        string
	Model        string
	Sandbox      string
	IssueTracker string
	CreateLabel  *bool
	BuildImage   *bool
	Answers      map[string]string
	Interactive  bool
	// BuildDockerImage and BuildPodmanImage override the image builders (the CLI passes its own seams).
	BuildDockerImage func(context.Context, docker.BuildImageOptions) error
	BuildPodmanImage func(context.Context, podman.BuildImageOptions) error
	Prompter         Prompter
	Out              io.Writer
}

// Run performs init: resolve choices, scaffold, label, build, next steps.
func Run(ctx context.Context, o Options) error {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.Answers == nil {
		o.Answers = map[string]string{}
	}
	if err := validateFlags(o); err != nil {
		return err
	}

	agentName, err := resolveSelect(o, o.Agent, "--agent", "Select an agent:", agentChoices(), "claude-code")
	if err != nil {
		return err
	}
	ag, _ := GetAgent(agentName)
	model := o.Model
	if model == "" {
		model = ag.DefaultModel
	}
	sandboxName, err := resolveSelect(o, o.Sandbox, "--sandbox", "Select a sandbox provider:", providerChoices(), "")
	if err != nil {
		return err
	}
	provider, _ := GetSandboxProvider(sandboxName)
	trackerName, err := resolveSelect(o, o.IssueTracker, "--issue-tracker", "Select an issue tracker:", trackerChoices(), "github-issues")
	if err != nil {
		return err
	}
	tracker, _ := GetIssueTracker(trackerName)
	templateName, err := resolveSelect(o, o.Template, "--template", "Select a template:", templateChoices(), "blank")
	if err != nil {
		return err
	}
	for _, q := range tracker.Questions {
		if _, ok := o.Answers[q.Key]; ok {
			continue
		}
		if !o.Interactive {
			// Defaults keep non-interactive runs working; --answer KEY=VALUE overrides.
			o.Answers[q.Key] = q.Default
			continue
		}
		a, err := o.Prompter.Input(q.Prompt, q.Default)
		if err != nil {
			return err
		}
		o.Answers[q.Key] = a
	}

	createLabel := false
	if tracker.Name == "github-issues" {
		createLabel, err = resolveConfirm(o, o.CreateLabel, "--create-label", `Create an "agentgopher" GitHub label? (Templates filter issues by this label)`)
		if err != nil {
			return err
		}
		if createLabel {
			// Best effort, as upstream: an existing label or a missing gh is not an error.
			cmd := execCommand(ctx, "gh", "label", "create", "agentgopher", "--description", "Issues for agentgopher to work on", "--color", "F9A825")
			cmd.Dir = o.RepoDir
			_ = cmd.Run()
		}
	}

	_, _ = fmt.Fprintln(o.Out, "Scaffolding .agentgopher/ config directory...")
	if err := Scaffold(o.RepoDir, ScaffoldOptions{
		Agent: ag, Model: model, Template: templateName, CreateLabel: &createLabel,
		IssueTracker: &tracker, Sandbox: &provider, Answers: o.Answers,
	}); err != nil {
		return err
	}

	if tracker.Name == "custom" {
		_, _ = fmt.Fprintln(o.Out, "Init complete! Your custom issue tracker isn't configured yet - see the steps below before building.")
	} else {
		build, err := resolveConfirm(o, o.BuildImage, "--build-image", fmt.Sprintf("Build the default %s image now?", provider.Label))
		if err != nil {
			return err
		}
		if build {
			imageName := o.ImageName
			if imageName == "" {
				imageName = docker.DefaultImageName(o.RepoDir)
			}
			_, _ = fmt.Fprintf(o.Out, "Building %s image '%s'...\n", provider.Label, imageName)
			contextDir := filepath.Join(o.RepoDir, configDirName)
			buildDocker, buildPodman := buildDockerImage, buildPodmanImage
			if o.BuildDockerImage != nil {
				buildDocker = o.BuildDockerImage
			}
			if o.BuildPodmanImage != nil {
				buildPodman = o.BuildPodmanImage
			}
			if provider.Name == "podman" {
				err = buildPodman(ctx, podman.BuildImageOptions{ImageName: imageName, ContextDir: contextDir})
			} else {
				err = buildDocker(ctx, docker.BuildImageOptions{ImageName: imageName, ContextDir: contextDir, BuildArgs: UIDBuildArgs()})
			}
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(o.Out, "Init complete! Image built successfully.")
		} else {
			_, _ = fmt.Fprintf(o.Out, "Init complete! Run `agentgopher %s build-image` to build the %s image later.\n", provider.CLINamespace, provider.Label)
		}
	}

	for _, line := range NextStepsLines(templateName, tracker, ag) {
		_, _ = fmt.Fprintln(o.Out, line)
	}
	return nil
}

// UIDBuildArgs aligns the image UID/GID with the host (ADR 0014). Empty on Windows.
func UIDBuildArgs() map[string]string {
	uid, gid := hostUIDGID()
	args := map[string]string{}
	if uid != "" {
		args["AGENT_UID"] = uid
	}
	if gid != "" {
		args["AGENT_GID"] = gid
	}
	return args
}

func validateFlags(o Options) error {
	if o.Template != "" {
		if _, ok := GetTemplate(o.Template); !ok {
			return fmt.Errorf("unknown template %q. Available: %s", o.Template, names(templateChoices()))
		}
	}
	if o.Sandbox != "" {
		if _, ok := GetSandboxProvider(o.Sandbox); !ok {
			return fmt.Errorf("unknown sandbox provider %q. Available: %s", o.Sandbox, names(providerChoices()))
		}
	}
	if o.IssueTracker != "" {
		if _, ok := GetIssueTracker(o.IssueTracker); !ok {
			return fmt.Errorf("unknown issue tracker %q. Available: %s", o.IssueTracker, names(trackerChoices()))
		}
	}
	if o.Agent != "" {
		if _, ok := GetAgent(o.Agent); !ok {
			return fmt.Errorf("unknown agent %q. Available: %s", o.Agent, names(agentChoices()))
		}
	}
	return nil
}

func resolveSelect(o Options, flagValue, flag, message string, choices []Choice, initial string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if !o.Interactive {
		return "", fmt.Errorf("%s is required in non-interactive mode (no TTY detected)", flag)
	}
	return o.Prompter.Select(message, choices, initial)
}

func resolveConfirm(o Options, flagValue *bool, flag, message string) (bool, error) {
	if flagValue != nil {
		return *flagValue, nil
	}
	if !o.Interactive {
		return false, fmt.Errorf("%s is required in non-interactive mode (no TTY detected)", flag)
	}
	return o.Prompter.Confirm(message, true)
}

func names(cs []Choice) string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Value
	}
	return strings.Join(out, ", ")
}

func agentChoices() []Choice {
	var cs []Choice
	for _, a := range ListAgents() {
		cs = append(cs, Choice{Value: a.Name, Label: a.Label, Hint: "Default model: " + a.DefaultModel})
	}
	return cs
}

func providerChoices() []Choice {
	var cs []Choice
	for _, p := range ListSandboxProviders() {
		cs = append(cs, Choice{Value: p.Name, Label: p.Label})
	}
	return cs
}

func trackerChoices() []Choice {
	var cs []Choice
	for _, t := range ListIssueTrackers() {
		cs = append(cs, Choice{Value: t.Name, Label: t.Label})
	}
	return cs
}

func templateChoices() []Choice {
	var cs []Choice
	for _, t := range ListTemplates() {
		cs = append(cs, Choice{Value: t.Name, Label: t.Name, Hint: t.Description})
	}
	return cs
}
