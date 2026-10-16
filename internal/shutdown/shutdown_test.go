package shutdown

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestSignalRunsTeardownsOnceAndExits(t *testing.T) {
	exited := make(chan int, 1)
	exit = func(code int) { exited <- code }
	t.Cleanup(func() { exit = os.Exit })
	calls := 0
	un1 := Register(func() { calls++ })
	un2 := Register(func() { calls++ })
	defer un1()
	defer un2()
	syscall.Kill(os.Getpid(), syscall.SIGTERM)
	select {
	case code := <-exited:
		if code != 1 || calls != 2 {
			t.Fatalf("code=%d calls=%d", code, calls)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("exit not called")
	}
}

func TestUnregisterIsIdempotent(t *testing.T) {
	un := Register(func() {})
	un()
	un()
	mu.Lock()
	n := len(teardowns)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("teardowns = %d", n)
	}
}
