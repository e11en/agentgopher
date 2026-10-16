package stream

import "testing"

func TestLineWriterSplitsAcrossWrites(t *testing.T) {
	var lines []string
	w := NewLineWriter(func(l string) { lines = append(lines, l) })
	w.Write([]byte("ab"))
	w.Write([]byte("c\nde\nf"))
	if len(lines) != 2 || lines[0] != "abc" || lines[1] != "de" {
		t.Fatalf("lines = %q", lines)
	}
	w.Flush()
	if len(lines) != 3 || lines[2] != "f" {
		t.Fatalf("after flush: %q", lines)
	}
	w.Flush()
	if len(lines) != 3 {
		t.Fatal("second flush must not emit")
	}
}
