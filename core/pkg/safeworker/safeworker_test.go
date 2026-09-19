package safeworker

import (
	"strings"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/pkg/logger"
)

// captured is a single captured log event.
type captured struct {
	level   string
	message string
	attrs   map[string]any
}

// newCaptureLogger creates a logger whose hook forwards every event to a
// buffered channel. The hook is invoked on the logger's worker-pool goroutine,
// so callers must read from the channel with a timeout — never assert directly.
func newCaptureLogger(t *testing.T) (*logger.Logger, <-chan captured) {
	t.Helper()
	l := logger.New("debug")
	ch := make(chan captured, 1)
	l.AddHook(func(level, message string, attrs map[string]any) {
		select {
		case ch <- captured{level: level, message: message, attrs: attrs}:
		default:
		}
	})
	return l, ch
}

// expectLog waits up to timeout for a log event on ch and returns it.
func expectLog(t *testing.T, ch <-chan captured, timeout time.Duration) captured {
	t.Helper()
	select {
	case c := <-ch:
		return c
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for log event")
		return captured{}
	}
}

func TestCallPanicLogged(t *testing.T) {
	l, ch := newCaptureLogger(t)

	Call(l, "test_worker", func() {
		panic("boom")
	})

	c := expectLog(t, ch, 2*time.Second)
	if c.level != "error" {
		t.Fatalf("level = %q, want %q", c.level, "error")
	}
	if c.message != "background worker panicked" {
		t.Fatalf("message = %q, want %q", c.message, "background worker panicked")
	}
	if c.attrs["worker"] != "test_worker" {
		t.Fatalf("worker attr = %v, want %q", c.attrs["worker"], "test_worker")
	}
	errStr, _ := c.attrs["error"].(string)
	if !strings.Contains(errStr, "boom") {
		t.Fatalf("error attr = %q, want to contain %q", errStr, "boom")
	}
	stack, ok := c.attrs["stack"].([]byte)
	if !ok || len(stack) == 0 {
		t.Fatalf("stack attr = %T, want non-empty []byte", c.attrs["stack"])
	}
}

func TestCallNonStringPanic(t *testing.T) {
	l, ch := newCaptureLogger(t)

	Call(l, "num_worker", func() {
		panic(42)
	})

	c := expectLog(t, ch, 2*time.Second)
	if c.level != "error" {
		t.Fatalf("level = %q, want %q", c.level, "error")
	}
	if c.attrs["error"] != "42" {
		t.Fatalf("error attr = %v, want %q", c.attrs["error"], "42")
	}
}

func TestCallRunsFn(t *testing.T) {
	l, _ := newCaptureLogger(t)
	ran := make(chan struct{})

	Call(l, "run_worker", func() {
		close(ran)
	})

	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatalf("fn did not run")
	}
}

func TestCallNilFn(t *testing.T) {
	l, ch := newCaptureLogger(t)

	Call(l, "nil_worker", nil) // must not panic

	select {
	case c := <-ch:
		t.Fatalf("unexpected log event: %+v", c)
	case <-time.After(200 * time.Millisecond):
	}
}
