package auth

import (
	"errors"
	"fmt"
	"sync"
)

type RefreshPolicy struct {
	RequireOAuthMethod bool
}

var (
	refreshLocksMu sync.Mutex
	refreshLocks   = make(map[string]*sync.Mutex)
)

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

func EnsureFreshCredential(provider string, cfg OAuthProviderConfig, policy RefreshPolicy) (*AuthCredential, error) {
	l := refreshLockFor(provider)
	l.Lock()
	defer l.Unlock()

	stored, err := GetCredential(provider)
	if err != nil {
		return nil, fmt.Errorf("loading auth credentials: %w", err)
	}
	if stored == nil {
		return nil, nil
	}

	if !stored.NeedsRefresh() {
		return stored, nil
	}

	if policy.RequireOAuthMethod && stored.AuthMethod != "oauth" {
		return stored, nil
	}

	if stored.RefreshToken == "" {
		return stored, nil
	}

	refreshed, err := RefreshAccessToken(stored, cfg)
	if err != nil {
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
				return nil, fmt.Errorf("saving rotated token after inference-scope failure: %w (gate error: %v)", saveErr, err)
			}
		}
		return nil, fmt.Errorf("refreshing token: %w", err)
	}

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
