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

// resume_enabled is tri-state (nil = "not configured"), exactly like
// durable_inbound, so it must survive the whole editable-document cycle
// without being flattened to false, and stay out of the file while unset so
// the code default keeps owning it. The helpers it leans on (sessionKey,
// ptrBoolEqual, formatTri) live in durable_inbound_config_test.go.

func TestResumeDefaultIsOff(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Session.Resume != nil {
		t.Errorf("default Session.Resume = %v, want nil (opt-in)",
			*cfg.Session.Resume)
	}
	if cfg.Session.ResumeEnabled() {
		t.Error("ResumeEnabled() = true by default, want false")
	}
}

func TestResumeEnabledTriState(t *testing.T) {
	yes, no := true, false
	unset := SessionConfig{}
	on := SessionConfig{Resume: &yes}
	off := SessionConfig{Resume: &no}

	if unset.ResumeEnabled() {
		t.Error("nil Resume enabled, want false")
	}
	if !on.ResumeEnabled() {
		t.Error("true Resume disabled, want true")
	}
	if off.ResumeEnabled() {
		t.Error("false Resume enabled, want false")
	}
}

func TestResumeRoundTrip(t *testing.T) {
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
			src.Session.Resume = tc.in

			doc := editableDocumentFromConfig(src)
			back, err := doc.ToConfig()
			if err != nil {
				t.Fatalf("ToConfig: %v", err)
			}
			if !ptrBoolEqual(back.Session.Resume, tc.in) {
				t.Errorf("ToConfig lost the flag: got %s, want %s",
					formatTri(back.Session.Resume), formatTri(tc.in))
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
			if !ptrBoolEqual(reloaded.Session.Resume, tc.in) {
				t.Errorf("after save+reload: got %s, want %s",
					formatTri(reloaded.Session.Resume), formatTri(tc.in))
			}
		})
	}
}

// TestResumeKeyAbsentWhenUnset pins the "unset means inherit" rule: an
// untouched config must not gain a resume_enabled: false that would pin the
// feature off for every future release.
func TestResumeKeyAbsentWhenUnset(t *testing.T) {
	doc := editableDocumentFromConfig(DefaultConfig())

	data, err := json.Marshal(doc.toSerializable())
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if _, ok := sessionKey(t, data, "resume_enabled"); ok {
		t.Errorf("unset flag was serialised: %s", data)
	}
}

// TestResumeEmittedAlone guards the emission gate: a config whose ONLY
// non-default session value is resume_enabled must still be persisted,
// otherwise enabling resume silently evaporates on the next save.
func TestResumeEmittedAlone(t *testing.T) {
	for name, want := range map[string]bool{"on": true, "off": false} {
		t.Run(name, func(t *testing.T) {
			value := want
			src := DefaultConfig()
			// Strip every other session override so resume_enabled is alone.
			src.Session = SessionConfig{Resume: &value}

			doc := editableDocumentFromConfig(src)
			data, err := json.Marshal(doc.toSerializable())
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}
			got, ok := sessionKey(t, data, "resume_enabled")
			if !ok {
				t.Fatalf("session block lost a lone resume_enabled: %s", data)
			}
			if got != want {
				t.Errorf("resume_enabled = %v, want %v", got, want)
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

// TestResumeIndependentOfDurability is the tri-state cross-check the three
// flags need now that they coexist: resume_enabled is configured on its own
// and the document round-trip never lets it imply (or get implied by) the
// durable flags — even though the resume FEATURE requires durable_inbound at
// runtime, the config layer keeps them orthogonal.
func TestResumeIndependentOfDurability(t *testing.T) {
	yes, no := true, false
	cases := []struct {
		name     string
		durable  *bool
		resume   *bool
		wantDur  bool
		wantResu bool
	}{
		{"resume without durable", &no, &yes, false, true},
		{"durable without resume", &yes, &no, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := DefaultConfig()
			src.Session.DurableInbound = tc.durable
			src.Session.Resume = tc.resume

			back, err := editableDocumentFromConfig(src).ToConfig()
			if err != nil {
				t.Fatalf("ToConfig: %v", err)
			}
			if back.Session.DurableInboundEnabled() != tc.wantDur {
				t.Errorf("DurableInboundEnabled() = %v, want %v", back.Session.DurableInboundEnabled(), tc.wantDur)
			}
			if back.Session.ResumeEnabled() != tc.wantResu {
				t.Errorf("ResumeEnabled() = %v, want %v", back.Session.ResumeEnabled(), tc.wantResu)
			}
		})
	}
}
