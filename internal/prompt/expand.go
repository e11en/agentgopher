package prompt

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/e11en/agentgopher/sandbox"
)

// ExpansionTimeout bounds each shell expression.
const ExpansionTimeout = 30 * time.Second

// expansionTimeout is a variable so tests can shorten it.
var expansionTimeout = ExpansionTimeout

var markedShellBlockPattern = regexp.MustCompile("!" + ShellBlockMarker + "`([^`]+)`")

// Executor runs a command in the sandbox. sandbox.Handle satisfies it.
type Executor interface {
	Exec(ctx context.Context, command string, opts sandbox.ExecOptions) (sandbox.ExecResult, error)
}

// ExpansionTimeoutError reports a shell expression that ran longer than Timeout.
type ExpansionTimeoutError struct {
	Expression string
	Timeout    time.Duration
	Elapsed    time.Duration
}

func (e *ExpansionTimeoutError) Error() string {
	return fmt.Sprintf("shell expression `%s` timed out after %v", e.Expression, e.Elapsed.Round(time.Millisecond))
}

type expansion struct {
	out string
	err error
}

// Expand runs every marked shell block in parallel inside cwd and replaces it
// with the command's stdout (trailing whitespace trimmed). All markers are
// stripped from the result. log, when set, receives one line per expression.
func Expand(ctx context.Context, prompt string, exec Executor, cwd string, log func(string)) (string, error) {
	matches := markedShellBlockPattern.FindAllStringSubmatchIndex(prompt, -1)
	if len(matches) == 0 {
		return strings.ReplaceAll(prompt, ShellBlockMarker, ""), nil
	}

	results := make([]expansion, len(matches))
	done := make(chan struct{})
	for i, m := range matches {
		go func(i int, command string) {
			results[i] = runExpression(ctx, exec, cwd, command)
			done <- struct{}{}
		}(i, prompt[m[2]:m[3]])
	}
	for range matches {
		<-done
	}

	for i, r := range results {
		if r.err != nil {
			return "", r.err
		}
		if log != nil {
			command := prompt[matches[i][2]:matches[i][3]]
			log(fmt.Sprintf("%s -> ~%d tokens", command, (len(r.out)+3)/4))
		}
	}

	var b strings.Builder
	last := 0
	for i, m := range matches {
		b.WriteString(prompt[last:m[0]])
		b.WriteString(results[i].out)
		last = m[1]
	}
	b.WriteString(prompt[last:])
	return strings.ReplaceAll(b.String(), ShellBlockMarker, ""), nil
}

func runExpression(ctx context.Context, exec Executor, cwd, command string) expansion {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, expansionTimeout)
	defer cancel()
	res, err := exec.Exec(ctx, command, sandbox.ExecOptions{Cwd: cwd})
	if ctx.Err() == context.DeadlineExceeded {
		return expansion{err: &ExpansionTimeoutError{Expression: command, Timeout: expansionTimeout, Elapsed: time.Since(start)}}
	}
	if err != nil {
		return expansion{err: &Error{Message: fmt.Sprintf("command `%s` failed: %v", command, err)}}
	}
	if res.ExitCode != 0 {
		return expansion{err: &Error{Message: fmt.Sprintf("command `%s` exited with code %d: %s", command, res.ExitCode, res.Stderr), ExitCode: res.ExitCode}}
	}
	return expansion{out: strings.TrimRight(res.Stdout, " \t\r\n")}
}
