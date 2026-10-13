// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

// Tests for the reactive media-recovery escalation in executeWithRetry: when a
// request is rejected with a context/size error and the history carries
// base64 image parts, the parts are evicted (in the request AND in the stored
// session) and the call is retried immediately, without paying for a
// summarization LLM call.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/providers/common"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/store"
)

// mediaRecoveryMockSessionManager is the summarizer double for the media
// recovery tests: it records summarizeSession calls instead of running the
// real compaction pipeline. It implements the full sessionManager interface so
// it can be swapped into al.sessionManager.
type mediaRecoveryMockSessionManager struct {
	summarizeCalls int
}

func (m *mediaRecoveryMockSessionManager) maybeSummarize(agent *AgentInstance, sessionKey, channel, chatID string) *SummarizeStats {
	return nil
}

func (m *mediaRecoveryMockSessionManager) summarizeSession(agent *AgentInstance, sessionKey string) *SummarizeStats {
	m.summarizeCalls++
	return &SummarizeStats{BeforeMessages: 1, AfterMessages: 1}
}

func (m *mediaRecoveryMockSessionManager) summarizeSessionWithError(agent *AgentInstance, sessionKey string) (*SummarizeStats, error) {
	return m.summarizeSession(agent, sessionKey), nil
}

func (m *mediaRecoveryMockSessionManager) AddTokenCounts(sessionKey string, inputTokens, outputTokens int) {
}

func (m *mediaRecoveryMockSessionManager) EstimateTokens(messages []providers.Message) int { return 0 }

func (m *mediaRecoveryMockSessionManager) RegisterSessionCancel(sessionKey string, cancel context.CancelFunc) func() {
	return func() {}
}

func (m *mediaRecoveryMockSessionManager) CancelSession(sessionKey string) int        { return 0 }
func (m *mediaRecoveryMockSessionManager) IsSessionProcessing(sessionKey string) bool { return false }
func (m *mediaRecoveryMockSessionManager) ModelForSession(a *AgentInstance, k string) string {
	return ""
}

// mediaRecoveryPlaceholder is the text ReduceImageParts substitutes for an
// evicted data: part when the message carries no textual content of its own
// (providers.omittedPartPlaceholder, unexported across packages).
const mediaRecoveryPlaceholder = "[image omitted from context]"

// mediaRecoverySizeRejection builds the structured router rejection that
// triggers the escalation: a 400 whose body carries the gateway's body-cap
// prose. T2's "request body too large" pattern group classifies it both as a
// context overflow and as a request-size error.
func mediaRecoverySizeRejection() error {
	return &providers.FailoverError{
		Reason:   providers.FailoverFormat,
		Provider: "llmproxy",
		Model:    "test-model",
		Status:   400,
		Wrapped: &common.APIError{
			StatusCode: 400,
			Body:       `{"error":{"message":"failed to read request body: http: request body too large","type":"invalid_request_error"}}`,
		},
	}
}

// mediaRecoveryTestMessages returns the poisoned payload shape: a large text
// body plus a freshly read image (a few hundred KiB of synthetic base64).
func mediaRecoveryTestMessages() []providers.Message {
	imageURL := "data:image/png;base64," + strings.Repeat("A", 300*1024)
	return []providers.Message{
		{Role: "user", Content: strings.Repeat("word ", 40000)}, // ~200 KiB of text
		{Role: "user", ContentParts: []providers.ContentPart{
			{Type: "text", Text: "here is the screenshot"},
			{Type: "image_url", ImageURL: &providers.ImageURL{URL: imageURL, Detail: "auto"}},
		}},
	}
}

// mediaRecoveryScriptedProvider returns a provider whose first call fails with
// the given error and whose second call succeeds, recording a snapshot of the
// messages of every call.
func mediaRecoveryScriptedProvider(firstCallErr error, received *[][]providers.Message) *llmRunnerMockLLMProvider {
	return &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, msgs []providers.Message, toolDefs []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
			snapshot := make([]providers.Message, len(msgs))
			copy(snapshot, msgs)
			*received = append(*received, snapshot)
			if len(*received) == 1 {
				return nil, firstCallErr
			}
			return &providers.LLMResponse{Content: "ok after recovery", ToolCalls: []providers.ToolCall{}}, nil
		},
	}
}

// TestExecuteWithRetry_SizeRejectionEvictsMediaAndSucceeds pins the primary
// escalation: a size rejection on a media-bearing payload evicts the parts
// from the retried request (no image_url part survives, the placeholder is
// injected) and costs zero summarization calls.
func TestExecuteWithRetry_SizeRejectionEvictsMediaAndSucceeds(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	sessionKey := "test-session-media-recovery"
	messages := mediaRecoveryTestMessages()

	// The session holds the same history: the escalation de-poisons it so the
	// next turn cannot rebuild the same payload.
	for _, m := range messages {
		agent.Sessions.AddFullMessage(sessionKey, m)
	}

	rejection := mediaRecoverySizeRejection()
	// Pin the classifier contract the escalation relies on.
	if !providers.IsContextOverflowError(rejection) {
		t.Fatal("harness: rejection must classify as a context overflow")
	}
	if !providers.IsRequestSizeError(rejection) {
		t.Fatal("harness: rejection must classify as a request-size error")
	}

	var received [][]providers.Message
	agent.Provider = mediaRecoveryScriptedProvider(rejection, &received)
	summarizer := &mediaRecoveryMockSessionManager{}
	al.sessionManager = summarizer

	caller := newLLMCaller(al)
	resp, outMsgs, err := caller.executeWithRetry(llmCallOptions{
		ctx:        context.Background(),
		agent:      agent,
		toolDefs:   []providers.ToolDefinition{},
		model:      "test-model",
		sessionKey: sessionKey,
		channel:    "test-channel",
		chatID:     "test-chat",
	}, messages)

	if err != nil {
		t.Fatalf("executeWithRetry: %v", err)
	}
	if resp == nil || resp.Content != "ok after recovery" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(received) != 2 {
		t.Fatalf("provider calls = %d, want exactly 2 (evict, then retry)", len(received))
	}
	if summarizer.summarizeCalls != 0 {
		t.Errorf("summarizer calls = %d, want 0 (media eviction must never cost an LLM call)", summarizer.summarizeCalls)
	}

	// Harness sanity: the rejected request DID carry the heavy part.
	if count, _ := providers.LiveMultimodalParts(received[0]); count != 1 {
		t.Fatalf("first request heavy parts = %d, want 1", count)
	}

	// The retried request carries no heavy part anywhere ...
	if count, _ := providers.LiveMultimodalParts(received[1]); count != 0 {
		t.Errorf("retried request heavy parts = %d, want 0", count)
	}
	if count, _ := providers.LiveMultimodalParts(outMsgs); count != 0 {
		t.Errorf("returned messages heavy parts = %d, want 0", count)
	}
	// ... and the evicted image was replaced by the text placeholder.
	placeholderFound := false
	for _, part := range received[1][len(received[1])-1].ContentParts {
		if part.Type == "text" && part.Text == mediaRecoveryPlaceholder {
			placeholderFound = true
		}
	}
	if !placeholderFound {
		t.Errorf("retried request carries no %q placeholder part: %+v",
			mediaRecoveryPlaceholder, received[1][len(received[1])-1].ContentParts)
	}
}

// TestExecuteWithRetry_SizeRejectionCleansStoredSession pins the durable half
// of the escalation: the recovery must rewrite the stored session (through
// markModified + Save, not a full rewrite) so the persisted rows no longer
// carry the blobs. (The durable proof is the LoadMessagesFromSeq row bytes;
// the fresh-manager reload check below is weak, see its comment.)
func TestExecuteWithRetry_SizeRejectionCleansStoredSession(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	sm := session.NewSessionManager()
	sm.SetSessionRepo(st.Sessions())
	agent.Sessions = sm

	sessionKey := "test-session-media-cleanup"
	messages := mediaRecoveryTestMessages()
	for _, m := range messages {
		sm.AddFullMessage(sessionKey, m)
	}
	// Persist the seeded rows first: only then does the escalation's Save take
	// the incremental (markModified-driven UPDATE) path — a never-persisted
	// session would be full-rewritten by any Save and would not prove that
	// markModified reached SQLite.
	if err := sm.Save(sessionKey); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	var received [][]providers.Message
	agent.Provider = mediaRecoveryScriptedProvider(mediaRecoverySizeRejection(), &received)
	summarizer := &mediaRecoveryMockSessionManager{}
	al.sessionManager = summarizer

	caller := newLLMCaller(al)
	_, _, err = caller.executeWithRetry(llmCallOptions{
		ctx:        context.Background(),
		agent:      agent,
		toolDefs:   []providers.ToolDefinition{},
		model:      "test-model",
		sessionKey: sessionKey,
		channel:    "test-channel",
		chatID:     "test-chat",
	}, messages)
	if err != nil {
		t.Fatalf("executeWithRetry: %v", err)
	}
	if summarizer.summarizeCalls != 0 {
		t.Errorf("summarizer calls = %d, want 0", summarizer.summarizeCalls)
	}

	hist := sm.GetHistory(sessionKey)
	if len(hist) != len(messages) {
		t.Fatalf("history len = %d, want %d (eviction must not delete messages)", len(hist), len(messages))
	}
	if count, _ := providers.LiveMultimodalParts(hist); count != 0 {
		t.Errorf("stored history heavy parts = %d, want 0", count)
	}
	placeholderFound := false
	for _, part := range hist[1].ContentParts {
		if part.Type == "text" && part.Text == mediaRecoveryPlaceholder {
			placeholderFound = true
		}
	}
	if !placeholderFound {
		t.Errorf("stored image message carries no %q placeholder: %+v", mediaRecoveryPlaceholder, hist[1].ContentParts)
	}

	// WEAK check — not proof of our rewrite: content_parts never survives a
	// reload today (Message.MarshalJSON is pointer-receiver but the save path
	// marshals values into a "content_parts" key that UnmarshalJSON ignores),
	// so a fresh manager drops heavy parts regardless of what the escalation
	// did. The durable proof is the LoadMessagesFromSeq row bytes below. Kept
	// as documentation of current loader behaviour.
	fresh := session.NewSessionManager()
	fresh.SetSessionRepo(st.Sessions())
	reloaded := fresh.GetHistory(sessionKey)
	if len(reloaded) != len(messages) {
		t.Fatalf("reloaded history len = %d, want %d", len(reloaded), len(messages))
	}
	if count, _ := providers.LiveMultimodalParts(reloaded); count != 0 {
		t.Errorf("reloaded history heavy parts = %d, want 0 (WEAK check: reload drops content_parts by construction; the durable proof is the row bytes below)", count)
	}

	// Byte-level proof against the store itself: the 300 KiB blob must be
	// gone from the rows and the placeholder must be on disk. (Reload-based
	// assertions alone cannot pin ContentParts: the Message JSON round-trip
	// through SQLite drops them pre-existing, see pkg/session test comments.)
	rows, rErr := st.Sessions().LoadMessagesFromSeq(sessionKey, 0)
	if rErr != nil {
		t.Fatalf("LoadMessagesFromSeq: %v", rErr)
	}
	if len(rows) != len(messages) {
		t.Fatalf("persisted rows = %d, want %d", len(rows), len(messages))
	}
	blob := strings.Repeat("A", 300*1024)
	for i, row := range rows {
		if strings.Contains(row, blob) {
			t.Errorf("row %d still carries the base64 blob after recovery", i)
		}
	}
	if !strings.Contains(rows[1], mediaRecoveryPlaceholder) {
		t.Errorf("row 1 does not carry the %q placeholder: %.120s...", mediaRecoveryPlaceholder, rows[1])
	}
}

// TestExecuteWithRetry_TextOverflowStillSummarizesUnchanged pins the
// fall-through: a text-only overflow (no media to evict) must keep taking the
// pre-existing summarization path exactly as before.
func TestExecuteWithRetry_TextOverflowStillSummarizesUnchanged(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	sessionKey := "test-session-text-overflow"
	messages := []providers.Message{
		{Role: "user", Content: strings.Repeat("token ", 5000)},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "and now a longer question"},
	}
	for _, m := range messages {
		agent.Sessions.AddFullMessage(sessionKey, m)
	}

	overflow := fmt.Errorf("prompt is too long: 250000 tokens > 200000 maximum")
	if !providers.IsContextOverflowError(overflow) {
		t.Fatal("harness: overflow must classify as a context overflow")
	}
	if providers.IsRequestSizeError(overflow) {
		t.Fatal("harness: a token overflow must NOT classify as a request-size error")
	}

	var received [][]providers.Message
	agent.Provider = mediaRecoveryScriptedProvider(overflow, &received)
	summarizer := &mediaRecoveryMockSessionManager{}
	al.sessionManager = summarizer

	caller := newLLMCaller(al)
	resp, _, err := caller.executeWithRetry(llmCallOptions{
		ctx:        context.Background(),
		agent:      agent,
		toolDefs:   []providers.ToolDefinition{},
		model:      "test-model",
		sessionKey: sessionKey,
		channel:    "test-channel",
		chatID:     "test-chat",
	}, messages)
	if err != nil {
		t.Fatalf("executeWithRetry: %v", err)
	}
	if resp == nil || resp.Content != "ok after recovery" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if summarizer.summarizeCalls != 1 {
		t.Errorf("summarizer calls = %d, want exactly 1 (text overflow must still summarize)", summarizer.summarizeCalls)
	}
	if len(received) != 2 {
		t.Fatalf("provider calls = %d, want exactly 2", len(received))
	}
	// Nothing was evicted: every original text body survives in the rebuilt
	// request (the real ContextBuilder prepends a system prompt and may
	// rewrite framing, so assert on containment, not on shape).
	var rebuilt strings.Builder
	for _, m := range received[1] {
		rebuilt.WriteString(m.Content)
		rebuilt.WriteString("\n")
	}
	for i, m := range messages {
		if !strings.Contains(rebuilt.String(), m.Content) {
			t.Errorf("message %d content missing from the rebuilt request (something was evicted)", i)
		}
	}
}

// mediaRecoveryHasEntry reports whether any message in msgs carries the exact
// Media entry (raw string scan, carrier by carrier — deliberately not via
// LiveMultimodalParts, which would also count ContentParts).
func mediaRecoveryHasEntry(msgs []providers.Message, entry string) bool {
	for _, m := range msgs {
		for _, e := range m.Media {
			if e == entry {
				return true
			}
		}
	}
	return false
}

// TestExecuteWithRetry_SizeRejectionEvictsMediaCarriedImage pins the reactive
// escalation for the SECOND carrier: the oversized request's heavy payload
// travels in Message.Media (a "data:image/…" entry), not in ContentParts —
// the exact shape commit 5388100 exists for, and the only shape the shipped
// tests used to cover from pkg/providers alone (round-2 review, N2).
//
// Three properties are pinned together, across the boundary:
//  1. the rejected request really carried the Media payload (anti-vacuity);
//  2. the retried request carries no heavy payload in EITHER carrier and the
//     user text survives;
//  3. the recovery costs zero summarization calls (a media eviction must
//     never pay for an LLM call);
//  4. the rewrite is durable: the stored row bytes lose the base64 blob
//     (row bytes, not a reload — Message.MarshalJSON/UnmarshalJSON drop the
//     media/content_parts round-trip on this base, see pkg/session tests) and
//     the live session holds no heavy payload anymore.
//
// Vision/strip check: this fixture's model is NOT resolved as vision-capable
// (ProviderModelConfig.Vision is unset for test-provider:test-model, so
// getSupportsImages → false), but that cannot make the test vacuous:
// stripContentParts only drops image_url/video_url ContentParts and never
// reads Message.Media, and it runs in runLLMIteration (before executeWithRetry)
// and per fallback candidate — neither of which this test enters, since it
// calls executeWithRetry directly with no fallback candidates. The guard in
// assertion 1 proves it empirically: the raw data: URL reached the provider.
func TestExecuteWithRetry_SizeRejectionEvictsMediaCarriedImage(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	sm := session.NewSessionManager()
	sm.SetSessionRepo(st.Sessions())
	agent.Sessions = sm

	sessionKey := "test-session-media-carried"
	blob := strings.Repeat("B", 300*1024)
	imageURL := "data:image/png;base64," + blob
	messages := []providers.Message{
		{Role: "user", Content: "here is the screenshot"},
		// The media-only message case: no Content, no ContentParts, the whole
		// payload in Message.Media.
		{Role: "user", Media: []string{imageURL}},
	}
	for _, m := range messages {
		sm.AddFullMessage(sessionKey, m)
	}
	// Persist the seeded rows first: only then does the escalation's Save take
	// the incremental (markModified-driven UPDATE) path — a never-persisted
	// session would be full-rewritten by any Save and would not prove that
	// markModified reached SQLite.
	if err := sm.Save(sessionKey); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	// Durable pre-state: the blob really is on disk before the recovery.
	rowsBefore, rErr := st.Sessions().LoadMessagesFromSeq(sessionKey, 0)
	if rErr != nil {
		t.Fatalf("LoadMessagesFromSeq (pre-state): %v", rErr)
	}
	if len(rowsBefore) != len(messages) {
		t.Fatalf("seeded rows = %d, want %d", len(rowsBefore), len(messages))
	}
	if !strings.Contains(rowsBefore[1], blob) {
		t.Fatalf("harness: seeded row 1 does not carry the blob: %.120s...", rowsBefore[1])
	}
	// Fixture shape for the PR body (visible with -v only; blob truncated).
	t.Logf("fixture row 1: %.100s...", rowsBefore[1])

	rejection := mediaRecoverySizeRejection()
	var received [][]providers.Message
	agent.Provider = mediaRecoveryScriptedProvider(rejection, &received)
	summarizer := &mediaRecoveryMockSessionManager{}
	al.sessionManager = summarizer

	caller := newLLMCaller(al)
	_, outMsgs, err := caller.executeWithRetry(llmCallOptions{
		ctx:        context.Background(),
		agent:      agent,
		toolDefs:   []providers.ToolDefinition{},
		model:      "test-model",
		sessionKey: sessionKey,
		channel:    "test-channel",
		chatID:     "test-chat",
	}, messages)
	if err != nil {
		t.Fatalf("executeWithRetry: %v", err)
	}
	if len(received) != 2 {
		t.Fatalf("provider calls = %d, want exactly 2 (evict, then retry)", len(received))
	}

	// 1. Anti-vacuity: the rejected request DID carry the Media payload,
	//    counted by the same predicate the evictor and estimator use, and
	//    present verbatim in the messages the provider received.
	if count, size := providers.LiveMultimodalParts(received[0]); count != 1 || size != len(imageURL) {
		t.Fatalf("first request heavy payloads = (%d, %d), want (1, %d): the Media fixture never reached the provider",
			count, size, len(imageURL))
	}
	if !mediaRecoveryHasEntry(received[0], imageURL) {
		t.Fatalf("first request does not carry the raw data: URL in Message.Media: %+v", received[0])
	}

	// 2. The retried request carries no heavy payload in EITHER carrier ...
	if count, size := providers.LiveMultimodalParts(received[1]); count != 0 || size != 0 {
		t.Errorf("retried request heavy payloads = (%d, %d), want (0, 0)", count, size)
	}
	if count, size := providers.LiveMultimodalParts(outMsgs); count != 0 || size != 0 {
		t.Errorf("returned messages heavy payloads = (%d, %d), want (0, 0)", count, size)
	}
	// ... a raw scan confirms no data: entry survived in either carrier ...
	for i, m := range received[1] {
		for _, e := range m.Media {
			if strings.HasPrefix(e, "data:") {
				t.Errorf("retried request message %d still carries heavy Media entry %.60q...", i, e)
			}
		}
		for _, part := range m.ContentParts {
			if part.ImageURL != nil && strings.HasPrefix(part.ImageURL.URL, "data:") {
				t.Errorf("retried request message %d still carries a heavy image_url part", i)
			}
			if part.VideoURL != nil && strings.HasPrefix(part.VideoURL.URL, "data:") {
				t.Errorf("retried request message %d still carries a heavy video_url part", i)
			}
		}
	}
	// ... the media-only message got the placeholder (it may not end up with
	// empty Content, no parts and no media), and the user text is intact.
	placeholderFound := false
	textFound := false
	for _, m := range received[1] {
		if strings.Contains(m.Content, "here is the screenshot") {
			textFound = true
		}
		for _, part := range m.ContentParts {
			if part.Type == "text" && part.Text == mediaRecoveryPlaceholder {
				placeholderFound = true
			}
		}
	}
	if !placeholderFound {
		t.Errorf("retried request carries no %q placeholder for the media-only message", mediaRecoveryPlaceholder)
	}
	if !textFound {
		t.Errorf("retried request lost the user text %q", "here is the screenshot")
	}

	// 3. The escalation must never pay for a summarization call when media
	//    can be evicted — that is the whole point of the reactive path.
	if summarizer.summarizeCalls != 0 {
		t.Errorf("summarizer calls = %d, want 0 (media eviction must never cost an LLM call)", summarizer.summarizeCalls)
	}

	// 4. Durable half: the live session and the stored rows both lost the
	//    blob. (In-memory checks are not proof on their own — ReduceImageParts
	//    works on a copy — so the row bytes below are what pins the
	//    de-poisoning of the next turn.)
	hist := sm.GetHistory(sessionKey)
	if len(hist) != len(messages) {
		t.Fatalf("history len = %d, want %d (eviction must not delete messages)", len(hist), len(messages))
	}
	if count, _ := providers.LiveMultimodalParts(hist); count != 0 {
		t.Errorf("live session history heavy payloads = %d, want 0", count)
	}
	if len(hist[1].Media) != 0 {
		t.Errorf("live session message Media = %v, want empty", hist[1].Media)
	}
	rows, rErr := st.Sessions().LoadMessagesFromSeq(sessionKey, 0)
	if rErr != nil {
		t.Fatalf("LoadMessagesFromSeq: %v", rErr)
	}
	if len(rows) != len(messages) {
		t.Fatalf("persisted rows = %d, want %d", len(rows), len(messages))
	}
	for i, row := range rows {
		if strings.Contains(row, blob) {
			t.Errorf("row %d still carries the base64 blob after media recovery", i)
		}
	}
	if strings.Contains(rows[1], `"media"`) {
		t.Errorf("row 1 still carries a media field after recovery: %.120s...", rows[1])
	}
	if !strings.Contains(rows[1], mediaRecoveryPlaceholder) {
		t.Errorf("row 1 does not carry the %q placeholder: %.120s...", mediaRecoveryPlaceholder, rows[1])
	}
	t.Logf("recovered row 1: %s", rows[1])
}
