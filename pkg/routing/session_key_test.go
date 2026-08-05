package routing

import (
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func TestBuildAgentMainSessionKey(t *testing.T) {
	got := BuildAgentMainSessionKey("sales")
	want := "agent:sales:main"
	if got != want {
		t.Errorf("BuildAgentMainSessionKey('sales') = %q, want %q", got, want)
	}
}

func TestBuildAgentMainSessionKey_Normalizes(t *testing.T) {
	got := BuildAgentMainSessionKey("Sales Bot")
	want := "agent:sales-bot:main"
	if got != want {
		t.Errorf("BuildAgentMainSessionKey('Sales Bot') = %q, want %q", got, want)
	}
}

func TestBuildAgentPeerSessionKey_DMScopeMain(t *testing.T) {
	got := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID: "main",
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "direct", ID: "user123"},
		DMScope: DMScopeMain,
	})
	want := "agent:main:main"
	if got != want {
		t.Errorf("DMScopeMain = %q, want %q", got, want)
	}
}

func TestBuildAgentPeerSessionKey_DMScopePerPeer(t *testing.T) {
	got := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID: "main",
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "direct", ID: "user123"},
		DMScope: DMScopePerPeer,
	})
	want := "agent:main:direct:user123"
	if got != want {
		t.Errorf("DMScopePerPeer = %q, want %q", got, want)
	}
}

func TestBuildAgentPeerSessionKey_DMScopePerChannelPeer(t *testing.T) {
	got := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID: "main",
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "direct", ID: "user123"},
		DMScope: DMScopePerChannelPeer,
	})
	want := "agent:main:telegram:direct:user123"
	if got != want {
		t.Errorf("DMScopePerChannelPeer = %q, want %q", got, want)
	}
}

func TestBuildAgentPeerSessionKey_DMScopePerAccountChannelPeer(t *testing.T) {
	got := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID:   "main",
		Channel:   "telegram",
		AccountID: "bot1",
		Peer:      &RoutePeer{Kind: "direct", ID: "User123"},
		DMScope:   DMScopePerAccountChannelPeer,
	})
	want := "agent:main:telegram:bot1:direct:user123"
	if got != want {
		t.Errorf("DMScopePerAccountChannelPeer = %q, want %q", got, want)
	}
}

func TestBuildAgentPeerSessionKey_GroupPeer(t *testing.T) {
	got := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID: "main",
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "group", ID: "chat456"},
		DMScope: DMScopePerPeer,
	})
	want := "agent:main:telegram:group:chat456"
	if got != want {
		t.Errorf("GroupPeer = %q, want %q", got, want)
	}
}

func TestBuildAgentPeerSessionKey_NilPeer(t *testing.T) {
	got := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID: "main",
		Channel: "telegram",
		Peer:    nil,
		DMScope: DMScopePerPeer,
	})
	// nil peer defaults to direct with empty ID, falls to main
	want := "agent:main:main"
	if got != want {
		t.Errorf("NilPeer = %q, want %q", got, want)
	}
}

func TestBuildAgentPeerSessionKey_IdentityLink(t *testing.T) {
	links := map[string][]string{
		"john": {"telegram:user123", "discord:john#1234"},
	}
	got := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID:       "main",
		Channel:       "telegram",
		Peer:          &RoutePeer{Kind: "direct", ID: "user123"},
		DMScope:       DMScopePerPeer,
		IdentityLinks: links,
	})
	want := "agent:main:direct:john"
	if got != want {
		t.Errorf("IdentityLink = %q, want %q", got, want)
	}
}

func TestParseAgentSessionKey_Valid(t *testing.T) {
	parsed := ParseAgentSessionKey("agent:sales:telegram:direct:user123")
	if parsed == nil {
		t.Fatal("expected non-nil result")
	}
	if parsed.AgentID != "sales" {
		t.Errorf("AgentID = %q, want 'sales'", parsed.AgentID)
	}
	if parsed.Rest != "telegram:direct:user123" {
		t.Errorf("Rest = %q, want 'telegram:direct:user123'", parsed.Rest)
	}
}

func TestParseAgentSessionKey_Invalid(t *testing.T) {
	tests := []string{
		"",
		"foo:bar",
		"notprefix:sales:main",
		"agent::main",
		"agent:sales:",
	}
	for _, input := range tests {
		if got := ParseAgentSessionKey(input); got != nil {
			t.Errorf("ParseAgentSessionKey(%q) = %+v, want nil", input, got)
		}
	}
}

func TestIsSubagentSessionKey(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"subagent:task-1", true},
		{"agent:main:subagent:task-1", true},
		{"agent:main:main", false},
		{"agent:main:telegram:direct:user123", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsSubagentSessionKey(tt.input); got != tt.want {
			t.Errorf("IsSubagentSessionKey(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ─── Cross-channel session unification tests (Phase 6) ────────────────────

// TestCrossChannel_UnifiedSession_PerPeer verifies the core unification scenario:
// same user on Telegram + WhatsApp + Discord with dm_scope=per-peer gets the
// same session key, enabling shared conversation history.
func TestCrossChannel_UnifiedSession_PerPeer(t *testing.T) {
	links := map[string][]string{
		"john": {"telegram:123456", "whatsapp:+1234567890", "discord:john#1234"},
	}
	params := func(channel, peerID string) SessionKeyParams {
		return SessionKeyParams{
			AgentID:       "main",
			Channel:       channel,
			Peer:          &RoutePeer{Kind: "direct", ID: peerID},
			DMScope:       DMScopePerPeer,
			IdentityLinks: links,
		}
	}

	tgKey := BuildAgentPeerSessionKey(params("telegram", "123456"))
	waKey := BuildAgentPeerSessionKey(params("whatsapp", "+1234567890"))
	dcKey := BuildAgentPeerSessionKey(params("discord", "john#1234"))

	// All three should resolve to the identity-linked canonical key
	want := "agent:main:direct:john"
	if tgKey != want {
		t.Errorf("Telegram key = %q, want %q", tgKey, want)
	}
	if waKey != want {
		t.Errorf("WhatsApp key = %q, want %q", waKey, want)
	}
	if dcKey != want {
		t.Errorf("Discord key = %q, want %q", dcKey, want)
	}
}

// TestCrossChannel_SeparateSession_PerChannelPeer verifies that with
// dm_scope=per-channel-peer, the same linked user gets different sessions
// per channel.
func TestCrossChannel_SeparateSession_PerChannelPeer(t *testing.T) {
	links := map[string][]string{
		"john": {"telegram:123456", "whatsapp:+1234567890"},
	}
	params := func(channel, peerID string) SessionKeyParams {
		return SessionKeyParams{
			AgentID:       "main",
			Channel:       channel,
			Peer:          &RoutePeer{Kind: "direct", ID: peerID},
			DMScope:       DMScopePerChannelPeer,
			IdentityLinks: links,
		}
	}

	tgKey := BuildAgentPeerSessionKey(params("telegram", "123456"))
	waKey := BuildAgentPeerSessionKey(params("whatsapp", "+1234567890"))

	if tgKey == waKey {
		t.Errorf("per-channel-peer should produce different keys, both got %q", tgKey)
	}

	if tgKey != "agent:main:telegram:direct:john" {
		t.Errorf("Telegram key = %q, want %q", tgKey, "agent:main:telegram:direct:john")
	}
	if waKey != "agent:main:whatsapp:direct:john" {
		t.Errorf("WhatsApp key = %q, want %q", waKey, "agent:main:whatsapp:direct:john")
	}
}

// TestCrossChannel_GroupSessionsRemainIsolated verifies that group sessions
// always include the channel+group in the key and never unify cross-channel,
// even with identity links.
func TestCrossChannel_GroupSessionsRemainIsolated(t *testing.T) {
	links := map[string][]string{
		// Identity links should NOT affect group sessions
		"team": {"telegram:group100", "discord:channel200"},
	}

	tgKey := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID:       "main",
		Channel:       "telegram",
		Peer:          &RoutePeer{Kind: "group", ID: "group100"},
		DMScope:       DMScopePerPeer,
		IdentityLinks: links,
	})
	dcKey := BuildAgentPeerSessionKey(SessionKeyParams{
		AgentID:       "main",
		Channel:       "discord",
		Peer:          &RoutePeer{Kind: "channel", ID: "channel200"},
		DMScope:       DMScopePerPeer,
		IdentityLinks: links,
	})

	if tgKey == dcKey {
		t.Errorf("group sessions should never unify cross-channel, both got %q", tgKey)
	}

	// Groups always include channel prefix
	if tgKey != "agent:main:telegram:group:group100" {
		t.Errorf("Telegram group key = %q, want %q", tgKey, "agent:main:telegram:group:group100")
	}
	if dcKey != "agent:main:discord:channel:channel200" {
		t.Errorf("Discord channel key = %q, want %q", dcKey, "agent:main:discord:channel:channel200")
	}
}

// TestCrossChannel_IdentityLink_ResolveMultiple verifies that a single identity
// can have many linked identifiers across channels and they all resolve to the same key.
func TestCrossChannel_IdentityLink_ResolveMultiple(t *testing.T) {
	links := map[string][]string{
		"alice": {
			"telegram:alice_tg",
			"whatsapp:+15550001111",
			"discord:alice_dc",
			"slack:U0ALICE",
			"feishu:ou_alice",
			"line:U_alice_line",
			"onebot:10001",
			"qq:alice_qq",
			"dingtalk:alice_dd",
		},
	}

	want := "agent:main:direct:alice"
	channels := []struct {
		channel string
		peerID  string
	}{
		{"telegram", "alice_tg"},
		{"whatsapp", "+15550001111"},
		{"discord", "alice_dc"},
		{"slack", "U0ALICE"},
		{"feishu", "ou_alice"},
		{"line", "U_alice_line"},
		{"onebot", "10001"},
		{"qq", "alice_qq"},
		{"dingtalk", "alice_dd"},
	}

	for _, ch := range channels {
		key := BuildAgentPeerSessionKey(SessionKeyParams{
			AgentID:       "main",
			Channel:       ch.channel,
			Peer:          &RoutePeer{Kind: "direct", ID: ch.peerID},
			DMScope:       DMScopePerPeer,
			IdentityLinks: links,
		})
		if key != want {
			t.Errorf("%s key = %q, want %q", ch.channel, key, want)
		}
	}
}

// TestCrossChannel_ResolveRoute_CrossChannelSameKey verifies the full pipeline:
// RouteResolver.ResolveRoute() produces the same session key for a linked user
// arriving from different channels with dm_scope=per-peer.
func TestCrossChannel_ResolveRoute_CrossChannelSameKey(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: "/tmp/lele-test",
				Model:     "gpt-4",
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true},
			},
		},
		Session: config.SessionConfig{
			DMScope: "per-peer",
			IdentityLinks: map[string][]string{
				"john": {"telegram:123456", "whatsapp:+1234567890", "discord:john#1234"},
			},
		},
	}
	r := NewRouteResolver(cfg)

	tgRoute := r.ResolveRoute(RouteInput{
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "direct", ID: "123456"},
	})
	waRoute := r.ResolveRoute(RouteInput{
		Channel: "whatsapp",
		Peer:    &RoutePeer{Kind: "direct", ID: "+1234567890"},
	})
	dcRoute := r.ResolveRoute(RouteInput{
		Channel: "discord",
		Peer:    &RoutePeer{Kind: "direct", ID: "john#1234"},
	})

	if tgRoute.SessionKey != waRoute.SessionKey {
		t.Errorf("Telegram=%q vs WhatsApp=%q — should be same session", tgRoute.SessionKey, waRoute.SessionKey)
	}
	if tgRoute.SessionKey != dcRoute.SessionKey {
		t.Errorf("Telegram=%q vs Discord=%q — should be same session", tgRoute.SessionKey, dcRoute.SessionKey)
	}

	// Verify the expected canonical key
	want := "agent:main:direct:john"
	if tgRoute.SessionKey != want {
		t.Errorf("SessionKey = %q, want %q", tgRoute.SessionKey, want)
	}
}

// TestCrossChannel_ResolveRoute_PerChannelPeer_ProducesDifferentKeys verifies
// that the full pipeline with per-channel-peer scope produces distinct session
// keys per channel even with identity links.
func TestCrossChannel_ResolveRoute_PerChannelPeer_ProducesDifferentKeys(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: "/tmp/lele-test",
				Model:     "gpt-4",
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true},
			},
		},
		Session: config.SessionConfig{
			DMScope: "per-channel-peer",
			IdentityLinks: map[string][]string{
				"john": {"telegram:123456", "whatsapp:+1234567890"},
			},
		},
	}
	r := NewRouteResolver(cfg)

	tgRoute := r.ResolveRoute(RouteInput{
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "direct", ID: "123456"},
	})
	waRoute := r.ResolveRoute(RouteInput{
		Channel: "whatsapp",
		Peer:    &RoutePeer{Kind: "direct", ID: "+1234567890"},
	})

	if tgRoute.SessionKey == waRoute.SessionKey {
		t.Errorf("per-channel-peer: Telegram=%q should differ from WhatsApp=%q", tgRoute.SessionKey, waRoute.SessionKey)
	}
}

// TestCrossChannel_ResolveRoute_GroupRoutes correctly verifies that group
// routes are always channel-scoped and never unified.
func TestCrossChannel_ResolveRoute_GroupRoutes(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "main", Default: true},
			},
		},
		Session: config.SessionConfig{
			DMScope: "per-peer",
		},
	}
	r := NewRouteResolver(cfg)

	tgGroup := r.ResolveRoute(RouteInput{
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "group", ID: "group100"},
	})
	waGroup := r.ResolveRoute(RouteInput{
		Channel: "whatsapp",
		Peer:    &RoutePeer{Kind: "group", ID: "group100"},
	})

	if tgGroup.SessionKey == waGroup.SessionKey {
		t.Errorf("group sessions should be channel-scoped, both got %q", tgGroup.SessionKey)
	}

	if tgGroup.SessionKey != "agent:main:telegram:group:group100" {
		t.Errorf("Telegram group = %q, want %q", tgGroup.SessionKey, "agent:main:telegram:group:group100")
	}
	if waGroup.SessionKey != "agent:main:whatsapp:group:group100" {
		t.Errorf("WhatsApp group = %q, want %q", waGroup.SessionKey, "agent:main:whatsapp:group:group100")
	}
}
