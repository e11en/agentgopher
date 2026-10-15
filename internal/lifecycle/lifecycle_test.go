package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/e11en/agentgopher/internal/display"
	"github.com/e11en/agentgopher/internal/testrepo"
	"github.com/e11en/agentgopher/sandbox"
	"github.com/e11en/agentgopher/sandbox/nosandbox"
)

func setup(t *testing.T) (string, sandbox.Handle, *bytes.Buffer) {
	t.Helper()
	repo := testrepo.Init(t)
	h, err := nosandbox.New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: repo})
	if err != nil {
		t.Fatal(err)
	}
	return repo, h, &bytes.Buffer{}
}

func TestRunCollectsCommitsMadeByWork(t *testing.T) {
	repo, h, log := setup(t)
	opts := Options{HostRepoDir: repo, SandboxRepoDir: repo, Branch: "main", Display: display.NewWriter(log)}
	res, err := Run(context.Background(), opts, h, func(c Context) (string, error) {
		if len(c.BaseHead) != 40 {
			t.Fatalf("BaseHead = %q", c.BaseHead)
		}
		os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x"), 0o644)
		_, _ = c.Sandbox.Exec(context.Background(), "git add . && git commit -q -m 'agent change'", sandbox.ExecOptions{})
		return "work-result", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Value != "work-result" || res.Branch != "main" || len(res.Commits) != 1 {
		t.Fatalf("res = %+v", res)
	}
	if !strings.Contains(log.String(), "Setting up sandbox") {
		t.Fatalf("log:\n%s", log.String())
	}
}

func TestRunNoCommits(t *testing.T) {
	repo, h, log := setup(t)
	res, err := Run(context.Background(), Options{HostRepoDir: repo, SandboxRepoDir: repo, Branch: "main", Display: display.NewWriter(log)}, h,
		func(Context) (int, error) { return 1, nil })
	if err != nil || len(res.Commits) != 0 {
		t.Fatalf("res = %+v, %v", res, err)
	}
}

func TestRunExecutesHooksAndLogsThem(t *testing.T) {
	repo, h, log := setup(t)
	opts := Options{HostRepoDir: repo, SandboxRepoDir: repo, Branch: "main", Display: display.NewWriter(log), Hooks: Hooks{
		SandboxOnSandboxReady: []SandboxHook{{Command: "touch from-sandbox"}},
		HostOnSandboxReady:    []HostHook{{Command: "touch from-host"}},
	}}
	if _, err := Run(context.Background(), opts, h, func(Context) (int, error) { return 0, nil }); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"from-sandbox", "from-host"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Fatalf("%s not created", f)
		}
	}
	if !strings.Contains(log.String(), "  touch from-sandbox") || !strings.Contains(log.String(), "  [host] touch from-host") {
		t.Fatalf("log:\n%s", log.String())
	}
}

func TestRunHookFailureAndTimeout(t *testing.T) {
	repo, h, log := setup(t)
	base := Options{HostRepoDir: repo, SandboxRepoDir: repo, Branch: "main", Display: display.NewWriter(log)}

	failing := base
	failing.Hooks = Hooks{SandboxOnSandboxReady: []SandboxHook{{Command: "exit 7"}}}
	_, err := Run(context.Background(), failing, h, func(Context) (int, error) { return 0, nil })
	var execErr *ExecError
	if !errors.As(err, &execErr) || execErr.ExitCode != 7 {
		t.Fatalf("err = %v", err)
	}

	slow := base
	slow.Hooks = Hooks{HostOnSandboxReady: []HostHook{{Command: "sleep 2", Timeout: 100 * time.Millisecond}}}
	_, err = Run(context.Background(), slow, h, func(Context) (int, error) { return 0, nil })
	var hookErr *HookTimeoutError
	if !errors.As(err, &hookErr) || hookErr.Command != "sleep 2" {
		t.Fatalf("err = %v", err)
	}
}

func TestRunHostHooksSequentialFailFast(t *testing.T) {
	dir := t.TempDir()
	err := RunHostHooks(context.Background(), []HostHook{{Command: "touch one"}, {Command: "exit 1"}, {Command: "touch three"}}, dir)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "three")); statErr == nil {
		t.Fatal("hooks after a failure must not run")
	}
}

func TestRunPropagatesWorkError(t *testing.T) {
	repo, h, log := setup(t)
	want := errors.New("agent failed")
	_, err := Run(context.Background(), Options{HostRepoDir: repo, SandboxRepoDir: repo, Branch: "main", Display: display.NewWriter(log)}, h,
		func(Context) (int, error) { return 0, want })
	if !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}

func TestRunGitSetupWritesOnlyWhenNotSkipped(t *testing.T) {
	repo, h, log := setup(t)
	globalConfig := os.Getenv("GIT_CONFIG_GLOBAL") // redirected by testrepo.Init
	base := Options{HostRepoDir: repo, SandboxRepoDir: repo, Branch: "main", Display: display.NewWriter(log)}

	skipped := base
	skipped.SkipGitSetup = true
	if _, err := Run(context.Background(), skipped, h, func(Context) (int, error) { return 0, nil }); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(globalConfig); strings.Contains(string(b), "safe.directory") || strings.Contains(string(b), "safedir") {
		t.Fatalf("global config touched with SkipGitSetup:\n%s", b)
	}

	if _, err := Run(context.Background(), base, h, func(Context) (int, error) { return 0, nil }); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(globalConfig)
	if err != nil {
		t.Fatalf("expected the redirected global config to be written: %v", err)
	}
	for _, want := range []string{"directory = " + repo, "name = Test User", "email = test@example.com"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %q in redirected global config:\n%s", want, b)
		}
	}
}
