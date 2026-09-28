package tui

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// toolCallMsg builds a single-tool-call assistant message, the shape the
// fingerprint is most often asked to hash in long chats.
func toolCallMsg(name, args string) providers.Message {
	return providers.Message{
		Role:    "assistant",
		Content: "running",
		ToolCalls: []providers.ToolCall{
			{Name: name, Function: &providers.FunctionCall{Name: name, Arguments: args}},
		},
	}
}

// TestMessageFingerprint_EqualMessages verifies the equality half of the
// contract: two messages with identical fields (including tool calls) hash to
// the same fingerprint.
func TestMessageFingerprint_EqualMessages(t *testing.T) {
	msg := toolCallMsg("exec", `{"command":"ls -la"}`)
	same := toolCallMsg("exec", `{"command":"ls -la"}`)

	if got, want := messageFingerprint(msg, 100), messageFingerprint(same, 100); got != want {
		t.Fatalf("equal messages produced different fingerprints: %q vs %q", got, want)
	}
}

// TestMessageFingerprint_FieldSensitivity verifies the inequality half of the
// contract for every field the hash mixes: changing any of them must change the
// fingerprint.
func TestMessageFingerprint_FieldSensitivity(t *testing.T) {
	base := toolCallMsg("exec", `{"command":"ls -la"}`)
	base.ReasoningContent = "thinking..."
	baseFp := messageFingerprint(base, 100)

	tests := []struct {
		name   string
		mutate func(m *providers.Message)
		width  int
	}{
		{"content", func(m *providers.Message) { m.Content = "running!" }, 100},
		{"reasoning content", func(m *providers.Message) { m.ReasoningContent = "thinking...." }, 100},
		{"tool call name", func(m *providers.Message) { m.ToolCalls[0].Name = "read" }, 100},
		{"function name", func(m *providers.Message) { m.ToolCalls[0].Function.Name = "read" }, 100},
		{"function arguments", func(m *providers.Message) { m.ToolCalls[0].Function.Arguments = `{"command":"pwd"}` }, 100},
		{"tool call count", func(m *providers.Message) { m.ToolCalls = append(m.ToolCalls, m.ToolCalls[0]) }, 100},
		{"nil function", func(m *providers.Message) { m.ToolCalls[0].Function = nil }, 100},
		{"empty function payload", func(m *providers.Message) {
			m.ToolCalls[0].Function = &providers.FunctionCall{}
		}, 100},
		{"width", func(m *providers.Message) {}, 120},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := base
			if len(base.ToolCalls) > 0 {
				msg.ToolCalls = append([]providers.ToolCall(nil), base.ToolCalls...)
				fn := *base.ToolCalls[0].Function
				msg.ToolCalls[0].Function = &fn
			}
			tt.mutate(&msg)
			if got := messageFingerprint(msg, tt.width); got == baseFp {
				t.Fatalf("changing %s did not change the fingerprint (%q)", tt.name, got)
			}
		})
	}
}

// TestFnv64aWriteStringWord_MatchesReference pins the word-at-a-time alignment
// against an independent reference built on encoding/binary: the first
// floor(len/8) little-endian groups mixed as h = (h ^ group) * prime, then the
// len%8 trailing bytes mixed one at a time. It catches an endianness flip in
// the manual packing and any tail-handling mistake, neither of which the
// partition tests can see (the length prefix masks them).
func TestFnv64aWriteStringWord_MatchesReference(t *testing.T) {
	reference := func(h uint64, s string) uint64 {
		for len(s) >= 8 {
			h = (h ^ binary.LittleEndian.Uint64([]byte(s[:8]))) * fnv64aPrime
			s = s[8:]
		}
		for i := 0; i < len(s); i++ {
			h = (h ^ uint64(s[i])) * fnv64aPrime
		}
		return h
	}

	// Deterministic byte soup plus a couple of realistic shapes, including
	// lengths that straddle every possible tail (0..7) and exact multiples of 8.
	inputs := []string{"", "x", "ab", "abcdefg", "abcdefgh", "abcdefghi", "abcdefghijklmno",
		strings.Repeat("0123456789abcdef", 4)}
	soup := make([]byte, 0, 64)
	for i := 0; i < 64; i++ {
		soup = append(soup, byte('a'+i%26))
	}
	for n := 0; n <= len(soup); n++ {
		inputs = append(inputs, string(soup[:n]))
	}

	for _, h := range []uint64{0, fnv64aOffset, 0xdeadbeefcafebabe} {
		for _, s := range inputs {
			if got, want := fnv64aWriteStringWord(h, s), reference(h, s); got != want {
				t.Fatalf("fnv64aWriteStringWord(%#x, %q) = %#x, want %#x", h, s, got, want)
			}
		}
	}
}

// TestMessageFingerprint_NoPartitionCollision is the mutation check for the
// length prefixes: without them, moving a character from one field to the next
// would leave the concatenated byte stream — and therefore the hash — unchanged.
func TestMessageFingerprint_NoPartitionCollision(t *testing.T) {
	tests := []struct {
		name string
		a, b providers.Message
	}{
		{
			name: "content vs reasoning content",
			a:    providers.Message{Role: "assistant", Content: "ab", ReasoningContent: "c"},
			b:    providers.Message{Role: "assistant", Content: "a", ReasoningContent: "bc"},
		},
		{
			name: "content vs reasoning split in two places",
			a:    providers.Message{Role: "user", Content: "abc", ReasoningContent: ""},
			b:    providers.Message{Role: "user", Content: "", ReasoningContent: "abc"},
		},
		{
			// The split lands exactly on the word-at-a-time mixer's group
			// boundary: one 16-byte field (two groups) versus two 8-byte fields
			// (one group each). Deterministic per-field alignment plus the
			// length prefixes keep those apart.
			name: "content split at the 8-byte group boundary",
			a:    providers.Message{Role: "assistant", Content: "aaaaaaaa" + "bbbbbbbb"},
			b:    providers.Message{Role: "assistant", Content: "aaaaaaaa", ReasoningContent: "bbbbbbbb"},
		},
		{
			// Same, one byte past a group boundary: the 2-byte tail of the
			// first field must not be foldable into the next field's bytes.
			name: "field tail moved to the next field",
			a:    providers.Message{Role: "assistant", Content: "aaaaaaaa" + "ij"},
			b:    providers.Message{Role: "assistant", Content: "aaaaaaaa", ReasoningContent: "ij"},
		},
		{
			name: "role vs content",
			a:    providers.Message{Role: "user", Content: "assistant"},
			b:    providers.Message{Role: "userassistant", Content: ""},
		},
		{
			name: "tool call name vs function name",
			a: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{
				{Name: "ab", Function: &providers.FunctionCall{Name: "c"}},
			}},
			b: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{
				{Name: "a", Function: &providers.FunctionCall{Name: "bc"}},
			}},
		},
		{
			name: "function name vs function arguments",
			a: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{
				{Name: "exec", Function: &providers.FunctionCall{Name: "ab", Arguments: "c"}},
			}},
			b: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{
				{Name: "exec", Function: &providers.FunctionCall{Name: "a", Arguments: "bc"}},
			}},
		},
		{
			name: "one tool call vs two",
			a:    providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{Name: "ab"}}},
			b:    providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{Name: "a"}, {Name: "b"}}},
		},
		{
			// A tool call without a function payload mixes no payload fields,
			// while one with an (empty) payload mixes two zero-length fields.
			// Without the per-record tag those two shapes are just empty
			// length-prefixed fields, so moving the tag between tool calls of
			// the same list would produce the same byte stream.
			name: "missing function payload vs empty payload across tool calls",
			a: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{
				{Name: "a"},
				{Name: "", Function: &providers.FunctionCall{}},
			}},
			b: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{
				{Name: "a", Function: &providers.FunctionCall{}},
				{Name: ""},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fpA := messageFingerprint(tt.a, 100)
			fpB := messageFingerprint(tt.b, 100)
			if fpA == fpB {
				t.Fatalf("fields split differently collided: %q == %q", fpA, fpB)
			}
		})
	}
}

// TestMessageFingerprint_Format pins the cache-key format (16 lower-case hex
// digits), which the render cache relies on only as an opaque string.
func TestMessageFingerprint_Format(t *testing.T) {
	fp := messageFingerprint(providers.Message{Role: "user", Content: "hi"}, 80)
	if len(fp) != 16 {
		t.Fatalf("fingerprint %q has length %d, want 16", fp, len(fp))
	}
	if strings.Trim(fp, "0123456789abcdef") != "" {
		t.Fatalf("fingerprint %q is not lower-case hex", fp)
	}
}

// benchFingerprint runs the fingerprint over msg and keeps the result alive so
// the compiler cannot drop the call. Sub-byte throughput is reported via
// SetBytes so both sub-benchmarks are comparable.
func benchFingerprint(b *testing.B, msg providers.Message, width int) {
	b.Helper()
	size := len(msg.Content) + len(msg.ReasoningContent)
	for _, tc := range msg.ToolCalls {
		if tc.Function != nil {
			size += len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}

	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()

	var fp string
	for i := 0; i < b.N; i++ {
		fp = messageFingerprint(msg, width)
	}
	if len(fp) != 16 {
		b.Fatalf("unexpected fingerprint %q", fp)
	}
}

// BenchmarkMessageFingerprint measures the per-message cost of the render cache
// key on a representative long-chat message (multi-KB content, a reasoning
// trailer and a tool call carrying multi-KB arguments) and on the pathological
// case that motivated the change: a tool result whose arguments are hundreds of
// kilobytes. In both cases the hashing must not copy the fields.
func BenchmarkMessageFingerprint(b *testing.B) {
	representative := providers.Message{
		Role:             "assistant",
		Content:          strings.Repeat("lorem ipsum dolor sit amet ", 200),
		ReasoningContent: strings.Repeat("reasoning step ", 100),
		ToolCalls: []providers.ToolCall{
			{
				Name: "exec",
				Function: &providers.FunctionCall{
					Name:      "exec",
					Arguments: `{"command":"` + strings.Repeat("x", 2000) + `"}`,
				},
			},
		},
	}
	hugeToolResult := providers.Message{
		Role:    "tool",
		Content: strings.Repeat("0123456789abcdef ", 32*1024), // ~544 KB
		ToolCalls: []providers.ToolCall{
			{
				Name: "read_file",
				Function: &providers.FunctionCall{
					Name:      "read_file",
					Arguments: `{"path":"/tmp/big.log","content":"` + strings.Repeat("y", 256*1024) + `"}`,
				},
			},
		},
	}

	b.Run("representative", func(b *testing.B) { benchFingerprint(b, representative, 120) })
	b.Run("huge_tool_result", func(b *testing.B) { benchFingerprint(b, hugeToolResult, 120) })
}
