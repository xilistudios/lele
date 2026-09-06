// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

// Package durable keeps inbound chat messages across process restarts.
//
// It sits between a channel and the in-memory bus: every external inbound
// message is written to the SQLite spool (pkg/store.SpoolRepo) BEFORE it is
// published, so a crash or a self-restart loses nothing that was accepted
// from a channel. After a restart Drain replays the rows that never made it
// through the agent loop, and the processed_messages ledger turns the spool's
// at-least-once delivery into exactly-once processing.
//
// pkg/bus stays free of any storage dependency: the spool handle travels on
// bus.InboundMessage.SpoolID and the idempotency key on
// bus.InboundMessage.DedupeID, both owned by this package.
package durable

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/store"
)

// Tuning knobs for the inbound durability path. They are package constants
// rather than configuration because they describe the storage protocol, not
// user intent: changing one without changing both ends of the lifecycle (the
// writer here and the replay loop) would break the guarantees.
const (
	// StaleClaimTimeout is how long a claim may be orphaned - the holder
	// crashed without releasing - before Drain hands it back at startup.
	StaleClaimTimeout = 30 * time.Second
	// ClaimLimit bounds one claim pass, keeping a replay burst off the
	// single-connection pool in a handful of short transactions.
	ClaimLimit = 64
	// PumpInterval is how often the steady-state pump looks for rows that a
	// full bus left unpublished.
	PumpInterval = 250 * time.Millisecond
	// PruneInterval is how often the pump trims the dedupe ledger.
	PruneInterval = 10 * time.Minute
	// ProcessedRetention is the age beyond which a ledger entry is dropped.
	// The ledger only has to cover the replay window, so a week is generous.
	ProcessedRetention = 7 * 24 * time.Hour
)

// Internal limits.
const (
	// publishRetryBudget bounds how long one replayed message waits for room
	// in the bus buffer. The buffer holds 500 messages, so this only spins
	// during a boot storm; after it the row stays in the spool and the pump
	// retries it later.
	publishRetryBudget = 5 * time.Second
	// publishRetryDelay is the gap between republish attempts.
	publishRetryDelay = 25 * time.Millisecond
	// poisonLimit is the number of failed deliveries after which a row whose
	// payload cannot even be decoded is dead-lettered instead of retried
	// forever. No retry can fix bytes that are not JSON.
	poisonLimit = 5
	// poisonPreview caps how much of a broken payload is logged.
	poisonPreview = 200
	// idBytes is the entropy behind an instance id or a synthesized dedupe
	// key: 8 bytes, hex-encoded to 16 characters.
	idBytes = 8
	// logComponent tags every line this package emits.
	logComponent = "durable"
)

// Inbound persists external inbound messages to the spool and publishes them
// to the bus, replaying what a previous process left behind.
//
// The zero value is not usable; build one with NewInbound. A nil repository
// is legal and means "durability off": every method degrades to a plain
// publish, which keeps callers free of a feature-flag branch.
//
// One instance drives at most one replay pass at a time. Claims are owned by
// instance id, so two concurrent passes over the same instance would release
// each other's in-flight rows and double-publish them. The intended wiring
// upholds this on its own: Drain runs at startup before live consumption, and
// StartPump owns the only other pass, from a single goroutine.
type Inbound struct {
	repo       *store.SpoolRepo
	instanceID string
	publish    func(bus.InboundMessage) bool
}

// NewInbound builds the durability wrapper around repo, publishing through
// publish (typically MessageBus.PublishInbound). repo may be nil.
//
// instanceID identifies this process in the spool's claimed_by column so a
// restart can tell its own orphaned claims from another instance's.
func NewInbound(repo *store.SpoolRepo, publish func(bus.InboundMessage) bool) *Inbound {
	return &Inbound{
		repo:       repo,
		instanceID: "lele-" + randomHex(),
		publish:    publish,
	}
}

// InstanceID returns the id this instance claims spool rows under.
func (d *Inbound) InstanceID() string { return d.instanceID }

// Enqueue persists msg to the spool and sets msg.DedupeID and msg.SpoolID in
// place. It NEVER publishes: the caller keeps its own publish path so it can
// react to a full bus with its rollback hook (a channel that already showed a
// typing indicator must be able to undo it, which a blind publish-and-log
// cannot express here).
//
// It returns true only if the row was durably written. It returns false,
// leaving msg untouched, when durability is off (nil receiver or nil repo),
// when msg already carries a SpoolID (it is backed by a row already, so
// spooling it again would duplicate it), or when the write fails. A false
// answer is never fatal: it means "this message is not persisted", and the
// caller still publishes it, because dropping a live user message is worse
// than losing it on a crash that may never happen.
//
// msg must not be nil. DedupeID is assigned before the write so the key that
// lands in the row is exactly the key the consumer echoes back to Finish.
func (d *Inbound) Enqueue(msg *bus.InboundMessage) bool {
	// Durability off, or nothing to back.
	if d == nil || d.repo == nil || msg == nil {
		return false
	}

	// Already backed by a row: a replay, or a channel that re-publishes.
	// Idempotent, so a caller cannot double-spool by accident.
	if msg.SpoolID != 0 {
		return false
	}

	if msg.DedupeID == "" {
		msg.DedupeID = dedupeIDFor(*msg)
	}

	// SpoolID is tagged json:"-", so marshalling here cannot bake a stale row
	// id into the payload; the replay reads it back from the row it claimed.
	payload, err := json.Marshal(*msg)
	if err != nil {
		logger.WarnCF(logComponent, "Durable inbound marshal failed",
			map[string]interface{}{
				"channel": msg.Channel,
				"chat_id": msg.ChatID,
				"error":   err.Error(),
			})
		return false
	}

	id, err := d.repo.Enqueue(store.SpoolInbound, msg.Channel, msg.ChatID, msg.SessionKey, msg.DedupeID, string(payload))
	if err != nil {
		logger.WarnCF(logComponent, "Durable inbound enqueue failed",
			map[string]interface{}{
				"channel": msg.Channel,
				"chat_id": msg.ChatID,
				"error":   err.Error(),
			})
		return false
	}

	msg.SpoolID = id
	return true
}

// ShouldSkip reports whether msg was already fully processed, i.e. this is a
// duplicate delivered by a replay after a restart. An empty DedupeID means
// there is nothing to compare, so the answer is never "skip".
//
// A ledger read error answers "process it": losing a user message is worse
// than handling one twice, and the agent turn is idempotent enough.
func (d *Inbound) ShouldSkip(msg bus.InboundMessage) bool {
	if d.repo == nil || msg.DedupeID == "" {
		return false
	}

	done, err := d.repo.WasProcessed(msg.Channel, msg.DedupeID)
	if err != nil {
		logger.WarnCF(logComponent, "Dedupe ledger read failed; processing anyway",
			map[string]interface{}{
				"channel":   msg.Channel,
				"dedupe_id": msg.DedupeID,
				"error":     err.Error(),
			})
		return false
	}
	if done {
		logger.InfoCF(logComponent, "Skipping already-processed inbound",
			map[string]interface{}{
				"channel":   msg.Channel,
				"dedupe_id": msg.DedupeID,
				"spool_id":  msg.SpoolID,
			})
	}
	return done
}

// Finish records that msg was handled: it marks the dedupe ledger and drops
// the spool row. Both are best-effort - a failure is logged, never returned,
// because the message has already been answered and there is no caller left
// to hand an error to.
//
// The order matters. Marking the ledger first means a crash between the two
// writes leaves a row that the next replay skips; completing first would
// leave a message that is neither queued nor recorded, i.e. silently lost. So
// if the ledger write fails, the row is kept and the pump retries the pair.
//
// A message with SpoolID 0 but a DedupeID still reaches the ledger: that is
// the durability-flag-off path, where the point is to remember the id in case
// the flag is turned on before the next restart.
func (d *Inbound) Finish(msg bus.InboundMessage) {
	if d.repo == nil {
		return
	}
	if msg.DedupeID == "" && msg.SpoolID == 0 {
		return
	}

	if msg.DedupeID != "" {
		if err := d.repo.MarkProcessed(msg.Channel, msg.DedupeID); err != nil {
			logger.WarnCF(logComponent, "Dedupe ledger write failed; keeping spool row",
				map[string]interface{}{
					"channel":   msg.Channel,
					"dedupe_id": msg.DedupeID,
					"error":     err.Error(),
				})
			return
		}
	}

	if msg.SpoolID != 0 {
		if err := d.repo.Complete([]int64{msg.SpoolID}); err != nil {
			logger.WarnCF(logComponent, "Spool complete failed",
				map[string]interface{}{
					"channel":   msg.Channel,
					"dedupe_id": msg.DedupeID,
					"spool_id":  msg.SpoolID,
					"error":     err.Error(),
				})
		}
	}
}

// Drain replays the spool at startup, before live consumption begins: it
// first hands back claims orphaned by the previous process (older than
// StaleClaimTimeout), then re-publishes every pending inbound row, oldest
// first. It returns how many messages were republished.
//
// A row whose payload will not fit in the bus is left for the pump rather
// than blocking startup forever, and a row whose payload cannot be decoded is
// retried poisonLimit times and then dead-lettered, so Drain always returns.
func (d *Inbound) Drain(ctx context.Context) (int, error) {
	if d.repo == nil {
		return 0, nil
	}

	reclaimed, err := d.repo.ReclaimStale(StaleClaimTimeout, time.Now())
	if err != nil {
		// Not fatal: the rows this step would have freed stay claimed until
		// the next startup, and everything else is still replayable.
		logger.WarnCF(logComponent, "Stale spool reclaim failed; replaying pending rows only",
			map[string]interface{}{"error": err.Error()})
	} else if reclaimed > 0 {
		logger.InfoCF(logComponent, "Reclaimed stale spool claims",
			map[string]interface{}{"count": reclaimed})
	}

	total, err := d.claimPass(ctx)
	if err != nil {
		logger.WarnCF(logComponent, "Durable inbound drain aborted",
			map[string]interface{}{
				"republished": total.republished,
				"skipped":     total.skipped,
				"deferred":    total.deferred,
				"error":       err.Error(),
			})
	} else {
		logger.InfoCF(logComponent, "Durable inbound drain finished",
			map[string]interface{}{
				"republished": total.republished,
				"skipped":     total.skipped,
				"deferred":    total.deferred,
			})
	}
	return total.republished, err
}

// ReleaseClaims returns this instance's claimed rows to the pending set. The
// gateway calls it from a shutdown hook so a restarting successor re-drains
// immediately instead of waiting out StaleClaimTimeout.
func (d *Inbound) ReleaseClaims() (int, error) {
	if d.repo == nil {
		return 0, nil
	}
	return d.repo.ReleaseClaims(d.instanceID)
}

// StartPump keeps the spool drained while the gateway runs. Every
// PumpInterval it replays a batch when - and only when - Stats says there is
// pending inbound work, so an idle gateway does no database churn at all.
// Every PruneInterval it also trims ledger entries older than
// ProcessedRetention. It returns when ctx is done.
func (d *Inbound) StartPump(ctx context.Context) {
	if d.repo == nil {
		return
	}

	ticker := time.NewTicker(PumpInterval)
	defer ticker.Stop()
	nextPrune := time.Now().Add(PruneInterval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.pumpOnce(ctx)

			now := time.Now()
			if now.Before(nextPrune) {
				continue
			}
			nextPrune = now.Add(PruneInterval)
			pruned, err := d.repo.PruneProcessed(ProcessedRetention, now)
			if err != nil {
				logger.WarnCF(logComponent, "Dedupe ledger prune failed",
					map[string]interface{}{"error": err.Error()})
				continue
			}
			if pruned > 0 {
				logger.InfoCF(logComponent, "Pruned dedupe ledger",
					map[string]interface{}{"pruned": pruned})
			}
		}
	}
}

// Stats reports queue depth for observability.
func (d *Inbound) Stats() (store.SpoolStats, error) {
	if d.repo == nil {
		return store.SpoolStats{}, nil
	}
	return d.repo.Stats()
}

// pumpOnce runs one replay pass, guarded by the pending check that keeps an
// idle gateway out of the database.
func (d *Inbound) pumpOnce(ctx context.Context) {
	stats, err := d.repo.Stats()
	if err != nil {
		logger.WarnCF(logComponent, "Spool stats failed; skipping pump tick",
			map[string]interface{}{"error": err.Error()})
		return
	}
	if stats.PendingInbound == 0 {
		return
	}

	republished, err := d.claimPass(ctx)
	if err != nil {
		logger.WarnCF(logComponent, "Spool pump pass failed",
			map[string]interface{}{"error": err.Error()})
	}
	if republished.republished > 0 || republished.skipped > 0 {
		logger.InfoCF(logComponent, "Spool pump pass",
			map[string]interface{}{
				"republished": republished.republished,
				"skipped":     republished.skipped,
				"deferred":    republished.deferred,
			})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Replay
// ──────────────────────────────────────────────────────────────────────────────

// outcome describes what happened to one claimed row.
type outcome int

const (
	// outcomePublished means the message reached the bus and its row is gone.
	outcomePublished outcome = iota
	// outcomeSkipped means the row is done with but nothing was sent: it was
	// already processed, or it was dead-lettered as poison.
	outcomeSkipped
	// outcomeDeferred means the row is still in the spool because the bus
	// never had room for it.
	outcomeDeferred
)

// String keeps log lines and test failures readable.
func (o outcome) String() string {
	switch o {
	case outcomePublished:
		return "published"
	case outcomeSkipped:
		return "skipped"
	default:
		return "deferred"
	}
}

// passResult counts what one replay pass did.
type passResult struct {
	republished int
	skipped     int
	deferred    int
}

// claimPass claims pending inbound rows and republishes each one, looping
// until a pass claims nothing. It is the shared body of Drain and the pump
// tick.
//
// The loop stops at the first deferral: a row that did not fit means the bus
// is full, so claiming more would only pile up failures - the pump retries
// them on the next tick.
//
// The pass owns an invariant: when it returns, it holds no claims at all.
// Every row it claimed is either deleted (published, duplicate, dead-lettered)
// or handed back to the pending set by the deferred release below. That is what
// makes the pump's "nothing pending, nothing to do" check sound - a row left
// claimed would be invisible to every later pass on this instance, because
// ClaimBatch only selects unclaimed rows and pumpOnce only looks at the pending
// count.
func (d *Inbound) claimPass(ctx context.Context) (passResult, error) {
	var total passResult
	var batchErr error

	defer func() {
		handedBack, err := d.repo.ReleaseClaims(d.instanceID)
		if err != nil {
			logger.WarnCF(logComponent, "Spool claim release failed",
				map[string]interface{}{"error": err.Error()})
		} else if handedBack > 0 {
			logger.InfoCF(logComponent, "Returned unpublished spool rows to pending",
				map[string]interface{}{"count": handedBack})
		}
	}()

	for {
		if err := ctx.Err(); err != nil {
			batchErr = err
			break
		}

		items, err := d.repo.ClaimBatch(store.SpoolInbound, ClaimLimit, d.instanceID, time.Now())
		if err != nil {
			batchErr = fmt.Errorf("durable: claim inbound spool: %w", err)
			break
		}
		if len(items) == 0 {
			break
		}

		var batch passResult
		for _, item := range items {
			res, err := d.republish(ctx, item)
			switch res {
			case outcomePublished:
				batch.republished++
			case outcomeSkipped:
				batch.skipped++
			case outcomeDeferred:
				batch.deferred++
			}
			if err != nil {
				batchErr = err
				break
			}
		}

		total.republished += batch.republished
		total.skipped += batch.skipped
		total.deferred += batch.deferred
		if batchErr != nil || batch.deferred > 0 {
			break
		}
	}

	return total, batchErr
}

// republish handles one claimed row: decode it, drop it if it was already
// processed, otherwise publish it and complete the row. The error it returns
// is an unexpected database failure, which stops the pass; a full bus is not
// an error, it is outcomeDeferred.
func (d *Inbound) republish(ctx context.Context, item store.SpoolItem) (outcome, error) {
	var msg bus.InboundMessage
	if err := json.Unmarshal([]byte(item.Payload), &msg); err != nil {
		return d.handlePoison(item, err)
	}

	// The row owns the message's identity: the payload's SpoolID is always
	// zero (it is tagged json:"-"), and a payload written by an older build
	// may predate the DedupeID field, so msg_id is the fallback key.
	msg.SpoolID = item.ID
	if msg.DedupeID == "" {
		msg.DedupeID = item.MsgID
	}

	// Already handled before the crash: the ledger is the authority, so the
	// row is dropped instead of replayed a second time.
	if d.ShouldSkip(msg) {
		if err := d.repo.Complete([]int64{item.ID}); err != nil {
			return outcomeSkipped, fmt.Errorf("durable: complete spool row %d: %w", item.ID, err)
		}
		return outcomeSkipped, nil
	}

	if d.publishWithRetry(ctx, msg) {
		if err := d.repo.Complete([]int64{item.ID}); err != nil {
			return outcomePublished, fmt.Errorf("durable: complete spool row %d: %w", item.ID, err)
		}
		return outcomePublished, nil
	}

	// The bus stayed full for the whole budget. Record the attempt and leave
	// the row in place: it is the only copy of the message.
	if err := d.repo.IncAttempt(item.ID); err != nil {
		return outcomeDeferred, fmt.Errorf("durable: count spool attempt %d: %w", item.ID, err)
	}
	logger.WarnCF(logComponent, "Spool replay blocked by a full bus; deferring to the pump",
		map[string]interface{}{
			"channel":  msg.Channel,
			"chat_id":  msg.ChatID,
			"spool_id": item.ID,
		})
	return outcomeDeferred, nil
}

// handlePoison deals with a row whose payload is not a decodable message. No
// retry can fix bytes that are not JSON, so the row is counted and, once it
// reaches poisonLimit attempts, dead-lettered: logged with a payload preview
// and then deleted. Without the ceiling a single corrupt row would be replayed
// on every startup forever.
func (d *Inbound) handlePoison(item store.SpoolItem, decodeErr error) (outcome, error) {
	if err := d.repo.IncAttempt(item.ID); err != nil {
		return outcomeSkipped, fmt.Errorf("durable: count poison attempt %d: %w", item.ID, err)
	}
	attempts := item.Attempts + 1
	if attempts < poisonLimit {
		logger.WarnCF(logComponent, "Undecodable spool payload; retrying",
			map[string]interface{}{
				"spool_id": item.ID,
				"attempts": attempts,
				"error":    decodeErr.Error(),
			})
		return outcomeSkipped, nil
	}

	logger.WarnCF(logComponent, "Dead-lettering poison spool row",
		map[string]interface{}{
			"spool_id": item.ID,
			"channel":  item.Channel,
			"attempts": attempts,
			"payload":  truncate(item.Payload, poisonPreview),
		})
	if err := d.repo.Complete([]int64{item.ID}); err != nil {
		return outcomeSkipped, fmt.Errorf("durable: complete poison row %d: %w", item.ID, err)
	}
	return outcomeSkipped, nil
}

// publishWithRetry keeps trying the non-blocking publish until the budget runs
// out or ctx ends. At boot the consumer may not be reading yet and the buffer
// may hold a backlog, so a single false says nothing about whether there will
// be room a moment later.
func (d *Inbound) publishWithRetry(ctx context.Context, msg bus.InboundMessage) bool {
	deadline := time.Now().Add(publishRetryBudget)
	for {
		if d.push(msg) {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		wait := publishRetryDelay
		if remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

// push is the one place the publish function is called, so a missing or
// mis-wired publisher cannot panic.
func (d *Inbound) push(msg bus.InboundMessage) bool {
	if d.publish == nil {
		logger.WarnCF(logComponent, "No publisher wired; inbound dropped",
			map[string]interface{}{
				"channel":   msg.Channel,
				"chat_id":   msg.ChatID,
				"dedupe_id": msg.DedupeID,
			})
		return false
	}
	return d.publish(msg)
}

// ──────────────────────────────────────────────────────────────────────────────
// Identity helpers
// ──────────────────────────────────────────────────────────────────────────────

// dedupeIDFor picks the idempotency key for msg: the channel's own message id
// when it supplied one, otherwise a fresh random id. Metadata is never
// written to - the caller's map stays exactly as the channel built it.
func dedupeIDFor(msg bus.InboundMessage) string {
	if id := msg.Metadata["message_id"]; id != "" {
		return id
	}
	return randomHex()
}

// randomHex returns idBytes random bytes hex-encoded. It backs the instance
// id and synthesized dedupe keys, where a collision would cost a message, so
// it comes from crypto/rand. If the system source ever fails, a nanosecond
// timestamp keeps ids unique without pulling in a dependency.
func randomHex() string {
	buf := make([]byte, idBytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// truncate keeps at most n bytes of s, marking that it cut. Payloads can be
// arbitrarily long; log lines must not.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
