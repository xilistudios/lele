package durable

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/store"
)

type fakeClock struct {
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Now().UTC()}
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func (c *fakeClock) Set(t time.Time) { c.now = t }

func withFakeClock(repo *store.SpoolRepo, deliver DeliverFunc) (*Outbound, *fakeClock) {
	clock := newFakeClock()
	d := NewOutbound(repo, deliver)
	d.now = clock.Now
	return d, clock
}

func nextAttemptAt(t *testing.T, fixture *spoolFixture, id int64) time.Time {
	t.Helper()

	var stamp string
	if err := fixture.s.DB().QueryRow(`SELECT next_attempt_at FROM spool WHERE id = ?`, id).Scan(&stamp); err != nil {
		t.Fatalf("read next_attempt_at for row %d failed: %v", id, err)
	}
	if stamp == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		t.Fatalf("next_attempt_at %q does not parse: %v", stamp, err)
	}
	return parsed
}

func advancedToNextAttempt(t *testing.T, fixture *spoolFixture, clock *fakeClock, id int64) {
	t.Helper()

	next := nextAttemptAt(t, fixture, id)
	if next.IsZero() {
		t.Fatalf("row %d was not deferred; next_attempt_at is empty", id)
	}
	if !next.After(clock.Now()) {
		t.Fatalf("next_attempt_at %v is not in the future of the fake clock %v", next, clock.Now())
	}
	clock.Set(next)
}

func spoolRowCount(t *testing.T, fixture *spoolFixture) int {
	t.Helper()

	var n int
	if err := fixture.s.DB().QueryRow(`SELECT COUNT(*) FROM spool`).Scan(&n); err != nil {
		t.Fatalf("count spool rows failed: %v", err)
	}
	return n
}

type deadLetterRow struct {
	ID       int64
	Channel  string
	ChatID   string
	Payload  string
	Attempts int
	Reason   string
}

func deadLetters(t *testing.T, fixture *spoolFixture) []deadLetterRow {
	t.Helper()

	rows, err := fixture.s.DB().Query(
		`SELECT id, channel, chat_id, payload, attempts, reason FROM spool_dead_letter ORDER BY id`)
	if err != nil {
		t.Fatalf("read spool_dead_letter failed: %v", err)
	}
	defer rows.Close()

	out := []deadLetterRow{}
	for rows.Next() {
		var row deadLetterRow
		if err := rows.Scan(&row.ID, &row.Channel, &row.ChatID, &row.Payload, &row.Attempts, &row.Reason); err != nil {
			t.Fatalf("scan spool_dead_letter failed: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read spool_dead_letter rows failed: %v", err)
	}
	return out
}

func TestOutboundBackoffDefersRetry(t *testing.T) {
	fixture := openSpool(t)
	id := rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("retry me")))

	box := &outbox{script: func(bus.OutboundMessage) Delivery {
		return Delivery{Err: errors.New("channel offline")}
	}}
	d, clock := withFakeClock(fixture.repo, box.deliverer())

	if _, err := d.Drain(context.Background()); err != nil {
		t.Fatalf("Drain() failed: %v", err)
	}
	if box.callCount() != 1 {
		t.Fatalf("deliverer called %d times, want 1", box.callCount())
	}

	wait := nextAttemptAt(t, fixture, id).Sub(clock.Now())
	if wait != outboundBackoff[0] {
		t.Fatalf("first retry delay = %v, want %v", wait, outboundBackoff[0])
	}

	if _, err := d.Drain(context.Background()); err != nil {
		t.Fatalf("Drain() before the backoff elapsed failed: %v", err)
	}
	if box.callCount() != 1 {
		t.Fatalf("deliverer called %d times before the backoff elapsed, want 1", box.callCount())
	}

	box.script = nil
	clock.Advance(wait)
	if _, err := d.Drain(context.Background()); err != nil {
		t.Fatalf("Drain() after the backoff elapsed failed: %v", err)
	}
	if box.callCount() != 2 {
		t.Fatalf("deliverer called %d times after the backoff elapsed, want 2", box.callCount())
	}
	if got := spoolRowCount(t, fixture); got != 0 {
		t.Errorf("spool rows after the retry succeeded = %d, want 0", got)
	}
}

func TestOutboundHonorsRetryAfter(t *testing.T) {
	fixture := openSpool(t)
	id := rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("slow down")))

	box := &outbox{script: func(bus.OutboundMessage) Delivery {
		return Delivery{Err: errors.New("429 too many requests"), RetryAfter: 30 * time.Second}
	}}
	d, clock := withFakeClock(fixture.repo, box.deliverer())

	if _, err := d.Drain(context.Background()); err != nil {
		t.Fatalf("Drain() failed: %v", err)
	}

	wait := nextAttemptAt(t, fixture, id).Sub(clock.Now())
	if wait != 30*time.Second {
		t.Fatalf("retry delay = %v, want the channel's retry_after %v", wait, 30*time.Second)
	}

	clock.Advance(29 * time.Second)
	if _, err := d.Drain(context.Background()); err != nil {
		t.Fatalf("Drain() inside retry_after failed: %v", err)
	}
	if box.callCount() != 1 {
		t.Fatalf("deliverer called %d times inside retry_after, want 1", box.callCount())
	}

	box.script = nil
	clock.Advance(time.Second)
	if _, err := d.Drain(context.Background()); err != nil {
		t.Fatalf("Drain() after retry_after failed: %v", err)
	}
	if box.callCount() != 2 {
		t.Errorf("deliverer called %d times after retry_after, want 2", box.callCount())
	}
}

func TestOutboundAttemptsSpanMinutes(t *testing.T) {
	fixture := openSpool(t)
	id := rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("undeliverable")))

	box := &outbox{script: func(bus.OutboundMessage) Delivery {
		return Delivery{Err: errors.New("chat botteado")}
	}}
	d, clock := withFakeClock(fixture.repo, box.deliverer())
	start := clock.Now()

	for pass := 1; pass <= MaxOutboundAttempts; pass++ {
		if _, err := d.Drain(context.Background()); err != nil {
			t.Fatalf("Drain() pass %d failed: %v", pass, err)
		}
		if pass < MaxOutboundAttempts {
			advancedToNextAttempt(t, fixture, clock, id)
		}
	}

	if box.callCount() != MaxOutboundAttempts {
		t.Fatalf("deliverer called %d times, want exactly %d", box.callCount(), MaxOutboundAttempts)
	}
	span := clock.Now().Sub(start)
	if span < 2*time.Minute {
		t.Errorf("retries spanned %v, want at least 2 minutes of spread instead of a burst", span)
	}
	if got := spoolRowCount(t, fixture); got != 0 {
		t.Errorf("spool rows after exhaustion = %d, want 0 (moved to the dead letter)", got)
	}
	if got := deadLetters(t, fixture); len(got) != 1 {
		t.Errorf("dead letters after exhaustion = %d, want 1", len(got))
	}
}

func TestOutboundDeadLetterRetained(t *testing.T) {
	fixture := openSpool(t)
	payload := outPayload(t, outMsg("never delivered"))
	id := rawOutEnqueue(t, fixture.repo, payload)

	box := &outbox{script: func(bus.OutboundMessage) Delivery {
		return Delivery{Err: errors.New("chat deleted")}
	}}
	d, clock := withFakeClock(fixture.repo, box.deliverer())

	for pass := 1; pass <= MaxOutboundAttempts; pass++ {
		if _, err := d.Drain(context.Background()); err != nil {
			t.Fatalf("Drain() pass %d failed: %v", pass, err)
		}
		if pass < MaxOutboundAttempts {
			advancedToNextAttempt(t, fixture, clock, id)
		}
	}

	var payloadLeft string
	err := fixture.s.DB().QueryRow(`SELECT payload FROM spool WHERE id = ?`, id).Scan(&payloadLeft)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("spool still holds row %d (%q, err=%v), want it dead-lettered", id, payloadLeft, err)
	}

	rows := deadLetters(t, fixture)
	if len(rows) != 1 {
		t.Fatalf("dead letters = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.ID != id {
		t.Errorf("dead letter id = %d, want the spool id %d", row.ID, id)
	}
	if row.Payload != payload {
		t.Errorf("dead letter payload = %q, want the fenced payload %q", row.Payload, payload)
	}
	if row.Attempts != MaxOutboundAttempts {
		t.Errorf("dead letter attempts = %d, want %d", row.Attempts, MaxOutboundAttempts)
	}
	if row.Reason != "chat deleted" {
		t.Errorf("dead letter reason = %q, want the delivery error", row.Reason)
	}
	if row.Channel != "telegram" || row.ChatID != "chat-1" {
		t.Errorf("dead letter routing = %s/%s, want telegram/chat-1", row.Channel, row.ChatID)
	}

	now := clock.Now()
	if n, err := fixture.repo.PruneDeadLetter(ProcessedRetention, now.Add(-time.Hour)); err != nil || n != 0 {
		t.Fatalf("PruneDeadLetter(fresh cutoff) = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := fixture.repo.PruneDeadLetter(ProcessedRetention, now.Add(ProcessedRetention+time.Minute)); err != nil || n != 1 {
		t.Fatalf("PruneDeadLetter(aged cutoff) = (%d, %v), want (1, nil)", n, err)
	}
	if got := deadLetters(t, fixture); len(got) != 0 {
		t.Errorf("dead letters after the aged prune = %d, want 0", len(got))
	}
}

func TestOutboundDeadLetterPruneKeepsRecentRows(t *testing.T) {
	fixture := openSpool(t)
	clock := newFakeClock()
	id := rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("never delivered")))
	if err := fixture.repo.DeadLetter([]int64{id}, "chat deleted", clock.Now()); err != nil {
		t.Fatalf("DeadLetter() failed: %v", err)
	}

	pruneDeadLetter(fixture.repo, clock.Now())
	if got := len(deadLetters(t, fixture)); got != 1 {
		t.Fatalf("dead letters after pruning inside the %v retention = %d, want 1", ProcessedRetention, got)
	}

	pruneDeadLetter(fixture.repo, clock.Now().Add(ProcessedRetention+time.Minute))
	if got := len(deadLetters(t, fixture)); got != 0 {
		t.Fatalf("dead letters after pruning past the %v retention = %d, want 0", ProcessedRetention, got)
	}

	pruneDeadLetter(fixture.repo, clock.Now().Add(ProcessedRetention+time.Minute))
	if got := len(deadLetters(t, fixture)); got != 0 {
		t.Fatalf("dead letters after a repeated prune = %d, want 0: the prune is idempotent", got)
	}
}

func TestOutboundPumpPrunesDeadLetterWithoutInbound(t *testing.T) {
	fixture := openSpool(t)
	clock := newFakeClock()
	d := NewOutbound(fixture.repo, alwaysDelivered())
	d.now = clock.Now

	id := rawOutEnqueue(t, fixture.repo, outPayload(t, outMsg("never delivered")))
	if err := fixture.repo.DeadLetter([]int64{id}, "chat deleted", clock.Now().Add(-ProcessedRetention-time.Hour)); err != nil {
		t.Fatalf("DeadLetter() failed: %v", err)
	}
	if got := len(deadLetters(t, fixture)); got != 1 {
		t.Fatalf("dead letters before the pump started = %d, want 1", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.StartPump(ctx)

	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if len(deadLetters(t, fixture)) == 0 {
			return
		}
	}
	t.Fatalf("dead letters after the outbound pump ran = %d, want 0: the outbound-only configuration must prune too", len(deadLetters(t, fixture)))
}

func TestOutboundBackoffCoversEveryAttemptGap(t *testing.T) {
	if got := len(outboundBackoff); got != MaxOutboundAttempts-1 {
		t.Fatalf("outboundBackoff has %d entries, want %d: the last attempt dead-letters instead of deferring, so an extra entry is unreachable",
			got, MaxOutboundAttempts-1)
	}

	for attempts := 0; attempts < len(outboundBackoff); attempts++ {
		if got, want := outboundBackoffFor(attempts, 0), outboundBackoff[attempts]; got != want {
			t.Errorf("outboundBackoffFor(%d, 0) = %v, want %v", attempts, got, want)
		}
	}

	last := outboundBackoff[len(outboundBackoff)-1]
	if got := outboundBackoffFor(len(outboundBackoff), 0); got != last {
		t.Errorf("outboundBackoffFor(past the schedule, 0) = %v, want the last step %v", got, last)
	}
	if got := outboundBackoffFor(0, time.Hour); got != time.Hour {
		t.Errorf("outboundBackoffFor(0, 1h) = %v, want the channel's retry_after 1h", got)
	}
}
