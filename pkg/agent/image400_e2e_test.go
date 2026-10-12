// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

// End-to-end verification of the oversized-image-request fix, driven over
// real HTTP through the real providers.NewHTTPProvider against a fake router
// that reproduces the configured transport exactly:
//
//   - body cap: http.MaxBytesReader(w, r.Body, cap) — the router uses 10 MiB
//     (~/LLmProxy/internal/proxy/handler.go:29);
//   - rejection: HTTP 400 with the exact body
//     {"error":{"message":"failed to read request body: <err>","type":"invalid_request_error"}}
//     (handler.go:32-33, dispatcher.go:348-357);
//   - acceptance: an OpenAI-compatible chat.completion (or SSE stream —
//     ChatIdle prefers the streaming transport, which sends the identical
//     messages with "stream":true, so the fake answers SSE for those).
//
// The three tests pin the two halves of the fix:
//
//   - TestImage400_ProactiveBudgetPreventsRejection (T4): a session carrying
//     more media than the 8 MiB request budget allows never reaches the
//     transport cap — the pre-request budget shrinks the payload first, and
//     shrinking alone (no summarization call) is enough.
//   - TestImage400_RecoveryWhenProviderCapIsTighter (T5): the one case T4
//     cannot prevent — a provider whose body cap is BELOW our budget. The
//     rejection evicts the media and retries instead of aborting, and the
//     eviction is persisted into the stored session.
//   - TestCase_NextTurnDoesNotResendImages: the persisted eviction is what
//     keeps the NEXT turn's rebuilt messages under the budget.
//
// Known pre-existing trap (out of scope here, see
// ~/.lele/workspace-software-engineer/artifacts/lele-session-contentparts-roundtrip-bug.md):
// ContentParts never survive a session reload (persist.go saves Message
// values, so the default struct encoder writes "content_parts" and
// Message.UnmarshalJSON only reads "content"). Any assertion of the form
// "reload a fresh SessionManager ⇒ no heavy parts" is therefore VACUOUS — it
// holds with or without this fix. The durable de-poisoning proofs in these
// tests are (a) GetHistory on the SAME live manager the turn used and (b) the
// persisted row bytes read directly via LoadMessagesFromSeq; every
// fresh-reload check is labelled WEAK where kept at all.

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

// image400Reply is the assistant content every successful fake-router answer
// carries; non-empty so the runner finishes the turn on the first iteration.
const image400Reply = "The images fit inside the request budget."

// image400EnvelopeAllowance bounds the non-messages fields of the
// OpenAI-compat request envelope (model, max_tokens, temperature, stream —
// measured < 100 bytes with the empty tool registry). It is the only slack
// allowed on top of providers.EstimateRequestBytes when comparing the
// estimate against the body a real provider put on the wire.
const image400EnvelopeAllowance = 1024

// image400OmittedPlaceholder mirrors providers.omittedPartPlaceholder
// (unexported across packages): the text part ReduceImageParts substitutes
// for an evicted data: part when the message has no textual Content.
const image400OmittedPlaceholder = "[image omitted from context]"

// image400StreamFieldOverhead bounds the ONLY wire difference between the two
// transport attempts of one logical call: ChatStream adds `"stream":true`
// (13 bytes + separator) to the otherwise identical body.
const image400StreamFieldOverhead = 32

// image400Request is one observed request to the fake router.
type image400Request struct {
	path          string
	rejected      bool // MaxBytesReader tripped → HTTP 400 with the router body
	contentLength int64
	acceptedLen   int    // len(raw) for accepted requests
	body          []byte // raw body for accepted requests
}

// image400Router reproduces the configured router's body-cap behaviour and
// instruments every request it sees.
type image400Router struct {
	cap     int64
	mu      sync.Mutex
	records []image400Request
}

func (rt *image400Router) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := image400Request{path: r.URL.Path, contentLength: r.ContentLength}
		w.Header().Set("Content-Type", "application/json")
		// Identical to the configured router (handler.go:29).
		body := http.MaxBytesReader(w, r.Body, rt.cap)
		raw, err := io.ReadAll(body)
		if err != nil {
			// Identical to handler.go:30-35: HTTP 400, invalid_request_error,
			// "failed to read request body: " + err.Error().
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

		// ChatIdle prefers the streaming transport; those requests carry the
		// identical messages with "stream":true and expect SSE.
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

// image400Env is the shared end-to-end harness: a real AgentLoop, a real
// HTTPProvider pointed at the fake router, and a store-backed SessionManager
// so the de-poisoning proof can read the persisted rows.
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

	// Register the fake provider's model as vision+video-capable where the SUT
	// looks for the capability (runLLMIteration strips ContentParts from
	// non-vision models BEFORE the budget runs, which would make every test
	// here vacuous) and give the Named provider the fake router's API base so
	// CreateProviderForCandidate (which llm_caller prefers for a prefixed
	// model) also routes to the fake.
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
	// Anti-hollowness guard: the capability lever must take effect in the
	// exact resolution path the SUT uses.
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

// image400SeedImages returns the shape read_image injects: user messages
// carrying data:image/jpeg;base64 ContentParts of payloadBytes each.
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

// seedSession stores the messages as the source of truth and persists them
// immediately, so any later Save takes the incremental (markModified-driven)
// path and the row proof below reads rows that the eviction really rewrote.
func (env *image400Env) seedSession(t *testing.T, sessionKey string, msgs []providers.Message) {
	t.Helper()
	for _, m := range msgs {
		env.agent.Sessions.AddFullMessage(sessionKey, m)
	}
	if err := env.agent.Sessions.Save(sessionKey); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
}

// buildMessages rebuilds the outgoing request exactly like the loop does
// (runAgentLoop steps 2): GetHistory + ensureSummaryMaterialized +
// ContextBuilder.BuildMessages.
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

// image400ParseAcceptedBody counts live media parts (image_url / video_url)
// and sums the byte length of their URLs in a body the fake router accepted.
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
			continue // plain string content (system prompt / plain text)
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

// image400AssertWithinEstimate is the comparison that must never be faked:
// the body a real provider puts on the wire for msgsForRequest stays under
// providers.EstimateRequestBytes(msgsForRequest) + the measured OpenAI
// envelope allowance. EstimateRequestBytes is a proven upper bound of
// json.Marshal(messages) and SerializeMessages emits the same shape for
// ContentParts, so this pins the estimator against reality, not against
// itself.
func image400AssertWithinEstimate(t *testing.T, label string, acceptedLen int, msgsForRequest []providers.Message) {
	t.Helper()
	est := providers.EstimateRequestBytes(msgsForRequest)
	if acceptedLen > est+image400EnvelopeAllowance {
		t.Errorf("%s: accepted body is %d bytes, over EstimateRequestBytes(%d msgs)=%d + envelope allowance %d — the budget does not bound the wire",
			label, acceptedLen, len(msgsForRequest), est, image400EnvelopeAllowance)
	}
}

// TestImage400_ProactiveBudgetPreventsRejection pins T4: a session whose
// media exceeds config.DefaultMaxRequestBytes is shrunk by the pre-request
// budget before it leaves the process, so the transport cap (10 MiB, the
// configured router) is never hit and no summarization call is paid.
func TestImage400_ProactiveBudgetPreventsRejection(t *testing.T) {
	const (
		sessionKey  = "image400-t1"
		routerCap   = int64(10 << 20) // the configured router's cap
		payloadSize = 2 << 20         // 2 MiB of base64 per part
		nParts      = 6               // 12 MiB total: over our 8 MiB budget AND over the cap
	)
	env := newImage400Env(t, routerCap)
	prompt := "Please review these screenshots."
	seeded := image400SeedImages(prompt, nParts, payloadSize)
	env.seedSession(t, sessionKey, seeded)

	msgs := env.buildMessages(t, sessionKey, prompt)

	// Anti-vacuity: the request we handed the loop really was over the budget,
	// so assertion 3 only passes because the budget shrank it.
	if est := providers.EstimateRequestBytes(msgs); est <= config.DefaultMaxRequestBytes {
		t.Fatalf("test setup: seeded request estimate %d is not over the %d budget", est, config.DefaultMaxRequestBytes)
	}

	content, _, err := env.runner.runLLMIteration(context.Background(), env.agent, msgs, processOptions{
		SessionKey:  sessionKey,
		Channel:     "test-channel",
		ChatID:      "test-chat",
		UserMessage: prompt,
	})

	// Assertion 1: the turn succeeded and produced content.
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

	// Assertion 2: the transport cap was NEVER hit — the budget acted first.
	rejected, accepted := env.router.counts()
	if rejected != 0 {
		t.Errorf("rejected = %d, want 0 (the transport rejected a request the budget should have shrunk)", rejected)
	}

	// Assertion 3: every accepted body fits our budget AND the router cap.
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
	// The estimator-vs-wire comparison (must not be faked): the loop's budget
	// is deterministic, so mirror it and compare the estimate for the exact
	// messages against the body the real provider put on the wire.
	reduced, _ := tools.EnforceImageBudget(msgs)
	image400AssertWithinEstimate(t, "test 1", maxAccepted, reduced)
	if estReduced, estSeeded := providers.EstimateRequestBytes(reduced), providers.EstimateRequestBytes(msgs); estReduced >= estSeeded {
		t.Errorf("budget mirror did not shrink: estimate(reduced)=%d >= estimate(seeded)=%d", estReduced, estSeeded)
	}
	// ... and the payload that stayed live really went out (lower bound):
	// the accepted body must physically carry the base64 bytes.
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

	// Assertion 4: at most tools.MaxLiveImages live media parts on the wire —
	// and at least one, so the assertion cannot pass vacuously.
	imageParts, videoParts, _ := image400ParseAcceptedBody(t, "test 1", lastAccepted(t, env.router).body)
	if live := imageParts + videoParts; live > tools.MaxLiveImages {
		t.Errorf("live media parts on the wire = %d, want at most MaxLiveImages=%d", live, tools.MaxLiveImages)
	} else if live == 0 {
		t.Errorf("live media parts on the wire = 0, want at least 1 (images must still reach the model)")
	}

	// Assertion 5: eviction alone was enough — the summarizer/compaction path
	// (CompactLoopMessages would add one provider call) was never used.
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

// TestImage400_RecoveryWhenProviderCapIsTighter pins T5: a provider whose
// body cap (4 MiB) is BELOW our 8 MiB budget rejects the first request; the
// rejection evicts the media and retries (no summarization call, no aborted
// turn) and the stored session is cleaned so the next turn is small.
func TestImage400_RecoveryWhenProviderCapIsTighter(t *testing.T) {
	const (
		sessionKey  = "image400-t2"
		routerCap   = int64(4 << 20) // tighter than our budget
		payloadSize = 6 << 20        // 6 MiB: under our budget, over the provider cap
	)
	env := newImage400Env(t, routerCap)
	seeded := image400SeedImages("Please look at this screenshot.", 1, payloadSize)
	env.seedSession(t, sessionKey, seeded)

	msgs := env.buildMessages(t, sessionKey, "Please look at this screenshot.")
	// Anti-vacuity for T4-does-not-apply: the first request is within OUR
	// budget, so only the T5 escalation can save this turn.
	if est := providers.EstimateRequestBytes(msgs); est > config.DefaultMaxRequestBytes {
		t.Fatalf("test setup: request estimate %d exceeds the %d budget — T4 would have fired and this would not test T5", est, config.DefaultMaxRequestBytes)
	}

	content, _, err := env.runner.runLLMIteration(context.Background(), env.agent, msgs, processOptions{
		SessionKey:  sessionKey,
		Channel:     "test-channel",
		ChatID:      "test-chat",
		UserMessage: "Please look at this screenshot.",
	})

	// Assertion 1: the turn succeeded — no "LLM call failed after retries".
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
	// Assertion 2: the rejection happened and the retry succeeded.
	//
	// The transport-rejection count is 2 for ONE logical rejected call, and
	// the doubling is a property of the real provider, not an extra logical
	// call: ChatIdle (pkg/providers/chat_idle.go) always attempts the
	// streaming transport first and, on a failure before the first chunk,
	// replays the identical payload once over the non-streaming transport as
	// its documented safety net. Both attempts carry the same messages, so
	// the cap rejects both. PROOF: the rejected records announce the SAME
	// payload size up to the `"stream":true` field the streaming attempt adds
	// (≤ 32 bytes apart); a second LOGICAL call (e.g. a summarization) would
	// carry different messages and a wildly different length.
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
		// Each rejected request really WAS the oversize one (client-announced
		// Content-Length over the cap, bounded by the estimate + envelope).
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

	// Assertion 3: every accepted body fits the cap, and the accepted body
	// AFTER the rejection carries no media (the escalation drops it all).
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

	// Assertion 4: exactly THREE wire requests: the two rejected transport
	// attempts of the single logical call (proven identical above) + one
	// accepted retry. Any extra LOGICAL call — a summarization through the
	// same provider would be accepted and show up here — breaks the count.
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

	// Assertion 5: session de-poisoning.
	// DURABLE proof (a): the SAME live manager the turn used holds the
	// rewritten slice in memory — no heavy part survives there.
	hist := env.agent.Sessions.GetHistory(sessionKey)
	if count, _ := providers.LiveMultimodalParts(hist); count != 0 {
		t.Errorf("live session history heavy parts = %d, want 0 (durable proof via the same manager)", count)
	}
	// DURABLE proof (b): the persisted row bytes — the blob is gone from
	// every row while the message count is unchanged.
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
	// WEAK check (kept as documentation of loader behaviour, NOT proof):
	// content_parts never survives a reload on this base (pre-existing
	// round-trip bug), so a fresh manager drops the blob with or without our
	// fix. The durable proofs are (a) and (b) above.
	fresh := session.NewSessionManager()
	fresh.SetSessionRepo(env.store.Sessions())
	if reloaded := fresh.GetHistory(sessionKey); len(reloaded) != len(seeded) {
		t.Errorf("WEAK check: reloaded history len = %d, want %d", len(reloaded), len(seeded))
	}

	// The second run of the turn produces no further rejection: the rebuilt
	// history carries no blob to resend.
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

// TestImage400_NextTurnDoesNotResendImages: after the recovery persisted the
// eviction, a fresh rebuild of the turn (fresh ContextBuilder over
// GetHistory) is under the byte budget again. Honest scope: this holds
// BECAUSE the recovery persisted the eviction — the anti-vacuity guard below
// shows the rebuilt history would still be over budget without it.
func TestImage400_NextTurnDoesNotResendImages(t *testing.T) {
	const (
		sessionKey  = "image400-t3"
		routerCap   = int64(4 << 20)
		payloadSize = 5 << 20 // two of these: over OUR budget too
	)
	env := newImage400Env(t, routerCap)
	seeded := image400SeedImages("Compare these two screenshots.", 2, payloadSize)
	env.seedSession(t, sessionKey, seeded)

	msgs := env.buildMessages(t, sessionKey, "Compare these two screenshots.")
	// Anti-vacuity: without the persisted eviction, the rebuilt history would
	// still carry both blobs and the assertion at the end would fail. (Here
	// the pre-request budget trims the REQUEST to one image — 5 MiB, still
	// over the provider cap — so the rejection+recovery still fires.)
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
	// The recovery was needed: both transport attempts of the first logical
	// call (stream attempt + Chat safety-net fallback, see test 2) hit the
	// cap. Without it the turn would have aborted before this test's point.
	rejected, _ := env.router.counts()
	if rejected != 2 {
		t.Fatalf("rejected = %d, want 2 (the cap must have rejected the first logical call for this test to prove anything)", rejected)
	}

	// Fresh ContextBuilder, fresh BuildMessages from GetHistory — the next
	// turn's real starting point.
	history := env.agent.Sessions.GetHistory(sessionKey)
	if count, _ := providers.LiveMultimodalParts(history); count != 0 {
		t.Errorf("live session history heavy parts = %d, want 0 (durable proof via the same manager)", count)
	}
	cb := NewContextBuilder(env.agent.Workspace)
	cb.SetToolsRegistry(env.agent.Tools)
	next := cb.BuildMessages(history, "", "Thanks, that is all.", nil,
		"test-channel", "test-chat", sessionKey, env.agent.Sessions.GetMode(sessionKey))

	// The brief's assertion, meaningful because of the guard above: the
	// recovery persisted the eviction, so the rebuilt messages fit the budget.
	if est := providers.EstimateRequestBytes(next); est >= config.DefaultMaxRequestBytes {
		t.Errorf("rebuilt next-turn estimate = %d, want < %d — without the persisted eviction the blobs would still be there",
			est, config.DefaultMaxRequestBytes)
	}

	// Disk proof: the blob is gone from every persisted row.
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
