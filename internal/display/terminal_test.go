package display

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTerminalFormatsEntries(t *testing.T) {
	var buf bytes.Buffer
	d := NewTerminal(&buf)
	d.Intro("agentgopher")
	d.Status("Iteration 1/3", SeverityInfo)
	d.Status("Oops", SeverityError)
	d.Summary("Run", [][2]string{{"Agent", "claude-code"}})
	d.ToolCall("Bash", "ls")
	d.Text("line")
	d.TextChunk("chunk\n")
	got := buf.String()
	for _, want := range []string{"\x1b[7m agentgopher \x1b[0m", "\x1b[1mIteration 1/3\x1b[0m", "\x1b[31m", "Run", "  \x1b[1mAgent\x1b[0m: \x1b[2mclaude-code\x1b[0m", "\x1b[2mBash(ls)\x1b[0m", "line\n", "chunk\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestTerminalTaskWithoutTTYPrintsPlainLines(t *testing.T) {
	var buf bytes.Buffer
	d := NewTerminal(&buf)
	err := d.Task("Setting up", func(log func(string)) error { log("step"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.Contains(got, "Setting up...\n  step\nSetting up done") {
		t.Fatalf("got %q", got)
	}
	want := errors.New("x")
	if err := d.Task("Fails", func(func(string)) error { return want }); !errors.Is(err, want) || !strings.Contains(buf.String(), "Fails (failed)") {
		t.Fatalf("err=%v out=%q", err, buf.String())
	}
}

func TestTerminalSpinnerAnimatesWhenEnabled(t *testing.T) {
	var buf bytes.Buffer
	d := NewTerminalWithTicker(&buf, 5*time.Millisecond).(*terminal)
	d.animate = true
	_ = d.Task("Working", func(func(string)) error { time.Sleep(30 * time.Millisecond); return nil })
	out := buf.String()
	if strings.Count(out, "\r") < 2 || !strings.Contains(out, "\x1b[?25l") || !strings.HasSuffix(out, "Working done\n") || !strings.Contains(out, "\x1b[?25h") {
		t.Fatalf("got %q", out)
	}
}
