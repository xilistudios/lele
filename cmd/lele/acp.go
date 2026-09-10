package main

import (
	"context"
	"net/http"

	"github.com/xilistudios/lele/pkg/acp"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
)

// leleACPBridge adapts channels.AgentProvidable to acp.LeleAgentSource.
type leleACPBridge struct {
	src channels.AgentProvidable
}

func (b *leleACPBridge) ListAvailableAgentIDs() []string {
	return b.src.ListAvailableAgentIDs()
}

func (b *leleACPBridge) GetAgentInfo(agentID string) (acp.AgentInfo, bool) {
	info, ok := b.src.GetAgentInfo(agentID)
	if !ok {
		return acp.AgentInfo{}, false
	}
	return acp.AgentInfo{
		ID:             info.ID,
		Name:           info.Name,
		Description:    info.Description,
		Model:          info.Model,
		SupportsImages: info.SupportsImages,
	}, true
}

func (b *leleACPBridge) ProcessDirectWithChannel(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	return b.src.ProcessDirectWithChannel(ctx, content, sessionKey, channel, chatID)
}

func (b *leleACPBridge) SetSessionAgent(sessionKey, agentID string) {
	b.src.SetSessionAgent(sessionKey, agentID)
}

func (b *leleACPBridge) HasMessages(sessionKey string) bool {
	return b.src.HasMessages(sessionKey)
}

// registerACP mounts the Agent Communication Protocol surface on the unified
// server when cfg.ACP.Enabled is true.
func registerACP(mux *http.ServeMux, cfg *config.Config, providable channels.AgentProvidable) *acp.Server {
	if cfg == nil || !cfg.ACP.Enabled || providable == nil {
		return nil
	}
	src := &leleACPBridge{src: providable}
	bridge := acp.NewBridge(src)
	mgr := acp.NewRunManager(bridge)
	srv := acp.NewServer(acp.Config{
		Enabled:   true,
		Token:     cfg.ACP.Token,
		AgentName: "lele",
	}, mgr)
	srv.Register(mux)
	logger.InfoC("acp", "ACP protocol enabled (Agent Communication Protocol "+acp.ProtocolVersion+")")
	return srv
}
