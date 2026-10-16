package initcmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/sandbox/docker"
	"github.com/e11en/agentgopher/sandbox/podman"
)

// scriptedPrompter answers in order; any answer is a string, "y"/"n" for confirms.
type scriptedPrompter struct {
	answers []string
	asked   []string
}

func (p *scriptedPrompter) next(msg string) string {
	p.asked = append(p.asked, msg)
	if len(p.answers) == 0 {
		return ""
	}
	a := p.answers[0]
	p.answers = p.answers[1:]
	return a
}
func (p *scriptedPrompter) Select(msg string, _ []Choice, initial string) (string, error) {
	if a := p.next(msg); a != "" {
		return a, nil
	}
	return initial, nil
}
func (p *scriptedPrompter) Confirm(msg string, initial bool) (bool, error) {
	switch p.next(msg) {
	case "y":
		return true, nil
	case "n":
		return false, nil
	}
	return initial, nil
}
func (p *scriptedPrompter) Input(msg, initial string) (string, error) {
	if a := p.next(msg); a != "" {
		return a, nil
	}
	return initial, nil
}

type fakes struct {
	ghCalls      [][]string
	dockerBuilds []docker.BuildImageOptions
	podmanBuilds []podman.BuildImageOptions
}

func installFakes(t *testing.T) *fakes {
	t.Helper()
	f := &fakes{}
	oldExec, oldD, oldP, oldUID := execCommand, buildDockerImage, buildPodmanImage, hostUIDGID
	execCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		f.ghCalls = append(f.ghCalls, append([]string{name}, args...))
		return exec.CommandContext(ctx, "true")
	}
	buildDockerImage = func(_ context.Context, o docker.BuildImageOptions) error {
		f.dockerBuilds = append(f.dockerBuilds, o)
		return nil
	}
	buildPodmanImage = func(_ context.Context, o podman.BuildImageOptions) error {
		f.podmanBuilds = append(f.podmanBuilds, o)
		return nil
	}
	hostUIDGID = func() (string, string) { return "501", "20" }
	t.Cleanup(func() { execCommand, buildDockerImage, buildPodmanImage, hostUIDGID = oldExec, oldD, oldP, oldUID })
	return f
}

func TestRunNonInteractiveWithAllFlags(t *testing.T) {
	f := installFakes(t)
	dir := t.TempDir()
	var out bytes.Buffer
	yes := true
	err := Run(context.Background(), Options{
		RepoDir: dir, Template: "simple-loop", Agent: "claude-code", Sandbox: "docker", IssueTracker: "github-issues",
		CreateLabel: &yes, BuildImage: &yes, Out: &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agentgopher", "main.go")); err != nil {
		t.Fatal("scaffold missing")
	}
	if len(f.ghCalls) != 1 || !strings.HasPrefix(strings.Join(f.ghCalls[0], " "), "gh label create agentgopher") {
		t.Fatalf("gh calls = %v", f.ghCalls)
	}
	if len(f.dockerBuilds) != 1 || f.dockerBuilds[0].ImageName != docker.DefaultImageName(dir) || f.dockerBuilds[0].BuildArgs["AGENT_UID"] != "501" || f.dockerBuilds[0].ContextDir != filepath.Join(dir, ".agentgopher") {
		t.Fatalf("docker builds = %+v", f.dockerBuilds)
	}
	s := out.String()
	for _, want := range []string{"Scaffolding .agentgopher/", "Init complete! Image built successfully.", "Next steps:"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestRunNonInteractiveMissingFlagFails(t *testing.T) {
	installFakes(t)
	err := Run(context.Background(), Options{RepoDir: t.TempDir(), Template: "blank", Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "--agent is required in non-interactive mode") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunRejectsUnknownFlagValuesBeforePrompting(t *testing.T) {
	installFakes(t)
	cases := map[string]Options{
		`unknown template "x". Available: blank, simple-loop`:                                 {Template: "x"},
		`unknown sandbox provider "x". Available: docker, podman`:                             {Sandbox: "x"},
		`unknown issue tracker "x". Available: github-issues, beads, custom, jira, windshift`: {IssueTracker: "x"},
		`unknown agent "x". Available: claude-code, pi`:                                       {Agent: "x"},
	}
	for want, o := range cases {
		o.RepoDir = t.TempDir()
		o.Out = &bytes.Buffer{}
		err := Run(context.Background(), o)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want %q, got %v", want, err)
		}
	}
}

func TestRunInteractiveAsksEverythingIncludingTrackerQuestions(t *testing.T) {
	f := installFakes(t)
	dir := t.TempDir()
	p := &scriptedPrompter{answers: []string{"pi", "podman", "jira", "simple-loop", "project = X", "Closed", "n"}}
	err := Run(context.Background(), Options{RepoDir: dir, Interactive: true, Prompter: p, Out: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	wantAsked := []string{"Select an agent:", "Select a sandbox provider:", "Select an issue tracker:", "Select a template:",
		"JQL that selects the work items ready for the agent:", "Status to transition a finished work item to:", "Build the default Podman image now?"}
	if strings.Join(p.asked, "|") != strings.Join(wantAsked, "|") {
		t.Fatalf("asked = %q", p.asked)
	}
	prompt, _ := os.ReadFile(filepath.Join(dir, ".agentgopher", "prompt.md"))
	if !strings.Contains(string(prompt), "--jql 'project = X'") || !strings.Contains(string(prompt), "--status 'Closed'") {
		t.Fatalf("answers not applied:\n%s", prompt)
	}
	if len(f.ghCalls) != 0 || len(f.podmanBuilds) != 0 || len(f.dockerBuilds) != 0 {
		t.Fatalf("no label for jira and no build when declined: %+v", f)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agentgopher", "Containerfile")); err != nil {
		t.Fatal("podman writes a Containerfile")
	}
}

func TestRunCustomTrackerSkipsBuildEvenWhenFlagSet(t *testing.T) {
	f := installFakes(t)
	yes := true
	var out bytes.Buffer
	err := Run(context.Background(), Options{RepoDir: t.TempDir(), Template: "blank", Agent: "claude-code", Sandbox: "docker", IssueTracker: "custom", BuildImage: &yes, Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.dockerBuilds) != 0 || !strings.Contains(out.String(), "custom issue tracker isn't configured yet") {
		t.Fatalf("builds=%d out=%s", len(f.dockerBuilds), out.String())
	}
}

func TestRunLabelOnlyAskedForGitHub(t *testing.T) {
	installFakes(t)
	p := &scriptedPrompter{answers: []string{"claude-code", "docker", "beads", "blank", "n"}}
	if err := Run(context.Background(), Options{RepoDir: t.TempDir(), Interactive: true, Prompter: p, Out: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	for _, q := range p.asked {
		if strings.Contains(q, "label") {
			t.Fatalf("label question asked for beads: %q", p.asked)
		}
	}
}

func TestStdinPrompter(t *testing.T) {
	in := strings.NewReader("2\n\nn\ncustom value\n")
	var out bytes.Buffer
	p := NewStdinPrompter(in, &out)
	v, err := p.Select("Pick:", []Choice{{Value: "a", Label: "A"}, {Value: "b", Label: "B", Hint: "second"}}, "a")
	if err != nil || v != "b" {
		t.Fatalf("select = %q, %v", v, err)
	}
	v, _ = p.Select("Pick:", []Choice{{Value: "a", Label: "A"}}, "a")
	if v != "a" {
		t.Fatalf("empty line must pick the initial value, got %q", v)
	}
	c, _ := p.Confirm("Sure?", true)
	if c {
		t.Fatal("n must be false")
	}
	s, _ := p.Input("Value:", "default")
	if s != "custom value" {
		t.Fatalf("input = %q", s)
	}
	if _, err := p.Input("Value:", "x"); err == nil {
		t.Fatal("EOF must be an error")
	}
	if !strings.Contains(out.String(), "2) B (second)") {
		t.Fatalf("menu = %q", out.String())
	}
}
