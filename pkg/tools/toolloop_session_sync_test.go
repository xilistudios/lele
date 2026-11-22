package tools

import (
	"errors"
	"testing"
)

type fakeSessionCompactor struct {
	calls     int
	lastKey   string
	lastSum   string
	lastKeep  int
	lastEvict bool
	err       error
}

func (f *fakeSessionCompactor) CompactSession(key, summary string, keepCount int, evict bool) error {
	f.calls++
	f.lastKey = key
	f.lastSum = summary
	f.lastKeep = keepCount
	f.lastEvict = evict
	return f.err
}

func TestSyncCompactionToSession_CallsCompactor(t *testing.T) {
	sc := &fakeSessionCompactor{}
	cfg := ToolLoopConfig{
		SessionKey:              "origin:task-1",
		SessionCompactor:        sc,
		EvictExcludedFromMemory: true,
	}
	syncCompactionToSession(cfg, "[Context compacted — summary of previous 4 messages]\nstuff happened")
	if sc.calls != 1 {
		t.Fatalf("expected 1 CompactSession call, got %d", sc.calls)
	}
	if sc.lastKey != "origin:task-1" {
		t.Errorf("expected key 'origin:task-1', got %q", sc.lastKey)
	}
	if sc.lastKeep != 6 {
		t.Errorf("expected keepCount 6 (loop keepLast), got %d", sc.lastKeep)
	}
	if !sc.lastEvict {
		t.Error("expected evict=true to be forwarded")
	}
}

func TestSyncCompactionToSession_SkipsWhenNilOrEmpty(t *testing.T) {
	syncCompactionToSession(ToolLoopConfig{SessionKey: "k"}, "summary")
	sc := &fakeSessionCompactor{}
	syncCompactionToSession(ToolLoopConfig{SessionCompactor: sc}, "summary")
	syncCompactionToSession(ToolLoopConfig{SessionCompactor: sc, SessionKey: "k"}, "")
	if sc.calls != 0 {
		t.Errorf("expected 0 CompactSession calls, got %d", sc.calls)
	}
}

func TestSyncCompactionToSession_SwallowsErrors(t *testing.T) {
	sc := &fakeSessionCompactor{err: errors.New("disk full")}
	cfg := ToolLoopConfig{SessionKey: "k", SessionCompactor: sc}
	syncCompactionToSession(cfg, "summary")
	if sc.calls != 1 {
		t.Errorf("expected the call to be attempted, got %d", sc.calls)
	}
}
