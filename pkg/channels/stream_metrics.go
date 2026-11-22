package channels

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

const (
	metaKeyTokens    = "tokens"
	metaKeyElapsedMS = "elapsed_ms"
	metaKeyTokPerSec = "tok_per_sec"
	metaKeyEstimated = "estimated"
)

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

func newWSThinkingPayload(messageID, sessionKey, chunk string, meta map[string]string) WSThinkingPayload {
	p := WSThinkingPayload{
		MessageID:  messageID,
		SessionKey: sessionKey,
		Chunk:      chunk,
	}
	p.applyStreamMetrics(meta)
	return p
}

func (p *WSStreamPayload) applyStreamMetrics(meta map[string]string) {
	p.Tokens, p.ElapsedMS, p.TokPerSec, p.Estimated = metricsFromMeta(meta)
}

func (p *WSThinkingPayload) applyStreamMetrics(meta map[string]string) {
	p.Tokens, p.ElapsedMS, p.TokPerSec, p.Estimated = metricsFromMeta(meta)
}

func metricsFromMeta(meta map[string]string) (tokens int, elapsedMS int64, tokPerSec float64, estimated bool) {
	return metadataInt(meta, metaKeyTokens),
		metadataInt64(meta, metaKeyElapsedMS),
		metadataFloat64(meta, metaKeyTokPerSec),
		metadataBool(meta, metaKeyEstimated)
}

func metadataInt(meta map[string]string, key string) int {
	return coerceInt(meta[key])
}

func metadataInt64(meta map[string]string, key string) int64 {
	return coerceInt64(meta[key])
}

func metadataFloat64(meta map[string]string, key string) float64 {
	return coerceFloat64(meta[key])
}

func metadataBool(meta map[string]string, key string) bool {
	return coerceBool(meta[key])
}

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

func finiteFloat64(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}
