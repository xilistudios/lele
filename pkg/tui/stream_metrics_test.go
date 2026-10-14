package tui

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// TestMetadataParsers_Tolerant exercises the tolerant metadata accessors used
// to read message.stream throughput metrics. On the wire the numbers are
// encoded as strings, but the signed contract allows int/int64/float64 writers,
// so every accessor must accept all of them and default to 0 otherwise.
func TestMetadataParsers_Tolerant(t *testing.T) {
	intCases := []struct {
		name string
		in   any
		want int
	}{
		{"string", "100", 100},
		{"string float", "100.7", 100},
		{"int", 100, 100},
		{"int64", int64(100), 100},
		{"float64", 100.9, 100},
		{"nil", nil, 0},
		{"empty", "", 0},
		{"garbage", "abc", 0},
		// Non-finite values (strconv.ParseFloat accepts them silently) must
		// degrade to the zero value, both as strings and as native floats.
		{"string NaN", "NaN", 0},
		{"string Inf", "Inf", 0},
		{"string +Inf", "+Inf", 0},
		{"string -Inf", "-Inf", 0},
		{"float64 NaN", math.NaN(), 0},
		{"float64 Inf", math.Inf(1), 0},
		{"float32 NaN", float32(math.NaN()), 0},
	}
	for _, tc := range intCases {
		if got := metadataInt(tc.in); got != tc.want {
			t.Errorf("metadataInt(%s=%v) = %d, want %d", tc.name, tc.in, got, tc.want)
		}
	}

	int64Cases := []struct {
		name string
		in   any
		want int64
	}{
		{"string", "2500", 2500},
		{"int64", int64(2500), 2500},
		{"int", 2500, 2500},
		{"float64", 2500.0, 2500},
		{"nil", nil, 0},
		{"string NaN", "NaN", 0},
		{"string Inf", "Inf", 0},
		{"float64 NaN", math.NaN(), 0},
		{"float64 Inf", math.Inf(1), 0},
	}
	for _, tc := range int64Cases {
		if got := metadataInt64(tc.in); got != tc.want {
			t.Errorf("metadataInt64(%s=%v) = %d, want %d", tc.name, tc.in, got, tc.want)
		}
	}

	floatCases := []struct {
		name string
		in   any
		want float64
	}{
		{"string", "42.5", 42.5},
		{"float64", 42.5, 42.5},
		{"int64", int64(42), 42.0},
		{"int", 42, 42.0},
		{"nil", nil, 0},
		{"empty", "", 0},
		{"string NaN", "NaN", 0},
		{"string Inf", "Inf", 0},
		{"string -Inf", "-Inf", 0},
		{"float64 NaN", math.NaN(), 0},
		{"float64 +Inf", math.Inf(1), 0},
		{"float64 -Inf", math.Inf(-1), 0},
	}
	for _, tc := range floatCases {
		if got := metadataFloat(tc.in); got != tc.want {
			t.Errorf("metadataFloat(%s=%v) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}

	boolCases := []struct {
		name string
		in   any
		want bool
	}{
		{"string true", "true", true},
		{"string 1", "1", true},
		{"bool true", true, true},
		{"string false", "false", false},
		{"bool false", false, false},
		{"nil", nil, false},
		// A non-finite value is not a truthiness token either.
		{"string NaN", "NaN", false},
		{"float64 Inf", math.Inf(1), false},
	}
	for _, tc := range boolCases {
		if got := metadataBool(tc.in); got != tc.want {
			t.Errorf("metadataBool(%s=%v) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}

// TestFormatTokPerSec checks the exact fragment rendered in the status line and
// that a non-positive rate yields an empty string (so callers can omit it).
func TestFormatTokPerSec(t *testing.T) {
	i18n.InitWithLanguage("en")
	if got := formatTokPerSec(42.53); got != "42.5 tok/s" {
		t.Fatalf("formatTokPerSec(42.53) = %q, want %q", got, "42.5 tok/s")
	}
	for _, zero := range []float64{0, -1} {
		if got := formatTokPerSec(zero); got != "" {
			t.Fatalf("formatTokPerSec(%v) = %q, want empty", zero, got)
		}
	}
}

// TestRenderStatusLine_ShowsLiveTokPerSec verifies the live throughput appears
// next to the spinner while a turn streams, in a single status line so the
// layout height is unchanged.
func TestRenderStatusLine_ShowsLiveTokPerSec(t *testing.T) {
	m := newTestModel(t)
	i18n.InitWithLanguage("en") // after NewModel, which resets to the config default

	key := "tui:chat:tok-per-sec-live"
	m.sessionMgr.GetOrCreate(key)
	m.currentKey = key
	m.showWelcome = false
	m.width, m.height = 200, 40

	m.processing = true
	m.startTime = time.Now()
	m.currentStream = "streaming answer"
	m.streamTokens = 100
	m.streamElapsedMs = 2000
	m.streamTokPerSec = 50.0

	status := m.renderStatusLine(int(float64(m.width) * leftColumnRatio))
	if !strings.Contains(status, "50.0 tok/s") {
		t.Fatalf("status line %q does not show the live throughput", status)
	}
	if !strings.Contains(status, i18n.T("tui.processing")) {
		t.Fatalf("status line %q lost the processing indicator", status)
	}
	if strings.Contains(status, "\n") {
		t.Fatalf("status line %q must stay a single line", status)
	}

	// The full frame must carry it too, and its height must not grow.
	view := m.View()
	if !strings.Contains(view, "50.0 tok/s") {
		t.Fatalf("view does not show the live throughput:\n%s", view)
	}
	if got := len(strings.Split(view, "\n")); got != m.height {
		t.Fatalf("view height = %d, want exactly %d", got, m.height)
	}
}

// TestRenderStatusLine_ShowsFinalTokPerSec verifies the last segment's rate is
// kept next to "Done in Xs" once the turn finishes.
func TestRenderStatusLine_ShowsFinalTokPerSec(t *testing.T) {
	m := newTestModel(t)
	i18n.InitWithLanguage("en") // after NewModel, which resets to the config default

	key := "tui:chat:tok-per-sec-done"
	m.sessionMgr.GetOrCreate(key)
	m.currentKey = key
	m.showWelcome = false
	m.width, m.height = 200, 40

	m.processing = false
	m.startTime = time.Time{}
	m.lastDuration = 3200 * time.Millisecond
	m.lastTokPerSec = 42.5

	status := m.renderStatusLine(int(float64(m.width) * leftColumnRatio))
	if !strings.Contains(status, "42.5 tok/s") {
		t.Fatalf("status line %q does not show the final throughput", status)
	}
	// The throughput must be joined to the done indicator, not stand alone.
	doneText := fmt.Sprintf(i18n.T("tui.doneIn"), m.lastDuration.Seconds())
	if !strings.Contains(status, doneText) {
		t.Fatalf("status line %q lost the done indicator %q", status, doneText)
	}
	if strings.Contains(status, "\n") {
		t.Fatalf("status line %q must stay a single line", status)
	}
}

// TestRenderStatusLine_HidesTokPerSecWithoutMetrics guards the clean-omission
// rule: a stream-less turn (error reply, tool-only reply) must never leave a
// stray "0.0 tok/s" on the status line, whether it is still processing or done.
func TestRenderStatusLine_HidesTokPerSecWithoutMetrics(t *testing.T) {
	m := newTestModel(t)
	i18n.InitWithLanguage("en") // after NewModel, which resets to the config default

	key := "tui:chat:tok-per-sec-hidden"
	m.sessionMgr.GetOrCreate(key)
	m.currentKey = key
	m.showWelcome = false
	m.width, m.height = 200, 40

	// Processing but no metrics seen yet.
	m.processing = true
	m.startTime = time.Now()
	m.currentStream = "no metrics here"
	m.streamTokens = 0
	m.streamElapsedMs = 0
	m.streamTokPerSec = 0
	m.streamMetricsEstimated = false

	status := m.renderStatusLine(int(float64(m.width) * leftColumnRatio))
	if strings.Contains(status, "tok/s") {
		t.Fatalf("processing status line %q should omit tok/s when there are no metrics", status)
	}

	// Done but the segment produced no throughput.
	m.processing = false
	m.startTime = time.Time{}
	m.lastDuration = 1500 * time.Millisecond
	m.lastTokPerSec = 0

	status = m.renderStatusLine(int(float64(m.width) * leftColumnRatio))
	if strings.Contains(status, "tok/s") {
		t.Fatalf("done status line %q should omit tok/s when there are no metrics", status)
	}
}

// TestHandleOutboundMsg_ParsesStreamMetrics verifies the event handler plumbs
// the metadata into the model state, including the "done" retention of the
// final rate, using the string encoding the wire actually uses.
func TestHandleOutboundMsg_ParsesStreamMetrics(t *testing.T) {
	m := newTestModel(t)
	i18n.InitWithLanguage("en") // after NewModel, which resets to the config default

	key := "tui:chat:tok-per-sec-handler"
	m.sessionMgr.GetOrCreate(key)
	m.currentKey = key
	m.showWelcome = false

	msg := outboundMsg{msg: bus.OutboundMessage{
		ChatID:    key,
		Event:     "message.stream",
		MessageID: "m1",
		Content:   "hello",
		Metadata: map[string]string{
			"tokens":      "100",
			"elapsed_ms":  "2000",
			"tok_per_sec": "50",
			"estimated":   "true",
			"done":        "false",
		},
	}}

	updated, _ := m.Update(msg)
	m = updated.(*Model)

	if m.streamTokens != 100 {
		t.Errorf("streamTokens = %d, want 100", m.streamTokens)
	}
	if m.streamElapsedMs != 2000 {
		t.Errorf("streamElapsedMs = %d, want 2000", m.streamElapsedMs)
	}
	if m.streamTokPerSec != 50 {
		t.Errorf("streamTokPerSec = %v, want 50", m.streamTokPerSec)
	}
	if !m.streamMetricsEstimated {
		t.Error("streamMetricsEstimated = false, want true")
	}
	if m.lastTokPerSec != 0 {
		t.Errorf("lastTokPerSec = %v, want 0 before the done chunk", m.lastTokPerSec)
	}

	// The final chunk (done=true) retains the rate for the "Done in" line.
	doneMsg := outboundMsg{msg: bus.OutboundMessage{
		ChatID:    key,
		Event:     "message.stream",
		MessageID: "m1",
		Content:   "",
		Metadata: map[string]string{
			"tokens":      "120",
			"elapsed_ms":  "2400",
			"tok_per_sec": "50",
			"estimated":   "true",
			"done":        "true",
		},
	}}
	updated, _ = m.Update(doneMsg)
	m = updated.(*Model)

	if m.lastTokPerSec != 50 {
		t.Errorf("lastTokPerSec = %v, want 50 after the done chunk", m.lastTokPerSec)
	}
}

// TestHandleOutboundMsg_ParsesThinkingMetrics verifies reasoning chunks feed the
// SAME live counters as the answer: the tok/s readout is up from the first
// thinking token, and a rate-less answer chunk does not blank it at the
// thinking → answer hand-off.
func TestHandleOutboundMsg_ParsesThinkingMetrics(t *testing.T) {
	m := newTestModel(t)
	i18n.InitWithLanguage("en") // after NewModel, which resets to the config default

	key := "tui:chat:tok-per-sec-thinking"
	m.sessionMgr.GetOrCreate(key)
	m.currentKey = key
	m.showWelcome = false
	m.width, m.height = 200, 40
	m.processing = true
	m.startTime = time.Now()

	// First reasoning chunk: metrics present → the readout turns on while the
	// model is still thinking.
	updated, _ := m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID:    key,
		Event:     "message.thinking",
		MessageID: "m1",
		Content:   "let me think",
		Metadata: map[string]string{
			"tokens":      "80",
			"elapsed_ms":  "2000",
			"tok_per_sec": "40",
			"estimated":   "true",
		},
	}})
	m = updated.(*Model)

	if m.streamTokPerSec != 40 {
		t.Fatalf("streamTokPerSec = %v, want 40 from the thinking chunk", m.streamTokPerSec)
	}
	if m.currentThinking != "let me think" {
		t.Fatalf("currentThinking = %q, want the reasoning chunk appended", m.currentThinking)
	}
	if status := m.renderStatusLine(int(float64(m.width) * leftColumnRatio)); !strings.Contains(status, "40.0 tok/s") {
		t.Fatalf("status line %q should show tok/s while thinking", status)
	}

	// The answer's first chunk reports no rate (no elapsed window yet): the
	// previous figure must be kept, not blanked.
	updated, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID:    key,
		Event:     "message.stream",
		MessageID: "m1",
		Content:   "Answer",
		Metadata: map[string]string{
			"tokens":      "2",
			"elapsed_ms":  "0",
			"tok_per_sec": "0",
			"estimated":   "true",
			"done":        "false",
		},
	}})
	m = updated.(*Model)

	if m.streamTokPerSec != 40 {
		t.Fatalf("streamTokPerSec = %v, want 40 retained across the thinking→answer hand-off", m.streamTokPerSec)
	}

	// A later answer chunk with a real rate takes over.
	updated, _ = m.Update(outboundMsg{msg: bus.OutboundMessage{
		ChatID:    key,
		Event:     "message.stream",
		MessageID: "m1",
		Content:   " more",
		Metadata: map[string]string{
			"tokens":      "10",
			"elapsed_ms":  "500",
			"tok_per_sec": "20",
			"estimated":   "true",
			"done":        "false",
		},
	}})
	m = updated.(*Model)
	if m.streamTokPerSec != 20 {
		t.Fatalf("streamTokPerSec = %v, want 20 from the answer chunk", m.streamTokPerSec)
	}
}

// TestResetStreamState_ClearsThroughputMetrics ensures no stale figure can
// survive a reset (new segment / session switch).
func TestResetStreamState_ClearsThroughputMetrics(t *testing.T) {
	m := newTestModel(t)
	m.streamTokens = 100
	m.streamElapsedMs = 2000
	m.streamTokPerSec = 50
	m.streamMetricsEstimated = true
	m.lastTokPerSec = 42.5

	m.resetStreamState()

	if m.streamTokens != 0 || m.streamElapsedMs != 0 || m.streamTokPerSec != 0 ||
		m.streamMetricsEstimated || m.lastTokPerSec != 0 {
		t.Fatalf("resetStreamState left throughput metrics behind: %+v", m)
	}
}
