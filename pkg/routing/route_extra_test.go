package routing

import (
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// TestResolveRoute_EmptyDMScope verifies the dmScope == "" branch inside
// ResolveRoute falls back to DMScopeMain.
func TestResolveRoute_EmptyDMScope(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{{ID: "main", Default: true}},
		},
		// Session.DMScope empty → should default to main
	}
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{
		Channel: "telegram",
		Peer:    &RoutePeer{Kind: "direct", ID: "user1"},
	})
	// DMScope main → falls to main session key
	if route.SessionKey != "agent:main:main" {
		t.Errorf("SessionKey = %q, want 'agent:main:main'", route.SessionKey)
	}
}

// TestResolveRoute_ParentPeerBinding verifies Priority 2 (parent peer) binding.
func TestResolveRoute_ParentPeerBinding(t *testing.T) {
	agents := []config.AgentConfig{{ID: "main", Default: true}, {ID: "room"}}
	bindings := []config.AgentBinding{
		{
			AgentID: "room",
			Match: config.BindingMatch{
				Channel:   "telegram",
				AccountID: "*",
				Peer:      &config.PeerMatch{Kind: "group", ID: "supergroup1"},
			},
		},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:    "telegram",
		Peer:       &RoutePeer{Kind: "direct", ID: "user1"},
		ParentPeer: &RoutePeer{Kind: "group", ID: "supergroup1"},
	})
	if route.AgentID != "room" {
		t.Errorf("AgentID = %q, want 'room'", route.AgentID)
	}
	if route.MatchedBy != "binding.peer.parent" {
		t.Errorf("MatchedBy = %q, want 'binding.peer.parent'", route.MatchedBy)
	}
}

// TestResolveRoute_ParentPeerNoMatchWhenNil verifies no parent peer → no parent binding match;
// should fall through to default.
func TestResolveRoute_ParentPeerEmptyID(t *testing.T) {
	agents := []config.AgentConfig{{ID: "main", Default: true}}
	bindings := []config.AgentBinding{
		{AgentID: "nope", Match: config.BindingMatch{
			Channel: "telegram", AccountID: "*", Peer: &config.PeerMatch{Kind: "group", ID: "pg"}}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)

	route := r.ResolveRoute(RouteInput{
		Channel:    "telegram",
		Peer:       &RoutePeer{Kind: "direct", ID: "u"},
		ParentPeer: &RoutePeer{Kind: "group", ID: "  "}, // empty ID → skip
	})
	if route.AgentID != "main" {
		t.Errorf("AgentID = %q, want 'main'", route.AgentID)
	}
}

// TestResolveRoute_ChannelCaseInsensitive verifies channel matching is
// case-insensitive and trimmed.
func TestResolveRoute_ChannelCaseInsensitive(t *testing.T) {
	agents := []config.AgentConfig{{ID: "main", Default: true}, {ID: "tg"}}
	bindings := []config.AgentBinding{
		{AgentID: "tg", Match: config.BindingMatch{Channel: "telegram", AccountID: "*"}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{Channel: "  TELEGRAM  "})
	if route.AgentID != "tg" {
		t.Errorf("AgentID = %q, want 'tg' (case-insensitive channel)", route.AgentID)
	}
}

// TestResolveRoute_ChannelMismatchFiltered verifies filterBindings skips bindings
// whose channel doesn't match (matchChannel != channel) AND skips empty-channel
// bindings (matchChannel == "").
func TestResolveRoute_ChannelMismatchFiltered(t *testing.T) {
	agents := []config.AgentConfig{{ID: "main", Default: true}, {ID: "other"}}
	bindings := []config.AgentBinding{
		{AgentID: "other", Match: config.BindingMatch{Channel: "slack", AccountID: "*"}},
		{AgentID: "other", Match: config.BindingMatch{Channel: "", AccountID: "*"}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{Channel: "telegram"})
	if route.AgentID != "main" {
		t.Errorf("AgentID = %q, want 'main' (mismatched/empty channels filtered)", route.AgentID)
	}
}

// TestResolveRoute_AccountIDMismatchFiltered verifies filterBindings drops a
// binding when the account ID does not match.
func TestResolveRoute_AccountIDMismatchFiltered(t *testing.T) {
	agents := []config.AgentConfig{{ID: "main", Default: true}, {ID: "other"}}
	bindings := []config.AgentBinding{
		{AgentID: "other", Match: config.BindingMatch{Channel: "telegram", AccountID: "bot2"}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{Channel: "telegram", AccountID: "bot1"})
	if route.AgentID != "main" {
		t.Errorf("AgentID = %q, want 'main' (account mismatch filtered)", route.AgentID)
	}
}

// TestResolveRoute_EmptyAccountMatchesDefault verifies matchesAccountID returns
// true when the binding's AccountID is empty and the actual account is default.
func TestResolveRoute_EmptyAccountMatchesDefault(t *testing.T) {
	agents := []config.AgentConfig{{ID: "main", Default: true}, {ID: "bot1"}}
	bindings := []config.AgentBinding{
		{AgentID: "bot1", Match: config.BindingMatch{Channel: "telegram"}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{Channel: "telegram", AccountID: "default"})
	if route.AgentID != "bot1" {
		t.Errorf("AgentID = %q, want 'bot1'", route.AgentID)
	}
	if route.MatchedBy != "binding.account" {
		t.Errorf("MatchedBy = %q, want 'binding.account'", route.MatchedBy)
	}
}

// TestResolveRoute_EmptyAccountNonDefaultFallsThrough verifies the empty
// AccountID binding does NOT match a non-default actual account.
func TestResolveRoute_EmptyAccountNonDefaultFallsThrough(t *testing.T) {
	agents := []config.AgentConfig{{ID: "main", Default: true}, {ID: "bot1"}}
	bindings := []config.AgentBinding{
		{AgentID: "bot1", Match: config.BindingMatch{Channel: "telegram"}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{Channel: "telegram", AccountID: "otherbot"})
	if route.AgentID != "main" {
		t.Errorf("AgentID = %q, want 'main'", route.AgentID)
	}
}

// TestResolveRoute_PeerBindingWithEmptyKindID verifies findPeerMatch skips
// bindings with empty peer kind or peer ID.
func TestResolveRoute_PeerBindingWithEmptyKindID(t *testing.T) {
	agents := []config.AgentConfig{{ID: "main", Default: true}, {ID: "agent1"}}
	bindings := []config.AgentBinding{
		{AgentID: "agent1", Match: config.BindingMatch{
			Channel: "telegram", AccountID: "*", Peer: &config.PeerMatch{Kind: "", ID: "user1"}}},
		{AgentID: "agent1", Match: config.BindingMatch{
			Channel: "telegram", AccountID: "*", Peer: &config.PeerMatch{Kind: "direct", ID: ""}}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{Channel: "telegram", Peer: &RoutePeer{Kind: "direct", ID: "user1"}})
	// Both peer bindings have empty kind or ID → skipped → default
	if route.AgentID != "main" {
		t.Errorf("AgentID = %q, want 'main'", route.AgentID)
	}
}

// TestResolveRoute_PeerKindMismatchCoversReturnNil verifies findPeerMatch
// returns nil when the peer kind does not match (end-of-loop return nil).
func TestResolveRoute_PeerKindMismatch(t *testing.T) {
	agents := []config.AgentConfig{{ID: "main", Default: true}, {ID: "agent1"}}
	bindings := []config.AgentBinding{
		{AgentID: "agent1", Match: config.BindingMatch{
			Channel: "telegram", AccountID: "*", Peer: &config.PeerMatch{Kind: "group", ID: "user1"}}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{Channel: "telegram", Peer: &RoutePeer{Kind: "direct", ID: "user1"}})
	if route.AgentID != "main" {
		t.Errorf("AgentID = %q, want 'main' (kind mismatch)", route.AgentID)
	}
}

// TestResolveRoute_GuildNoMatch verifies findGuildMatch returns nil.
func TestResolveRoute_GuildNoMatch(t *testing.T) {
	agents := []config.AgentConfig{{ID: "main", Default: true}, {ID: "g1"}}
	bindings := []config.AgentBinding{
		{AgentID: "g1", Match: config.BindingMatch{Channel: "discord", AccountID: "*", GuildID: "guild-a"}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{Channel: "discord", GuildID: "guild-b"})
	if route.AgentID != "main" {
		t.Errorf("AgentID = %q, want 'main' (guild no match)", route.AgentID)
	}
}

// TestResolveRoute_TeamNoMatch verifies findTeamMatch returns nil.
func TestResolveRoute_TeamNoMatch(t *testing.T) {
	agents := []config.AgentConfig{{ID: "main", Default: true}, {ID: "t1"}}
	bindings := []config.AgentBinding{
		{AgentID: "t1", Match: config.BindingMatch{Channel: "slack", AccountID: "*", TeamID: "T1"}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{Channel: "slack", TeamID: "T2"})
	if route.AgentID != "main" {
		t.Errorf("AgentID = %q, want 'main' (team no match)", route.AgentID)
	}
}

// TestResolveRoute_LowerPriorityGuildTeamPeerCombined verifies the account match
// skips bindings that also carry peer/guild/team criteria (return nil covered).
func TestResolveRoute_AccountBindingWithOtherCriteriaSkipped(t *testing.T) {
	agents := []config.AgentConfig{{ID: "main", Default: true}, {ID: "acct1"}}
	bindings := []config.AgentBinding{
		{AgentID: "acct1", Match: config.BindingMatch{
			Channel: "telegram", AccountID: "bot2", GuildID: "guild-x"}},
		{AgentID: "acct1", Match: config.BindingMatch{
			Channel: "telegram", AccountID: "bot2", TeamID: "T-x"}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)
	// Account matches bot2 but peer is also set on both bindings → skip → default
	route := r.ResolveRoute(RouteInput{Channel: "telegram", AccountID: "bot2"})
	if route.AgentID != "main" {
		t.Errorf("AgentID = %q, want 'main' (account binding with other criteria skipped)", route.AgentID)
	}
}

// TestResolveRoute_AccountMatchSkipsWildcard verifies findAccountMatch skips
// wildcard ("*") account bindings (those go to channel-wildcard priority).
func TestResolveRoute_AccountWildcardGoesToChannel(t *testing.T) {
	agents := []config.AgentConfig{{ID: "main", Default: true}, {ID: "wc"}}
	bindings := []config.AgentBinding{
		{AgentID: "wc", Match: config.BindingMatch{Channel: "telegram", AccountID: "*"}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{Channel: "telegram", AccountID: "bot9"})
	if route.MatchedBy != "binding.channel" {
		t.Errorf("MatchedBy = %q, want 'binding.channel'", route.MatchedBy)
	}
}

// TestResolveRoute_ChannelWildcardSkipNonWildcard + miscriteria covers the
// findChannelWildcardMatch branch where accountID != "*" is skipped and where a
// wildcard binding with peer/guild/team is skipped.
func TestResolveRoute_ChannelWildcardSkipCriteria(t *testing.T) {
	agents := []config.AgentConfig{{ID: "main", Default: true}, {ID: "wc"}}
	bindings := []config.AgentBinding{
		// non-wildcard, skipped by findChannelWildcardMatch
		{AgentID: "wc", Match: config.BindingMatch{Channel: "telegram", AccountID: "bot2"}},
		// wildcard but has guild → skipped
		{AgentID: "wc", Match: config.BindingMatch{Channel: "telegram", AccountID: "*", GuildID: "g"}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{Channel: "telegram", AccountID: "bot2"})
	// first binding matches account bot2 → binding.account
	if route.MatchedBy != "binding.account" {
		t.Errorf("MatchedBy = %q, want 'binding.account'", route.MatchedBy)
	}
}

// TestResolveRoute_EmptyAgentIDInBindingFallsBackToDefault verifies pickAgentID
// handles an empty agent ID in a binding (falls back to DefaultAgentID).
func TestResolveRoute_EmptyAgentIDInBinding(t *testing.T) {
	agents := []config.AgentConfig{{ID: "main", Default: true}}
	bindings := []config.AgentBinding{
		{AgentID: "  ", Match: config.BindingMatch{Channel: "telegram", AccountID: "*"}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{Channel: "telegram"})
	if route.AgentID != "main" {
		t.Errorf("AgentID = %q, want 'main'", route.AgentID)
	}
}

// TestResolveRoute_EmptyAgentListUsesDefaultAgentID covers pickAgentID +
// resolveDefaultAgentID when no agents are configured at all.
func TestResolveRoute_EmptyAgentListUsesDefaultAgentID(t *testing.T) {
	cfg := testConfig(nil, []config.AgentBinding{
		{AgentID: "", Match: config.BindingMatch{Channel: "telegram", AccountID: "*"}},
	})
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{Channel: "telegram"})
	if route.AgentID != DefaultAgentID {
		t.Errorf("AgentID = %q, want %q", route.AgentID, DefaultAgentID)
	}
}

// TestResolveRoute_DefaultAgentWithEmptyID falls back to DefaultAgentID when the
// marked-default agent has a blank ID.
func TestResolveRoute_DefaultAgentWithEmptyID(t *testing.T) {
	agents := []config.AgentConfig{{ID: "", Default: true}, {ID: "second"}}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{Channel: "cli"})
	if route.AgentID != "main" {
		t.Errorf("AgentID = %q, want 'main'", route.AgentID)
	}
}

// TestResolveRoute_FirstAgentEmptyIDFallsBackToDefault verifies that when the
// first agent has an empty ID and no default is marked, resolveDefaultAgentID
// returns DefaultAgentID (the final return).
func TestResolveRoute_FirstAgentEmptyIDFallsBackToDefault(t *testing.T) {
	agents := []config.AgentConfig{{ID: "   "}, {ID: "  "}}
	cfg := testConfig(agents, nil)
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{Channel: "cli"})
	if route.AgentID != DefaultAgentID {
		t.Errorf("AgentID = %q, want %q", route.AgentID, DefaultAgentID)
	}
}

// TestResolveRoute_AccountIDCaseInsensitive verifies account matching is
// case-insensitive (EqualFold branch).
func TestResolveRoute_AccountIDCaseInsensitive(t *testing.T) {
	agents := []config.AgentConfig{{ID: "main", Default: true}, {ID: "acct"}}
	bindings := []config.AgentBinding{
		{AgentID: "acct", Match: config.BindingMatch{Channel: "telegram", AccountID: "MyBot"}},
	}
	cfg := testConfig(agents, bindings)
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{Channel: "telegram", AccountID: "mybot"})
	if route.AgentID != "acct" {
		t.Errorf("AgentID = %q, want 'acct'", route.AgentID)
	}
}

// TestResolveRoute_NormalizesInput verifies channel normalization and account
// normalization flow through to the session key.
func TestResolveRoute_NormalizesInput(t *testing.T) {
	cfg := testConfig(nil, nil)
	r := NewRouteResolver(cfg)
	route := r.ResolveRoute(RouteInput{
		Channel:   " TELEGRAM ",
		AccountID: "",
		Peer:      &RoutePeer{Kind: "direct", ID: "USER1"},
	})
	if route.Channel != "telegram" {
		t.Errorf("route.Channel = %q, want 'telegram'", route.Channel)
	}
	if route.AccountID != DefaultAccountID {
		t.Errorf("route.AccountID = %q, want %q", route.AccountID, DefaultAccountID)
	}
	if route.MainSessionKey != "agent:main:main" {
		t.Errorf("MainSessionKey = %q, want 'agent:main:main'", route.MainSessionKey)
	}
}