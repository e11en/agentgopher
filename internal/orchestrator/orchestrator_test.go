package orchestrator

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/testrepo"
)

func baseOptions(t *testing.T, handles ...*fakeHandle) (Options, *bytes.Buffer) {
	t.Helper()
	repo := testrepo.Init(t)
	log := &bytes.Buffer{}
	return Options{
		HostRepoDir: repo,
		Iterations:  len(handles),
		Prompt:      "work",
		Branch:      "main",
		Provider:    fakeProvider{},
		Sandbox:     &fakeNoSandboxProvider{handles: handles},
		Display:     display.NewWriter(log),
		IdleTimeout: time.Second,
	}, log
}

func TestOrchestrateStopsOnCompletionSignal(t *testing.T) {
	h1 := &fakeHandle{script: []scriptedLine{{0, "iteration one"}}}
	h2 := &fakeHandle{script: []scriptedLine{{0, "RESULT " + DefaultCompletionSignal}}}
	h3 := &fakeHandle{script: []scriptedLine{{0, "never"}}}
	o, log := baseOptions(t, h1, h2, h3)
	res, err := Orchestrate(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Iterations) != 2 || res.CompletionSignal != DefaultCompletionSignal || res.Branch != "main" {
		t.Fatalf("res = %+v", res)
	}
	if len(h3.commands) != 0 {
		t.Fatal("third iteration must not run")
	}
	for _, want := range []string{"Iteration 1/3", "Agent started", "Agent stopped", "Agent signaled completion after 2 iteration(s)."} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("missing %q in log:\n%s", want, log.String())
		}
	}
}

func TestOrchestrateReachesMaxIterations(t *testing.T) {
	o, log := baseOptions(t, &fakeHandle{script: []scriptedLine{{0, "a"}}}, &fakeHandle{script: []scriptedLine{{0, "b"}}})
	res, err := Orchestrate(context.Background(), o)
	if err != nil || res.CompletionSignal != "" || len(res.Iterations) != 2 || res.Stdout != "ab" {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if !strings.Contains(log.String(), "Reached max iterations (2).") {
		t.Fatalf("log:\n%s", log.String())
	}
}

func TestOrchestrateExpandsPromptInSandboxUnlessSkipped(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{0, "RESULT " + DefaultCompletionSignal}}}
	o, _ := baseOptions(t, h)
	o.Prompt = "Branch: !\x01`git branch --show-current`"
	if _, err := Orchestrate(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if h.stdins[0] != "Branch: main" {
		t.Fatalf("prompt sent = %q", h.stdins[0])
	}

	h = &fakeHandle{script: []scriptedLine{{0, "RESULT " + DefaultCompletionSignal}}}
	o, _ = baseOptions(t, h)
	o.Prompt = "literal !`echo x`"
	o.SkipPromptExpansion = true
	if _, err := Orchestrate(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if h.stdins[0] != "literal !`echo x`" {
		t.Fatalf("prompt sent = %q", h.stdins[0])
	}
}

func TestOrchestrateEmitsStreamEventsAndLabels(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{0, "hi there\n"}, {0, "TOOL Bash ls"}, {0, "RESULT " + DefaultCompletionSignal}}}
	o, log := baseOptions(t, h)
	o.Name = "worker"
	var events []StreamEvent
	o.OnStreamEvent = func(e StreamEvent) { events = append(events, e) }
	if _, err := Orchestrate(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range events {
		if e.Iteration != 1 || e.Timestamp.IsZero() {
			t.Fatalf("bad event %+v", e)
		}
		kinds = append(kinds, string(e.Type))
	}
	joined := strings.Join(kinds, ",")
	if !strings.Contains(joined, "raw") || !strings.Contains(joined, "text") || !strings.Contains(joined, "toolCall") {
		t.Fatalf("kinds = %v", kinds)
	}
	if !strings.Contains(log.String(), "[Bash] ls") || strings.Contains(log.String(), "[worker]") {
		t.Fatalf("log:\n%s", log.String())
	}
}

func TestOrchestratePropagatesAgentFailure(t *testing.T) {
	o, _ := baseOptions(t, &fakeHandle{exitCode: 3, stderr: "crash"})
	_, err := Orchestrate(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "crash") {
		t.Fatalf("err = %v", err)
	}
}
