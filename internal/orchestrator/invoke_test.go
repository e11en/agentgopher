package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const signal = "<promise>COMPLETE</promise>"

func baseInvoke(h *fakeHandle) (invokeOptions, *[]string) {
	var texts []string
	return invokeOptions{
		Prompt:            "do the thing",
		Provider:          fakeProvider{},
		SkipPermissions:   true,
		IdleTimeout:       time.Second,
		CompletionTimeout: 300 * time.Millisecond,
		CompletionSignals: []string{signal},
		OnText:            func(t string) { texts = append(texts, t) },
	}, &texts
}

func TestInvokeParsesEventsAndPipesPrompt(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{0, "hello"}, {0, "TOOL Bash ls"}, {0, "SESSION s-9"}, {0, "RESULT " + signal}}}
	o, texts := baseInvoke(h)
	var tools []string
	o.OnToolCall = func(n, a string) { tools = append(tools, n+":"+a) }
	res, err := invokeAgent(context.Background(), h, "/repo", o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != signal || res.SessionID != "s-9" {
		t.Fatalf("res = %+v", res)
	}
	if len(*texts) != 1 || (*texts)[0] != "hello" || len(tools) != 1 || tools[0] != "Bash:ls" {
		t.Fatalf("texts=%v tools=%v", *texts, tools)
	}
	if h.commands[0] != "fake-agent --yolo" || h.stdins[0] != "do the thing" {
		t.Fatalf("commands=%v stdins=%v", h.commands, h.stdins)
	}
}

func TestInvokeIdleTimeoutFails(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{0, "working"}, {2 * time.Second, "late"}}}
	o, _ := baseInvoke(h)
	o.IdleTimeout = 150 * time.Millisecond
	_, err := invokeAgent(context.Background(), h, "/repo", o)
	var idle *AgentIdleTimeoutError
	if !errors.As(err, &idle) || idle.Timeout != 150*time.Millisecond {
		t.Fatalf("err = %v", err)
	}
}

func TestInvokeIdleTimerResetsOnOutput(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{100 * time.Millisecond, "a"}, {100 * time.Millisecond, "b"}, {100 * time.Millisecond, "c"}}}
	o, _ := baseInvoke(h)
	o.IdleTimeout = 200 * time.Millisecond
	if _, err := invokeAgent(context.Background(), h, "/repo", o); err != nil {
		t.Fatalf("err = %v", err)
	}
}

func TestInvokeCompletionSignalSplitAcrossLinesForceCompletesHangingProcess(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{0, "<promise>COMP"}, {0, "LETE</promise>"}}, hang: true}
	o, _ := baseInvoke(h)
	var graceSeen time.Duration
	o.OnCompletionTimeout = func(d time.Duration) { graceSeen = d }
	start := time.Now()
	res, err := invokeAgent(context.Background(), h, "/repo", o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, signal) {
		t.Fatalf("Output = %q", res.Output)
	}
	if graceSeen != 300*time.Millisecond || time.Since(start) > 900*time.Millisecond {
		t.Fatalf("grace=%v elapsed=%v", graceSeen, time.Since(start))
	}
}

func TestInvokeNonZeroExitUsesStderrThenStdoutTail(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{0, "l1"}, {0, "l2"}}, exitCode: 2, stderr: "boom"}
	o, _ := baseInvoke(h)
	_, err := invokeAgent(context.Background(), h, "/repo", o)
	var aerr *AgentError
	if !errors.As(err, &aerr) || !strings.Contains(aerr.Message, "exited with code 2") || !strings.Contains(aerr.Message, "boom") {
		t.Fatalf("err = %v", err)
	}

	var lines []scriptedLine
	for i := 0; i < 25; i++ {
		lines = append(lines, scriptedLine{0, "line" + strings.Repeat("x", i)})
	}
	h = &fakeHandle{script: lines, exitCode: 1}
	o, _ = baseInvoke(h)
	_, err = invokeAgent(context.Background(), h, "/repo", o)
	if !errors.As(err, &aerr) || strings.Contains(aerr.Message, "line\n") || !strings.Contains(aerr.Message, "line"+strings.Repeat("x", 24)) {
		t.Fatalf("err = %v", err)
	}
	if strings.Count(aerr.Message, "\n") > 21 {
		t.Fatalf("more than 20 tail lines: %q", aerr.Message)
	}
}

func TestInvokeIdleWarnings(t *testing.T) {
	h := &fakeHandle{script: []scriptedLine{{450 * time.Millisecond, "RESULT " + signal}}}
	o, _ := baseInvoke(h)
	o.IdleWarningInterval = 100 * time.Millisecond
	var warnings []int
	o.OnIdleWarning = func(m int) { warnings = append(warnings, m) }
	if _, err := invokeAgent(context.Background(), h, "/repo", o); err != nil {
		t.Fatal(err)
	}
	if len(warnings) < 2 || warnings[0] != 1 || warnings[1] != 2 {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestInvokeCancelledContext(t *testing.T) {
	h := &fakeHandle{hang: true}
	o, _ := baseInvoke(h)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := invokeAgent(ctx, h, "/repo", o)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
