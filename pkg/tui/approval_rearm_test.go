package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func runCmdForOutbound(cmd tea.Cmd, timeout time.Duration) *outboundMsg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		if om, ok := msg.(outboundMsg); ok {
			return &om
		}
		return nil
	}
	type res struct {
		om *outboundMsg
	}
	ch := make(chan res, len(batch))
	for _, c := range batch {
		if c == nil {
			continue
		}
		go func(c tea.Cmd) {
			m := c()
			if om, ok := m.(outboundMsg); ok {
				ch <- res{&om}
				return
			}
			ch <- res{nil}
		}(c)
	}
	deadline := time.After(timeout)
	for i := 0; i < len(batch); i++ {
		select {
		case r := <-ch:
			if r.om != nil {
				return r.om
			}
		case <-deadline:
			return nil
		}
	}
	return nil
}

func TestQueuedApprovalRearmsOutboundListener(t *testing.T) {
	m, _, b := setupApprovalModel(t)

	id1 := createAndPublish(t, m, b, "first dangerous", "r1")
	cmd := m.startOutboundListener()
	om, ok := cmd().(outboundMsg)
	if !ok {
		t.Fatal("listener did not return outboundMsg")
	}
	_, retCmd := m.Update(om)

	if stash := m.pendingApprovals[b]; stash.id != id1 {
		t.Fatalf("stash after first request = %+v, want id %q", stash, id1)
	}

	id2 := createAndPublish(t, m, b, "second dangerous", "r2")
	got := runCmdForOutbound(retCmd, 3*time.Second)
	if got == nil {
		t.Fatal("TUI went deaf after an approval.request: no listener was re-armed")
	}
	if got.msg.Event != "approval.request" || got.msg.Metadata["id"] != id2 {
		t.Fatalf("listener delivered %q id=%q, want approval.request id=%q",
			got.msg.Event, got.msg.Metadata["id"], id2)
	}

	m.Update(*got)
	if m.pendingApprovalID != "" {
		t.Fatalf("background approval leaked into visible state: %q", m.pendingApprovalID)
	}
	stash, ok := m.pendingApprovals[b]
	if !ok || stash.id != id2 || stash.cmd != "second dangerous" {
		t.Fatalf("stash after re-armed delivery = %+v (found=%v), want snapshot for %q", stash, ok, id2)
	}
}
