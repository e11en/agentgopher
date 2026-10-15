// Package prompt resolves, substitutes and expands agent prompts.
package prompt

import (
	"fmt"
	"os"
	"strings"
)

// Error reports a prompt that could not be resolved, substituted or expanded.
// ExitCode is set when a shell expression exited non-zero, otherwise 0.
type Error struct {
	Message  string
	ExitCode int
}

func (e *Error) Error() string { return e.Message }

// Source says where a prompt came from.
type Source string

const (
	// SourceInline prompts are delivered to the agent verbatim.
	SourceInline Source = "inline"
	// SourceTemplate prompts get placeholder substitution and shell expansion.
	SourceTemplate Source = "template"
)

// Resolved is a prompt text plus its source.
type Resolved struct {
	Text   string
	Source Source
}

// Resolve returns the inline prompt, or reads file. Exactly one must be set.
// Line endings are normalised to LF.
func Resolve(inline, file string) (Resolved, error) {
	switch {
	case inline != "" && file != "":
		return Resolved{}, &Error{Message: "cannot provide both Prompt and PromptFile"}
	case inline != "":
		return Resolved{Text: inline, Source: SourceInline}, nil
	case file == "":
		return Resolved{}, &Error{Message: "must provide either Prompt or PromptFile; pass Prompt: \"...\" or PromptFile: \"./.agentgopher/prompt.md\" to Run"}
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return Resolved{}, &Error{Message: fmt.Sprintf("failed to read prompt from %s: %v", file, err)}
	}
	return Resolved{Text: strings.ReplaceAll(string(b), "\r\n", "\n"), Source: SourceTemplate}, nil
}
