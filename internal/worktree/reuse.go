package worktree

import (
	"context"
	"fmt"
	"os"
)

// fastForwardFromOrigin is implemented in reuse.go (Task 5).
func fastForwardFromOrigin(ctx context.Context, worktreePath, branch string) {
	fmt.Fprintf(os.Stderr, "Reusing existing worktree at %s (branch '%s')\n", worktreePath, branch)
}
