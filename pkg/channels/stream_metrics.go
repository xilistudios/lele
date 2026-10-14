package channels

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// Streaming throughput metrics (tokens, elapsed time, tok/s).
//
// The agent's stream handler publishes them on every "message.stream" (answer
// surface) and "message.thinking" (reasoning surface) event inside
// bus.OutboundMessage.Metadata, which is a map[string]string: the
// producer pre-formats the numbers with strconv, and "done"/"estimated" travel
// as the strings "true"/"false". The coercion helpers below accept BOTH that
// string encoding (what travels on the bus today) and the native numeric/bool
// types, because the same values can arrive typed in two situations:
//
//   - a future/alternate producer writes a Go int/int64/float64/bool directly;
//   - a value that went through a JSON round-trip (durable spool, relay) comes
//     back as float64, since encoding/json decodes every number that way.
//
// All helpers are total functions: an unknown type, a malformed string or a
// missing key yields the zero value instead of panicking. A non-finite number
// (NaN, +Inf, -Inf) is treated as a missing one too, so a bogus metric can never
// reach the wire: json.Marshal refuses to encode NaN/Inf and int(NaN) is an
// implementation-defined huge value. That mirrors the pre-existing style of
// this package (see the strconv.Atoi calls on Metadata["layer"] etc. in
// dispatchOutboundMessage) and keeps a malformed metric from taking down a live
// stream.
const (
	metaKeyTokens    = "tokens"
	metaKeyElapsedMS = "elapsed_ms"
	metaKeyTokPerSec = "tok_per_sec"
	metaKeyEstimated = "estimated"
)

// newWSStreamPayload builds the payload of a message.stream event. It is the
// single place where the agent's throughput metrics are mirrored onto the wire
// for the ANSWER surface, shared by the streaming case and by the legacy
// full-message path so both emit an identically shaped payload. The legacy path
// carries no metrics in Metadata, so they serialise as zeros - the shape stays
// deterministic, which is what the frontend contract relies on.
func newWSStreamPayload(messageID, sessionKey, chunk string, done bool, meta map[string]string) WSStreamPayload {
	p := WSStreamPayload{
		MessageID:  messageID,
		SessionKey: sessionKey,
		Chunk:      chunk,
		Done:       done,
	}
	p.applyStreamMetrics(meta)
	return p
}

// newWSThinkingPayload builds the payload of a message.thinking event. It is the
// reasoning-surface counterpart of newWSStreamPayload: same metric keys, same
// zero-defaulting contract, so the web UI can render a live token rate while the
// model is still thinking.
func newWSThinkingPayload(messageID, sessionKey, chunk string, meta map[string]string) WSThinkingPayload {
	p := WSThinkingPayload{
		MessageID:  messageID,
		SessionKey: sessionKey,
		Chunk:      chunk,
	}
	p.applyStreamMetrics(meta)
	return p
}

// applyStreamMetrics fills the throughput fields of the payload from the
// outbound Metadata of a message.stream event. It is a no-op (zero values) when
// the map is nil or the keys are absent, which is exactly what the frontend
// contract expects for events that carry no metrics.
func (p *WSStreamPayload) applyStreamMetrics(meta map[string]string) {
	p.Tokens, p.ElapsedMS, p.TokPerSec, p.Estimated = metricsFromMeta(meta)
}

// applyStreamMetrics fills the throughput fields of a message.thinking payload
// from the same Metadata keys as the stream payload: the reasoning surface is
// mirrored with an identical shape, only sourced from a different event.
func (p *WSThinkingPayload) applyStreamMetrics(meta map[string]string) {
	p.Tokens, p.ElapsedMS, p.TokPerSec, p.Estimated = metricsFromMeta(meta)
}

// metricsFromMeta reads the four throughput metrics out of a Metadata map. It is
// shared by the message.stream and message.thinking payload builders so the two
// surfaces can never diverge on the wire (a missing key defaults to the zero
// value in every case).
func metricsFromMeta(meta map[string]string) (tokens int, elapsedMS int64, tokPerSec float64, estimated bool) {
	return metadataInt(meta, metaKeyTokens),
		metadataInt64(meta, metaKeyElapsedMS),
		metadataFloat64(meta, metaKeyTokPerSec),
		metadataBool(meta, metaKeyEstimated)
}

// metadataInt reads key from the metadata map and coerces it to int (0 when
// the key is missing or the value cannot be interpreted as a number).
func metadataInt(meta map[string]string, key string) int {
	return coerceInt(meta[key])
}

// metadataInt64 reads key from the metadata map and coerces it to int64 (0
// when the key is missing or the value cannot be interpreted as a number).
func metadataInt64(meta map[string]string, key string) int64 {
	return coerceInt64(meta[key])
}

// metadataFloat64 reads key from the metadata map and coerces it to float64 (0
// when the key is missing or the value cannot be interpreted as a number).
func metadataFloat64(meta map[string]string, key string) float64 {
	return coerceFloat64(meta[key])
}

// metadataBool reads key from the metadata map and coerces it to bool (false
// when the key is missing or the value is not a recognised truthiness token).
func metadataBool(meta map[string]string, key string) bool {
	return coerceBool(meta[key])
}

// coerceInt converts an arbitrary metric value to int.
//
// Strings are parsed as integers first and as decimals second (a numeric value
// that went through JSON may be "12.0" or even "1.2e3"); both are truncated
// toward zero, matching the float64 branch. Anything unparseable yields 0, and
// so does a non-finite value (NaN/Inf): int(NaN) would otherwise evaluate to an
// implementation-defined huge negative number rather than to "no metric".
func coerceInt(v any) int {
	switch t := v.(type) {
	case nil:
		return 0
	case int:
		return t
	case int8:
		return int(t)
	case int16:
		return int(t)
	case int32:
		return int(t)
	case int64:
		return int(t)
	case uint:
		return int(t)
	case uint8:
		return int(t)
	case uint16:
		return int(t)
	case uint32:
		return int(t)
	case uint64:
		return int(t)
	case float32:
		return int(finiteFloat64(float64(t)))
	case float64:
		return int(finiteFloat64(t))
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return int(i)
		}
		if f, err := t.Float64(); err == nil {
			return int(finiteFloat64(f))
		}
		return 0
	case bool:
		if t {
			return 1
		}
		return 0
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0
		}
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return int(i)
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int(finiteFloat64(f))
		}
		return 0
	default:
		return 0
	}
}

// coerceInt64 converts an arbitrary metric value to int64. It keeps the full
// int64 range instead of narrowing through int (32-bit builds included).
func coerceInt64(v any) int64 {
	switch t := v.(type) {
	case nil:
		return 0
	case int:
		return int64(t)
	case int8:
		return int64(t)
	case int16:
		return int64(t)
	case int32:
		return int64(t)
	case int64:
		return t
	case uint:
		return int64(t)
	case uint8:
		return int64(t)
	case uint16:
		return int64(t)
	case uint32:
		return int64(t)
	case uint64:
		return int64(t)
	case float32:
		return int64(finiteFloat64(float64(t)))
	case float64:
		return int64(finiteFloat64(t))
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return int64(finiteFloat64(f))
		}
		return 0
	case bool:
		if t {
			return 1
		}
		return 0
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0
		}
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int64(finiteFloat64(f))
		}
		return 0
	default:
		return 0
	}
}

// coerceFloat64 converts an arbitrary metric value to float64. Strings are
// parsed with strconv.ParseFloat, which accepts both the plain decimal the
// producer emits (strconv.FormatFloat with format 'f' from the agent's stream
// handler, which never uses exponent notation) and the exponent form a JSON
// round-trip can introduce. A non-finite value (NaN, +Inf, -Inf - all accepted
// by ParseFloat without error) degrades to 0.
func coerceFloat64(v any) float64 {
	switch t := v.(type) {
	case nil:
		return 0
	case int:
		return float64(t)
	case int8:
		return float64(t)
	case int16:
		return float64(t)
	case int32:
		return float64(t)
	case int64:
		return float64(t)
	case uint:
		return float64(t)
	case uint8:
		return float64(t)
	case uint16:
		return float64(t)
	case uint32:
		return float64(t)
	case uint64:
		return float64(t)
	case float32:
		return finiteFloat64(float64(t))
	case float64:
		return finiteFloat64(t)
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return finiteFloat64(f)
		}
		return 0
	case bool:
		if t {
			return 1
		}
		return 0
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return finiteFloat64(f)
		}
		return 0
	default:
		return 0
	}
}

// coerceBool converts an arbitrary metric value to bool.
//
// Only the tokens strconv.ParseBool understands count as true ("1", "t", "T",
// "true", "TRUE", "True"); everything else is false, so an unexpected payload
// degrades to "not estimated" rather than to a phantom indicator. Numbers
// follow the usual non-zero convention.
func coerceBool(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(t))
		return err == nil && b
	default:
		return coerceFloat64(v) != 0
	}
}

// finiteFloat64 maps a non-finite float64 (NaN, +Inf, -Inf) to 0 and returns
// anything else unchanged. The metrics never legitimately carry a non-finite
// value, but strconv.ParseFloat accepts "NaN"/"Inf"/"+Inf"/"-Inf" without
// error and a native float64 can hold them too. Encoding the current value
// would either blow up the JSON payload (json.Marshal cannot encode NaN/Inf) or
// turn an int conversion into an arbitrary huge number, so the zero value - the
// same "no metric" the helpers return for a malformed input - is used instead.
func finiteFloat64(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}
