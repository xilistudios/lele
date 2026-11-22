package agent

import (
	"fmt"
	"path/filepath"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/store"
)

func NewAgentLoop(cfg *config.Config, msgBus *bus.MessageBus) *AgentLoop {
	dbPath := filepath.Join(config.GetLeleDir(), "lele.db")
	var s *store.Store
	if opened, err := store.Open(dbPath); err != nil {
		logger.WarnC("store", fmt.Sprintf("Failed to open SQLite store at %s: %v — falling back to JSON storage", dbPath, err))
	} else {
		s = opened
	}
	return NewAgentLoopWithStore(cfg, msgBus, s)
}
