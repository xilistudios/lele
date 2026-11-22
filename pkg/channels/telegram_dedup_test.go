package channels

import (
	"fmt"
	"testing"

	"github.com/xilistudios/lele/pkg/logger"
)

func newDedupTestChannel() *TelegramChannel {
	return &TelegramChannel{processedIDs: make(map[string]int64)}
}

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

func TestIsDuplicate_EvictsOldestKeepsNewest(t *testing.T) {
	logger.SetLevel(logger.ERROR)
	c := newDedupTestChannel()

	total := telegramProcessedMax + 1
	for i := 0; i < total; i++ {
		c.isDuplicate(fmt.Sprintf("m%d", i))
	}

	if got := len(c.processedIDs); got != telegramProcessedKeep {
		t.Fatalf("cache size after eviction = %d, want %d", got, telegramProcessedKeep)
	}

	for i := telegramProcessedMax - telegramProcessedKeep + 1; i < total; i++ {
		key := fmt.Sprintf("m%d", i)
		if _, ok := c.processedIDs[key]; !ok {
			t.Fatalf("newest key %s was evicted; kept a random subset instead of the newest", key)
		}
	}
	if len(c.processedIDs) != telegramProcessedKeep {
		t.Fatalf("unexpected extra survivors beyond newest %d", telegramProcessedKeep)
	}
}

func TestIsDuplicate_ReprocessedAfterEviction(t *testing.T) {
	logger.SetLevel(logger.ERROR)
	c := newDedupTestChannel()

	for i := 0; i < telegramProcessedMax+1; i++ {
		c.isDuplicate(fmt.Sprintf("old-%d", i))
	}
	c.isDuplicate("old-0")
	if !c.isDuplicate("old-0") {
		t.Fatalf("just re-recorded key not detected as duplicate")
	}
	if got := len(c.processedIDs); got > telegramProcessedMax {
		t.Fatalf("cache size after post-eviction insert = %d, must stay <= Max(%d)", got, telegramProcessedMax)
	}
}
