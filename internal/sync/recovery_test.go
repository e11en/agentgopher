package sync

import "testing"

func TestRecoveryMessage(t *testing.T) {
	cases := []struct {
		name string
		in   RecoveryInput
		want string
	}{
		{
			"am failure with remaining steps",
			RecoveryInput{PatchDir: ".agentgopher/patches/20261015-120000", FailedStep: StepCommits, HasCommits: true, HasDiff: true, HasUntracked: true},
			"Patch application failed at step 1 (committed changes).\n\nResolve conflicts, then continue with:\n  git am --continue\n\nAfter all commits are applied, run the remaining steps:\n  git apply .agentgopher/patches/20261015-120000/changes.patch && \\\n  cp -r .agentgopher/patches/20261015-120000/untracked/* .",
		},
		{
			"am failure alone",
			RecoveryInput{PatchDir: "p", FailedStep: StepCommits, HasCommits: true},
			"Patch application failed at step 1 (committed changes).\n\nResolve conflicts, then continue with:\n  git am --continue",
		},
		{
			"diff failure after commits",
			RecoveryInput{PatchDir: "p", FailedStep: StepDiff, HasCommits: true, HasDiff: true},
			"Patch application failed at step 2 (uncommitted changes).\n\nRun the remaining steps:\n  git apply p/changes.patch",
		},
		{
			"untracked failure only",
			RecoveryInput{PatchDir: "p", FailedStep: StepUntracked, HasUntracked: true},
			"Patch application failed at step 1 (untracked files).\n\nRun the remaining steps:\n  cp -r p/untracked/* .",
		},
	}
	for _, c := range cases {
		if got := RecoveryMessage(c.in); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}
