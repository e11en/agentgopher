package agentgopher

import (
	"context"
	"strings"
	"testing"

	"github.com/e11en/agentgopher/internal/sandboxtest"
	"github.com/e11en/agentgopher/internal/testrepo"
	"github.com/e11en/agentgopher/sandbox"
)

func TestRunRejectsHeadWithIsolatedProvider(t *testing.T) {
	_, err := Run(context.Background(), RunOptions{
		Agent:          echoAgent{script: "true"},
		Sandbox:        sandboxtest.Isolated(),
		Cwd:            testrepo.Init(t),
		Prompt:         "x",
		BranchStrategy: &sandbox.BranchStrategy{Type: sandbox.BranchHead},
	})
	if err == nil || !strings.Contains(err.Error(), "head branch strategy is not supported with isolated providers") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunDefaultsIsolatedToMergeToHead(t *testing.T) {
	repo := testrepo.Init(t)
	res, err := Run(context.Background(), RunOptions{
		Agent:   echoAgent{script: `echo "RESULT <promise>COMPLETE</promise>"`},
		Sandbox: sandboxtest.Isolated(),
		Cwd:     repo,
		Prompt:  "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "main" || res.CompletionSignal == "" {
		t.Fatalf("res = %+v", res)
	}
}
