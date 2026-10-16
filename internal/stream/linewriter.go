package stream

import "bytes"

// LineWriter splits a byte stream into lines and hands each complete line to
// onLine as soon as it arrives.
type LineWriter struct {
	onLine func(string)
	buf    []byte
}

// NewLineWriter returns a LineWriter that calls onLine per line, without the newline.
func NewLineWriter(onLine func(string)) *LineWriter { return &LineWriter{onLine: onLine} }

func (w *LineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		w.onLine(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
}

// Flush emits a trailing partial line, if any.
func (w *LineWriter) Flush() {
	if len(w.buf) > 0 {
		w.onLine(string(w.buf))
		w.buf = nil
	}
}
