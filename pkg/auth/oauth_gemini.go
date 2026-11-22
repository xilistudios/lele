package auth

func GeminiOAuthConfig() OAuthProviderConfig {
	clientID := decodeBase64(
		"NjgxMjU1ODA5Mzk1LW9vOGZ0Mm9wcmRybnA5ZTNhcWY2YXYzaG1kaWIxMzVqLmFwcHMuZ29vZ2xldXNlcmNvbnRlbnQuY29t",
	)
	clientSecret := decodeBase64("R09DU1BYLTR1SGdNUG0tMW83U2stZ2VWNkN1NWNsWEZzeGw=")

	return OAuthProviderConfig{
		Name:              "gemini",
		DisplayName:       "Gemini",
		ClientID:          clientID,
		ClientSecret:      clientSecret,
		Issuer:            "https://accounts.google.com/o/oauth2/v2",
		AuthorizeURL:      "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:          "https://oauth2.googleapis.com/token",
		Scopes:            "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile",
		Port:              0,
		CallbackPath:      "/oauth2callback",
		RedirectURI:       "",
		ManualRedirectURI: "https://codeassist.google.com/authcode",
		ExtraAuthParams: map[string]string{
			"access_type": "offline",
			"prompt":      "consent",
		},
		Originator:            "",
		StateIsVerifier:       false,
		RequireInferenceScope: false,
		RequireRefreshToken:   true,
		RefreshScopes:         "",
		OmitRefreshScope:      true,
		TokenRequestStyle:     TokenRequestFormURLEncoded,
		AuthHeaderStyle:       TokenAuthBody,
		HTTPClient:            nil,
	}
}
