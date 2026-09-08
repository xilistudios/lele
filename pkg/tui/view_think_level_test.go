// Lele - Ultra-lightweight personal AI agent
// Inspired and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package tui

import (
	"strings"
	"testing"
)

// TestView_BottomBarShowsEffectiveThinkLevel guards the review fix: the status
// bar must render the EFFECTIVE level (session override → agent thinking_level →
// "default"), the same value /status shows and buildLLMOptions applies. Before
// the fix it read the override-only getter, so an agent configured with
// thinking_level "medium" displayed "default" while actually reasoning.
func TestView_BottomBarShowsEffectiveThinkLevel(t *testing.T) {
	cfg := testModelConfig(t)
	medium := "medium"
	cfg.Agents.Defaults.ThinkingLevel = &medium

	m := newTestModelWithConfig(t, cfg, true)
	key := "tui:chat:think-level-view"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.sessionMgr.AddMessage(key, "user", "hi")
	m.currentKey = key
	m.showWelcome = false
	m.width = 160
	m.height = 40

	out := m.View()
	if !strings.Contains(out, "medium") {
		t.Fatalf("status bar does not show the agent's effective thinking level:\n%s", out)
	}
	if strings.Contains(out, "· default") {
		t.Errorf("status bar still renders the override-only 'default' while agent level is medium:\n%s", out)
	}

	// A session override must win over the agent level, exactly like /think.
	if !m.agentLoop.GetProvidable().SetThinkLevel(key, "high") {
		t.Fatal("SetThinkLevel(high) failed")
	}
	m.lastViewportKey = ""
	m.renderedBaseKey = ""
	out = m.View()
	if !strings.Contains(out, "high") || strings.Contains(out, "medium") {
		t.Errorf("status bar ignores the session override (want high, not medium):\n%s", out)
	}
}
