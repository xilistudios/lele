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
		if m.Media != nil {
			out[i].Media = append([]string(nil), m.Media...)
		}
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

// TestIsHeavyMediaEntry pins the Media-entry counterpart of
// IsHeavyMultimodalPart: only "data:" URLs embed payload; file paths and
// remote URLs are tiny and are never heavy. URI schemes are case-insensitive
// (RFC 3986), so uppercase/mixed-case schemes must be heavy too (N4: the
// estimator charges every entry, so the evictor must see every entry). The
// 4-byte/5-byte boundary pair pins the length guard (N8: dropping it makes
// "data" panic instead of returning false).
func TestIsHeavyMediaEntry(t *testing.T) {
	cases := []struct {
		name  string
		media string
		want  bool
	}{
		{"image data URL", "data:image/png;base64,AAAA", true},
		{"video data URL", "data:video/mp4;base64,AAAA", true},
		{"image data URL, uppercase scheme", "DATA:image/png;base64,AAAA", true},
		{"image data URL, mixed-case scheme", "Data:image/png;base64,AAAA", true},
		{"video data URL, uppercase scheme", "DATA:VIDEO/mp4;base64,AAAA", true},
		{"uppercase scheme but not data", "FILE:///tmp/x", false},
		{"file path", "/tmp/photo.jpg", false},
		{"remote URL", "https://x/y.png", false},
		{"empty", "", false},
		{"bare scheme word, 4 bytes", "data", false},
		{"scheme only, no payload, 5 bytes", "data:", true},
	}
	for _, tc := range cases {
		if got := IsHeavyMediaEntry(tc.media); got != tc.want {
			t.Errorf("%s (%q): IsHeavyMediaEntry = %v, want %v", tc.name, tc.media, got, tc.want)
		}
	}
}

// TestReduceImageParts_EvictsHeavyMediaDataURLs: a "data:" Media entry is
// payload the estimator already charges, so the evictor must remove it under
// the same keepLastImages budget as heavy parts - while leaving the textual
// Content and the caller's Media backing array untouched (copy-on-write).
func TestReduceImageParts_EvictsHeavyMediaDataURLs(t *testing.T) {
	heavyMedia := "data:image/png;base64," + strings.Repeat("A", 200)
	msgs := []Message{{
		Role:    "user",
		Content: "hi",
		Media:   []string{heavyMedia, "/tmp/photo.jpg"},
	}}
	snapshot := deepCopyMessages(msgs)

	got, evicted := ReduceImageParts(msgs, 0)
	if evicted != 1 {
		t.Fatalf("evicted = %d, want 1", evicted)
	}
	if len(got[0].Media) != 1 || got[0].Media[0] != "/tmp/photo.jpg" {
		t.Fatalf("Media = %+v, want only the path entry", got[0].Media)
	}
	if got[0].Content != "hi" {
		t.Fatalf("Content = %q, want %q untouched", got[0].Content, "hi")
	}
	// Copy-on-write proof: the input message's Media slice must be unchanged.
	if len(msgs[0].Media) != 2 {
		t.Fatalf("len(input Media) = %d, want 2 (copy-on-write violated)", len(msgs[0].Media))
	}
	if msgs[0].Media[0] != heavyMedia || msgs[0].Media[1] != "/tmp/photo.jpg" {
		t.Fatalf("input Media = %+v, want the original entries", msgs[0].Media)
	}
	if !reflect.DeepEqual(msgs, snapshot) {
		t.Fatalf("input messages were mutated:\ngot:  %+v\nwant: %+v", msgs, snapshot)
	}
}

// TestReduceImageParts_MediaOnlyMessageStaysValid: evicting the only payload
// of a media-only message (no ContentParts, empty Content) must leave the
// message valid for the adapters by appending exactly one text placeholder
// part - never zero parts AND empty content.
func TestReduceImageParts_MediaOnlyMessageStaysValid(t *testing.T) {
	msgs := []Message{{
		Role:    "user",
		Content: "",
		Media:   []string{"data:image/png;base64," + strings.Repeat("B", 128)},
	}}
	got, evicted := ReduceImageParts(msgs, 0)
	if evicted != 1 {
		t.Fatalf("evicted = %d, want 1", evicted)
	}
	m := got[0]
	if len(m.Media) != 0 {
		t.Fatalf("Media = %+v, want empty after eviction", m.Media)
	}
	if len(m.ContentParts) != 1 || m.ContentParts[0].Type != "text" || m.ContentParts[0].Text != omittedPartPlaceholder {
		t.Fatalf("ContentParts = %+v, want exactly one %q text placeholder part", m.ContentParts, omittedPartPlaceholder)
	}
	if m.Content != "" {
		t.Fatalf("Content = %q, want %q untouched", m.Content, "")
	}
	// The validity invariant, asserted explicitly.
	if len(m.ContentParts) == 0 && m.Content == "" {
		t.Fatalf("message ended with zero parts and empty content (invalid for adapters)")
	}
}

// TestReduceImageParts_SurvivingPathMediaNeedsNoPlaceholder pins the NEGATIVE
// direction of the combined validity invariant (review mutation P2): after
// the heavy data: entry is evicted, the surviving path-only Media entry still
// counts as content for the invariant, so no placeholder part may be added -
// even though the serializers drop path entries on the wire (pre-existing
// shape, out of scope). A placeholder there would claim an image was omitted
// when the message still carries its attachment path.
func TestReduceImageParts_SurvivingPathMediaNeedsNoPlaceholder(t *testing.T) {
	msgs := []Message{{
		Role:    "user",
		Content: "",
		Media:   []string{"data:image/png;base64," + strings.Repeat("E", 128), "/tmp/keep.jpg"},
	}}
	got, evicted := ReduceImageParts(msgs, 0)
	if evicted != 1 {
		t.Fatalf("evicted = %d, want 1 (only the data: entry)", evicted)
	}
	m := got[0]
	if len(m.Media) != 1 || m.Media[0] != "/tmp/keep.jpg" {
		t.Fatalf("Media = %+v, want only the surviving path entry", m.Media)
	}
	if len(m.ContentParts) != 0 {
		t.Fatalf("ContentParts = %+v, want empty: decision - a surviving Media entry counts as "+
			"content for the validity invariant, so the placeholder must be skipped", m.ContentParts)
	}
	if m.Content != "" {
		t.Fatalf("Content = %q, want %q untouched", m.Content, "")
	}
}

// TestReduceImageParts_EvictsUppercaseSchemeMedia pins the N4 invariant in
// the direction that can bite: EstimateRequestBytes charges every Media entry
// regardless of the scheme's case (URI schemes are case-insensitive, RFC
// 3986), so a DATA: entry must be evictable too - otherwise the pre-request
// guard believes it is over budget forever and pays for a summarisation call
// that cannot remove a payload the transport never sees.
func TestReduceImageParts_EvictsUppercaseSchemeMedia(t *testing.T) {
	heavy := "DATA:image/png;base64," + strings.Repeat("U", 128)
	msgs := []Message{{
		Role:    "user",
		Content: "caption",
		Media:   []string{heavy, "/tmp/keep.jpg"},
	}}
	got, evicted := ReduceImageParts(msgs, 0)
	if evicted != 1 {
		t.Fatalf("evicted = %d, want 1 (charged => evictable, scheme case included)", evicted)
	}
	if len(got[0].Media) != 1 || got[0].Media[0] != "/tmp/keep.jpg" {
		t.Fatalf("Media = %+v, want only the path entry", got[0].Media)
	}
	if got[0].Content != "caption" {
		t.Fatalf("Content = %q, want %q untouched", got[0].Content, "caption")
	}
}

// TestLiveMultimodalParts_CountsMediaDataURLs pins the accounting across both
// carriers: a ContentParts-only control (existing behaviour), plus one heavy
// part + one heavy Media entry + one path + one remote URL in the same
// message: only the two heavy payloads count.
func TestLiveMultimodalParts_CountsMediaDataURLs(t *testing.T) {
	// Control: ContentParts-only accounting is unchanged.
	partURL := "data:image/png;base64," + strings.Repeat("C", 64)
	control := []Message{{Role: "user", ContentParts: []ContentPart{
		{Type: "image_url", ImageURL: &ImageURL{URL: partURL}},
	}}}
	count, bytes := LiveMultimodalParts(control)
	if count != 1 || bytes != len(partURL) {
		t.Fatalf("control: count, bytes = %d, %d; want 1, %d", count, bytes, len(partURL))
	}

	mediaURL := "data:video/mp4;base64," + strings.Repeat("D", 128)
	msgs := []Message{{
		Role: "user",
		ContentParts: []ContentPart{
			{Type: "image_url", ImageURL: &ImageURL{URL: partURL}},
		},
		Media: []string{mediaURL, "/tmp/photo.jpg", "https://example.com/x.png"},
	}}
	count, bytes = LiveMultimodalParts(msgs)
	if count != 2 {
		t.Errorf("count = %d, want 2 (1 heavy part + 1 heavy media)", count)
	}
	if want := len(partURL) + len(mediaURL); bytes != want {
		t.Errorf("bytes = %d, want %d", bytes, want)
	}
}

// TestReduceImageParts_MediaKeepLastOrdering pins the shared keep budget and
// its ordering: across messages the newest media entry survives; within one
// message a heavy media entry outranks a heavy part because it is added later
// on the wire (common.SerializeMessages emits parts -> text -> media).
func TestReduceImageParts_MediaKeepLastOrdering(t *testing.T) {
	mediaA := "data:image/png;base64," + strings.Repeat("A", 64)
	mediaB := "data:image/png;base64," + strings.Repeat("B", 64)

	// Across messages: keepLast=1 keeps only the LAST message's media entry.
	msgs := []Message{
		{Role: "user", Content: "a", Media: []string{mediaA}},
		{Role: "user", Content: "b", Media: []string{mediaB}},
	}
	got, evicted := ReduceImageParts(msgs, 1)
	if evicted != 1 {
		t.Fatalf("across messages: evicted = %d, want 1", evicted)
	}
	if len(got[0].Media) != 0 {
		t.Errorf("message 0 Media = %+v, want empty (older entry evicted)", got[0].Media)
	}
	if len(got[1].Media) != 1 || got[1].Media[0] != mediaB {
		t.Errorf("message 1 Media = %+v, want [%s] (newest entry kept)", got[1].Media, mediaB)
	}

	// Within one message: heavy part + heavy media, keepLast=1 -> the media
	// entry (later on the wire) survives and the part is evicted.
	partURL := "data:image/png;base64," + strings.Repeat("P", 96)
	mixed := []Message{{
		Role:    "user",
		Content: "",
		ContentParts: []ContentPart{
			{Type: "text", Text: "prompt"},
			{Type: "image_url", ImageURL: &ImageURL{URL: partURL}},
		},
		Media: []string{mediaA},
	}}
	got, evicted = ReduceImageParts(mixed, 1)
	if evicted != 1 {
		t.Fatalf("within message: evicted = %d, want 1", evicted)
	}
	if len(got[0].Media) != 1 || got[0].Media[0] != mediaA {
		t.Errorf("Media = %+v, want [%s] (later on the wire, kept)", got[0].Media, mediaA)
	}
	for _, p := range got[0].ContentParts {
		if IsHeavyMultimodalPart(p) {
			t.Errorf("heavy part should have been evicted in favor of the media entry: %+v", p)
		}
	}
	if got[0].ContentParts[0].Text != "prompt" {
		t.Errorf("text part changed: %+v", got[0].ContentParts[0])
	}
}

// TestReduceImageParts_PlainMediaIsNoop: a message carrying only path/remote
// Media entries is never heavy; keepLast=0 must evict nothing and return the
// very same input slice (cheap no-op contract).
func TestReduceImageParts_PlainMediaIsNoop(t *testing.T) {
	msgs := []Message{
		{Role: "user", Content: "hi", Media: []string{"/tmp/photo.jpg", "https://example.com/x.png"}},
	}
	got, evicted := ReduceImageParts(msgs, 0)
	if evicted != 0 {
		t.Fatalf("evicted = %d, want 0", evicted)
	}
	if len(got) != len(msgs) || &got[0] != &msgs[0] {
		t.Fatalf("no-op must return the input slice as-is (got &got[0]=%p, &msgs[0]=%p)", &got[0], &msgs[0])
	}
	if !reflect.DeepEqual(got, msgs) {
		t.Fatalf("no-op changed the messages:\ngot:  %+v\nwant: %+v", got, msgs)
	}
}

// TestIsHeavyMultimodalPart pins the ContentPart carrier of the scheme fold
// (N7): EstimateRequestBytes charges a DATA: payload its full byte length, so
// the evictor's predicate must see it too - URI schemes are case-insensitive
// (RFC 3986), exactly as for IsHeavyMediaEntry, and the two predicates share
// hasDataScheme so they can never diverge again. Every row runs as BOTH an
// image part and a video part: heaviness follows the URL field that is set,
// never the MIME or the part's Type string.
func TestIsHeavyMultimodalPart(t *testing.T) {
	cases := []struct {
		name      string
		url       string
		wantImage bool
		wantVideo bool
	}{
		{"image data URL", "data:image/png;base64,AA", true, true},
		{"image data URL, uppercase scheme", "DATA:image/png;base64,AA", true, true},
		{"image data URL, mixed-case scheme", "DaTa:image/png;base64,AA", true, true},
		// The video row, checked field by field: the VideoURL field carries
		// it (heavy), and the ImageURL field must be heavy too, because
		// contentPartWireUpperBound charges an ImageURL of ANY MIME at its
		// full length - a false here would recreate the charged-but-
		// un-evictable N7 hole for a part the wire does emit.
		{"video data URL, uppercase scheme", "DATA:video/mp4;base64,AA", true, true},
		// Controls: the fold must not over-match beyond ASCII case.
		{"file URL", "file:///tmp/a.png", false, false},
		{"file URL, uppercase scheme", "FILE:///tmp/a.png", false, false},
		{"remote URL", "https://example.com/a.png", false, false},
		{"absolute path", "/tmp/a.png", false, false},
		{"empty", "", false, false},
		{"bare scheme word, 4 bytes", "data", false, false},
		{"scheme only, no payload, 5 bytes", "data:", true, true},
	}
	for _, tc := range cases {
		img := ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: tc.url}}
		vid := ContentPart{Type: "video_url", VideoURL: &VideoURL{URL: tc.url}}
		if got := IsHeavyMultimodalPart(img); got != tc.wantImage {
			t.Errorf("image part %s (%q): IsHeavyMultimodalPart = %v, want %v", tc.name, tc.url, got, tc.wantImage)
		}
		if got := IsHeavyMultimodalPart(vid); got != tc.wantVideo {
			t.Errorf("video part %s (%q): IsHeavyMultimodalPart = %v, want %v", tc.name, tc.url, got, tc.wantVideo)
		}
	}
	// "Check the field, not the string": the predicates inspect the ImageURL/
	// VideoURL fields only. A part whose Type or Text merely MENTIONS a DATA:
	// URL while both URL fields are nil is not heavy.
	mention := ContentPart{Type: "image_url", Text: "DATA:video/mp4;base64,AA"}
	if IsHeavyMultimodalPart(mention) {
		t.Errorf("image part mentioning a DATA: URL only in Text = heavy, want false (field, not string)")
	}
	mentionVideo := ContentPart{Type: "video_url", Text: "DATA:video/mp4;base64,AA"}
	if IsHeavyMultimodalPart(mentionVideo) {
		t.Errorf("video part mentioning a DATA: URL only in Text = heavy, want false (field, not string)")
	}
}

// TestReduceImageParts_EvictsUppercaseSchemeContentPart is the end-to-end N7
// symptom: a DATA: URL inside a ContentPart must be counted by
// LiveMultimodalParts, evicted by ReduceImageParts and shrink
// EstimateRequestBytes - all three on the same tree. A case-sensitive
// predicate leaves all three at their pre-fix values (heavy=0, evicted=0,
// estimate unchanged - the review measured estimate=9437307 with count=0 and
// evicted=0, i.e. a guard that believes it is over budget forever and pays
// for a summarisation call that cannot remove the payload).
func TestReduceImageParts_EvictsUppercaseSchemeContentPart(t *testing.T) {
	payload := "DATA:image/png;base64," + strings.Repeat("U", 32*1024)
	msgs := []Message{{
		Role:    "user",
		Content: "",
		ContentParts: []ContentPart{
			{Type: "text", Text: "look at this"},
			{Type: "image_url", ImageURL: &ImageURL{URL: payload}},
		},
	}}
	snapshot := deepCopyMessages(msgs)

	// Symptom 1: the estimator's counter must see the payload.
	count, bytes := LiveMultimodalParts(msgs)
	if count != 1 || bytes != len(payload) {
		t.Fatalf("LiveMultimodalParts = (%d, %d), want (1, %d): a case-sensitive predicate leaves the "+
			"pre-fix values heavy=0, bytes=0 (review measurement: estimate=9437307, count=0, evicted=0)",
			count, bytes, len(payload))
	}

	// Symptom 2 + 3: the evictor removes it and the estimate drops accordingly.
	before := EstimateRequestBytes(msgs)
	got, evicted := ReduceImageParts(msgs, 0)
	if evicted != 1 {
		t.Fatalf("evicted = %d, want 1: a case-sensitive predicate leaves the pre-fix value evicted=0 "+
			"(review measurement: estimate=9437307, count=0, evicted=0)", evicted)
	}
	m := got[0]
	if len(m.ContentParts) != 2 ||
		m.ContentParts[0].Text != "look at this" ||
		m.ContentParts[1].Type != "text" || m.ContentParts[1].Text != omittedPartPlaceholder {
		t.Fatalf("ContentParts = %+v, want the original text part plus the DATA: part replaced by %q",
			m.ContentParts, omittedPartPlaceholder)
	}
	after := EstimateRequestBytes(got)
	if drop := before - after; drop < len(payload)-64 {
		t.Fatalf("estimate dropped only %d bytes (%d -> %d), want >= %d: the %d-byte payload is still "+
			"charged - a case-sensitive predicate leaves the estimate unchanged (heavy=0, evicted=0)", drop, before, after, len(payload)-64, len(payload))
	}
	// Copy-on-write: the input must be untouched.
	if !reflect.DeepEqual(msgs, snapshot) {
		t.Fatalf("input messages were mutated:\ngot:  %+v\nwant: %+v", msgs, snapshot)
	}
}

// schemePredicateSink gives the allocation measurement below a home for its
// results, so the compiler cannot optimize the predicate calls away.
var schemePredicateSink bool

// TestSchemeFoldIsAllocationFree pins the N9 doc claim on hasDataScheme: the
// scheme test runs on payload-sized strings (a 1 MiB entry here), so it must
// stay length-guarded and allocation-free - a whole-string ToLower fold would
// allocate a megabyte copy per predicate pass and turn this red.
func TestSchemeFoldIsAllocationFree(t *testing.T) {
	payload := "data:image/png;base64," + strings.Repeat("U", 1<<20) // 1 MiB+
	img := ContentPart{Type: "image_url", ImageURL: &ImageURL{URL: payload}}
	vid := ContentPart{Type: "video_url", VideoURL: &VideoURL{URL: payload}}
	allocs := testing.AllocsPerRun(200, func() {
		schemePredicateSink = hasDataScheme(payload)
		schemePredicateSink = IsHeavyMediaEntry(payload)
		schemePredicateSink = IsHeavyMultimodalPart(img)
		schemePredicateSink = IsHeavyMultimodalPart(vid)
	})
	if allocs != 0 {
		t.Errorf("allocs per run = %v, want 0 for hasDataScheme, IsHeavyMediaEntry and "+
			"IsHeavyMultimodalPart on a 1 MiB entry: a whole-string fold allocates a megabyte copy "+
			"per estimator/evictor pass", allocs)
	}
}
