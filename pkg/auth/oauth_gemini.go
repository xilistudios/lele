package auth

// GeminiOAuthConfig returns the OAuth preset for a Gemini Code Assist login,
// i.e. the flow Google's official Gemini CLI (`@google/gemini-cli`) runs. It is
// a direct port of jcode's Gemini OAuth client (jcode-base `auth/gemini.rs`,
// summarized in .lele/specs/jcode-gemini.md §1/§2).
//
// The preset is registered under the canonical key "gemini" (oauth_registry.go),
// with "gemini-cli" — the spelling the official CLI uses — as an accepted alias,
// so `lele auth login --provider gemini` reaches this flow and pkg/providers
// selects the Code Assist transport once providers.gemini.auth_method is "oauth".
//
// Port 0 makes LoginBrowser ask the OS for an ephemeral loopback port
// (bindCallbackListener) and advertise the port it actually got
// (redirectURIForListener); RedirectURI must therefore stay EMPTY — a hardcoded
// value would not match the bound port and Google rejects the exchange whose
// redirect_uri differs from the one in the authorize URL. jcode binds port 0
// the same way (gemini.rs `bind_callback_listener(0)`); unlike its Antigravity
// sibling, Gemini never hardcodes a port.
//
// StateIsVerifier stays at its zero value because jcode's Gemini flow generates
// a RANDOM `state` (16 random bytes, hex encoded; oauth.rs `generate_state`)
// for CSRF protection and echoes it back through the callback path — it is NOT
// the PKCE verifier. Claude is the opposite (StateIsVerifier: true), so leaving
// this unset here is the correct behavior, not an omission.
//
// The refresh omits the `scope` parameter (OmitRefreshScope): per RFC 6749 §6
// the server then applies the scope originally granted, which is exactly what
// jcode's Google refresh does (google_oauth.rs). Sending the long Scopes list
// on refresh would widen the request for no benefit. RefreshScopes stays empty
// for the same reason.
//
// RequireRefreshToken is true because jcode's Gemini exchange treats a missing
// `refresh_token` as a hard error ("No refresh token received…", gemini.rs
// exchange_authorization_code): the access token expires within the hour, so a
// credential without a refresh token "logs in" successfully and is unusable
// forever after. The ExtraAuthParams below (`access_type=offline` and
// `prompt=consent`) are what make Google actually hand one out — dropping
// either one can produce a token response with no refresh_token.
//
// The embedded client secret is NOT a leak: it is a public "Desktop app"
// credential published by Google's official Gemini CLI (gemini.rs:14-16, which
// carries an explicit gitleaks exemption), and Desktop-app secrets are safe to
// ship by design. lele still obfuscates it with decodeBase64, the same
// mechanism the google-antigravity preset uses, so it never appears verbatim in
// the source.
//
// Deliberately absent: `Originator` (jcode's Gemini builder has no such param),
// any device-flow endpoint (Gemini has no device flow), and `HTTPClient` (nil
// means http.DefaultClient, which is what jcode's shared_http_client() gives
// its OAuth calls). RequireInferenceScope stays at zero: jcode never validates
// Gemini scopes for Code Assist (contrast Claude, where the gate is real).
func GeminiOAuthConfig() OAuthProviderConfig {
	// Google's official Gemini CLI desktop-app credentials. Public by design —
	// see the doc comment above.
	clientID := decodeBase64(
		"NjgxMjU1ODA5Mzk1LW9vOGZ0Mm9wcmRybnA5ZTNhcWY2YXYzaG1kaWIxMzVqLmFwcHMuZ29vZ2xldXNlcmNvbnRlbnQuY29t",
	)
	clientSecret := decodeBase64("R09DU1BYLTR1SGdNUG0tMW83U2stZ2VWNkN1NWNsWEZzeGw=")

	return OAuthProviderConfig{
		Name: "gemini",
		// DisplayName labels user-facing gate messages, e.g. the
		// RequireRefreshToken failure text. Without it the message would name
		// Claude, since that was the only provider using the gate.
		DisplayName:  "Gemini",
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Issuer:       "https://accounts.google.com/o/oauth2/v2",
		AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:     "https://oauth2.googleapis.com/token",
		// The three Code Assist scopes, in jcode's order, joined with single
		// spaces (the authorize builder URL-encodes them as one parameter).
		Scopes: "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile",
		// Ephemeral loopback port; the redirect is derived from the port the OS
		// actually assigned, so RedirectURI must stay empty.
		Port:         0,
		CallbackPath: "/oauth2callback",
		RedirectURI:  "",
		// Hosted redirect for the headless paste-code flow.
		ManualRedirectURI: "https://codeassist.google.com/authcode",
		ExtraAuthParams: map[string]string{
			// access_type=offline + prompt=consent are what make Google return
			// a refresh_token; without them the credential cannot be refreshed.
			"access_type": "offline",
			"prompt":      "consent",
		},
		// No Originator: jcode's Gemini authorize URL carries no originator
		// param (unlike OpenAI's).
		Originator: "",
		// Random CSRF state, not the PKCE verifier: jcode Gemini behavior.
		StateIsVerifier: false,
		// jcode never validates Gemini scopes, so the gate stays off.
		RequireInferenceScope: false,
		// A Gemini credential without a refresh token is unusable (jcode calls
		// it a hard error on the exchange).
		RequireRefreshToken: true,
		// Google refreshes with no scope parameter (RFC 6749 §6).
		RefreshScopes:    "",
		OmitRefreshScope: true,
		// Form-encoded token requests with client_id/client_secret in the body,
		// matching jcode's reqwest `.form(&…)` call.
		TokenRequestStyle: TokenRequestFormURLEncoded,
		AuthHeaderStyle:   TokenAuthBody,
		HTTPClient:        nil,
	}
}
