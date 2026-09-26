package channels

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
)

// This file covers the *identity* half of GET /api/v1/chat/history: how a
// resident message gets its wire id (`<sessionKey>:<key>[-<occurrence>]`) and
// how `before_id` is resolved back to a position.
//
// The endpoint used to hash the FULL content of every resident message with
// SHA-256 on every request (the WebUI polls it every ~4s while a turn streams),
// which made a 3.5k-message session cost ~15MB of copying+hashing to return 50
// messages. The key is now a bounded sample (residentMessageKeyBytes) and the
// occurrence index keeps only indices — see the block comment in rest_chat.go.

// repeatContent returns a deterministic `length`-byte payload. It stands in for
// message content in the tests: long enough to be sampled, stable across runs.
func repeatContent(length int) string {
	const unit = "0123456789abcdef"
	return strings.Repeat(unit, length/len(unit)+1)[:length]
}

// withByteAt returns s with the byte at idx replaced, i.e. two strings of the
// SAME length that differ at exactly one position.
func withByteAt(s string, idx int, b byte) string {
	return s[:idx] + string(b) + s[idx+1:]
}

// ---------------------------------------------------------------------------
// Identity key
// ---------------------------------------------------------------------------

// TestResidentMessageKeyContract pins the two properties occurrence indexing
// depends on: identical messages MUST collapse onto one key (otherwise
// duplicates lose their `-<n>` disambiguation) and messages differing in
// content MUST NOT (otherwise two different messages share a wire id).
func TestResidentMessageKeyContract(t *testing.T) {
	long := repeatContent(1000)

	base := providers.Message{Role: "assistant", Content: long, ToolCallID: "call-1"}

	cases := []struct {
		name string
		msg  providers.Message
		same bool
	}{
		{
			name: "identical message",
			msg:  providers.Message{Role: "assistant", Content: long, ToolCallID: "call-1"},
			same: true,
		},
		{
			name: "differ in the middle (shared long prefix and suffix)",
			msg:  providers.Message{Role: "assistant", Content: withByteAt(long, len(long)/2, 'X'), ToolCallID: "call-1"},
			same: false,
		},
		{
			name: "differ at the tail",
			msg:  providers.Message{Role: "assistant", Content: withByteAt(long, len(long)-1, 'X'), ToolCallID: "call-1"},
			same: false,
		},
		{
			name: "differ in length only",
			msg:  providers.Message{Role: "assistant", Content: long + "x", ToolCallID: "call-1"},
			same: false,
		},
		{
			name: "differ in role only",
			msg:  providers.Message{Role: "user", Content: long, ToolCallID: "call-1"},
			same: false,
		},
		{
			name: "differ in tool_call_id only",
			msg:  providers.Message{Role: "assistant", Content: long, ToolCallID: "call-2"},
			same: false,
		},
	}

	baseKey := residentMessageKey(base)
	for _, tc := range cases {
		if same := residentMessageKey(tc.msg) == baseKey; same != tc.same {
			t.Errorf("%s: keys equal = %v, want %v", tc.name, same, tc.same)
		}
	}

	// The tool-call identity fields contribute on their own: same role/content
	// with a different tool call name (or id) must not collapse.
	withCall := func(id, name string) providers.Message {
		return providers.Message{Role: "assistant", Content: long, ToolCallID: "call-1",
			ToolCalls: []providers.ToolCall{{ID: id, Name: name}}}
	}
	if residentMessageKey(withCall("tc-1", "exec")) == residentMessageKey(withCall("tc-1", "read_file")) {
		t.Error("tool call name must contribute to the key")
	}
	if residentMessageKey(withCall("tc-1", "exec")) == residentMessageKey(withCall("tc-2", "exec")) {
		t.Error("tool call id must contribute to the key")
	}
}

// TestResidentMessageKeyBoundedWork proves the key reads a BOUNDED slice of the
// content: a change inside the sampled windows changes the key, a change
// outside them does not. That is the exact reason a 40MB session no longer
// costs 40MB of hashing per request — and it documents the resulting failure
// mode (two messages of equal length differing only in an unsampled span are
// treated as duplicates; they still get distinct ids and each id still resolves
// to its own message, see TestChatHistory_CollidingKeyKeepsIDsUnique).
func TestResidentMessageKeyBoundedWork(t *testing.T) {
	long := repeatContent(1000) // > residentKeySampleSpan, so it is sampled
	base := providers.Message{Role: "assistant", Content: long}
	baseKey := residentMessageKey(base)

	sampled := []int{0, len(long) / 4, len(long) / 2, 3 * len(long) / 4, len(long) - 1}
	for _, idx := range sampled {
		mutated := providers.Message{Role: "assistant", Content: withByteAt(long, idx, 'X')}
		if residentMessageKey(mutated) == baseKey {
			t.Errorf("change at sampled offset %d did not change the key", idx)
		}
	}

	// Offset 150 sits between the head window (0..32) and the quarter window
	// (250..282): outside the read set on purpose.
	unsampled := providers.Message{Role: "assistant", Content: withByteAt(long, 150, 'X')}
	if residentMessageKey(unsampled) != baseKey {
		t.Fatalf("key changed for a change outside the sampled span: the key must not read the whole content")
	}

	// Content at or below the sample budget is hashed whole, so nothing is
	// "outside" it.
	short := repeatContent(residentKeySampleSpan)
	shortMutated := providers.Message{Role: "assistant", Content: withByteAt(short, 150, 'X')}
	if residentMessageKey(shortMutated) == residentMessageKey(providers.Message{Role: "assistant", Content: short}) {
		t.Error("content within the sample budget must be hashed whole")
	}

	// Allocation-free, even for a multi-megabyte message: no per-message copy
	// of the content happens anywhere in the identity path.
	huge := providers.Message{Role: "assistant", Content: repeatContent(4 << 20)}
	if allocs := testing.AllocsPerRun(20, func() { _ = residentMessageKeyBytes(&huge) }); allocs != 0 {
		t.Fatalf("residentMessageKeyBytes allocated %.1f times for a 4MB message, want 0", allocs)
	}
}

// TestResidentMessageKeyGroupingMatchesLegacySha256 proves the OCCURRENCE
// SCHEME is unchanged: on realistic content the new bounded key partitions the
// session exactly like the legacy full-content SHA-256 did, i.e. two messages
// collapse onto one identity iff their full content is identical. (The key
// VALUE differs; the wire id format and the grouping do not.)
func TestResidentMessageKeyGroupingMatchesLegacySha256(t *testing.T) {
	long := repeatContent(1000)
	msgs := []providers.Message{
		{Role: "user", Content: "same question"},
		{Role: "assistant", Content: "same answer"},
		{Role: "user", Content: "second question"},
		{Role: "assistant", Content: "same answer"}, // duplicate
		{Role: "tool", Content: long, ToolCallID: "call-1"},
		{Role: "assistant", Content: long + " tail", ToolCallID: "call-1"}, // length differs only
		{Role: "assistant", Content: long},                                 // same role+length as the next line
		{Role: "assistant", Content: withByteAt(long, 500, 'X')},           // middle differs only
		{Role: "assistant", Content: "same answer"},                        // third duplicate
		{Role: "user", Content: "same question"},                           // duplicate user turn
	}

	for i := range msgs {
		for j := range msgs {
			wantSame := legacyResidentKey(msgs[i]) == legacyResidentKey(msgs[j])
			gotSame := residentMessageKey(msgs[i]) == residentMessageKey(msgs[j])
			if wantSame != gotSame {
				t.Fatalf("grouping differs for messages %d and %d: legacy same=%v, bounded same=%v", i, j, wantSame, gotSame)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Cursor decoding
// ---------------------------------------------------------------------------

func TestParseResidentHistoryCursor(t *testing.T) {
	sessionKey := "native:client-1:session"
	msg := providers.Message{Role: "assistant", Content: "hello"}
	key := residentMessageKey(msg)

	cases := []struct {
		name       string
		cursor     string
		wantOK     bool
		wantKey    string
		wantOccurr int
	}{
		{name: "plain id", cursor: sessionKey + ":" + key, wantOK: true, wantKey: key, wantOccurr: 0},
		{name: "occurrence suffix", cursor: sessionKey + ":" + key + "-3", wantOK: true, wantKey: key, wantOccurr: 3},
		{name: "explicit zero occurrence", cursor: sessionKey + ":" + key + "-0", wantOK: true, wantKey: key, wantOccurr: 0},
		{name: "empty", cursor: ""},
		{name: "evicted cursor", cursor: "evicted:12"},
		{name: "foreign session", cursor: "native:other:" + key, wantOK: false},
		{name: "session prefix only", cursor: sessionKey + ":"},
		{name: "short key", cursor: sessionKey + ":" + key[:10]},
		{name: "non hex key", cursor: sessionKey + ":zzzzzzzzzzzzzzzz"},
		{name: "negative occurrence", cursor: sessionKey + ":" + key + "--1"},
		{name: "non numeric occurrence", cursor: sessionKey + ":" + key + "-abc"},
	}

	for _, tc := range cases {
		gotKey, gotOccurr, ok := parseResidentHistoryCursor(sessionKey, tc.cursor)
		if ok != tc.wantOK {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if gotKey != mustDecodeKey(t, tc.wantKey) {
			t.Errorf("%s: key = %x, want %s", tc.name, gotKey[:], tc.wantKey)
		}
		if gotOccurr != tc.wantOccurr {
			t.Errorf("%s: occurrence = %d, want %d", tc.name, gotOccurr, tc.wantOccurr)
		}
	}
}

func mustDecodeKey(t *testing.T, hexKey string) residentKey {
	t.Helper()
	raw, err := hex.DecodeString(hexKey)
	if err != nil || len(raw) != residentKeyBytes {
		t.Fatalf("could not decode key %q: %v", hexKey, err)
	}
	var key residentKey
	copy(key[:], raw)
	return key
}

// ---------------------------------------------------------------------------
// Handler behaviour on duplicate / near-duplicate content
// ---------------------------------------------------------------------------

// TestChatHistory_DuplicateContentDistinctIDsAndCursor verifies the contract
// the WebUI depends on (issue #324): two messages with IDENTICAL content get
// distinct ids, and each id pings back to its own position as a cursor.
func TestChatHistory_DuplicateContentDistinctIDsAndCursor(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID + ":dup-bounded"
	ts.channel.auth.TrackSessionKey(ts.clientID, sessionKey)

	answer := "a repeated answer that is long enough to be sampled by the bounded key " + repeatContent(512)
	ts.loop.histories[sessionKey] = []providers.Message{
		{Role: "user", Content: "first question"},
		{Role: "assistant", Content: answer},
		{Role: "user", Content: "second question"},
		{Role: "assistant", Content: answer},
	}

	payload := getChatHistory(t, ts, sessionKey, "?limit=50")
	if len(payload.Messages) != 4 {
		t.Fatalf("len(messages) = %d, want 4", len(payload.Messages))
	}

	key := residentMessageKey(ts.loop.histories[sessionKey][1])
	seen := map[string]bool{}
	for i, m := range payload.Messages {
		if seen[m.ID] {
			t.Fatalf("duplicate id %q at index %d", m.ID, i)
		}
		seen[m.ID] = true
	}
	// First copy keeps the legacy id, the repeat carries the occurrence suffix.
	wantFirst, wantSecond := sessionKey+":"+key, sessionKey+":"+key+"-1"
	if payload.Messages[1].ID != wantFirst || payload.Messages[3].ID != wantSecond {
		t.Fatalf("duplicate ids = [%q, %q], want [%q, %q]",
			payload.Messages[1].ID, payload.Messages[3].ID, wantFirst, wantSecond)
	}

	// Cursor = second occurrence → the three messages before it.
	page := getChatHistory(t, ts, sessionKey, "?limit=50&before_id="+url.QueryEscape(payload.Messages[3].ID))
	if len(page.Messages) != 3 {
		t.Fatalf("before_id=second occurrence: %d messages, want 3", len(page.Messages))
	}
	// Cursor = first occurrence → only the first question.
	page = getChatHistory(t, ts, sessionKey, "?limit=50&before_id="+url.QueryEscape(payload.Messages[1].ID))
	if len(page.Messages) != 1 || page.Messages[0].Content != "first question" {
		t.Fatalf("before_id=first occurrence: %+v, want exactly the first question", page.Messages)
	}
}

// TestChatHistory_LongPrefixMiddleDifferenceKeepsDistinctKeys pins that the
// bounded key still discriminates the most common near-duplicate shape: two
// messages sharing a long prefix (and length) that differ in the middle. They
// must get DIFFERENT keys — i.e. ids without an occurrence suffix — and each id
// must resolve to its own message.
func TestChatHistory_LongPrefixMiddleDifferenceKeepsDistinctKeys(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID + ":prefix-siblings"
	ts.channel.auth.TrackSessionKey(ts.clientID, sessionKey)

	shared := "shared prefix: " + repeatContent(2000)
	first := shared
	second := withByteAt(shared, len(shared)/2, 'X')

	ts.loop.histories[sessionKey] = []providers.Message{
		{Role: "user", Content: "run it"},
		{Role: "assistant", Content: first},
		{Role: "user", Content: "again"},
		{Role: "assistant", Content: second},
	}

	payload := getChatHistory(t, ts, sessionKey, "?limit=50")
	if len(payload.Messages) != 4 {
		t.Fatalf("len(messages) = %d, want 4", len(payload.Messages))
	}

	id1, id2 := payload.Messages[1].ID, payload.Messages[3].ID
	if id1 == id2 {
		t.Fatalf("middle difference collapsed the ids: both are %q", id1)
	}
	if strings.HasSuffix(id1, "-1") || strings.HasSuffix(id2, "-1") {
		t.Fatalf("middle difference was treated as a duplicate: ids = [%q, %q]", id1, id2)
	}

	// Each of the two ids is its own cursor.
	page := getChatHistory(t, ts, sessionKey, "?limit=50&before_id="+url.QueryEscape(id2))
	if len(page.Messages) != 3 || page.Messages[1].Content != first {
		t.Fatalf("before_id=second copy returned %d messages (content[1] %q), want the first copy at index 1",
			len(page.Messages), page.Messages[1].Content)
	}
	page = getChatHistory(t, ts, sessionKey, "?limit=50&before_id="+url.QueryEscape(id1))
	if len(page.Messages) != 1 || page.Messages[0].Content != "run it" {
		t.Fatalf("before_id=first copy returned %+v, want exactly the first user message", page.Messages)
	}
}

// TestChatHistory_CollidingKeyKeepsIDsUnique documents the bounded key's only
// weak spot: two messages of the same length differing ONLY outside the sampled
// span share a key. The response must stay well formed — unique ids, one cursor
// per message — because a collision is meant to degrade into "these two are
// occurrences of one identity", never into a lost or unreachable message.
func TestChatHistory_CollidingKeyKeepsIDsUnique(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID + ":key-collision"
	ts.channel.auth.TrackSessionKey(ts.clientID, sessionKey)

	shared := repeatContent(1000)
	// Offset 150 is outside the sampled windows (0, 250, 500, 750, 968).
	first := shared
	second := withByteAt(shared, 150, 'X')

	if residentMessageKey(providers.Message{Role: "assistant", Content: first}) !=
		residentMessageKey(providers.Message{Role: "assistant", Content: second}) {
		t.Fatal("test premise broken: the sampled offsets changed, pick an unsampled one")
	}

	ts.loop.histories[sessionKey] = []providers.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", Content: first},
		{Role: "assistant", Content: second},
	}

	payload := getChatHistory(t, ts, sessionKey, "?limit=50")
	if len(payload.Messages) != 3 {
		t.Fatalf("len(messages) = %d, want 3 (a collision must never drop a message)", len(payload.Messages))
	}
	if payload.Messages[1].ID == payload.Messages[2].ID {
		t.Fatalf("colliding messages share the id %q", payload.Messages[1].ID)
	}
	if payload.Messages[1].ID+"-1" != payload.Messages[2].ID {
		t.Fatalf("ids = [%q, %q], want the second to be the first + -1", payload.Messages[1].ID, payload.Messages[2].ID)
	}

	// Both ids remain usable cursors, each at its own position.
	for i, cursor := range []string{payload.Messages[1].ID, payload.Messages[2].ID} {
		page := getChatHistory(t, ts, sessionKey, "?limit=50&before_id="+url.QueryEscape(cursor))
		if len(page.Messages) != i+1 {
			t.Fatalf("cursor %q returned %d messages, want %d", cursor, len(page.Messages), i+1)
		}
	}
}

// TestChatHistory_UnmatchedCursorServesNewestPage pins the fallback path that
// the cursor rewrite touched: a cursor that resolves nowhere (pruned, forged,
// from another session, or from an older server release) must behave exactly
// like it used to — serve the newest resident page.
func TestChatHistory_UnmatchedCursorServesNewestPage(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID + ":unmatched-cursor"
	ts.channel.auth.TrackSessionKey(ts.clientID, sessionKey)

	msgs := make([]providers.Message, 40)
	for i := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = providers.Message{Role: role, Content: fmt.Sprintf("msg-%02d", i)}
	}
	ts.loop.histories[sessionKey] = msgs

	cursors := []string{
		"",
		"total-garbage",
		"native:someone-else:0123456789abcdef",
		sessionKey + ":0000000000000000",   // valid shape, no such message
		sessionKey + ":0123456789abcdef-7", // valid shape, no such occurrence
		"native:" + ts.clientID + ":oops",  // same client, another session
	}
	for _, cursor := range cursors {
		payload := getChatHistory(t, ts, sessionKey, "?limit=10&before_id="+url.QueryEscape(cursor))
		if len(payload.Messages) != 10 {
			t.Fatalf("cursor %q: %d messages, want 10 (newest page)", cursor, len(payload.Messages))
		}
		if payload.Messages[9].Content != "msg-39" {
			t.Fatalf("cursor %q: last message = %q, want msg-39 (newest page)", cursor, payload.Messages[9].Content)
		}
		if !payload.HasMore {
			t.Fatalf("cursor %q: has_more = false, want true", cursor)
		}
	}
}

// TestChatHistory_CursorStableAcrossAppendsAndPruning checks the property the
// content key exists for: an id minted on one request keeps resolving after the
// session grew and after older messages were pruned (the cache evicted).
func TestChatHistory_CursorStableAcrossAppendsAndPruning(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID + ":cursor-stability"
	ts.channel.auth.TrackSessionKey(ts.clientID, sessionKey)

	msgs := make([]providers.Message, 30)
	for i := range msgs {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs[i] = providers.Message{Role: role, Content: fmt.Sprintf("msg-%02d", i)}
	}
	ts.loop.histories[sessionKey] = msgs

	first := getChatHistory(t, ts, sessionKey, "?limit=5")
	if len(first.Messages) != 5 {
		t.Fatalf("first page: %d messages, want 5", len(first.Messages))
	}
	cursor := first.Messages[0].ID // oldest of the resident page
	if first.Messages[0].Content != "msg-25" {
		t.Fatalf("first page starts at %q, want msg-25", first.Messages[0].Content)
	}

	// Session grows...
	grown := append(append([]providers.Message{}, msgs...), providers.Message{Role: "user", Content: "appended"})
	// ...and the oldest messages leave the resident window (evicted to SQLite).
	ts.loop.histories[sessionKey] = grown[10:]

	page := getChatHistory(t, ts, sessionKey, "?limit=5&before_id="+url.QueryEscape(cursor))
	if len(page.Messages) != 5 {
		t.Fatalf("after append+prune: %d messages, want 5", len(page.Messages))
	}
	if page.Messages[4].Content != "msg-24" {
		t.Fatalf("after append+prune: page ends at %q, want msg-24 (the message before the cursor)", page.Messages[4].Content)
	}
}

// getChatHistory issues a history request and decodes the (always 200) payload.
func getChatHistory(t *testing.T, ts *nativeTestServer, sessionKey, query string) *ChatHistoryResponse {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet,
		ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/history"+query, nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var payload ChatHistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	return &payload
}

// legacyResidentKey is the pre-refactor discriminator: SHA-256 over the full
// content. Kept in the test only, as the reference the bounded key must agree
// with on grouping (and as the baseline the benchmark measures against).
func legacyResidentKey(msg providers.Message) string {
	hasher := sha256.New()
	hasher.Write([]byte(msg.Role))
	hasher.Write([]byte(msg.Content))
	if msg.ToolCallID != "" {
		hasher.Write([]byte(msg.ToolCallID))
	}
	for _, tc := range msg.ToolCalls {
		hasher.Write([]byte(tc.ID))
		hasher.Write([]byte(tc.Name))
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)[:8])
}

// legacyResidentPass replays, message for message, the work the previous
// handler did for a whole session before it could emit a page:
//
//	sha256 over the full content + a hex string per message + a value copy of
//	every message into the response slice.
//
// It exists so the benchmark below can put a number on the change instead of
// asserting it in prose.
func legacyResidentPass(history []providers.Message) int {
	type indexedMessage struct {
		id  string
		msg providers.Message
	}
	valid := make([]indexedMessage, 0, len(history))
	occurrences := make(map[string]int, len(history))
	for _, msg := range history {
		if !residentHistoryVisible(&msg) {
			continue
		}
		key := legacyResidentKey(msg)
		occurrence := occurrences[key]
		occurrences[key]++
		valid = append(valid, indexedMessage{id: msg.Role + key + strconv.Itoa(occurrence), msg: msg})
	}
	return len(valid)
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

// benchSessionMessages builds a session of n messages with realistic content
// sizes (~2KB per message, tool calls on every 4th message, a few repeated
// bodies) so a sub-benchmark's session size translates into a real content
// volume: messages=20000 is ~40MB of transcript.
func benchSessionMessages(n int) []providers.Message {
	msgs := make([]providers.Message, 0, n)
	repeated := "the same assistant answer " + repeatContent(1024)
	for i := 0; i < n; i++ {
		switch i % 4 {
		case 0:
			msgs = append(msgs, providers.Message{
				Role: "user", Content: "user prompt " + strconv.Itoa(i) + " " + repeatContent(512),
			})
		case 1:
			msgs = append(msgs, providers.Message{
				Role: "assistant", Content: repeatContent(2048),
				ToolCalls: []providers.ToolCall{{ID: "call-" + strconv.Itoa(i), Type: "function", Name: "read_file"}},
			})
		case 2:
			msgs = append(msgs, providers.Message{
				Role: "tool", Content: repeatContent(4096), ToolCallID: "call-" + strconv.Itoa(i-1),
			})
		default:
			// Repeated body: exercises the occurrence index (and the id suffix)
			// the way a chat session does.
			msgs = append(msgs, providers.Message{Role: "assistant", Content: repeated})
		}
	}
	return msgs
}

// benchSessionMessagesWithBody builds n messages whose bodies are all bodyLen
// bytes long, so the only dimension that changes between benchmark runs is the
// session's total content volume.
func benchSessionMessagesWithBody(n, bodyLen int) []providers.Message {
	msgs := make([]providers.Message, 0, n)
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, providers.Message{Role: role, Content: strconv.Itoa(i) + repeatContent(bodyLen)})
	}
	return msgs
}

// chatHistoryBenchLoop serves the benchmark session through a history VIEW, the
// way the production agent loop does (GetHistoryView). The HTTP test stub copies
// the slice on every call, which for a 20k-message session would add ~4MB of
// memcpy per request and swamp the measurement.
type chatHistoryBenchLoop struct {
	*nativeTestAgentLoop
	view []providers.Message
}

func (m *chatHistoryBenchLoop) GetSessionHistory(string) []providers.Message { return m.view }

// newChatHistoryBenchTarget builds the minimum NativeChannel handleChatHistory
// needs (auth for the ownership check + agent loop) and returns the channel, the
// session key and the client id header value.
func newChatHistoryBenchTarget(b *testing.B, msgs []providers.Message) (*NativeChannel, string, string) {
	b.Helper()

	cfg := config.DefaultConfig()
	auth, err := NewAuthManager(&cfg.Channels.Native, b.TempDir())
	if err != nil {
		b.Fatalf("NewAuthManager() error = %v", err)
	}
	pending, err := auth.GeneratePIN("Bench")
	if err != nil {
		b.Fatalf("GeneratePIN() error = %v", err)
	}
	client, _, _, err := auth.PairWithPIN(pending.PIN, "Bench")
	if err != nil {
		b.Fatalf("PairWithPIN() error = %v", err)
	}

	loop := newChatHistoryBenchLoop(cfg, msgs)
	sessionKey := "native:" + client.ClientID + ":bench"
	loop.histories[sessionKey] = msgs

	return &NativeChannel{auth: auth, agentLoop: loop}, sessionKey, client.ClientID
}

func newChatHistoryBenchLoop(cfg *config.Config, msgs []providers.Message) *chatHistoryBenchLoop {
	return &chatHistoryBenchLoop{nativeTestAgentLoop: newNativeTestAgentLoop(cfg), view: msgs}
}

// BenchmarkChatHistoryResidentPoll measures one GET /api/v1/chat/history
// request of the kind the WebUI issues every ~4s while a session streams
// (no cursor). Every sub-benchmark returns the same page from a session that is
// up to 20x larger and carries up to ~40MB of content, none of which is hashed,
// copied or scanned per byte:
//
//   - the resident walk costs one bounded key per message (residentMessageKeyBytes),
//   - only the returned page (limit messages) is materialized, formatted into
//     ids and JSON-encoded,
//   - the response size is set by limit, not by the session.
//
// Run with -benchtime=20x to see the request cost without per-op noise, or
// compare the messages=1000 and messages=20000 rows: the delta is the price of
// the index walk alone.
func BenchmarkChatHistoryResidentPoll(b *testing.B) {
	for _, sessionMessages := range []int{1000, 20000} {
		msgs := benchSessionMessages(sessionMessages)
		for _, limit := range []int{50, 200} {
			b.Run(fmt.Sprintf("messages=%d/limit=%d", sessionMessages, limit), func(b *testing.B) {
				channel, sessionKey, clientID := newChatHistoryBenchTarget(b, msgs)

				req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/history", nil)
				req.SetPathValue("sessionKey", sessionKey)
				req.Header.Set("X-Client-Id", clientID)
				req.URL.RawQuery = "limit=" + strconv.Itoa(limit)
				rec := httptest.NewRecorder()

				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					rec.Body.Reset()
					channel.handleChatHistory(rec, req)
					if rec.Code != http.StatusOK {
						b.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
					}
				}
			})
		}
	}
}

// BenchmarkChatHistoryResidentPageCursor measures the same request one page
// older (the "load older" scroll): a resident cursor is resolved from the
// (key, occurrence) pair instead of by minting an id for every message.
func BenchmarkChatHistoryResidentPageCursor(b *testing.B) {
	msgs := benchSessionMessages(20000)
	b.Run("messages=20000/limit=50", func(b *testing.B) {
		channel, sessionKey, clientID := newChatHistoryBenchTarget(b, msgs)

		// Oldest resident message of the newest page = its id is the cursor.
		cursor := residentMessageID(sessionKey, residentMessageKey(msgs[len(msgs)-50]), 0)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/chat/history", nil)
		req.SetPathValue("sessionKey", sessionKey)
		req.Header.Set("X-Client-Id", clientID)
		req.URL.RawQuery = "limit=50&before_id=" + url.QueryEscape(cursor)
		rec := httptest.NewRecorder()

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			rec.Body.Reset()
			channel.handleChatHistory(rec, req)
			if rec.Code != http.StatusOK {
				b.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
		}
	})
}

// BenchmarkChatHistoryLegacyResidentPass is the reference baseline: the same
// session put through the work the previous handler did before it could emit a
// page (SHA-256 over every message's full content, a hex string per message, a
// value copy of every message). It is the measurement behind the claim that the
// endpoint used to be O(session bytes) and is now O(page) plus a bounded walk.
func BenchmarkChatHistoryLegacyResidentPass(b *testing.B) {
	for _, sessionMessages := range []int{1000, 20000} {
		msgs := benchSessionMessages(sessionMessages)
		b.Run(fmt.Sprintf("messages=%d", sessionMessages), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := legacyResidentPass(msgs); got != len(msgs) {
					b.Fatalf("legacy pass saw %d messages, want %d", got, len(msgs))
				}
			}
		})
	}
}

// residentIdentityPass replays what the new handler does before it emits a
// page: one bounded key per visible message and a fixed-size occurrence index.
// No id string is built, no message is copied — ids are minted only for the
// ≤limit messages that are actually returned.
//
// It is the counterpart of legacyResidentPass, so the pair of benchmarks below
// measures the same walk over the same session and the ratio between them is
// the whole change, not an artefact of one of them doing less work.
func residentIdentityPass(history []providers.Message) int {
	occurrences := make(map[residentKey]int, len(history))
	valid := 0
	for i := range history {
		msg := &history[i]
		if !residentHistoryVisible(msg) {
			continue
		}
		key := residentMessageKeyBytes(msg)
		occurrences[key]++
		valid++
	}
	return valid
}

// BenchmarkChatHistoryResidentIdentityPass is the new pass, head to head with
// BenchmarkChatHistoryLegacyResidentPass above. Read the two side by side: the
// session, the visibility filter and the occurrence index are identical; only
// the key changes (bounded sample + fixed-size key instead of SHA-256 over the
// full body + a hex string per message), and with it the per-request cost goes
// from scaling with the session's BYTES to scaling with its MESSAGE COUNT.
func BenchmarkChatHistoryResidentIdentityPass(b *testing.B) {
	for _, sessionMessages := range []int{1000, 20000} {
		msgs := benchSessionMessages(sessionMessages)
		b.Run(fmt.Sprintf("messages=%d", sessionMessages), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if got := residentIdentityPass(msgs); got != len(msgs) {
					b.Fatalf("resident pass saw %d messages, want %d", got, len(msgs))
				}
			}
		})
	}
}

// BenchmarkResidentIdentityPassContentSize holds the message count fixed (2000)
// and varies the session's content volume 20x, running both passes over the
// SAME sessions. Read the columns together:
//
//   - legacy scales with the bytes it hashes (SHA-256 over every full body),
//   - new scales with the message count only (a bounded 160-byte sample per
//     message), so a 40MB session costs what a 2MB session costs plus memory
//     traffic.
//
// -benchtime=20x keeps it quick; SetBytes reports the session's MB throughput,
// which is the number that stops being meaningful for the new pass.
func BenchmarkResidentIdentityPassContentSize(b *testing.B) {
	const sessionMessages = 2000
	for _, bodyLen := range []int{2 << 10, 40 << 10} {
		msgs := benchSessionMessagesWithBody(sessionMessages, bodyLen)
		volume := int64(sessionMessages * bodyLen)
		for _, impl := range []struct {
			name string
			pass func([]providers.Message) int
		}{
			{"new", residentIdentityPass},
			{"legacy", legacyResidentPass},
		} {
			b.Run(strconv.Itoa(bodyLen)+"/"+impl.name, func(b *testing.B) {
				b.SetBytes(volume)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if got := impl.pass(msgs); got != len(msgs) {
						b.Fatalf("%s pass saw %d messages, want %d", impl.name, got, len(msgs))
					}
				}
			})
		}
	}
}
