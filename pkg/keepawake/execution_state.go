package keepawake

// Windows execution-state flags for kernel32!SetThreadExecutionState
// (declared in winbase.h; see the Microsoft "SetThreadExecutionState"
// documentation). They live in this file — which has NO build tag — so a
// plain `go test` on Linux pins their exact values: a typo here would
// otherwise only surface on a Windows build.
//
// Usage: OR the flags with esContinuous to ACTIVATE the assertion
// (ES_CONTINUOUS | ES_SYSTEM_REQUIRED | ES_DISPLAY_REQUIRED), and pass
// esContinuous ALONE to RESET it (clearing both requirements while keeping
// the thread in a continuous state).
const (
	// esContinuous (ES_CONTINUOUS) marks the state as continuous rather than
	// one-shot; it must be present both when activating and when resetting.
	esContinuous = 0x80000000
	// esSystemRequired (ES_SYSTEM_REQUIRED) asks the system to stay
	// awake/away-from-sleep.
	esSystemRequired = 0x00000001
	// esDisplayRequired (ES_DISPLAY_REQUIRED) asks for the display to stay on
	// (also keeps the monitor on).
	esDisplayRequired = 0x00000002
)
