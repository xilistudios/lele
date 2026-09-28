//go:build !race

// raceEnabled reports whether the test binary was built with the race detector
// (-race), as a COMPILE-TIME constant: exactly one of race_on_test.go /
// race_off_test.go is in the build, so `go test -race` and a plain `go test`
// never disagree about it.
//
// Why a test needs to know: testing.AllocsPerRun is not reliable under -race.
// The race runtime allocates for its own shadow state, so a call that allocates
// nothing can be reported as 1 alloc/op — deterministically enough to fail an
// "allocs == 0" assertion that is perfectly valid in a normal build. Tests that
// make such an assertion must keep their allocation-independent assertions
// unconditional (they are the real guard: no backend lookups, no listing
// rebuilt) and run the AllocsPerRun one only when !raceEnabled.
package tui

const raceEnabled = false
