// Package shutdown runs container teardowns when the process is interrupted.
package shutdown

import (
	"io"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
)

// ShowCursor is the DECTCEM escape that makes the cursor visible again.
const ShowCursor = "\x1b[?25h"

var (
	mu        sync.Mutex
	teardowns = map[int]func(){}
	nextID    int
	sigCh     chan os.Signal
	exit      = os.Exit
)

// Register adds a teardown that runs on SIGINT or SIGTERM, after which the
// process exits with code 1. The returned function removes it; calling it
// twice is safe.
func Register(teardown func()) func() {
	mu.Lock()
	defer mu.Unlock()
	id := nextID
	nextID++
	teardowns[id] = teardown
	if sigCh == nil {
		sigCh = make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func(ch chan os.Signal) {
			if _, ok := <-ch; !ok {
				return
			}
			runTeardowns()
			exit(1)
		}(sigCh)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			mu.Lock()
			defer mu.Unlock()
			delete(teardowns, id)
			if len(teardowns) == 0 && sigCh != nil {
				signal.Stop(sigCh)
				close(sigCh)
				sigCh = nil
			}
		})
	}
}

// runTeardowns calls every registered teardown once, in registration order.
// A panic in one teardown must not block the others.
func runTeardowns() {
	mu.Lock()
	ids := make([]int, 0, len(teardowns))
	for id := range teardowns {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	fns := make([]func(), 0, len(ids))
	for _, id := range ids {
		fns = append(fns, teardowns[id])
	}
	mu.Unlock()
	for _, fn := range fns {
		func() {
			defer func() { _ = recover() }()
			fn()
		}()
	}
}

// TerminalCleanup returns a teardown that shows the cursor again, for use
// after a spinner was interrupted.
func TerminalCleanup(stdout io.Writer) func() {
	return func() { _, _ = io.WriteString(stdout, ShowCursor) }
}
