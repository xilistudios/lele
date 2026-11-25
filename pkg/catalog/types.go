package catalog

const (
	// ProtocolChatCompletions is the OpenAI-compatible chat/completions wire protocol.
	ProtocolChatCompletions = "chat_completions"
	// ProtocolResponses is the OpenAI Responses API.
	ProtocolResponses = "responses"
	// ProtocolAnthropicMessages is the Anthropic Messages API.
	ProtocolAnthropicMessages = "anthropic_messages"
	// ProtocolGemini is the Google generateContent API.
	ProtocolGemini = "gemini"
)

type Model struct {
	ID             string   `json:"id"`
	Name           string   `json:"name,omitempty"`
	ContextWindow  int      `json:"context_window,omitempty"`
	MaxOutput      int      `json:"max_output,omitempty"`
	Vision         bool     `json:"vision,omitempty"`
	ThinkingLevels []string `json:"thinking_levels,omitempty"`
	Reasoning      bool     `json:"reasoning,omitempty"`
	ToolCall       bool     `json:"tool_call,omitempty"`
	// Protocol names the wire protocol the provider serves this model over.
	// Empty (and ProtocolChatCompletions) means chat/completions, so catalog
	// files written before the marker existed keep working unchanged.
	Protocol string `json:"protocol,omitempty"`
}

func (m Model) SupportsThinking() bool {
	return m.Reasoning || len(m.ThinkingLevels) > 0
}

func (m Model) SupportsChatCompletions() bool {
	return m.Protocol == "" || m.Protocol == ProtocolChatCompletions
}

type Provider struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Type    string  `json:"type,omitempty"`
	APIBase string  `json:"api_base,omitempty"`
	Models  []Model `json:"models,omitempty"`
}
