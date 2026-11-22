//go:build windows

package keepawake

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
)

var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")
)

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
