// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package commands

import (
	"reflect"
	"testing"
)

func TestWithCustom_MergesAndSorts(t *testing.T) {
	base := []CommandInfo{
		{Name: "/compact", Description: "compact", Usage: "/compact"},
		{Name: "/clear", Description: "clear", Usage: "/clear"},
	}
	custom := []CustomCommandInfo{
		{Name: "/review", Description: "review code", Usage: "/review $ARGUMENTS", Source: "workspace"},
		{Name: "audit", Description: "audit", Usage: "/audit", Source: "config"},
	}

	got := WithCustom(base, custom)
	// Source is carried over from CustomCommandInfo (custom entries) and stays
	// empty for the built-in base entries, so UIs can badge them apart.
	want := []CommandInfo{
		{Name: "/audit", Description: "audit", Usage: "/audit", Source: "config"},
		{Name: "/clear", Description: "clear", Usage: "/clear"},
		{Name: "/compact", Description: "compact", Usage: "/compact"},
		{Name: "/review", Description: "review code", Usage: "/review $ARGUMENTS", Source: "workspace"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WithCustom = %+v, want %+v", got, want)
	}
}

// TestWithCustom_BuiltInWinsCollision pins the precedence rule: a custom command
// shadowed by a dispatched built-in must not be advertised, because the backend
// never reaches it. Comparison is normalized (slash + case).
func TestWithCustom_BuiltInWinsCollision(t *testing.T) {
	base := []CommandInfo{{Name: "/Clear", Description: "builtin", Usage: "/clear"}}
	custom := []CustomCommandInfo{
		{Name: "clear", Description: "custom", Usage: "/clear", Source: "directory"},
		{Name: "/CLEAR", Description: "custom2", Usage: "/clear", Source: "config"},
	}

	got := WithCustom(base, custom)
	if len(got) != 1 {
		t.Fatalf("expected built-in to win both collisions, got %+v", got)
	}
	if got[0].Description != "builtin" {
		t.Errorf("entry = %+v, want the built-in one", got[0])
	}
}

func TestWithCustom_DedupesCustomsAndSkipsGarbage(t *testing.T) {
	custom := []CustomCommandInfo{
		{Name: "/dup", Description: "first", Usage: "/dup", Source: "config"},
		{Name: "dup", Description: "second", Usage: "/dup", Source: "global"},
		{Name: "", Description: "no name", Usage: "x"},
		{Name: "/", Description: "slash only", Usage: "/"},
		{Name: "   ", Description: "blank", Usage: " "},
	}
	got := WithCustom(nil, custom)
	if len(got) != 1 || got[0].Name != "/dup" || got[0].Description != "first" {
		t.Fatalf("got %+v", got)
	}
	// First definition wins the dedupe, and with it its source.
	if got[0].Source != "config" {
		t.Errorf("/dup source = %q, want %q (first definition wins)", got[0].Source, "config")
	}
}

// TestWithCustom_LeavesInputsUntouched guards the "fresh slice" contract that
// WebUICommands() already provides for the static list.
func TestWithCustom_LeavesInputsUntouched(t *testing.T) {
	base := []CommandInfo{{Name: "/b", Description: "d", Usage: "u"}}
	original := append([]CommandInfo(nil), base...)

	_ = WithCustom(base, []CustomCommandInfo{{Name: "/a"}})
	if !reflect.DeepEqual(base, original) {
		t.Errorf("base slice was mutated: %+v", base)
	}
}

func TestWithCustom_EmptyInputs(t *testing.T) {
	if got := WithCustom(nil, nil); len(got) != 0 {
		t.Errorf("expected empty result, got %+v", got)
	}
}

// TestWebUICommands_StillStaticList guards the untouched source of truth.
func TestWebUICommands_StillStaticList(t *testing.T) {
	got := WebUICommands()
	if len(got) != 2 || got[0].Name != "/clear" || got[1].Name != "/compact" {
		t.Fatalf("unexpected built-in list: %+v", got)
	}
}
