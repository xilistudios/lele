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

// durable_outbound is tri-state (nil = "not configured"), so it has to survive
// the whole editable-document cycle without being flattened to false, and must
// stay out of the file while unset so the code default keeps owning it. Mirror
// of durable_inbound_config_test.go: same guarantees, reply side.

func TestDurableOutboundDefaultIsOff(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Session.DurableOutbound != nil {
		t.Errorf("default Session.DurableOutbound = %v, want nil (opt-in)",
			*cfg.Session.DurableOutbound)
	}
	if cfg.Session.DurableOutboundEnabled() {
		t.Error("DurableOutboundEnabled() = true by default, want false")
	}
}

func TestDurableOutboundEnabledTriState(t *testing.T) {
	yes, no := true, false
	unset := SessionConfig{}
	on := SessionConfig{DurableOutbound: &yes}
	off := SessionConfig{DurableOutbound: &no}

	if unset.DurableOutboundEnabled() {
		t.Error("nil DurableOutbound enabled, want false")
	}
	if !on.DurableOutboundEnabled() {
		t.Error("true DurableOutbound disabled, want true")
	}
	if off.DurableOutboundEnabled() {
		t.Error("false DurableOutbound enabled, want false")
	}
}

func TestDurableOutboundRoundTrip(t *testing.T) {
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
			src.Session.DurableOutbound = tc.in

			doc := editableDocumentFromConfig(src)
			back, err := doc.ToConfig()
			if err != nil {
				t.Fatalf("ToConfig: %v", err)
			}
			if !ptrBoolEqual(back.Session.DurableOutbound, tc.in) {
				t.Errorf("ToConfig lost the flag: got %s, want %s",
					formatTri(back.Session.DurableOutbound), formatTri(tc.in))
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
			if !ptrBoolEqual(reloaded.Session.DurableOutbound, tc.in) {
				t.Errorf("after save+reload: got %s, want %s",
					formatTri(reloaded.Session.DurableOutbound), formatTri(tc.in))
			}
		})
	}
}

// TestDurableOutboundKeyAbsentWhenUnset pins the "unset means inherit" rule: an
// untouched config must not gain a durable_outbound: false that would pin the
// feature off for every future release.
func TestDurableOutboundKeyAbsentWhenUnset(t *testing.T) {
	doc := editableDocumentFromConfig(DefaultConfig())

	data, err := json.Marshal(doc.toSerializable())
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if _, ok := sessionKey(t, data, "durable_outbound"); ok {
		t.Errorf("unset flag was serialised: %s", data)
	}
}

// TestDurableOutboundEmittedAlone guards the emission gate: the session block is
// only written when something in it is set, so a config whose ONLY non-default
// session value is durable_outbound must still be persisted. Otherwise enabling
// durability silently evaporates on the next save.
func TestDurableOutboundEmittedAlone(t *testing.T) {
	for name, want := range map[string]bool{"on": true, "off": false} {
		t.Run(name, func(t *testing.T) {
			value := want
			src := DefaultConfig()
			// Strip every other session override so durable_outbound is alone.
			src.Session = SessionConfig{DurableOutbound: &value}

			doc := editableDocumentFromConfig(src)
			data, err := json.Marshal(doc.toSerializable())
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}
			got, ok := sessionKey(t, data, "durable_outbound")
			if !ok {
				t.Fatalf("session block lost a lone durable_outbound: %s", data)
			}
			if got != want {
				t.Errorf("durable_outbound = %v, want %v", got, want)
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

// TestDurableOutboundIndependentOfInbound is the tri-state cross-check the two
// flags need now that they coexist: each one is configured on its own and the
// document round-trip never lets one imply the other.
func TestDurableOutboundIndependentOfInbound(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name            string
		in, out         *bool
		wantIn, wantOut bool
	}{
		{"in only", &yes, &no, true, false},
		{"out only", &no, &yes, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := DefaultConfig()
			src.Session.DurableInbound = tc.in
			src.Session.DurableOutbound = tc.out

			back, err := editableDocumentFromConfig(src).ToConfig()
			if err != nil {
				t.Fatalf("ToConfig: %v", err)
			}
			if back.Session.DurableInboundEnabled() != tc.wantIn {
				t.Errorf("DurableInboundEnabled() = %v, want %v", back.Session.DurableInboundEnabled(), tc.wantIn)
			}
			if back.Session.DurableOutboundEnabled() != tc.wantOut {
				t.Errorf("DurableOutboundEnabled() = %v, want %v", back.Session.DurableOutboundEnabled(), tc.wantOut)
			}
		})
	}
}
