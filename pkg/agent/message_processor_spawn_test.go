// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"strings"
	"testing"
)

func TestParseSystemSpawnMessage(t *testing.T) {
	mp := &messageProcessorImpl{}

	tests := []struct {
		name     string
		content  string
		expected spawnConfig
	}{
		{
			name:    "basic task only",
			content: "SYSTEM_SPAWN:\nTASK: Create backup of database",
			expected: spawnConfig{
				Task:  "Create backup of database",
				Label: "Create backup of database",
			},
		},
		{
			name: "full configuration",
			content: `SYSTEM_SPAWN:
TASK: Create backup of the database PostgreSQL and subirlo a S3
LABEL: backup-diario
AGENT_ID: coder
GUIDANCE: Usa pg_dump y aws cli. El bucket es backups-db
CONTEXT: Backup diario de base de datos`,
			expected: spawnConfig{
				Task:     "Context: Backup diario de base de datos\n\nTask: Create backup of the database PostgreSQL and subirlo a S3\n\nAdditional guidance: Usa pg_dump y aws cli. El bucket es backups-db",
				Label:    "backup-diario",
				AgentID:  "coder",
				Guidance: "Usa pg_dump y aws cli. El bucket es backups-db",
				Context:  "Backup diario de base de datos",
			},
		},
		{
			name: "task with label",
			content: `SYSTEM_SPAWN:
TASK: Generate weekly report
LABEL: weekly-report`,
			expected: spawnConfig{
				Task:  "Generate weekly report",
				Label: "weekly-report",
			},
		},
		{
			name:     "empty content",
			content:  "SYSTEM_SPAWN:",
			expected: spawnConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mp.parseSystemSpawnMessage(tt.content)

			if result.Task != tt.expected.Task {
				t.Errorf("Task mismatch: got %q, want %q", result.Task, tt.expected.Task)
			}
			if result.Label != tt.expected.Label {
				t.Errorf("Label mismatch: got %q, want %q", result.Label, tt.expected.Label)
			}
			if result.AgentID != tt.expected.AgentID {
				t.Errorf("AgentID mismatch: got %q, want %q", result.AgentID, tt.expected.AgentID)
			}
			if result.Guidance != tt.expected.Guidance {
				t.Errorf("Guidance mismatch: got %q, want %q", result.Guidance, tt.expected.Guidance)
			}
			if result.Context != tt.expected.Context {
				t.Errorf("Context mismatch: got %q, want %q", result.Context, tt.expected.Context)
			}
		})
	}
}

func TestParseSystemSpawnMessage_Truncation(t *testing.T) {
	mp := &messageProcessorImpl{}

	// Test that long tasks are truncated for label
	longTask := strings.Repeat("a", 100)
	content := "SYSTEM_SPAWN:\nTASK: " + longTask

	result := mp.parseSystemSpawnMessage(content)

	if len(result.Label) != 30 {
		t.Errorf("Label should be truncated to 30 chars, got %d", len(result.Label))
	}
	if result.Task != longTask {
		t.Errorf("Task should not be truncated")
	}
}
