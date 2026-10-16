package stream

import (
	"sync"
	"testing"
	"time"
)

type flushRecorder struct {
	mu     sync.Mutex
	chunks []string
}

func (r *flushRecorder) record(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chunks = append(r.chunks, s)
}

func (r *flushRecorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.chunks...)
}

func TestTextDeltaBufferFlushesOnNewline(t *testing.T) {
	rec := &flushRecorder{}
	b := NewTextDeltaBuffer(rec.record)
	defer b.Close()
	b.Write("hello")
	b.Write(" world\n")
	if got := rec.get(); len(got) != 1 || got[0] != "hello world\n" {
		t.Fatalf("chunks = %q", got)
	}
}

func TestTextDeltaBufferFlushesOnSentenceBoundary(t *testing.T) {
	rec := &flushRecorder{}
	b := NewTextDeltaBuffer(rec.record)
	defer b.Close()
	b.Write("Done. ")
	if got := rec.get(); len(got) != 1 || got[0] != "Done. " {
		t.Fatalf("chunks = %q", got)
	}
}

func TestTextDeltaBufferFlushesAtLengthThreshold(t *testing.T) {
	rec := &flushRecorder{}
	b := NewTextDeltaBuffer(rec.record)
	defer b.Close()
	long := make([]byte, 80)
	for i := range long {
		long[i] = 'x'
	}
	b.Write(string(long))
	if got := rec.get(); len(got) != 1 {
		t.Fatalf("chunks = %q", got)
	}
}

func TestTextDeltaBufferFlushesAfterDebounce(t *testing.T) {
	rec := &flushRecorder{}
	b := NewTextDeltaBuffer(rec.record)
	defer b.Close()
	b.Write("partial")
	if got := rec.get(); len(got) != 0 {
		t.Fatalf("flushed too early: %q", got)
	}
	time.Sleep(120 * time.Millisecond)
	if got := rec.get(); len(got) != 1 || got[0] != "partial" {
		t.Fatalf("chunks = %q", got)
	}
}

func TestTextDeltaBufferCloseFlushesRemainder(t *testing.T) {
	rec := &flushRecorder{}
	b := NewTextDeltaBuffer(rec.record)
	b.Write("tail")
	b.Close()
	if got := rec.get(); len(got) != 1 || got[0] != "tail" {
		t.Fatalf("chunks = %q", got)
	}
}
