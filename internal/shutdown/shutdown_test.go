package shutdown

import (
	"bytes"
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

func TestRunTeardownsSurvivesPanicAndKeepsOrder(t *testing.T) {
	var order []string
	un1 := Register(func() { order = append(order, "a") })
	un2 := Register(func() { order = append(order, "b"); panic("must not block c") })
	un3 := Register(func() { order = append(order, "c") })
	defer un1()
	defer un2()
	defer un3()
	runTeardowns()
	if len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Fatalf("order = %v", order)
	}
}

func TestTerminalCleanupShowsCursor(t *testing.T) {
	var buf bytes.Buffer
	TerminalCleanup(&buf)()
	if buf.String() != ShowCursor {
		t.Fatalf("got %q", buf.String())
	}
}
