package providers

import (
	"strings"
	"unicode/utf8"
)

const (
	ImagePartCharCost = 2500
	MediaPartCharCost = 2500
	VideoPartCharCost = 20000
)

const omittedPartPlaceholder = "[image omitted from context]"

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
	total += 10
	return total
}

func jsonFieldOverhead(name string) int {
	return len(name) + 4
}

func jsonStringUpperBound(s string) int {
	return len(s) + 2 + 5*countEscapeRisk(s)
}

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
			n++
		case r == '\u2028' || r == '\u2029':
			n++
		}
		i += size
	}
	return n
}

func EstimateRequestBytes(messages []Message) int {
	if len(messages) == 0 {
		return 4
	}
	total := 2 + (len(messages) - 1)
	for _, m := range messages {
		total += messageWireUpperBound(m)
	}
	return total
}

func messageWireUpperBound(m Message) int {
	n := 2
	n += jsonFieldOverhead("role") + jsonStringUpperBound(m.Role)
	n += jsonFieldOverhead("content") + jsonStringUpperBound(m.Content)
	if len(m.ContentParts) > 0 {
		n += 2
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

func contentPartWireUpperBound(part ContentPart) int {
	n := 2
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
			n += jsonFieldOverhead("fps") + 25
		}
	}
	return n
}

func toolCallWireUpperBound(tc ToolCall) int {
	n := 2
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

func attachmentWireUpperBound(a MessageAttachment) int {
	n := 2
	n += jsonFieldOverhead("name") + jsonStringUpperBound(a.Name)
	n += jsonFieldOverhead("path") + jsonStringUpperBound(a.Path)
	n += jsonFieldOverhead("mime_type") + jsonStringUpperBound(a.MIMEType)
	n += jsonFieldOverhead("kind") + jsonStringUpperBound(a.Kind)
	n += jsonFieldOverhead("caption") + jsonStringUpperBound(a.Caption)
	return n
}

func commandWireUpperBound(c CommandApplied) int {
	n := 2
	n += jsonFieldOverhead("command") + jsonStringUpperBound(c.Name)
	n += jsonFieldOverhead("description") + jsonStringUpperBound(c.Description)
	n += jsonFieldOverhead("args") + jsonStringUpperBound(c.Args)
	n += jsonFieldOverhead("agent") + jsonStringUpperBound(c.Agent)
	n += jsonFieldOverhead("model") + jsonStringUpperBound(c.Model)
	n += jsonFieldOverhead("source") + jsonStringUpperBound(c.Source)
	return n
}

func hasDataScheme(s string) bool {
	return len(s) >= len("data:") && strings.EqualFold(s[:len("data:")], "data:")
}

func IsHeavyMultimodalPart(part ContentPart) bool {
	if part.ImageURL != nil && hasDataScheme(part.ImageURL.URL) {
		return true
	}
	if part.VideoURL != nil && hasDataScheme(part.VideoURL.URL) {
		return true
	}
	return false
}

func IsHeavyMediaEntry(media string) bool {
	return hasDataScheme(media)
}

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

func ReduceImageParts(messages []Message, keepLastImages int) ([]Message, int) {
	if keepLastImages < 0 {
		keepLastImages = 0
	}
	evicted := 0
	var reduced []Message
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		newMedia, mediaEvicted := evictHeavyMediaEntries(m.Media, &keepLastImages)
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
		reduced[i].Media = newMedia
		evicted += mediaEvicted
		if m.Content == "" && len(reduced[i].ContentParts) == 0 && len(reduced[i].Media) == 0 {
			reduced[i].ContentParts = []ContentPart{{Type: "text", Text: omittedPartPlaceholder}}
		}
	}
	if evicted == 0 {
		return messages, 0
	}
	return reduced, evicted
}
