package env

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFileHandlesCommentsQuotesAndEscapes(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(p, []byte("# comment\n\nA=1\nB=\"two\\nlines\"\nC='raw\\n'\nnoequals\nD = spaced \n"), 0o644)
	got, err := ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "1", "B": "two\nlines", "C": "raw\\n", "D": "spaced"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestParseFileMissingIsEmpty(t *testing.T) {
	got, err := ParseFile("/nonexistent/.env")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestResolveOnlyDeclaredKeysWithProcessFallback(t *testing.T) {
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, ".agentgopher"), 0o755)
	os.WriteFile(filepath.Join(repo, ".agentgopher", ".env"), []byte("TOKEN=\nFIXED=yes\nABSENT=\n"), 0o644)
	t.Setenv("TOKEN", "from-process")
	t.Setenv("UNDECLARED", "ignored")
	os.Unsetenv("ABSENT")
	got, err := Resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got["TOKEN"] != "from-process" || got["FIXED"] != "yes" {
		t.Fatalf("got %v", got)
	}
	if _, ok := got["UNDECLARED"]; ok {
		t.Fatal("undeclared key leaked")
	}
	if _, ok := got["ABSENT"]; ok {
		t.Fatal("empty key with no fallback must be dropped")
	}
}

func TestMergeProviderEnvPrecedenceAndOverlap(t *testing.T) {
	got, err := MergeProviderEnv(map[string]string{"A": "env", "B": "env"}, map[string]string{"A": "agent"}, map[string]string{"B": "sandbox"})
	if err != nil || got["A"] != "agent" || got["B"] != "sandbox" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := MergeProviderEnv(nil, map[string]string{"X": "1"}, map[string]string{"X": "2"}); err == nil {
		t.Fatal("expected overlap error")
	}
}
