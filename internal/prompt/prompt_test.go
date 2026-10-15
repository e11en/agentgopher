package prompt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

func TestResolveInline(t *testing.T) {
	r, err := Resolve("do it", "")
	if err != nil || r.Text != "do it" || r.Source != SourceInline {
		t.Fatalf("got %+v, %v", r, err)
	}
}

func TestResolveFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "prompt.md")
	os.WriteFile(p, []byte("from file"), 0o644)
	r, err := Resolve("", p)
	if err != nil || r.Text != "from file" || r.Source != SourceTemplate {
		t.Fatalf("got %+v, %v", r, err)
	}
}

func TestResolveRejectsBothAndNeither(t *testing.T) {
	var perr *Error
	if _, err := Resolve("a", "b"); !errors.As(err, &perr) {
		t.Fatalf("both: err = %v", err)
	}
	if _, err := Resolve("", ""); !errors.As(err, &perr) {
		t.Fatalf("neither: err = %v", err)
	}
}

func TestResolveMissingFile(t *testing.T) {
	var perr *Error
	_, err := Resolve("", "/nonexistent/prompt.md")
	if !errors.As(err, &perr) || !strings.Contains(perr.Message, "/nonexistent/prompt.md") {
		t.Fatalf("err = %v", err)
	}
}

func TestSubstituteReplacesPlaceholders(t *testing.T) {
	out, err := Substitute("Work on {{ BRANCH }} for {{OWNER}}", Args{"BRANCH": "main", "OWNER": "ellen"}, nil, nil)
	if err != nil || out != "Work on main for ellen" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestSubstituteFailsOnMissingKey(t *testing.T) {
	_, err := Substitute("{{MISSING}}", Args{}, nil, nil)
	var perr *Error
	if !errors.As(err, &perr) || !strings.Contains(perr.Message, "{{MISSING}}") {
		t.Fatalf("err = %v", err)
	}
}

func TestSubstituteWarnsOnUnusedKeyExceptSilent(t *testing.T) {
	var warnings []string
	_, err := Substitute("no placeholders", Args{"UNUSED": "x", "SOURCE_BRANCH": "y"},
		map[string]bool{"SOURCE_BRANCH": true}, func(m string) { warnings = append(warnings, m) })
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "UNUSED") {
		t.Fatalf("warnings = %q", warnings)
	}
}

func TestSubstituteMarksTemplateShellBlocksOnly(t *testing.T) {
	out, err := Substitute("!`ls` and {{CMD}}", Args{"CMD": "!`rm -rf /`"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "!"+ShellBlockMarker+"`ls` and !`rm -rf /`" {
		t.Fatalf("got %q", out)
	}
}

func TestSubstituteStripsForgedMarkers(t *testing.T) {
	out, err := Substitute("!"+ShellBlockMarker+"`forged`", Args{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The forged marker is stripped, then the block is marked again as a
	// genuine template block, which is the same result as a clean template.
	if strings.Count(out, ShellBlockMarker) != 1 {
		t.Fatalf("got %q", out)
	}
}

func TestSubstituteHandlesCRLF(t *testing.T) {
	out, err := Substitute("line1\r\n{{A}}\r\n", Args{"A": "x"}, nil, nil)
	if err != nil || strings.Contains(out, "\r") || out != "line1\nx\n" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestFindMissingKeys(t *testing.T) {
	got := FindMissingKeys("{{A}} {{B}} {{A}} {{SOURCE_BRANCH}}", Args{"A": "1"})
	if len(got) != 1 || got[0] != "B" {
		t.Fatalf("got %v", got)
	}
}

func TestValidateBuiltIns(t *testing.T) {
	if err := ValidateNoBuiltInOverride(Args{"TARGET_BRANCH": "x"}); err == nil {
		t.Fatal("expected error")
	}
	if err := ValidateNoArgsWithInlinePrompt(Args{"A": "x"}); err == nil {
		t.Fatal("expected error")
	}
	if err := ValidateNoArgsWithInlinePrompt(Args{}); err != nil {
		t.Fatal(err)
	}
}

func TestExpandRunsMarkedBlocksAndStripsMarkers(t *testing.T) {
	h := newHostHandle(t)
	in := "A: !" + ShellBlockMarker + "`echo one`\nB: !`echo untouched`\nC: !" + ShellBlockMarker + "`printf ''`."
	var logs []string
	out, err := Expand(context.Background(), in, h, h.WorktreePath(), func(m string) { logs = append(logs, m) })
	if err != nil {
		t.Fatal(err)
	}
	if out != "A: one\nB: !`echo untouched`\nC: ." {
		t.Fatalf("got %q", out)
	}
	if len(logs) != 2 {
		t.Fatalf("logs = %q", logs)
	}
}

func TestExpandNoBlocksStripsStrayMarkers(t *testing.T) {
	out, err := Expand(context.Background(), "plain "+ShellBlockMarker+"text", nil, "", nil)
	if err != nil || out != "plain text" {
		t.Fatalf("got %q, %v", out, err)
	}
}

func TestExpandFailsOnNonZeroExit(t *testing.T) {
	h := newHostHandle(t)
	_, err := Expand(context.Background(), "!"+ShellBlockMarker+"`echo bad 1>&2; exit 2`", h, h.WorktreePath(), nil)
	var perr *Error
	if !errors.As(err, &perr) || perr.ExitCode != 2 || !strings.Contains(perr.Message, "bad") {
		t.Fatalf("err = %v", err)
	}
}

func TestExpandTimesOut(t *testing.T) {
	h := newHostHandle(t)
	old := expansionTimeout
	expansionTimeout = 100 * time.Millisecond
	defer func() { expansionTimeout = old }()
	_, err := Expand(context.Background(), "!"+ShellBlockMarker+"`sleep 2`", h, h.WorktreePath(), nil)
	var terr *ExpansionTimeoutError
	if !errors.As(err, &terr) || terr.Expression != "sleep 2" {
		t.Fatalf("err = %v", err)
	}
}

func newHostHandle(t *testing.T) sandbox.Handle {
	t.Helper()
	h, err := nosandbox.New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return h
}
