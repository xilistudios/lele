package tui

import (
	"fmt"
	"math"
	"strconv"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// Stream throughput metrics travel in bus.OutboundMessage.Metadata. Today that
// field is map[string]string on the wire, so the numeric counters arrive
// encoded as strings; the signed contract, however, allows a writer to publish
// int, int64 or float64 values. Every accessor below therefore accepts `any`
// and degrades to a zero value instead of panicking. Callers pass the map cell
// directly (e.g. md["tokens"]), which keeps the call sites compiling and
// behaving identically whether Metadata stays map[string]string or later
// becomes map[string]any.

// metadataInt parses an integer metric (e.g. "tokens").
func metadataInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case int32:
		return int(n)
	case float64:
		return int(finiteFloat(n))
	case float32:
		return int(finiteFloat(float64(n)))
	case string:
		if n == "" {
			return 0
		}
		if i, err := strconv.Atoi(n); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return int(finiteFloat(f))
		}
	}
	return 0
}

// metadataInt64 parses a 64-bit integer metric (e.g. "elapsed_ms").
func metadataInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case int32:
		return int64(n)
	case float64:
		return int64(finiteFloat(n))
	case float32:
		return int64(finiteFloat(float64(n)))
	case string:
		if n == "" {
			return 0
		}
		if i, err := strconv.ParseInt(n, 10, 64); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return int64(finiteFloat(f))
		}
	}
	return 0
}

// metadataFloat parses a floating-point metric (e.g. "tok_per_sec").
func metadataFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return finiteFloat(n)
	case float32:
		return finiteFloat(float64(n))
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	case string:
		if n == "" {
			return 0
		}
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return finiteFloat(f)
		}
	}
	return 0
}

// metadataBool parses a boolean metric ("estimated", "done"). The wire encodes
// booleans as "true"/"false" strings, but a real bool is accepted too.
func metadataBool(v any) bool {
	switch n := v.(type) {
	case bool:
		return n
	case string:
		return n == "true" || n == "1"
	}
	return false
}

// finiteFloat maps a non-finite float64 (NaN, +Inf, -Inf) to 0 and returns
// anything else unchanged. These accessors read metrics off the wire, and both
// strconv.ParseFloat ("NaN", "Inf", "+Inf", "-Inf") and a native float64 can
// carry a non-finite value that no metric ever legitimately has; int(NaN) in
// particular would render a nonsense counter. The zero value is the same "no
// metric" these parsers already return for malformed input, and it keeps the
// two wire parsers (this one and channels/stream_metrics.go) agreeing on the
// edge.
func finiteFloat(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

// applyStreamMetrics folds one chunk's throughput metadata into the live
// counters. BOTH surfaces feed it: message.stream (answer) and message.thinking
// (reasoning), which the backend meters separately, so the readout is already
// live while the model is still thinking.
//
// A non-positive rate is not a usable reading — it is exactly what the first
// chunk of a surface reports when no time has elapsed yet — so the previous
// figure is kept instead. That is what stops the readout from blinking off at
// the thinking → answer hand-off (the answer's first chunk carries no rate).
func (m *Model) applyStreamMetrics(md map[string]string) {
	if md == nil {
		return
	}
	m.streamTokens = metadataInt(md["tokens"])
	m.streamElapsedMs = metadataInt64(md["elapsed_ms"])
	m.streamMetricsEstimated = metadataBool(md["estimated"])
	if rate := metadataFloat(md["tok_per_sec"]); rate > 0 {
		m.streamTokPerSec = rate
	}
}

// formatTokPerSec renders the tok/s fragment shown in the status line, e.g.
// "42.5 tok/s". It returns "" for a non-positive rate so callers can omit the
// fragment cleanly: a stream-less turn (error reply, tool call with no visible
// output) must never leave a stray "0.0 tok/s" on the line.
func formatTokPerSec(rate float64) string {
	if rate <= 0 {
		return ""
	}
	return fmt.Sprintf(i18n.T("tui.tokensPerSecond"), rate)
}
