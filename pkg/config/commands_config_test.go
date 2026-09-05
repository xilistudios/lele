// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package config

import (
	"os"
	"path/filepath"
	"testing"
)

const commandsJSON = `{
  "agents": {"defaults": {"workspace": "/tmp/ws", "provider": "openai", "model": "gpt-4o", "max_tokens": 1024}},
  "channels": {},
  "gateway": {"host": "127.0.0.1", "port": 18790},
  "tools": {},
  "heartbeat": {"enabled": false, "interval": 0},
  "devices": {},
  "logs": {},
  "commands": {
    "review": {"description": "d", "agent": "coder", "model": "m", "template": "t"}
  },
  "harness": {"allow_shell": true, "allow_absolute_files": true}
}`

func writeCommandsConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(commandsJSON), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func assertCommandFields(t *testing.T, cmds map[string]CommandDefinition, h HarnessConfig) {
	t.Helper()
	if h.AllowShell != true {
		t.Error("harness.allow_shell = false, want true")
	}
	if h.AllowAbsoluteFiles != true {
		t.Error("harness.allow_absolute_files = false, want true")
	}
	def, ok := cmds["review"]
	if !ok {
		t.Fatalf("commands map missing \"review\": %v", cmds)
	}
	if def.Description != "d" || def.Agent != "coder" || def.Model != "m" || def.Template != "t" {
		t.Errorf("review def = %+v", def)
	}
	if def.AllowShell {
		t.Error("allow_shell should default to false when omitted")
	}
}

func TestLoadConfigReadsCommandsAndHarness(t *testing.T) {
	cfg, err := LoadConfig(writeCommandsConfig(t))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	assertCommandFields(t, cfg.Commands, cfg.Harness)
}

func TestLoadEditableDocumentReadsCommandsAndHarness(t *testing.T) {
	doc, _, err := LoadEditableDocument(writeCommandsConfig(t))
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}
	assertCommandFields(t, doc.Commands, doc.Harness)
}

func TestEditableDocumentToConfigPreservesCommands(t *testing.T) {
	doc, _, err := LoadEditableDocument(writeCommandsConfig(t))
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}
	cfg, err := doc.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}
	assertCommandFields(t, cfg.Commands, cfg.Harness)
}

func TestEditableDocumentFromConfigRoundTrip(t *testing.T) {
	src, err := LoadConfig(writeCommandsConfig(t))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	doc := editableDocumentFromConfig(src)
	assertCommandFields(t, doc.Commands, doc.Harness)

	back, err := doc.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig: %v", err)
	}
	assertCommandFields(t, back.Commands, back.Harness)

	// The document must not alias the config map, and the config produced by
	// ToConfig must not alias the document map.
	doc2 := editableDocumentFromConfig(src)
	back2, err := doc2.ToConfig()
	if err != nil {
		t.Fatalf("ToConfig (2): %v", err)
	}
	delete(doc2.Commands, "review")
	if _, ok := src.Commands["review"]; !ok {
		t.Error("editableDocumentFromConfig shares the Commands map with Config")
	}
	delete(back2.Commands, "review")
	if _, ok := doc2.Commands["review"]; ok {
		t.Fatal("doc map was mutated by back2 (unexpected aliasing chain)")
	}
	if len(back2.Commands) != 0 {
		t.Error("ToConfig shares the Commands map with the document")
	}
	if !src.Harness.AllowShell || !back.Harness.AllowShell {
		t.Error("harness flag lost on the round trip")
	}
	if !src.Harness.AllowAbsoluteFiles || !back.Harness.AllowAbsoluteFiles {
		t.Error("harness.allow_absolute_files lost on the round trip")
	}
}

// TestHarnessFlagsRoundTripIndependently guards the toSerializable gate: the
// harness section must be emitted when EITHER flag is set, so a config with
// only allow_absolute_files:true survives save/reload (gating on AllowShell
// alone dropped it silently).
func TestHarnessFlagsRoundTripIndependently(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name    string
		shell   *bool
		absFile *bool
	}{
		{"both off", nil, nil},
		{"shell only", &yes, nil},
		{"abs only", nil, &yes},
		{"both on", &yes, &yes},
		{"shell on abs off", &yes, &no},
		{"shell off abs on", &no, &yes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shell, abs := false, false
			if tc.shell != nil {
				shell = *tc.shell
			}
			if tc.absFile != nil {
				abs = *tc.absFile
			}
			src := &Config{}
			src.Harness = HarnessConfig{AllowShell: shell, AllowAbsoluteFiles: abs}
			doc := editableDocumentFromConfig(src)
			back, err := doc.ToConfig()
			if err != nil {
				t.Fatalf("ToConfig: %v", err)
			}
			if back.Harness.AllowShell != shell {
				t.Errorf("ToConfig AllowShell = %v, want %v", back.Harness.AllowShell, shell)
			}
			if back.Harness.AllowAbsoluteFiles != abs {
				t.Errorf("ToConfig AllowAbsoluteFiles = %v, want %v", back.Harness.AllowAbsoluteFiles, abs)
			}

			// Full save/reload cycle.
			path := filepath.Join(t.TempDir(), "config.json")
			if err := SaveEditableDocument(path, doc); err != nil {
				t.Fatalf("SaveEditableDocument: %v", err)
			}
			reloaded, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if reloaded.Harness.AllowShell != shell || reloaded.Harness.AllowAbsoluteFiles != abs {
				t.Errorf("after save+reload: %+v, want shell=%v abs=%v", reloaded.Harness, shell, abs)
			}
		})
	}
}

func TestSaveEditableDocumentKeepsCommands(t *testing.T) {
	path := writeCommandsConfig(t)
	doc, _, err := LoadEditableDocument(path)
	if err != nil {
		t.Fatalf("LoadEditableDocument: %v", err)
	}
	doc.Commands["deploy"] = CommandDefinition{Description: "ship", Template: "deploy $ARGUMENTS"}
	if err := SaveEditableDocument(path, doc); err != nil {
		t.Fatalf("SaveEditableDocument: %v", err)
	}

	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	assertCommandFields(t, reloaded.Commands, reloaded.Harness)
	if _, ok := reloaded.Commands["deploy"]; !ok {
		t.Fatalf("new command lost on save: %v", reloaded.Commands)
	}
}

func TestCommandsAbsentByDefault(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.Commands) != 0 {
		t.Errorf("default Commands = %v, want empty", cfg.Commands)
	}
	if cfg.Harness.AllowShell {
		t.Error("shell expansion must be off by default")
	}
	doc := editableDocumentFromConfig(cfg)
	if len(doc.Commands) != 0 {
		t.Errorf("default doc Commands = %v, want empty", doc.Commands)
	}
}
