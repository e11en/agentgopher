// Package stream holds helpers for agent output streams.
package stream

import "strings"

// MaxTailChars is the default budget of a BoundedTail: well above any
// completion signal or structured output payload, far below memory limits.
const MaxTailChars = 64 * 1024

// BoundedTail is a rolling tail of strings bounded by total character length.
type BoundedTail struct {
	items     []string
	total     int
	maxChars  int
	separator string
}

// NewBoundedTail returns a tail that joins items with separator and keeps
// String() at most maxChars long.
func NewBoundedTail(maxChars int, separator string) *BoundedTail {
	return &BoundedTail{maxChars: maxChars, separator: separator}
}

// Push appends item, evicting the oldest items to stay within budget. An item
// longer than the budget is truncated to its own tail.
func (b *BoundedTail) Push(item string) {
	if len(item) > b.maxChars {
		item = item[len(item)-b.maxChars:]
	}
	if len(b.items) > 0 {
		b.total += len(b.separator)
	}
	b.total += len(item)
	b.items = append(b.items, item)
	for b.total > b.maxChars && len(b.items) > 1 {
		b.total -= len(b.items[0]) + len(b.separator)
		b.items = b.items[1:]
	}
}

// String joins the retained items.
func (b *BoundedTail) String() string { return strings.Join(b.items, b.separator) }
