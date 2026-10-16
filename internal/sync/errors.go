// Package sync moves a repo into an isolated sandbox and its changes back out.
package sync

import (
	"fmt"
	"time"
)

// Error reports a failed sync step.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

// InTimeoutError reports a sync-in that ran longer than Timeout.
type InTimeoutError struct{ Timeout time.Duration }

func (e *InTimeoutError) Error() string { return fmt.Sprintf("sync-in timed out after %v", e.Timeout) }
