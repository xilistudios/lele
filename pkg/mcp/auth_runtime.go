package mcp

import (
	"github.com/mark3labs/mcp-go/client/transport"
)

type oauthRuntime struct {
	clientID                     string
	clientSecret                 string
	redirectURI                  string
	scopes                       []string
	authServerMetadataURL        string
	protectedResourceMetadataURL string
	tokenStore                   transport.TokenStore
}

func (r *oauthRuntime) oauthConfig() transport.OAuthConfig {
	return transport.OAuthConfig{
		ClientID:                     r.clientID,
		ClientSecret:                 r.clientSecret,
		RedirectURI:                  r.redirectURI,
		Scopes:                       r.scopes,
		TokenStore:                   r.tokenStore,
		AuthServerMetadataURL:        r.authServerMetadataURL,
		ProtectedResourceMetadataURL: r.protectedResourceMetadataURL,
		PKCEEnabled:                  true,
	}
}

func buildAuthRuntime(store *AuthStore, name string, cfg ServerConfig) *oauthRuntime {
	if store == nil || cfg.URL == "" || cfg.Command != "" {
		return nil
	}
	rec, hasRec := store.Record(name)
	if hasRec && rec.URL != cfg.URL {
		hasRec = false
	}
	explicit := cfg.Auth != nil && cfg.Auth.Type == typeAuthOAuth
	if !explicit && !(hasRec && rec.AuthRequired) {
		return nil
	}

	rt := &oauthRuntime{tokenStore: store.TokenStore(name, cfg.URL)}
	if explicit {
		rt.clientID = cfg.Auth.ClientID
		rt.clientSecret = cfg.Auth.ClientSecret
		rt.redirectURI = cfg.Auth.RedirectURI
		rt.scopes = cfg.Auth.Scopes
		rt.authServerMetadataURL = cfg.Auth.AuthServerMetadataURL
		rt.protectedResourceMetadataURL = cfg.Auth.ProtectedResourceMetadataURL
		if hasRec {
			if rt.clientID == "" {
				rt.clientID = rec.ClientID
			}
			if rt.clientSecret == "" {
				rt.clientSecret = rec.ClientSecret
			}
			if rt.redirectURI == "" {
				rt.redirectURI = rec.RedirectURI
			}
			if rt.protectedResourceMetadataURL == "" {
				rt.protectedResourceMetadataURL = rec.ResourceMetadataURL
			}
		}
		return rt
	}

	if hasRec {
		rt.clientID = rec.ClientID
		rt.clientSecret = rec.ClientSecret
		rt.redirectURI = rec.RedirectURI
		rt.protectedResourceMetadataURL = rec.ResourceMetadataURL
	}
	return rt
}

func AttachAuthRuntime(name string, cfg ServerConfig, store *AuthStore) ServerConfig {
	cfg.authRuntime = buildAuthRuntime(store, name, cfg)
	return cfg
}
