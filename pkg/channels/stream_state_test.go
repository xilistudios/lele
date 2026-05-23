// Lele - Ultra-lightweight personal AI agent
// Copyright (c) 2026 Lele contributors
// License: MIT

package channels

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStreamStateManager_BasicFlow(t *testing.T) {
	dir := t.TempDir()
	mgr := NewStreamStateManager(dir)

	sessionKey := "test-session"
	messageID := "msg-123"

	// Start stream
	mgr.StartStream(sessionKey, messageID)

	// Append chunks
	mgr.AppendChunk(sessionKey, messageID, "Hello ")
	mgr.AppendChunk(sessionKey, messageID, "World")
	mgr.AppendReasoning(sessionKey, messageID, "thinking...")

	// Get state
	state := mgr.GetStream(sessionKey, messageID)
	if state == nil {
		t.Fatal("expected state to exist")
	}
	if state.Content != "Hello World" {
		t.Errorf("expected content 'Hello World', got '%s'", state.Content)
	}
	if state.ReasoningContent != "thinking..." {
		t.Errorf("expected reasoning 'thinking...', got '%s'", state.ReasoningContent)
	}
	if state.Done {
		t.Error("expected done to be false")
	}

	// Mark done
	mgr.MarkDone(sessionKey, messageID)
	state = mgr.GetStream(sessionKey, messageID)
	if !state.Done {
		t.Error("expected done to be true")
	}

	// List active streams (should be empty since it's done)
	streams := mgr.ListActiveStreams(sessionKey)
	if len(streams) != 1 {
		t.Errorf("expected 1 stream in list (done streams still listed), got %d", len(streams))
	}
}

func TestStreamStateManager_AutoCreate(t *testing.T) {
	dir := t.TempDir()
	mgr := NewStreamStateManager(dir)

	sessionKey := "auto-session"
	messageID := "auto-msg"

	// Append without starting first (auto-create)
	mgr.AppendChunk(sessionKey, messageID, "auto-chunk")

	state := mgr.GetStream(sessionKey, messageID)
	if state == nil {
		t.Fatal("expected auto-created state to exist")
	}
	if state.Content != "auto-chunk" {
		t.Errorf("expected 'auto-chunk', got '%s'", state.Content)
	}
}

func TestStreamStateManager_ListActiveStreams(t *testing.T) {
	dir := t.TempDir()
	mgr := NewStreamStateManager(dir)

	// Create 3 streams for same session
	mgr.StartStream("session-a", "msg-1")
	mgr.StartStream("session-a", "msg-2")
	mgr.StartStream("session-a", "msg-3")

	// Different session
	mgr.StartStream("session-b", "msg-4")

	streams := mgr.ListActiveStreams("session-a")
	if len(streams) != 3 {
		t.Errorf("expected 3 streams for session-a, got %d", len(streams))
	}

	streams = mgr.ListActiveStreams("session-b")
	if len(streams) != 1 {
		t.Errorf("expected 1 stream for session-b, got %d", len(streams))
	}

	streams = mgr.ListActiveStreams("nonexistent")
	if len(streams) != 0 {
		t.Errorf("expected 0 streams for nonexistent, got %d", len(streams))
	}
}

func TestStreamStateManager_MarkError(t *testing.T) {
	dir := t.TempDir()
	mgr := NewStreamStateManager(dir)

	mgr.StartStream("sess", "msg")
	mgr.MarkError("sess", "msg", "something went wrong")

	state := mgr.GetStream("sess", "msg")
	if state.Error != "something went wrong" {
		t.Errorf("expected error 'something went wrong', got '%s'", state.Error)
	}
	if !state.Done {
		t.Error("expected done to be true after error")
	}
}

func TestStreamStateManager_NoOpOnMissing(t *testing.T) {
	dir := t.TempDir()
	mgr := NewStreamStateManager(dir)

	// These should not panic
	mgr.AppendChunk("no-session", "no-msg", "chunk") // auto-creates now
	mgr.MarkDone("no-session", "no-msg-2")           // no-op
	mgr.MarkError("no-session", "no-msg-3", "err")   // no-op
	mgr.RemoveStream("no-session", "no-msg-4")       // no-op

	// Auto-created should exist
	state := mgr.GetStream("no-session", "no-msg")
	if state == nil {
		t.Error("auto-created stream should exist")
	}
}

func TestStreamStateManager_DiskPersistence(t *testing.T) {
	dir := t.TempDir()
	mgr := NewStreamStateManager(dir)

	mgr.StartStream("disk-session", "disk-msg")
	mgr.AppendChunk("disk-session", "disk-msg", "persisted data")
	mgr.MarkDone("disk-session", "disk-msg")

	// Verify file exists
	diskPath := filepath.Join(dir, "disk-session", "disk-msg.json")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		t.Error("expected disk file to exist")
	}

	// Create a new manager (simulating restart) and verify recovery
	mgr2 := NewStreamStateManager(dir)
	state := mgr2.GetStream("disk-session", "disk-msg")
	if state == nil {
		t.Fatal("expected state to survive restart")
	}
	if state.Content != "persisted data" {
		t.Errorf("expected 'persisted data', got '%s'", state.Content)
	}
	if !state.Done {
		t.Error("expected done to be true after recovery")
	}
}

func TestStreamStateManager_Cleanup(t *testing.T) {
	dir := t.TempDir()
	mgr := NewStreamStateManager(dir)

	// Create a stream that will be immediately "old"
	mgr.StartStream("old-session", "old-msg")
	// Manually set last chunk time to be very old
	mgr.mu.Lock()
	key := stateKey("old-session", "old-msg")
	if state, ok := mgr.states[key]; ok {
		state.LastChunkAt = time.Now().Add(-20 * time.Minute).UnixMilli()
		state.Done = true
	}
	mgr.mu.Unlock()

	// Run cleanup
	mgr.cleanup()

	state := mgr.GetStream("old-session", "old-msg")
	if state != nil {
		t.Error("expected old stream to be cleaned up")
	}
}

func TestStreamStateManager_RemoveStream(t *testing.T) {
	dir := t.TempDir()
	mgr := NewStreamStateManager(dir)

	mgr.StartStream("rm-session", "rm-msg")
	mgr.AppendChunk("rm-session", "rm-msg", "data")

	// Verify it exists
	if mgr.GetStream("rm-session", "rm-msg") == nil {
		t.Fatal("expected stream to exist")
	}

	// Remove it
	mgr.RemoveStream("rm-session", "rm-msg")

	// Verify it's gone
	if mgr.GetStream("rm-session", "rm-msg") != nil {
		t.Error("expected stream to be removed")
	}

	// Verify disk file is gone
	diskPath := filepath.Join(dir, "rm-session", "rm-msg.json")
	if _, err := os.Stat(diskPath); !os.IsNotExist(err) {
		t.Error("expected disk file to be removed")
	}
}

func TestStreamStateManager_WasStreamed(t *testing.T) {
	dir := t.TempDir()
	mgr := NewStreamStateManager(dir)

	// Empty stream (started but no content) → not streamed
	mgr.StartStream("sess", "empty-msg")
	if mgr.WasStreamed("sess", "empty-msg") {
		t.Error("empty stream should not be considered streamed")
	}

	// Stream with content → streamed
	mgr.AppendChunk("sess", "content-msg", "hello")
	if !mgr.WasStreamed("sess", "content-msg") {
		t.Error("stream with content should be considered streamed")
	}

	// Stream with only reasoning → streamed
	mgr.AppendReasoning("sess", "reasoning-msg", "thinking...")
	if !mgr.WasStreamed("sess", "reasoning-msg") {
		t.Error("stream with reasoning should be considered streamed")
	}

	// Non-existent stream → not streamed
	if mgr.WasStreamed("sess", "nonexistent") {
		t.Error("non-existent stream should not be considered streamed")
	}
}

func TestStreamStateManager_WasStreamedPrefix(t *testing.T) {
	dir := t.TempDir()
	mgr := NewStreamStateManager(dir)

	sessionKey := "prefix-session"
	baseMsgID := "msg-456"

	// Base message — no content yet
	if mgr.WasStreamedPrefix(sessionKey, baseMsgID) {
		t.Error("no streams exist yet, should return false")
	}

	// Stream iteration 1 (exact match)
	mgr.AppendChunk(sessionKey, baseMsgID, "iteration 1 content")
	if !mgr.WasStreamedPrefix(sessionKey, baseMsgID) {
		t.Error("exact match with content should return true")
	}

	// Stream iteration 2 (prefixed: msg-456-2)
	iter2ID := baseMsgID + "-2"
	mgr.AppendChunk(sessionKey, iter2ID, "iteration 2 content")

	// WasStreamedPrefix for baseMsgID should find iteration 2 via prefix
	if !mgr.WasStreamedPrefix(sessionKey, baseMsgID) {
		t.Error("prefix match for iteration 2 should return true")
	}

	// But WasStreamed (exact) for iteration 2 should also work
	if !mgr.WasStreamed(sessionKey, iter2ID) {
		t.Error("exact match for iteration 2 should return true")
	}

	// Different base message — no streams
	if mgr.WasStreamedPrefix(sessionKey, "other-msg") {
		t.Error("unrelated message should not match")
	}

	// Prefix with only reasoning content
	mgr.AppendReasoning(sessionKey, baseMsgID+"-3", "thinking iter 3")
	if !mgr.WasStreamedPrefix(sessionKey, baseMsgID) {
		t.Error("prefix match with reasoning content should return true")
	}
}

func TestStreamStateManager_WasStreamedPrefix_DiskPersistence(t *testing.T) {
	dir := t.TempDir()
	mgr := NewStreamStateManager(dir)

	sessionKey := "disk-prefix"
	baseMsgID := "msg-789"

	// Create iteration streams on disk
	mgr.AppendChunk(sessionKey, baseMsgID+"-2", "disk iteration 2")

	// Simulate a new manager (restart) — iteration stream should be on disk
	mgr2 := NewStreamStateManager(dir)
	if !mgr2.WasStreamedPrefix(sessionKey, baseMsgID) {
		t.Error("prefix match should survive disk roundtrip")
	}

	// Exact match on disk
	if !mgr2.WasStreamed(sessionKey, baseMsgID+"-2") {
		t.Error("exact match should survive disk roundtrip")
	}
}
