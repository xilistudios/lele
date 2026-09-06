// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// durable_inbound is tri-state (nil = "not configured"), so it has to survive
// the whole editable-document cycle without being flattened to false, and must
// stay out of the file while unset so the code default keeps owning it.

func TestDurableInboundDefaultIsOff(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Session.DurableInbound != nil {
		t.Errorf("default Session.DurableInbound = %v, want nil (opt-in)",
			*cfg.Session.DurableInbound)
	}
	if cfg.Session.DurableInboundEnabled() {
		t.Error("DurableInboundEnabled() = true by default, want false")
	}
}

func TestDurableInboundEnabledTriState(t *testing.T) {
	yes, no := true, false
	unset := SessionConfig{}
	on := SessionConfig{DurableInbound: &yes}
	off := SessionConfig{DurableInbound: &no}

	if unset.DurableInboundEnabled() {
		t.Error("nil DurableInbound enabled, want false")
	}
	if !on.DurableInboundEnabled() {
		t.Error("true DurableInbound disabled, want true")
	}
	if off.DurableInboundEnabled() {
		t.Error("false DurableInbound enabled, want false")
	}
}

func TestDurableInboundRoundTrip(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name string
		in   *bool
	}{
		{"unset", nil},
		{"on", &yes},
		{"off", &no},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := DefaultConfig()
			src.Session.DurableInbound = tc.in

			doc := editableDocumentFromConfig(src)
			back, err := doc.ToConfig()
			if err != nil {
				t.Fatalf("ToConfig: %v", err)
			}
			if ptrBoolEqual(back.Session.DurableInbound, tc.in) != true {
				t.Errorf("ToConfig lost the flag: got %s, want %s",
					formatTri(back.Session.DurableInbound), formatTri(tc.in))
			}

			// Full save/reload cycle, the path a user's edit actually takes.
			path := filepath.Join(t.TempDir(), "config.json")
			if err := SaveEditableDocument(path, doc); err != nil {
				t.Fatalf("SaveEditableDocument: %v", err)
			}
			reloaded, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if !ptrBoolEqual(reloaded.Session.DurableInbound, tc.in) {
				t.Errorf("after save+reload: got %s, want %s",
					formatTri(reloaded.Session.DurableInbound), formatTri(tc.in))
			}
		})
	}
}

// TestDurableInboundKeyAbsentWhenUnset pins the "unset means inherit" rule: an
// untouched config must not gain a durable_inbound: false that would pin the
// feature off for every future release.
func TestDurableInboundKeyAbsentWhenUnset(t *testing.T) {
	doc := editableDocumentFromConfig(DefaultConfig())

	data, err := json.Marshal(doc.toSerializable())
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if _, ok := sessionKey(t, data, "durable_inbound"); ok {
		t.Errorf("unset flag was serialised: %s", data)
	}
}

// TestDurableInboundEmittedAlone guards the emission gate: the session block is
// only written when something in it is set, so a config whose ONLY non-default
// session value is durable_inbound must still be persisted. Otherwise enabling
// durability silently evaporates on the next save.
func TestDurableInboundEmittedAlone(t *testing.T) {
	for name, want := range map[string]bool{"on": true, "off": false} {
		t.Run(name, func(t *testing.T) {
			value := want
			src := DefaultConfig()
			// Strip every other session override so durable_inbound is alone.
			src.Session = SessionConfig{DurableInbound: &value}

			doc := editableDocumentFromConfig(src)
			data, err := json.Marshal(doc.toSerializable())
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}
			got, ok := sessionKey(t, data, "durable_inbound")
			if !ok {
				t.Fatalf("session block lost a lone durable_inbound: %s", data)
			}
			if got != want {
				t.Errorf("durable_inbound = %v, want %v", got, want)
			}

			path := filepath.Join(t.TempDir(), "config.json")
			if err := SaveEditableDocument(path, doc); err != nil {
				t.Fatalf("SaveEditableDocument: %v", err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if !json.Valid(raw) {
				t.Fatalf("saved config is not valid JSON: %s", raw)
			}
		})
	}
}

// sessionKey digs session.<key> out of a serialised document.
func sessionKey(t *testing.T, data []byte, key string) (any, bool) {
	t.Helper()

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	session, ok := parsed["session"].(map[string]any)
	if !ok {
		return nil, false
	}
	value, ok := session[key]
	return value, ok
}

// ptrBoolEqual compares two *bool by value, treating nil as distinct from any
// non-nil pointer: the tri-state must not collapse.
func ptrBoolEqual(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func formatTri(v *bool) string {
	if v == nil {
		return "nil"
	}
	if *v {
		return "true"
	}
	return "false"
}
