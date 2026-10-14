package channels

// Tests for the streaming throughput metrics (tokens / elapsed_ms / tok_per_sec
// / estimated) mirrored from the agent's message.stream (answer surface) and
// message.thinking (reasoning surface) Metadata onto the WebSocket and SSE
// payloads.
//
// The agent's stream handler (pkg/agent/llm_stream.go) publishes them inside
// bus.OutboundMessage.Metadata, which is a map[string]string, so the numbers
// travel pre-formatted with strconv. native.go only has to read them back and
// put them on the wire; the frontend (web/src/hooks/event-handlers/streaming.ts)
// owns the presentation decision (it hides the indicator while every metric is
// zero and estimated is false). These tests pin the JSON contract, so a change
// here is a breaking change for that consumer.

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
)

// fullStreamMetrics is the metadata shape a real message.stream event carries,
// exactly as the agent formats it (strconv outputs).
func fullStreamMetrics() map[string]string {
	return map[string]string{
		"done":        "false",
		"tokens":      "132",
		"elapsed_ms":  "1500",
		"tok_per_sec": "88.5",
		"estimated":   "true",
	}
}

// fullThinkingMetrics is the metadata shape a real message.thinking event
// carries, exactly as the agent formats it (strconv outputs). It has no "done"
// key: reasoning has no terminal chunk.
func fullThinkingMetrics() map[string]string {
	return map[string]string{
		"tokens":      "96",
		"elapsed_ms":  "2400",
		"tok_per_sec": "40",
		"estimated":   "true",
	}
}

// streamEventPayload reads the single message.stream frame queued on a fake
// client and returns its decoded payload plus the raw JSON body.
func streamEventPayload(t *testing.T, client *WSClient) (WSStreamPayload, string) {
	t.Helper()

	events := drainWSEvents(t, client)
	stream := findWSEvent(events, "message.stream")
	if stream == nil {
		t.Fatalf("message.stream not emitted; got events: %v", eventNames(events))
	}
	var payload WSStreamPayload
	decodeWSData(t, stream.Data, &payload)
	return payload, string(stream.Data)
}

// thinkingEventPayload reads the single message.thinking frame queued on a fake
// client and returns its decoded payload plus the raw JSON body.
func thinkingEventPayload(t *testing.T, client *WSClient) (WSThinkingPayload, string) {
	t.Helper()

	events := drainWSEvents(t, client)
	thinking := findWSEvent(events, "message.thinking")
	if thinking == nil {
		t.Fatalf("message.thinking not emitted; got events: %v", eventNames(events))
	}
	var payload WSThinkingPayload
	decodeWSData(t, thinking.Data, &payload)
	return payload, string(thinking.Data)
}

// TestDispatchOutboundStreamCarriesThroughputMetrics is the core contract test:
// a message.stream with full Metadata must reach the WebSocket payload with
// every metric populated, and the serialized JSON must expose the exact snake_case
// keys the frontend reads.
func TestDispatchOutboundStreamCarriesThroughputMetrics(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-metrics"
	client := registerFakeWSClient(t, ts, sessionKey)

	meta := fullStreamMetrics()
	meta["done"] = "false"

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Event:     "message.stream",
		Content:   "hola",
		MessageID: "msg-metrics-1",
		Metadata:  meta,
	})

	payload, raw := streamEventPayload(t, client)

	if payload.MessageID != "msg-metrics-1" || payload.SessionKey != sessionKey || payload.Chunk != "hola" {
		t.Errorf("identity fields = %+v, want msg-metrics-1/%s/hola", payload, sessionKey)
	}
	if payload.Done {
		t.Error("done = true, want false for an intermediate chunk")
	}
	if payload.Tokens != 132 {
		t.Errorf("tokens = %d, want 132", payload.Tokens)
	}
	if payload.ElapsedMS != 1500 {
		t.Errorf("elapsed_ms = %d, want 1500", payload.ElapsedMS)
	}
	if payload.TokPerSec != 88.5 {
		t.Errorf("tok_per_sec = %v, want 88.5", payload.TokPerSec)
	}
	if !payload.Estimated {
		t.Error("estimated = false, want true")
	}

	for _, want := range []string{
		`"tokens":132`,
		`"elapsed_ms":1500`,
		`"tok_per_sec":88.5`,
		`"estimated":true`,
		`"done":false`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("serialized stream payload missing %s:\n%s", want, raw)
		}
	}
}

// TestDispatchOutboundStreamWithoutMetricsDefaultsToZero pins the absent-metadata
// path: no panic, metrics at their zero values, and the four keys STILL present
// in the JSON (no omitempty) so the frontend shape never changes.
func TestDispatchOutboundStreamWithoutMetricsDefaultsToZero(t *testing.T) {
	cases := []struct {
		name string
		meta map[string]string
	}{
		{name: "nil metadata", meta: nil},
		{name: "empty metadata", meta: map[string]string{}},
		{name: "done key only", meta: map[string]string{"done": "false"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := newNativeTestServer(t)

			sessionKey := "native:" + ts.clientID + "-nometrics"
			client := registerFakeWSClient(t, ts, sessionKey)

			ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
				Channel:   ChannelName,
				ChatID:    sessionKey,
				Event:     "message.stream",
				Content:   "chunk",
				MessageID: "msg-nometrics-1",
				Metadata:  tc.meta,
			})

			payload, raw := streamEventPayload(t, client)

			if payload.Tokens != 0 || payload.ElapsedMS != 0 || payload.TokPerSec != 0 || payload.Estimated {
				t.Errorf("metrics = (%d, %d, %v, %v), want all zero/false",
					payload.Tokens, payload.ElapsedMS, payload.TokPerSec, payload.Estimated)
			}
			for _, want := range []string{
				`"tokens":0`,
				`"elapsed_ms":0`,
				`"tok_per_sec":0`,
				`"estimated":false`,
			} {
				if !strings.Contains(raw, want) {
					t.Errorf("serialized stream payload missing %s:\n%s", want, raw)
				}
			}
		})
	}
}

// TestDispatchOutboundStreamDoneEventKeepsFinalMetrics covers the terminal
// chunk: done=true must still be honoured AND the payload must carry the final
// cumulative metrics of the segment.
func TestDispatchOutboundStreamDoneEventKeepsFinalMetrics(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-metrics-done"
	client := registerFakeWSClient(t, ts, sessionKey)

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Event:     "message.stream",
		Content:   "",
		MessageID: "msg-metrics-done",
		Metadata: map[string]string{
			"done":        "true",
			"tokens":      "512",
			"elapsed_ms":  "8000",
			"tok_per_sec": "64",
			"estimated":   "true",
		},
	})

	payload, raw := streamEventPayload(t, client)

	if !payload.Done {
		t.Error("done = false, want true for the terminal chunk")
	}
	if payload.Tokens != 512 || payload.ElapsedMS != 8000 || payload.TokPerSec != 64 || !payload.Estimated {
		t.Errorf("final metrics = %+v, want 512/8000/64/true", payload)
	}
	if !strings.Contains(raw, `"done":true`) {
		t.Errorf("serialized payload missing done=true:\n%s", raw)
	}
}

// TestWSStreamPayloadJSONShapeIsDeterministic freezes the wire shape: the
// frontend parser and the backwards-compatibility guarantee both depend on the
// four metric keys always being present, in this order, with no omitempty.
func TestWSStreamPayloadJSONShapeIsDeterministic(t *testing.T) {
	payload := WSStreamPayload{
		MessageID:  "m1",
		SessionKey: "s1",
		Chunk:      "hi",
		Done:       false,
		Tokens:     12,
		ElapsedMS:  1500,
		TokPerSec:  8,
		Estimated:  true,
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	want := `{"message_id":"m1","session_key":"s1","chunk":"hi","done":false,` +
		`"tokens":12,"elapsed_ms":1500,"tok_per_sec":8,"estimated":true}`
	if string(raw) != want {
		t.Errorf("payload JSON = %s\nwant            %s", raw, want)
	}

	zero, err := json.Marshal(WSStreamPayload{MessageID: "m2", Chunk: ""})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(zero), `"tokens":0`) ||
		!strings.Contains(string(zero), `"estimated":false`) {
		t.Errorf("metric keys must be present even at their zero value: %s", zero)
	}
}

// TestSSEFrameCarriesStreamMetrics proves the SSE fan-out serializes the SAME
// struct (and therefore the same keys): writeSSE marshals the payload the
// channel mirrored into the restStreamEvent, with no separate SSE-only format.
func TestSSEFrameCarriesStreamMetrics(t *testing.T) {
	payload := newWSStreamPayload("m1", "s1", "hola", false, map[string]string{
		"tokens":      "40",
		"elapsed_ms":  "2000",
		"tok_per_sec": "20",
		"estimated":   "true",
	})

	rec := httptest.NewRecorder()
	if err := writeSSE(rec, "message.stream", payload); err != nil {
		t.Fatalf("writeSSE() error = %v", err)
	}

	body := rec.Body.String()
	if !strings.HasPrefix(body, "event: message.stream\ndata: ") {
		t.Fatalf("SSE frame shape = %q, want an event/data frame", body)
	}
	for _, want := range []string{
		`"tokens":40`,
		`"elapsed_ms":2000`,
		`"tok_per_sec":20`,
		`"estimated":true`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE frame missing %s:\n%s", want, body)
		}
	}
}

// TestStreamMetricsFanoutToRESTSubscriber is the fan-out integration check: the
// subscriber that feeds /chat/send/stream receives the metrics-bearing payload
// (the same value the SSE writer above serializes).
func TestStreamMetricsFanoutToRESTSubscriber(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-fanout"
	sub := ts.channel.registerRESTStreamSubscriber(sessionKey, "msg-fanout-1")
	defer ts.channel.unregisterRESTStreamSubscriber(sub.id)

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Event:     "message.stream",
		Content:   "chunk",
		MessageID: "msg-fanout-1",
		Metadata:  fullStreamMetrics(),
	})

	select {
	case evt := <-sub.ch:
		if evt.event != "message.stream" {
			t.Fatalf("fanned-out event = %q, want message.stream", evt.event)
		}
		payload, ok := evt.data.(WSStreamPayload)
		if !ok {
			t.Fatalf("fanned-out data type = %T, want WSStreamPayload", evt.data)
		}
		if payload.Tokens != 132 || payload.TokPerSec != 88.5 || !payload.Estimated {
			t.Errorf("fanned-out payload = %+v, want the stream metrics", payload)
		}
	default:
		t.Fatal("no event fanned out to the REST stream subscriber")
	}
}

// TestMetadataCoercionIsTolerant covers the type-tolerant helpers: the metrics
// can arrive as a strconv-formatted string (today's bus encoding), as a native
// number, or as a float64 after a JSON round-trip (durable spool). Nothing may
// panic, and anything unrecognised degrades to the zero value.
func TestMetadataCoercionIsTolerant(t *testing.T) {
	cases := []struct {
		name      string
		value     any
		wantInt   int
		wantInt64 int64
		wantFloat float64
		wantBool  bool
	}{
		{name: "int", value: 42, wantInt: 42, wantInt64: 42, wantFloat: 42, wantBool: true},
		{name: "int zero", value: 0, wantInt: 0, wantInt64: 0, wantFloat: 0, wantBool: false},
		{name: "int64", value: int64(7), wantInt: 7, wantInt64: 7, wantFloat: 7, wantBool: true},
		{name: "float64", value: 88.5, wantInt: 88, wantInt64: 88, wantFloat: 88.5, wantBool: true},
		{name: "float64 zero", value: 0.0, wantInt: 0, wantInt64: 0, wantFloat: 0, wantBool: false},
		{name: "float32", value: float32(1.5), wantInt: 1, wantInt64: 1, wantFloat: 1.5, wantBool: true},
		{name: "bool true", value: true, wantInt: 1, wantInt64: 1, wantFloat: 1, wantBool: true},
		{name: "bool false", value: false},
		// A numeric string is a valid number but NOT a truthiness token:
		// coerceBool only accepts strconv.ParseBool's vocabulary, so the
		// "estimated" flag can never be switched on by a stray counter.
		{name: "string int", value: "132", wantInt: 132, wantInt64: 132, wantFloat: 132, wantBool: false},
		{name: "string float", value: "88.5", wantInt: 88, wantInt64: 88, wantFloat: 88.5, wantBool: false},
		{name: "string bool", value: "true", wantBool: true},
		{name: "string bool false", value: "false", wantBool: false},
		{name: "string with spaces", value: "  64  ", wantInt: 64, wantInt64: 64, wantFloat: 64, wantBool: false},
		{name: "json number int", value: json.Number("1500"), wantInt: 1500, wantInt64: 1500, wantFloat: 1500, wantBool: true},
		// Non-finite values: strconv.ParseFloat accepts "NaN"/"Inf"/"+Inf"/
		// "-Inf" without error and a native float can hold them too, but a
		// metric is never legitimately non-finite. They must degrade to the
		// zero value: int(NaN) would be an implementation-defined huge negative
		// number and a NaN/Inf float is not even JSON-encodable.
		{name: "string NaN", value: "NaN"},
		{name: "string Inf", value: "Inf"},
		{name: "string +Inf", value: "+Inf"},
		{name: "string -Inf", value: "-Inf"},
		{name: "json number NaN", value: json.Number("NaN")},
		{name: "native float64 NaN", value: math.NaN()},
		{name: "native float64 +Inf", value: math.Inf(1)},
		{name: "native float64 -Inf", value: math.Inf(-1)},
		{name: "native float32 NaN", value: float32(math.NaN())},
		{name: "unexpected string", value: "not-a-number"},
		{name: "empty string", value: ""},
		{name: "nil", value: nil},
		{name: "unexpected type", value: struct{}{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := coerceInt(tc.value); got != tc.wantInt {
				t.Errorf("coerceInt(%#v) = %d, want %d", tc.value, got, tc.wantInt)
			}
			if got := coerceInt64(tc.value); got != tc.wantInt64 {
				t.Errorf("coerceInt64(%#v) = %d, want %d", tc.value, got, tc.wantInt64)
			}
			if got := coerceFloat64(tc.value); got != tc.wantFloat {
				t.Errorf("coerceFloat64(%#v) = %v, want %v", tc.value, got, tc.wantFloat)
			}
			if got := coerceBool(tc.value); got != tc.wantBool {
				t.Errorf("coerceBool(%#v) = %v, want %v", tc.value, got, tc.wantBool)
			}
		})
	}
}

// TestNonFiniteMetricsKeepPayloadMarshalable closes the loop opened by the
// coercion guards: before them a metadata value of "NaN"/"Inf" reached
// WSStreamPayload verbatim, json.Marshal failed and native.go's mustMarshal
// swallowed the error, publishing "data": null to the client. The guards make
// the payload always marshalable, with the offending metric at 0.
func TestNonFiniteMetricsKeepPayloadMarshalable(t *testing.T) {
	meta := map[string]string{
		"tokens":      "NaN",
		"elapsed_ms":  "Inf",
		"tok_per_sec": "-Inf",
		"estimated":   "NaN",
	}

	payload := newWSStreamPayload("m1", "s1", "chunk", false, meta)

	if payload.Tokens != 0 || payload.ElapsedMS != 0 || payload.TokPerSec != 0 || payload.Estimated {
		t.Errorf("metrics = (%d, %d, %v, %v), want all zero/false",
			payload.Tokens, payload.ElapsedMS, payload.TokPerSec, payload.Estimated)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v, payload must stay encodable: %+v", err, payload)
	}
	for _, want := range []string{`"tokens":0`, `"elapsed_ms":0`, `"tok_per_sec":0`, `"estimated":false`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("serialized payload missing %s:\n%s", want, raw)
		}
	}
}

// TestMetadataAccessorsDefaultOnMissingKey pins the map-level accessors: a
// missing key (or a nil map) yields the zero value instead of a parse error.
func TestMetadataAccessorsDefaultOnMissingKey(t *testing.T) {
	var nilMeta map[string]string

	metaCases := map[string]map[string]string{
		"nil map":     nilMeta,
		"empty map":   {},
		"other keys":  {"chunk": "x"},
		"wrong value": {"tokens": "nope", "elapsed_ms": "", "tok_per_sec": "?", "estimated": "maybe"},
	}

	for name, meta := range metaCases {
		t.Run(name, func(t *testing.T) {
			if got := metadataInt(meta, "tokens"); got != 0 {
				t.Errorf("metadataInt = %d, want 0", got)
			}
			if got := metadataInt64(meta, "elapsed_ms"); got != 0 {
				t.Errorf("metadataInt64 = %d, want 0", got)
			}
			if got := metadataFloat64(meta, "tok_per_sec"); got != 0 {
				t.Errorf("metadataFloat64 = %v, want 0", got)
			}
			if got := metadataBool(meta, "estimated"); got != false {
				t.Errorf("metadataBool = %v, want false", got)
			}
		})
	}

	meta := fullStreamMetrics()
	if got := metadataInt(meta, "tokens"); got != 132 {
		t.Errorf("metadataInt(tokens) = %d, want 132", got)
	}
	if got := metadataInt64(meta, "elapsed_ms"); got != 1500 {
		t.Errorf("metadataInt64(elapsed_ms) = %d, want 1500", got)
	}
	if got := metadataFloat64(meta, "tok_per_sec"); got != 88.5 {
		t.Errorf("metadataFloat64(tok_per_sec) = %v, want 88.5", got)
	}
	if got := metadataBool(meta, "estimated"); !got {
		t.Error("metadataBool(estimated) = false, want true")
	}
}

// TestStreamMetricsOnFullMessagePath guarantees the legacy full-message path
// (no explicit message.stream event, content present) still emits a payload
// with the four keys, so no client ever sees two different stream shapes.
func TestStreamMetricsOnFullMessagePath(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-fullpath"
	client := registerFakeWSClient(t, ts, sessionKey)

	if err := ts.channel.Send(context.Background(), bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Content:   "hola mundo",
		MessageID: "msg-fullpath-1",
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	payload, raw := streamEventPayload(t, client)

	if !payload.Done {
		t.Error("full-message stream chunk must be done=true")
	}
	if payload.Tokens != 0 || payload.Estimated {
		t.Errorf("metrics = (%d, %v), want zeros on the non-streaming path", payload.Tokens, payload.Estimated)
	}
	for _, want := range []string{`"tokens":0`, `"elapsed_ms":0`, `"tok_per_sec":0`, `"estimated":false`} {
		if !strings.Contains(raw, want) {
			t.Errorf("serialized payload missing %s:\n%s", want, raw)
		}
	}
}

// TestDispatchOutboundThinkingCarriesThroughputMetrics is the reasoning-surface
// counterpart of the stream contract test: a message.thinking with full Metadata
// must reach the WebSocket payload with every metric populated (so the UI can
// show a live tok/s while the model is still thinking) and the exact snake_case
// keys the frontend reads.
func TestDispatchOutboundThinkingCarriesThroughputMetrics(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-thinking"
	client := registerFakeWSClient(t, ts, sessionKey)

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Event:     "message.thinking",
		Content:   "let me think",
		MessageID: "msg-thinking-1",
		Metadata:  fullThinkingMetrics(),
	})

	payload, raw := thinkingEventPayload(t, client)

	if payload.MessageID != "msg-thinking-1" || payload.SessionKey != sessionKey || payload.Chunk != "let me think" {
		t.Errorf("identity fields = %+v, want msg-thinking-1/%s/let me think", payload, sessionKey)
	}
	if payload.Tokens != 96 {
		t.Errorf("tokens = %d, want 96", payload.Tokens)
	}
	if payload.ElapsedMS != 2400 {
		t.Errorf("elapsed_ms = %d, want 2400", payload.ElapsedMS)
	}
	if payload.TokPerSec != 40 {
		t.Errorf("tok_per_sec = %v, want 40", payload.TokPerSec)
	}
	if !payload.Estimated {
		t.Error("estimated = false, want true")
	}

	for _, want := range []string{
		`"tokens":96`,
		`"elapsed_ms":2400`,
		`"tok_per_sec":40`,
		`"estimated":true`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("serialized thinking payload missing %s:\n%s", want, raw)
		}
	}
}

// TestDispatchOutboundThinkingWithoutMetricsDefaultsToZero pins the
// absent-metadata path for reasoning: no panic, metrics at their zero values,
// and the four keys STILL present in the JSON (no omitempty) so the frontend
// shape never changes.
func TestDispatchOutboundThinkingWithoutMetricsDefaultsToZero(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-thinking-nometrics"
	client := registerFakeWSClient(t, ts, sessionKey)

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Event:     "message.thinking",
		Content:   "chunk",
		MessageID: "msg-thinking-nometrics",
	})

	payload, raw := thinkingEventPayload(t, client)

	if payload.Tokens != 0 || payload.ElapsedMS != 0 || payload.TokPerSec != 0 || payload.Estimated {
		t.Errorf("metrics = (%d, %d, %v, %v), want all zero/false",
			payload.Tokens, payload.ElapsedMS, payload.TokPerSec, payload.Estimated)
	}
	for _, want := range []string{
		`"tokens":0`,
		`"elapsed_ms":0`,
		`"tok_per_sec":0`,
		`"estimated":false`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("serialized thinking payload missing %s:\n%s", want, raw)
		}
	}
}

// TestWSThinkingPayloadJSONShapeIsDeterministic freezes the reasoning wire
// shape: the same four metric keys as the stream payload, always present, with
// no omitempty and no extra "done" field.
func TestWSThinkingPayloadJSONShapeIsDeterministic(t *testing.T) {
	payload := WSThinkingPayload{
		MessageID:  "m1",
		SessionKey: "s1",
		Chunk:      "hmm",
		Tokens:     12,
		ElapsedMS:  1500,
		TokPerSec:  8,
		Estimated:  true,
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	want := `{"message_id":"m1","session_key":"s1","chunk":"hmm",` +
		`"tokens":12,"elapsed_ms":1500,"tok_per_sec":8,"estimated":true}`
	if string(raw) != want {
		t.Errorf("payload JSON = %s\nwant            %s", raw, want)
	}

	zero, err := json.Marshal(WSThinkingPayload{MessageID: "m2", Chunk: ""})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(zero), `"tokens":0`) ||
		!strings.Contains(string(zero), `"estimated":false`) {
		t.Errorf("metric keys must be present even at their zero value: %s", zero)
	}
}

// TestNonFiniteThinkingMetricsKeepPayloadMarshalable mirrors the stream test:
// the shared coercion guards make the reasoning payload always marshalable, with
// the offending metric at 0.
func TestNonFiniteThinkingMetricsKeepPayloadMarshalable(t *testing.T) {
	meta := map[string]string{
		"tokens":      "NaN",
		"elapsed_ms":  "Inf",
		"tok_per_sec": "-Inf",
		"estimated":   "NaN",
	}

	payload := newWSThinkingPayload("m1", "s1", "chunk", meta)

	if payload.Tokens != 0 || payload.ElapsedMS != 0 || payload.TokPerSec != 0 || payload.Estimated {
		t.Errorf("metrics = (%d, %d, %v, %v), want all zero/false",
			payload.Tokens, payload.ElapsedMS, payload.TokPerSec, payload.Estimated)
	}

	if _, err := json.Marshal(payload); err != nil {
		t.Fatalf("Marshal() error = %v, payload must stay encodable: %+v", err, payload)
	}
}

// TestThinkingMetricsFanoutToRESTSubscriber proves the reasoning payload rides
// the same REST/SSE fan-out as the answer one: the SSE writer serializes this
// exact value, so the stream contract covers both surfaces.
func TestThinkingMetricsFanoutToRESTSubscriber(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID + "-thinking-fanout"
	sub := ts.channel.registerRESTStreamSubscriber(sessionKey, "msg-thinking-fanout")
	defer ts.channel.unregisterRESTStreamSubscriber(sub.id)

	ts.channel.dispatchOutboundMessage(bus.OutboundMessage{
		Channel:   ChannelName,
		ChatID:    sessionKey,
		Event:     "message.thinking",
		Content:   "chunk",
		MessageID: "msg-thinking-fanout",
		Metadata:  fullThinkingMetrics(),
	})

	select {
	case evt := <-sub.ch:
		if evt.event != "message.thinking" {
			t.Fatalf("fanned-out event = %q, want message.thinking", evt.event)
		}
		payload, ok := evt.data.(WSThinkingPayload)
		if !ok {
			t.Fatalf("fanned-out data type = %T, want WSThinkingPayload", evt.data)
		}
		if payload.Tokens != 96 || payload.TokPerSec != 40 || !payload.Estimated {
			t.Errorf("fanned-out payload = %+v, want the thinking metrics", payload)
		}
	default:
		t.Fatal("no event fanned out to the REST stream subscriber")
	}
}
