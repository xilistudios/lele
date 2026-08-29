package agent

import "testing"

func TestClassifySessionKind(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"", "chat"},
		{"heartbeat", "heartbeat"},
		{"cron-spawn-abc123", "cron-spawn"},
		{"native:cron-dbcb45b8f62b875d:subagent-16", "cron-spawn"},
		{"native:cron-1d2f112fda883073:subagent-5", "cron-spawn"},
		{"cron-8e957c0c468bc927", "cron"},
		{"native:5f0c2e1a-1234-5678-9abc-def012345678:subagent-1", "subagent"},
		{"subagent:subagent-1", "subagent"},
		{"native:some-chat", "chat"},
	}
	for _, c := range cases {
		if got := classifySessionKind(c.key); got != c.want {
			t.Errorf("classifySessionKind(%q) = %q, want %q", c.key, got, c.want)
		}
	}
}
