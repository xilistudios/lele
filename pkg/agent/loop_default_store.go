// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"fmt"
	"path/filepath"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/store"
)

// NewAgentLoop creates a new agent loop instance by opening the default
// SQLite store at <leleDir>/lele.db and delegating to NewAgentLoopWithStore.
//
// Production entry points MUST use NewAgentLoopWithStore directly with a
// store opened via cmd/lele's openSharedStore so that every database open
// goes through a single helper. This wrapper exists for tests and
// embedding convenience.
//
// Deprecated: production code should inject the store via NewAgentLoopWithStore.
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
