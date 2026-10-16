package stream

import (
	"strings"
	"sync"
	"time"
)

const (
	deltaLengthThreshold = 80
	deltaDebounce        = 50 * time.Millisecond
)

// TextDeltaBuffer buffers streaming text deltas and flushes them as readable
// multi-word chunks: on a newline, a sentence boundary, 80 characters, or
// after 50 ms without new writes.
type TextDeltaBuffer struct {
	mu      sync.Mutex
	buf     strings.Builder
	timer   *time.Timer
	onFlush func(string)
}

// NewTextDeltaBuffer returns a buffer that calls onFlush with each chunk.
func NewTextDeltaBuffer(onFlush func(string)) *TextDeltaBuffer {
	return &TextDeltaBuffer{onFlush: onFlush}
}

// Write appends text and flushes when a boundary is reached.
func (b *TextDeltaBuffer) Write(text string) {
	if text == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.WriteString(text)
	b.stopTimer()
	if b.shouldFlush() {
		b.flushLocked()
		return
	}
	b.timer = time.AfterFunc(deltaDebounce, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.flushLocked()
	})
}

// Flush force-flushes buffered text.
func (b *TextDeltaBuffer) Flush() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopTimer()
	b.flushLocked()
}

// Close flushes the remainder and stops the timer.
func (b *TextDeltaBuffer) Close() { b.Flush() }

func (b *TextDeltaBuffer) shouldFlush() bool {
	s := b.buf.String()
	if strings.Contains(s, "\n") || len(s) >= deltaLengthThreshold {
		return true
	}
	return strings.HasSuffix(s, ". ") || strings.HasSuffix(s, "! ") || strings.HasSuffix(s, "? ")
}

func (b *TextDeltaBuffer) flushLocked() {
	if b.buf.Len() == 0 {
		return
	}
	text := b.buf.String()
	b.buf.Reset()
	b.onFlush(text)
}

func (b *TextDeltaBuffer) stopTimer() {
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
}
