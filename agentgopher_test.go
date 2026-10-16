package agentgopher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/agent"
	"github.com/e11en/agentgopher/internal/testrepo"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

// echoAgent is a Provider whose "CLI" is a shell script: it prints the prompt
// it received over stdin, then the completion signal as a result event.
type echoAgent struct{ script string }

func (echoAgent) Name() string           { return "echo-agent" }
func (echoAgent) Env() map[string]string { return map[string]string{"AGENT_ENV": "1"} }
func (echoAgent) CaptureSessions() bool  { return false }
func (a echoAgent) BuildPrintCommand(o agent.CommandOptions) agent.PrintCommand {
	p := o.Prompt
	return agent.PrintCommand{Command: a.script, Stdin: &p}
}
func (echoAgent) ParseStreamLine(line string) []agent.StreamEvent {
	if strings.HasPrefix(line, "RESULT ") {
		return []agent.StreamEvent{{Type: agent.EventResult, Text: strings.TrimPrefix(line, "RESULT ")}}
	}
	return []agent.StreamEvent{{Type: agent.EventText, Text: line + "\n"}}
}

func TestRunEndToEndOnHost(t *testing.T) {
	repo := testrepo.Init(t)
	os.MkdirAll(filepath.Join(repo, ".agentgopher"), 0o755)
	os.WriteFile(filepath.Join(repo, ".agentgopher", "prompt.md"), []byte("Hello {{WHO}} on {{SOURCE_BRANCH}}\nFiles: !`ls`\n"), 0o644)
	os.WriteFile(filepath.Join(repo, ".agentgopher", ".env"), []byte("FROM_ENV=yes\n"), 0o644)

	script := `cat; echo "env=$FROM_ENV agent=$AGENT_ENV"; echo "RESULT <promise>COMPLETE</promise>"`
	res, err := Run(context.Background(), RunOptions{
		Agent:         echoAgent{script: script},
		Sandbox:       nosandbox.New(nil),
		Cwd:           repo,
		PromptFile:    filepath.Join(repo, ".agentgopher", "prompt.md"),
		PromptArgs:    map[string]string{"WHO": "world"},
		MaxIterations: 3,
		Name:          "worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.CompletionSignal != "<promise>COMPLETE</promise>" || len(res.Iterations) != 1 || res.Branch != "main" {
		t.Fatalf("res = %+v", res)
	}
	wantLog := filepath.Join(repo, ".agentgopher", "logs", "main-worker.log")
	if res.LogFilePath != wantLog {
		t.Fatalf("LogFilePath = %q, want %q", res.LogFilePath, wantLog)
	}
	logBytes, err := os.ReadFile(wantLog)
	if err != nil {
		t.Fatal(err)
	}
	log := string(logBytes)
	for _, want := range []string{"--- Run started: ", "agentgopher Run\n  Agent: worker\n  Sandbox: no-sandbox\n  Max iterations: 3\n  Branch: main", "Hello world on main", "Files: README.md", "env=yes agent=1", "Run complete: agent finished after 1 iteration(s)."} {
		if !strings.Contains(log, want) {
			t.Fatalf("missing %q in log:\n%s", want, log)
		}
	}
}

func TestRunInlinePromptIsVerbatim(t *testing.T) {
	repo := testrepo.Init(t)
	res, err := Run(context.Background(), RunOptions{
		Agent:   echoAgent{script: `cat; echo; echo "RESULT <promise>COMPLETE</promise>"`},
		Sandbox: nosandbox.New(nil),
		Cwd:     repo,
		Prompt:  "keep {{THIS}} and !`this`",
	})
	if err != nil {
		t.Fatal(err)
	}
	log, _ := os.ReadFile(res.LogFilePath)
	if !strings.Contains(string(log), "keep {{THIS}} and !`this`") {
		t.Fatalf("log:\n%s", log)
	}
}

func TestRunValidation(t *testing.T) {
	repo := testrepo.Init(t)
	base := RunOptions{Agent: echoAgent{script: "true"}, Sandbox: nosandbox.New(nil), Cwd: repo, Prompt: "x"}

	o := base
	o.PromptArgs = map[string]string{"A": "1"}
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "PromptArgs is only supported with PromptFile") {
		t.Fatalf("inline+args: %v", err)
	}

	o = base
	o.Cwd = filepath.Join(repo, "missing")
	var cwdErr *CwdError
	if _, err := Run(context.Background(), o); !errors.As(err, &cwdErr) {
		t.Fatalf("cwd: %v", err)
	}

	o = base
	o.Prompt = ""
	var perr *PromptError
	if _, err := Run(context.Background(), o); !errors.As(err, &perr) {
		t.Fatalf("no prompt: %v", err)
	}
}

func TestRunRejectsBuiltInOverrideAndMissingArg(t *testing.T) {
	repo := testrepo.Init(t)
	pf := filepath.Join(repo, "p.md")
	os.WriteFile(pf, []byte("{{NEEDED}}"), 0o644)
	base := RunOptions{Agent: echoAgent{script: "true"}, Sandbox: nosandbox.New(nil), Cwd: repo, PromptFile: pf}

	o := base
	o.PromptArgs = map[string]string{"TARGET_BRANCH": "x", "NEEDED": "y"}
	var perr *PromptError
	if _, err := Run(context.Background(), o); !errors.As(err, &perr) || !strings.Contains(perr.Message, "built-in") {
		t.Fatalf("override: %v", err)
	}

	o = base
	if _, err := Run(context.Background(), o); !errors.As(err, &perr) || !strings.Contains(perr.Message, "{{NEEDED}}") {
		t.Fatalf("missing: %v", err)
	}
}

func TestRunAlreadyCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, RunOptions{Agent: echoAgent{script: "true"}, Sandbox: nosandbox.New(nil), Cwd: testrepo.Init(t), Prompt: "x"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunStdoutLoggingAndStreamEvents(t *testing.T) {
	repo := testrepo.Init(t)
	var events []StreamEvent
	res, err := Run(context.Background(), RunOptions{
		Agent:   echoAgent{script: `echo "RESULT <promise>COMPLETE</promise>"`},
		Sandbox: nosandbox.New(nil),
		Cwd:     repo,
		Prompt:  "x",
		Logging: &Logging{Type: LoggingFile, Path: filepath.Join(repo, "custom.log"), OnAgentStreamEvent: func(e StreamEvent) { events = append(events, e) }},
	})
	if err != nil || res.LogFilePath != filepath.Join(repo, "custom.log") {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if len(events) == 0 {
		t.Fatal("expected stream events")
	}

	if h := buildStreamHandler(Logging{Type: LoggingStdout}, nil); h != nil {
		t.Fatal("stdout mode without verbose must not build a stream handler")
	}
}

func TestBuildLogFilename(t *testing.T) {
	cases := []struct{ branch, target, name, want string }{
		{"feat/x", "", "", "feat-x.log"},
		{"tmp-1", "main", "", "main-tmp-1.log"},
		{"main", "", "My Worker!", "main-my-worker-.log"},
	}
	for _, c := range cases {
		if got := BuildLogFilename(c.branch, c.target, c.name); got != c.want {
			t.Errorf("BuildLogFilename(%q,%q,%q) = %q, want %q", c.branch, c.target, c.name, got, c.want)
		}
	}
}

func TestRunMergeToHeadEndToEnd(t *testing.T) {
	repo := testrepo.Init(t)
	pf := filepath.Join(repo, "p.md")
	os.WriteFile(pf, []byte("source={{SOURCE_BRANCH}} target={{TARGET_BRANCH}}\n"), 0o644)
	res, err := Run(context.Background(), RunOptions{
		Agent:          echoAgent{script: `cat > prompt.txt; git add prompt.txt; git commit -q -m "agent"; echo "RESULT <promise>COMPLETE</promise>"`},
		Sandbox:        nosandbox.New(nil),
		Cwd:            repo,
		PromptFile:     pf,
		Name:           "worker",
		BranchStrategy: &sandbox.BranchStrategy{Type: sandbox.BranchMergeToHead},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "main" || len(res.Commits) != 1 || res.PreservedWorktreePath != "" {
		t.Fatalf("res = %+v", res)
	}
	b, _ := os.ReadFile(filepath.Join(repo, "prompt.txt"))
	if !strings.HasPrefix(string(b), "source=agentgopher/worker/") || !strings.Contains(string(b), "target=main") {
		t.Fatalf("prompt seen by agent: %q", b)
	}
	if !strings.HasPrefix(filepath.Base(res.LogFilePath), "main-agentgopher-worker-") {
		t.Fatalf("LogFilePath = %q", res.LogFilePath)
	}
}

func TestRunStrategyValidation(t *testing.T) {
	repo := testrepo.Init(t)
	base := RunOptions{Agent: echoAgent{script: "true"}, Sandbox: nosandbox.New(nil), Cwd: repo, Prompt: "x"}
	o := base
	o.CopyToWorktree = []string{"node_modules"}
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "copyToWorktree is not supported with head") {
		t.Fatalf("head+copy: %v", err)
	}
	o = base
	o.Sandbox = isolatedStub{}
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "phase 4") {
		t.Fatalf("isolated: %v", err)
	}
}

type isolatedStub struct{}

func (isolatedStub) Kind() sandbox.Kind     { return sandbox.KindIsolated }
func (isolatedStub) Name() string           { return "iso" }
func (isolatedStub) Env() map[string]string { return nil }
