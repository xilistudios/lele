package channels

import (
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/logger"
)

func captureLogEntries(t *testing.T) func() []logger.LogEntry {
	t.Helper()

	var mu sync.Mutex
	entries := make([]logger.LogEntry, 0, 8)
	previousLevel := logger.GetLevel()
	logger.SetLevel(logger.INFO)
	restore := logger.SetHook(func(entry logger.LogEntry) {
		mu.Lock()
		entries = append(entries, entry)
		mu.Unlock()
	})
	t.Cleanup(func() {
		restore()
		logger.SetLevel(previousLevel)
	})

	return func() []logger.LogEntry {
		mu.Lock()
		defer mu.Unlock()
		return append([]logger.LogEntry(nil), entries...)
	}
}

func loggedMessages(entries []logger.LogEntry, substring string) []logger.LogEntry {
	matched := make([]logger.LogEntry, 0, len(entries))
	for _, entry := range entries {
		if strings.Contains(entry.Message, substring) {
			matched = append(matched, entry)
		}
	}
	return matched
}

func TestApprovalRequesterForTurn_Matrix(t *testing.T) {
	cases := []struct {
		name     string
		channel  string
		senderID string
		want     string
	}{
		{name: "telegram bare id", channel: "telegram", senderID: "111", want: "111"},
		{name: "telegram id with username", channel: "telegram", senderID: "111|alice", want: "111|alice"},
		{name: "telegram without a sender", channel: "telegram", senderID: "", want: ""},
		{name: "telegram with an automation sender", channel: "telegram", senderID: "cron", want: ""},
		{name: "telegram with a non numeric id", channel: "telegram", senderID: "alice|alice", want: ""},
		{name: "native client id", channel: ChannelName, senderID: "875f804ab60792a630f85d1d52493d8a", want: ""},
		{name: "native without a sender", channel: ChannelName, senderID: "", want: ""},
		{name: "unknown surface", channel: "whatsapp", senderID: "111", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ApprovalRequesterForTurn(tc.channel, tc.senderID); got != tc.want {
				t.Fatalf("ApprovalRequesterForTurn(%q, %q) = %q, want %q", tc.channel, tc.senderID, got, tc.want)
			}
		})
	}
}

func TestApprovalRequesterForTurn_WarnsOnlyWhenAUserIdentityIsMissing(t *testing.T) {
	entries := captureLogEntries(t)
	const warning = "falling back to session authority"

	ApprovalRequesterForTurn("telegram", "cron")
	ApprovalRequesterForTurn("telegram", "111")

	if got := len(loggedMessages(entries(), warning)); got != 1 {
		t.Fatalf("warnings for a telegram turn without a user identity = %d, want 1", got)
	}

	entries = captureLogEntries(t)
	ApprovalRequesterForTurn(ChannelName, "875f804ab60792a630f85d1d52493d8a")
	ApprovalRequesterForTurn(ChannelName, "")

	if got := len(loggedMessages(entries(), warning)); got != 0 {
		t.Fatalf("warnings for a surface without requester authority = %d, want 0", got)
	}
}
