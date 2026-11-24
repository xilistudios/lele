package durable

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/store"
)

type outbox struct {
	mu     sync.Mutex
	sent   []bus.OutboundMessage
	calls  int
	script func(msg bus.OutboundMessage) Delivery
}

func (o *outbox) deliverer() DeliverFunc {
	return func(_ context.Context, msg bus.OutboundMessage) Delivery {
		o.mu.Lock()
		o.calls++
		o.sent = append(o.sent, msg)
		script := o.script
		o.mu.Unlock()

		if script == nil {
			return Delivery{Delivered: true}
		}
		return script(msg)
	}
}

func (o *outbox) delivered() []bus.OutboundMessage {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]bus.OutboundMessage(nil), o.sent...)
}

func (o *outbox) callCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls
}

func alwaysDelivered() DeliverFunc {
	return func(context.Context, bus.OutboundMessage) Delivery { return Delivery{Delivered: true} }
}

func outMsg(content string) bus.OutboundMessage {
	return bus.OutboundMessage{
		Channel: "telegram",
		ChatID:  "chat-1",
		Content: content,
	}
}

func outPayload(t *testing.T, m bus.OutboundMessage) string {
	t.Helper()

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return string(data)
}

func rawOutEnqueue(t *testing.T, repo *store.SpoolRepo, payload string) int64 {
	t.Helper()

	id, err := repo.Enqueue(store.SpoolOutbound, "telegram", "chat-1", "chat-1", "", payload)
	if err != nil {
		t.Fatalf("Enqueue(outbound) failed: %v", err)
	}
	return id
}

func agedOutEnqueue(t *testing.T, fixture *spoolFixture, payload string, age time.Duration) int64 {
	t.Helper()

	id := rawOutEnqueue(t, fixture.repo, payload)
	stamp := time.Now().UTC().Add(-age).Format(time.RFC3339Nano)
	if _, err := fixture.s.DB().Exec(`UPDATE spool SET created_at = ? WHERE id = ?`, stamp, id); err != nil {
		t.Fatalf("backdating spool row %d failed: %v", id, err)
	}
	return id
}

func staleClaim(t *testing.T, fixture *spoolFixture, id int64, owner string, age time.Duration) {
	t.Helper()

	stamp := time.Now().UTC().Add(-age).Format(time.RFC3339Nano)
	_, err := fixture.s.DB().Exec(
		`UPDATE spool SET claimed_by = ?, claimed_at = ? WHERE id = ?`, owner, stamp, id)
	if err != nil {
		t.Fatalf("stale-claiming spool row %d failed: %v", id, err)
	}
}

func ledgerCount(t *testing.T, fixture *spoolFixture) int {
	t.Helper()

	var n int
	if err := fixture.s.DB().QueryRow(`SELECT COUNT(*) FROM processed_messages`).Scan(&n); err != nil {
		t.Fatalf("counting the dedupe ledger failed: %v", err)
	}
	return n
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string, args ...interface{}) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out after %v: %s", timeout, fmt.Sprintf(msg, args...))
}

func TestOutboundEnqueueClaimsRowAndTagsSpoolID(t *testing.T) {
	fixture := openSpool(t)
	d := NewOutbound(fixture.repo, alwaysDelivered())

	got := outMsg("hello")
	if !d.Enqueue(&got) {
		t.Fatal("Enqueue() = false, want the row written")
	}
	if got.SpoolID == 0 {
		t.Fatal("SpoolID = 0, want the row id tagged in place")
	}

	st := statsOf(t, fixture.repo)
	if st.ClaimedOutbound != 1 {
		t.Errorf("ClaimedOutbound = %d, want 1: a live row must be invisible to the pump", st.ClaimedOutbound)
	}
	if st.PendingOutbound != 0 {
		t.Errorf("PendingOutbound = %d, want 0", st.PendingOutbound)
	}
	if st.PendingInbound+st.ClaimedInbound != 0 {
		t.Errorf("spool = %+v, want outbound spooling to leave the inbound side alone", st)
	}

	owners := claimedBy(t, fixture.s)
	if len(owners) != 1 || owners[0] != d.InstanceID() {
		t.Errorf("claimed_by = %v, want only %q", owners, d.InstanceID())
	}

	row := peekRow(t, fixture, got.SpoolID)
	if row.Direction != store.SpoolOutbound {
		t.Errorf("direction = %q, want %q", row.Direction, store.SpoolOutbound)
	}
	if row.SessionKey != "chat-1" || row.ChatID != "chat-1" {
		t.Errorf("session_key/chat_id = %q/%q, want both %q", row.SessionKey, row.ChatID, "chat-1")
	}
	if row.MsgID != "" {
		t.Errorf("msg_id = %q, want empty: outbound has no dedupe key", row.MsgID)
	}
}

func TestOutboundPayloadRoundTripsFields(t *testing.T) {
	fixture := openSpool(t)
	d := NewOutbound(fixture.repo, alwaysDelivered())

	got := outMsg("approve?")
	got.MessageID = "msg-7"
	got.ReplyTo = "msg-6"
	got.ReplyMarkup = map[string]any{"inline_keyboard": [][]map[string]any{
		{{"text": "yes", "callback_data": "ok"}, {"text": "no", "callback_data": "no"}},
	}}
	got.Attachments = []bus.FileAttachment{{Name: "log.txt", Path: "/tmp/log.txt", MIMEType: "text/plain"}}
	got.TextMode = "html"
	got.PlainText = "approve?"
	preview := false
	got.LinkPreview = &preview
	got.Metadata = map[string]string{"k": "v"}

	if !d.Enqueue(&got) {
		t.Fatal("Enqueue() = false, want the row written")
	}

	row := peekRow(t, fixture, got.SpoolID)
	var back bus.OutboundMessage
	if err := json.Unmarshal([]byte(row.Payload), &back); err != nil {
		t.Fatalf("unmarshal payload failed: %v", err)
	}

	if back.ReplyMarkup == nil {
		t.Fatal("ReplyMarkup lost: approval keyboards must survive the spool")
	}
	if len(back.Attachments) != 1 || back.Attachments[0].Path != "/tmp/log.txt" {
		t.Errorf("Attachments = %+v, want the one attachment back", back.Attachments)
	}
	if back.MessageID != "msg-7" || back.ReplyTo != "msg-6" {
		t.Errorf("MessageID/ReplyTo = %q/%q, want msg-7/msg-6", back.MessageID, back.ReplyTo)
	}
	if back.Content != "approve?" || back.TextMode != "html" || back.PlainText != "approve?" {
		t.Errorf("text fields lost: %+v", back)
	}
	if back.LinkPreview == nil || *back.LinkPreview {
		t.Errorf("LinkPreview = %v, want a restored false", back.LinkPreview)
	}
	if back.Metadata["k"] != "v" {
		t.Errorf("Metadata = %v, want k=v", back.Metadata)
	}
	if back.SpoolID != 0 {
		t.Errorf("payload SpoolID = %d, want 0", back.SpoolID)
	}
}

func TestShouldSpoolOutbound(t *testing.T) {
	withAttachment := outMsg("")
	withAttachment.Attachments = []bus.FileAttachment{{Path: "/tmp/x.png"}}

	blank := outMsg("   ")
	blankWithFile := outMsg("   ")
	blankWithFile.Attachments = []bus.FileAttachment{{Path: "/tmp/x.png"}}

	event := outMsg("hello")
	event.Event = "stream_end"

	intermediate := outMsg("hello")
	intermediate.IsIntermediate = true

	anonymous := outMsg("hello")
	anonymous.ChatID = ""

	backed := outMsg("hello")
	backed.SpoolID = 42

	tests := []struct {
		name string
		msg  bus.OutboundMessage
		want bool
	}{
		{"content only", outMsg("hello"), true},
		{"attachments only", withAttachment, true},
		{"blank content with file", blankWithFile, true},
		{"blank content", blank, false},
		{"event", event, false},
		{"intermediate", intermediate, false},
		{"no chat", anonymous, false},
		{"already backed", backed, false},
	}

	for _, tc := range tests {
		if got := ShouldSpoolOutbound(tc.msg); got != tc.want {
			t.Errorf("%s: ShouldSpoolOutbound(%+v) = %v, want %v", tc.name, tc.msg, got, tc.want)
		}
	}

	for _, channel := range []string{"cli", "system", "subagent"} {
		msg := outMsg("hello")
		msg.Channel = channel
		if ShouldSpoolOutbound(msg) {
			t.Errorf("internal channel %q must never be spooled", channel)
		}
	}
}

func TestOutboundNilSafe(t *testing.T) {
	var d *Outbound

	got := outMsg("hello")
	if d.Enqueue(&got) {
		t.Error("(*Outbound)(nil).Enqueue() = true, want false")
	}
	if got.SpoolID != 0 {
		t.Errorf("SpoolID = %d, want 0: a nil service writes nothing", got.SpoolID)
	}
	if d.Release(&got) {
		t.Error("Release() = true on a nil service, want false")
	}
	if d.Complete(&got) {
		t.Error("Complete() = true on a nil service, want false")
	}
	if d.Forget(&got, "reason") {
		t.Error("Forget() = true on a nil service, want false")
	}
	d.FlushPeer("native", "peer-1")

	if n, err := d.Drain(context.Background()); n != 0 || err != nil {
		t.Errorf("Drain() = (%d, %v), want (0, nil) on a nil service", n, err)
	}
	if released, err := d.ReleaseClaims(); released != 0 || err != nil {
		t.Errorf("ReleaseClaims() = (%d, %v), want (0, nil) on a nil service", released, err)
	}
	if st, err := d.Stats(); err != nil || st != (store.SpoolStats{}) {
		t.Errorf("Stats() = (%+v, %v), want zero value on a nil service", st, err)
	}
	if id := d.InstanceID(); id != "" {
		t.Errorf("InstanceID() = %q, want empty on a nil service", id)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.StartPump(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("StartPump() did not return after cancellation on a nil service")
	}
}

func TestOutboundNilRepoDisablesEverything(t *testing.T) {
	d := NewOutbound(nil, alwaysDelivered())

	got := outMsg("hello")
	if d.Enqueue(&got) {
		t.Error("Enqueue() = true with no repo, want false")
	}
	if got.SpoolID != 0 {
		t.Errorf("SpoolID = %d, want 0 with no repo", got.SpoolID)
	}
	if d.Release(&got) || d.Complete(&got) || d.Forget(&got, "x") {
		t.Error("Release/Complete/Forget = true with no repo, want false")
	}
	if n, err := d.Drain(context.Background()); n != 0 || err != nil {
		t.Errorf("Drain() = (%d, %v), want (0, nil) with no repo", n, err)
	}
	if released, err := d.ReleaseClaims(); released != 0 || err != nil {
		t.Errorf("ReleaseClaims() = (%d, %v), want (0, nil) with no repo", released, err)
	}
	if st, err := d.Stats(); err != nil || st != (store.SpoolStats{}) {
		t.Errorf("Stats() = (%+v, %v), want zero value with no repo", st, err)
	}
	if d.Enqueue(nil) {
		t.Error("Enqueue(nil) = true, want false")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.StartPump(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("StartPump() did not return after cancellation with no repo")
	}
}

func TestOutboundReleaseHandsRowBackToPump(t *testing.T) {
	fixture := openSpool(t)
	d := NewOutbound(fixture.repo, alwaysDelivered())

	got := outMsg("hello")
	if !d.Enqueue(&got) {
		t.Fatal("Enqueue() = false, want the row written")
	}
	if st := statsOf(t, fixture.repo); st.ClaimedOutbound != 1 {
		t.Fatalf("ClaimedOutbound = %d before Release, want 1", st.ClaimedOutbound)
	}

	if !d.Release(&got) {
		t.Error("Release() = false, want the claim undone")
	}
	st := statsOf(t, fixture.repo)
	if st.PendingOutbound != 1 || st.ClaimedOutbound != 0 {
		t.Errorf("spool = %+v, want the row back in the pending set", st)
	}

	if d.Release(&got) {
		t.Error("second Release() = true, want false: the row is no longer ours")
	}
	if d.Release(&bus.OutboundMessage{}) {
		t.Error("Release(SpoolID 0) = true, want false")
	}
}

func TestOutboundCompleteDeletesRow(t *testing.T) {
	fixture := openSpool(t)
	d := NewOutbound(fixture.repo, alwaysDelivered())

	got := outMsg("hello")
	if !d.Enqueue(&got) {
		t.Fatal("Enqueue() = false, want the row written")
	}
	if !d.Complete(&got) {
		t.Error("Complete() = false, want the row deleted")
	}

	st := statsOf(t, fixture.repo)
	if st.PendingOutbound+st.ClaimedOutbound != 0 {
		t.Errorf("spool = %+v, want the delivered row gone", st)
	}
	got.SpoolID = 9999
	if !d.Complete(&got) {
		t.Error("Complete(unknown id) = false, want true: the delete simply matched nothing")
	}
	if d.Complete(&bus.OutboundMessage{}) {
		t.Error("Complete(SpoolID 0) = true, want false")
	}
}

func TestOutboundForgetDeletesWithoutRetry(t *testing.T) {
	fixture := openSpool(t)
	d := NewOutbound(fixture.repo, alwaysDelivered())

	got := outMsg("half sent")
	if !d.Enqueue(&got) {
		t.Fatal("Enqueue() = false, want the row written")
	}
	if !d.Forget(&got, "partial send") {
		t.Error("Forget() = false, want the row deleted")
	}

	st := statsOf(t, fixture.repo)
	if st.PendingOutbound+st.ClaimedOutbound != 0 {
		t.Errorf("spool = %+v, want the dead-lettered row deleted", st)
	}
	if d.Forget(&bus.OutboundMessage{}, "reason") {
		t.Error("Forget(SpoolID 0) = true, want false")
	}
}

func TestOutboundDrainDeliversPending(t *testing.T) {
	fixture := openSpool(t)
	for _, content := range []string{"first", "second", "third"} {
		rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg(content)))
	}

	box := &outbox{}
	d := NewOutbound(fixture.repo, box.deliverer())

	n, err := d.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain() failed: %v", err)
	}
	if n != 3 {
		t.Errorf("Drain() = %d delivered, want 3", n)
	}

	got := box.delivered()
	if len(got) != 3 {
		t.Fatalf("deliverer called %d times, want 3", len(got))
	}
	for i, want := range []string{"first", "second", "third"} {
		if got[i].Content != want {
			t.Errorf("delivered[%d] = %q, want %q (FIFO)", i, got[i].Content, want)
		}
		if got[i].SpoolID == 0 {
			t.Errorf("delivered[%d].SpoolID = 0, want the row id", i)
		}
	}

	if st := statsOf(t, fixture.repo); st.PendingOutbound+st.ClaimedOutbound != 0 {
		t.Errorf("spool = %+v after a clean drain, want it empty", st)
	}
}

func TestOutboundDrainReclaimsStaleFirst(t *testing.T) {
	fixture := openSpool(t)
	id := rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("orphan")))
	staleClaim(t, fixture, id, "other-instance", StaleClaimTimeout+time.Minute)

	keep := rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("someone else's")))
	staleClaim(t, fixture, keep, "other-instance", time.Second)

	box := &outbox{}
	d := NewOutbound(fixture.repo, box.deliverer())

	n, err := d.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain() failed: %v", err)
	}
	if n != 1 {
		t.Errorf("Drain() = %d, want 1 (the orphaned row only)", n)
	}
	if got := box.delivered(); len(got) != 1 || got[0].Content != "orphan" {
		t.Errorf("delivered %+v, want just the stale row", got)
	}

	if row := peekRow(t, fixture, keep); row.Attempts != 0 {
		t.Errorf("fresh foreign claim attempts = %d, want 0: it must not be touched", row.Attempts)
	}
	owners := claimedBy(t, fixture.s)
	if len(owners) != 1 || owners[0] != "other-instance" {
		t.Errorf("claimed_by = %v, want only the untouched foreign owner", owners)
	}
}

func TestOutboundDeliverFailureReleases(t *testing.T) {
	fixture := openSpool(t)
	id := rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("retry me")))

	box := &outbox{script: func(bus.OutboundMessage) Delivery {
		return Delivery{Err: errors.New("channel offline")}
	}}
	d, clock := withFakeClock(fixture.repo, box.deliverer())

	n, err := d.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain() failed: %v", err)
	}
	if n != 0 {
		t.Errorf("Drain() = %d, want 0 delivered", n)
	}

	st := statsOf(t, fixture.repo)
	if st.PendingOutbound != 1 || st.ClaimedOutbound != 0 {
		t.Fatalf("spool = %+v, want the failed row released", st)
	}
	items, err := fixture.repo.PendingBySession(store.SpoolOutbound)
	if err != nil {
		t.Fatalf("PendingBySession() failed: %v", err)
	}
	if items["chat-1"] != 1 {
		t.Errorf("pending by session = %v, want chat-1:1", items)
	}

	var attempts int
	if err := fixture.s.DB().QueryRow(`SELECT attempts FROM spool WHERE direction = 'outbound'`).Scan(&attempts); err != nil {
		t.Fatalf("reading attempts failed: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}

	if wait := nextAttemptAt(t, fixture, id).Sub(clock.Now()); wait != outboundBackoff[0] {
		t.Errorf("retry delay = %v, want %v: a released row must not be re-claimed immediately", wait, outboundBackoff[0])
	}
}

func TestOutboundPartialChunkDeadLettersRetained(t *testing.T) {
	fixture := openSpool(t)
	rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("long answer")))

	box := &outbox{script: func(bus.OutboundMessage) Delivery {
		return Delivery{SentAnyChunk: true, Err: errors.New("chunk 2 of 3 failed")}
	}}
	d := NewOutbound(fixture.repo, box.deliverer())

	if _, err := d.Drain(context.Background()); err != nil {
		t.Fatalf("Drain() failed: %v", err)
	}

	st := statsOf(t, fixture.repo)
	if st.PendingOutbound+st.ClaimedOutbound != 0 {
		t.Errorf("spool = %+v, want the partial row out of the queue, NOT requeued", st)
	}
	if box.callCount() != 1 {
		t.Errorf("deliverer called %d times, want 1: a partial send must not be retried", box.callCount())
	}
	rows := deadLetters(t, fixture)
	if len(rows) != 1 {
		t.Fatalf("dead letters after a partial send = %d, want 1 (retained for inspection)", len(rows))
	}
	if rows[0].Reason != "chunk 2 of 3 failed" {
		t.Errorf("dead letter reason = %q, want the delivery error", rows[0].Reason)
	}
}

func TestOutboundAttemptsCapDeadLetters(t *testing.T) {
	fixture := openSpool(t)
	rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("undeliverable")))

	box := &outbox{script: func(bus.OutboundMessage) Delivery {
		return Delivery{Err: errors.New("chat botteado")}
	}}
	d, clock := withFakeClock(fixture.repo, box.deliverer())
	id := mustFirstID(t, fixture)

	for pass := 1; pass <= MaxOutboundAttempts; pass++ {
		if _, err := d.Drain(context.Background()); err != nil {
			t.Fatalf("Drain() pass %d failed: %v", pass, err)
		}
		st := statsOf(t, fixture.repo)
		if pass < MaxOutboundAttempts {
			if st.PendingOutbound != 1 {
				t.Fatalf("pass %d: spool = %+v, want the row requeued for the next attempt", pass, st)
			}
			advancedToNextAttempt(t, fixture, clock, id)
		}
	}

	if st := statsOf(t, fixture.repo); st.PendingOutbound+st.ClaimedOutbound != 0 {
		t.Errorf("after %d attempts the row survived (%+v), want it dead-lettered", MaxOutboundAttempts, st)
	}
	if box.callCount() != MaxOutboundAttempts {
		t.Errorf("deliverer called %d times, want exactly %d", box.callCount(), MaxOutboundAttempts)
	}
	if got := deadLetters(t, fixture); len(got) != 1 || got[0].ID != id {
		t.Errorf("dead letters = %+v, want the exhausted row %d retained", got, id)
	}
}

func TestOutboundAgeCapDropsStaleRows(t *testing.T) {
	fixture := openSpool(t)
	fresh := outMsg("still relevant")
	aged := outMsg("a reply from yesterday")
	agedID := agedOutEnqueue(t, fixture, outPayload(t, aged), MaxOutboundAge+time.Minute)
	rawOutEnqueue(t, fixture.repo, outPayload(t, fresh))

	box := &outbox{}
	d := NewOutbound(fixture.repo, box.deliverer())

	n, err := d.Drain(context.Background())
	if err != nil {
		t.Fatalf("Drain() failed: %v", err)
	}
	if n != 1 {
		t.Errorf("Drain() = %d, want 1 (only the fresh row delivered)", n)
	}

	got := box.delivered()
	if len(got) != 1 || got[0].Content != "still relevant" {
		t.Errorf("delivered %+v, want only the fresh message", got)
	}
	for _, row := range got {
		if row.Content == "a reply from yesterday" {
			t.Error("an expired row was delivered, want it dropped before the send")
		}
	}
	if st := statsOf(t, fixture.repo); st.PendingOutbound+st.ClaimedOutbound != 0 {
		t.Errorf("spool = %+v, want the expired row dropped from the queue", st)
	}
	if got := rowPayload(t, fixture, agedID); got != "" {
		t.Errorf("expired row %d survived in the queue with payload %q, want it dead-lettered", agedID, got)
	}
	rows := deadLetters(t, fixture)
	if len(rows) != 1 || rows[0].ID != agedID {
		t.Fatalf("dead letters = %+v, want the expired row %d retained", rows, agedID)
	}
	if rows[0].Reason != "expired" {
		t.Errorf("dead letter reason = %q, want %q", rows[0].Reason, "expired")
	}
}

func TestOutboundPoisonPayloadDies(t *testing.T) {
	fixture := openSpool(t)
	rawOutEnqueue(t, fixture.repo, "{not json")

	box := &outbox{}
	d := NewOutbound(fixture.repo, box.deliverer())

	for pass := 1; pass <= poisonLimit; pass++ {
		d.pumpOnce(context.Background())
		st := statsOf(t, fixture.repo)
		if pass < poisonLimit && st.PendingOutbound != 1 {
			t.Fatalf("pass %d: spool = %+v, want the poison row requeued for the next tick", pass, st)
		}
	}

	if st := statsOf(t, fixture.repo); st.PendingOutbound+st.ClaimedOutbound != 0 {
		t.Errorf("after %d ticks the poison row survived (%+v), want it dead-lettered", poisonLimit, st)
	}
	if box.callCount() != 0 {
		t.Errorf("deliverer called %d times, want 0 for an undecodable row", box.callCount())
	}
}

func TestOutboundNilDeliverDefers(t *testing.T) {
	fixture := openSpool(t)
	rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("hello")))

	d := NewOutbound(fixture.repo, nil)

	res, err := d.deliverOne(context.Background(), peekRow(t, fixture, mustFirstID(t, fixture)))
	if err != nil {
		t.Fatalf("deliverOne() failed: %v", err)
	}
	if res != outcomeDeferred {
		t.Errorf("deliverOne() = %v, want %v", res, outcomeDeferred)
	}

	st := statsOf(t, fixture.repo)
	if st.PendingOutbound != 1 || st.ClaimedOutbound != 0 {
		t.Errorf("spool = %+v, want the row released back to pending", st)
	}
	var attempts int
	if err := fixture.s.DB().QueryRow(`SELECT attempts FROM spool`).Scan(&attempts); err != nil {
		t.Fatalf("reading attempts failed: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}

func rowPayload(t *testing.T, fixture *spoolFixture, id int64) string {
	t.Helper()

	var payload string
	err := fixture.s.DB().QueryRow(`SELECT payload FROM spool WHERE id = ?`, id).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatalf("reading spool row %d failed: %v", id, err)
	}
	return payload
}

func mustFirstID(t *testing.T, fixture *spoolFixture) int64 {
	t.Helper()

	var id int64
	if err := fixture.s.DB().QueryRow(`SELECT id FROM spool WHERE direction = 'outbound' ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("reading the first outbound row failed: %v", err)
	}
	return id
}

func TestOutboundPumpSkipsWhenNothingPending(t *testing.T) {
	t.Run("empty spool", func(t *testing.T) {
		fixture := openSpool(t)
		box := &outbox{}
		NewOutbound(fixture.repo, box.deliverer()).pumpOnce(context.Background())

		if box.callCount() != 0 {
			t.Errorf("deliverer called %d times, want 0 on an idle spool", box.callCount())
		}
	})

	t.Run("only live claimed rows", func(t *testing.T) {
		fixture := openSpool(t)
		d := NewOutbound(fixture.repo, alwaysDelivered())
		live := outMsg("being sent right now")
		if !d.Enqueue(&live) {
			t.Fatal("Enqueue() = false, want the row written")
		}

		box := &outbox{}
		NewOutbound(fixture.repo, box.deliverer()).pumpOnce(context.Background())

		if box.callCount() != 0 {
			t.Errorf("deliverer called %d times, want 0: PendingOutbound is 0", box.callCount())
		}
		st := statsOf(t, fixture.repo)
		if st.ClaimedOutbound != 1 {
			t.Errorf("spool = %+v, want the live row left claimed and untouched", st)
		}
	})

	t.Run("only inbound work", func(t *testing.T) {
		fixture := openSpool(t)
		rawEnqueue(t, fixture.repo, "telegram", "m-1", payloadFor(t, msg("hi", "m-1")))

		box := &outbox{}
		NewOutbound(fixture.repo, box.deliverer()).pumpOnce(context.Background())

		if box.callCount() != 0 {
			t.Errorf("deliverer called %d times, want 0: the pending work is inbound", box.callCount())
		}
		if st := statsOf(t, fixture.repo); st.PendingInbound != 1 {
			t.Errorf("spool = %+v, want the inbound row left for the inbound pump", st)
		}
	})
}

func TestOutboundStartPumpDrainsInBackground(t *testing.T) {
	fixture := openSpool(t)
	rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("hi")))

	box := &outbox{}
	d := NewOutbound(fixture.repo, box.deliverer())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.StartPump(ctx); close(done) }()

	waitFor(t, 5*time.Second, func() bool { return box.callCount() == 1 },
		"the pump never delivered the pending row")
	if st := statsOf(t, fixture.repo); st.PendingOutbound != 0 {
		t.Errorf("PendingOutbound = %d, want 0 after the pump ran", st.PendingOutbound)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartPump() did not return after cancellation")
	}
}

func TestOutboundFlushPeerKicksPump(t *testing.T) {
	fixture := openSpool(t)
	box := &outbox{}
	d := NewOutbound(fixture.repo, box.deliverer())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { d.StartPump(ctx); close(done) }()

	rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("A")))
	waitFor(t, 3*time.Second, func() bool { return box.callCount() == 1 },
		"the pump tick never delivered row A")

	start := time.Now()
	rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("B")))
	d.FlushPeer("native", "peer-1")

	waitFor(t, OutboundPumpInterval/2, func() bool { return box.callCount() == 2 },
		"FlushPeer did not wake the pump for the reconnecting peer")
	if elapsed := time.Since(start); elapsed >= OutboundPumpInterval {
		t.Errorf("row B took %v to deliver, want under one tick (%v): the kick must be what ran",
			elapsed, OutboundPumpInterval)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartPump() did not return after cancellation")
	}
}

func TestOutboundKickBeforeStartIsNotLost(t *testing.T) {
	fixture := openSpool(t)
	rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("backlog")))

	box := &outbox{}
	d := NewOutbound(fixture.repo, box.deliverer())
	d.FlushPeer("native", "peer-1")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	start := time.Now()
	go func() { d.StartPump(ctx); close(done) }()

	waitFor(t, 100*time.Millisecond, func() bool { return box.callCount() == 1 },
		"the buffered kick did not wake the pump")
	if elapsed := time.Since(start); elapsed >= OutboundPumpInterval {
		t.Errorf("delivery took %v, want under one tick (%v)", elapsed, OutboundPumpInterval)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartPump() did not return after cancellation")
	}
}

func TestOutboundFlushPeerCoalescesAndNeverBlocks(t *testing.T) {
	fixture := openSpool(t)
	d := NewOutbound(fixture.repo, alwaysDelivered())

	for i := 0; i < 10; i++ {
		d.FlushPeer("native", fmt.Sprintf("peer-%d", i))
	}
	if n := len(d.kick); n != kickBuffer {
		t.Errorf("kick queue length = %d, want %d: extra wakes must be dropped", n, kickBuffer)
	}

	blocked := make(chan struct{})
	go func() { d.FlushPeer("native", "peer-late"); close(blocked) }()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("FlushPeer() blocked on a busy pump")
	}
}

func TestOutboundFlushPeerNilSafe(t *testing.T) {
	var d *Outbound
	d.FlushPeer("native", "peer-1")

	off := NewOutbound(nil, nil)
	off.FlushPeer("native", "peer-1")
	if n := len(off.kick); n != 1 {
		t.Errorf("kick queue length = %d, want 1: the wake-up is harmless with no repo", n)
	}
}

func TestOutboundReleaseClaimsOnShutdown(t *testing.T) {
	fixture := openSpool(t)
	d := NewOutbound(fixture.repo, alwaysDelivered())

	for _, content := range []string{"one", "two", "three"} {
		m := outMsg(content)
		if !d.Enqueue(&m) {
			t.Fatalf("Enqueue(%q) = false, want the row written", content)
		}
	}
	foreign := rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("foreign")))
	staleClaim(t, fixture, foreign, "other-instance", time.Second)

	released, err := d.ReleaseClaims()
	if err != nil {
		t.Fatalf("ReleaseClaims() failed: %v", err)
	}
	if released != 3 {
		t.Errorf("ReleaseClaims() = %d, want 3 (this instance's rows only)", released)
	}

	st := statsOf(t, fixture.repo)
	if st.ClaimedOutbound != 1 || st.PendingOutbound != 3 {
		t.Errorf("spool = %+v, want our 3 rows pending and the foreign row still claimed", st)
	}
	if owners := claimedBy(t, fixture.s); len(owners) != 1 || owners[0] != "other-instance" {
		t.Errorf("claimed_by = %v, want only %q", owners, "other-instance")
	}
}

func TestOutboundConcurrentEnqueueAndPump(t *testing.T) {
	fixture := openSpool(t)

	const total = 50
	var (
		mu       sync.Mutex
		sentOnce = map[string]int{}
		answers  atomic.Int64
	)
	deliver := func(_ context.Context, msg bus.OutboundMessage) Delivery {
		mu.Lock()
		sentOnce[msg.Content]++
		mu.Unlock()
		answers.Add(1)
		return Delivery{Delivered: true}
	}

	d := NewOutbound(fixture.repo, deliver)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.StartPump(ctx); close(done) }()

	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			content := fmt.Sprintf("reply-%02d", n)
			m := outMsg(content)
			if !d.Enqueue(&m) {
				t.Errorf("Enqueue(%q) = false, want the row written", content)
				return
			}
			if n%2 == 0 {
				d.Release(&m)
				return
			}
			d.Complete(&m)
		}(i)
	}
	wg.Wait()

	waitFor(t, 10*time.Second, func() bool {
		st, err := fixture.repo.Stats()
		return err == nil && st.PendingOutbound == 0 && st.ClaimedOutbound == 0
	}, "rows left in the spool after the pump caught up")

	if got := answers.Load(); got != total/2 {
		t.Errorf("deliverer called %d times, want %d (the released rows only)", got, total/2)
	}
	mu.Lock()
	defer mu.Unlock()
	for content, n := range sentOnce {
		if n != 1 {
			t.Errorf("%q delivered %d times, want exactly 1", content, n)
		}
	}
	if len(sentOnce) != total/2 {
		t.Errorf("distinct delivered rows = %d, want %d", len(sentOnce), total/2)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("StartPump() did not return after cancellation")
	}
}

func TestOutboundClaimPassDoesNotReleaseLiveRows(t *testing.T) {
	fixture := openSpool(t)
	d := NewOutbound(fixture.repo, alwaysDelivered())

	live := outMsg("in flight")
	if !d.Enqueue(&live) {
		t.Fatal("Enqueue() = false, want the live row written")
	}
	rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("pending")))

	box := &outbox{}
	replay := NewOutbound(fixture.repo, box.deliverer())
	if _, err := replay.Drain(context.Background()); err != nil {
		t.Fatalf("Drain() failed: %v", err)
	}

	if got := box.delivered(); len(got) != 1 || got[0].Content != "pending" {
		t.Errorf("delivered %+v, want only the pending row", got)
	}
	st := statsOf(t, fixture.repo)
	if st.ClaimedOutbound != 1 || st.PendingOutbound != 0 {
		t.Errorf("spool = %+v, want the live row still claimed by this instance", st)
	}
	if owners := claimedBy(t, fixture.s); len(owners) != 1 || owners[0] != d.InstanceID() {
		t.Errorf("claimed_by = %v, want only %q", owners, d.InstanceID())
	}
}

func TestOutboundPassErrorDoesNotStrandClaims(t *testing.T) {
	fixture := openSpool(t)
	for _, content := range []string{"m-1", "m-2", "m-3"} {
		rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg(content)))
	}

	blockSpoolDeletes(t, fixture.s)

	box := &outbox{}
	d := NewOutbound(fixture.repo, box.deliverer())
	if _, err := d.Drain(context.Background()); err == nil {
		t.Error("Drain() error = nil, want the store failure reported")
	}

	st := statsOf(t, fixture.repo)
	if st.ClaimedOutbound != 0 {
		t.Errorf("ClaimedOutbound = %d after a failed pass, want 0 (claims must be released)", st.ClaimedOutbound)
	}
	if st.PendingOutbound != 3 {
		t.Errorf("PendingOutbound = %d, want 3 (all rows back in the queue)", st.PendingOutbound)
	}

	allowSpoolDeletes(t, fixture.s)
	recovered := &outbox{}
	NewOutbound(fixture.repo, recovered.deliverer()).pumpOnce(context.Background())
	waitFor(t, 2*time.Second, func() bool { return recovered.callCount() == 3 },
		"the pump did not recover the stranded rows")
	if st := statsOf(t, fixture.repo); st.PendingOutbound != 0 {
		t.Errorf("PendingOutbound = %d after recovery, want 0", st.PendingOutbound)
	}
}

func TestOutboundDoesNotWriteProcessedLedger(t *testing.T) {
	fixture := openSpool(t)
	d := NewOutbound(fixture.repo, alwaysDelivered())

	live := outMsg("hello")
	if !d.Enqueue(&live) {
		t.Fatal("Enqueue() = false, want the row written")
	}
	d.Release(&live)
	d.Complete(&live)

	box := &outbox{script: func(m bus.OutboundMessage) Delivery {
		switch m.Content {
		case "partial":
			return Delivery{SentAnyChunk: true, Err: errors.New("chunk 2 failed")}
		case "undeliverable":
			return Delivery{Err: errors.New("channel offline")}
		default:
			return Delivery{Delivered: true}
		}
	}}
	replay := NewOutbound(fixture.repo, box.deliverer())
	clock := newFakeClock()
	replay.now = clock.Now

	rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("replayed")))
	rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("partial")))
	agedOutEnqueue(t, fixture, outPayload(t, outMsg("stale")), MaxOutboundAge+time.Minute)
	rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("undeliverable")))
	for pass := 0; pass < MaxOutboundAttempts; pass++ {
		if _, err := replay.Drain(context.Background()); err != nil {
			t.Fatalf("Drain() pass %d failed: %v", pass, err)
		}
		clock.Advance(3 * time.Minute)
	}
	rawOutEnqueue(t, fixture.repo, "{not json")
	for pass := 0; pass < poisonLimit; pass++ {
		replay.pumpOnce(context.Background())
	}

	if st := statsOf(t, fixture.repo); st.PendingOutbound+st.ClaimedOutbound != 0 {
		t.Fatalf("spool = %+v, want every outbound row settled", st)
	}
	if n := ledgerCount(t, fixture); n != 0 {
		t.Errorf("processed_messages holds %d rows after a full outbound lifecycle, want 0", n)
	}
}
