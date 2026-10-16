package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncodeProjectPath(t *testing.T) {
	cases := map[string]string{
		"/Users/ellen/repo":  "-Users-ellen-repo",
		"/Users/ellen/repo/": "-Users-ellen-repo",
		"/":                  "-",
		`C:\work\repo`:       "C-work-repo",
	}
	for in, want := range cases {
		if got := EncodeProjectPath(in); got != want {
			t.Errorf("EncodeProjectPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClaudePaths(t *testing.T) {
	if got := ClaudeHostPath("/r", "abc", "/p"); got != filepath.Join("/p", "-r", "abc.jsonl") {
		t.Fatalf("host = %q", got)
	}
	if got := ClaudeSandboxPath("/home/agent/workspace", "abc", "/home/agent/.claude/projects"); got != "/home/agent/.claude/projects/-home-agent-workspace/abc.jsonl" {
		t.Fatalf("sandbox = %q", got)
	}
	if got := ClaudeSubagentsDirInSandbox("/w", "abc", "/p"); got != "/p/-w/abc/subagents" {
		t.Fatalf("subagents = %q", got)
	}
}

func TestTransferClaudeRewritesCwdAndKeepsTornLine(t *testing.T) {
	in := `{"type":"user","cwd":"/sandbox/repo","text":"hi"}
{"type":"session_meta","payload":{"cwd":"/sandbox/repo"}}
{"type":"other","cwd":"/elsewhere"}
{"type":"user","cwd":"/sandbox/re`
	out := TransferClaude(in, "/sandbox/repo", "/host/repo")
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[0], `"cwd":"/host/repo"`) || !strings.Contains(lines[1], `"cwd":"/host/repo"`) {
		t.Fatalf("not rewritten:\n%s", out)
	}
	if !strings.Contains(lines[2], "/elsewhere") {
		t.Fatalf("unrelated cwd changed:\n%s", out)
	}
	if lines[3] != `{"type":"user","cwd":"/sandbox/re` {
		t.Fatalf("torn line altered: %q", lines[3])
	}
	if TransferClaude("", "/a", "/b") != "" {
		t.Fatal("empty input must stay empty")
	}
}

func TestTransferPiRewritesOnlySessionHeader(t *testing.T) {
	in := `{"type":"session","id":"x","cwd":"/s"}
{"type":"message","cwd":"/s"}`
	out := TransferPi(in, "/s", "/h")
	lines := strings.Split(out, "\n")
	if !strings.Contains(lines[0], `"cwd":"/h"`) || !strings.Contains(lines[1], `"cwd":"/s"`) {
		t.Fatalf("got:\n%s", out)
	}
}

func TestFindClaudeOnHost(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "-some-dir"), 0o755)
	os.WriteFile(filepath.Join(root, "-some-dir", "sess-1.jsonl"), []byte("{}"), 0o644)
	found := FindClaudeOnHost("sess-1", root)
	if found.Path != filepath.Join(root, "-some-dir", "sess-1.jsonl") || found.SearchedRoot != root {
		t.Fatalf("got %+v", found)
	}
	if FindClaudeOnHost("nope", root).Path != "" {
		t.Fatal("expected not found")
	}
	if FindClaudeOnHost("x", filepath.Join(root, "missing")).Path != "" {
		t.Fatal("missing root must not fail")
	}
}

func TestCodexLookupScansDateDirs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "10", "15")
	os.MkdirAll(dir, 0o755)
	file := filepath.Join(dir, "rollout-2026-10-15T10-00-00-thread-9.jsonl")
	os.WriteFile(file, []byte("{}"), 0o644)
	if !IsCodexSessionFilename("rollout-x-thread-9.jsonl", "thread-9") || IsCodexSessionFilename("other-thread-9.jsonl", "thread-9") {
		t.Fatal("filename matching wrong")
	}
	loc, err := LocateCodexOnHost("thread-9", root)
	if err != nil || loc.Path != file || loc.RelativePath != filepath.Join("2026", "10", "15", "rollout-2026-10-15T10-00-00-thread-9.jsonl") {
		t.Fatalf("loc = %+v, %v", loc, err)
	}
	if _, err := LocateCodexOnHost("missing", root); err == nil {
		t.Fatal("expected error")
	}
	if FindCodexOnHost("thread-9", root).Path != file {
		t.Fatal("find failed")
	}
}

func TestPiEncodingAndLookup(t *testing.T) {
	if got := EncodePiSessionDir("/Users/ellen/repo"); got != "--Users-ellen-repo--" {
		t.Fatalf("encode = %q", got)
	}
	if got := EncodePiSessionDir(`C:\work`); got != "--C-work--" {
		t.Fatalf("encode windows = %q", got)
	}
	root := t.TempDir()
	dir := PiSessionDirPath("/r", root)
	os.MkdirAll(dir, 0o755)
	file := filepath.Join(dir, "2026-10-15T10-00-00_sess-7.jsonl")
	os.WriteFile(file, []byte("{}"), 0o644)
	loc, err := LocatePiOnHost("sess-7", root)
	if err != nil || loc.Path != file || loc.RelativePath != filepath.Join("--r--", "2026-10-15T10-00-00_sess-7.jsonl") {
		t.Fatalf("loc = %+v, %v", loc, err)
	}
	if FindPiOnHost("nope", root).Path != "" {
		t.Fatal("expected not found")
	}
}

func TestDefaultDirsUseHome(t *testing.T) {
	t.Setenv("HOME", "/home/x")
	if DefaultClaudeProjectsDir() != filepath.Join("/home/x", ".claude", "projects") {
		t.Fatal(DefaultClaudeProjectsDir())
	}
	if DefaultCodexSessionsDir() != filepath.Join("/home/x", ".codex", "sessions") {
		t.Fatal(DefaultCodexSessionsDir())
	}
	if DefaultPiSessionsDir() != filepath.Join("/home/x", ".pi", "agent", "sessions") {
		t.Fatal(DefaultPiSessionsDir())
	}
}
