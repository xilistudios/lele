package skills

import (
	"fmt"
	"io"
	"net/http"
)

const (
	// MaxSkillBodyBytes limits the size of a remote HTTP response body read
	// by this package. A SKILL.md is a Markdown file of at most a few KB; the
	// skills.json catalogue is similarly small. 5 MiB provides generous
	// headroom (≈5000 KB) while still protecting against a malicious or
	// misconfigured server returning an unbounded body that would exhaust
	// process memory. The value is intentionally two orders of magnitude
	// larger than expected payloads so that legitimate content is never
	// rejected.
	MaxSkillBodyBytes = 5 << 20 // 5 MiB
)

// readLimitedBody reads resp.Body with a size guard of MaxSkillBodyBytes.
// Returns an explicit error when the limit is exceeded instead of silently
// truncating. Follows the same +1 pattern used in pkg/locales/manager.go.
func readLimitedBody(resp *http.Response) ([]byte, error) {
	limited := io.LimitReader(resp.Body, MaxSkillBodyBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if len(data) > MaxSkillBodyBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes limit", MaxSkillBodyBytes)
	}
	return data, nil
}
