package keepawake

import "testing"

// TestExecutionStateConstants pin the Windows SetThreadExecutionState flags to
// their documented ABI values (winbase.h). These constants live in
// execution_state.go, which has NO build tag, precisely so this test can run
// (and fail) on any platform — a typo would otherwise only surface on a
// Windows build. It also verifies that esContinuous carries the high bit
// (it is 0x80000000, not a plain int), which is what makes
// `esContinuous | esSystemRequired | esDisplayRequired` fit in uint32.
func TestExecutionStateConstants(t *testing.T) {
	cases := []struct {
		name string
		got  uint32
		want uint32
	}{
		{"esContinuous", esContinuous, 0x80000000},
		{"esSystemRequired", esSystemRequired, 0x00000001},
		{"esDisplayRequired", esDisplayRequired, 0x00000002},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = 0x%08X, want 0x%08X", c.name, c.got, c.want)
		}
	}
}
