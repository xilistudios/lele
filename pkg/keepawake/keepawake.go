// Package keepawake keeps the host machine from sleeping while lele is
// running, using a real per-platform mechanism:
//
//   - Linux:   systemd-inhibit --what=idle:sleep wrapping a self-terminating
//     shell loop (requires systemd — works on openSUSE/Fedora/Debian/Arch
//     default installs; on WSL or non-systemd distros, where systemd-inhibit
//     is missing or cannot acquire the lock, Start returns a plain start
//     failure such as "keep-awake: systemd-inhibit not available" or
//     "keep-awake: systemd-inhibit exited immediately: …", NOT an
//     ErrUnsupported-wrapped error).
//   - macOS:   caffeinate -dims -w <pid>, which drops the assertion by
//     itself when the lele process exits.
//   - Windows: SetThreadExecutionState on a dedicated, OS-thread-locked
//     goroutine (the state is per thread and disappears with the process).
//
// Any other (unknown) platform is the only one that returns ErrUnsupported
// from Start.
//
// Known, deliberate limitation (applies to every platform): the inhibitor is
// not liveness-probed. If the mechanism dies AFTER a successful Start — e.g.
// logind restarting and dropping the inhibitor, or caffeinate being killed —
// Active() keeps reporting true until the next Stop/Start cycle, so a status
// row may show "active" while the inhibition no longer exists. Probing
// (`systemd-inhibit --list` per frame, IOKit queries) would be too expensive
// for a TUI refreshed every frame, and the designs are self-releasing (the
// child only exits on its own when logind restarts or the lele pid
// disappears), so the exposure is rare by construction. This is documented,
// not implemented, on purpose.
//
// The package is generic on purpose (a future `lele gateway` mode may reuse
// it) and therefore does not import pkg/config or pkg/tui.
package keepawake

import (
	"errors"
	"sync"
)

// ErrUnsupported is returned by Start when the current platform has no
// mechanism to inhibit sleep (i.e. no platform-specific implementation is
// compiled in).
var ErrUnsupported = errors.New("keep-awake is not supported on this platform")

// Inhibitor inhibits host sleep between Start and Stop. The zero value is
// usable as-is: if starter is nil, Start falls back to the platform starter
// (platformStart), and Active/Stop work on the zero value. All methods are
// safe for concurrent use.
type Inhibitor struct {
	mu      sync.Mutex
	starter func() (release func(), err error)
	release func()
}

// New returns an Inhibitor backed by the platform mechanism.
func New() *Inhibitor {
	return NewWithStarter(platformStart)
}

// NewWithStarter returns an Inhibitor that acquires sleep inhibition through
// starter. Intended for tests and for embedding the package with a custom
// mechanism.
func NewWithStarter(starter func() (func(), error)) *Inhibitor {
	return &Inhibitor{starter: starter}
}

// Start acquires the sleep inhibition. It is idempotent: if the inhibitor is
// already active it returns nil without starting a second mechanism. On
// failure the Inhibitor is left inactive and the starter's error is returned
// unchanged, so callers can surface it verbatim.
//
// A starter that reports success (err == nil) MUST hand back a release
// closure; (nil, nil) is a contract violation and Start rejects it with an
// explicit error instead of pretending success with Active() == false (the
// same class of bug as a mechanism dying after a successful Start). If a
// starter returns BOTH a release closure and an error, the closure is invoked
// before the error is returned, so the half-acquired resource is not leaked.
//
// The platform starters already prefix their messages with "keep-awake:" and
// wrap the underlying error with %w (so errors.Is/errors.As keep working);
// Start deliberately adds nothing of its own for REAL starter errors, which
// keeps the message free of duplicated prefixes and keeps a custom starter
// (NewWithStarter) in control of its own wording. The (nil, nil) violation
// error is generated here (there is no starter error to preserve). An
// unsupported platform returns ErrUnsupported unwrapped, so
// errors.Is(err, ErrUnsupported) is a valid check.
func (i *Inhibitor) Start() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.release != nil {
		return nil
	}
	starter := i.starter
	if starter == nil {
		starter = platformStart
	}
	release, err := starter()
	if err != nil {
		// The starter failed AND still handed back a closure: run it so the
		// half-acquired resource is not leaked, then surface the error.
		if release != nil {
			release()
		}
		return err
	}
	if release == nil {
		// Contract violation: "success" without a way to undo it. Reporting
		// nil here would leave Active() == false with a mechanism running.
		return errors.New("keep-awake: starter returned no release func")
	}
	i.release = release
	return nil
}

// Stop releases the sleep inhibition. It is idempotent: a no-op when not
// active. After Stop, Start may be called again to acquire a fresh
// inhibition. Stop is safe to call from a different goroutine than Start.
//
// The release happens OUTSIDE the Inhibitor lock, so a concurrent Start may
// acquire a second mechanism while the first one is still being torn down —
// benign, because the acquire/release counters still balance (verified by
// tests). Stop blocks until the release returns: normally ~1 ms (the release
// closure signals its process group and waits for cmd.Wait, which returns as
// soon as the group is dead); the 2 s SIGTERM grace + 1 s kill timeout bounds
// are only paid in the pathological case of a child that ignores SIGTERM.
func (i *Inhibitor) Stop() {
	i.mu.Lock()
	release := i.release
	i.release = nil
	i.mu.Unlock()
	if release != nil {
		release()
	}
}

// Active reports whether the inhibitor is currently held (true only between a
// successful Start and a Stop).
//
// Known, deliberate limitation: Active is bookkeeping, not a liveness probe.
// It does NOT detect a mechanism that dies after a successful Start (e.g.
// logind restarting and taking the inhibitor with it, or a caffeinate killed
// by an external signal): it keeps reporting true until the next Stop, even
// though the inhibition may no longer exist. Probing `systemd-inhibit --list`
// every frame would be far too expensive for a TUI status row, and the
// designs are "self-releasing" (the child only exits on its own if logind
// restarts or lele's pid disappears), so the window is rare by construction.
func (i *Inhibitor) Active() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.release != nil
}
