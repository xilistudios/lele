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
