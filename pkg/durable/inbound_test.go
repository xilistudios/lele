// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package durable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/store"
)

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

// spoolFixture is a throwaway database plus the handles tests use.
type spoolFixture struct {
	s      *store.Store
	repo   *store.SpoolRepo
	path   string
	closed bool
}

// openSpool returns a fixture backed by a throwaway database.
func openSpool(t *testing.T) *spoolFixture {
	t.Helper()

	path := filepath.Join(t.TempDir(), "durable.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open(%q) failed: %v", path, err)
	}
	f := &spoolFixture{s: s, repo: s.Spool(), path: path}
	t.Cleanup(func() {
		// Tests that deliberately break the database close it themselves to
		// exercise failure paths; a second Close would just be noise.
		if f.closed {
			return
		}
		if err := f.s.Close(); err != nil {
			t.Errorf("store.Close() failed: %v", err)
		}
	})
	return f
}

// closeDB kills the database so the next write fails.
func (f *spoolFixture) closeDB(t *testing.T) {
	t.Helper()

	f.closed = true
	if err := f.s.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
}

// reopen opens the same file again, as a restart would.
func (f *spoolFixture) reopen(t *testing.T) *store.Store {
	t.Helper()

	s, err := store.Open(f.path)
	if err != nil {
		t.Fatalf("reopen %q failed: %v", f.path, err)
	}
	t.Cleanup(func() { s.Close() }) //nolint:errcheck // may already be closed
	return s
}

// fakeBus is a stand-in for MessageBus.PublishInbound: a non-blocking send into
// a buffered channel, so a "full bus" is a test that simply does not read.
//
// rejects optionally scripts the first N publish outcomes (false = bus full);
// once exhausted, publishing succeeds.
type fakeBus struct {
	mu      sync.Mutex
	ch      chan bus.InboundMessage
	rejects []bool
	calls   int
}

func newFakeBus(buf int) *fakeBus {
	return &fakeBus{ch: make(chan bus.InboundMessage, buf)}
}

func (f *fakeBus) publish(msg bus.InboundMessage) bool {
	f.mu.Lock()
	var reject bool
	if f.calls < len(f.rejects) {
		reject = !f.rejects[f.calls]
	}
	f.calls++
	f.mu.Unlock()
	if reject {
		return false
	}

	select {
	case f.ch <- msg:
		return true
	default:
		return false
	}
}

// published drains everything currently buffered without blocking.
func (f *fakeBus) published() []bus.InboundMessage {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []bus.InboundMessage
	for {
		select {
		case msg := <-f.ch:
			out = append(out, msg)
		default:
			return out
		}
	}
}

// waitPublished polls until n messages are available or the deadline passes.
// Polling keeps the timing-sensitive tests (the pump) from being flaky.
func (f *fakeBus) waitPublished(t *testing.T, n int, timeout time.Duration) []bus.InboundMessage {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		got := f.published()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d messages published within %s", len(got), n, timeout)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *fakeBus) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// msg builds a minimal inbound message with the fields the spool path cares about.
func msg(content, dedupe string) bus.InboundMessage {
	m := bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "user-1",
		ChatID:     "chat-1",
		Content:    content,
		SessionKey: "telegram:chat-1",
	}
	if dedupe != "" {
		m.Metadata = map[string]string{"message_id": dedupe}
	}
	return m
}

// statsOf reads spool depth, failing the test on error.
func statsOf(t *testing.T, repo *store.SpoolRepo) store.SpoolStats {
	t.Helper()

	st, err := repo.Stats()
	if err != nil {
		t.Fatalf("Stats() failed: %v", err)
	}
	return st
}

// rawEnqueue writes a spool row directly, bypassing Enqueue, so tests
// can stage payloads the normal path would never produce (corrupt JSON, foreign
// claims, legacy rows without a dedupe id).
func rawEnqueue(t *testing.T, repo *store.SpoolRepo, channel, msgID, payload string) int64 {
	t.Helper()

	id, err := repo.Enqueue(store.SpoolInbound, channel, "chat-1", "telegram:chat-1", msgID, payload)
	if err != nil {
		t.Fatalf("Enqueue(%q, %q) failed: %v", channel, msgID, err)
	}
	return id
}

// payloadFor is the JSON shape Enqueue writes for msg.
func payloadFor(t *testing.T, m bus.InboundMessage) string {
	t.Helper()

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return string(data)
}

// peekRow reads one spool row straight from the table. ClaimBatch cannot be
// used for that any more on the live path: Enqueue claims the row it writes, so
// a test that wants to look at an in-flight row has to go around the claim
// protocol.
func peekRow(t *testing.T, f *spoolFixture, id int64) store.SpoolItem {
	t.Helper()

	rows, err := f.s.DB().Query(
		`SELECT id, direction, channel, chat_id, session_key, msg_id, payload, attempts, created_at
		 FROM spool WHERE id = ?`, id,
	)
	if err != nil {
		t.Fatalf("read spool row %d failed: %v", id, err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatalf("spool row %d does not exist", id)
	}
	var item store.SpoolItem
	var createdAt string
	if err := rows.Scan(
		&item.ID, &item.Direction, &item.Channel, &item.ChatID,
		&item.SessionKey, &item.MsgID, &item.Payload, &item.Attempts, &createdAt,
	); err != nil {
		t.Fatalf("scan spool row %d failed: %v", id, err)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read spool row %d: %v", id, err)
	}
	return item
}

// claimedBy returns the distinct non-empty claimed_by values in the spool
// table, so a test can assert who holds a row without going through the claim
// protocol (which cannot see claimed rows at all).
func claimedBy(t *testing.T, s *store.Store) []string {
	t.Helper()

	rows, err := s.DB().Query(`SELECT DISTINCT claimed_by FROM spool WHERE claimed_by <> ''`)
	if err != nil {
		t.Fatalf("read claimed_by failed: %v", err)
	}
	defer rows.Close()

	var owners []string
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			t.Fatalf("scan claimed_by failed: %v", err)
		}
		owners = append(owners, owner)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read claimed_by: %v", err)
	}
	return owners
}

// ──────────────────────────────────────────────────────────────────────────────
// Durability off (nil repo): Enqueue declines and the caller publishes anyway
// ──────────────────────────────────────────────────────────────────────────────

func TestNilRepoDisablesEnqueue(t *testing.T) {
	b := newFakeBus(8)
	d := NewInbound(nil, b.publish)

	got := msg("hello", "m-1")
	// Durability off is not an error, it is a plain "not persisted": the
	// message keeps no spool identity, and the caller's own publish path is the
	// only thing that moves it.
	if d.Enqueue(&got) {
		t.Error("Enqueue() = true with no repo, want false")
	}
	if got.SpoolID != 0 {
		t.Errorf("SpoolID = %d, want 0 with no repo", got.SpoolID)
	}
	if got.DedupeID != "" {
		t.Errorf("DedupeID = %q, want it untouched: a declined Enqueue writes nothing", got.DedupeID)
	}

	// The message still flows: the channel publishes it itself.
	if !b.publish(got) {
		t.Fatal("publish() = false, want the unpersisted message to reach the bus")
	}
	pub := b.waitPublished(t, 1, time.Second)
	if pub[0].Content != "hello" {
		t.Errorf("published content = %q, want %q", pub[0].Content, "hello")
	}

	// Every other method must be a safe no-op rather than a nil-pointer panic.
	if d.ShouldSkip(got) {
		t.Error("ShouldSkip() = true with no repo, want false")
	}
	d.Finish(got)

	n, err := d.Drain(context.Background())
	if n != 0 || err != nil {
		t.Errorf("Drain() = (%d, %v), want (0, nil) with no repo", n, err)
	}
	if released, err := d.ReleaseClaims(); released != 0 || err != nil {
		t.Errorf("ReleaseClaims() = (%d, %v), want (0, nil) with no repo", released, err)
	}
	if st, err := d.Stats(); err != nil || st != (store.SpoolStats{}) {
		t.Errorf("Stats() = (%+v, %v), want zero value with no repo", st, err)
	}

	// StartPump must return immediately when cancelled, not spin.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.StartPump(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("StartPump() did not return after context cancellation with no repo")
	}
}

// A nil service is a legal "durability off" wiring: the channels hold it
// behind an interface, so Enqueue must decline instead of panicking.
func TestNilServiceEnqueueIsSafe(t *testing.T) {
	var d *Inbound

	got := msg("hello", "m-1")
	if d.Enqueue(&got) {
		t.Error("(*Inbound)(nil).Enqueue() = true, want false")
	}
	if got.SpoolID != 0 || got.DedupeID != "" {
		t.Errorf("msg = %+v, want it untouched by a nil service", got)
	}
	if d.Enqueue(nil) {
		t.Error("Enqueue(nil) = true, want false")
	}
}

func TestNilPublisherNeverPanics(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	d := NewInbound(repo, nil)

	got := msg("hello", "m-1")
	if !d.Enqueue(&got) {
		t.Fatal("Enqueue() = false, want the row written even with no publisher wired")
	}
	if got.SpoolID == 0 {
		t.Error("SpoolID = 0, want a row even when the publisher is missing")
	}
	// Enqueue never touches the bus, so a missing publisher cannot lose the
	// message: the row is the only copy left, and it must survive.
	if st := statsOf(t, repo); st.PendingInbound+st.ClaimedInbound != 1 {
		t.Errorf("spool holds %d inbound rows, want 1", st.PendingInbound+st.ClaimedInbound)
	}
	// It is claimed by this instance (the live in-flight protocol), not pending.
	if st := statsOf(t, repo); st.PendingInbound != 0 || st.ClaimedInbound != 1 {
		t.Errorf("stats = %+v, want the row claimed by this instance, not pending", st)
	}

	// The live row is claimed by this instance, so a replay pass must not see
	// it at all: nothing panics and nothing is republished.
	if n, err := d.Drain(context.Background()); n != 0 || err != nil {
		t.Errorf("Drain() = (%d, %v), want (0, nil): a live in-flight claim is invisible to a pass", n, err)
	}

	// The replay path is where a missing publisher is actually exercised: once
	// the row is handed back (what a channel does when the bus refuses the
	// publish), a pass must degrade to a deferral rather than a panic.
	if !d.Release(&got) {
		t.Fatal("Release() = false, want the row handed back to the pending set")
	}
	if _, err := d.Drain(context.Background()); err != nil {
		t.Errorf("Drain() with no publisher failed: %v", err)
	}
	if st := statsOf(t, repo); st.PendingInbound != 1 {
		t.Errorf("PendingInbound = %d after Drain, want 1 (row handed back)", st.PendingInbound)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Enqueue
// ──────────────────────────────────────────────────────────────────────────────

func TestEnqueueWritesRowAndFillsIDs(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	b := newFakeBus(8)
	d := NewInbound(repo, b.publish)

	got := msg("hello", "m-1")
	if !d.Enqueue(&got) {
		t.Fatal("Enqueue() = false, want the message persisted")
	}

	if got.SpoolID == 0 {
		t.Fatal("SpoolID = 0, want the id of the spool row")
	}
	if got.DedupeID != "m-1" {
		t.Errorf("DedupeID = %q, want the channel message id %q", got.DedupeID, "m-1")
	}

	// Enqueue is write-only: it must never publish.
	if b.callCount() != 0 {
		t.Errorf("publish called %d times, want 0 (Enqueue must not publish)", b.callCount())
	}

	// Enqueue claims its row for this instance, so no pass can see it: the
	// assertions read the row straight from the table.
	if st := statsOf(t, repo); st.PendingInbound != 0 {
		t.Fatalf("PendingInbound = %d, want 0: a live row must not be claimable", st.PendingInbound)
	}
	if items, err := repo.ClaimBatch(store.SpoolInbound, 10, "peek", time.Now()); err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	} else if len(items) != 0 {
		t.Fatalf("claimed %d rows, want 0: a live in-flight row is invisible to a pass", len(items))
	}
	row := peekRow(t, f, got.SpoolID)
	if row.MsgID != "m-1" {
		t.Errorf("row msg_id = %q, want %q", row.MsgID, "m-1")
	}
	if row.Channel != "telegram" || row.SessionKey != "telegram:chat-1" {
		t.Errorf("row = %+v, want telegram/telegram:chat-1", row)
	}

	// SpoolID is json:"-", so the payload must not bake in a row id that would
	// be stale after a replay.
	var decoded bus.InboundMessage
	if err := json.Unmarshal([]byte(row.Payload), &decoded); err != nil {
		t.Fatalf("payload is not a valid message: %v", err)
	}
	if decoded.SpoolID != 0 {
		t.Errorf("payload SpoolID = %d, want 0 (must not be serialised)", decoded.SpoolID)
	}
	if decoded.DedupeID != "m-1" {
		t.Errorf("payload DedupeID = %q, want %q", decoded.DedupeID, "m-1")
	}
	if strings.Contains(row.Payload, "spool_id") {
		t.Errorf("payload leaks a spool id: %s", row.Payload)
	}

	// The identity the caller publishes is the identity the consumer must echo
	// back to Finish, so the spooled and the published message agree.
	if !b.publish(got) {
		t.Fatal("publish() = false, want the spooled message published")
	}
	pub := b.waitPublished(t, 1, time.Second)
	if pub[0].SpoolID != got.SpoolID || pub[0].DedupeID != got.DedupeID {
		t.Errorf("published identity = (%d, %q), want (%d, %q)",
			pub[0].SpoolID, pub[0].DedupeID, got.SpoolID, got.DedupeID)
	}
}

func TestEnqueueIsIdempotent(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	d := NewInbound(repo, (&fakeBus{ch: make(chan bus.InboundMessage, 8)}).publish)

	first := msg("hello", "m-1")
	if !d.Enqueue(&first) {
		t.Fatal("first Enqueue() = false, want the message persisted")
	}

	second := first
	if d.Enqueue(&second) {
		t.Error("second Enqueue() = true, want false for an already-backed message")
	}
	if second.SpoolID != first.SpoolID {
		t.Errorf("second SpoolID = %d, want the original %d", second.SpoolID, first.SpoolID)
	}
	if st := statsOf(t, repo); st.PendingInbound+st.ClaimedInbound != 1 {
		t.Errorf("spool holds %d inbound rows, want 1 (no double-spool)",
			st.PendingInbound+st.ClaimedInbound)
	}
}

func TestEnqueueSynthesizesDedupeIDWithoutMessageID(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	d := NewInbound(repo, (&fakeBus{ch: make(chan bus.InboundMessage, 8)}).publish)

	got := msg("hello", "")
	if !d.Enqueue(&got) {
		t.Fatal("Enqueue() = false, want the message persisted")
	}
	if got.DedupeID == "" {
		t.Error("DedupeID is empty, want a synthesized key")
	}
	// The caller's map must be left exactly as built: the doc promises no writes.
	original := msg("hello", "")
	before := len(original.Metadata)
	d.Enqueue(&original)
	if len(original.Metadata) != before {
		t.Errorf("caller Metadata has %d entries, want it untouched at %d",
			len(original.Metadata), before)
	}
}

// A declined Enqueue must not leave a half-assigned identity behind: a message
// that is not persisted must not claim a row it does not own.
func TestEnqueueLeavesMessageUntouchedWhenAlreadyBacked(t *testing.T) {
	f := openSpool(t)
	d := NewInbound(f.repo, (&fakeBus{ch: make(chan bus.InboundMessage, 8)}).publish)

	got := bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "chat-1",
		SessionKey: "telegram:chat-1",
		Content:    "hello",
		SpoolID:    42,
	}
	if d.Enqueue(&got) {
		t.Error("Enqueue() = true for a message that already carries a SpoolID")
	}
	if got.SpoolID != 42 || got.DedupeID != "" {
		t.Errorf("msg = %+v, want it untouched (SpoolID 42, empty DedupeID)", got)
	}
}

func TestDedupeIDsAreUniqueAcrossMessages(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	d := NewInbound(repo, (&fakeBus{ch: make(chan bus.InboundMessage, 64)}).publish)

	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		// No message_id: every key is synthesized and must not collide.
		m := msg("x", "")
		if !d.Enqueue(&m) {
			t.Fatalf("Enqueue() = false on message %d", i)
		}
		if seen[m.DedupeID] {
			t.Fatalf("duplicate synthesized DedupeID %q", m.DedupeID)
		}
		seen[m.DedupeID] = true
	}
}

func TestEnqueueFailureLeavesMessageUnpersisted(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	b := newFakeBus(8)
	d := NewInbound(repo, b.publish)

	// Close the database to make the durable write fail. Enqueue reports it and
	// the caller still publishes: availability wins over durability.
	f.closeDB(t)

	got := msg("hello", "m-1")
	if d.Enqueue(&got) {
		t.Error("Enqueue() = true after a failed write, want false")
	}
	if got.SpoolID != 0 {
		t.Errorf("SpoolID = %d, want 0 after a failed enqueue", got.SpoolID)
	}
	if got.DedupeID != "m-1" {
		t.Errorf("DedupeID = %q, want %q: the key is assigned before the write", got.DedupeID, "m-1")
	}

	if !b.publish(got) {
		t.Fatal("publish() = false, want the unpersisted message to reach the bus")
	}
	pub := b.waitPublished(t, 1, time.Second)
	if pub[0].DedupeID != "m-1" {
		t.Errorf("published DedupeID = %q, want %q", pub[0].DedupeID, "m-1")
	}
}

func TestFullBusLeavesRowForPump(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	// Unbuffered channel and nobody reading: the bus is "full".
	b := newFakeBus(0)
	d := NewInbound(repo, b.publish)

	got := msg("hello", "m-1")
	if !d.Enqueue(&got) {
		t.Fatal("Enqueue() = false, want the row written before the publish attempt")
	}
	if got.SpoolID == 0 {
		t.Fatal("SpoolID = 0, want the row id so the caller can roll back")
	}

	// The caller's publish is rejected. The row survives on purpose: it is the
	// only copy left, so nobody may Complete it here. It is still claimed by
	// this instance, which is exactly why the caller must undo that claim.
	if b.publish(got) {
		t.Fatal("publish() = true, want a full bus to reject the message")
	}
	st := statsOf(t, repo)
	if st.PendingInbound != 0 {
		t.Errorf("PendingInbound = %d, want 0: the rejected row is still claimed", st.PendingInbound)
	}
	if st.ClaimedInbound != 1 {
		t.Errorf("ClaimedInbound = %d, want 1 (row must survive a full bus)", st.ClaimedInbound)
	}
	if !d.Release(&got) {
		t.Fatal("Release() = false, want the row handed back to the pending set")
	}
	if st := statsOf(t, repo); st.PendingInbound != 1 {
		t.Fatalf("PendingInbound = %d after Release, want 1", st.PendingInbound)
	}

	// The pump is what recovers it once there is room.
	room := make(chan bus.InboundMessage, 1)
	d2 := NewInbound(repo, func(m bus.InboundMessage) bool {
		select {
		case room <- m:
			return true
		default:
			return false
		}
	})
	d2.pumpOnce(context.Background())

	select {
	case m := <-room:
		if m.Content != "hello" {
			t.Errorf("pumped content = %q, want %q", m.Content, "hello")
		}
	default:
		t.Fatal("pumpOnce did not republish the deferred row")
	}
	if st := statsOf(t, repo); st.PendingInbound != 0 {
		t.Errorf("PendingInbound = %d after pumping, want 0", st.PendingInbound)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// ShouldSkip / Finish
// ──────────────────────────────────────────────────────────────────────────────

func TestShouldSkipNeedsDedupeID(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	d := NewInbound(repo, (&fakeBus{ch: make(chan bus.InboundMessage, 4)}).publish)

	if d.ShouldSkip(bus.InboundMessage{Channel: "telegram"}) {
		t.Error("ShouldSkip() = true for an empty DedupeID, want false")
	}
	if d.ShouldSkip(bus.InboundMessage{Channel: "telegram", DedupeID: "never-seen"}) {
		t.Error("ShouldSkip() = true for an unknown id, want false")
	}
}

func TestFinishRecordsLedgerAndDropsRow(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	b := newFakeBus(4)
	d := NewInbound(repo, b.publish)

	got := msg("hello", "m-1")
	d.Enqueue(&got)
	d.Finish(got)

	st := statsOf(t, repo)
	if st.PendingInbound != 0 {
		t.Errorf("PendingInbound = %d after Finish, want 0 (row dropped)", st.PendingInbound)
	}
	if st.ProcessedCount != 1 {
		t.Errorf("ProcessedCount = %d after Finish, want 1", st.ProcessedCount)
	}
	if !d.ShouldSkip(got) {
		t.Error("ShouldSkip() = false after Finish, want true (exactly-once processing)")
	}
}

func TestFinishWithoutDedupeIDIsANoOp(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	d := NewInbound(repo, (&fakeBus{ch: make(chan bus.InboundMessage, 4)}).publish)

	d.Finish(bus.InboundMessage{Channel: "telegram"})

	if st := statsOf(t, repo); st.ProcessedCount != 0 {
		t.Errorf("ProcessedCount = %d, want 0 for an unidentifiable message", st.ProcessedCount)
	}
}

func TestFinishLedgerFailureKeepsRow(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	b := newFakeBus(4)
	d := NewInbound(repo, b.publish)

	got := msg("hello", "m-1")
	if !d.Enqueue(&got) {
		t.Fatal("Enqueue() = false, want the row written before Finish")
	}

	// Kill the database: Finish must not drop the row, because the row is the
	// only thing that can still get the message replayed.
	f.closeDB(t)
	d.Finish(got) // best-effort: logs, never panics

	// Reopen the same file to confirm the row survived.
	if st := statsOf(t, f.reopen(t).Spool()); st.PendingInbound+st.ClaimedInbound != 1 {
		t.Errorf("inbound rows = %d pending + %d claimed, want 1 (row kept when the ledger write failed)",
			st.PendingInbound, st.ClaimedInbound)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Release: the rollback of a live claim
// ──────────────────────────────────────────────────────────────────────────────

// TestEnqueueClaimsItsOwnRow is the invariant the whole in-flight protocol rests
// on: the row Enqueue writes belongs to this instance from the moment it exists,
// so no replay pass can see a message the agent loop is working on.
func TestEnqueueClaimsItsOwnRow(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	b := newFakeBus(8)
	d := NewInbound(repo, b.publish)

	got := msg("hello", "m-1")
	if !d.Enqueue(&got) {
		t.Fatal("Enqueue() = false, want the message persisted")
	}

	st := statsOf(t, repo)
	if st.PendingInbound != 0 {
		t.Errorf("PendingInbound = %d, want 0: a live row is never pending", st.PendingInbound)
	}
	if st.ClaimedInbound != 1 {
		t.Errorf("ClaimedInbound = %d, want 1", st.ClaimedInbound)
	}

	// Not even another instance can claim it.
	if items, err := repo.ClaimBatch(store.SpoolInbound, 10, "other-instance", time.Now()); err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	} else if len(items) != 0 {
		t.Errorf("ClaimBatch(other) = %d rows, want 0", len(items))
	}
	if owners := claimedBy(t, f.s); len(owners) != 1 || owners[0] != d.InstanceID() {
		t.Errorf("claimed_by = %v, want [%s]", owners, d.InstanceID())
	}
}

// TestReleaseHandsRowBackToPump is the other half: when the bus refuses a live
// message, Release makes exactly that row pending again and the pump replays it.
func TestReleaseHandsRowBackToPump(t *testing.T) {
	f := openSpool(t)
	repo := f.repo

	got := msg("hello", "m-1")
	d := NewInbound(repo, func(bus.InboundMessage) bool { return false }) // bus full
	if !d.Enqueue(&got) {
		t.Fatal("Enqueue() = false, want the row written")
	}
	if !d.Release(&got) {
		t.Fatal("Release() = false, want the row handed back to pending")
	}
	if st := statsOf(t, repo); st.PendingInbound != 1 || st.ClaimedInbound != 0 {
		t.Errorf("stats = %+v, want 1 pending / 0 claimed after Release", st)
	}

	// Releasing twice is not an error, there is simply nothing left to release.
	if d.Release(&got) {
		t.Error("Release() = true the second time, want false (row no longer claimed here)")
	}
	// Nothing to back: a nil message, a message with no row, a nil service.
	if d.Release(nil) {
		t.Error("Release(nil) = true, want false")
	}
	if d.Release(&bus.InboundMessage{Channel: "telegram"}) {
		t.Error("Release(unspooled message) = true, want false")
	}
	var noService *Inbound
	if noService.Release(&got) {
		t.Error("(*Inbound)(nil).Release() = true, want false")
	}

	// A pass with room on the bus now picks it up.
	b := newFakeBus(4)
	NewInbound(repo, b.publish).pumpOnce(context.Background())
	pub := b.waitPublished(t, 1, time.Second)
	if pub[0].Content != "hello" {
		t.Errorf("pumped content = %q, want %q", pub[0].Content, "hello")
	}
	if pub[0].SpoolID != got.SpoolID {
		t.Errorf("pumped spool id = %d, want %d", pub[0].SpoolID, got.SpoolID)
	}
	if st := statsOf(t, repo); st.PendingInbound+st.ClaimedInbound != 0 {
		t.Errorf("spool holds %+v after the replay, want nothing left", st)
	}
}

// TestClaimPassDoesNotReleaseForeignLiveRows is the anti-regression test for the
// double-delivery bug. claimPass used to finish with a blanket ReleaseClaims for
// its own instance, which also handed back every LIVE row that Enqueue had
// claimed - and the next tick published those a second time. Now the pass only
// releases the ids it claimed itself.
func TestClaimPassDoesNotReleaseForeignLiveRows(t *testing.T) {
	f := openSpool(t)
	repo := f.repo

	// One service plays both roles, exactly as the gateway does: the channel
	// path spools live rows through it and the pump replays through it too, so
	// the live claims and the pass claims carry the SAME instance id. That is
	// the only case a blanket release got wrong.
	d := NewInbound(repo, func(bus.InboundMessage) bool { return false }) // bus always full

	first := msg("live-1", "m-live-1")
	second := msg("live-2", "m-live-2")
	for _, m := range []*bus.InboundMessage{&first, &second} {
		if !d.Enqueue(m) {
			t.Fatalf("Enqueue(%q) = false, want the row written", m.Content)
		}
	}
	pendingID := rawEnqueue(t, repo, "telegram", "m-pending", payloadFor(t, msg("pending", "m-pending")))

	// A pass whose bus is full: it claims the pending row, defers it, and must
	// hand back ONLY that row. A short context keeps the retry budget from
	// turning the deferral into a five second wait.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	res, err := d.claimPass(ctx)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("claimPass() failed: %v", err)
	}
	if res.deferred != 1 {
		t.Errorf("claimPass() deferred %d rows, want 1", res.deferred)
	}

	st := statsOf(t, repo)
	if st.PendingInbound != 1 {
		t.Errorf("PendingInbound = %d, want 1: the deferred row goes back for the next tick", st.PendingInbound)
	}
	if st.ClaimedInbound != 2 {
		t.Errorf("ClaimedInbound = %d, want 2: the two live rows must still be held", st.ClaimedInbound)
	}
	if owners := claimedBy(t, f.s); len(owners) != 1 || owners[0] != d.InstanceID() {
		t.Errorf("claimed_by = %v, want only this instance's live claims", owners)
	}
	if counts, err := repo.PendingBySession(store.SpoolInbound); err != nil {
		t.Fatalf("PendingBySession() failed: %v", err)
	} else if counts["telegram:chat-1"] != 1 {
		t.Errorf("pending rows per session = %v, want just the deferred one", counts)
	}

	// The two live rows are still there, still claimed, and invisible to any
	// pass: which is what stops the pump from delivering them twice.
	for _, id := range []int64{first.SpoolID, second.SpoolID} {
		if id == pendingID || id == 0 {
			t.Fatalf("fixture broken: live row id %d collides with the pending row %d", id, pendingID)
		}
		if row := peekRow(t, f, id); row.Attempts != 0 {
			t.Errorf("live row %d attempts = %d, want 0: a pass must never touch it", id, row.Attempts)
		}
	}

	// A second instance can only ever reach the row that really is pending.
	if items, err := repo.ClaimBatch(store.SpoolInbound, 10, "other-instance", time.Now()); err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	} else if len(items) != 1 || items[0].ID != pendingID {
		t.Errorf("ClaimBatch(other) returned %d rows, want only row %d", len(items), pendingID)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Drain / replay
// ──────────────────────────────────────────────────────────────────────────────

func TestDrainReplaysPendingFIFO(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	for i := 0; i < 3; i++ {
		rawEnqueue(t, repo, "telegram", "m-"+string(rune('0'+i)),
			payloadFor(t, bus.InboundMessage{
				Channel: "telegram", ChatID: "chat-1", SessionKey: "telegram:chat-1",
				Content: fmt.Sprintf("c-%d", i), DedupeID: fmt.Sprintf("m-%d", i),
			}))
	}

	b := newFakeBus(8)
	d := NewInbound(repo, b.publish)

	n, err := d.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain() failed: %v", err)
	}
	if n != 3 {
		t.Errorf("Drain() = %d republished, want 3", n)
	}

	pub := b.waitPublished(t, 3, time.Second)
	for i, m := range pub {
		if want := fmt.Sprintf("c-%d", i); m.Content != want {
			t.Errorf("message %d = %q, want %q (FIFO order)", i, m.Content, want)
		}
	}
	if st := statsOf(t, repo); st.PendingInbound != 0 {
		t.Errorf("PendingInbound = %d after Drain, want 0", st.PendingInbound)
	}
}

func TestDrainTagsIdentityFromTheRow(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	// A legacy payload: no dedupe_id, and a stale spool_id baked in by an old
	// build. The row must own both values after a replay.
	id := rawEnqueue(t, repo, "telegram", "legacy-9",
		`{"channel":"telegram","chat_id":"chat-1","session_key":"telegram:chat-1","content":"hi","spool_id":9999}`)

	b := newFakeBus(4)
	d := NewInbound(repo, b.publish)

	if _, err := d.Drain(context.Background()); err != nil {
		t.Fatalf("Drain() failed: %v", err)
	}
	pub := b.waitPublished(t, 1, time.Second)

	if pub[0].DedupeID != "legacy-9" {
		t.Errorf("DedupeID = %q, want the row msg_id %q", pub[0].DedupeID, "legacy-9")
	}
	if pub[0].SpoolID != id {
		t.Errorf("SpoolID = %d, want the claimed row id %d (payload value must be ignored)",
			pub[0].SpoolID, id)
	}
}

func TestDrainSkipsAlreadyProcessed(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	rawEnqueue(t, repo, "telegram", "dup-1",
		payloadFor(t, bus.InboundMessage{
			Channel: "telegram", ChatID: "chat-1", Content: "hi", DedupeID: "dup-1",
		}))

	// The ledger says this one was answered before the crash.
	if err := repo.MarkProcessed("telegram", "dup-1"); err != nil {
		t.Fatalf("MarkProcessed() failed: %v", err)
	}

	b := newFakeBus(4)
	d := NewInbound(repo, b.publish)

	n, err := d.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain() failed: %v", err)
	}
	if n != 0 {
		t.Errorf("Drain() = %d republished, want 0 for a duplicate", n)
	}
	if b.callCount() != 0 {
		t.Errorf("publish called %d times, want 0", b.callCount())
	}
	// The duplicate must be dropped, not left to be replayed again forever.
	if st := statsOf(t, repo); st.PendingInbound+st.ClaimedInbound != 0 {
		t.Errorf("spool still holds %d inbound rows, want 0", st.PendingInbound+st.ClaimedInbound)
	}
}

func TestDrainReclaimsStaleClaims(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	rawEnqueue(t, repo, "telegram", "m-1",
		payloadFor(t, bus.InboundMessage{Channel: "telegram", ChatID: "chat-1", Content: "hi", DedupeID: "m-1"}))

	// A claim by a previous instance, aged past StaleClaimTimeout: this is what
	// a hard crash leaves behind.
	if _, err := repo.ClaimBatch(store.SpoolInbound, 10, "gw-old", time.Now().Add(-2*StaleClaimTimeout)); err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	}
	if st := statsOf(t, repo); st.ClaimedInbound != 1 {
		t.Fatalf("ClaimedInbound = %d, want 1 before Drain", st.ClaimedInbound)
	}

	b := newFakeBus(4)
	d := NewInbound(repo, b.publish)

	n, err := d.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain() failed: %v", err)
	}
	if n != 1 {
		t.Errorf("Drain() = %d republished, want 1 (stale claim must be replayed)", n)
	}
	b.waitPublished(t, 1, time.Second)
}

func TestDrainDoesNotStealAFreshForeignClaim(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	rawEnqueue(t, repo, "telegram", "m-1",
		payloadFor(t, bus.InboundMessage{Channel: "telegram", ChatID: "chat-1", Content: "hi", DedupeID: "m-1"}))

	// Another instance is working on it right now: it must not be replayed
	// twice by two live processes.
	if _, err := repo.ClaimBatch(store.SpoolInbound, 10, "gw-other", time.Now()); err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	}

	b := newFakeBus(4)
	d := NewInbound(repo, b.publish)

	if n, err := d.Drain(context.Background()); n != 0 || err != nil {
		t.Errorf("Drain() = (%d, %v), want (0, nil) with a fresh foreign claim", n, err)
	}
	if b.callCount() != 0 {
		t.Errorf("publish called %d times, want 0", b.callCount())
	}
}

func TestDrainDeadLettersPoison(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	id := rawEnqueue(t, repo, "telegram", "poison-1", "{not json")

	b := newFakeBus(4)
	d := NewInbound(repo, b.publish)

	// Each pass claims the row once (it stays claimed until released), so
	// StaleClaimTimeout or a graceful release separates the attempts.
	for attempt := 1; attempt <= poisonLimit; attempt++ {
		if _, err := d.Drain(context.Background()); err != nil {
			t.Fatalf("Drain() pass %d failed: %v", attempt, err)
		}
		if _, err := d.ReleaseClaims(); err != nil {
			t.Fatalf("ReleaseClaims() pass %d failed: %v", attempt, err)
		}

		st := statsOf(t, repo)
		if attempt < poisonLimit {
			if st.PendingInbound+st.ClaimedInbound != 1 {
				t.Fatalf("pass %d: spool holds %d rows, want 1 until the ceiling",
					attempt, st.PendingInbound+st.ClaimedInbound)
			}
			continue
		}
		if st.PendingInbound+st.ClaimedInbound != 0 {
			t.Errorf("after %d attempts the poison row survived (%+v), want it dead-lettered",
				poisonLimit, st)
		}
	}
	if b.callCount() != 0 {
		t.Errorf("publish called %d times, want 0 for an undecodable row", b.callCount())
	}
	_ = id
}

func TestDrainRespectsContextCancellation(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	rawEnqueue(t, repo, "telegram", "m-1", payloadFor(t, msg("hi", "m-1")))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already expired

	d := NewInbound(repo, (&fakeBus{ch: make(chan bus.InboundMessage, 4)}).publish)

	// An expired context must not hang or replay work into a dying process.
	if _, err := d.Drain(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("Drain() error = %v, want context.Canceled or nil", err)
	}
}

func TestDrainFullBusDefersAndReleasesClaim(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	rawEnqueue(t, repo, "telegram", "m-1", payloadFor(t, msg("hi", "m-1")))

	// Nothing can ever be published: the pass must spend its retry budget,
	// leave the row in the spool, and hand the claim back.
	d := NewInbound(repo, func(bus.InboundMessage) bool { return false })

	start := time.Now()
	n, err := d.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain() failed: %v", err)
	}
	if n != 0 {
		t.Errorf("Drain() = %d republished, want 0 on a full bus", n)
	}
	if elapsed := time.Since(start); elapsed < publishRetryBudget {
		t.Errorf("Drain() returned after %s, want the full %s retry budget", elapsed, publishRetryBudget)
	}

	st := statsOf(t, repo)
	if st.PendingInbound != 1 {
		t.Errorf("PendingInbound = %d, want 1: the row is the only copy", st.PendingInbound)
	}
	if st.ClaimedInbound != 0 {
		t.Errorf("ClaimedInbound = %d, want 0: a deferred claim must be released", st.ClaimedInbound)
	}
	if items, err := repo.ClaimBatch(store.SpoolInbound, 10, "probe", time.Now()); err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	} else if len(items) != 1 || items[0].Attempts != 1 {
		t.Errorf("claimed %+v, want 1 row with 1 attempt recorded", items)
	}
}

func TestDrainRepublishesWhenRoomAppears(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	rawEnqueue(t, repo, "telegram", "m-1", payloadFor(t, msg("hi", "m-1")))

	// The bus is full for the first tries and frees up mid-budget: this is the
	// boot-storm case publishWithRetry exists for.
	b := &fakeBus{ch: make(chan bus.InboundMessage, 1), rejects: []bool{false, false, true}}
	d := NewInbound(repo, b.publish)

	n, err := d.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain() failed: %v", err)
	}
	if n != 1 {
		t.Errorf("Drain() = %d, want 1", n)
	}
	// rejects == {false,false,true}: two "bus full" answers then room appears,
	// so exactly three publish attempts are expected.
	if b.callCount() != 3 {
		t.Errorf("publish called %d times, want 3 (2 rejected attempts retried)", b.callCount())
	}
	if st := statsOf(t, repo); st.PendingInbound != 0 {
		t.Errorf("PendingInbound = %d, want 0", st.PendingInbound)
	}
}

func TestReleaseClaimsHandsBackOwnRowsOnly(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	for _, id := range []string{"m-1", "m-2"} {
		rawEnqueue(t, repo, "telegram", id, payloadFor(t, msg("hi", id)))
	}

	d := NewInbound(repo, (&fakeBus{ch: make(chan bus.InboundMessage, 4)}).publish)
	if _, err := repo.ClaimBatch(store.SpoolInbound, 1, d.InstanceID(), time.Now()); err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	}
	if _, err := repo.ClaimBatch(store.SpoolInbound, 1, "gw-other", time.Now()); err != nil {
		t.Fatalf("ClaimBatch() failed: %v", err)
	}

	released, err := d.ReleaseClaims()
	if err != nil {
		t.Fatalf("ReleaseClaims() failed: %v", err)
	}
	if released != 1 {
		t.Errorf("ReleaseClaims() = %d, want 1 (only this instance's row)", released)
	}

	st := statsOf(t, repo)
	if st.PendingInbound != 1 || st.ClaimedInbound != 1 {
		t.Errorf("stats = %+v, want 1 pending / 1 still claimed by the other instance", st)
	}
}

func TestInstanceIDsAreUnique(t *testing.T) {
	a := NewInbound(nil, nil)
	b := NewInbound(nil, nil)

	if a.InstanceID() == b.InstanceID() {
		t.Errorf("two instances share id %q", a.InstanceID())
	}
	if !strings.HasPrefix(a.InstanceID(), "lele-") {
		t.Errorf("InstanceID() = %q, want a %q prefix", a.InstanceID(), "lele-")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Pump
// ──────────────────────────────────────────────────────────────────────────────

func TestPumpOnceIdleSkipsReplay(t *testing.T) {
	f := openSpool(t)
	repo := f.repo

	calls := 0
	d := NewInbound(repo, func(bus.InboundMessage) bool { calls++; return true })

	// Nothing pending: the pass must not touch the publisher at all.
	d.pumpOnce(context.Background())
	if calls != 0 {
		t.Errorf("publish called %d times, want 0 on an idle spool", calls)
	}
}

func TestPumpOnceReplaysWorkThatAppeared(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	rawEnqueue(t, repo, "telegram", "m-1", payloadFor(t, msg("hi", "m-1")))

	b := newFakeBus(4)
	d := NewInbound(repo, b.publish)
	d.pumpOnce(context.Background())
	pub := b.waitPublished(t, 1, time.Second)
	if pub[0].Content != "hi" {
		t.Errorf("pumped content = %q, want %q", pub[0].Content, "hi")
	}
	if st := statsOf(t, repo); st.PendingInbound != 0 {
		t.Errorf("PendingInbound = %d after pump, want 0", st.PendingInbound)
	}
}

func TestStartPumpDrainsInTheBackground(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	rawEnqueue(t, repo, "telegram", "m-1", payloadFor(t, msg("hi", "m-1")))

	b := newFakeBus(4)
	d := NewInbound(repo, b.publish)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.StartPump(ctx); close(done) }()

	// The pump ticks every PumpInterval; give it a few ticks of slack.
	b.waitPublished(t, 1, 5*time.Second)
	if st := statsOf(t, repo); st.PendingInbound != 0 {
		t.Errorf("PendingInbound = %d, want 0 after the pump ran", st.PendingInbound)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartPump() did not return after cancellation")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// End-to-end: the restart story
// ──────────────────────────────────────────────────────────────────────────────

// TestRestartReplaysExactlyOnce is the package's reason to exist: a message
// accepted from a channel, answered, and then still sitting in the spool when
// the process dies must not be answered twice by the successor.
func TestRestartReplaysExactlyOnce(t *testing.T) {
	f := openSpool(t)
	repo := f.repo

	// Process 1: message arrives, is spooled, is published, is answered.
	bus1 := newFakeBus(4)
	first := NewInbound(repo, bus1.publish)
	m1 := msg("first", "m-1")
	if !first.Enqueue(&m1) {
		t.Fatal("Enqueue() = false for the first message")
	}
	first.Finish(m1)

	// A second message arrives but the process dies before it is answered. Its
	// row stays claimed by the dead instance: that is what a crash leaves behind
	// now that live rows are born claimed.
	m2 := msg("second", "m-2")
	if !first.Enqueue(&m2) {
		t.Fatal("Enqueue() = false for the second message")
	}
	if m2.SpoolID == 0 {
		t.Fatal("second message was not spooled")
	}

	// Restart: a brand new instance over the same database.
	reopened := f.reopen(t)
	bus2 := newFakeBus(4)
	second := NewInbound(reopened.Spool(), bus2.publish)

	// The stale-claim reclaim Drain performs is time-based, so stand in for the
	// timeout elapsing by running that same step against a clock far enough in
	// the future: the successor's own Drain then sees a pending row.
	if n, err := reopened.Spool().ReclaimStale(StaleClaimTimeout, time.Now().Add(2*StaleClaimTimeout)); err != nil || n != 1 {
		t.Fatalf("ReclaimStale() = (%d, %v), want the orphaned claim handed back", n, err)
	}

	if n, err := second.Drain(context.Background()); err != nil || n != 1 {
		t.Fatalf("Drain() = (%d, %v), want 1 replayed message", n, err)
	}

	pub := bus2.waitPublished(t, 1, time.Second)
	if pub[0].Content != "second" {
		t.Errorf("replayed content = %q, want %q", pub[0].Content, "second")
	}
	if pub[0].DedupeID != "m-2" {
		t.Errorf("replayed DedupeID = %q, want %q", pub[0].DedupeID, "m-2")
	}

	// The answered message must never come back.
	if !second.ShouldSkip(m1) {
		t.Error("first message is not recognised as already processed")
	}
	if second.ShouldSkip(m2) {
		t.Error("second message was skipped, but it was never answered")
	}

	// Draining again is a no-op: the spool is empty.
	if n, err := second.Drain(context.Background()); n != 0 || err != nil {
		t.Errorf("second Drain() = (%d, %v), want (0, nil)", n, err)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("truncate() = %q, want the input unchanged", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("truncate() = %q, want %q", got, "hello...")
	}
	// An exact fit must not gain an ellipsis.
	if got := truncate("abcde", 5); got != "abcde" {
		t.Errorf("truncate() = %q, want %q", got, "abcde")
	}
}

func TestRandomHexIsUsable(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := randomHex()
		if len(id) != idBytes*2 {
			t.Fatalf("randomHex() = %q, want %d chars", id, idBytes*2)
		}
		if seen[id] {
			t.Fatalf("randomHex() repeated %q", id)
		}
		seen[id] = true
	}
}

func TestOutcomeString(t *testing.T) {
	cases := map[outcome]string{
		outcomePublished: "published",
		outcomeSkipped:   "skipped",
		outcomeDeferred:  "deferred",
	}
	for o, want := range cases {
		if got := o.String(); got != want {
			t.Errorf("outcome(%d).String() = %q, want %q", o, got, want)
		}
	}
}

func TestDedupeIDFor(t *testing.T) {
	if got := dedupeIDFor(msg("x", "abc")); got != "abc" {
		t.Errorf("dedupeIDFor() = %q, want the channel id %q", got, "abc")
	}
	withEmpty := bus.InboundMessage{Channel: "telegram", Metadata: map[string]string{"message_id": ""}}
	if got := dedupeIDFor(withEmpty); got == "" {
		t.Error("dedupeIDFor() = empty, want a synthesized key")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Claim lifecycle (regression: stranded claims)
//
// A row that ends a pass still claimed is invisible to every later pass:
// ClaimBatch only selects claimed_by = '' and pumpOnce only looks at
// PendingInbound. Such a row is therefore lost to the running gateway until
// the next restart's ReclaimStale - and a poison row can never reach its
// dead-letter ceiling, because it is never claimed a second time.
// ──────────────────────────────────────────────────────────────────────────────

// blockSpoolDeletes installs a trigger that makes every spool DELETE fail,
// which stands in for a database error partway through a replay pass.
func blockSpoolDeletes(t *testing.T, s *store.Store) {
	t.Helper()

	if _, err := s.DB().Exec(`CREATE TRIGGER block_spool_delete BEFORE DELETE ON spool
		BEGIN SELECT RAISE(ABORT, 'boom: delete blocked'); END`); err != nil {
		t.Fatalf("installing trigger failed: %v", err)
	}
}

// allowSpoolDeletes removes the trigger. A trigger lives in the database file,
// so reopening the store would not clear it.
func allowSpoolDeletes(t *testing.T, s *store.Store) {
	t.Helper()

	if _, err := s.DB().Exec(`DROP TRIGGER block_spool_delete`); err != nil {
		t.Fatalf("dropping trigger failed: %v", err)
	}
}

func TestPassErrorDoesNotStrandClaims(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	for _, id := range []string{"m-1", "m-2", "m-3"} {
		rawEnqueue(t, repo, "telegram", id, payloadFor(t, msg("hi", id)))
	}

	// The first row completes its publish but its DELETE fails, which aborts
	// the pass while rows 2 and 3 are still claimed and unprocessed.
	blockSpoolDeletes(t, f.s)

	d := NewInbound(repo, (&fakeBus{ch: make(chan bus.InboundMessage, 8)}).publish)
	if _, err := d.Drain(context.Background()); err == nil {
		t.Error("Drain() error = nil, want the store failure reported")
	}

	// The invariant: a finished pass holds nothing.
	st := statsOf(t, repo)
	if st.ClaimedInbound != 0 {
		t.Errorf("ClaimedInbound = %d after a failed pass, want 0 (claims must be released)",
			st.ClaimedInbound)
	}
	if st.PendingInbound != 3 {
		t.Errorf("PendingInbound = %d, want 3 (all rows back in the queue)", st.PendingInbound)
	}

	// And the pump can actually recover them once the store works again: this
	// is the behaviour the bug destroyed.
	allowSpoolDeletes(t, f.s)
	b := newFakeBus(8)
	NewInbound(repo, b.publish).pumpOnce(context.Background())
	b.waitPublished(t, 3, 2*time.Second)
	if st := statsOf(t, repo); st.PendingInbound != 0 {
		t.Errorf("PendingInbound = %d after recovery, want 0", st.PendingInbound)
	}
}

func TestPoisonRowReachesDeadLetterCeiling(t *testing.T) {
	f := openSpool(t)
	repo := f.repo
	rawEnqueue(t, repo, "telegram", "poison", "{not json")

	b := newFakeBus(4)
	d := NewInbound(repo, b.publish)

	// Only the pump is running - no manual ReleaseClaims, no restart. Each tick
	// must be able to claim the row again, so the attempt counter climbs.
	for pass := 1; pass <= poisonLimit; pass++ {
		d.pumpOnce(context.Background())

		items, err := repo.PendingBySession(store.SpoolInbound)
		if err != nil {
			t.Fatalf("PendingBySession() failed: %v", err)
		}
		if pass < poisonLimit && items["telegram:chat-1"] != 1 {
			t.Fatalf("pass %d: pending rows = %d, want the poison row requeued for the next tick",
				pass, items["telegram:chat-1"])
		}
	}

	if st := statsOf(t, repo); st.PendingInbound+st.ClaimedInbound != 0 {
		t.Errorf("after %d pump ticks the poison row survived (%+v), want it dead-lettered",
			poisonLimit, st)
	}
	if b.callCount() != 0 {
		t.Errorf("publish called %d times, want 0 for an undecodable row", b.callCount())
	}
}
