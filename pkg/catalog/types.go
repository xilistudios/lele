package catalog

// Model is a catalog entry for a single model exposed by a provider.
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

// SupportsThinking reports whether the model advertises reasoning/thinking.
func (m Model) SupportsThinking() bool {
	return m.Reasoning || len(m.ThinkingLevels) > 0
}

// Provider is a known provider with its default endpoint and curated models.
type Provider struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Type    string  `json:"type,omitempty"` // transport hint: openai | anthropic
	APIBase string  `json:"api_base,omitempty"`
	Models  []Model `json:"models,omitempty"`
}

// Snapshot is the on-disk / embedded catalog document.
type Snapshot struct {
	Version   int                 `json:"version"`
	UpdatedAt string              `json:"updated_at,omitempty"`
	Source    string              `json:"source,omitempty"`
	Providers map[string]Provider `json:"providers"`
}
