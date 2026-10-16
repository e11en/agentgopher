package initcmd

import (
	"strings"
	"testing"
)

func joined(lines []string) string { return strings.Join(lines, "\n") }

func TestNextStepsCustomTrackerWinsOverTemplate(t *testing.T) {
	custom, _ := GetIssueTracker("custom")
	pi, _ := GetAgent("pi")
	s := joined(NextStepsLines("parallel-planner", custom, pi))
	if !strings.Contains(s, pi.SetupCommand) || !strings.Contains(s, "Runs on the host") || !strings.Contains(s, SetupIssueTrackerDoc) {
		t.Fatalf("got:\n%s", s)
	}
	if strings.Contains(s, "go run -C") {
		t.Fatal("custom tracker steps must not include the normal run step")
	}
}

func TestNextStepsBlank(t *testing.T) {
	gh, _ := GetIssueTracker("github-issues")
	cc, _ := GetAgent("claude-code")
	s := joined(NextStepsLines("blank", gh, cc))
	for _, want := range []string{"Next steps:", ".agentgopher/.env", "claude setup-token", "prompt.md", ".agentgopher/main.go", "go run -C .agentgopher .", "go mod tidy"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "CODING_STANDARDS") {
		t.Fatal("blank must not mention coding standards")
	}
}

func TestNextStepsReviewerAndNonClaude(t *testing.T) {
	gh, _ := GetIssueTracker("github-issues")
	codex, _ := GetAgent("codex")
	s := joined(NextStepsLines("sequential-reviewer", gh, codex))
	if !strings.Contains(s, "CODING_STANDARDS.md") || !strings.Contains(s, "CopyToWorktree") || strings.Contains(s, "claude setup-token") {
		t.Fatalf("got:\n%s", s)
	}
	s = joined(NextStepsLines("simple-loop", gh, codex))
	if strings.Contains(s, "CODING_STANDARDS.md") {
		t.Fatal("simple-loop must not mention coding standards")
	}
}

func TestNextStepsJiraMentionsLogin(t *testing.T) {
	jira, _ := GetIssueTracker("jira")
	cc, _ := GetAgent("claude-code")
	s := joined(NextStepsLines("simple-loop", jira, cc))
	if !strings.Contains(s, "JIRA_API_TOKEN") {
		t.Fatalf("got:\n%s", s)
	}
}
