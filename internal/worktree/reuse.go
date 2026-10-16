package worktree

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// reuseLog reports why a reused worktree was or was not refreshed.
var reuseLog = func(line string) { fmt.Fprintln(os.Stderr, line) }

// fastForwardFromOrigin refreshes a clean reused worktree from origin/<branch>
// when that is a fast-forward. Detached HEAD, a failed fetch and a diverged
// branch all skip the refresh; nothing here is fatal (ADR 0003).
func fastForwardFromOrigin(ctx context.Context, worktreePath, branch string) {
	head, err := gitRun(ctx, worktreePath, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || strings.TrimSpace(head) != "refs/heads/"+branch {
		reuseLog(fmt.Sprintf("Reusing worktree at %s (branch '%s') - HEAD is not on '%s', skipping origin refresh", worktreePath, branch, branch))
		return
	}
	if _, err := gitRun(ctx, worktreePath, "fetch", "origin", branch); err != nil {
		reuseLog(fmt.Sprintf("Could not fetch from origin (reusing worktree at %s as-is, branch '%s')", worktreePath, branch))
		return
	}
	before, _ := gitRun(ctx, worktreePath, "rev-parse", "HEAD")
	if _, err := gitRun(ctx, worktreePath, "merge", "--ff-only", "origin/"+branch); err != nil {
		reuseLog(fmt.Sprintf("Branch '%s' has diverged from origin (reusing worktree at %s as-is)", branch, worktreePath))
		return
	}
	after, _ := gitRun(ctx, worktreePath, "rev-parse", "HEAD")
	if strings.TrimSpace(before) != strings.TrimSpace(after) {
		reuseLog(fmt.Sprintf("Fast-forwarded worktree at %s (branch '%s') to origin/%s", worktreePath, branch, branch))
		return
	}
	reuseLog(fmt.Sprintf("Reusing existing worktree at %s (branch '%s')", worktreePath, branch))
}
