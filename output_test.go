package agentgopher

import (
	"errors"
	"strings"
	"testing"
)

type answer struct {
	Answer int `json:"answer"`
}

func TestOutputObjectLastMatchWinsAndUnwrapsFence(t *testing.T) {
	def := OutputObject[answer]("result", nil)
	stdout := "draft <result>{\"answer\":1}</result> fixed <result>\n```json\n{\"answer\": 42}\n```\n</result>"
	got, err := extractStructuredOutput(stdout, def, extractionContext{})
	if err != nil || got.(answer).Answer != 42 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestOutputObjectErrors(t *testing.T) {
	def := OutputObject[answer]("result", func(a answer) error {
		if a.Answer < 0 {
			return errors.New("must be positive")
		}
		return nil
	})
	var soe *StructuredOutputError

	_, err := extractStructuredOutput("no tag here", def, extractionContext{Branch: "b", Commits: []Commit{{SHA: "x"}}})
	if !errors.As(err, &soe) || soe.Found || soe.Branch != "b" || len(soe.Commits) != 1 || !strings.Contains(soe.Message, "not found") {
		t.Fatalf("missing: %+v", err)
	}
	_, err = extractStructuredOutput("<result>{nope</result>", def, extractionContext{})
	if !errors.As(err, &soe) || !soe.Found || soe.RawMatched != "{nope" || soe.Cause == nil || !strings.Contains(soe.Message, "invalid JSON") {
		t.Fatalf("json: %+v", err)
	}
	_, err = extractStructuredOutput(`<result>{"answer":-1}</result>`, def, extractionContext{SessionID: "s"})
	if !errors.As(err, &soe) || soe.SessionID != "s" || !strings.Contains(soe.Message, "failed validation") || soe.Cause.Error() != "must be positive" {
		t.Fatalf("validate: %+v", err)
	}
}

func TestOutputStringTrims(t *testing.T) {
	got, err := extractStructuredOutput("<summary>\n  hello \n</summary>", OutputString("summary"), extractionContext{})
	if err != nil || got != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
	if OutputString("s", WithMaxRetries(2)).MaxRetries() != 2 || OutputString("s").MaxRetries() != 0 {
		t.Fatal("MaxRetries")
	}
}

func TestRetryFeedbackMentionsEverything(t *testing.T) {
	msg := BuildStructuredOutputRetryFeedback(&StructuredOutputError{Tag: "result", Message: "bad", RawMatched: "{x", Found: true, Cause: errors.New("unexpected x")}, 1)
	for _, want := range []string{"Retries remaining after this attempt: 1", "bad", "unexpected x", "{x", "<result>"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missing %q in %q", want, msg)
		}
	}
	msg = BuildStructuredOutputRetryFeedback(&StructuredOutputError{Tag: "r"}, 0)
	if !strings.Contains(msg, "(no matching tag was emitted)") || !strings.Contains(msg, "(no parser detail)") {
		t.Fatalf("got %q", msg)
	}
}
