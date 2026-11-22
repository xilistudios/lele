package session

import (
	"github.com/xilistudios/lele/pkg/providers"
)

type messageSnapshot struct {
	epoch uint64
	view  []providers.Message
}

func (s *Session) publishViewLocked() []providers.Message {
	if s == nil {
		return nil
	}
	view := cloneMessages(s.Messages)
	s.viewSnapshot.Store(&messageSnapshot{epoch: s.saveEpoch, view: view})
	return view
}

func cloneMessages(msgs []providers.Message) []providers.Message {
	out := make([]providers.Message, len(msgs))
	copy(out, msgs)
	return out
}
