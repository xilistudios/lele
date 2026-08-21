package tui

import (
	"testing"
)

func TestChatModeString(t *testing.T) {
	if got := ModeAgent.String(); got != "agent" {
		t.Errorf("ModeAgent.String() = %q", got)
	}
	if got := ModeChat.String(); got != "chat" {
		t.Errorf("ModeChat.String() = %q", got)
	}
	if got := ModeGroup.String(); got != "group" {
		t.Errorf("ModeGroup.String() = %q", got)
	}
	// A value beyond the enumerated set falls back to "agent".
	if got := (chatMode(99)).String(); got != "agent" {
		t.Errorf("chatMode(99).String() = %q", got)
	}
}

func TestSysGroupFromSectionInvalid(t *testing.T) {
	tests := []string{"", "sys", "sys_", "sys_x", "sys_-1", "-1", "other_0"}
	for _, tc := range tests {
		if got := sysGroupFromSection(tc); got != -1 {
			t.Errorf("sysGroupFromSection(%q) = %d, want -1", tc, got)
		}
	}
}

func TestSysGroupConstants(t *testing.T) {
	// Verify the group index constants map to expected sub-view names.
	names := []string{
		sysSubViewName(sysGroupSession),
		sysSubViewName(sysGroupTools),
		sysSubViewName(sysGroupLogs),
		sysSubViewName(sysGroupLanguage),
		sysSubViewName(sysGroupGoal),
		sysSubViewName(sysGroupUpdates),
	}
	expected := []string{"sys_0", "sys_1", "sys_2", "sys_3", "sys_4", "sys_5"}
	for i := range names {
		if names[i] != expected[i] {
			t.Errorf("group %d name = %q, want %q", i, names[i], expected[i])
		}
	}
}