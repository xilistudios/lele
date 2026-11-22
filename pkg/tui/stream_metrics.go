package tui

import (
	"fmt"
	"math"
	"strconv"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

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

func metadataBool(v any) bool {
	switch n := v.(type) {
	case bool:
		return n
	case string:
		return n == "true" || n == "1"
	}
	return false
}

func finiteFloat(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

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

func formatTokPerSec(rate float64) string {
	if rate <= 0 {
		return ""
	}
	return fmt.Sprintf(i18n.T("tui.tokensPerSecond"), rate)
}
