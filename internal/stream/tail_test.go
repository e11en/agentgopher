package stream

import "testing"

func TestBoundedTailKeepsEverythingUnderBudget(t *testing.T) {
	tail := NewBoundedTail(100, "\n")
	tail.Push("a")
	tail.Push("b")
	if got := tail.String(); got != "a\nb" {
		t.Fatalf("String() = %q", got)
	}
}

func TestBoundedTailDropsOldestItems(t *testing.T) {
	tail := NewBoundedTail(5, "\n")
	tail.Push("aa")
	tail.Push("bb")
	tail.Push("cc")
	if got := tail.String(); got != "bb\ncc" {
		t.Fatalf("String() = %q, want %q", got, "bb\ncc")
	}
}

func TestBoundedTailTruncatesOversizedItem(t *testing.T) {
	tail := NewBoundedTail(3, "")
	tail.Push("abcdef")
	if got := tail.String(); got != "def" {
		t.Fatalf("String() = %q, want %q", got, "def")
	}
}

func TestBoundedTailNeverExceedsMax(t *testing.T) {
	tail := NewBoundedTail(10, "\n")
	for i := 0; i < 50; i++ {
		tail.Push("xxxx")
	}
	if l := len(tail.String()); l > 10 {
		t.Fatalf("len = %d, want <= 10", l)
	}
}
