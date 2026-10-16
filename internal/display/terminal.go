package display

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiInverse = "\x1b[7m"
	ansiRed     = "\x1b[31m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiBlue    = "\x1b[34m"
	hideCursor  = "\x1b[?25l"
	showCursor  = "\x1b[?25h"
)

var spinnerFrames = []string{"|", "/", "-", "\\"}

// IsTerminal reports whether f is a character device (an interactive terminal).
func IsTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

type terminal struct {
	mu       sync.Mutex
	w        io.Writer
	animate  bool
	interval time.Duration
}

// NewTerminal returns a Display that renders styled lines and, on a real
// terminal, an animated spinner for tasks.
func NewTerminal(w io.Writer) Display { return NewTerminalWithTicker(w, 80*time.Millisecond) }

// NewTerminalWithTicker is NewTerminal with a custom spinner interval.
func NewTerminalWithTicker(w io.Writer, interval time.Duration) Display {
	t := &terminal{w: w, interval: interval}
	if f, ok := w.(*os.File); ok {
		t.animate = IsTerminal(f)
	}
	return t
}

func (t *terminal) print(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = io.WriteString(t.w, s)
}

func (t *terminal) Intro(title string) { t.print(ansiInverse + " " + title + " " + ansiReset + "\n") }

func (t *terminal) Status(message string, sev Severity) {
	color := map[Severity]string{SeverityInfo: ansiBlue, SeveritySuccess: ansiGreen, SeverityWarn: ansiYellow, SeverityError: ansiRed}[sev]
	t.print(color + "*" + ansiReset + " " + ansiBold + message + ansiReset + "\n")
}

func (t *terminal) Summary(title string, rows [][2]string) {
	var b strings.Builder
	b.WriteString(ansiBold + title + ansiReset + "\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "  %s%s%s: %s%s%s\n", ansiBold, r[0], ansiReset, ansiDim, r[1], ansiReset)
	}
	t.print(b.String())
}

func (t *terminal) Task(title string, fn func(log func(string)) error) error {
	var logs []string
	var logMu sync.Mutex
	logFn := func(m string) {
		logMu.Lock()
		logs = append(logs, m)
		logMu.Unlock()
		if !t.animate {
			t.print("  " + m + "\n")
		}
	}
	if !t.animate {
		t.print(title + "...\n")
		err := fn(logFn)
		if err != nil {
			t.print(title + " (failed)\n")
			return err
		}
		t.print(title + " done\n")
		return nil
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t.print(hideCursor)
		i := 0
		ticker := time.NewTicker(t.interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				t.print("\r" + spinnerFrames[i%len(spinnerFrames)] + " " + title)
				i++
			}
		}
	}()
	err := fn(logFn)
	close(stop)
	<-done
	var b strings.Builder
	b.WriteString("\r\x1b[2K" + showCursor)
	if err != nil {
		b.WriteString(ansiRed + "x" + ansiReset + " " + title + " (failed)\n")
	} else {
		b.WriteString(ansiGreen + "+" + ansiReset + " " + title + " done\n")
	}
	logMu.Lock()
	for _, m := range logs {
		b.WriteString("  " + ansiDim + m + ansiReset + "\n")
	}
	logMu.Unlock()
	t.print(b.String())
	return err
}

func (t *terminal) Text(line string)       { t.print(line + "\n") }
func (t *terminal) TextChunk(chunk string) { t.print(chunk) }
func (t *terminal) ToolCall(name, args string) {
	t.print(ansiDim + name + "(" + args + ")" + ansiReset + "\n")
}
