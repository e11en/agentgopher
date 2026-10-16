package agentgopher

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/prompt"
	"github.com/e11en/agentgopher/internal/testrepo"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

// tuiAgent "launches" a TUI that writes its args to a file and commits.
type tuiAgent struct{ echoAgent }

func (tuiAgent) BuildInteractiveArgs(o agent.CommandOptions) []string {
	return []string{"sh", "-c", `printf '%s' "$1" > tui-args.txt; git add -A; git commit -q -m "from tui"; exit 3`, "sh", o.Prompt}
}

func TestInteractiveRunsAgentAndCollectsCommits(t *testing.T) {
	repo := testrepo.Init(t)
	pf := filepath.Join(repo, "p.md")
	os.WriteFile(pf, []byte("Hello {{NAME}} on {{SOURCE_BRANCH}}"), 0o644)
	var out bytes.Buffer
	res, err := Interactive(context.Background(), InteractiveOptions{
		Agent:      tuiAgent{},
		Sandbox:    nosandbox.New(nil),
		Cwd:        repo,
		PromptFile: pf,
		PromptArgs: map[string]string{"NAME": "ellen"},
		Stdin:      strings.NewReader(""),
		Stdout:     &out,
		Stderr:     &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.Branch != "main" || len(res.Commits) != 1 {
		t.Fatalf("res = %+v", res)
	}
	args, _ := os.ReadFile(filepath.Join(repo, "tui-args.txt"))
	if string(args) != "Hello ellen on main" {
		t.Fatalf("args = %q", args)
	}
	if !strings.Contains(out.String(), "Session Complete") {
		t.Fatalf("out:\n%s", out.String())
	}
}

func TestInteractiveRejectsProviderWithoutInteractiveArgs(t *testing.T) {
	_, err := Interactive(context.Background(), InteractiveOptions{Agent: echoAgent{}, Cwd: testrepo.Init(t), Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "does not support interactive sessions") {
		t.Fatalf("err = %v", err)
	}
}

func TestCollectMissingArgsAsksAndRejectsEmpty(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("\nvalue-a\nvalue-b\n")
	got, err := collectMissingArgs("{{A}} {{B}} {{A}}", prompt.Args{}, in, &out)
	if err != nil || got["A"] != "value-a" || got["B"] != "value-b" {
		t.Fatalf("got %v, %v", got, err)
	}
	if strings.Count(out.String(), "Enter value for {{A}}") != 2 || !strings.Contains(out.String(), "A value is required") {
		t.Fatalf("out = %q", out.String())
	}
	if _, err := collectMissingArgs("{{A}}", prompt.Args{}, strings.NewReader(""), &out); err == nil {
		t.Fatal("EOF must fail")
	}
}
