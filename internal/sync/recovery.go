package sync

import (
	"strconv"
	"strings"
)

// FailedStep names the sync-out step that failed.
type FailedStep string

const (
	StepCommits   FailedStep = "commits"
	StepDiff      FailedStep = "diff"
	StepUntracked FailedStep = "untracked"
)

// RecoveryInput describes a failed sync-out.
type RecoveryInput struct {
	PatchDir     string
	FailedStep   FailedStep
	HasCommits   bool
	HasDiff      bool
	HasUntracked bool
}

type step struct {
	key   FailedStep
	label string
	cmd   string
}

// RecoveryMessage builds copy-pastable commands to finish a failed sync-out
// by hand from the preserved patch directory.
func RecoveryMessage(in RecoveryInput) string {
	var steps []step
	if in.HasCommits {
		steps = append(steps, step{StepCommits, "committed changes", "git am --3way " + in.PatchDir + "/*.patch"})
	}
	if in.HasDiff {
		steps = append(steps, step{StepDiff, "uncommitted changes", "git apply " + in.PatchDir + "/changes.patch"})
	}
	if in.HasUntracked {
		steps = append(steps, step{StepUntracked, "untracked files", "cp -r " + in.PatchDir + "/untracked/* ."})
	}
	failed := 0
	for i, s := range steps {
		if s.key == in.FailedStep {
			failed = i
		}
	}
	var b strings.Builder
	b.WriteString("Patch application failed at step " + itoa(failed+1) + " (" + steps[failed].label + ").\n\n")
	if in.FailedStep == StepCommits {
		b.WriteString("Resolve conflicts, then continue with:\n  git am --continue")
		if rest := commands(steps[failed+1:]); rest != "" {
			b.WriteString("\n\nAfter all commits are applied, run the remaining steps:\n" + rest)
		}
		return b.String()
	}
	b.WriteString("Run the remaining steps:\n" + commands(steps[failed:]))
	return b.String()
}

func commands(steps []step) string {
	lines := make([]string, len(steps))
	for i, s := range steps {
		if i < len(steps)-1 {
			lines[i] = "  " + s.cmd + " && \\"
		} else {
			lines[i] = "  " + s.cmd
		}
	}
	return strings.Join(lines, "\n")
}

func itoa(n int) string { return strconv.Itoa(n) }
