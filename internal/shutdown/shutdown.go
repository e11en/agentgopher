// Package shutdown runs container teardowns when the process is interrupted.
package shutdown

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

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
			mu.Lock()
			all := make([]func(), 0, len(teardowns))
			for _, f := range teardowns {
				all = append(all, f)
			}
			mu.Unlock()
			for _, f := range all {
				func() {
					defer func() { _ = recover() }()
					f()
				}()
			}
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
