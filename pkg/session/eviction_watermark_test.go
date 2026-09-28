package session

// Tests for the LRU water-mark (evictionWaterMark in eviction.go): the
// hysteresis band that amortizes the eviction cascade fired by every session
// insertion.
//
// Evictions are counted by diffing the resident key set around each operation
// (residentKeysOf, manager_saveall_test.go). pkg/session has no eviction
// callback: the tools-layer sessionEvictCallback fires on subagent cleanup (a
// call to EvictSession, a different path), while evictIfNeeded reports each
// session it drops only through its per-session "LRU evicted session" log line
// — pinned separately in TestEvictIfNeeded_WaterMark_LogsOneLinePerEvictedSession.
//
// All the trigger numbers below are pinned for the shipped configuration
// (maxInMemory=50 → band 16, manager.go:39): creations #1..#66 fill the band
// with 0 evictions, #67 fires ONE cascade of 16 evictions back down to
// maxInMemory, and the cycle then repeats every 16 insertions.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/store"
)

// wmKey builds a distinct session key per insertion.
func wmKey(i int) string { return fmt.Sprintf("wm:s%03d", i) }

// wmBand is the hysteresis band actually applied for the given limit
// (eviction.go caps it at maxInMemory so small limits stay tight).
func wmBand(max int) int {
	if max < evictionWaterMark {
		return max
	}
	return evictionWaterMark
}

// evictionObserver counts the sessions evictIfNeeded dropped between two
// observations by diffing the resident key set: the keys that were resident
// before an operation and are gone after it are exactly the sessions it
// evicted (the operation's own key only ever appears).
type evictionObserver struct {
	sm   *SessionManager
	prev map[string]bool
}

func newEvictionObserver(sm *SessionManager) *evictionObserver {
	return &evictionObserver{sm: sm, prev: residentKeysOf(sm)}
}

// observe reports how many sessions vanished since the previous observation and
// the current resident count, then re-arms.
func (o *evictionObserver) observe() (evicted, resident int) {
	now := residentKeysOf(o.sm)
	for key := range o.prev {
		if !now[key] {
			evicted++
		}
	}
	o.prev = now
	return evicted, len(now)
}

// pinAccessTimes gives the given keys a strictly increasing access order
// (keys[0] oldest) so the LRU batch order is deterministic and independent of
// the clock resolution.
func pinAccessTimes(sm *SessionManager, keys []string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for i, key := range keys {
		sm.accessTimes[key] = time.Unix(int64(1000+i), 0)
	}
}

// TestEvictIfNeeded_WaterMark_TriggerAndBatch pins test (a): the exact trigger
// point and the batch size of the water-marked LRU cascade.
//
//	maxInMemory = 50, band = 16 → mark = 66
//	  creations #1..#66   → 0 evictions (resident grows to 66 = the mark)
//	  creation  #67       → ONE cascade of 16 evictions, back down to 50
//	  creations #68..#82  → 0 evictions (resident grows to 66 again)
//	  creation  #83       → cascade of 16 … and so on every 16 insertions
func TestEvictIfNeeded_WaterMark_TriggerAndBatch(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	const max = 50 // production default
	const band = 16
	sm.SetMaxInMemory(max)
	sm.SetEvictionTTL(0) // isolate the LRU trigger from the TTL sweep

	if wmBand(max) != band {
		t.Fatalf("fixture broken: band for max=%d is %d, want %d", max, wmBand(max), band)
	}

	obs := newEvictionObserver(sm)
	cascades, totalEvicted := 0, 0

	// One cascade lands on the creation that finds max+band residents already
	// in memory (#max+band+1) and then every `band` creations.
	isCascade := func(i int) bool {
		return i >= max+band+1 && (i-(max+band+1))%band == 0
	}

	const creations = 200 // a subagent burst of the size reported in the profile
	for i := 1; i <= creations; i++ {
		sm.GetOrCreate(wmKey(i))
		evicted, resident := obs.observe()

		wantEvicted := 0
		if isCascade(i) {
			wantEvicted = band
			cascades++
		}
		if evicted != wantEvicted {
			t.Fatalf("creation #%d evicted %d session(s), want %d (mark at %d)", i, evicted, wantEvicted, max+band)
		}
		totalEvicted += evicted

		// Test (c): the resident peak never exceeds maxInMemory+band.
		if resident > max+band {
			t.Fatalf("resident after creation #%d = %d, exceeds the %d bound", i, resident, max+band)
		}
		if i == max+band && resident != max+band {
			t.Fatalf("resident at the mark = %d, want %d", resident, max+band)
		}
	}

	// Amortization: 9 cascades of 16 instead of one-in-one-out's
	// creations-max = 150 cascades of 1.
	if cascades != 9 {
		t.Errorf("cascades over %d insertions = %d, want 9 (one-in-one-out would fire %d)", creations, cascades, creations-max)
	}
	if totalEvicted != cascades*band {
		t.Errorf("total evictions = %d, want %d (band per cascade)", totalEvicted, cascades*band)
	}
	if got := len(residentKeysOf(sm)); got != creations-totalEvicted {
		t.Errorf("resident sessions = %d, want %d", got, creations-totalEvicted)
	}
	t.Logf("insertions=%d cascades=%d evictions=%d resident=%d", creations, cascades, totalEvicted, len(residentKeysOf(sm)))
}

// TestEvictIfNeeded_WaterMark_MemoryBound pins test (c) for the full range of
// limits: the resident peak is maxInMemory+min(evictionWaterMark, maxInMemory),
// i.e. the band is capped at the limit itself (16 extra sessions at the default
// 50, but never more than a doubling for a small limit).
func TestEvictIfNeeded_WaterMark_MemoryBound(t *testing.T) {
	for _, tc := range []struct {
		name string
		max  int
	}{
		{"default_max50", 50},
		{"band_capped_at_limit_max8", 8},
		{"single_resident_max1", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			sm := NewSessionManager()
			sm.SetSessionRepo(s.Sessions())
			sm.SetMaxInMemory(tc.max)
			sm.SetEvictionTTL(0)

			band := wmBand(tc.max)
			wantPeak := tc.max + band
			obs := newEvictionObserver(sm)
			peak, cascades := 0, 0
			for i := 1; i <= 4*wantPeak; i++ {
				sm.GetOrCreate(wmKey(i))
				evicted, resident := obs.observe()
				if resident > wantPeak {
					t.Fatalf("resident after creation #%d = %d, exceeds the %d bound", i, resident, wantPeak)
				}
				if resident > peak {
					peak = resident
				}
				if evicted > 0 {
					cascades++
					// A cascade drains the whole backlog in one batch.
					if evicted != band {
						t.Fatalf("cascade at #%d evicted %d session(s), want %d", i, evicted, band)
					}
				}
			}
			if peak != wantPeak {
				t.Errorf("resident peak = %d, want %d (maxInMemory+band)", peak, wantPeak)
			}
			// Without the band the manager would have evicted on (almost) every
			// insertion past the limit; hysteresis must collapse those into
			// whole-band cascades.
			if leg := 4*wantPeak - tc.max; cascades >= leg {
				t.Errorf("cascades = %d, want far fewer than the %d one-in-one-out evictions", cascades, leg)
			}
			t.Logf("max=%d band=%d peak=%d cascades=%d", tc.max, band, peak, cascades)
		})
	}
}

// TestEvictIfNeeded_WaterMark_SingleResidentKeepsLegacyTrigger pins the cap on
// the band for the pathological limit: with maxInMemory=1 the band is 1, which
// reproduces the historical one-in-one-out trigger (evict as soon as a second
// session is resident). This is the configuration TestSaveAllSkipsEvicted
// pins, so the water-mark must not change it.
func TestEvictIfNeeded_WaterMark_SingleResidentKeepsLegacyTrigger(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	sm.SetMaxInMemory(1)
	sm.SetEvictionTTL(0)

	obs := newEvictionObserver(sm)

	// #1 and #2 fill the band (peak = max+band = 2) with 0 evictions.
	for i := 1; i <= 2; i++ {
		sm.GetOrCreate(wmKey(i))
		if evicted, resident := obs.observe(); evicted != 0 || resident != i {
			t.Fatalf("creation #%d: evicted=%d resident=%d, want 0 and %d", i, evicted, resident, i)
		}
	}
	// #3 finds 2 residents → evicts exactly one, as before the water-mark.
	sm.GetOrCreate(wmKey(3))
	if evicted, resident := obs.observe(); evicted != 1 || resident != 2 {
		t.Fatalf("creation #3: evicted=%d resident=%d, want 1 and 2 (legacy one-in-one-out)", evicted, resident)
	}
	// The survivor is the newer one: the LRU criterion is untouched.
	if residents := residentKeysOf(sm); residents[wmKey(1)] {
		t.Error("the oldest session survived the eviction: LRU criterion broken")
	}
}

// TestEvictIfNeeded_WaterMark_ResidentLookupsNeverEvict pins test (b):
// GetOrCreate of an already-resident key is a lookup, not an insertion, so it
// can never evict anything — no phantom cascade, not even with the resident set
// sitting exactly on the mark. A cold load (session on disk but not resident)
// does go through evictIfNeeded and must stay under the mark: 0 evictions too.
func TestEvictIfNeeded_WaterMark_ResidentLookupsNeverEvict(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	const max = 4 // band = min(16, 4) = 4 → mark at 8
	sm.SetMaxInMemory(max)
	sm.SetEvictionTTL(0)

	mark := max + wmBand(max)
	for i := 1; i <= mark; i++ {
		sm.GetOrCreate(wmKey(i))
	}
	if got := len(residentKeysOf(sm)); got != mark {
		t.Fatalf("fixture broken: resident = %d, want %d", got, mark)
	}

	// 200 lookups of resident keys, right on the mark: 0 evictions.
	obs := newEvictionObserver(sm)
	for i := 0; i < 200; i++ {
		sm.GetOrCreate(wmKey(1 + i%mark))
		if evicted, resident := obs.observe(); evicted != 0 || resident != mark {
			t.Fatalf("resident lookup #%d: evicted=%d resident=%d, want 0 and %d", i, evicted, resident, mark)
		}
	}

	// A cold load of a session that is on disk but not resident: the insertion
	// returns the resident set to the mark without evicting anything.
	if err := sm.Save(wmKey(1)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !sm.EvictSession(wmKey(1)) {
		t.Fatal("EvictSession(wmKey(1)) = false")
	}
	obs = newEvictionObserver(sm) // re-arm after the explicit eviction
	sm.GetOrCreate(wmKey(1))      // cold load
	if evicted, resident := obs.observe(); evicted != 0 || resident != mark {
		t.Fatalf("cold load: evicted=%d resident=%d, want 0 and %d", evicted, resident, mark)
	}

	// Only the NEXT insertion past the mark cascades — once.
	obs = newEvictionObserver(sm)
	sm.GetOrCreate(wmKey(mark + 1))
	if evicted, resident := obs.observe(); evicted != wmBand(max) || resident != max+1 {
		t.Errorf("insertion past the mark: evicted=%d resident=%d, want %d and %d", evicted, resident, wmBand(max), max+1)
	}
}

// TestEvictIfNeeded_WaterMark_BatchKeepsLRUOrder pins the eviction criterion
// across a batch: the sessions dropped in one cascade are the least recently
// used ones, in LRU order (as the one-in-one-out path did, one per cascade).
func TestEvictIfNeeded_WaterMark_BatchKeepsLRUOrder(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	const max = 2 // band = min(16, 2) = 2 → mark at 4
	sm.SetMaxInMemory(max)
	sm.SetEvictionTTL(0)

	keys := []string{wmKey(1), wmKey(2), wmKey(3), wmKey(4)} // oldest → newest
	for _, key := range keys {
		sm.GetOrCreate(key)
	}
	pinAccessTimes(sm, keys) // deterministic LRU order

	obs := newEvictionObserver(sm)
	sm.GetOrCreate(wmKey(5))
	evicted, resident := obs.observe()
	if evicted != 2 {
		t.Fatalf("cascade evicted %d session(s), want 2 (the whole backlog)", evicted)
	}
	if resident != max+1 {
		t.Fatalf("resident after the cascade = %d, want %d", resident, max+1)
	}

	residents := residentKeysOf(sm)
	for _, key := range []string{wmKey(1), wmKey(2)} { // the two oldest
		if residents[key] {
			t.Errorf("%s survived the batch: LRU order broken", key)
		}
	}
	for _, key := range []string{wmKey(3), wmKey(4), wmKey(5)} {
		if !residents[key] {
			t.Errorf("%s was evicted: not the least recently used one", key)
		}
	}
}

// TestEvictIfNeeded_WaterMark_BatchStillPersistsEveryEvictedSession proves the
// water-mark changed nothing about durability: every session dropped by a
// batch cascade is still saved on its way out (one save per session, exactly
// like the one-in-one-out path), so a restart can still read all of them.
func TestEvictIfNeeded_WaterMark_BatchStillPersistsEveryEvictedSession(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	const max = 4 // band = 4 → mark at 8
	sm.SetMaxInMemory(max)
	sm.SetEvictionTTL(0)

	// Each session is dirty (never persisted) when the cascade fires.
	const perSession = 2
	keys := make([]string, 0, max+wmBand(max)+1)
	for i := 1; i <= max+wmBand(max); i++ {
		key := wmKey(i)
		keys = append(keys, key)
		sm.AddMessage(key, "user", "hello")
		sm.AddMessage(key, "assistant", "hi")
	}
	pinAccessTimes(sm, keys) // keys[0..band-1] are the least recently used ones

	obs := newEvictionObserver(sm)
	sm.GetOrCreate(wmKey(len(keys) + 1)) // crosses the mark → one cascade
	evicted, resident := obs.observe()
	if evicted != wmBand(max) {
		t.Fatalf("cascade evicted %d session(s), want %d", evicted, wmBand(max))
	}
	if resident != max+1 {
		t.Fatalf("resident after the cascade = %d, want %d", resident, max+1)
	}

	for _, key := range keys[:evicted] {
		count, err := s.Sessions().MessageCount(key)
		if err != nil {
			t.Fatalf("MessageCount(%s): %v", key, err)
		}
		if count != perSession {
			t.Errorf("evicted session %s persisted %d messages, want %d", key, count, perSession)
		}
	}
}

// TestEvictIfNeeded_TTLSweepRunsInsideWaterMark documents the boundary of the
// water-mark: it gates the LRU cascade only. The TTL sweep keeps running on
// every insertion exactly as it did before, even far below the mark — an idle
// session is evicted once and then gone from accessTimes, so that sweep cannot
// repeat and cannot storm.
func TestEvictIfNeeded_TTLSweepRunsInsideWaterMark(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	const max = 50
	sm.SetMaxInMemory(max)
	sm.SetEvictionTTL(time.Minute)

	for i := 1; i <= 3; i++ {
		sm.GetOrCreate(wmKey(i))
	}
	// Age the three sessions past the TTL without sleeping.
	sm.mu.Lock()
	old := time.Now().Add(-2 * time.Minute)
	for i := 1; i <= 3; i++ {
		sm.accessTimes[wmKey(i)] = old
	}
	sm.mu.Unlock()

	obs := newEvictionObserver(sm)
	sm.GetOrCreate(wmKey(4)) // 3 residents, mark is at 66
	evicted, resident := obs.observe()
	if evicted != 3 {
		t.Fatalf("idle sweep evicted %d session(s), want 3 (the TTL pass must not be gated by the water-mark)", evicted)
	}
	if resident != 1 {
		t.Fatalf("resident after the idle sweep = %d, want 1", resident)
	}
}

// TestEvictIfNeeded_WaterMark_LogsOneLinePerEvictedSession pins the observable
// the TUI/profile tooling counts: a batch cascade must still log ONE
// "LRU evicted session" line per session, exactly as the one-in-one-out path
// did — batching must not collapse 16 evictions into a single event.
func TestEvictIfNeeded_WaterMark_LogsOneLinePerEvictedSession(t *testing.T) {
	logDir := t.TempDir()
	prevLevel := logger.GetLevel()
	prevBase := logger.GetLogsPath()
	logger.SetQuiet(true)
	logger.SetLevel(logger.INFO)
	if err := logger.EnableMultiFileLogging(logDir); err != nil {
		t.Fatalf("EnableMultiFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLogsPath(prevBase) // don't leave a deleted temp dir behind
		logger.SetQuiet(false)
		logger.SetLevel(prevLevel)
	})

	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	const max = 4 // band = 4 → mark at 8
	sm.SetMaxInMemory(max)
	sm.SetEvictionTTL(0)

	keys := make([]string, 0, max+wmBand(max)+1)
	for i := 1; i <= max+wmBand(max); i++ {
		key := wmKey(i)
		keys = append(keys, key)
		sm.GetOrCreate(key)
	}
	pinAccessTimes(sm, keys)
	sm.GetOrCreate(wmKey(len(keys) + 1)) // one cascade of wmBand(max) evictions

	logPath := filepath.Join(logDir, "info-"+time.Now().Format("2006-01-02")+".log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read info log: %v", err)
	}
	lines := strings.Count(string(data), `"message":"LRU evicted session"`)
	if want := wmBand(max); lines != want {
		t.Fatalf("eviction log lines = %d, want %d (one per evicted session)", lines, want)
	}
	// …and the logged keys are the least recently used ones.
	logged := string(data)
	for _, key := range keys[:wmBand(max)] {
		if !strings.Contains(logged, fmt.Sprintf(`"session_key":"%s"`, key)) {
			t.Errorf("no eviction log line for %s (LRU batch must log every evicted session)", key)
		}
	}
}

// BenchmarkSessionInsertionBurst measures the cost of the insertion path that
// fires the LRU cascade: b.N brand-new sessions inserted past maxInMemory, each
// one calling evictIfNeeded (and, past the mark, saving the sessions it drops).
// It is the shape of the reported write storm — a burst of subagent sessions —
// and the cost the water-mark amortizes: re-run it with evictionWaterMark=0 to
// measure the historical one-in-one-out trigger.
//
// Deliberately not named *Scale*/*History*/*Subagents*: it is not part of the
// frozen docs/perf/tui-long-chat-baseline.md table (make bench filters those).
func BenchmarkSessionInsertionBurst(b *testing.B) {
	s, err := store.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	defer s.Close()

	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	sm.SetMaxInMemory(50) // production default
	sm.SetEvictionTTL(0)

	// Warm: fill the resident set up to the limit (no evictions yet).
	for i := 0; i < 50; i++ {
		sm.GetOrCreate(fmt.Sprintf("bench:warm:%03d", i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sm.GetOrCreate(fmt.Sprintf("bench:new:%06d", i))
	}
}
