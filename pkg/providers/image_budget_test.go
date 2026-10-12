// Package providers: byte/char accounting and deterministic shrinking for
// multimodal (image/video) message payloads. See image_budget.go.

package providers

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// deepCopyMessages snapshots a message list including the pointed-to
// ImageURL/VideoURL values, so tests can detect any mutation of the input.
func deepCopyMessages(in []Message) []Message {
	out := make([]Message, len(in))
	for i, m := range in {
		out[i] = m
		if m.ContentParts != nil {
			out[i].ContentParts = append([]ContentPart(nil), m.ContentParts...)
			for j := range out[i].ContentParts {
				p := &out[i].ContentParts[j]
				if p.ImageURL != nil {
					u := *p.ImageURL
					p.ImageURL = &u
				}
				if p.VideoURL != nil {
					u := *p.VideoURL
					p.VideoURL = &u
				}
			}
		}
		if m.ToolCalls != nil {
			out[i].ToolCalls = append([]ToolCall(nil), m.ToolCalls...)
			for j := range out[i].ToolCalls {
				if out[i].ToolCalls[j].Function != nil {
					f := *out[i].ToolCalls[j].Function
					out[i].ToolCalls[j].Function = &f
				}
			}
		}
	}
	return out
}

// TestEstimateRequestBytes_Calibration pins the two bounds of the estimator:
// it must never fall below the real marshaled size and must stay within 25 %
// on realistic content.
func TestEstimateRequestBytes_Calibration(t *testing.T) {
	longText := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 100) // ~4.4 KB
	bigBase64 := strings.Repeat("A", 40*1024)

	msgs := []Message{
		{Role: "user", Content: longText},
		{Role: "user", Content: "", ContentParts: []ContentPart{
			{Type: "text", Text: "describe this screenshot"},
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64," + bigBase64}},
		}},
		{Role: "user", Content: "what happens in this clip?", ContentParts: []ContentPart{
			{Type: "video_url", VideoURL: &VideoURL{
				URL: "data:video/mp4;base64," + strings.Repeat("B", 512),
				FPS: 0.5,
			}},
		}},
		{
			Role:             "assistant",
			Content:          "",
			ReasoningContent: "thinking about it",
			ToolCallID:       "call_123",
			ToolCalls: []ToolCall{{
				ID: "call_123",
				Function: &FunctionCall{
					Name:      "get_weather",
					Arguments: `{"city":"Paris","unit":"c"}`,
				},
			}},
		},
	}

	wire, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	got := EstimateRequestBytes(msgs)
	if got < len(wire) {
		t.Errorf("EstimateRequestBytes = %d is BELOW the real wire size %d (unsafe)", got, len(wire))
	}
	if got > len(wire)*5/4 {
		t.Errorf("EstimateRequestBytes = %d exceeds the real wire size %d by more than 25%%", got, len(wire))
	}

	// Sub-case: JSON-escapable bytes (quotes, angle brackets, ampersand,
	// newline, tab) plus CJK text. The escaping surcharge must stay safe:
	// got >= len(wire) has to hold even when most bytes need escaping.
	tricky := []Message{{
		Role: "user",
		// CJK text (written as escapes to keep source ASCII-only) to prove the
		// surcharge stays safe on multi-byte runes too.
		Content: `he said "hi" & <bye>` + "\n" +
			"\u65e5\u672c\u8a9e\u306e\u30c6\u30ad\u30b9\u30c8\u3067\u3059" + "\n\ttabbed",
	}}
	wireTricky, err := json.Marshal(tricky)
	if err != nil {
		t.Fatalf("marshal tricky fixture: %v", err)
	}
	gotTricky := EstimateRequestBytes(tricky)
	if gotTricky < len(wireTricky) {
		t.Errorf("EstimateRequestBytes(escaping-heavy) = %d is BELOW the real wire size %d (unsafe)",
			gotTricky, len(wireTricky))
	}
}

// TestReduceImageParts_KeepsNewestAndPreservesShape: six messages each carrying
// one heavy part; keepLastImages=4 must keep exactly the four newest heavy
// parts alive, evict the two oldest without leaving any message invalid, and
// must not mutate the input.
func TestReduceImageParts_KeepsNewestAndPreservesShape(t *testing.T) {
	makeMsg := func(i int) Message {
		return Message{
			Role:    "user",
			Content: "",
			ContentParts: []ContentPart{
				{Type: "text", Text: fmt.Sprintf("prompt %d", i)},
				{Type: "image_url", ImageURL: &ImageURL{
					URL: "data:image/png;base64," + strings.Repeat("C", 256),
				}},
			},
		}
	}
	msgs := make([]Message, 6)
	for i := range msgs {
		msgs[i] = makeMsg(i)
	}
	snapshot := deepCopyMessages(msgs)

	got, evicted := ReduceImageParts(msgs, 4)
	if evicted != 2 {
		t.Fatalf("evicted = %d, want 2", evicted)
	}
	if len(got) != len(msgs) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(msgs))
	}
	for i, m := range got {
		if i < 2 {
			// Evicted: the text prompt stays first and intact, in order, and a
			// placeholder part keeps the message valid (never zero parts).
			if len(m.ContentParts) < 1 {
				t.Errorf("message %d: evicted message lost all parts", i)
				continue
			}
			if m.ContentParts[0].Text != fmt.Sprintf("prompt %d", i) {
				t.Errorf("message %d: text part = %q, want %q", i, m.ContentParts[0].Text, fmt.Sprintf("prompt %d", i))
			}
			last := m.ContentParts[len(m.ContentParts)-1]
			if last.Type != "text" || last.Text != omittedPartPlaceholder {
				t.Errorf("message %d: last part = %+v, want placeholder text part", i, last)
			}
			if IsHeavyMultimodalPart(m.ContentParts[len(m.ContentParts)-1]) {
				t.Errorf("message %d: heavy part was not evicted", i)
			}
			continue
		}
		// The four newest heavy parts must be untouched.
		if !IsHeavyMultimodalPart(m.ContentParts[len(m.ContentParts)-1]) {
			t.Errorf("message %d: newest heavy part should have been kept", i)
		}
		if m.ContentParts[0].Text != fmt.Sprintf("prompt %d", i) {
			t.Errorf("message %d: kept message text changed", i)
		}
	}
	// The input slice, its messages and its parts must be untouched.
	if !reflect.DeepEqual(msgs, snapshot) {
		t.Errorf("input messages were mutated:\ngot:  %+v\nwant: %+v", msgs, snapshot)
	}
}

// TestReduceImageParts_RehydratedMessageDropsPart: a message whose textual
// Content already carries the "[image]" marker loses the heavy part outright
// (no duplicate placeholder text).
func TestReduceImageParts_RehydratedMessageDropsPart(t *testing.T) {
	msgs := []Message{{
		Role:    "user",
		Content: "[image]",
		ContentParts: []ContentPart{
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"}},
		},
	}}
	got, evicted := ReduceImageParts(msgs, 0)
	if evicted != 1 {
		t.Fatalf("evicted = %d, want 1", evicted)
	}
	if len(got[0].ContentParts) != 0 {
		t.Fatalf("ContentParts = %+v, want the heavy part deleted", got[0].ContentParts)
	}
	if got[0].Content != "[image]" {
		t.Fatalf("Content = %q, want %q", got[0].Content, "[image]")
	}
}

// TestMessageCharCost_ImageCountedOnceAfterRehydrate pins the accounting bug
// where images stop being counted once Content is repopulated from the content
// parts by Message.UnmarshalJSON: the same logical message must cost the same
// before and after a wire round trip.
func TestMessageCharCost_ImageCountedOnceAfterRehydrate(t *testing.T) {
	// The image part carries the same textual marker the rehydrator will
	// synthesize ("[image]"), so the message's textual weight is identical
	// before and after the round trip and any cost delta means the image
	// itself was counted twice or not at all.
	before := Message{
		Role:    "user",
		Content: "",
		ContentParts: []ContentPart{
			{Type: "image_url", Text: "[image]", ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"}},
		},
	}
	wire, err := json.Marshal([]Message{before})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round []Message
	if err := json.Unmarshal(wire, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	after := round[0]

	if got, want := MessageCharCost(after), MessageCharCost(before); got != want {
		t.Errorf("MessageCharCost after rehydrate = %d, want %d (image must be counted exactly once)", got, want)
	}
	if after.Content != "[image]" {
		t.Fatalf("fixture sanity: after.Content = %q, want the rehydrated %q", after.Content, "[image]")
	}

	// Natural shape (no mirrored marker text): the only delta allowed is the
	// "[image]" marker that textFromParts injects into Content - the image
	// part itself must still be counted exactly once on both sides.
	natural := Message{
		Role:    "user",
		Content: "",
		ContentParts: []ContentPart{
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"}},
		},
	}
	wire, err = json.Marshal([]Message{natural})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(wire, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	delta := MessageCharCost(round[0]) - MessageCharCost(natural)
	if marker := utf8.RuneCountInString("[image]"); delta != marker {
		t.Errorf("natural-shape cost delta after rehydrate = %d, want %d (only the marker)", delta, marker)
	}
}

// TestMessageCharCost_CountsVideoAndMedia pins the fixed per-part costs and the
// double-count guard for text parts.
func TestMessageCharCost_CountsVideoAndMedia(t *testing.T) {
	base := Message{Role: "user"}

	video := Message{
		Role:    "user",
		Content: "",
		ContentParts: []ContentPart{
			{Type: "video_url", VideoURL: &VideoURL{URL: "data:video/mp4;base64,VV"}},
		},
	}
	if got, want := MessageCharCost(video)-MessageCharCost(base), VideoPartCharCost; got != want {
		t.Errorf("video part cost = %d, want %d", got, want)
	}

	image := Message{
		Role:    "user",
		Content: "",
		ContentParts: []ContentPart{
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,II"}},
		},
	}
	if got, want := MessageCharCost(image)-MessageCharCost(base), ImagePartCharCost; got != want {
		t.Errorf("image part cost = %d, want %d", got, want)
	}

	media := Message{Role: "user", Media: []string{"a", "b"}}
	if got, want := MessageCharCost(media)-MessageCharCost(base), 2*MediaPartCharCost; got != want {
		t.Errorf("media cost = %d, want %d", got, want)
	}

	// Text parts are counted only when Content == "" (Message.UnmarshalJSON
	// repopulates Content from the text parts; counting both would double it).
	textOnly := []ContentPart{{Type: "text", Text: "hello"}}
	empty := Message{Role: "user", Content: "", ContentParts: textOnly}
	if got, want := MessageCharCost(empty)-MessageCharCost(base), 5; got != want {
		t.Errorf("text-part cost with empty Content = %d, want 5", got)
	}
	populated := Message{Role: "user", Content: "hello", ContentParts: textOnly}
	if got, want := MessageCharCost(populated)-MessageCharCost(base), 5; got != want {
		t.Errorf("text-part cost with populated Content = %d, want 5 (no double count)", got)
	}
}

// TestReduceImageParts_NoHeavyPartsIsNoop: without heavy parts the call is a
// cheap no-op returning the input slice itself.
func TestReduceImageParts_NoHeavyPartsIsNoop(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "user", Content: "", ContentParts: []ContentPart{
			{Type: "text", Text: "a remote image is not heavy"},
			{Type: "image_url", ImageURL: &ImageURL{URL: "https://example.com/x.png"}},
		}},
	}
	got, evicted := ReduceImageParts(msgs, 1)
	if evicted != 0 {
		t.Fatalf("evicted = %d, want 0", evicted)
	}
	if len(got) != len(msgs) || &got[0] != &msgs[0] {
		t.Fatalf("no-op must return the input slice as-is")
	}
	for _, m := range got {
		for _, p := range m.ContentParts {
			if p.Text == omittedPartPlaceholder {
				t.Fatalf("no-op introduced a placeholder part: %+v", p)
			}
		}
	}
}

// TestReduceImageParts_ZeroKeepsNothing: keepLastImages <= 0 evicts every
// heavy part without panicking.
func TestReduceImageParts_ZeroKeepsNothing(t *testing.T) {
	for _, keep := range []int{0, -3} {
		msgs := []Message{
			{Role: "user", Content: "", ContentParts: []ContentPart{
				{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"}},
			}},
			{Role: "user", Content: "", ContentParts: []ContentPart{
				{Type: "text", Text: "before"},
				{Type: "video_url", VideoURL: &VideoURL{URL: "data:video/mp4;base64,VV"}},
			}},
		}
		got, evicted := ReduceImageParts(msgs, keep)
		if evicted != 2 {
			t.Fatalf("keepLastImages=%d: evicted = %d, want 2", keep, evicted)
		}
		for i, m := range got {
			for _, p := range m.ContentParts {
				if IsHeavyMultimodalPart(p) {
					t.Errorf("keepLastImages=%d: message %d still has a heavy part: %+v", keep, i, p)
				}
			}
			if len(m.ContentParts) == 0 && m.Content == "" {
				t.Errorf("keepLastImages=%d: message %d ended with zero parts and empty content", keep, i)
			}
		}
	}
}

// TestLiveMultimodalParts_IgnoresRemoteURLs: only data: URLs are heavy; remote
// http(s) URLs are tiny and must not be counted or evicted.
func TestLiveMultimodalParts_IgnoresRemoteURLs(t *testing.T) {
	remote := "https://example.com/remote.png"
	dataURL := "data:image/png;base64," + strings.Repeat("D", 128)
	msgs := []Message{
		{Role: "user", Content: "", ContentParts: []ContentPart{
			{Type: "image_url", ImageURL: &ImageURL{URL: remote}},
		}},
		{Role: "user", Content: "", ContentParts: []ContentPart{
			{Type: "image_url", ImageURL: &ImageURL{URL: dataURL}},
		}},
	}
	count, bytes := LiveMultimodalParts(msgs)
	if count != 1 {
		t.Errorf("count = %d, want 1 (remote URLs must not count)", count)
	}
	if bytes != len(dataURL) {
		t.Errorf("bytes = %d, want %d (only the data: URL)", bytes, len(dataURL))
	}
}
