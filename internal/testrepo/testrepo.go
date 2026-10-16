// Package testrepo builds throwaway git repositories for tests.
package testrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Init creates a repo with one commit on branch main and returns its path.
// The global git config is redirected to a throwaway file for the test, so
// code under test can never touch the developer's ~/.gitconfig.
func Init(t testing.TB) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	dir := t.TempDir()
	Git(t, dir, "init", "-q", "-b", "main")
	Git(t, dir, "config", "user.name", "Test User")
	Git(t, dir, "config", "user.email", "test@example.com")
	Commit(t, dir, "README.md", "hi\n", "initial")
	return dir
}

// Git runs git in dir and fails the test on error.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// Commit writes file with content inside dir and commits it.
func Commit(t testing.TB, dir, file, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	Git(t, dir, "add", file)
	Git(t, dir, "commit", "-q", "-m", message)
}
