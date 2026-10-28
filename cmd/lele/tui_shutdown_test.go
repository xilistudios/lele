package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeCloser records that the TUI model was closed.
type fakeCloser struct {
	mu     sync.Mutex
	closed int
	onCall func()
}

func (f *fakeCloser) Close() {
	f.mu.Lock()
	f.closed++
	cb := f.onCall
	f.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// fakeStopper records the grace and the state of the root context when the
// join ran, which is what the exit latency depends on.
type fakeStopper struct {
	mu     sync.Mutex
	calls  int
	graces []time.Duration
	ctxErr []error
	ctx    context.Context
	err    error
}

func (f *fakeStopper) StopWithin(grace time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.graces = append(f.graces, grace)
	if f.ctx != nil {
		f.ctxErr = append(f.ctxErr, f.ctx.Err())
	} else {
		f.ctxErr = append(f.ctxErr, nil)
	}
	return f.err
}

// TestShutdownTUI_CancelsRootBeforeJoining pins the ordering the exit latency
// depends on: the root context must be cancelled BEFORE the join, because that
// cancellation is what aborts an in-flight provider stream. Joining first waits
// out the whole turn (the pre-fix behaviour: 11.5 s to exit during a 15 s
// streamed response).
func TestShutdownTUI_CancelsRootBeforeJoining(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	model := &fakeCloser{onCall: func() {
		if ctx.Err() != nil {
			t.Error("Close cancelled the root context; ordering is not what this test measures")
		}
	}}
	loop := &fakeStopper{ctx: ctx}

	shutdownTUI(model, cancel, loop)

	if model.closed != 1 {
		t.Errorf("model.Close() calls = %d, want 1", model.closed)
	}
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("root context error after shutdown = %v, want context.Canceled", err)
	}
	if loop.calls != 1 {
		t.Fatalf("StopWithin() calls = %d, want 1", loop.calls)
	}
	if !errors.Is(loop.ctxErr[0], context.Canceled) {
		t.Error("StopWithin ran before the root context was cancelled; an in-flight turn would not be aborted and the join would wait out the whole response")
	}
	if loop.graces[0] != tuiExitJoinGrace {
		t.Errorf("StopWithin grace = %v, want %v", loop.graces[0], tuiExitJoinGrace)
	}
}

// TestShutdownTUI_AbandonedJoinIsLoggedNotFatal verifies an abandoned teardown
// (a turn that ignores cancellation) does not panic or abort the exit path: the
// process must still return the terminal to the user.
func TestShutdownTUI_AbandonedJoinIsLoggedNotFatal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loop := &fakeStopper{ctx: ctx, err: errors.New("agent loop stop timed out")}

	shutdownTUI(&fakeCloser{}, cancel, loop)

	if loop.calls != 1 {
		t.Errorf("StopWithin() calls = %d, want 1", loop.calls)
	}
}

// TestShutdownTUI_Idempotent pins that the deferred safety net can run after the
// explicit call on the exit path without side effects.
func TestShutdownTUI_Idempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	model := &fakeCloser{}
	loop := &fakeStopper{ctx: ctx}

	shutdownTUI(model, cancel, loop)
	shutdownTUI(model, cancel, loop)
	shutdownTUI(model, cancel, loop)

	if model.closed != 3 {
		t.Errorf("model.Close() calls = %d, want 3 (Close itself is idempotent)", model.closed)
	}
	for i, err := range loop.ctxErr {
		if !errors.Is(err, context.Canceled) {
			t.Errorf("StopWithin call %d saw ctx error %v, want a cancelled root context", i, err)
		}
	}
}

// TestShutdownTUI_NilTargetsDoNotPanic guards the early-return paths (a loop
// that failed to initialise) against a nil dereference during exit.
func TestShutdownTUI_NilTargetsDoNotPanic(t *testing.T) {
	shutdownTUI(nil, nil, nil)
}
