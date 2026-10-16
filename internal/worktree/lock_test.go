package worktree

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/e11en/agentgopher/internal/testrepo"
)

func TestAcquireCreatesAndReleasesLock(t *testing.T) {
	repo := testrepo.Init(t)
	release, err := Acquire(repo, "feat-x", "feat/x")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, LocksDir, "feat-x.lock")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lf lockFile
	if json.Unmarshal(b, &lf) != nil || lf.PID != os.Getpid() || lf.Branch != "feat/x" {
		t.Fatalf("lock = %s", b)
	}
	release()
	release()
	if _, err := os.Stat(path); err == nil {
		t.Fatal("lock not removed")
	}
}

func TestAcquireFailsWhenHeldByLiveProcess(t *testing.T) {
	repo := testrepo.Init(t)
	release, _ := Acquire(repo, "feat-x", "feat/x")
	defer release()
	_, err := Acquire(repo, "feat-x", "feat/x")
	var locked *LockedError
	if !errors.As(err, &locked) || locked.PID != os.Getpid() {
		t.Fatalf("err = %v", err)
	}
}

func TestAcquireReclaimsStaleLock(t *testing.T) {
	repo := testrepo.Init(t)
	os.MkdirAll(filepath.Join(repo, LocksDir), 0o755)
	stale, _ := json.Marshal(lockFile{PID: 999999999, Branch: "x", AcquiredAt: time.Now()})
	os.WriteFile(filepath.Join(repo, LocksDir, "feat-x.lock"), stale, 0o644)
	release, err := Acquire(repo, "feat-x", "feat/x")
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestCreateTakesLockAndSecondCreateFails(t *testing.T) {
	repo := testrepo.Init(t)
	info, err := Create(ctx, repo, "locked-branch", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Create(ctx, repo, "locked-branch", "")
	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("err = %v", err)
	}
	info.Unlock()
	if _, err := Create(ctx, repo, "locked-branch", ""); err != nil {
		t.Fatalf("after unlock: %v", err)
	}
}
