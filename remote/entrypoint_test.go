package remote

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shimTools puts fake git, gh and go on PATH. Each appends its argv to
// <dir>/calls and behaves per the given script body.
func shimTools(t *testing.T, revListCount string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\necho \""+name+" $*\" >> "+filepath.Join(dir, "calls")+"\n"+body), 0o755)
	}
	write("git", `case "$1 $2" in
  "clone --branch") mkdir -p "$5" ;;
  "rev-list "*) echo `+revListCount+` ;;
  "config user.name") [ -z "$3" ] && exit 1 ;;
  "config user.email") [ -z "$3" ] && exit 1 ;;
esac
exit 0`)
	write("gh", `case "$1 $2" in
  "pr create") echo https://github.com/e11en/demo/pull/7 ;;
esac
exit 0`)
	write("go", `exit 0`)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return dir
}

func calls(t *testing.T, dir string) string {
	b, _ := os.ReadFile(filepath.Join(dir, "calls"))
	return string(b)
}

func TestLoadSecretsReadsFilesAndIgnoresMissingDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "GH_TOKEN"), []byte("tok\n"), 0o600)
	os.MkdirAll(filepath.Join(dir, "..data"), 0o755)
	got, err := LoadSecrets(dir)
	if err != nil || len(got) != 1 || got["GH_TOKEN"] != "tok" {
		t.Fatalf("got %v, %v", got, err)
	}
	if got, err := LoadSecrets(filepath.Join(dir, "nope")); err != nil || len(got) != 0 {
		t.Fatalf("missing dir: %v, %v", got, err)
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("AGENTGOPHER_REPO_URL", "https://x/y.git")
	t.Setenv("AGENTGOPHER_SCRIPT", ".agentgopher")
	t.Setenv("AGENTGOPHER_BRANCH", "agentgopher/1")
	t.Setenv("AGENTGOPHER_BASE_BRANCH", "main")
	cfg, err := ConfigFromEnv()
	if err != nil || cfg.WorkDir != "/work/repo" || cfg.Branch != "agentgopher/1" {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
	t.Setenv("AGENTGOPHER_REPO_URL", "")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("expected error without repo url")
	}
}

func TestRunPushesAndOpensPR(t *testing.T) {
	dir := shimTools(t, "2")
	t.Setenv("GH_TOKEN", "tok")
	var out bytes.Buffer
	cfg := EntrypointConfig{RepoURL: "https://x/y.git", Script: ".agentgopher", Branch: "agentgopher/1", BaseBranch: "main", WorkDir: filepath.Join(t.TempDir(), "repo"), Stdout: &out}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	c := calls(t, dir)
	for _, want := range []string{
		"gh auth setup-git",
		"git clone --branch main https://x/y.git " + cfg.WorkDir,
		"git checkout -b agentgopher/1",
		"go run ./.agentgopher",
		"git push -u origin agentgopher/1",
		"gh pr create --base main --head agentgopher/1",
	} {
		if !strings.Contains(c, want) {
			t.Fatalf("missing %q in calls:\n%s", want, c)
		}
	}
	if !strings.Contains(out.String(), "PR: https://github.com/e11en/demo/pull/7\n") {
		t.Fatalf("stdout:\n%s", out.String())
	}
}

func TestRunWithoutCommitsSkipsPushAndPR(t *testing.T) {
	dir := shimTools(t, "0")
	var out bytes.Buffer
	cfg := EntrypointConfig{RepoURL: "https://x/y.git", Script: ".agentgopher", Branch: "agentgopher/1", BaseBranch: "main", WorkDir: filepath.Join(t.TempDir(), "repo"), Stdout: &out}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	c := calls(t, dir)
	if strings.Contains(c, "git push") || strings.Contains(c, "pr create") {
		t.Fatalf("must not push or open PR:\n%s", c)
	}
	if !strings.Contains(out.String(), "No commits produced, no PR") {
		t.Fatalf("stdout:\n%s", out.String())
	}
}

func TestRunSecretsOverrideEnvAndScriptSeesBranch(t *testing.T) {
	dir := shimTools(t, "0")
	secrets := t.TempDir()
	os.WriteFile(filepath.Join(secrets, "GH_TOKEN"), []byte("from-secret"), 0o600)
	t.Setenv("GH_TOKEN", "stale")
	os.WriteFile(filepath.Join(dir, "go"), []byte("#!/bin/sh\necho \"go $* GH_TOKEN=$GH_TOKEN BRANCH=$AGENTGOPHER_BRANCH\" >> "+filepath.Join(dir, "calls")+"\nexit 0"), 0o755)
	cfg := EntrypointConfig{RepoURL: "u", Script: "s", Branch: "b", BaseBranch: "main", SecretsDir: secrets, WorkDir: filepath.Join(t.TempDir(), "repo"), Stdout: &bytes.Buffer{}}
	if err := run(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(calls(t, dir), "GH_TOKEN=from-secret BRANCH=b") {
		t.Fatalf("calls:\n%s", calls(t, dir))
	}
}

func TestRunFailingScriptReturnsError(t *testing.T) {
	dir := shimTools(t, "0")
	os.WriteFile(filepath.Join(dir, "go"), []byte("#!/bin/sh\necho boom 1>&2\nexit 5"), 0o755)
	cfg := EntrypointConfig{RepoURL: "u", Script: "s", Branch: "b", BaseBranch: "main", WorkDir: filepath.Join(t.TempDir(), "repo"), Stdout: &bytes.Buffer{}}
	err := run(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "go run") {
		t.Fatalf("err = %v", err)
	}
}
