package session

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

func wmKey(i int) string { return fmt.Sprintf("wm:s%03d", i) }

func wmBand(max int) int {
	if max < evictionWaterMark {
		return max
	}
	return evictionWaterMark
}

type evictionObserver struct {
	sm   *SessionManager
	prev map[string]bool
}

func newEvictionObserver(sm *SessionManager) *evictionObserver {
	return &evictionObserver{sm: sm, prev: residentKeysOf(sm)}
}

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

func pinAccessTimes(sm *SessionManager, keys []string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for i, key := range keys {
		sm.accessTimes[key] = time.Unix(int64(1000+i), 0)
	}
}

func TestEvictIfNeeded_WaterMark_TriggerAndBatch(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	const max = 50
	const band = 16
	sm.SetMaxInMemory(max)
	sm.SetEvictionTTL(0)

	if wmBand(max) != band {
		t.Fatalf("fixture broken: band for max=%d is %d, want %d", max, wmBand(max), band)
	}

	obs := newEvictionObserver(sm)
	cascades, totalEvicted := 0, 0

	isCascade := func(i int) bool {
		return i >= max+band+1 && (i-(max+band+1))%band == 0
	}

	const creations = 200
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

		if resident > max+band {
			t.Fatalf("resident after creation #%d = %d, exceeds the %d bound", i, resident, max+band)
		}
		if i == max+band && resident != max+band {
			t.Fatalf("resident at the mark = %d, want %d", resident, max+band)
		}
	}

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
					if evicted != band {
						t.Fatalf("cascade at #%d evicted %d session(s), want %d", i, evicted, band)
					}
				}
			}
			if peak != wantPeak {
				t.Errorf("resident peak = %d, want %d (maxInMemory+band)", peak, wantPeak)
			}
			if leg := 4*wantPeak - tc.max; cascades >= leg {
				t.Errorf("cascades = %d, want far fewer than the %d one-in-one-out evictions", cascades, leg)
			}
			t.Logf("max=%d band=%d peak=%d cascades=%d", tc.max, band, peak, cascades)
		})
	}
}

func TestEvictIfNeeded_WaterMark_SingleResidentKeepsLegacyTrigger(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	sm.SetMaxInMemory(1)
	sm.SetEvictionTTL(0)

	obs := newEvictionObserver(sm)

	for i := 1; i <= 2; i++ {
		sm.GetOrCreate(wmKey(i))
		if evicted, resident := obs.observe(); evicted != 0 || resident != i {
			t.Fatalf("creation #%d: evicted=%d resident=%d, want 0 and %d", i, evicted, resident, i)
		}
	}
	sm.GetOrCreate(wmKey(3))
	if evicted, resident := obs.observe(); evicted != 1 || resident != 2 {
		t.Fatalf("creation #3: evicted=%d resident=%d, want 1 and 2 (legacy one-in-one-out)", evicted, resident)
	}
	if residents := residentKeysOf(sm); residents[wmKey(1)] {
		t.Error("the oldest session survived the eviction: LRU criterion broken")
	}
}

func TestEvictIfNeeded_WaterMark_ResidentLookupsNeverEvict(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	const max = 4
	sm.SetMaxInMemory(max)
	sm.SetEvictionTTL(0)

	mark := max + wmBand(max)
	for i := 1; i <= mark; i++ {
		sm.GetOrCreate(wmKey(i))
	}
	if got := len(residentKeysOf(sm)); got != mark {
		t.Fatalf("fixture broken: resident = %d, want %d", got, mark)
	}

	obs := newEvictionObserver(sm)
	for i := 0; i < 200; i++ {
		sm.GetOrCreate(wmKey(1 + i%mark))
		if evicted, resident := obs.observe(); evicted != 0 || resident != mark {
			t.Fatalf("resident lookup #%d: evicted=%d resident=%d, want 0 and %d", i, evicted, resident, mark)
		}
	}

	if err := sm.Save(wmKey(1)); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !sm.EvictSession(wmKey(1)) {
		t.Fatal("EvictSession(wmKey(1)) = false")
	}
	obs = newEvictionObserver(sm)
	sm.GetOrCreate(wmKey(1))
	if evicted, resident := obs.observe(); evicted != 0 || resident != mark {
		t.Fatalf("cold load: evicted=%d resident=%d, want 0 and %d", evicted, resident, mark)
	}

	obs = newEvictionObserver(sm)
	sm.GetOrCreate(wmKey(mark + 1))
	if evicted, resident := obs.observe(); evicted != wmBand(max) || resident != max+1 {
		t.Errorf("insertion past the mark: evicted=%d resident=%d, want %d and %d", evicted, resident, wmBand(max), max+1)
	}
}

func TestEvictIfNeeded_WaterMark_BatchKeepsLRUOrder(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	const max = 2
	sm.SetMaxInMemory(max)
	sm.SetEvictionTTL(0)

	keys := []string{wmKey(1), wmKey(2), wmKey(3), wmKey(4)}
	for _, key := range keys {
		sm.GetOrCreate(key)
	}
	pinAccessTimes(sm, keys)

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
	for _, key := range []string{wmKey(1), wmKey(2)} {
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

func TestEvictIfNeeded_WaterMark_BatchStillPersistsEveryEvictedSession(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	const max = 4
	sm.SetMaxInMemory(max)
	sm.SetEvictionTTL(0)

	const perSession = 2
	keys := make([]string, 0, max+wmBand(max)+1)
	for i := 1; i <= max+wmBand(max); i++ {
		key := wmKey(i)
		keys = append(keys, key)
		sm.AddMessage(key, "user", "hello")
		sm.AddMessage(key, "assistant", "hi")
	}
	pinAccessTimes(sm, keys)

	obs := newEvictionObserver(sm)
	sm.GetOrCreate(wmKey(len(keys) + 1))
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
	sm.mu.Lock()
	old := time.Now().Add(-2 * time.Minute)
	for i := 1; i <= 3; i++ {
		sm.accessTimes[wmKey(i)] = old
	}
	sm.mu.Unlock()

	obs := newEvictionObserver(sm)
	sm.GetOrCreate(wmKey(4))
	evicted, resident := obs.observe()
	if evicted != 3 {
		t.Fatalf("idle sweep evicted %d session(s), want 3 (the TTL pass must not be gated by the water-mark)", evicted)
	}
	if resident != 1 {
		t.Fatalf("resident after the idle sweep = %d, want 1", resident)
	}
}

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
		logger.SetLogsPath(prevBase)
		logger.SetQuiet(false)
		logger.SetLevel(prevLevel)
	})

	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	const max = 4
	sm.SetMaxInMemory(max)
	sm.SetEvictionTTL(0)

	keys := make([]string, 0, max+wmBand(max)+1)
	for i := 1; i <= max+wmBand(max); i++ {
		key := wmKey(i)
		keys = append(keys, key)
		sm.GetOrCreate(key)
	}
	pinAccessTimes(sm, keys)
	sm.GetOrCreate(wmKey(len(keys) + 1))

	logPath := filepath.Join(logDir, "info-"+time.Now().Format("2006-01-02")+".log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read info log: %v", err)
	}
	lines := strings.Count(string(data), `"message":"LRU evicted session"`)
	if want := wmBand(max); lines != want {
		t.Fatalf("eviction log lines = %d, want %d (one per evicted session)", lines, want)
	}
	logged := string(data)
	for _, key := range keys[:wmBand(max)] {
		if !strings.Contains(logged, fmt.Sprintf(`"session_key":"%s"`, key)) {
			t.Errorf("no eviction log line for %s (LRU batch must log every evicted session)", key)
		}
	}
}

func BenchmarkSessionInsertionBurst(b *testing.B) {
	s, err := store.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	defer s.Close()

	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())
	sm.SetMaxInMemory(50)
	sm.SetEvictionTTL(0)

	for i := 0; i < 50; i++ {
		sm.GetOrCreate(fmt.Sprintf("bench:warm:%03d", i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sm.GetOrCreate(fmt.Sprintf("bench:new:%06d", i))
	}
}
