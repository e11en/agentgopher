// Package display renders run progress to a log file or the terminal.
package display

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Severity classifies a status line.
type Severity string

const (
	SeverityInfo    Severity = "info"
	SeveritySuccess Severity = "success"
	SeverityWarn    Severity = "warn"
	SeverityError   Severity = "error"
)

// Display receives progress from the orchestrator.
type Display interface {
	Intro(title string)
	Status(message string, severity Severity)
	Summary(title string, rows [][2]string)
	Task(title string, fn func(log func(string)) error) error
	Text(line string)
	TextChunk(chunk string)
	ToolCall(name, args string)
}

var labelPrefix = regexp.MustCompile(`^\[[^\]]+\] `)

type writer struct {
	mu      sync.Mutex
	w       io.Writer
	midLine bool
}

// NewWriter returns a Display that appends plain lines to w.
func NewWriter(w io.Writer) Display { return &writer{w: w} }

func (d *writer) line(s string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.midLine {
		_, _ = io.WriteString(d.w, "\n")
		d.midLine = false
	}
	_, _ = io.WriteString(d.w, s+"\n")
}

func (d *writer) Intro(string) {}

func (d *writer) Status(message string, _ Severity) {
	d.line(labelPrefix.ReplaceAllString(message, ""))
}

func (d *writer) Summary(title string, rows [][2]string) {
	var b strings.Builder
	b.WriteString(title)
	for _, r := range rows {
		b.WriteString("\n  " + r[0] + ": " + r[1])
	}
	d.line(b.String())
}

func (d *writer) Task(title string, fn func(log func(string)) error) error {
	d.line(title + "...")
	start := time.Now()
	err := fn(func(m string) { d.line("  " + m) })
	if err != nil {
		return err
	}
	d.line(fmt.Sprintf("%s done (%.1fs)", title, time.Since(start).Seconds()))
	return nil
}

func (d *writer) Text(line string) { d.line(line) }

func (d *writer) TextChunk(chunk string) {
	if chunk == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, _ = io.WriteString(d.w, chunk)
	d.midLine = !strings.HasSuffix(chunk, "\n")
}

func (d *writer) ToolCall(name, args string) { d.line("[" + name + "] " + args) }

// OpenLogFile opens path for appending, creating parent directories, and
// writes a run delimiter.
func OpenLogFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(f, "\n--- Run started: %s ---\n", time.Now().UTC().Format(time.RFC3339)); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
