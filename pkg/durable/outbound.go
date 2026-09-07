// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

// This file is the outbound half of package durable (the package doc lives on
// inbound.go): it keeps agent replies across process restarts.
//
// It is the mirror image of inbound.go and sits on the bus instead of in
// front of it: every reply the gateway is about to send is written to the
// SQLite spool (pkg/store.SpoolRepo) BEFORE it is published. After a restart,
// Drain and the steady pump replay the rows that never reached a channel.
//
// What that protects, precisely: a real content message the gateway accepted
// is never lost to a restart, a full bus, a full per-channel queue, or a
// transient send failure.
//
// What it does NOT protect: atomicity inside a multi-chunk message. Once the
// first chunk is on the wire, a failure cannot be replayed without re-sending
// it, so a failure after chunk k>=1 dead-letters the rest rather than
// re-sending chunks 1..k. A single-chunk message - the overwhelming majority -
// has no such window.
//
// The accepted tradeoff is at-least-once delivery. The only duplicate window
// is a crash between the last successful send and the row delete (sub-ms);
// there is no outbound dedupe key, because the agent may legitimately send
// identical text twice and Channel.Send returns no channel-side id to compare.
// A row's age ceiling (MaxOutboundAge) bounds the worst case to "a restart
// within the hour".
//
// pkg/bus stays free of any storage dependency: the spool handle travels on
// bus.OutboundMessage.SpoolID, owned by this package. pkg/durable in turn
// never imports pkg/channels - delivery arrives as a DeliverFunc closure that
// the gateway wires to the channel manager, exactly as the inbound side keeps
// the transport layer out of storage.

package durable

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/constants"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/store"
)

// Tuning knobs for the outbound durability path. Like the inbound ones they
// describe the storage protocol rather than user intent, so they are package
// constants: the writer here and the replay loop have to agree on them.
// StaleClaimTimeout, ClaimLimit, poisonLimit and poisonPreview are shared
// with the inbound path and deliberately not duplicated.
const (
	// OutboundPumpInterval is how often the steady pump looks for rows a
	// full bus or a failed send left behind. Same rhythm as the inbound pump.
	OutboundPumpInterval = 250 * time.Millisecond
	// MaxOutboundAttempts caps delivery attempts for a row whose send keeps
	// failing. After it the message is dead-lettered instead of retried
	// forever: a chat that is gone does not come back on attempt six.
	MaxOutboundAttempts = 5
	// MaxOutboundAge is how old a pending row may be before it is dropped
	// unplayed. A reply nobody could deliver for an hour is no longer worth
	// sending - and the ceiling bounds the at-least-once window above.
	MaxOutboundAge = time.Hour
	// kickBuffer is the depth of the FlushPeer signal: one pending wake-up is
	// as good as a hundred, because every pass is a full FIFO sweep.
	kickBuffer = 1
)

// Delivery is what one replay attempt got done. pkg/channels has its own
// three-field twin (SendOutcome); the duplication is deliberate - they are the
// vocabularies of two layers, and keeping them apart is what stops durable
// from ever needing to know about chunking.
type Delivery struct {
	// Delivered means every chunk reached the channel: Complete the row.
	Delivered bool
	// SentAnyChunk means at least one chunk went out: a failure is PARTIAL,
	// so Forget the row - never Release, which would re-send what went out.
	SentAnyChunk bool
	Err          error
}

// DeliverFunc sends one replayed message. It is supplied by the gateway and
// wraps the same send path live traffic uses, so replay cannot diverge from
// the normal publish.
type DeliverFunc func(ctx context.Context, msg bus.OutboundMessage) Delivery

// Outbound persists agent replies to the spool and replays the ones that
// never made it to a channel.
//
// The zero value is not usable; build one with NewOutbound. A nil repository
// is legal and means "durability off": every method degrades to a no-op that
// reports false, which keeps callers free of a feature-flag branch.
//
// One instance drives at most one replay pass at a time. Claims are owned by
// instance id, so two concurrent passes over the same instance would release
// each other's in-flight rows and double-send them. The intended wiring
// upholds this on its own: Drain runs at startup before live consumption, and
// StartPump owns the only other pass, from a single goroutine.
type Outbound struct {
	repo       *store.SpoolRepo
	deliver    DeliverFunc
	instanceID string
	// kick wakes the pump early, the moment a peer reconnects. Buffered at
	// kickBuffer so the sender never blocks on a busy pump.
	kick chan struct{}
}

// NewOutbound builds the durability wrapper around repo, delivering replayed
// messages through deliver. repo may be nil.
//
// instanceID identifies this process in the spool's claimed_by column so a
// restart can tell its own orphaned claims from another instance's.
func NewOutbound(repo *store.SpoolRepo, deliver DeliverFunc) *Outbound {
	return &Outbound{
		repo:       repo,
		deliver:    deliver,
		instanceID: "lele-" + randomHex(),
		kick:       make(chan struct{}, kickBuffer),
	}
}

// InstanceID returns the id this instance claims spool rows under.
func (d *Outbound) InstanceID() string {
	if d == nil {
		return ""
	}
	return d.instanceID
}

// ShouldSpoolOutbound reports whether msg is worth a durable row: real content
// addressed to a peer that can be found again. It is the single eligibility
// authority - the bus hook calls it through Enqueue and never filters
// anything itself, so the rule lives in exactly one place.
//
// Excluded, and why: events and streaming intermediates (they are signals, not
// messages, and replaying them would fake a turn boundary); empty content with
// no attachments (nothing to say); a missing ChatID (a broadcast has no peer
// for the pump to resend to, so its row could never complete); internal
// channels (cli/system/subagent never leave the process); and messages already
// backed by a row, which would be spooled twice.
func ShouldSpoolOutbound(msg bus.OutboundMessage) bool {
	if msg.SpoolID != 0 || msg.Event != "" || msg.IsIntermediate {
		return false
	}
	if msg.ChatID == "" {
		return false
	}
	if strings.TrimSpace(msg.Content) == "" && len(msg.Attachments) == 0 {
		return false
	}
	return !constants.IsInternalChannel(msg.Channel)
}

// Enqueue persists msg to the spool and tags msg.SpoolID in place. It NEVER
// publishes: MessageBus.PublishOutbound keeps its own handoff to the outbound
// queue, so a bus that refuses the message can roll the claim back with
// Release.
//
// The row is BORN CLAIMED by this instance (claimed_by = instanceID), which is
// what keeps a live message in flight: ClaimBatch only selects rows whose
// claimed_by is empty, so the steady pump cannot see this row and cannot
// double-send something the dispatcher is already delivering. The row leaves
// the claimed state in exactly three ways - Release hands it back when the bus
// or the send refused it, Complete deletes it once every chunk is out, and
// Forget deletes it as a dead letter.
//
// It returns true only if the row was durably written. It returns false,
// leaving msg untouched, when durability is off (nil receiver or nil repo),
// when msg is not eligible (see ShouldSpoolOutbound), when msg already carries
// a SpoolID, or when the write fails. A false answer is never fatal: it means
// "this reply is not persisted", and the caller still publishes it, because
// withholding a live answer is worse than losing it on a crash that may never
// happen.
func (d *Outbound) Enqueue(msg *bus.OutboundMessage) bool {
	// Durability off, or nothing to back.
	if d == nil || d.repo == nil || msg == nil {
		return false
	}

	// Already backed by a row: a replay, or a producer that re-publishes.
	// Idempotent, so a caller cannot double-spool by accident.
	if msg.SpoolID != 0 {
		return false
	}

	// Ineligible messages are the common case (streaming chunks, events), so
	// this guard stays silent: logging here would spam the log every turn.
	if !ShouldSpoolOutbound(*msg) {
		return false
	}

	// SpoolID is tagged json:"-", so marshalling here cannot bake a stale row
	// id into the payload; the replay re-attaches it from the row it claimed.
	payload, err := json.Marshal(*msg)
	if err != nil {
		logger.WarnCF(logComponent, "Durable outbound marshal failed",
			map[string]interface{}{
				"channel": msg.Channel,
				"chat_id": msg.ChatID,
				"error":   err.Error(),
			})
		return false
	}

	// In outbound the chat id IS the peer key: native resolves the session
	// from it and telegram/whatsapp use it as the chat. msg_id stays empty -
	// there is no dedupe key to record (see the package doc).
	id, err := d.repo.EnqueueClaimed(store.SpoolOutbound, msg.Channel, msg.ChatID, msg.ChatID, "", string(payload), d.instanceID, time.Now())
	if err != nil {
		logger.WarnCF(logComponent, "Durable outbound enqueue failed",
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

// Release hands a single in-flight row back to the pending set so the pump can
// retry it. The bus calls it when it could not accept a message it had already
// spooled: the row was born claimed by this instance (see Enqueue), and without
// this it would sit claimed until StaleClaimTimeout even though the process is
// alive and simply needs to try again later. Best-effort: a nil receiver, a nil
// repo, a message with no SpoolID, or a database error all return false and
// change nothing.
func (d *Outbound) Release(msg *bus.OutboundMessage) bool {
	if d == nil || d.repo == nil || msg == nil || msg.SpoolID == 0 {
		return false
	}
	released, err := d.repo.ReleaseIDs([]int64{msg.SpoolID}, d.instanceID)
	if err != nil {
		logger.WarnCF(logComponent, "Durable outbound release failed",
			map[string]interface{}{
				"channel":  msg.Channel,
				"spool_id": msg.SpoolID,
				"error":    err.Error(),
			})
		return false
	}
	return released > 0
}

// Complete deletes the row behind msg: every chunk reached the channel, so the
// spool no longer has to carry it. Both ends call it (the live dispatcher and
// the replay pass), and it is safe to call on a row that is already gone.
//
// A failed delete is logged, not returned: the message has already been sent,
// and the only consequence is that ReclaimStale frees the row later, costing
// one duplicate - the accepted tradeoff in the package doc.
func (d *Outbound) Complete(msg *bus.OutboundMessage) bool {
	if d == nil || d.repo == nil || msg == nil || msg.SpoolID == 0 {
		return false
	}
	if err := d.repo.Complete([]int64{msg.SpoolID}); err != nil {
		logger.WarnCF(logComponent, "Durable outbound complete failed",
			map[string]interface{}{
				"channel":  msg.Channel,
				"chat_id":  msg.ChatID,
				"spool_id": msg.SpoolID,
				"error":    err.Error(),
			})
		return false
	}
	return true
}

// Forget deletes the row behind msg WITHOUT retrying it and logs the
// dead letter. It is the answer to a partial send: chunks already on the wire
// cannot be un-sent, so replaying the remainder would duplicate them, and the
// honest move is to drop the row with a loud log line.
func (d *Outbound) Forget(msg *bus.OutboundMessage, reason string) bool {
	if d == nil || d.repo == nil || msg == nil || msg.SpoolID == 0 {
		return false
	}
	if err := d.deadLetter(msg, reason); err != nil {
		return false
	}
	return true
}

// Drain replays the spool at startup, before live sending begins: it first
// hands back claims orphaned by the previous process (older than
// StaleClaimTimeout), then delivers every pending outbound row, oldest first.
// It returns how many messages were delivered.
//
// A row whose send keeps failing is left for the pump rather than blocking
// startup, and a row whose payload cannot be decoded is retried poisonLimit
// times and then dead-lettered, so Drain always returns.
//
// Run it while the live dispatcher is NOT yet consuming: pump and dispatcher
// share this instance id, and ReclaimStale here could otherwise free a row the
// dispatcher is sending right now.
func (d *Outbound) Drain(ctx context.Context) (int, error) {
	if d == nil || d.repo == nil {
		return 0, nil
	}

	reclaimed, err := d.repo.ReclaimStale(StaleClaimTimeout, time.Now())
	if err != nil {
		// Not fatal: the rows this step would have freed stay claimed until
		// the next startup, and everything else is still replayable.
		logger.WarnCF(logComponent, "Stale outbound spool reclaim failed; replaying pending rows only",
			map[string]interface{}{"error": err.Error()})
	} else if reclaimed > 0 {
		logger.InfoCF(logComponent, "Reclaimed stale outbound spool claims",
			map[string]interface{}{"count": reclaimed})
	}

	total, err := d.claimPass(ctx)
	if err != nil {
		logger.WarnCF(logComponent, "Durable outbound drain aborted",
			map[string]interface{}{
				"delivered": total.republished,
				"skipped":   total.skipped,
				"deferred":  total.deferred,
				"error":     err.Error(),
			})
	} else {
		logger.InfoCF(logComponent, "Durable outbound drain finished",
			map[string]interface{}{
				"delivered": total.republished,
				"skipped":   total.skipped,
				"deferred":  total.deferred,
			})
	}
	return total.republished, err
}

// ReleaseClaims returns this instance's claimed rows to the pending set. The
// gateway calls it from a shutdown hook so a restarting successor re-drains
// immediately instead of waiting out StaleClaimTimeout.
func (d *Outbound) ReleaseClaims() (int, error) {
	if d == nil || d.repo == nil {
		return 0, nil
	}
	return d.repo.ReleaseClaims(d.instanceID)
}

// Stats reports queue depth for observability.
func (d *Outbound) Stats() (store.SpoolStats, error) {
	if d == nil || d.repo == nil {
		return store.SpoolStats{}, nil
	}
	return d.repo.Stats()
}

// StartPump keeps the spool drained while the gateway runs. Every
// OutboundPumpInterval - and every time FlushPeer kicks it - it replays a
// batch when, and only when, Stats says there is pending outbound work, so an
// idle gateway does no database churn at all. Unlike the inbound pump it does
// no ledger prune: outbound never writes processed_messages. It returns when
// ctx is done.
func (d *Outbound) StartPump(ctx context.Context) {
	if d == nil || d.repo == nil {
		return
	}

	ticker := time.NewTicker(OutboundPumpInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.pumpOnce(ctx)
		case <-d.kick:
			d.pumpOnce(ctx)
		}
	}
}

// FlushPeer wakes the pump so pending rows for a peer that just reconnected go
// out now instead of on the next tick.
//
// v1 IGNORES channel and peer: the pump runs a full FIFO pass, in id order,
// which is already the exact order for that peer. The parameters stay for
// a future per-peer claim. It never claims anything itself: that is what
// guarantees the pump and FlushPeer cannot race for the same row. The kick is
// non-blocking - a pending kick already means "a pass is coming".
func (d *Outbound) FlushPeer(channel, peer string) {
	if d == nil {
		return
	}
	logger.DebugCF(logComponent, "Outbound flush requested",
		map[string]interface{}{"channel": channel, "peer": peer})
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// pumpOnce runs one replay pass, guarded by the pending check that keeps an
// idle gateway out of the database.
func (d *Outbound) pumpOnce(ctx context.Context) {
	stats, err := d.repo.Stats()
	if err != nil {
		logger.WarnCF(logComponent, "Outbound spool stats failed; skipping pump tick",
			map[string]interface{}{"error": err.Error()})
		return
	}
	if stats.PendingOutbound == 0 {
		return
	}

	pass, err := d.claimPass(ctx)
	if err != nil {
		logger.WarnCF(logComponent, "Outbound spool pump pass failed",
			map[string]interface{}{"error": err.Error()})
	}
	if pass.republished > 0 || pass.skipped > 0 {
		logger.InfoCF(logComponent, "Outbound spool pump pass",
			map[string]interface{}{
				"delivered": pass.republished,
				"skipped":   pass.skipped,
				"deferred":  pass.deferred,
			})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Replay
// ──────────────────────────────────────────────────────────────────────────────

// claimPass claims pending outbound rows and delivers each one, looping until
// a pass claims nothing. It is the shared body of Drain and the pump tick, and
// it mirrors Inbound.claimPass down to the invariant.
//
// The loop stops at the first deferral: a row whose send failed means the
// channel is unhappy, so claiming more would only pile up failures - the pump
// retries them on the next tick.
//
// The pass owns an invariant: when it returns, it holds no claims OF ITS OWN.
// Every row this pass claimed is either deleted (delivered, expired,
// dead-lettered) or handed back to the pending set by the scoped release below,
// which is limited to the ids ClaimBatch returned. That is what makes the
// pump's "nothing pending, nothing to do" check sound - a row left claimed
// would be invisible to every later pass on this instance.
//
// The release is deliberately scoped instead of a blanket ReleaseClaims for the
// instance: live traffic spooled by Enqueue is claimed by this very instance
// and is being sent right now, so a blanket release would hand those rows back
// to the pending set and the next tick would send them a second time.
func (d *Outbound) claimPass(ctx context.Context) (passResult, error) {
	var total passResult
	var batchErr error
	var claimedIDs []int64

	defer func() {
		if len(claimedIDs) == 0 {
			return
		}
		handedBack, err := d.repo.ReleaseIDs(claimedIDs, d.instanceID)
		if err != nil {
			logger.WarnCF(logComponent, "Outbound spool claim release failed",
				map[string]interface{}{"error": err.Error()})
		} else if handedBack > 0 {
			logger.InfoCF(logComponent, "Returned undelivered outbound spool rows to pending",
				map[string]interface{}{"count": handedBack})
		}
	}()

	for {
		if err := ctx.Err(); err != nil {
			batchErr = err
			break
		}

		items, err := d.repo.ClaimBatch(store.SpoolOutbound, ClaimLimit, d.instanceID, time.Now())
		if err != nil {
			batchErr = fmt.Errorf("durable: claim outbound spool: %w", err)
			break
		}
		if len(items) == 0 {
			break
		}

		// Record ownership of the whole batch BEFORE delivering any of it:
		// ClaimBatch handed every one of these rows to this instance, and the
		// release below must cover them all even when a delivery error breaks
		// out of the loop early. A row whose delete already succeeded is simply
		// not matched by ReleaseIDs any more.
		for _, item := range items {
			claimedIDs = append(claimedIDs, item.ID)
		}

		var batch passResult
		for _, item := range items {
			res, err := d.deliverOne(ctx, item)
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

// deliverOne handles one claimed row: decode it, drop it if it is too old to
// be worth sending, deliver it, and settle the row according to how far the
// send got. The error it returns is an unexpected database failure, which stops
// the pass; a failed send is not an error, it is an outcome.
//
// Unlike inbound's republish, a row this pass cannot deliver is released
// IMMEDIATELY rather than only in the pass's defer: pump and live dispatcher
// share this instance id, and a row the pump still holds is invisible to the
// dispatcher until the whole batch is done.
func (d *Outbound) deliverOne(ctx context.Context, item store.SpoolItem) (outcome, error) {
	var msg bus.OutboundMessage
	if err := json.Unmarshal([]byte(item.Payload), &msg); err != nil {
		return d.handlePoisonOutbound(item, err)
	}

	// The row owns the message's identity: the payload's SpoolID is always
	// zero, because the field is tagged json:"-".
	msg.SpoolID = item.ID

	// A reply this old is no longer an answer to anything. Drop it before
	// spending a send on it.
	if !item.CreatedAt.IsZero() && time.Since(item.CreatedAt) > MaxOutboundAge {
		if err := d.deadLetter(&msg, "expired"); err != nil {
			return outcomeSkipped, err
		}
		return outcomeSkipped, nil
	}

	if d.deliver == nil {
		// Mis-wired, not fatal: the row is the only copy of the reply, so it
		// stays in the spool for a wiring that can send it.
		logger.WarnCF(logComponent, "No deliverer wired; outbound replay deferred",
			map[string]interface{}{
				"channel":  msg.Channel,
				"chat_id":  msg.ChatID,
				"spool_id": item.ID,
			})
		if err := d.repo.IncAttempt(item.ID); err != nil {
			return outcomeDeferred, fmt.Errorf("durable: count outbound attempt %d: %w", item.ID, err)
		}
		return outcomeDeferred, d.releaseRow(item.ID)
	}

	delivery := d.deliver(ctx, msg)
	switch {
	case delivery.Delivered:
		if err := d.repo.Complete([]int64{item.ID}); err != nil {
			return outcomePublished, fmt.Errorf("durable: complete outbound spool row %d: %w", item.ID, err)
		}
		return outcomePublished, nil

	case delivery.SentAnyChunk:
		// Part of the reply is already on the wire. Releasing would send those
		// chunks twice, so the remainder is dead-lettered instead.
		if err := d.deadLetter(&msg, deliveryErr(delivery.Err)); err != nil {
			return outcomeSkipped, err
		}
		return outcomeSkipped, nil

	default:
		// Nothing reached the channel: the row can be replayed whole.
		if err := d.repo.IncAttempt(item.ID); err != nil {
			return outcomeDeferred, fmt.Errorf("durable: count outbound attempt %d: %w", item.ID, err)
		}
		if item.Attempts+1 >= MaxOutboundAttempts {
			if err := d.deadLetter(&msg, deliveryErr(delivery.Err)); err != nil {
				return outcomeSkipped, err
			}
			return outcomeSkipped, nil
		}
		return outcomeDeferred, d.releaseRow(item.ID)
	}
}

// releaseRow hands one row back to the pending set, reporting a database
// failure to the caller so the pass stops on it.
func (d *Outbound) releaseRow(id int64) error {
	if _, err := d.repo.ReleaseIDs([]int64{id}, d.instanceID); err != nil {
		return fmt.Errorf("durable: release outbound spool row %d: %w", id, err)
	}
	return nil
}

// deadLetter logs and deletes a row that will never be delivered: an expired
// reply, a send that got partway out, or one that ran out of attempts. The
// delete is unconditional - keeping the row would only replay the same
// failure - and the log is the record that anything happened at all.
func (d *Outbound) deadLetter(msg *bus.OutboundMessage, reason string) error {
	logger.WarnCF(logComponent, "Outbound dead-lettered",
		map[string]interface{}{
			"channel":         msg.Channel,
			"chat_id":         msg.ChatID,
			"spool_id":        msg.SpoolID,
			"reason":          reason,
			"content_preview": truncate(msg.Content, poisonPreview),
		})
	if err := d.repo.Complete([]int64{msg.SpoolID}); err != nil {
		return fmt.Errorf("durable: dead-letter outbound spool row %d: %w", msg.SpoolID, err)
	}
	return nil
}

// handlePoisonOutbound deals with a row whose payload is not a decodable
// message. No retry can fix bytes that are not JSON, so the row is counted
// and, once it reaches poisonLimit attempts, dead-lettered. Mirrors
// Inbound.handlePoison.
func (d *Outbound) handlePoisonOutbound(item store.SpoolItem, decodeErr error) (outcome, error) {
	if err := d.repo.IncAttempt(item.ID); err != nil {
		return outcomeSkipped, fmt.Errorf("durable: count poison outbound attempt %d: %w", item.ID, err)
	}
	attempts := item.Attempts + 1
	if attempts < poisonLimit {
		logger.WarnCF(logComponent, "Undecodable outbound spool payload; retrying",
			map[string]interface{}{
				"spool_id": item.ID,
				"attempts": attempts,
				"error":    decodeErr.Error(),
			})
		// Skipped, not deferred: one corrupt row must not stop the pass from
		// delivering everything else behind it. The row stays claimed and the
		// pass's scoped release hands it back, so the next tick counts the next
		// attempt on the way to the dead-letter ceiling.
		return outcomeSkipped, nil
	}

	logger.WarnCF(logComponent, "Dead-lettering poison outbound spool row",
		map[string]interface{}{
			"spool_id": item.ID,
			"channel":  item.Channel,
			"attempts": attempts,
			"payload":  truncate(item.Payload, poisonPreview),
		})
	if err := d.repo.Complete([]int64{item.ID}); err != nil {
		return outcomeSkipped, fmt.Errorf("durable: complete poison outbound row %d: %w", item.ID, err)
	}
	return outcomeSkipped, nil
}

// deliveryErr renders a delivery error for a log field, including the case
// where the deliverer reported failure but kept the reason to itself.
func deliveryErr(err error) string {
	if err == nil {
		return "delivery failed"
	}
	return err.Error()
}
