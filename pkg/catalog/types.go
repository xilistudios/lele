package catalog

type Model struct {
	ID             string   `json:"id"`
	Name           string   `json:"name,omitempty"`
	ContextWindow  int      `json:"context_window,omitempty"`
	MaxOutput      int      `json:"max_output,omitempty"`
	Vision         bool     `json:"vision,omitempty"`
	ThinkingLevels []string `json:"thinking_levels,omitempty"`
	Reasoning      bool     `json:"reasoning,omitempty"`
	ToolCall       bool     `json:"tool_call,omitempty"`
}

func (m Model) SupportsThinking() bool {
	return m.Reasoning || len(m.ThinkingLevels) > 0
}

type Provider struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Type    string  `json:"type,omitempty"`
	APIBase string  `json:"api_base,omitempty"`
	Models  []Model `json:"models,omitempty"`
}
