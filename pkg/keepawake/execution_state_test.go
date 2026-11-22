package keepawake

import "testing"

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
