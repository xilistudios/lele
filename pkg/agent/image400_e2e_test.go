package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/store"
	"github.com/xilistudios/lele/pkg/tools"
)

const image400Reply = "The images fit inside the request budget."

const image400EnvelopeAllowance = 1024

const image400OmittedPlaceholder = "[image omitted from context]"

const image400StreamFieldOverhead = 32

type image400Request struct {
	path          string
	rejected      bool
	contentLength int64
	acceptedLen   int
	body          []byte
}

type image400Router struct {
	cap     int64
	mu      sync.Mutex
	records []image400Request
}

func (rt *image400Router) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := image400Request{path: r.URL.Path, contentLength: r.ContentLength}
		w.Header().Set("Content-Type", "application/json")
		body := http.MaxBytesReader(w, r.Body, rt.cap)
		raw, err := io.ReadAll(body)
		if err != nil {
			rec.rejected = true
			rt.append(rec)
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
				"message": "failed to read request body: " + err.Error(),
				"type":    "invalid_request_error",
			}})
			return
		}
		rec.acceptedLen = len(raw)
		rec.body = raw
		rt.append(rec)

		var probe struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(raw, &probe)
		if probe.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			chunk, _ := json.Marshal(map[string]any{
				"id":      "chatcmpl-image400",
				"object":  "chat.completion.chunk",
				"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant", "content": image400Reply}, "finish_reason": "stop"}},
			})
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-image400",
			"object": "chat.completion",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": image400Reply},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	})
}

func (rt *image400Router) append(rec image400Request) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.records = append(rt.records, rec)
}

func (rt *image400Router) snapshot() []image400Request {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := make([]image400Request, len(rt.records))
	copy(out, rt.records)
	return out
}

func (rt *image400Router) counts() (rejected, accepted int) {
	for _, rec := range rt.snapshot() {
		if rec.rejected {
			rejected++
		} else {
			accepted++
		}
	}
	return rejected, accepted
}

type image400Env struct {
	al     *AgentLoop
	runner *llmRunnerImpl
	agent  *AgentInstance
	store  *store.Store
	router *image400Router
}

func newImage400Env(t *testing.T, routerCap int64) *image400Env {
	t.Helper()
	router := &image400Router{cap: routerCap}
	srv := httptest.NewServer(router.handler())
	t.Cleanup(srv.Close)

	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	t.Cleanup(func() { _ = os.RemoveAll(tmpDir) })

	cfg := al.cfg()
	if cfg.Providers == nil {
		cfg.Providers = &config.ProvidersConfig{}
	}
	if cfg.Providers.Named == nil {
		cfg.Providers.Named = map[string]config.NamedProviderConfig{}
	}
	cfg.Providers.Named["test-provider"] = config.NamedProviderConfig{
		ProviderConfig: config.ProviderConfig{APIKey: "k", APIBase: srv.URL + "/v1"},
		Models: map[string]config.ProviderModelConfig{
			"test-model": {Model: "test-model", Vision: true, Video: true, ContextWindow: 128000},
		},
	}
	if !getSupportsImages(cfg, "test-provider:test-model", "test-provider") {
		t.Fatal("test setup: model must resolve as vision-capable, or stripContentParts defeats the budget")
	}

	agent := createLLMRunnerTestAgentInstance(t, tmpDir)
	agent.Provider = providers.NewHTTPProvider("k", srv.URL+"/v1", "")

	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sm := session.NewSessionManager()
	sm.SetSessionRepo(st.Sessions())
	agent.Sessions = sm

	return &image400Env{al: al, runner: newLLMRunner(al), agent: agent, store: st, router: router}
}

func image400SeedImages(prompt string, nParts, payloadBytes int) []providers.Message {
	payload := "data:image/jpeg;base64," + strings.Repeat("A", payloadBytes)
	msgs := []providers.Message{{Role: "user", Content: prompt}}
	for i := 0; i < nParts; i++ {
		msgs = append(msgs, providers.Message{Role: "user", ContentParts: []providers.ContentPart{
			{Type: "text", Text: fmt.Sprintf("screenshot %d", i)},
			{Type: "image_url", ImageURL: &providers.ImageURL{URL: payload, Detail: "auto"}},
		}})
	}
	return msgs
}

func (env *image400Env) seedSession(t *testing.T, sessionKey string, msgs []providers.Message) {
	t.Helper()
	for _, m := range msgs {
		env.agent.Sessions.AddFullMessage(sessionKey, m)
	}
	if err := env.agent.Sessions.Save(sessionKey); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
}

func (env *image400Env) buildMessages(t *testing.T, sessionKey, prompt string) []providers.Message {
	t.Helper()
	history := env.agent.Sessions.GetHistory(sessionKey)
	summary := env.agent.Sessions.GetSummary(sessionKey)
	history = ensureSummaryMaterialized(env.agent, sessionKey, history, summary)
	return env.agent.ContextBuilder.BuildMessages(
		history, summary, prompt, nil,
		"test-channel", "test-chat", sessionKey,
		env.agent.Sessions.GetMode(sessionKey),
	)
}

func image400ParseAcceptedBody(t *testing.T, label string, body []byte) (imageParts, videoParts, livePayloadBytes int) {
	t.Helper()
	var parsed struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("%s: accepted body is not valid JSON: %v", label, err)
	}
	var parts []struct {
		Type     string `json:"type"`
		ImageURL *struct {
			URL string `json:"url"`
		} `json:"image_url"`
		VideoURL *struct {
			URL string `json:"url"`
		} `json:"video_url"`
	}
	for _, msg := range parsed.Messages {
		if len(msg.Content) == 0 || msg.Content[0] != '[' {
			continue
		}
		if err := json.Unmarshal(msg.Content, &parts); err != nil {
			t.Fatalf("%s: message content is neither string nor parts array: %v", label, err)
		}
		for _, p := range parts {
			switch p.Type {
			case "image_url":
				imageParts++
				if p.ImageURL != nil {
					livePayloadBytes += len(p.ImageURL.URL)
				}
			case "video_url":
				videoParts++
				if p.VideoURL != nil {
					livePayloadBytes += len(p.VideoURL.URL)
				}
			}
		}
	}
	return imageParts, videoParts, livePayloadBytes
}

func image400AssertWithinEstimate(t *testing.T, label string, acceptedLen int, msgsForRequest []providers.Message) {
	t.Helper()
	est := providers.EstimateRequestBytes(msgsForRequest)
	if acceptedLen > est+image400EnvelopeAllowance {
		t.Errorf("%s: accepted body is %d bytes, over EstimateRequestBytes(%d msgs)=%d + envelope allowance %d — the budget does not bound the wire",
			label, acceptedLen, len(msgsForRequest), est, image400EnvelopeAllowance)
	}
}

func TestImage400_ProactiveBudgetPreventsRejection(t *testing.T) {
	const (
		sessionKey  = "image400-t1"
		routerCap   = int64(10 << 20)
		payloadSize = 2 << 20
		nParts      = 6
	)
	env := newImage400Env(t, routerCap)
	prompt := "Please review these screenshots."
	seeded := image400SeedImages(prompt, nParts, payloadSize)
	env.seedSession(t, sessionKey, seeded)

	msgs := env.buildMessages(t, sessionKey, prompt)

	if est := providers.EstimateRequestBytes(msgs); est <= config.DefaultMaxRequestBytes {
		t.Fatalf("test setup: seeded request estimate %d is not over the %d budget", est, config.DefaultMaxRequestBytes)
	}

	content, _, err := env.runner.runLLMIteration(context.Background(), env.agent, msgs, processOptions{
		SessionKey:  sessionKey,
		Channel:     "test-channel",
		ChatID:      "test-chat",
		UserMessage: prompt,
	})

	if err != nil {
		t.Fatalf("runLLMIteration: %v", err)
	}
	if strings.TrimSpace(content) == "" {
		t.Error("returned content is empty")
	}

	records := env.router.snapshot()
	for i, rec := range records {
		if rec.path != "/v1/chat/completions" {
			t.Errorf("request %d hit path %q, want the provider's chat endpoint /v1/chat/completions", i, rec.path)
		}
	}

	rejected, accepted := env.router.counts()
	if rejected != 0 {
		t.Errorf("rejected = %d, want 0 (the transport rejected a request the budget should have shrunk)", rejected)
	}

	if accepted < 1 {
		t.Fatalf("accepted = %d, want at least 1", accepted)
	}
	maxAccepted := 0
	for _, rec := range records {
		if rec.rejected {
			continue
		}
		if rec.acceptedLen > maxAccepted {
			maxAccepted = rec.acceptedLen
		}
	}
	if maxAccepted > config.DefaultMaxRequestBytes {
		t.Errorf("max accepted body %d exceeds the %d budget", maxAccepted, config.DefaultMaxRequestBytes)
	}
	if int64(maxAccepted) > routerCap {
		t.Errorf("max accepted body %d exceeds the router cap %d", maxAccepted, routerCap)
	}
	reduced, _ := tools.EnforceImageBudget(msgs)
	image400AssertWithinEstimate(t, "test 1", maxAccepted, reduced)
	if estReduced, estSeeded := providers.EstimateRequestBytes(reduced), providers.EstimateRequestBytes(msgs); estReduced >= estSeeded {
		t.Errorf("budget mirror did not shrink: estimate(reduced)=%d >= estimate(seeded)=%d", estReduced, estSeeded)
	}
	for _, rec := range records {
		if rec.rejected {
			continue
		}
		imageParts, videoParts, liveBytes := image400ParseAcceptedBody(t, "test 1", rec.body)
		if liveBytes == 0 && (imageParts+videoParts) > 0 {
			t.Errorf("accepted body has %d media parts but no data: payload bytes", imageParts+videoParts)
		}
		if liveBytes > rec.acceptedLen {
			t.Errorf("accepted body %d bytes but live payload alone is %d", rec.acceptedLen, liveBytes)
		}
	}

	imageParts, videoParts, _ := image400ParseAcceptedBody(t, "test 1", lastAccepted(t, env.router).body)
	if live := imageParts + videoParts; live > tools.MaxLiveImages {
		t.Errorf("live media parts on the wire = %d, want at most MaxLiveImages=%d", live, tools.MaxLiveImages)
	} else if live == 0 {
		t.Errorf("live media parts on the wire = 0, want at least 1 (images must still reach the model)")
	}

	if accepted != 1 {
		t.Errorf("accepted = %d, want exactly 1 (eviction must not cost a summarization call)", accepted)
	}
}

func lastAccepted(t *testing.T, rt *image400Router) image400Request {
	t.Helper()
	records := rt.snapshot()
	for i := len(records) - 1; i >= 0; i-- {
		if !records[i].rejected {
			return records[i]
		}
	}
	t.Fatal("no accepted request was recorded")
	return image400Request{}
}

func TestImage400_RecoveryWhenProviderCapIsTighter(t *testing.T) {
	const (
		sessionKey  = "image400-t2"
		routerCap   = int64(4 << 20)
		payloadSize = 6 << 20
	)
	env := newImage400Env(t, routerCap)
	seeded := image400SeedImages("Please look at this screenshot.", 1, payloadSize)
	env.seedSession(t, sessionKey, seeded)

	msgs := env.buildMessages(t, sessionKey, "Please look at this screenshot.")
	if est := providers.EstimateRequestBytes(msgs); est > config.DefaultMaxRequestBytes {
		t.Fatalf("test setup: request estimate %d exceeds the %d budget — T4 would have fired and this would not test T5", est, config.DefaultMaxRequestBytes)
	}

	content, _, err := env.runner.runLLMIteration(context.Background(), env.agent, msgs, processOptions{
		SessionKey:  sessionKey,
		Channel:     "test-channel",
		ChatID:      "test-chat",
		UserMessage: "Please look at this screenshot.",
	})

	if err != nil {
		errText := fmt.Sprintf("%v", err)
		if strings.Contains(errText, "LLM call failed after retries") {
			t.Fatalf("turn aborted with the pre-fix failure: %v", err)
		}
		t.Fatalf("runLLMIteration: %v", err)
	}
	if strings.TrimSpace(content) == "" {
		t.Error("returned content is empty")
	}

	records := env.router.snapshot()
	rejected, accepted := env.router.counts()
	if rejected != 2 {
		t.Errorf("rejected = %d, want 2 (one logical oversize call = stream attempt + Chat safety-net fallback, identical payloads)", rejected)
	}
	if accepted < 1 {
		t.Fatalf("accepted = %d, want at least 1", accepted)
	}
	var rejectedLen int64 = -1
	for _, rec := range records {
		if !rec.rejected {
			continue
		}
		if rec.contentLength <= routerCap {
			t.Errorf("rejected request Content-Length = %d, want > cap %d", rec.contentLength, routerCap)
		}
		if limit := int64(providers.EstimateRequestBytes(msgs) + image400EnvelopeAllowance); rec.contentLength > limit {
			t.Errorf("rejected request Content-Length %d exceeds estimate+envelope %d", rec.contentLength, limit)
		}
		if rejectedLen == -1 {
			rejectedLen = rec.contentLength
		} else if diff := rejectedLen - rec.contentLength; diff > image400StreamFieldOverhead || diff < -image400StreamFieldOverhead {
			t.Errorf("rejected requests announce Content-Lengths %d bytes apart — not the same payload over two transports (stream attempt + Chat fallback)", diff)
		}
	}

	for i, rec := range records {
		if rec.rejected {
			continue
		}
		if int64(rec.acceptedLen) > routerCap {
			t.Errorf("accepted request %d is %d bytes, over the cap %d", i, rec.acceptedLen, routerCap)
		}
	}
	var postRejection *image400Request
	for i := range records {
		if !records[i].rejected {
			postRejection = &records[i]
			break
		}
	}
	if postRejection == nil {
		t.Fatal("no accepted request after the rejection")
	}
	image400Retry, _ := providers.ReduceImageParts(msgs, 0)
	image400AssertWithinEstimate(t, "test 2 retry", postRejection.acceptedLen, image400Retry)
	if imageParts, videoParts, _ := image400ParseAcceptedBody(t, "test 2", postRejection.body); imageParts+videoParts != 0 {
		t.Errorf("post-rejection accepted body still carries %d media parts, want 0", imageParts+videoParts)
	}

	if len(records) != 3 {
		summary := make([]string, 0, len(records))
		for i, rec := range records {
			state := "accepted"
			if rec.rejected {
				state = "rejected"
			}
			summary = append(summary, fmt.Sprintf("#%d %s %dB", i, state, rec.acceptedLen))
		}
		t.Errorf("distinct requests = %d, want exactly 3 (stream attempt + Chat fallback, then one retry): %s", len(records), strings.Join(summary, ", "))
	}
	if accepted != 1 {
		t.Errorf("accepted = %d, want exactly 1 (recovery must cost no summarization call)", accepted)
	}

	hist := env.agent.Sessions.GetHistory(sessionKey)
	if count, _ := providers.LiveMultimodalParts(hist); count != 0 {
		t.Errorf("live session history heavy parts = %d, want 0 (durable proof via the same manager)", count)
	}
	rows, rErr := env.store.Sessions().LoadMessagesFromSeq(sessionKey, 0)
	if rErr != nil {
		t.Fatalf("LoadMessagesFromSeq: %v", rErr)
	}
	if len(rows) != len(seeded) {
		t.Fatalf("persisted rows = %d, want %d (eviction must not delete messages)", len(rows), len(seeded))
	}
	blob := strings.Repeat("A", payloadSize)
	for i, row := range rows {
		if strings.Contains(row, blob) {
			t.Errorf("persisted row %d still carries the base64 blob after recovery", i)
		}
	}
	if !strings.Contains(strings.Join(rows, ""), image400OmittedPlaceholder) {
		t.Errorf("persisted rows carry no %q placeholder", image400OmittedPlaceholder)
	}
	fresh := session.NewSessionManager()
	fresh.SetSessionRepo(env.store.Sessions())
	if reloaded := fresh.GetHistory(sessionKey); len(reloaded) != len(seeded) {
		t.Errorf("WEAK check: reloaded history len = %d, want %d", len(reloaded), len(seeded))
	}

	content2, _, err2 := env.runner.runLLMIteration(context.Background(), env.agent,
		env.buildMessages(t, sessionKey, "And what do you conclude?"), processOptions{
			SessionKey:  sessionKey,
			Channel:     "test-channel",
			ChatID:      "test-chat",
			UserMessage: "And what do you conclude?",
		})
	if err2 != nil {
		t.Fatalf("second run: %v", err2)
	}
	if strings.TrimSpace(content2) == "" {
		t.Error("second run returned empty content")
	}
	rejectedAfter, acceptedAfter := env.router.counts()
	if rejectedAfter != 2 {
		t.Errorf("rejected after second run = %d, want 2 (the cleaned session must not resend the blobs)", rejectedAfter)
	}
	if acceptedAfter != 2 {
		t.Errorf("accepted after second run = %d, want exactly 2 (one clean request)", acceptedAfter)
	}
}

func TestImage400_NextTurnDoesNotResendImages(t *testing.T) {
	const (
		sessionKey  = "image400-t3"
		routerCap   = int64(4 << 20)
		payloadSize = 5 << 20
	)
	env := newImage400Env(t, routerCap)
	seeded := image400SeedImages("Compare these two screenshots.", 2, payloadSize)
	env.seedSession(t, sessionKey, seeded)

	msgs := env.buildMessages(t, sessionKey, "Compare these two screenshots.")
	seedEstimate := providers.EstimateRequestBytes(msgs)
	if seedEstimate <= config.DefaultMaxRequestBytes {
		t.Fatalf("test setup: seeded estimate %d is not over the %d budget — the final assertion would be vacuous",
			seedEstimate, config.DefaultMaxRequestBytes)
	}

	_, _, err := env.runner.runLLMIteration(context.Background(), env.agent, msgs, processOptions{
		SessionKey:  sessionKey,
		Channel:     "test-channel",
		ChatID:      "test-chat",
		UserMessage: "Compare these two screenshots.",
	})
	if err != nil {
		t.Fatalf("runLLMIteration: %v", err)
	}
	rejected, _ := env.router.counts()
	if rejected != 2 {
		t.Fatalf("rejected = %d, want 2 (the cap must have rejected the first logical call for this test to prove anything)", rejected)
	}

	history := env.agent.Sessions.GetHistory(sessionKey)
	if count, _ := providers.LiveMultimodalParts(history); count != 0 {
		t.Errorf("live session history heavy parts = %d, want 0 (durable proof via the same manager)", count)
	}
	cb := NewContextBuilder(env.agent.Workspace)
	cb.SetToolsRegistry(env.agent.Tools)
	next := cb.BuildMessages(history, "", "Thanks, that is all.", nil,
		"test-channel", "test-chat", sessionKey, env.agent.Sessions.GetMode(sessionKey))

	if est := providers.EstimateRequestBytes(next); est >= config.DefaultMaxRequestBytes {
		t.Errorf("rebuilt next-turn estimate = %d, want < %d — without the persisted eviction the blobs would still be there",
			est, config.DefaultMaxRequestBytes)
	}

	rows, rErr := env.store.Sessions().LoadMessagesFromSeq(sessionKey, 0)
	if rErr != nil {
		t.Fatalf("LoadMessagesFromSeq: %v", rErr)
	}
	blob := strings.Repeat("A", payloadSize)
	for i, row := range rows {
		if strings.Contains(row, blob) {
			t.Errorf("persisted row %d still carries the base64 blob", i)
		}
	}
}
