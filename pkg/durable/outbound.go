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

const (
	OutboundPumpInterval = time.Second
	MaxOutboundAttempts  = 5
	MaxOutboundAge       = time.Hour
	kickBuffer           = 1
)

var outboundBackoff = []time.Duration{
	time.Second,
	5 * time.Second,
	30 * time.Second,
	2 * time.Minute,
}

type Delivery struct {
	Delivered    bool
	SentAnyChunk bool
	Err          error
	RetryAfter   time.Duration
}

type DeliverFunc func(ctx context.Context, msg bus.OutboundMessage) Delivery

type Outbound struct {
	repo       *store.SpoolRepo
	deliver    DeliverFunc
	instanceID string
	kick       chan struct{}
	now        func() time.Time
}

func NewOutbound(repo *store.SpoolRepo, deliver DeliverFunc) *Outbound {
	return &Outbound{
		repo:       repo,
		deliver:    deliver,
		instanceID: "lele-" + randomHex(),
		kick:       make(chan struct{}, kickBuffer),
		now:        time.Now,
	}
}

func (d *Outbound) InstanceID() string {
	if d == nil {
		return ""
	}
	return d.instanceID
}

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

func (d *Outbound) Enqueue(msg *bus.OutboundMessage) bool {
	if d == nil || d.repo == nil || msg == nil {
		return false
	}

	if msg.SpoolID != 0 {
		return false
	}

	if !ShouldSpoolOutbound(*msg) {
		return false
	}

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

func (d *Outbound) Forget(msg *bus.OutboundMessage, reason string) bool {
	if d == nil || d.repo == nil || msg == nil || msg.SpoolID == 0 {
		return false
	}
	if err := d.deadLetter(msg, reason); err != nil {
		return false
	}
	return true
}

func (d *Outbound) Drain(ctx context.Context) (int, error) {
	if d == nil || d.repo == nil {
		return 0, nil
	}

	reclaimed, err := d.repo.ReclaimStale(StaleClaimTimeout, d.now())
	if err != nil {
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

func (d *Outbound) ReleaseClaims() (int, error) {
	if d == nil || d.repo == nil {
		return 0, nil
	}
	return d.repo.ReleaseClaims(d.instanceID)
}

func (d *Outbound) Stats() (store.SpoolStats, error) {
	if d == nil || d.repo == nil {
		return store.SpoolStats{}, nil
	}
	return d.repo.Stats()
}

func (d *Outbound) StartPump(ctx context.Context) {
	if d == nil || d.repo == nil {
		return
	}

	ticker := time.NewTicker(OutboundPumpInterval)
	defer ticker.Stop()
	nextPrune := d.now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.pumpOnce(ctx)
			if now := d.now(); !now.Before(nextPrune) {
				nextPrune = now.Add(PruneInterval)
				pruneDeadLetter(d.repo, now)
			}
		case <-d.kick:
			d.pumpOnce(ctx)
		}
	}
}

func pruneDeadLetter(repo *store.SpoolRepo, now time.Time) {
	pruned, err := repo.PruneDeadLetter(ProcessedRetention, now)
	if err != nil {
		logger.WarnCF(logComponent, "Dead-letter prune failed",
			map[string]interface{}{"error": err.Error()})
		return
	}
	if pruned > 0 {
		logger.InfoCF(logComponent, "Pruned dead-letter rows",
			map[string]interface{}{"pruned": pruned})
	}
}

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

		items, err := d.repo.ClaimBatch(store.SpoolOutbound, ClaimLimit, d.instanceID, d.now())
		if err != nil {
			batchErr = fmt.Errorf("durable: claim outbound spool: %w", err)
			break
		}
		if len(items) == 0 {
			break
		}

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

func (d *Outbound) deliverOne(ctx context.Context, item store.SpoolItem) (outcome, error) {
	var msg bus.OutboundMessage
	if err := json.Unmarshal([]byte(item.Payload), &msg); err != nil {
		return d.handlePoisonOutbound(item, err)
	}

	msg.SpoolID = item.ID

	if !item.CreatedAt.IsZero() && d.now().Sub(item.CreatedAt) > MaxOutboundAge {
		if err := d.deadLetter(&msg, "expired"); err != nil {
			return outcomeSkipped, err
		}
		return outcomeSkipped, nil
	}

	if d.deliver == nil {
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
		if err := d.deadLetter(&msg, deliveryErr(delivery.Err)); err != nil {
			return outcomeSkipped, err
		}
		return outcomeSkipped, nil

	default:
		if item.Attempts+1 >= MaxOutboundAttempts {
			if err := d.repo.IncAttempt(item.ID); err != nil {
				return outcomeDeferred, fmt.Errorf("durable: count outbound attempt %d: %w", item.ID, err)
			}
			if err := d.deadLetter(&msg, deliveryErr(delivery.Err)); err != nil {
				return outcomeSkipped, err
			}
			return outcomeSkipped, nil
		}
		nextAt := d.now().Add(outboundBackoffFor(item.Attempts, delivery.RetryAfter))
		if err := d.repo.IncAttemptWithBackoff(item.ID, nextAt); err != nil {
			return outcomeDeferred, fmt.Errorf("durable: defer outbound attempt %d: %w", item.ID, err)
		}
		return outcomeDeferred, d.releaseRow(item.ID)
	}
}

func outboundBackoffFor(attempts int, retryAfter time.Duration) time.Duration {
	delay := outboundBackoff[len(outboundBackoff)-1]
	if attempts >= 0 && attempts < len(outboundBackoff) {
		delay = outboundBackoff[attempts]
	}
	if retryAfter > delay {
		return retryAfter
	}
	return delay
}

func (d *Outbound) releaseRow(id int64) error {
	if _, err := d.repo.ReleaseIDs([]int64{id}, d.instanceID); err != nil {
		return fmt.Errorf("durable: release outbound spool row %d: %w", id, err)
	}
	return nil
}

func (d *Outbound) deadLetter(msg *bus.OutboundMessage, reason string) error {
	logger.WarnCF(logComponent, "Outbound dead-lettered",
		map[string]interface{}{
			"channel":         msg.Channel,
			"chat_id":         msg.ChatID,
			"spool_id":        msg.SpoolID,
			"reason":          reason,
			"content_preview": truncate(msg.Content, poisonPreview),
		})
	if err := d.repo.DeadLetter([]int64{msg.SpoolID}, reason, d.now()); err != nil {
		return fmt.Errorf("durable: dead-letter outbound spool row %d: %w", msg.SpoolID, err)
	}
	return nil
}

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

func deliveryErr(err error) string {
	if err == nil {
		return "delivery failed"
	}
	return err.Error()
}
