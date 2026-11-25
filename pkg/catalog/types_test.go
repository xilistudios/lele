package catalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestModelSupportsChatCompletions(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		want     bool
	}{
		{name: "unset means chat completions", protocol: "", want: true},
		{name: "explicit chat completions", protocol: ProtocolChatCompletions, want: true},
		{name: "openai responses", protocol: ProtocolResponses, want: false},
		{name: "anthropic messages", protocol: ProtocolAnthropicMessages, want: false},
		{name: "gemini generateContent", protocol: ProtocolGemini, want: false},
		{name: "unknown transport", protocol: "some-future-transport", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{ID: "model", Protocol: tt.protocol}
			if got := m.SupportsChatCompletions(); got != tt.want {
				t.Fatalf("Model{Protocol:%q}.SupportsChatCompletions() = %v, want %v", tt.protocol, got, tt.want)
			}
		})
	}
}

func TestModelJSONRoundTripProtocol(t *testing.T) {
	t.Run("empty protocol is omitted", func(t *testing.T) {
		data, err := json.Marshal(Model{ID: "gpt-4o"})
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if strings.Contains(strings.ToLower(string(data)), "protocol") {
			t.Fatalf("Marshal() = %s, want no protocol key", data)
		}
	})

	t.Run("protocol uses the snake_case key", func(t *testing.T) {
		data, err := json.Marshal(Model{ID: "gpt-5.6-luna", Protocol: ProtocolResponses})
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if !strings.Contains(string(data), `"protocol":"responses"`) {
			t.Fatalf("Marshal() = %s, want a lowercase protocol key", data)
		}
	})

	t.Run("protocol survives unmarshal", func(t *testing.T) {
		var m Model
		if err := json.Unmarshal([]byte(`{"id":"gpt-5.6-luna","protocol":"responses"}`), &m); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if m.Protocol != ProtocolResponses {
			t.Fatalf("Protocol = %q, want %q", m.Protocol, ProtocolResponses)
		}
		if m.SupportsChatCompletions() {
			t.Fatal("SupportsChatCompletions() = true, want false")
		}
	})

	t.Run("missing protocol keeps backward compatibility", func(t *testing.T) {
		var m Model
		if err := json.Unmarshal([]byte(`{"id":"deepseek-v4-pro","context_window":1000000}`), &m); err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if m.Protocol != "" {
			t.Fatalf("Protocol = %q, want empty", m.Protocol)
		}
		if !m.SupportsChatCompletions() {
			t.Fatal("SupportsChatCompletions() = false, want true for pre-marker catalogs")
		}
	})
}

func TestChatCompletionsModels(t *testing.T) {
	models := []Model{
		{ID: "gpt-5.6-luna", Protocol: ProtocolResponses},
		{ID: "glm-5.1"},
		{ID: "minimax-m2.7", Protocol: ProtocolAnthropicMessages},
		{ID: "explicit", Protocol: ProtocolChatCompletions},
	}

	got := ChatCompletionsModels(models)
	if len(got) != 2 || got[0].ID != "glm-5.1" || got[1].ID != "explicit" {
		t.Fatalf("ChatCompletionsModels() = %v, want [glm-5.1 explicit]", modelIDs(got))
	}

	if got := ChatCompletionsModels(nil); got == nil || len(got) != 0 {
		t.Fatalf("ChatCompletionsModels(nil) = %v, want an empty non-nil slice", got)
	}
}
