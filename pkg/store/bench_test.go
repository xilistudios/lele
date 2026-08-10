package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────────────

func benchOpenStore(b *testing.B) *Store {
	b.Helper()
	path := filepath.Join(b.TempDir(), "bench.db")
	s, err := Open(path)
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { s.Close() })
	return s
}

// makeMessageJSON generates a realistic message JSON blob of ~1KB.
func makeMessageJSON(role string, idx int) string {
	msg := map[string]interface{}{
		"role":    role,
		"content": fmt.Sprintf("This is message number %d in the conversation. It contains some realistic text content to simulate a real-world message payload with moderate verbosity. The purpose is to benchmark storage read/write performance with typical message sizes.", idx),
	}
	if role == "assistant" {
		msg["reasoning_content"] = "Internal reasoning chain that adds some extra bytes to the payload..."
	}
	b, _ := json.Marshal(msg)
	return string(b)
}

// makeSessionMeta returns a SessionMeta for benchmarking.
func makeSessionMeta(key string) SessionMeta {
	now := time.Now()
	return SessionMeta{
		Key:          key,
		Name:         fmt.Sprintf("Benchmark Session %s", key),
		Mode:         "agent",
		Summary:      "A benchmark session for performance testing",
		VerboseLevel: "basic",
		Model:        "claude-sonnet-4-20250514",
		InputTokens:  1500,
		OutputTokens: 800,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// seedSession inserts n messages into a session via the repo.
func seedSession(b *testing.B, repo *SessionRepo, key string, n int) {
	b.Helper()
	meta := makeSessionMeta(key)
	if err := repo.UpsertSession(meta); err != nil {
		b.Fatalf("UpsertSession: %v", err)
	}
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgJSON := makeMessageJSON(role, i)
		if err := repo.InsertMessage(key, i, role, msgJSON, false); err != nil {
			b.Fatalf("InsertMessage: %v", err)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// JSON-file reference implementation (mirrors the old session storage)
// ──────────────────────────────────────────────────────────────────────────────

type jsonSession struct {
	Key      string   `json:"key"`
	Name     string   `json:"name"`
	Mode     string   `json:"mode"`
	Summary  string   `json:"summary"`
	Messages []string `json:"messages"` // raw JSON strings
	Created  string   `json:"created"`
	Updated  string   `json:"updated"`
}

func jsonWriteSession(dir string, key string, messages []string) error {
	s := jsonSession{
		Key:      key,
		Name:     "Benchmark Session",
		Mode:     "agent",
		Summary:  "A benchmark session for performance testing",
		Messages: messages,
		Created:  time.Now().Format(time.RFC3339Nano),
		Updated:  time.Now().Format(time.RFC3339Nano),
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, key+".json")
	return os.WriteFile(path, data, 0644)
}

func jsonReadSession(dir string, key string) (*jsonSession, error) {
	path := filepath.Join(dir, key+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s jsonSession
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func jsonListSessions(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			keys = append(keys, e.Name()[:len(e.Name())-5])
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// ──────────────────────────────────────────────────────────────────────────────
// BENCHMARK 1: Session Write (full save with N messages)
// ──────────────────────────────────────────────────────────────────────────────

func BenchmarkSessionWrite_SQLite(b *testing.B) {
	for _, msgCount := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("messages=%d", msgCount), func(b *testing.B) {
			s := benchOpenStore(b)
			repo := s.Sessions()

			// Pre-generate messages
			msgs := make([]string, msgCount)
			for i := range msgs {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				msgs[i] = makeMessageJSON(role, i)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				key := fmt.Sprintf("bench:write:%d", i)

				// Upsert metadata
				meta := makeSessionMeta(key)
				if err := repo.UpsertSession(meta); err != nil {
					b.Fatal(err)
				}

				// Insert all messages (same as saveToSQLite pattern)
				for j, msgJSON := range msgs {
					role := "user"
					if j%2 == 1 {
						role = "assistant"
					}
					if err := repo.InsertMessage(key, j, role, msgJSON, false); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func BenchmarkSessionWrite_JSON(b *testing.B) {
	for _, msgCount := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("messages=%d", msgCount), func(b *testing.B) {
			dir := b.TempDir()

			// Pre-generate messages
			msgs := make([]string, msgCount)
			for i := range msgs {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				msgs[i] = makeMessageJSON(role, i)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				key := fmt.Sprintf("bench-write-%d", i)
				if err := jsonWriteSession(dir, key, msgs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// BENCHMARK 2: Session Read (load full session with N messages)
// ──────────────────────────────────────────────────────────────────────────────

func BenchmarkSessionRead_SQLite(b *testing.B) {
	for _, msgCount := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("messages=%d", msgCount), func(b *testing.B) {
			s := benchOpenStore(b)
			repo := s.Sessions()

			seedSession(b, repo, "bench:read", msgCount)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Simulate loadFromSQLite: metadata + all messages
				meta, err := repo.GetSessionMeta("bench:read")
				if err != nil {
					b.Fatal(err)
				}
				if meta == nil {
					b.Fatal("session not found")
				}
				msgs, err := repo.LoadMessages("bench:read")
				if err != nil {
					b.Fatal(err)
				}
				if len(msgs) != msgCount {
					b.Fatalf("expected %d messages, got %d", msgCount, len(msgs))
				}
			}
		})
	}
}

func BenchmarkSessionRead_JSON(b *testing.B) {
	for _, msgCount := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("messages=%d", msgCount), func(b *testing.B) {
			dir := b.TempDir()

			// Seed: write the JSON file
			msgs := make([]string, msgCount)
			for i := range msgs {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				msgs[i] = makeMessageJSON(role, i)
			}
			if err := jsonWriteSession(dir, "bench-read", msgs); err != nil {
				b.Fatal(err)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s, err := jsonReadSession(dir, "bench-read")
				if err != nil {
					b.Fatal(err)
				}
				if len(s.Messages) != msgCount {
					b.Fatalf("expected %d messages, got %d", msgCount, len(s.Messages))
				}
			}
		})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// BENCHMARK 3: Incremental Append (add 1 message to existing session)
// This is the MOST COMMON real-world operation (each user/assistant turn).
// ──────────────────────────────────────────────────────────────────────────────

func BenchmarkSessionAppend_SQLite(b *testing.B) {
	for _, existingMsgs := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("existing=%d", existingMsgs), func(b *testing.B) {
			s := benchOpenStore(b)
			repo := s.Sessions()

			seedSession(b, repo, "bench:append", existingMsgs)
			newMsg := makeMessageJSON("user", 999)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				seq := existingMsgs + i
				if err := repo.InsertMessage("bench:append", seq, "user", newMsg, false); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSessionAppend_JSON(b *testing.B) {
	for _, existingMsgs := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("existing=%d", existingMsgs), func(b *testing.B) {
			dir := b.TempDir()

			// Seed: create initial JSON file with N messages
			msgs := make([]string, existingMsgs)
			for i := range msgs {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				msgs[i] = makeMessageJSON(role, i)
			}
			if err := jsonWriteSession(dir, "bench-append", msgs); err != nil {
				b.Fatal(err)
			}

			newMsg := makeMessageJSON("user", 999)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// JSON path: read entire file, append, rewrite entire file
				s, err := jsonReadSession(dir, "bench-append")
				if err != nil {
					b.Fatal(err)
				}
				s.Messages = append(s.Messages, newMsg)
				if err := jsonWriteSession(dir, "bench-append", s.Messages); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// BENCHMARK 4: Full Re-save (delete-and-reinsert pattern, simulating saveToSQLite)
// This is what happens on every flush in the current dual-write transition.
// ──────────────────────────────────────────────────────────────────────────────

func BenchmarkSessionFullResave_SQLite(b *testing.B) {
	for _, msgCount := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("messages=%d", msgCount), func(b *testing.B) {
			s := benchOpenStore(b)
			repo := s.Sessions()

			// Pre-generate messages
			msgs := make([]string, msgCount)
			for i := range msgs {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				msgs[i] = makeMessageJSON(role, i)
			}

			meta := makeSessionMeta("bench:resave")
			if err := repo.UpsertSession(meta); err != nil {
				b.Fatal(err)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Delete all and re-insert (exact pattern from saveToSQLite)
				if err := repo.DeleteMessagesFrom("bench:resave", 0); err != nil {
					b.Fatal(err)
				}
				for j, msgJSON := range msgs {
					role := "user"
					if j%2 == 1 {
						role = "assistant"
					}
					if err := repo.InsertMessage("bench:resave", j, role, msgJSON, false); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func BenchmarkSessionFullResave_JSON(b *testing.B) {
	for _, msgCount := range []int{10, 100, 500} {
		b.Run(fmt.Sprintf("messages=%d", msgCount), func(b *testing.B) {
			dir := b.TempDir()

			// Pre-generate messages
			msgs := make([]string, msgCount)
			for i := range msgs {
				role := "user"
				if i%2 == 1 {
					role = "assistant"
				}
				msgs[i] = makeMessageJSON(role, i)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// JSON: overwrite entire file every time (same semantics)
				if err := jsonWriteSession(dir, "bench-resave", msgs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// BENCHMARK 5: List Sessions (metadata-only scan)
// ──────────────────────────────────────────────────────────────────────────────

func BenchmarkSessionList_SQLite(b *testing.B) {
	for _, sessionCount := range []int{10, 50, 200} {
		b.Run(fmt.Sprintf("sessions=%d", sessionCount), func(b *testing.B) {
			s := benchOpenStore(b)
			repo := s.Sessions()

			for i := 0; i < sessionCount; i++ {
				meta := makeSessionMeta(fmt.Sprintf("bench:list:%04d", i))
				meta.UpdatedAt = time.Now().Add(time.Duration(i) * time.Minute)
				if err := repo.UpsertSession(meta); err != nil {
					b.Fatal(err)
				}
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				metas, err := repo.ListSessionMeta()
				if err != nil {
					b.Fatal(err)
				}
				if len(metas) != sessionCount {
					b.Fatalf("expected %d sessions, got %d", sessionCount, len(metas))
				}
			}
		})
	}
}

func BenchmarkSessionList_JSON(b *testing.B) {
	for _, sessionCount := range []int{10, 50, 200} {
		b.Run(fmt.Sprintf("sessions=%d", sessionCount), func(b *testing.B) {
			dir := b.TempDir()

			// Seed: create session files
			for i := 0; i < sessionCount; i++ {
				key := fmt.Sprintf("bench-list-%04d", i)
				msgs := []string{makeMessageJSON("user", i)}
				if err := jsonWriteSession(dir, key, msgs); err != nil {
					b.Fatal(err)
				}
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				keys, err := jsonListSessions(dir)
				if err != nil {
					b.Fatal(err)
				}
				if len(keys) != sessionCount {
					b.Fatalf("expected %d sessions, got %d", sessionCount, len(keys))
				}
			}
		})
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// BENCHMARK 6: KV Store (SQLite) vs JSON File
// ──────────────────────────────────────────────────────────────────────────────

func BenchmarkKV_SQLite(b *testing.B) {
	s := benchOpenStore(b)
	kv := s.KV()

	b.Run("Set", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if err := kv.Set(fmt.Sprintf("key:%d", i), fmt.Sprintf("value-%d", i)); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("Get", func(b *testing.B) {
		// Seed
		for i := 0; i < 100; i++ {
			kv.Set(fmt.Sprintf("get-key:%d", i), fmt.Sprintf("value-%d", i))
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, _, err := kv.Get(fmt.Sprintf("get-key:%d", i%100)); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("SetGetRoundtrip", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			k := fmt.Sprintf("rt:%d", i)
			if err := kv.Set(k, "roundtrip-value"); err != nil {
				b.Fatal(err)
			}
			if _, _, err := kv.Get(k); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkKV_JSON(b *testing.B) {
	dir := b.TempDir()

	b.Run("Set", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			k := fmt.Sprintf("key:%d", i)
			v := fmt.Sprintf("value-%d", i)
			path := filepath.Join(dir, k+".json")
			data, _ := json.Marshal(map[string]string{k: v})
			if err := os.WriteFile(path, data, 0644); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("Get", func(b *testing.B) {
		// Seed
		for i := 0; i < 100; i++ {
			k := fmt.Sprintf("get-key:%d", i)
			path := filepath.Join(dir, k+".json")
			data, _ := json.Marshal(map[string]string{k: fmt.Sprintf("value-%d", i)})
			os.WriteFile(path, data, 0644)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			k := fmt.Sprintf("get-key:%d", i%100)
			path := filepath.Join(dir, k+".json")
			data, err := os.ReadFile(path)
			if err != nil {
				b.Fatal(err)
			}
			var m map[string]string
			json.Unmarshal(data, &m)
		}
	})

	b.Run("SetGetRoundtrip", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			k := fmt.Sprintf("rt:%d", i)
			path := filepath.Join(dir, k+".json")
			data, _ := json.Marshal(map[string]string{k: "roundtrip-value"})
			os.WriteFile(path, data, 0644)
			data2, _ := os.ReadFile(path)
			var m map[string]string
			json.Unmarshal(data2, &m)
		}
	})
}

// ──────────────────────────────────────────────────────────────────────────────
// BENCHMARK 7: Cron Jobs — CRUD comparison
// ──────────────────────────────────────────────────────────────────────────────

type jsonCronJob struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Schedule string `json:"schedule"`
	Payload  string `json:"payload"`
}

func BenchmarkCronWrite_SQLite(b *testing.B) {
	s := benchOpenStore(b)
	repo := s.Cron()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		row := &CronJobRow{
			ID:          fmt.Sprintf("job-%d", i),
			Name:        fmt.Sprintf("Job %d", i),
			Enabled:     true,
			Schedule:    `{"kind":"cron","expr":"0 9 * * *"}`,
			Payload:     `{"type":"spawn","spawn":{"task":"heartbeat"}}`,
			State:       `{}`,
			Scope:       "session",
			SessionKey:  "telegram:main",
			CreatedAtMS: time.Now().UnixMilli(),
			UpdatedAtMS: time.Now().UnixMilli(),
		}
		if err := repo.UpsertCronJob(row); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCronWrite_JSON(b *testing.B) {
	dir := b.TempDir()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job := jsonCronJob{
			ID:       fmt.Sprintf("job-%d", i),
			Name:     fmt.Sprintf("Job %d", i),
			Enabled:  true,
			Schedule: `{"kind":"cron","expr":"0 9 * * *"}`,
			Payload:  `{"type":"spawn","spawn":{"task":"heartbeat"}}`,
		}
		data, _ := json.Marshal(job)
		path := filepath.Join(dir, fmt.Sprintf("job-%d.json", i))
		if err := os.WriteFile(path, data, 0644); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCronList_SQLite(b *testing.B) {
	s := benchOpenStore(b)
	repo := s.Cron()

	// Seed 50 jobs
	for i := 0; i < 50; i++ {
		repo.UpsertCronJob(&CronJobRow{
			ID: fmt.Sprintf("job-%d", i), Name: fmt.Sprintf("Job %d", i),
			Enabled: true, Schedule: `{}`, Payload: `{}`, State: `{}`,
			CreatedAtMS: int64(i), UpdatedAtMS: int64(i),
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		jobs, err := repo.ListCronJobs()
		if err != nil {
			b.Fatal(err)
		}
		if len(jobs) != 50 {
			b.Fatalf("expected 50, got %d", len(jobs))
		}
	}
}

func BenchmarkCronList_JSON(b *testing.B) {
	dir := b.TempDir()

	// Seed 50 job files
	for i := 0; i < 50; i++ {
		job := jsonCronJob{
			ID: fmt.Sprintf("job-%d", i), Name: fmt.Sprintf("Job %d", i),
			Enabled: true, Schedule: `{}`, Payload: `{}`,
		}
		data, _ := json.Marshal(job)
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("job-%d.json", i)), data, 0644)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entries, err := os.ReadDir(dir)
		if err != nil {
			b.Fatal(err)
		}
		var jobs []jsonCronJob
		for _, e := range entries {
			data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			var j jsonCronJob
			json.Unmarshal(data, &j)
			jobs = append(jobs, j)
		}
		if len(jobs) != 50 {
			b.Fatalf("expected 50, got %d", len(jobs))
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// BENCHMARK 8: Memory Usage Comparison (allocs/op)
// ──────────────────────────────────────────────────────────────────────────────

func BenchmarkMemory_SessionLoad100(b *testing.B) {
	b.Run("SQLite", func(b *testing.B) {
		s := benchOpenStore(b)
		repo := s.Sessions()
		seedSession(b, repo, "bench:mem", 100)

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			repo.GetSessionMeta("bench:mem")
			repo.LoadMessages("bench:mem")
		}
	})

	b.Run("JSON", func(b *testing.B) {
		dir := b.TempDir()
		msgs := make([]string, 100)
		for i := range msgs {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			msgs[i] = makeMessageJSON(role, i)
		}
		jsonWriteSession(dir, "bench-mem", msgs)

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			jsonReadSession(dir, "bench-mem")
		}
	})
}

func BenchmarkMemory_SessionAppend(b *testing.B) {
	b.Run("SQLite", func(b *testing.B) {
		s := benchOpenStore(b)
		repo := s.Sessions()
		seedSession(b, repo, "bench:mem-append", 100)
		newMsg := makeMessageJSON("user", 999)

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			repo.InsertMessage("bench:mem-append", 100+i, "user", newMsg, false)
		}
	})

	b.Run("JSON", func(b *testing.B) {
		dir := b.TempDir()
		msgs := make([]string, 100)
		for i := range msgs {
			msgs[i] = makeMessageJSON("user", i)
		}
		jsonWriteSession(dir, "bench-mem-append", msgs)
		newMsg := makeMessageJSON("user", 999)

		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			s, _ := jsonReadSession(dir, "bench-mem-append")
			s.Messages = append(s.Messages, newMsg)
			jsonWriteSession(dir, "bench-mem-append", s.Messages)
		}
	})
}

// ──────────────────────────────────────────────────────────────────────────────
// BENCHMARK 9: Disk Size Comparison (informational, runs once)
// ──────────────────────────────────────────────────────────────────────────────

func BenchmarkDiskSize_100Sessions_50Msgs(b *testing.B) {
	const sessionCount = 100
	const msgCount = 50

	b.Run("SQLite", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			path := filepath.Join(b.TempDir(), "size.db")
			s, err := Open(path)
			if err != nil {
				b.Fatal(err)
			}
			repo := s.Sessions()

			for j := 0; j < sessionCount; j++ {
				key := fmt.Sprintf("sess:%04d", j)
				meta := makeSessionMeta(key)
				repo.UpsertSession(meta)
				for k := 0; k < msgCount; k++ {
					role := "user"
					if k%2 == 1 {
						role = "assistant"
					}
					repo.InsertMessage(key, k, role, makeMessageJSON(role, k), false)
				}
			}

			s.Close()
			info, _ := os.Stat(path)
			b.ReportMetric(float64(info.Size()), "bytes")
			b.ReportMetric(float64(info.Size())/(sessionCount*msgCount), "bytes/msg")
		}
	})

	b.Run("JSON", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			dir := b.TempDir()
			var totalSize int64

			for j := 0; j < sessionCount; j++ {
				key := fmt.Sprintf("sess-%04d", j)
				msgs := make([]string, msgCount)
				for k := range msgs {
					role := "user"
					if k%2 == 1 {
						role = "assistant"
					}
					msgs[k] = makeMessageJSON(role, k)
				}
				jsonWriteSession(dir, key, msgs)
				info, _ := os.Stat(filepath.Join(dir, key+".json"))
				totalSize += info.Size()
			}

			b.ReportMetric(float64(totalSize), "bytes")
			b.ReportMetric(float64(totalSize)/(sessionCount*msgCount), "bytes/msg")
		}
	})
}
