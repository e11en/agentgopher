package prompt

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ShellBlockMarker sits between `!` and the backtick of shell blocks that
// were present in the raw template, so Expand only runs those.
const ShellBlockMarker = "\x01"

var (
	placeholderPattern = regexp.MustCompile(`\{\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}\}`)
	shellBlockPattern  = regexp.MustCompile("!`([^`]+)`")
)

// Args maps {{KEY}} placeholders to values.
type Args map[string]string

// BuiltInKeys are injected by the orchestrator and cannot be overridden.
var BuiltInKeys = []string{"SOURCE_BRANCH", "TARGET_BRANCH"}

// ValidateNoArgsWithInlinePrompt fails when args are passed with an inline
// prompt, which would silently ignore them.
func ValidateNoArgsWithInlinePrompt(args Args) error {
	if len(args) == 0 {
		return nil
	}
	return &Error{Message: "PromptArgs is only supported with PromptFile. Inline prompts are passed to the agent as-is; interpolate values in Go, or switch to PromptFile to use {{KEY}} substitution."}
}

// ValidateNoBuiltInOverride fails when args contains a built-in key.
func ValidateNoBuiltInOverride(args Args) error {
	for _, k := range BuiltInKeys {
		if _, ok := args[k]; ok {
			return &Error{Message: fmt.Sprintf("%q is a built-in prompt argument and cannot be overridden via PromptArgs", k)}
		}
	}
	return nil
}

// FindMissingKeys returns placeholder keys that are not in args, excluding
// built-ins, in order of first appearance.
func FindMissingKeys(prompt string, args Args) []string {
	seen := map[string]bool{}
	for _, k := range BuiltInKeys {
		seen[k] = true
	}
	var missing []string
	for _, m := range placeholderPattern.FindAllStringSubmatch(prompt, -1) {
		key := m[1]
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, ok := args[key]; !ok {
			missing = append(missing, key)
		}
	}
	return missing
}

// Substitute replaces {{KEY}} placeholders with args. Shell blocks written in
// the template are marked with ShellBlockMarker; markers in the input or in
// argument values are stripped so they cannot be forged. warn is called for
// every provided key that the prompt does not reference, except silentKeys.
func Substitute(prompt string, args Args, silentKeys map[string]bool, warn func(string)) (string, error) {
	prompt = strings.ReplaceAll(prompt, "\r\n", "\n")
	marked := strings.ReplaceAll(prompt, ShellBlockMarker, "")
	marked = shellBlockPattern.ReplaceAllString(marked, "!"+ShellBlockMarker+"`$1`")

	clean := make(Args, len(args))
	for k, v := range args {
		clean[k] = strings.ReplaceAll(v, ShellBlockMarker, "")
	}

	referenced := map[string]bool{}
	for _, m := range placeholderPattern.FindAllStringSubmatch(marked, -1) {
		referenced[m[1]] = true
	}
	for key := range referenced {
		if _, ok := clean[key]; !ok {
			return "", &Error{Message: fmt.Sprintf("prompt argument \"{{%s}}\" has no matching value in PromptArgs", key)}
		}
	}

	if warn != nil {
		unused := make([]string, 0)
		for k := range clean {
			if !referenced[k] && !silentKeys[k] {
				unused = append(unused, k)
			}
		}
		sort.Strings(unused)
		for _, k := range unused {
			warn(fmt.Sprintf("prompt argument %q was provided but not referenced in the prompt", k))
		}
	}

	return placeholderPattern.ReplaceAllStringFunc(marked, func(m string) string {
		return clean[placeholderPattern.FindStringSubmatch(m)[1]]
	}), nil
}
