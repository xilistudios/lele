package session

import (
	"fmt"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// TestConcurrentReadWriteHistory exercises the read/write paths that were
// previously racy: writers append via AppendAssistantChunk/AddFullMessage
// while readers call GetHistoryView/HasMessages. Run with -race; it must
// complete without any data race reports.
func TestConcurrentReadWriteHistory(t *testing.T) {
	sm := NewSessionManager()

	const writers = 5
	const readers = 5
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(writers + readers)

	for w := 0; w < writers; w++ {
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("writer-session-%d", id)
			for i := 0; i < iterations; i++ {
				sm.AppendAssistantChunk(key, fmt.Sprintf("chunk %d ", i))
				sm.AddFullMessage(key, providers.Message{
					Role:    "assistant",
					Content: fmt.Sprintf("final %d", i),
				})
				sm.AddFullMessage(key, providers.Message{
					Role:    "user",
					Content: fmt.Sprintf("msg %d", i),
				})
			}
		}(w)
	}

	for r := 0; r < readers; r++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				// Read both writer sessions and a cold (nonexistent) session
				// to exercise the store fallback path in HasMessages.
				_ = sm.GetHistoryView(fmt.Sprintf("writer-session-%d", i%writers))
				_ = sm.HasMessages(fmt.Sprintf("writer-session-%d", i%writers))
				_ = sm.GetHistoryView("cold-session")
				_ = sm.HasMessages("cold-session")
			}
		}(r)
	}

	wg.Wait()
}
