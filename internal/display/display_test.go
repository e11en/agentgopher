package display

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriterDisplayFormatsEntries(t *testing.T) {
	var buf bytes.Buffer
	d := NewWriter(&buf)
	d.Intro("agentgopher")
	d.Summary("agentgopher Run", [][2]string{{"Agent", "claude-code"}, {"Branch", "main"}})
	d.Status("[worker] Iteration 1/3", SeverityInfo)
	err := d.Task("Setting up", func(log func(string)) error { log("git config"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	d.TextChunk("streamed prose")
	d.ToolCall("Bash", "ls")
	d.Text("Context window: 12k")
	got := buf.String()
	for _, want := range []string{
		"agentgopher Run\n  Agent: claude-code\n  Branch: main\n",
		"Iteration 1/3\n",
		"Setting up...\n  git config\nSetting up done (",
		"streamed prose\n[Bash] ls\n",
		"Context window: 12k\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "[worker]") {
		t.Fatal("label must be stripped")
	}
}

func TestTaskPropagatesError(t *testing.T) {
	d := NewWriter(&bytes.Buffer{})
	want := errors.New("boom")
	if err := d.Task("x", func(func(string)) error { return want }); !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenLogFileCreatesDirAndDelimiter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "run.log")
	f, err := OpenLogFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	b, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(b), "\n--- Run started: ") {
		t.Fatalf("got %q", b)
	}
}
