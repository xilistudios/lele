package mcp

import (
	"github.com/mark3labs/mcp-go/client/transport"
)

// This file is the T5 attach decision: the bridge between the persisted OAuth
// state (auth_store.go) and the mcp-go transport constructors. It exists as a
// separate, tiny file because the decision is the delicate part — see below.

// oauthRuntime is the resolved OAuth state of ONE server dial. Unexported and
// unserialized on purpose: it is plumbing between the manager and the dialer.
//
// It is carried on ServerConfig.authRuntime (json:"-") so buildTransport can
// read it without an extra parameter threaded through every Dial call, and so
// the config fingerprint ignores it by construction. It is set per resolve and
// never by the config reader.
type oauthRuntime struct {
	clientID                     string
	clientSecret                 string
	redirectURI                  string
	scopes                       []string
	authServerMetadataURL        string
	protectedResourceMetadataURL string
	tokenStore                   transport.TokenStore
}

// oauthConfig maps the resolved runtime onto mcp-go's transport.OAuthConfig.
// PKCEEnabled is always true: lele's flow is a loopback public-client flow, so
// PKCE is mandatory rather than optional.
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

// buildAuthRuntime resolves the OAuth state of ONE dial, or returns nil when
// the server is NOT opted in.
//
// The nil case is the load-bearing one. In mcp-go v1.1.1 an attached
// oauthHandler with no usable token makes SendRequest return
// OAuthAuthorizationRequiredError BEFORE sending anything, so attaching OAuth
// to a server that does not need it BREAKS a healthy server. The attach rule
// is therefore narrow, and a nil return means "build the plain transport".
//
// A server is opted in iff EITHER:
//
//	(a) its entry declares auth.type == "oauth", OR
//	(b) the store records that a real 401 was observed (AuthRequired true).
//
// Everything else returns nil: no store, no URL, a stdio entry, an auth block
// of type "none"/unset, or — crucially — a persisted ClientID ALONE. A client
// id with AuthRequired false is the residue of a cancelled or failed attempt;
// letting it attach OAuth would permanently break a server that works fine
// without a token.
//
// For (a) the AuthSpec is the starting point and every EMPTY field is filled
// from the persisted record (when one exists for the same URL): the client id,
// the client secret, the redirect URI and the protected-resource metadata URL.
// An explicit config value always wins over the stored one. For (b) the record
// supplies those fields outright.
func buildAuthRuntime(store *AuthStore, name string, cfg ServerConfig) *oauthRuntime {
	if store == nil || cfg.URL == "" || cfg.Command != "" {
		return nil
	}
	// ONE read of the record answers BOTH questions below. Asking the store
	// twice (AuthRequired, then Record) opens a TOCTOU window between the
	// calls: a concurrent write landing in it can make AuthRequired report
	// true while the second read reports no record, and the server would then
	// silently fall through to dynamic client registration against fields the
	// store did not describe. Reading once makes the two decisions describe
	// the same instant. The URL-binding rule is replicated exactly as
	// AuthRequired applies it: a record bound to another resource (or absent)
	// counts as absent.
	rec, hasRec := store.Record(name)
	if hasRec && rec.URL != cfg.URL {
		hasRec = false // a record bound to another resource is absent
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

	// Case (b): the server answered 401, so the record is the only source of
	// the registration details. tokenStore is already bound above.
	if hasRec {
		rt.clientID = rec.ClientID
		rt.clientSecret = rec.ClientSecret
		rt.redirectURI = rec.RedirectURI
		rt.protectedResourceMetadataURL = rec.ResourceMetadataURL
	}
	return rt
}

// AttachAuthRuntime returns cfg with its OAuth runtime resolved exactly as the
// manager resolves it before a dial. It is exported because the CLI probes a
// server without a Manager, and a probe that skipped this step would report
// "authentication required" for a server the user is already logged into.
func AttachAuthRuntime(name string, cfg ServerConfig, store *AuthStore) ServerConfig {
	cfg.authRuntime = buildAuthRuntime(store, name, cfg)
	return cfg
}
