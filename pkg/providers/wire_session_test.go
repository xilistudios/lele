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
	// FIX-C: empty/blank keys are NOT "no id" — they map to the shared
	// unkeyed bucket (now the named symbol UnkeyedSessionBucket, NIT-3: a
	// reader who sees e3b0c442… in a log can grep it), so unkeyed egresses
	// (cron, standalone tool loops) still satisfy required providers instead
	// of aborting. The literal below pins the symbol itself: a change of the
	// empty-key scheme fails here.
	const unkeyedBucket = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if UnkeyedSessionBucket != unkeyedBucket {
		t.Fatalf("UnkeyedSessionBucket = %q, want the sha256-of-empty literal %q", UnkeyedSessionBucket, unkeyedBucket)
	}
	if got := WireSessionID(""); got != UnkeyedSessionBucket {
		t.Fatalf("WireSessionID(%q) = %q, want the unkeyed bucket %q", "", got, UnkeyedSessionBucket)
	}
	if got := WireSessionID("   "); got != UnkeyedSessionBucket {
		t.Fatalf("WireSessionID(%q) = %q, want the unkeyed bucket %q", "   ", got, UnkeyedSessionBucket)
	}
	if !hex64.MatchString(UnkeyedSessionBucket) {
		t.Fatal("test setup: the unkeyed bucket must have the same 64-hex shape")
	}
	// Known vectors: sha256("abc") and sha256("") — the second one pins the
	// unkeyed bucket itself so a change of the empty-key scheme fails here.
	if got := WireSessionID("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("WireSessionID(%q) = %q, want known sha256 vector", "abc", got)
	}
	if got := WireSessionID(""); got != unkeyedBucket {
		t.Fatalf("WireSessionID(%q) = %q, want known sha256-of-empty vector", "", got)
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
			t.Fatalf("out[OptWireSessionID] = %v, want %q", out[protocoltypes.OptWireSessionID], WireSessionID("session-key"))
		}
		if got := out["max_tokens"]; got != 1234 {
			t.Fatalf("out[max_tokens] = %v, want 1234", got)
		}
	})

	t.Run("empty key still stamps the unkeyed bucket", func(t *testing.T) {
		// FIX-C: presence of the option = "this egress remembered to stamp";
		// there is no skip-on-empty path anymore.
		in := map[string]interface{}{"max_tokens": 1234}
		out := WithWireSessionID(in, "")
		got, ok := out[protocoltypes.OptWireSessionID]
		if !ok {
			t.Fatal("WithWireSessionID skipped the stamp on an empty key")
		}
		if want := UnkeyedSessionBucket; got != want {
			t.Fatalf("out[OptWireSessionID] = %v, want the unkeyed bucket %q", got, want)
		}
		if len(out) != len(in)+1 {
			t.Fatalf("len(out) = %d, want %d", len(out), len(in)+1)
		}
		if _, ok := in[protocoltypes.OptWireSessionID]; ok {
			t.Fatal("WithWireSessionID mutated the input map")
		}
	})

	t.Run("empty key on nil map returns a stamped copy", func(t *testing.T) {
		out := WithWireSessionID(nil, "")
		if out == nil {
			t.Fatal(`WithWireSessionID(nil, "") returned nil: the stamp must always be added`)
		}
		got, ok := out[protocoltypes.OptWireSessionID]
		if !ok {
			t.Fatal(`WithWireSessionID(nil, "") did not stamp the unkeyed bucket`)
		}
		if want := UnkeyedSessionBucket; got != want {
			t.Fatalf("out[OptWireSessionID] = %v, want the unkeyed bucket %q", got, want)
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
