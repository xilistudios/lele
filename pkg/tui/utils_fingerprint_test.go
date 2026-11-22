package tui

import (
	"encoding/binary"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func toolCallMsg(name, args string) providers.Message {
	return providers.Message{
		Role:    "assistant",
		Content: "running",
		ToolCalls: []providers.ToolCall{
			{Name: name, Function: &providers.FunctionCall{Name: name, Arguments: args}},
		},
	}
}

func TestMessageFingerprint_EqualMessages(t *testing.T) {
	msg := toolCallMsg("exec", `{"command":"ls -la"}`)
	same := toolCallMsg("exec", `{"command":"ls -la"}`)

	if got, want := messageFingerprint(msg, 100), messageFingerprint(same, 100); got != want {
		t.Fatalf("equal messages produced different fingerprints: %q vs %q", got, want)
	}
}

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
			name: "content split at the 8-byte group boundary",
			a:    providers.Message{Role: "assistant", Content: "aaaaaaaa" + "bbbbbbbb"},
			b:    providers.Message{Role: "assistant", Content: "aaaaaaaa", ReasoningContent: "bbbbbbbb"},
		},
		{
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

func TestMessageFingerprint_Format(t *testing.T) {
	fp := messageFingerprint(providers.Message{Role: "user", Content: "hi"}, 80)
	if len(fp) != 16 {
		t.Fatalf("fingerprint %q has length %d, want 16", fp, len(fp))
	}
	if strings.Trim(fp, "0123456789abcdef") != "" {
		t.Fatalf("fingerprint %q is not lower-case hex", fp)
	}
}

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
		Content: strings.Repeat("0123456789abcdef ", 32*1024),
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
