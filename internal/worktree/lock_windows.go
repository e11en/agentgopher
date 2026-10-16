//go:build windows

package worktree

import "os"

// shortcut: FindProcess on Windows fails only for a missing PID, which is the
// stale case we need; a PID that was recycled by another program still counts as live.
func processAlive(pid int) bool {
	_, err := os.FindProcess(pid)
	return err == nil
}
