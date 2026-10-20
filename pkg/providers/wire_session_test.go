// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package providers

import (
	"regexp"
	"testing"

	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

func TestWireSessionID(t *testing.T) {
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)

	id := WireSessionID("native:951a0f6e:chat:3")
	if !hex64.MatchString(id) {
		t.Fatalf("WireSessionID() = %q, want 64 lowercase hex chars", id)
	}
	if got := WireSessionID("native:951a0f6e:chat:3"); got != id {
		t.Fatalf("WireSessionID() not deterministic: %q != %q", got, id)
	}
	if got := WireSessionID("native:951a0f6e:chat:4"); got == id {
		t.Fatal("WireSessionID() same value for different session keys")
	}
	if got := WireSessionID(""); got != "" {
		t.Fatalf("WireSessionID(%q) = %q, want %q", "", got, "")
	}
	if got := WireSessionID("   "); got != "" {
		t.Fatalf("WireSessionID(%q) = %q, want %q", "   ", got, "")
	}
	// Known vector: sha256("abc").
	if got := WireSessionID("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("WireSessionID(%q) = %q, want known sha256 vector", "abc", got)
	}
}

func TestWithWireSessionID(t *testing.T) {
	t.Run("input map unmutated", func(t *testing.T) {
		in := map[string]interface{}{"max_tokens": 1234}
		out := WithWireSessionID(in, "session-key")
		if _, ok := in[protocoltypes.OptWireSessionID]; ok {
			t.Fatal("WithWireSessionID mutated the input map")
		}
		if got := out[protocoltypes.OptWireSessionID]; got != WireSessionID("session-key") {
			t.Fatalf("out[OptWireSessionID] = %v, want %q", got, WireSessionID("session-key"))
		}
		if got := out["max_tokens"]; got != 1234 {
			t.Fatalf("out[max_tokens] = %v, want 1234", got)
		}
	})

	t.Run("empty key returns identical map", func(t *testing.T) {
		in := map[string]interface{}{"max_tokens": 1234}
		out := WithWireSessionID(in, "")
		if _, ok := out[protocoltypes.OptWireSessionID]; ok {
			t.Fatal("empty session key must not add the option")
		}
		if len(out) != len(in) {
			t.Fatalf("len(out) = %d, want %d", len(out), len(in))
		}
	})

	t.Run("empty key on nil map stays nil", func(t *testing.T) {
		out := WithWireSessionID(nil, "")
		if out != nil {
			t.Fatalf("out = %v, want nil", out)
		}
	})

	t.Run("overwrite semantics", func(t *testing.T) {
		in := map[string]interface{}{protocoltypes.OptWireSessionID: "stale"}
		out := WithWireSessionID(in, "session-key")
		if got := out[protocoltypes.OptWireSessionID]; got != WireSessionID("session-key") {
			t.Fatalf("out[OptWireSessionID] = %v, want overwritten value", got)
		}
	})
}
