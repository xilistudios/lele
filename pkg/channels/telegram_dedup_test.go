package channels

import (
	"fmt"
	"testing"

	"github.com/xilistudios/lele/pkg/logger"
)

// Tests for SURV-04: the Telegram fallback dedup cache must evict the OLDEST
// entries when it overflows, keeping the newest ones. Before the fix the
// eviction kept a random half (Go map iteration order) of an untyped set, so
// freshly-seen message keys were as likely to be evicted as ancient ones and
// duplicates could be reprocessed right after an eviction.

func newDedupTestChannel() *TelegramChannel {
	return &TelegramChannel{processedIDs: make(map[string]int64)}
}

// TestIsDuplicate_DetectsAndRecords covers the base behavior the eviction
// must not break: first sight records and returns false, second sight
// returns true.
func TestIsDuplicate_DetectsAndRecords(t *testing.T) {
	logger.SetLevel(logger.ERROR)
	c := newDedupTestChannel()

	if c.isDuplicate("10:1") {
		t.Fatalf("first sight of a message reported as duplicate")
	}
	if !c.isDuplicate("10:1") {
		t.Fatalf("second sight of a message not reported as duplicate")
	}
}

// TestIsDuplicate_EvictsOldestKeepsNewest is the SURV-04 assertion: after
// pushing the cache past telegramProcessedMax, exactly telegramProcessedKeep
// entries survive and they are the NEWEST ones (m500..m1000 in insertion
// order), never a random subset.
func TestIsDuplicate_EvictsOldestKeepsNewest(t *testing.T) {
	logger.SetLevel(logger.ERROR)
	c := newDedupTestChannel()

	// Insert m0..m1000 sequentially — timestamps make insertion order
	// observable even when they land in the same nanosecond bucket is
	// impossible here because each call takes the lock sequentially and
	// time.Now() advances (worst case equal ns is broken by the key
	// tie-break, which would still favor later keys only if ts equal —
	// to keep the test strictly deterministic we don't rely on ns
	// resolution: assertions below tolerate the tie-break ordering).
	total := telegramProcessedMax + 1 // trigger eviction on the last insert
	for i := 0; i < total; i++ {
		c.isDuplicate(fmt.Sprintf("m%d", i))
	}

	if got := len(c.processedIDs); got != telegramProcessedKeep {
		t.Fatalf("cache size after eviction = %d, want %d", got, telegramProcessedKeep)
	}

	// The 500 NEWEST keys (m501..m1000 — of the 1001 inserts the newest
	// 500) must all survive; any survivor outside that range means eviction
	// dropped fresh entries.
	for i := telegramProcessedMax - telegramProcessedKeep + 1; i < total; i++ {
		key := fmt.Sprintf("m%d", i)
		if _, ok := c.processedIDs[key]; !ok {
			t.Fatalf("newest key %s was evicted; kept a random subset instead of the newest", key)
		}
	}
	// With exactly Keep survivors, nothing older may remain either.
	if len(c.processedIDs) != telegramProcessedKeep {
		t.Fatalf("unexpected extra survivors beyond newest %d", telegramProcessedKeep)
	}
}

// TestIsDuplicate_ReprocessedAfterEviction verifies the cache keeps working
// after eviction: a key evicted as oldest can be re-recorded (its duplicate
// window legitimately expired) and stays detected as a duplicate. Size may
// exceed Keep between evictions — the cap only triggers above Max.
func TestIsDuplicate_ReprocessedAfterEviction(t *testing.T) {
	logger.SetLevel(logger.ERROR)
	c := newDedupTestChannel()

	// Fill and force eviction.
	for i := 0; i < telegramProcessedMax+1; i++ {
		c.isDuplicate(fmt.Sprintf("old-%d", i))
	}
	// A key that was evicted can be recorded again without error...
	c.isDuplicate("old-0")
	// ...and the duplicate check still works for that key.
	if !c.isDuplicate("old-0") {
		t.Fatalf("just re-recorded key not detected as duplicate")
	}
	if got := len(c.processedIDs); got > telegramProcessedMax {
		t.Fatalf("cache size after post-eviction insert = %d, must stay <= Max(%d)", got, telegramProcessedMax)
	}
}
