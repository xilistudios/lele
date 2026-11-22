package routing

import (
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func TestConfiguredDefaultAgentID(t *testing.T) {
	tests := []struct {
		name   string
		agents []config.AgentConfig
		want   string
	}{
		{
			name:   "no agents.list falls back to the implicit main",
			agents: nil,
			want:   "main",
		},
		{
			name:   "explicit default wins over list order",
			agents: []config.AgentConfig{{ID: "coder"}, {ID: "reviewer", Default: true}, {ID: "vision"}},
			want:   "reviewer",
		},
		{
			name:   "no explicit default falls back to the first entry",
			agents: []config.AgentConfig{{ID: "coder"}, {ID: "reviewer"}},
			want:   "coder",
		},
		{
			name:   "the returned ID is normalized like a registry key",
			agents: []config.AgentConfig{{ID: "Main"}, {ID: " Software-Engineer ", Default: true}},
			want:   "software-engineer",
		},
		{
			name:   "an empty ID cannot win as the default",
			agents: []config.AgentConfig{{ID: "ghost"}, {ID: "  ", Default: true}},
			want:   "ghost",
		},
		{
			name:   "duplicate IDs are one agent",
			agents: []config.AgentConfig{{ID: "dup"}, {ID: "DUP", Default: true}},
			want:   "dup",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Agents.List = tt.agents
			if got := ConfiguredDefaultAgentID(cfg); got != tt.want {
				t.Errorf("ConfiguredDefaultAgentID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConfiguredDefaultAgentID_NilConfig(t *testing.T) {
	if got := ConfiguredDefaultAgentID(nil); got != DefaultAgentID {
		t.Errorf("ConfiguredDefaultAgentID(nil) = %q, want %q", got, DefaultAgentID)
	}
}

func TestRouteResolver_DefaultMatchesConfiguredDefault(t *testing.T) {
	fixtures := [][]config.AgentConfig{
		nil,
		{{ID: "coder"}, {ID: "reviewer", Default: true}},
		{{ID: "coder"}, {ID: "reviewer"}},
	}
	for _, agents := range fixtures {
		cfg := &config.Config{}
		cfg.Agents.List = agents
		r := NewRouteResolver(cfg)
		if got, want := r.resolveDefaultAgentID(), ConfiguredDefaultAgentID(cfg); got != want {
			t.Errorf("resolveDefaultAgentID() = %q, ConfiguredDefaultAgentID() = %q", got, want)
		}
	}
}
