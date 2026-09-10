package acp

import (
	"context"
	"strings"
	"sync"
)

// LeleAgentSource is the subset of lele agent runtime needed by the bridge.
// It is satisfied by the agent loop's AgentProvidable (via a thin adapter).
type LeleAgentSource interface {
	ListAvailableAgentIDs() []string
	GetAgentInfo(agentID string) (AgentInfo, bool)
	ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error)
	SetSessionAgent(sessionKey, agentID string)
	HasMessages(sessionKey string) bool
}

// Bridge adapts LeleAgentSource to AgentBridge with ACP name sanitization.
type Bridge struct {
	src LeleAgentSource

	mu    sync.RWMutex
	byACP map[string]string // sanitized ACP name -> lele agent ID
	byID  map[string]string // lele agent ID -> sanitized ACP name
}

// NewBridge builds a name map from the current agent set.
func NewBridge(src LeleAgentSource) *Bridge {
	b := &Bridge{
		src:   src,
		byACP: make(map[string]string),
		byID:  make(map[string]string),
	}
	b.rebuild()
	return b
}

func (b *Bridge) rebuild() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.byACP = make(map[string]string)
	b.byID = make(map[string]string)
	for _, id := range b.src.ListAvailableAgentIDs() {
		name := SanitizeAgentName(id)
		if name == "" {
			// Fall back to a stable unique label.
			name = "agent"
			for i := 2; ; i++ {
				if _, taken := b.byACP[name]; !taken {
					break
				}
				name = "agent-" + itoa(i)
			}
		}
		// Prefer first mapping if two IDs sanitize the same.
		if _, taken := b.byACP[name]; !taken {
			b.byACP[name] = id
		}
		b.byID[id] = name
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// Refresh rebuilds the name map after a registry reload.
func (b *Bridge) Refresh() { b.rebuild() }

// ListAgents implements AgentBridge.
func (b *Bridge) ListAgents() []AgentInfo {
	b.rebuild()
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]AgentInfo, 0, len(b.byACP))
	for name, id := range b.byACP {
		info, ok := b.src.GetAgentInfo(id)
		if !ok {
			continue
		}
		out = append(out, AgentInfo{
			ID:             id,
			Name:           name,
			Description:    info.Description,
			Model:          info.Model,
			SupportsImages: info.SupportsImages,
		})
	}
	// Stable order by name.
	sortAgentInfos(out)
	return out
}

// GetAgent implements AgentBridge.
func (b *Bridge) GetAgent(name string) (AgentInfo, bool) {
	b.mu.RLock()
	id, ok := b.byACP[name]
	if !ok {
		// Try a lazy rebuild in case agents were added after construction.
		b.mu.RUnlock()
		b.rebuild()
		b.mu.RLock()
		id, ok = b.byACP[name]
	}
	b.mu.RUnlock()
	if !ok {
		return AgentInfo{}, false
	}
	info, found := b.src.GetAgentInfo(id)
	if !found {
		return AgentInfo{}, false
	}
	return AgentInfo{
		ID:             id,
		Name:           name,
		Description:    info.Description,
		Model:          info.Model,
		SupportsImages: info.SupportsImages,
	}, true
}

// Process implements AgentBridge. It binds the session to the agent and
// runs one direct turn through the lele agent loop.
func (b *Bridge) Process(ctx context.Context, agentName, sessionKey, content string) (string, error) {
	b.mu.RLock()
	id, ok := b.byACP[agentName]
	b.mu.RUnlock()
	if !ok {
		b.rebuild()
		b.mu.RLock()
		id, ok = b.byACP[agentName]
		b.mu.RUnlock()
	}
	if !ok {
		id = agentName
	}

	if id != "" {
		b.src.SetSessionAgent(sessionKey, id)
	}
	// Channel "acp" keeps ACP turns distinguishable from web/cli/gateway.
	return b.src.ProcessDirectWithChannel(ctx, content, sessionKey, "acp", sessionKey)
}

// SessionExists implements AgentBridge.
func (b *Bridge) SessionExists(sessionKey string) bool {
	return b.src.HasMessages(sessionKey)
}

func sortAgentInfos(list []AgentInfo) {
	// Insertion sort is fine for a handful of agents.
	for i := 1; i < len(list); i++ {
		j := i
		for j > 0 && strings.Compare(list[j-1].Name, list[j].Name) > 0 {
			list[j-1], list[j] = list[j], list[j-1]
			j--
		}
	}
}
