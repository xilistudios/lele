// Package providers: shared accounting and deterministic shrinking for
// multimodal (image/video) message payloads.
//
// The transport a request travels through caps the body size (the configured
// router rejects bodies > 10 MiB with HTTP 400). A turn carrying many or large
// base64 image parts can exceed that cap, the turn aborts, and the session
// stays poisoned. This file provides the two primitives the fix builds on:
//
//   - a single char-accounting rule per message (MessageCharCost) shared by
//     every estimator, so token estimates cannot drift between call sites;
//   - byte accounting (EstimateRequestBytes) plus deterministic eviction
//     (ReduceImageParts) to shrink multimodal payloads without an LLM.
//
// Counting image bytes as chars/tokens would be wrong (1.37 MiB of base64 is
// ~550k "tokens"), so images carry a fixed char cost here and their real byte
// weight is measured separately by LiveMultimodalParts/EstimateRequestBytes.

package providers

import (
	"strings"
	"unicode/utf8"
)

const (
	// ImagePartCharCost is the fixed char cost charged for one image_url content part.
	ImagePartCharCost = 2500
	// MediaPartCharCost is the fixed char cost charged for one Media (file path) entry.
	MediaPartCharCost = 2500
	// VideoPartCharCost is the fixed char cost charged for one video_url content part.
	// It is the canonical value: pkg/agent/session_manager.go defines its local
	// videoPartEstimateChars as a reference to this constant, so the two
	// estimators cannot drift.
	VideoPartCharCost = 20000
)

// omittedPartPlaceholder replaces an evicted multimodal part when the message
// has no textual Content to fall back on (a message may never end up with zero
// parts *and* empty content for the OpenAI/Anthropic/Bedrock adapters).
const omittedPartPlaceholder = "[image omitted from context]"

// MessageCharCost is the ONE accounting rule per message, in chars (~2.5
// chars/token elsewhere in the codebase). Callers that skip
// ExcludeFromContext messages must filter before summing; that filter is the
// only intended difference between estimators built on this rule.
//
// Text parts are counted only when Content == "" because Message.UnmarshalJSON
// repopulates Content from the text parts (counting both would double it), but
// image and video parts are ALWAYS counted: textFromParts renders them as
// "[image]"/"[video]" markers, which never carry the payload weight.
func MessageCharCost(m Message) int {
	total := utf8.RuneCountInString(m.Content)
	total += utf8.RuneCountInString(m.ReasoningContent)
	for _, tc := range m.ToolCalls {
		if tc.Function != nil {
			total += utf8.RuneCountInString(tc.Function.Name)
			total += utf8.RuneCountInString(tc.Function.Arguments)
		}
	}
	total += len(m.Media) * MediaPartCharCost
	for _, part := range m.ContentParts {
		if part.Text != "" && m.Content == "" {
			total += utf8.RuneCountInString(part.Text)
		}
		if part.ImageURL != nil {
			total += ImagePartCharCost
		}
		if part.VideoURL != nil {
			total += VideoPartCharCost
		}
	}
	total += 10 // per-message structural overhead
	return total
}

// jsonFieldOverhead is the byte cost of one JSON object field: the quoted
// key, the colon and the separating comma (the comma is absent on the last
// field, which makes the estimate conservatively high by at most one byte per
// field).
func jsonFieldOverhead(name string) int {
	return len(name) + 4
}

// jsonStringUpperBound is a safe upper bound for len(json.Marshal(s)) of a
// single string: the two quotes plus a per-byte surcharge for every byte that
// encoding/json escapes (< 0x20 as \u00XX or a two-letter code, and
// " \ < > & - the last three because Marshal escapes HTML - as \u00XX or \" ).
// U+2028/U+2029 and invalid UTF-8 bytes are also escaped by the encoder and
// are therefore counted as risky. Single pass, no allocation.
func jsonStringUpperBound(s string) int {
	return len(s) + 2 + 5*countEscapeRisk(s)
}

// countEscapeRisk returns how many runes/bytes of s encoding/json would
// replace with a longer escape sequence.
func countEscapeRisk(s string) int {
	n := 0
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			if c < 0x20 || c == '"' || c == '\\' || c == '<' || c == '>' || c == '&' {
				n++
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			// Invalid UTF-8 byte: encoded as \ufffd (6 bytes vs 1 counted here).
			n++
		case r == '\u2028' || r == '\u2029':
			// The only two runes the encoder always escapes unconditionally.
			n++
		}
		i += size
	}
	return n
}

// EstimateRequestBytes is a conservative upper bound of
// len(json.Marshal(messages)) computed without allocating a payload-sized
// buffer. It mirrors the wire shape of Message.MarshalJSON (content is the
// parts array when parts exist; ToolCall uses the canonical OpenAI shape) and
// is guaranteed to satisfy EstimateRequestBytes(msgs) >= len(json.Marshal(msgs))
// for any input, staying within 25 % on realistic content (pinned by
// TestEstimateRequestBytes_Calibration).
func EstimateRequestBytes(messages []Message) int {
	if len(messages) == 0 {
		// json.Marshal of a nil or empty slice yields "null"/"[]".
		return 4
	}
	// Array brackets plus the comma between consecutive messages.
	total := 2 + (len(messages) - 1)
	for _, m := range messages {
		total += messageWireUpperBound(m)
	}
	return total
}

// messageWireUpperBound is the byte estimate for one marshaled Message. It
// counts every field Message.MarshalJSON can emit, including the UI-only
// Attachments/DisplayContent/Command fields (they ARE marshaled by the generic
// marshaler even though providers serialize from explicit fields) and, as a
// safe over-estimate, Media entries, which the marshaler currently drops.
func messageWireUpperBound(m Message) int {
	// Braces plus the always-emitted role, content and reasoning_content.
	n := 2
	n += jsonFieldOverhead("role") + jsonStringUpperBound(m.Role)
	n += jsonFieldOverhead("content") + jsonStringUpperBound(m.Content)
	if len(m.ContentParts) > 0 {
		// The marshaler replaces Content with the parts array; counting the
		// Content string above as well over-estimates by its (small) length.
		n += 2 // array brackets
		for i, part := range m.ContentParts {
			if i > 0 {
				n++
			}
			n += contentPartWireUpperBound(part)
		}
	}
	n += jsonFieldOverhead("reasoning_content") + jsonStringUpperBound(m.ReasoningContent)
	if len(m.ToolCalls) > 0 {
		n += jsonFieldOverhead("tool_calls") + 2
		for i, tc := range m.ToolCalls {
			if i > 0 {
				n++
			}
			n += toolCallWireUpperBound(tc)
		}
	}
	if m.ToolCallID != "" {
		n += jsonFieldOverhead("tool_call_id") + jsonStringUpperBound(m.ToolCallID)
	}
	if len(m.Media) > 0 {
		n += jsonFieldOverhead("media") + 2
		for i, path := range m.Media {
			if i > 0 {
				n++
			}
			n += jsonStringUpperBound(path)
		}
	}
	if m.ExcludeFromContext {
		n += jsonFieldOverhead("exclude_from_context") + len("true")
	}
	if m.Streaming {
		n += jsonFieldOverhead("streaming") + len("true")
	}
	if len(m.Attachments) > 0 {
		n += jsonFieldOverhead("attachments") + 2
		for i, a := range m.Attachments {
			if i > 0 {
				n++
			}
			n += attachmentWireUpperBound(a)
		}
	}
	if m.DisplayContent != "" {
		n += jsonFieldOverhead("display_content") + jsonStringUpperBound(m.DisplayContent)
	}
	if m.Command != nil {
		n += jsonFieldOverhead("harness_command") + commandWireUpperBound(*m.Command)
	}
	return n
}

// contentPartWireUpperBound is the byte estimate for one marshaled
// ContentPart. The "type" field is always emitted; text, image_url and
// video_url are omitempty and counted only when present (counting an absent
// field's key would still be safe, but the conditional keeps the estimate
// tight).
func contentPartWireUpperBound(part ContentPart) int {
	n := 2 // braces
	n += jsonFieldOverhead("type") + jsonStringUpperBound(part.Type)
	if part.Text != "" {
		n += jsonFieldOverhead("text") + jsonStringUpperBound(part.Text)
	}
	if part.ImageURL != nil {
		n += jsonFieldOverhead("image_url") + 2
		n += jsonFieldOverhead("url") + jsonStringUpperBound(part.ImageURL.URL)
		if part.ImageURL.Detail != "" {
			n += jsonFieldOverhead("detail") + jsonStringUpperBound(part.ImageURL.Detail)
		}
	}
	if part.VideoURL != nil {
		n += jsonFieldOverhead("video_url") + 2
		n += jsonFieldOverhead("url") + jsonStringUpperBound(part.VideoURL.URL)
		if part.VideoURL.FPS != 0 {
			// A float64 rendered by encoding/json is at most 25 bytes.
			n += jsonFieldOverhead("fps") + 25
		}
	}
	return n
}

// toolCallWireUpperBound is the byte estimate for one marshaled ToolCall.
// ToolCall.MarshalJSON always emits id, a type defaulting to "function", and
// the function object with both name and arguments (ArgumentJSON also covers
// the legacy in-memory shape that only carries the decoded arguments map).
func toolCallWireUpperBound(tc ToolCall) int {
	n := 2 // braces
	n += jsonFieldOverhead("id") + jsonStringUpperBound(tc.ID)
	callType := tc.Type
	if callType == "" {
		callType = "function"
	}
	n += jsonFieldOverhead("type") + jsonStringUpperBound(callType)
	n += jsonFieldOverhead("function") + 2
	n += jsonFieldOverhead("name") + jsonStringUpperBound(tc.FunctionName())
	n += jsonFieldOverhead("arguments") + jsonStringUpperBound(tc.ArgumentJSON())
	if tc.Function != nil && tc.Function.ThoughtSignature != "" {
		n += jsonFieldOverhead("thought_signature") + jsonStringUpperBound(tc.Function.ThoughtSignature)
	}
	if tc.ThoughtSignature != "" {
		n += jsonFieldOverhead("thought_signature") + jsonStringUpperBound(tc.ThoughtSignature)
	}
	if tc.ExtraContent != nil && tc.ExtraContent.Google != nil && tc.ExtraContent.Google.ThoughtSignature != "" {
		n += jsonFieldOverhead("extra_content") + 2
		n += jsonFieldOverhead("google") + 2
		n += jsonFieldOverhead("thought_signature") +
			jsonStringUpperBound(tc.ExtraContent.Google.ThoughtSignature)
	}
	return n
}

// attachmentWireUpperBound is the byte estimate for one marshaled
// MessageAttachment. All keys are counted even when the omitempty value is
// absent; the over-estimate is a few bytes.
func attachmentWireUpperBound(a MessageAttachment) int {
	n := 2 // braces
	n += jsonFieldOverhead("name") + jsonStringUpperBound(a.Name)
	n += jsonFieldOverhead("path") + jsonStringUpperBound(a.Path)
	n += jsonFieldOverhead("mime_type") + jsonStringUpperBound(a.MIMEType)
	n += jsonFieldOverhead("kind") + jsonStringUpperBound(a.Kind)
	n += jsonFieldOverhead("caption") + jsonStringUpperBound(a.Caption)
	return n
}

// commandWireUpperBound is the byte estimate for one marshaled
// CommandApplied; like attachments, all keys are counted unconditionally.
func commandWireUpperBound(c CommandApplied) int {
	n := 2 // braces
	n += jsonFieldOverhead("command") + jsonStringUpperBound(c.Name)
	n += jsonFieldOverhead("description") + jsonStringUpperBound(c.Description)
	n += jsonFieldOverhead("args") + jsonStringUpperBound(c.Args)
	n += jsonFieldOverhead("agent") + jsonStringUpperBound(c.Agent)
	n += jsonFieldOverhead("model") + jsonStringUpperBound(c.Model)
	n += jsonFieldOverhead("source") + jsonStringUpperBound(c.Source)
	return n
}

// IsHeavyMultimodalPart reports whether the part embeds a base64 payload
// ("data:" URL). Remote http(s) URLs are tiny and are never heavy.
func IsHeavyMultimodalPart(part ContentPart) bool {
	if part.ImageURL != nil && strings.HasPrefix(part.ImageURL.URL, "data:") {
		return true
	}
	if part.VideoURL != nil && strings.HasPrefix(part.VideoURL.URL, "data:") {
		return true
	}
	return false
}

// IsHeavyMediaEntry reports whether a Message.Media entry embeds a base64
// payload ("data:" URL). Channels normally store file paths here, which are
// tiny; a data: entry is what the OpenAI-compatible serializer (and the legacy
// Bedrock fallback) turns into a real image/video part, so it is payload
// weight the estimator already charges and the evictor must be able to
// remove.
func IsHeavyMediaEntry(media string) bool {
	return strings.HasPrefix(media, "data:")
}

// LiveMultimodalParts counts the heavy multimodal payloads across both
// carriers they travel in - ContentParts image/video parts (see
// IsHeavyMultimodalPart) and Message.Media "data:" entries (see
// IsHeavyMediaEntry) - and sums the byte length of their URLs, i.e. the
// actual base64 payload weight that must stay below the transport body cap.
func LiveMultimodalParts(messages []Message) (count int, bytes int) {
	for _, m := range messages {
		for _, part := range m.ContentParts {
			if !IsHeavyMultimodalPart(part) {
				continue
			}
			count++
			switch {
			case part.ImageURL != nil:
				bytes += len(part.ImageURL.URL)
			case part.VideoURL != nil:
				bytes += len(part.VideoURL.URL)
			}
		}
		for _, entry := range m.Media {
			if !IsHeavyMediaEntry(entry) {
				continue
			}
			count++
			bytes += len(entry)
		}
	}
	return count, bytes
}

// evictHeavyMediaEntries shrinks one message's Media list under a shared keep
// budget: it walks the entries from the end (most recently added first), lets
// up to *budget heavy "data:" entries stay alive (decrementing
// *budget for each) and marks the rest for eviction. Plain file paths and
// remote URLs are tiny and never consume the budget. It returns a fresh slice
// holding the kept entries in their original order - so the caller's backing
// array is never written to - and the number of evicted entries. kept == media
// (the very same slice) when nothing was evicted.
func evictHeavyMediaEntries(media []string, budget *int) (kept []string, evicted int) {
	var evict []bool
	for j := len(media) - 1; j >= 0; j-- {
		if !IsHeavyMediaEntry(media[j]) {
			continue
		}
		if *budget > 0 {
			*budget--
			continue
		}
		if evict == nil {
			evict = make([]bool, len(media))
		}
		evict[j] = true
	}
	if evict == nil {
		return media, 0
	}
	kept = make([]string, 0, len(media))
	for j, entry := range media {
		if evict[j] {
			evicted++
			continue
		}
		kept = append(kept, entry)
	}
	return kept, evicted
}

// ReduceImageParts deterministically shrinks multimodal payloads without an
// LLM: it keeps the keepLastImages most recent heavy payloads alive - across
// BOTH carriers, ContentParts image/video parts and Message.Media "data:"
// entries - and replaces every other heavy part with a text placeholder or
// deletes it (media entries are always deleted; they are payload-only carriers
// with no textual slot). "Most recent" is defined by walking messages from the
// end and, within a message, from the end of Media first and then of
// ContentParts: on the wire the order is parts -> text -> media
// (common.SerializeMessages), so a Media entry is the payload most recently
// added to the message and consumes the shared keep budget before the parts do.
//
// Eviction per message keeps the result valid for the OpenAI/Anthropic/Bedrock
// adapters (a message may never end up with zero parts *and* empty content):
// when Content != "" the part is deleted (the rehydrated message already
// carries its textual marker), otherwise it is replaced in place with a text
// placeholder part. The invariant spans both carriers: if after the rewrite a
// message with empty Content has neither ContentParts nor Media left, one text
// placeholder part is appended.
//
// It returns a copy - the input slice, its Message values and their
// ContentParts *and* Media backing arrays are never mutated - and the number
// of payloads evicted. When nothing is evicted it returns the input slice
// unchanged (0), so callers can detect a no-op cheaply. keepLastImages <= 0
// evicts all heavy payloads.
func ReduceImageParts(messages []Message, keepLastImages int) ([]Message, int) {
	if keepLastImages < 0 {
		keepLastImages = 0
	}
	evicted := 0
	var reduced []Message // lazily allocated: only when the first edit lands
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		// Media pass first: see the budget-order note in the doc comment above.
		newMedia, mediaEvicted := evictHeavyMediaEntries(m.Media, &keepLastImages)
		// Decide, from the end of the message, which heavy parts stay alive.
		var evict []bool
		changed := false
		for j := len(m.ContentParts) - 1; j >= 0; j-- {
			if !IsHeavyMultimodalPart(m.ContentParts[j]) {
				continue
			}
			if keepLastImages > 0 {
				keepLastImages--
				continue
			}
			if evict == nil {
				evict = make([]bool, len(m.ContentParts))
			}
			evict[j] = true
			changed = true
		}
		if !changed && mediaEvicted == 0 {
			continue
		}
		// Copy-on-write of the parts list: evicted parts are either deleted or
		// replaced by the placeholder.
		newParts := m.ContentParts
		if changed {
			keepPlaceholder := m.Content == ""
			newParts = make([]ContentPart, 0, len(m.ContentParts))
			for j, part := range m.ContentParts {
				if !evict[j] {
					newParts = append(newParts, part)
					continue
				}
				evicted++
				if keepPlaceholder {
					newParts = append(newParts, ContentPart{Type: "text", Text: omittedPartPlaceholder})
				}
			}
		}
		if reduced == nil {
			reduced = make([]Message, len(messages))
			copy(reduced, messages)
		}
		reduced[i] = m
		reduced[i].ContentParts = newParts
		// newMedia already is a freshly allocated slice when (and only when)
		// an entry was evicted (see evictHeavyMediaEntries), so assigning it
		// here never touches the caller's Media backing array.
		reduced[i].Media = newMedia
		evicted += mediaEvicted
		// Combined validity invariant across both carriers: a message with
		// empty Content may never end up with neither parts nor media left.
		if m.Content == "" && len(reduced[i].ContentParts) == 0 && len(reduced[i].Media) == 0 {
			reduced[i].ContentParts = []ContentPart{{Type: "text", Text: omittedPartPlaceholder}}
		}
	}
	if evicted == 0 {
		return messages, 0
	}
	return reduced, evicted
}
