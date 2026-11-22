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

func repeatContent(length int) string {
	const unit = "0123456789abcdef"
	return strings.Repeat(unit, length/len(unit)+1)[:length]
}

func withByteAt(s string, idx int, b byte) string {
	return s[:idx] + string(b) + s[idx+1:]
}

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

func TestResidentMessageKeyBoundedWork(t *testing.T) {
	long := repeatContent(1000)
	base := providers.Message{Role: "assistant", Content: long}
	baseKey := residentMessageKey(base)

	sampled := []int{0, len(long) / 4, len(long) / 2, 3 * len(long) / 4, len(long) - 1}
	for _, idx := range sampled {
		mutated := providers.Message{Role: "assistant", Content: withByteAt(long, idx, 'X')}
		if residentMessageKey(mutated) == baseKey {
			t.Errorf("change at sampled offset %d did not change the key", idx)
		}
	}

	unsampled := providers.Message{Role: "assistant", Content: withByteAt(long, 150, 'X')}
	if residentMessageKey(unsampled) != baseKey {
		t.Fatalf("key changed for a change outside the sampled span: the key must not read the whole content")
	}

	short := repeatContent(residentKeySampleSpan)
	shortMutated := providers.Message{Role: "assistant", Content: withByteAt(short, 150, 'X')}
	if residentMessageKey(shortMutated) == residentMessageKey(providers.Message{Role: "assistant", Content: short}) {
		t.Error("content within the sample budget must be hashed whole")
	}

	huge := providers.Message{Role: "assistant", Content: repeatContent(4 << 20)}
	if allocs := testing.AllocsPerRun(20, func() { _ = residentMessageKeyBytes(&huge) }); allocs != 0 {
		t.Fatalf("residentMessageKeyBytes allocated %.1f times for a 4MB message, want 0", allocs)
	}
}

func TestResidentMessageKeyGroupingMatchesLegacySha256(t *testing.T) {
	long := repeatContent(1000)
	msgs := []providers.Message{
		{Role: "user", Content: "same question"},
		{Role: "assistant", Content: "same answer"},
		{Role: "user", Content: "second question"},
		{Role: "assistant", Content: "same answer"},
		{Role: "tool", Content: long, ToolCallID: "call-1"},
		{Role: "assistant", Content: long + " tail", ToolCallID: "call-1"},
		{Role: "assistant", Content: long},
		{Role: "assistant", Content: withByteAt(long, 500, 'X')},
		{Role: "assistant", Content: "same answer"},
		{Role: "user", Content: "same question"},
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
	wantFirst, wantSecond := sessionKey+":"+key, sessionKey+":"+key+"-1"
	if payload.Messages[1].ID != wantFirst || payload.Messages[3].ID != wantSecond {
		t.Fatalf("duplicate ids = [%q, %q], want [%q, %q]",
			payload.Messages[1].ID, payload.Messages[3].ID, wantFirst, wantSecond)
	}

	page := getChatHistory(t, ts, sessionKey, "?limit=50&before_id="+url.QueryEscape(payload.Messages[3].ID))
	if len(page.Messages) != 3 {
		t.Fatalf("before_id=second occurrence: %d messages, want 3", len(page.Messages))
	}
	page = getChatHistory(t, ts, sessionKey, "?limit=50&before_id="+url.QueryEscape(payload.Messages[1].ID))
	if len(page.Messages) != 1 || page.Messages[0].Content != "first question" {
		t.Fatalf("before_id=first occurrence: %+v, want exactly the first question", page.Messages)
	}
}

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

func TestChatHistory_CollidingKeyKeepsIDsUnique(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID + ":key-collision"
	ts.channel.auth.TrackSessionKey(ts.clientID, sessionKey)

	shared := repeatContent(1000)
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

	for i, cursor := range []string{payload.Messages[1].ID, payload.Messages[2].ID} {
		page := getChatHistory(t, ts, sessionKey, "?limit=50&before_id="+url.QueryEscape(cursor))
		if len(page.Messages) != i+1 {
			t.Fatalf("cursor %q returned %d messages, want %d", cursor, len(page.Messages), i+1)
		}
	}
}

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
		sessionKey + ":0000000000000000",
		sessionKey + ":0123456789abcdef-7",
		"native:" + ts.clientID + ":oops",
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
	cursor := first.Messages[0].ID
	if first.Messages[0].Content != "msg-25" {
		t.Fatalf("first page starts at %q, want msg-25", first.Messages[0].Content)
	}

	grown := append(append([]providers.Message{}, msgs...), providers.Message{Role: "user", Content: "appended"})
	ts.loop.histories[sessionKey] = grown[10:]

	page := getChatHistory(t, ts, sessionKey, "?limit=5&before_id="+url.QueryEscape(cursor))
	if len(page.Messages) != 5 {
		t.Fatalf("after append+prune: %d messages, want 5", len(page.Messages))
	}
	if page.Messages[4].Content != "msg-24" {
		t.Fatalf("after append+prune: page ends at %q, want msg-24 (the message before the cursor)", page.Messages[4].Content)
	}
}

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
			msgs = append(msgs, providers.Message{Role: "assistant", Content: repeated})
		}
	}
	return msgs
}

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

type chatHistoryBenchLoop struct {
	*nativeTestAgentLoop
	view []providers.Message
}

func (m *chatHistoryBenchLoop) GetSessionHistory(string) []providers.Message { return m.view }

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

func BenchmarkChatHistoryResidentPageCursor(b *testing.B) {
	msgs := benchSessionMessages(20000)
	b.Run("messages=20000/limit=50", func(b *testing.B) {
		channel, sessionKey, clientID := newChatHistoryBenchTarget(b, msgs)

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
