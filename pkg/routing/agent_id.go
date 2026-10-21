package routing

import (
	"regexp"
	"strings"

	"github.com/xilistudios/lele/pkg/config"
)

const (
	DefaultAgentID   = "main"
	DefaultMainKey   = "main"
	DefaultAccountID = "default"
	MaxAgentIDLength = 64
)

var (
	validIDRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	invalidCharsRe = regexp.MustCompile(`[^a-z0-9_-]+`)
	leadingDashRe  = regexp.MustCompile(`^-+`)
	trailingDashRe = regexp.MustCompile(`-+$`)
)

// NormalizeAgentID sanitizes an agent ID to [a-z0-9][a-z0-9_-]{0,63}.
// Invalid characters are collapsed to "-". Leading/trailing dashes stripped.
// Empty input returns DefaultAgentID ("main").
func NormalizeAgentID(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return DefaultAgentID
	}
	lower := strings.ToLower(trimmed)
	if validIDRe.MatchString(lower) {
		return lower
	}
	result := invalidCharsRe.ReplaceAllString(lower, "-")
	result = leadingDashRe.ReplaceAllString(result, "")
	result = trailingDashRe.ReplaceAllString(result, "")
	if len(result) > MaxAgentIDLength {
		result = result[:MaxAgentIDLength]
	}
	if result == "" {
		return DefaultAgentID
	}
	return result
}

// ConfiguredDefaultAgentID returns the ID of the agent a request addresses when
// no agent ID was given. With an explicit agents.list that is the configured
// default (the first entry with default: true), else the first configured
// agent; with no agents.list at all it is the implicit DefaultAgentID ("main").
//
// This is the ONE place the "which agent is the default?" rule lives: the route
// resolver uses it at runtime, and the CLI needs exactly the same answer to
// resolve its own --agent default. Before it existed every CLI flag defaulted
// to the literal "main", which is only correct for a single-agent install — on
// a multi-agent config (`agents.list: [coder, explorer, …]`) the very first
// `lele mcp list` failed with `unknown agent "main"`.
//
// The returned ID is always normalized, i.e. exactly what the registry keys its
// map with, so a caller can look it up directly. Duplicate IDs collapse in that
// map, which is why the first match wins here: it is the same agent.
func ConfiguredDefaultAgentID(cfg *config.Config) string {
	if cfg == nil {
		return DefaultAgentID
	}
	agents := cfg.Agents.List
	if len(agents) == 0 {
		return DefaultAgentID
	}
	for i := range agents {
		if !agents[i].Default {
			continue
		}
		if id := strings.TrimSpace(agents[i].ID); id != "" {
			return NormalizeAgentID(id)
		}
	}
	if id := strings.TrimSpace(agents[0].ID); id != "" {
		return NormalizeAgentID(id)
	}
	return DefaultAgentID
}

// NormalizeAccountID sanitizes an account ID. Empty returns DefaultAccountID.
func NormalizeAccountID(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return DefaultAccountID
	}
	lower := strings.ToLower(trimmed)
	if validIDRe.MatchString(lower) {
		return lower
	}
	result := invalidCharsRe.ReplaceAllString(lower, "-")
	result = leadingDashRe.ReplaceAllString(result, "")
	result = trailingDashRe.ReplaceAllString(result, "")
	if len(result) > MaxAgentIDLength {
		result = result[:MaxAgentIDLength]
	}
	if result == "" {
		return DefaultAccountID
	}
	return result
}
