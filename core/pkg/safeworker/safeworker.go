package safeworker

import (
	"fmt"
	"runtime/debug"

	"github.com/alpkeskin/rota/core/pkg/logger"
)

// Call runs fn and logs a panic instead of crashing the hosting goroutine.
//
// The recover is registered before fn is called, in the same goroutine, so it
// fires for any panic raised by fn (including defers inside fn). On panic the
// worker name, formatted panic value, and goroutine stack are logged at error
// level; the panic is not re-raised, so the hosting goroutine keeps running.
func Call(log *logger.Logger, worker string, fn func()) {
	if fn == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Error("background worker panicked",
				"worker", worker,
				"error", fmt.Sprintf("%v", r),
				"stack", debug.Stack(),
			)
		}
	}()
	fn()
}
