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

const (
	StaleClaimTimeout  = 30 * time.Second
	ClaimLimit         = 64
	PumpInterval       = time.Second
	PruneInterval      = 10 * time.Minute
	ProcessedRetention = 7 * 24 * time.Hour
)

const (
	publishRetryBudget = 5 * time.Second
	publishRetryDelay  = 25 * time.Millisecond
	poisonLimit        = 5
	poisonPreview      = 200
	idBytes            = 8
	logComponent       = "durable"
)

type Inbound struct {
	repo       *store.SpoolRepo
	instanceID string
	publish    func(bus.InboundMessage) bool
}

func NewInbound(repo *store.SpoolRepo, publish func(bus.InboundMessage) bool) *Inbound {
	return &Inbound{
		repo:       repo,
		instanceID: "lele-" + randomHex(),
		publish:    publish,
	}
}

func (d *Inbound) InstanceID() string { return d.instanceID }

func (d *Inbound) Enqueue(msg *bus.InboundMessage) bool {
	if d == nil || d.repo == nil || msg == nil {
		return false
	}

	if msg.SpoolID != 0 {
		return false
	}

	if msg.DedupeID == "" {
		msg.DedupeID = dedupeIDFor(*msg)
	}

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

	id, err := d.repo.EnqueueClaimed(store.SpoolInbound, msg.Channel, msg.ChatID, msg.SessionKey, msg.DedupeID, string(payload), d.instanceID, time.Now())
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

func (d *Inbound) Release(msg *bus.InboundMessage) bool {
	if d == nil || d.repo == nil || msg == nil || msg.SpoolID == 0 {
		return false
	}
	released, err := d.repo.ReleaseIDs([]int64{msg.SpoolID}, d.instanceID)
	if err != nil {
		logger.WarnCF(logComponent, "Durable inbound release failed",
			map[string]interface{}{
				"channel":  msg.Channel,
				"spool_id": msg.SpoolID,
				"error":    err.Error(),
			})
		return false
	}
	return released > 0
}

func (d *Inbound) Drain(ctx context.Context) (int, error) {
	if d.repo == nil {
		return 0, nil
	}

	reclaimed, err := d.repo.ReclaimStale(StaleClaimTimeout, time.Now())
	if err != nil {
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

func (d *Inbound) ReleaseClaims() (int, error) {
	if d.repo == nil {
		return 0, nil
	}
	return d.repo.ReleaseClaims(d.instanceID)
}

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
			pruneProcessed(d.repo, now)
			pruneDeadLetter(d.repo, now)
		}
	}
}

func pruneProcessed(repo *store.SpoolRepo, now time.Time) {
	pruned, err := repo.PruneProcessed(ProcessedRetention, now)
	if err != nil {
		logger.WarnCF(logComponent, "Dedupe ledger prune failed",
			map[string]interface{}{"error": err.Error()})
		return
	}
	if pruned > 0 {
		logger.InfoCF(logComponent, "Pruned dedupe ledger",
			map[string]interface{}{"pruned": pruned})
	}
}

func (d *Inbound) Stats() (store.SpoolStats, error) {
	if d.repo == nil {
		return store.SpoolStats{}, nil
	}
	return d.repo.Stats()
}

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

type outcome int

const (
	outcomePublished outcome = iota
	outcomeSkipped
	outcomeDeferred
)

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

type passResult struct {
	republished int
	skipped     int
	deferred    int
}

func (d *Inbound) claimPass(ctx context.Context) (passResult, error) {
	var total passResult
	var batchErr error
	var claimedIDs []int64

	defer func() {
		if len(claimedIDs) == 0 {
			return
		}
		handedBack, err := d.repo.ReleaseIDs(claimedIDs, d.instanceID)
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

		for _, item := range items {
			claimedIDs = append(claimedIDs, item.ID)
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

func (d *Inbound) republish(ctx context.Context, item store.SpoolItem) (outcome, error) {
	var msg bus.InboundMessage
	if err := json.Unmarshal([]byte(item.Payload), &msg); err != nil {
		return d.handlePoison(item, err)
	}

	msg.SpoolID = item.ID
	if msg.DedupeID == "" {
		msg.DedupeID = item.MsgID
	}

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

func dedupeIDFor(msg bus.InboundMessage) string {
	if id := msg.Metadata["message_id"]; id != "" {
		return id
	}
	if ts := msg.Metadata["message_ts"]; ts != "" {
		if ch := msg.Metadata["channel_id"]; ch != "" {
			return ch + ":" + ts
		}
		return ts
	}
	return randomHex()
}

func randomHex() string {
	buf := make([]byte, idBytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
