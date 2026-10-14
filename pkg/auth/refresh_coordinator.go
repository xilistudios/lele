package auth

import (
	"errors"
	"fmt"
	"sync"
)

// RefreshPolicy controls when the coordinator is allowed to refresh a credential.
type RefreshPolicy struct {
	// RequireOAuthMethod: when true, a credential whose AuthMethod is not
	// "oauth" is returned as-is without refreshing (the codex API-key case).
	// The zero value (false) reproduces antigravity's behavior (no check).
	RequireOAuthMethod bool
}

// refreshLocks holds one mutex per provider key. The map itself is guarded by
// refreshLocksMu; the values are the per-key locks that serialize refreshes so
// that concurrent callers sharing a key collapse into a single token request.
var (
	refreshLocksMu sync.Mutex
	refreshLocks   = make(map[string]*sync.Mutex)
)

// refreshLockFor returns the mutex guarding refreshes for the given provider
// key, creating it on first use. Keys are independent: two different providers
// never block each other.
func refreshLockFor(provider string) *sync.Mutex {
	refreshLocksMu.Lock()
	defer refreshLocksMu.Unlock()
	l, ok := refreshLocks[provider]
	if !ok {
		l = &sync.Mutex{}
		refreshLocks[provider] = l
	}
	return l
}

// EnsureFreshCredential returns a usable credential for provider, refreshing it
// at most once across concurrent callers.
//
// The lock is per provider (the store key), not per cfg.Name. It returns
// (nil, nil) when no credential is stored for provider (the caller decides the
// message), and returns the stored credential untouched (no network) when no
// refresh is warranted.
//
// Refresh failures never lose a rotated refresh token. When the token endpoint
// succeeds but the response fails the inference-scope gate, RefreshAccessToken
// returns the freshly issued credential TOGETHER with an *inferenceScopeError:
// the endpoint has already consumed the old refresh token and handed out the
// new one, so the coordinator persists that credential (with the same metadata
// carry-over as the success path) BEFORE returning the error. jcode runs the
// gate before persisting, which drops the rotated token while the old one is
// already spent and therefore bricks the account; this ordering deliberately
// inverts it. The gate error keeps its own text under the usual
// "refreshing token: <err>" prefix, and a failing save is reported too: both
// failures are mentioned in the returned message.
//
// Returned errors are already contextualized with the same prefixes the
// providers used to build inline: "loading auth credentials: <err>" when the
// credential cannot be read, "refreshing token: <err>" when the token endpoint
// fails and "saving refreshed token: <err>" when the refreshed credential
// cannot be persisted. Callers MUST return the error as-is and MUST NOT wrap it
// again, or the prefixes would be duplicated/nested.
func EnsureFreshCredential(provider string, cfg OAuthProviderConfig, policy RefreshPolicy) (*AuthCredential, error) {
	l := refreshLockFor(provider)
	l.Lock()
	defer l.Unlock()

	// Reload the persisted state inside the lock: a previous winner may have
	// already refreshed it, and we must never refresh using a stale (possibly
	// already-consumed, rotated) refresh token.
	stored, err := GetCredential(provider)
	if err != nil {
		return nil, fmt.Errorf("loading auth credentials: %w", err)
	}
	if stored == nil {
		return nil, nil
	}

	// Fast path: the stored credential is already fresh. The 5-minute margin of
	// NeedsRefresh subsumes the 60s FRESHNESS_MARGIN of the reference
	// implementation, so no extra margin constant is introduced.
	if !stored.NeedsRefresh() {
		return stored, nil
	}

	// Policy filter: only "oauth" credentials are refreshable when required.
	if policy.RequireOAuthMethod && stored.AuthMethod != "oauth" {
		return stored, nil
	}

	// No refresh token: nothing to do, hand back the stored credential as-is.
	if stored.RefreshToken == "" {
		return stored, nil
	}

	refreshed, err := RefreshAccessToken(stored, cfg)
	if err != nil {
		// Anti-bug port: when the endpoint rotated the refresh token but the
		// response failed the inference-scope gate, the old token is already
		// consumed. Persist the freshly issued credential (mirroring the
		// success path's metadata carry-over below, field for field) before
		// surfacing the failure, or the account would be bricked: the new
		// refresh token would exist nowhere while the stored one no longer
		// works. The typed error is detected with errors.As, never by string.
		var scopeErr *inferenceScopeError
		if errors.As(err, &scopeErr) && scopeErr.Credential != nil {
			rotated := scopeErr.Credential
			if stored.Email != "" {
				rotated.Email = stored.Email
			}
			if rotated.AuthMethod == "" {
				rotated.AuthMethod = stored.AuthMethod
			}
			if rotated.Provider == "" {
				rotated.Provider = stored.Provider
			}
			if saveErr := SetCredential(provider, rotated); saveErr != nil {
				// Never swallow one failure to report the other: the message
				// still starts with the gate error and also names the save.
				return nil, fmt.Errorf("saving rotated token after inference-scope failure: %w (gate error: %v)", saveErr, err)
			}
		}
		// Do not corrupt the stored credential when the refresh fails.
		return nil, fmt.Errorf("refreshing token: %w", err)
	}

	// The token endpoint can never change the user's identity: carry the stored
	// metadata over. (RefreshAccessToken already preserves RefreshToken,
	// AccountID and ProjectID when the response omits them.)
	if stored.Email != "" {
		refreshed.Email = stored.Email
	}
	if refreshed.AuthMethod == "" {
		refreshed.AuthMethod = stored.AuthMethod
	}
	if refreshed.Provider == "" {
		refreshed.Provider = stored.Provider
	}

	if err := SetCredential(provider, refreshed); err != nil {
		return nil, fmt.Errorf("saving refreshed token: %w", err)
	}
	return refreshed, nil
}
