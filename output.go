package agentgopher

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// OutputDefinition declares structured output extracted from an XML tag in
// the agent's stdout after the run (ADR 0010).
type OutputDefinition interface {
	Tag() string
	MaxRetries() int
	extract(raw string) (any, error)
}

type outputConfig struct {
	tag        string
	maxRetries int
}

func (c outputConfig) Tag() string     { return c.tag }
func (c outputConfig) MaxRetries() int { return c.maxRetries }

// OutputOption configures an OutputDefinition.
type OutputOption func(*outputConfig)

// WithMaxRetries lets Run resume the failed session up to n times with a
// feedback prompt when extraction or validation fails. Requires a provider
// with session storage.
func WithMaxRetries(n int) OutputOption { return func(c *outputConfig) { c.maxRetries = n } }

type outputObject[T any] struct {
	outputConfig
	validate func(T) error
}

// OutputObject declares JSON output decoded into T. validate, when set, runs
// after decoding and its error becomes the StructuredOutputError cause.
func OutputObject[T any](tag string, validate func(T) error, opts ...OutputOption) OutputDefinition {
	c := outputConfig{tag: tag}
	for _, o := range opts {
		o(&c)
	}
	return outputObject[T]{outputConfig: c, validate: validate}
}

type parseError struct{ cause error }

func (e parseError) Error() string { return e.cause.Error() }

type validationError struct{ cause error }

func (e validationError) Error() string { return e.cause.Error() }

func (d outputObject[T]) extract(raw string) (any, error) {
	var v T
	if err := json.Unmarshal([]byte(unwrapFences(strings.TrimSpace(raw))), &v); err != nil {
		return nil, parseError{err}
	}
	if d.validate != nil {
		if err := d.validate(v); err != nil {
			return nil, validationError{err}
		}
	}
	return v, nil
}

type outputString struct{ outputConfig }

// OutputString declares output returned as a trimmed string, without parsing.
func OutputString(tag string, opts ...OutputOption) OutputDefinition {
	c := outputConfig{tag: tag}
	for _, o := range opts {
		o(&c)
	}
	return outputString{c}
}

func (outputString) extract(raw string) (any, error) { return strings.TrimSpace(raw), nil }

// StructuredOutputError reports a missing tag, invalid JSON or failed
// validation. It carries the run's side effects so the caller can recover.
type StructuredOutputError struct {
	Tag                   string
	Message               string
	RawMatched            string
	Found                 bool
	Cause                 error
	Commits               []Commit
	Branch                string
	PreservedWorktreePath string
	SessionID             string
	SessionFilePath       string
}

func (e *StructuredOutputError) Error() string { return e.Message }
func (e *StructuredOutputError) Unwrap() error { return e.Cause }

type extractionContext struct {
	Commits               []Commit
	Branch                string
	PreservedWorktreePath string
	SessionID             string
	SessionFilePath       string
}

// extractStructuredOutput finds the last <tag>...</tag> in stdout and decodes it.
func extractStructuredOutput(stdout string, def OutputDefinition, ctx extractionContext) (any, error) {
	fail := func(msg, raw string, found bool, cause error) error {
		return &StructuredOutputError{Tag: def.Tag(), Message: msg, RawMatched: raw, Found: found, Cause: cause,
			Commits: ctx.Commits, Branch: ctx.Branch, PreservedWorktreePath: ctx.PreservedWorktreePath, SessionID: ctx.SessionID, SessionFilePath: ctx.SessionFilePath}
	}
	raw, found := findLastTagContent(stdout, def.Tag())
	if !found {
		return nil, fail(fmt.Sprintf("structured output tag <%s> not found in agent output", def.Tag()), "", false, nil)
	}
	v, err := def.extract(raw)
	switch e := err.(type) {
	case nil:
		return v, nil
	case parseError:
		return nil, fail(fmt.Sprintf("structured output tag <%s> contains invalid JSON", def.Tag()), raw, true, e.cause)
	case validationError:
		return nil, fail(fmt.Sprintf("structured output tag <%s> failed validation", def.Tag()), raw, true, e.cause)
	default:
		return nil, fail(err.Error(), raw, true, err)
	}
}

// findLastTagContent returns the content of the last complete <tag>...</tag> pair.
func findLastTagContent(text, tag string) (string, bool) {
	open, closeTag := "<"+tag+">", "</"+tag+">"
	var last string
	found := false
	from := 0
	for {
		i := strings.Index(text[from:], open)
		if i < 0 {
			break
		}
		start := from + i + len(open)
		j := strings.Index(text[start:], closeTag)
		if j < 0 {
			break
		}
		last, found = text[start:start+j], true
		from = start + j + len(closeTag)
	}
	return last, found
}

var fencePattern = regexp.MustCompile("(?s)^```(?:json)?[ \t]*\n(.*?)\n[ \t]*```[ \t]*$")

// unwrapFences strips one optional ```json fence around the payload.
func unwrapFences(text string) string {
	if m := fencePattern.FindStringSubmatch(text); m != nil {
		return strings.TrimSpace(m[1])
	}
	return text
}

// BuildStructuredOutputRetryFeedback is the prompt sent on a retry: the agent
// already did the work in the resumed session, it only re-emits the tag.
func BuildStructuredOutputRetryFeedback(e *StructuredOutputError, retriesRemaining int) string {
	raw := "(no matching tag was emitted)"
	if e.Found {
		raw = e.RawMatched
	}
	cause := "(no parser detail)"
	if e.Cause != nil {
		cause = e.Cause.Error()
	}
	return fmt.Sprintf(`Your previous response did not produce valid structured output.

Retries remaining after this attempt: %d.

Problem:
%s

Parser detail:
%s

Previous matched output:
%s

Emit only a corrected <%s> block. Do not change files or run commands.`, retriesRemaining, e.Message, cause, raw, e.Tag)
}
