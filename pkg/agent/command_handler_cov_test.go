// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"strings"
	"testing"
)

func TestThinkLevelResponse(t *testing.T) {
	cases := map[string]string{
		"off":    "Think mode **OFF**",
		"low":    "Think mode **LOW**",
		"medium": "Think mode **MEDIUM**",
		"high":   "Think mode **HIGH**",
		"bogus":  "Unknown think level",
	}
	for lvl, want := range cases {
		got := thinkLevelResponse(lvl)
		if !strings.Contains(got, want) {
			t.Errorf("thinkLevelResponse(%q) = %q, want substring %q", lvl, got, want)
		}
	}
}

func TestCommandHandler_HandleThinkCommand(t *testing.T) {
	al := newCovLoop(t)
	ch := newCommandHandler(al)

	// empty session key
	got := ch.handleThinkCommand("", nil)
	if !strings.Contains(got, "session context") {
		t.Errorf("empty key: %q", got)
	}

	// cycle from whatever default to next
	got = ch.handleThinkCommand("telegram:think1", nil)
	if got == "" {
		t.Error("expected cycle result")
	}

	// explicit level
	got = ch.handleThinkCommand("telegram:think1", []string{"high"})
	if !strings.Contains(got, "THINK/HIGH") && !strings.Contains(got, "HIGH") {
		t.Errorf("explicit high: %q", got)
	}

	// invalid level
	got = ch.handleThinkCommand("telegram:think1", []string{"bogus"})
	if !strings.Contains(got, "Unknown think level") {
		t.Errorf("invalid level: %q", got)
	}
}

func TestCommandHandler_HandleGoalCommand(t *testing.T) {
	al := newCovLoop(t)
	ch := newCommandHandler(al)

	// set a goal
	got := ch.handleGoalCommand(context.Background(), "cli", "chat1", "telegram:goal1", []string{"Fix all lint errors"})
	if !strings.Contains(got, "Objetivo establecido") && !strings.Contains(got, "goal") {
		t.Errorf("set goal: %q", got)
	}

	// status shows it
	got = ch.handleGoalCommand(context.Background(), "cli", "chat1", "telegram:goal1", []string{"status"})
	if !strings.Contains(got, "Fix all lint errors") {
		t.Errorf("status: %q", got)
	}

	// pause
	got = ch.handleGoalCommand(context.Background(), "cli", "chat1", "telegram:goal1", []string{"pause"})
	if !strings.Contains(got, "pausado") {
		t.Errorf("pause: %q", got)
	}
	// pause again -> fail
	got = ch.handleGoalCommand(context.Background(), "cli", "chat1", "telegram:goal1", []string{"pause"})
	if !strings.Contains(got, "No hay objetivo activo") {
		t.Errorf("pause again: %q", got)
	}

	// resume
	got = ch.handleGoalCommand(context.Background(), "cli", "chat1", "telegram:goal1", []string{"resume"})
	if !strings.Contains(got, "reanudado") {
		t.Errorf("resume: %q", got)
	}

	// no-args usage
	got = ch.handleGoalCommand(context.Background(), "cli", "chat1", "telegram:nogoal", nil)
	if !strings.Contains(got, "Uso: /goal") {
		t.Errorf("no args: %q", got)
	}

	// status with no goal
	got = ch.handleGoalCommand(context.Background(), "cli", "chat1", "telegram:nogoal", []string{"status"})
	if !strings.Contains(got, "No hay objetivo") {
		t.Errorf("status no goal: %q", got)
	}
	// pause with no goal
	got = ch.handleGoalCommand(context.Background(), "cli", "chat1", "telegram:nogoal", []string{"pause"})
	if !strings.Contains(got, "No hay objetivo") {
		t.Errorf("pause no goal: %q", got)
	}
	// resume with no goal
	got = ch.handleGoalCommand(context.Background(), "cli", "chat1", "telegram:nogoal", []string{"resume"})
	if !strings.Contains(got, "No hay objetivo") {
		t.Errorf("resume no goal: %q", got)
	}
	// clear with no goal
	got = ch.handleGoalCommand(context.Background(), "cli", "chat1", "telegram:nogoal", []string{"clear"})
	if !strings.Contains(got, "No hay objetivo") {
		t.Errorf("clear no goal: %q", got)
	}

	// --turns flag: first token "--turns" alone stays in the default branch as goal text
	got = ch.handleGoalCommand(context.Background(), "cli", "chat1", "telegram:goal2", []string{"--turns"})
	if !strings.Contains(got, "Objetivo establecido") && !strings.Contains(got, "goal") {
		t.Errorf("--turns alone: %q", got)
	}
	// true --turns with value + text
	got = ch.handleGoalCommand(context.Background(), "cli", "chat1", "telegram:goal2", []string{"--turns", "15", "Achieve victory"})
	if !strings.Contains(got, "Achieve victory") {
		t.Errorf("--turns text: %q", got)
	}

	// clear existing goal
	got = ch.handleGoalCommand(context.Background(), "cli", "chat1", "telegram:goal2", []string{"clear"})
	if !strings.Contains(got, "eliminado") {
		t.Errorf("clear: %q", got)
	}
}

func TestCommandHandler_KickoffGoalTurn(t *testing.T) {
	al := newCovLoop(t)
	ch := newCommandHandler(al)
	ch.kickoffGoalTurn(context.Background(), "", "", "chatX", "telegram:k", "the goal text")
	ch.kickoffGoalTurn(context.Background(), "me", "telegram-chan", "chatY", "telegram:k2", "second goal")
}

// TestCommandHandler_HandleGoalCommand_NilManager verifies graceful handling
// when the goal manager is nil.
func TestCommandHandler_HandleGoalCommand_NilManager(t *testing.T) {
	al := newCovLoop(t)
	ch := newCommandHandler(al)
	al.goalManager = nil
	got := ch.handleGoalCommand(context.Background(), "cli", "chat1", "telegram:g", []string{"status"})
	if !strings.Contains(got, "not initialized") {
		t.Errorf("nil manager: %q", got)
	}
}
