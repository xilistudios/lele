//go:build windows

package keepawake

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
)

// Windows execution state is PER THREAD, so the state must be set and cleared
// on one dedicated goroutine pinned with runtime.LockOSThread(): the
// assertion lives and dies with that thread, and therefore with the process —
// there is no orphan risk if lele dies without calling Stop.
var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")
)

// setThreadExecutionState wraps kernel32!SetThreadExecutionState. A zero
// return value means failure; the errno from Call's third return is used when
// non-zero, otherwise a generic error is produced.
func setThreadExecutionState(flags uint32) error {
	r, _, callErr := procSetThreadExecutionState.Call(uintptr(flags))
	if r == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return fmt.Errorf("SetThreadExecutionState(0x%08X): %w", flags, callErr)
		}
		return fmt.Errorf("SetThreadExecutionState(0x%08X) failed", flags)
	}
	return nil
}

// platformStart pins a dedicated goroutine, applies the continuous execution
// state (system + display required) and only returns once the state has been
// confirmed — so Start never reports success before the inhibition exists.
func platformStart() (func(), error) {
	ready := make(chan error, 1)
	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		err := setThreadExecutionState(esContinuous | esSystemRequired | esDisplayRequired)
		ready <- err
		if err != nil {
			close(done)
			return
		}
		<-stop
		// Reset to a plain continuous state, clearing both requirements.
		// The error is deliberately ignored: the thread (and therefore the
		// assertion) disappears with the process anyway, so a failed reset on
		// teardown must not surface or block anything.
		_ = setThreadExecutionState(esContinuous)
		close(done)
	}()

	if err := <-ready; err != nil {
		return nil, fmt.Errorf("keep-awake: SetThreadExecutionState not available: %w", err)
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
		})
	}, nil
}
