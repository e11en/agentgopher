package initcmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Choice is one option in a Select prompt.
type Choice struct {
	Value string
	Label string
	Hint  string
}

// Prompter asks the user questions.
type Prompter interface {
	Select(message string, choices []Choice, initial string) (string, error)
	Confirm(message string, initial bool) (bool, error)
	Input(message, initial string) (string, error)
}

type stdinPrompter struct {
	in  *bufio.Scanner
	out io.Writer
}

// NewStdinPrompter prompts on out and reads one line per answer from in.
func NewStdinPrompter(in io.Reader, out io.Writer) Prompter {
	return &stdinPrompter{in: bufio.NewScanner(in), out: out}
}

func (p *stdinPrompter) readLine() (string, error) {
	if !p.in.Scan() {
		if err := p.in.Err(); err != nil {
			return "", err
		}
		return "", errors.New("input closed: stdin is not a terminal, pass --yes or the corresponding flags")
	}
	return strings.TrimSpace(p.in.Text()), nil
}

func (p *stdinPrompter) Select(message string, choices []Choice, initial string) (string, error) {
	_, _ = fmt.Fprintln(p.out, message)
	initialIdx := 1
	for i, c := range choices {
		line := fmt.Sprintf("  %d) %s", i+1, c.Label)
		if c.Hint != "" {
			line += " (" + c.Hint + ")"
		}
		_, _ = fmt.Fprintln(p.out, line)
		if c.Value == initial {
			initialIdx = i + 1
		}
	}
	for {
		_, _ = fmt.Fprintf(p.out, "Choice [%d]: ", initialIdx)
		line, err := p.readLine()
		if err != nil {
			return "", err
		}
		if line == "" {
			return choices[initialIdx-1].Value, nil
		}
		n, err := strconv.Atoi(line)
		if err == nil && n >= 1 && n <= len(choices) {
			return choices[n-1].Value, nil
		}
		for _, c := range choices {
			if c.Value == line {
				return c.Value, nil
			}
		}
		_, _ = fmt.Fprintln(p.out, "Please enter a number from the list.")
	}
}

func (p *stdinPrompter) Confirm(message string, initial bool) (bool, error) {
	hint := "Y/n"
	if !initial {
		hint = "y/N"
	}
	_, _ = fmt.Fprintf(p.out, "%s [%s] ", message, hint)
	line, err := p.readLine()
	if err != nil {
		return false, err
	}
	switch strings.ToLower(line) {
	case "":
		return initial, nil
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func (p *stdinPrompter) Input(message, initial string) (string, error) {
	_, _ = fmt.Fprintf(p.out, "%s [%s] ", message, initial)
	line, err := p.readLine()
	if err != nil {
		return "", err
	}
	if line == "" {
		return initial, nil
	}
	return line, nil
}
