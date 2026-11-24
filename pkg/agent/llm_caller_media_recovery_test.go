package agent

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

const mediaRecoveryPlaceholder = "[image omitted from context]"

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

func mediaRecoveryTestMessages() []providers.Message {
	imageURL := "data:image/png;base64," + strings.Repeat("A", 300*1024)
	return []providers.Message{
		{Role: "user", Content: strings.Repeat("word ", 40000)},
		{Role: "user", ContentParts: []providers.ContentPart{
			{Type: "text", Text: "here is the screenshot"},
			{Type: "image_url", ImageURL: &providers.ImageURL{URL: imageURL, Detail: "auto"}},
		}},
	}
}

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

func TestExecuteWithRetry_SizeRejectionEvictsMediaAndSucceeds(t *testing.T) {
	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	defer os.RemoveAll(tmpDir)

	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	sessionKey := "test-session-media-recovery"
	messages := mediaRecoveryTestMessages()

	for _, m := range messages {
		agent.Sessions.AddFullMessage(sessionKey, m)
	}

	rejection := mediaRecoverySizeRejection()
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

	if count, _ := providers.LiveMultimodalParts(received[0]); count != 1 {
		t.Fatalf("first request heavy parts = %d, want 1", count)
	}

	if count, _ := providers.LiveMultimodalParts(received[1]); count != 0 {
		t.Errorf("retried request heavy parts = %d, want 0", count)
	}
	if count, _ := providers.LiveMultimodalParts(outMsgs); count != 0 {
		t.Errorf("returned messages heavy parts = %d, want 0", count)
	}
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

	fresh := session.NewSessionManager()
	fresh.SetSessionRepo(st.Sessions())
	reloaded := fresh.GetHistory(sessionKey)
	if len(reloaded) != len(messages) {
		t.Fatalf("reloaded history len = %d, want %d", len(reloaded), len(messages))
	}
	if count, _ := providers.LiveMultimodalParts(reloaded); count != 0 {
		t.Errorf("reloaded history heavy parts = %d, want 0", count)
	}
	if got := reloaded[1].ContentParts; len(got) != 2 || got[1].Type != "text" || got[1].Text != mediaRecoveryPlaceholder {
		t.Errorf("reloaded image turn parts = %+v, want the prompt text plus the %q placeholder", got, mediaRecoveryPlaceholder)
	}

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
		{Role: "user", Media: []string{imageURL}},
	}
	for _, m := range messages {
		sm.AddFullMessage(sessionKey, m)
	}
	if err := sm.Save(sessionKey); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
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

	if count, size := providers.LiveMultimodalParts(received[0]); count != 1 || size != len(imageURL) {
		t.Fatalf("first request heavy payloads = (%d, %d), want (1, %d): the Media fixture never reached the provider",
			count, size, len(imageURL))
	}
	if !mediaRecoveryHasEntry(received[0], imageURL) {
		t.Fatalf("first request does not carry the raw data: URL in Message.Media: %+v", received[0])
	}

	if count, size := providers.LiveMultimodalParts(received[1]); count != 0 || size != 0 {
		t.Errorf("retried request heavy payloads = (%d, %d), want (0, 0)", count, size)
	}
	if count, size := providers.LiveMultimodalParts(outMsgs); count != 0 || size != 0 {
		t.Errorf("returned messages heavy payloads = (%d, %d), want (0, 0)", count, size)
	}
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

	if summarizer.summarizeCalls != 0 {
		t.Errorf("summarizer calls = %d, want 0 (media eviction must never cost an LLM call)", summarizer.summarizeCalls)
	}

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
