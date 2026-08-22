package channels

import (
	"net/http"
	"testing"
)

func TestStreamStatus_RequiresSessionKey(t *testing.T) {
	ts := newNativeTestServer(t)
	resp := doSecretsRequest(t, ts, http.MethodGet, "/api/v1/chat/streams/", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (route needs session key)", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestStreamStatus_AnyOwnedSession(t *testing.T) {
	ts := newNativeTestServer(t)
	// The test client owns any plain session key; in-progress is nil → 200 with empty list.
	resp := doSecretsRequest(t, ts, http.MethodGet, "/api/v1/chat/streams/whatever-session", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestStreamStatus_SessionOwnedEmpty(t *testing.T) {
	ts := newNativeTestServer(t)
	// Own a session via the client. The test client already owns arbitrary sessions
	// (see validateSessionOwnership tests). Use a plausible session key.
	resp := doSecretsRequest(t, ts, http.MethodGet, "/api/v1/chat/streams/my-session", nil)
	// The mock returns nil in-progress → empty stream list.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestStreamState_RequiresParams(t *testing.T) {
	ts := newNativeTestServer(t)
	// Missing messageID in path → 404 route miss (or 400 if handled).
	resp := doSecretsRequest(t, ts, http.MethodGet, "/api/v1/chat/streams/", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()

	// Non-owned session → 403 (validateSessionOwnership only denies subagents/different
	// clients; the test client owns plain sessions) → so expect 404 (no in-progress).
	resp = doSecretsRequest(t, ts, http.MethodGet, "/api/v1/chat/streams/other/msg1", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (owned session with nil in-progress)", resp.StatusCode)
	}
	resp.Body.Close()

	// Owned session, no in-progress → 404.
	resp = doSecretsRequest(t, ts, http.MethodGet, "/api/v1/chat/streams/my-session/msg1", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}
