package agentgopher

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestErrorsMatchWithErrorsAs(t *testing.T) {
	wrapped := fmt.Errorf("iteration 1: %w", &AgentIdleTimeoutError{Timeout: 5 * time.Second})
	var idle *AgentIdleTimeoutError
	if !errors.As(wrapped, &idle) {
		t.Fatal("expected errors.As to find AgentIdleTimeoutError")
	}
	if idle.Timeout != 5*time.Second {
		t.Fatalf("Timeout = %v, want 5s", idle.Timeout)
	}
	if got := idle.Error(); got != "agent idle for 5s - no output received" {
		t.Fatalf("Error() = %q", got)
	}
}

func TestExecErrorMessage(t *testing.T) {
	err := &ExecError{Command: "git status", ExitCode: 128, Stderr: "fatal: not a git repo"}
	want := "command failed (exit 128): git status\nfatal: not a git repo"
	if err.Error() != want {
		t.Fatalf("Error() = %q, want %q", err.Error(), want)
	}
}
