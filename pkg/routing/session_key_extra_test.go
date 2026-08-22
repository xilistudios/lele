package routing

import (
	"testing"
)

// TestBuildAgentPeerSessionKey_EmptyDMScope ensures empty DMScope defaults to main.
func TestBuildAgentPeerSessionKey_EmptyDMScope(t *testing.T) {
	got := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID: "main",
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "direct", ID: "user1"},
		// DMScope empty
	})
	if got != "agent:main:main" {
		t.Errorf("Empty DMScope = %q, want 'agent:main:main'", got)
	}
}

// TestBuildAgentPeerSessionKey_EmptyPeerKind ensures an empty peer kind is
// treated as "direct" (the peerKind == "" branch).
func TestBuildAgentPeerSessionKey_EmptyPeerKind(t *testing.T) {
	got := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID: "sales",
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "", ID: "user1"},
		DMScope: DMScopePerPeer,
	})
	if got != "agent:sales:direct:user1" {
		t.Errorf("Empty peer kind = %q, want 'agent:sales:direct:user1'", got)
	}
}

// TestBuildAgentPeerSessionKey_DMPerAccountNoPeerID covers the per-account-channel-peer
// branch where peerID is empty (falls through to main).
func TestBuildAgentPeerSessionKey_DMPerAccountNoPeerID(t *testing.T) {
	got := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID:       "main",
		Channel:       "telegram",
		AccountID:     "bot1",
		Peer:          &RoutePeer{Kind: "direct", ID: ""},
		DMScope:       DMScopePerAccountChannelPeer,
		IdentityLinks: nil,
	})
	if got != "agent:main:main" {
		t.Errorf("per-account-channel-peer empty peerID = %q, want 'agent:main:main'", got)
	}
}

// TestBuildAgentPeerSessionKey_DMPerChannelNoPeerID covers the per-channel-peer
// branch where peerID is empty (falls through to main).
func TestBuildAgentPeerSessionKey_DMPerChannelNoPeerID(t *testing.T) {
	got := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID:   "main",
		Channel:   "telegram",
		Peer:      &RoutePeer{Kind: "direct", ID: "   "},
		DMScope:   DMScopePerChannelPeer,
		AccountID: "bot1",
	})
	if got != "agent:main:main" {
		t.Errorf("per-channel-peer empty peerID = %q, want 'agent:main:main'", got)
	}
}

// TestBuildAgentPeerSessionKey_DMPerPeerNoPeerID covers the per-peer branch where
// peerID is empty (falls through to main).
func TestBuildAgentPeerSessionKey_DMPerPeerNoPeerID(t *testing.T) {
	got := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID: "main",
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "direct", ID: ""},
		DMScope: DMScopePerPeer,
	})
	if got != "agent:main:main" {
		t.Errorf("per-peer empty peerID = %q, want 'agent:main:main'", got)
	}
}

// TestBuildAgentPeerSessionKey_GroupEmptyID ensures group/channel peers with an
// empty ID fall back to "unknown".
func TestBuildAgentPeerSessionKey_GroupEmptyID(t *testing.T) {
	got := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID: "main",
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "group", ID: ""},
		DMScope: DMScopePerPeer,
	})
	if got != "agent:main:telegram:group:unknown" {
		t.Errorf("group empty ID = %q, want 'agent:main:telegram:group:unknown'", got)
	}
}

// TestBuildAgentPeerSessionKey_GroupEmptyChannel ensures an empty channel becomes
// "unknown" in the group key.
func TestBuildAgentPeerSessionKey_GroupEmptyChannel(t *testing.T) {
	got := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID: "main",
		Channel: "",
		Peer:    &RoutePeer{Kind: "channel", ID: "c1"},
	})
	if got != "agent:main:unknown:channel:c1" {
		t.Errorf("group empty channel = %q, want 'agent:main:unknown:channel:c1'", got)
	}
}

// TestBuildAgentPeerSessionKey_NormalizesPeerID verifies peer ID is lowercased.
func TestBuildAgentPeerSessionKey_NormalizesPeerID(t *testing.T) {
	got := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID: "main",
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "direct", ID: "USER123"},
		DMScope: DMScopePerPeer,
	})
	if got != "agent:main:direct:user123" {
		t.Errorf("normalized peerID = %q, want 'agent:main:direct:user123'", got)
	}
}

// TestBuildAgentPeerSessionKey_AccountChannelIdentityLink verifies the
// per-account-channel-peer scope also resolves identity links.
func TestBuildAgentPeerSessionKey_AccountChannelIdentityLink(t *testing.T) {
	links := map[string][]string{
		"john": {"telegram:user123"},
	}
	got := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID:       "main",
		Channel:       "telegram",
		AccountID:     "bot1",
		Peer:          &RoutePeer{Kind: "direct", ID: "user123"},
		DMScope:       DMScopePerAccountChannelPeer,
		IdentityLinks: links,
	})
	if got != "agent:main:telegram:bot1:direct:john" {
		t.Errorf("per-account-channel-peer identity link = %q, want 'agent:main:telegram:bot1:direct:john'", got)
	}
}

// TestIsSubagentSessionKey_ChannelFormat covers the last-part "subagent-" branch.
func TestIsSubagentSessionKey_ChannelFormat(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"agent:main:telegram:subagent-2", true}, // last part starts with subagent-
		{"agent:main:subagent-42", true},
		{"agent:main:subagent:", true},
		{"agent:main:telegram:regular", false}, // last part not a subagent
		{"native:uuid:subagent-1", false},      // not agent-prefixed → cannot be subagent
	}
	for _, tt := range tests {
		if got := IsSubagentSessionKey(tt.input); got != tt.want {
			t.Errorf("IsSubagentSessionKey(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// TestParseAgentSessionKey_SpacePadding verifies leading/trailing whitespace is
// trimmed on the whole key.
func TestParseAgentSessionKey_SpacePadding(t *testing.T) {
	parsed := ParseAgentSessionKey("  agent:sales:telegram  ")
	if parsed == nil {
		t.Fatal("expected non-nil result for padded key")
	}
	if parsed.AgentID != "sales" {
		t.Errorf("AgentID = %q, want 'sales'", parsed.AgentID)
	}
	if parsed.Rest != "telegram" {
		t.Errorf("Rest = %q, want 'telegram'", parsed.Rest)
	}
}

// TestNormalizeChannel_Empty covers the normalizeChannel "unknown" branch.
func TestNormalizeChannel_Empty(t *testing.T) {
	if got := normalizeChannel(""); got != "unknown" {
		t.Errorf("normalizeChannel('') = %q, want 'unknown'", got)
	}
	if got := normalizeChannel("   "); got != "unknown" {
		t.Errorf("normalizeChannel('   ') = %q, want 'unknown'", got)
	}
}

// TestNormalizeChannel_Uppercase verifies channels are lowercased + trimmed.
func TestNormalizeChannel_Uppercase(t *testing.T) {
	if got := normalizeChannel("  Telegram  "); got != "telegram" {
		t.Errorf("normalizeChannel('  Telegram  ') = %q, want 'telegram'", got)
	}
}

// TestResolveLinkedPeerID_NoLinks covers the empty identityLinks branch.
func TestResolveLinkedPeerID_NoLinks(t *testing.T) {
	if got := resolveLinkedPeerID(nil, "telegram", "user1"); got != "" {
		t.Errorf("nil links = %q, want ''", got)
	}
	if got := resolveLinkedPeerID(map[string][]string{}, "telegram", "user1"); got != "" {
		t.Errorf("empty links = %q, want ''", got)
	}
}

// TestResolveLinkedPeerID_EmptyPeerID covers the empty peerID branch.
func TestResolveLinkedPeerID_EmptyPeerID(t *testing.T) {
	links := map[string][]string{"john": {"telegram:user1"}}
	if got := resolveLinkedPeerID(links, "telegram", "   "); got != "" {
		t.Errorf("empty peerID = %q, want ''", got)
	}
}

// TestResolveLinkedPeerID_NoMatch covers the case where the peer is not in any
// identity link, returning "".
func TestResolveLinkedPeerID_NoMatch(t *testing.T) {
	links := map[string][]string{"john": {"telegram:user1"}}
	if got := resolveLinkedPeerID(links, "telegram", "someone-else"); got != "" {
		t.Errorf("no match = %q, want ''", got)
	}
}

// TestResolveLinkedPeerID_EmptyCanonicalName covers the branch where a canonical
// name is empty and is skipped.
func TestResolveLinkedPeerID_EmptyCanonicalName(t *testing.T) {
	links := map[string][]string{
		"":    {"telegram:user1"},
		"bob": {"telegram:user2"},
	}
	// user2 matches bob; ensure the empty-canonical entry is skipped without panic
	if got := resolveLinkedPeerID(links, "telegram", "user2"); got != "bob" {
		t.Errorf("empty canonical skip = %q, want 'bob'", got)
	}
	// user1 is only under the empty canonical → skipped → ""
	if got := resolveLinkedPeerID(links, "telegram", "user1"); got != "" {
		t.Errorf("user1 should be skipped = %q, want ''", got)
	}
}

// TestResolveLinkedPeerID_ExactMatchNoChannel verifies a bare peerID matching an
// identity link stored without a channel prefix works without channel scoping.
func TestResolveLinkedPeerID_ExactMatchNoChannel(t *testing.T) {
	links := map[string][]string{"alice": {"alice_tg"}}
	if got := resolveLinkedPeerID(links, "", "alice_tg"); got != "alice" {
		t.Errorf("exact match = %q, want 'alice'", got)
	}
}

// TestResolveLinkedPeerID_ChannelScopedMatch verifies channel:peerID matching.
func TestResolveLinkedPeerID_ChannelScopedMatch(t *testing.T) {
	links := map[string][]string{"alice": {"discord:alice_dc"}}
	if got := resolveLinkedPeerID(links, "discord", "alice_dc"); got != "alice" {
		t.Errorf("channel-scoped match = %q, want 'alice'", got)
	}
}

// TestResolveLinkedPeerID_CaseInsensitive verifies matching is case-insensitive.
func TestResolveLinkedPeerID_CaseInsensitive(t *testing.T) {
	links := map[string][]string{"John": {"Telegram:User123"}}
	if got := resolveLinkedPeerID(links, "telegram", "user123"); got != "John" {
		t.Errorf("case-insensitive match = %q, want 'John'", got)
	}
}
