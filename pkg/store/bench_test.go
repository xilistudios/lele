package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Smoke benchmark comparing the current persistence pattern (full JSON
// rewrite on every session save) against the SQLite pattern (one row
// per message: incremental INSERT for new messages, UPDATE of the last
// row during streaming).
//
// Motivation: docs/sqlite-storage-migration-plan.md §1. Today every
// Save rewrites the whole session JSON (up to 10,000 messages); the
// new pattern only touches the rows that changed.

// messageJSON mirrors the on-disk shape of a session message. It is
// defined inline (instead of importing pkg/session) to avoid coupling
// the store package to session internals.
type messageJSON struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

// sessionJSON mirrors the on-disk shape of a session file.
type sessionJSON struct {
	Key      string        `json:"key"`
	Name     string        `json:"name"`
	Mode     string        `json:"mode"`
	Messages []messageJSON `json:"messages"`
}

// benchMessageContent is a realistic ~200 byte message body.
const benchMessageContent = "The migration plan replaces the full-file JSON rewrite with " +
	"per-row SQLite writes: new messages are appended with a single INSERT and the " +
	"message being streamed is flushed by updating only its own row."

// newBenchSession builds a simulated session with n messages.
func newBenchSession(n int) *sessionJSON {
	s := &sessionJSON{
		Key:      "agent:telegram:42",
		Name:     "benchmark session",
		Mode:     "agent",
		Messages: make([]messageJSON, 0, n),
	}
	ts := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		s.Messages = append(s.Messages, messageJSON{
			Role:      role,
			Content:   benchMessageContent,
			Timestamp: ts.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
		})
	}
	return s
}

// benchJSONSaveSession performs one full save of s exactly like the
// current hot path: marshal the whole session and write it atomically
// (temp file + rename).
func benchJSONSaveSession(b *testing.B, dir string, s *sessionJSON) {
	b.Helper()

	data, err := json.Marshal(s)
	if err != nil {
		b.Fatalf("marshal session: %v", err)
	}

	tmp := filepath.Join(dir, "session.json.tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		b.Fatalf("write temp file: %v", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "session.json")); err != nil {
		b.Fatalf("rename temp file: %v", err)
	}
}

// BenchmarkJSONSaveSession_1000Messages measures the current hot path
// with a mid-size session: every save rewrites the entire 1000-message
// JSON file.
func BenchmarkJSONSaveSession_1000Messages(b *testing.B) {
	dir := b.TempDir()
	s := newBenchSession(1000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchJSONSaveSession(b, dir, s)
	}
}

// BenchmarkJSONSaveSession_10000Messages measures the current hot path
// at maxStoredMessages scale: every save rewrites the entire
// 10,000-message JSON file.
func BenchmarkJSONSaveSession_10000Messages(b *testing.B) {
	dir := b.TempDir()
	s := newBenchSession(10000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchJSONSaveSession(b, dir, s)
	}
}

// seedBenchStore opens a fresh Store in a temporary directory, inserts
// a parent session row and initialMessages rows for session_key
// 'bench' (seq 1..initialMessages) inside one setup transaction, and
// registers cleanup. The transaction keeps the one-time setup fast; it
// is not representative of the measured hot paths.
func seedBenchStore(b *testing.B, initialMessages int) *Store {
	b.Helper()

	s, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() {
		if err := s.Close(); err != nil {
			b.Errorf("Close: %v", err)
		}
	})

	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.DB().Begin()
	if err != nil {
		b.Fatalf("begin setup transaction: %v", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT INTO sessions(key, name, mode, created_at, updated_at) VALUES(?, ?, ?, ?, ?)`,
		"bench", "benchmark session", "agent", now, now,
	); err != nil {
		b.Fatalf("insert session row: %v", err)
	}

	for seq := 1; seq <= initialMessages; seq++ {
		msg := messageJSON{
			Role:      "user",
			Content:   benchMessageContent,
			Timestamp: now,
		}
		raw, err := json.Marshal(msg)
		if err != nil {
			b.Fatalf("marshal message: %v", err)
		}
		if _, err := tx.Exec(
			`INSERT INTO session_messages(session_key, seq, role, content, message, created_at)
			 VALUES(?, ?, ?, ?, ?, ?)`,
			"bench", seq, msg.Role, msg.Content, string(raw), now,
		); err != nil {
			b.Fatalf("seed message %d: %v", seq, err)
		}
	}

	if err := tx.Commit(); err != nil {
		b.Fatalf("commit setup transaction: %v", err)
	}
	return s
}

// BenchmarkSQLiteAppendMessage measures the new hot path: appending a
// single new message with one INSERT while the session already holds
// 1000 messages.
func BenchmarkSQLiteAppendMessage(b *testing.B) {
	s := seedBenchStore(b, 1000)
	db := s.DB()

	now := time.Now().UTC().Format(time.RFC3339)
	msg := messageJSON{Role: "assistant", Content: benchMessageContent, Timestamp: now}
	raw, err := json.Marshal(msg)
	if err != nil {
		b.Fatalf("marshal message: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		seq := 1000 + i + 1
		if _, err := db.Exec(
			`INSERT INTO session_messages(session_key, seq, role, content, message, created_at)
			 VALUES(?, ?, ?, ?, ?, ?)`,
			"bench", seq, msg.Role, msg.Content, string(raw), now,
		); err != nil {
			b.Fatalf("insert message seq %d: %v", seq, err)
		}
	}
}

// BenchmarkSQLiteUpdateLastMessage measures the streaming flush path:
// updating the last message row in place while the session already
// holds 1000 messages.
func BenchmarkSQLiteUpdateLastMessage(b *testing.B) {
	s := seedBenchStore(b, 1000)
	db := s.DB()

	now := time.Now().UTC().Format(time.RFC3339)
	msg := messageJSON{Role: "assistant", Content: benchMessageContent, Timestamp: now}
	raw, err := json.Marshal(msg)
	if err != nil {
		b.Fatalf("marshal message: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.Exec(
			`UPDATE session_messages SET content = ?, message = ?
			 WHERE session_key = ? AND seq = ?`,
			msg.Content, string(raw), "bench", 1000,
		); err != nil {
			b.Fatalf("update last message: %v", err)
		}
	}
}
