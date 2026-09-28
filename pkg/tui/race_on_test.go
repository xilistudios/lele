//go:build race

// The race-detector half of the raceEnabled switch. See race_off_test.go for
// the rationale (testing.AllocsPerRun is not reliable under -race).
package tui

const raceEnabled = true
