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
	"github.com/e11en/agentgopher/internal/worktree"
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

	// plainHandle hides RunsOnHost, so the lifecycle treats it as a real sandbox.
	type plainHandle struct{ sandbox.Handle }
	if _, err := Run(context.Background(), base, plainHandle{h}, func(Context) (int, error) { return 0, nil }); err != nil {
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

func TestRunMergeToHeadMergesAndDeletesTempBranch(t *testing.T) {
	repo := testrepo.Init(t)
	wt, err := worktree.CreateTemp(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	h, _ := nosandbox.New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: wt.Path})
	log := &bytes.Buffer{}
	res, err := Run(context.Background(), Options{
		HostRepoDir: repo, SandboxRepoDir: wt.Path, HostWorktreePath: wt.Path, Display: display.NewWriter(log),
	}, h, func(c Context) (int, error) {
		testrepo.Commit(t, wt.Path, "agent.txt", "x", "agent change")
		return 1, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Branch != "main" || len(res.Commits) != 1 {
		t.Fatalf("res = %+v", res)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "agent.txt")); statErr != nil {
		t.Fatal("commit not merged into main")
	}
	if strings.Contains(testrepo.Git(t, repo, "branch", "--list", wt.Branch), wt.Branch) {
		t.Fatal("temp branch not deleted")
	}
	if !strings.Contains(log.String(), "Merging to main") {
		t.Fatalf("log:\n%s", log.String())
	}
}

func TestRunMergeToHeadWithDivergedHostIsRealMerge(t *testing.T) {
	repo := testrepo.Init(t)
	wt, _ := worktree.CreateTemp(context.Background(), repo, "")
	h, _ := nosandbox.New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: wt.Path})
	res, err := Run(context.Background(), Options{HostRepoDir: repo, SandboxRepoDir: wt.Path, HostWorktreePath: wt.Path, Display: display.NewWriter(&bytes.Buffer{})}, h,
		func(Context) (int, error) {
			testrepo.Commit(t, repo, "host.txt", "h", "host moved")
			testrepo.Commit(t, wt.Path, "agent.txt", "a", "agent change")
			return 1, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	// host commit, agent commit and the merge commit
	if len(res.Commits) != 3 {
		t.Fatalf("commits = %v", res.Commits)
	}
}

func TestRunMergeConflictPreservesTempBranch(t *testing.T) {
	repo := testrepo.Init(t)
	wt, _ := worktree.CreateTemp(context.Background(), repo, "")
	h, _ := nosandbox.New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: wt.Path})
	_, err := Run(context.Background(), Options{HostRepoDir: repo, SandboxRepoDir: wt.Path, HostWorktreePath: wt.Path, Display: display.NewWriter(&bytes.Buffer{})}, h,
		func(Context) (int, error) {
			testrepo.Commit(t, repo, "README.md", "host version\n", "host edit")
			testrepo.Commit(t, wt.Path, "README.md", "agent version\n", "agent edit")
			return 1, nil
		})
	var merr *MergeError
	if !errors.As(err, &merr) || !strings.Contains(merr.Message, "git merge "+wt.Branch) {
		t.Fatalf("err = %v", err)
	}
	testrepo.Git(t, repo, "merge", "--abort")
	if !strings.Contains(testrepo.Git(t, repo, "branch", "--list", wt.Branch), wt.Branch) {
		t.Fatal("temp branch must be preserved on conflict")
	}
}

func TestRunMergeToHeadNoCommitsSkipsMerge(t *testing.T) {
	repo := testrepo.Init(t)
	wt, _ := worktree.CreateTemp(context.Background(), repo, "")
	h, _ := nosandbox.New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: wt.Path})
	log := &bytes.Buffer{}
	res, err := Run(context.Background(), Options{HostRepoDir: repo, SandboxRepoDir: wt.Path, HostWorktreePath: wt.Path, Display: display.NewWriter(log)}, h,
		func(Context) (int, error) { return 1, nil })
	if err != nil || len(res.Commits) != 0 || strings.Contains(log.String(), "Merging") {
		t.Fatalf("res=%+v err=%v log=%s", res, err, log.String())
	}
}

func TestRunKeepSourceBranch(t *testing.T) {
	repo := testrepo.Init(t)
	wt, _ := worktree.CreateTemp(context.Background(), repo, "")
	h, _ := nosandbox.New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: wt.Path})
	_, err := Run(context.Background(), Options{HostRepoDir: repo, SandboxRepoDir: wt.Path, HostWorktreePath: wt.Path, KeepSourceBranch: true, Display: display.NewWriter(&bytes.Buffer{})}, h,
		func(Context) (int, error) { testrepo.Commit(t, wt.Path, "a.txt", "a", "agent"); return 1, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(testrepo.Git(t, wt.Path, "rev-parse", "--abbrev-ref", "HEAD")); got != wt.Branch {
		t.Fatalf("worktree detached or moved: %q", got)
	}
}

func TestRunCallsApplyToHostBeforeMerge(t *testing.T) {
	repo := testrepo.Init(t)
	wt, _ := worktree.CreateTemp(context.Background(), repo, "")
	h, _ := nosandbox.New(nil).Create(context.Background(), sandbox.NoSandboxCreateOptions{WorktreePath: wt.Path})
	applied := false
	res, err := Run(context.Background(), Options{HostRepoDir: repo, SandboxRepoDir: wt.Path, HostWorktreePath: wt.Path, Display: display.NewWriter(&bytes.Buffer{}),
		ApplyToHost: func(context.Context) error {
			applied = true
			testrepo.Commit(t, wt.Path, "synced.txt", "s", "synced")
			return nil
		},
	}, h, func(Context) (int, error) { return 1, nil })
	if err != nil || !applied || len(res.Commits) != 1 {
		t.Fatalf("res=%+v err=%v applied=%v", res, err, applied)
	}
}

func TestRunNeverTouchesGlobalConfigThroughHostHandle(t *testing.T) {
	repo, h, log := setup(t)
	globalConfig := os.Getenv("GIT_CONFIG_GLOBAL")
	// SkipGitSetup deliberately false: the handle itself must stop the writes.
	_, err := Run(context.Background(), Options{HostRepoDir: repo, SandboxRepoDir: repo, Branch: "main", Display: display.NewWriter(log)}, h,
		func(Context) (int, error) { return 0, nil })
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(globalConfig); strings.Contains(string(b), "safe") || strings.Contains(string(b), "Test User") {
		t.Fatalf("global config touched through a host handle:\n%s", b)
	}
}
