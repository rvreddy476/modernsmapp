package config

import (
	"os"
	"strings"
)

// OAuthProviderConfig holds credentials for a single OAuth provider.
type OAuthProviderConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	// IDTokenAudiences are the client ids (besides ClientID) an ID token may
	// be issued to on the mobile token route, e.g. the Android installed-app
	// client. Google only; empty plus an empty ClientID closes the route.
	IDTokenAudiences []string
}

// OAuthConfig holds OAuth settings for all supported providers.
type OAuthConfig struct {
	Google OAuthProviderConfig
	GitHub OAuthProviderConfig
	Apple  OAuthProviderConfig
}

// LoadOAuth reads OAuth configuration from environment variables.
func LoadOAuth() *OAuthConfig {
	return &OAuthConfig{
		Google: OAuthProviderConfig{
			ClientID:     os.Getenv("OAUTH_GOOGLE_CLIENT_ID"),
			ClientSecret: os.Getenv("OAUTH_GOOGLE_CLIENT_SECRET"),
			RedirectURL:  getEnv("OAUTH_GOOGLE_REDIRECT_URL", "http://localhost:8081/v1/auth/oauth/google/callback"),
			// Comma-separated. The Android app's client id goes here: its
			// ID tokens carry aud = that id, not the web client's.
			IDTokenAudiences: splitList(os.Getenv("OAUTH_GOOGLE_ID_TOKEN_AUDIENCES")),
		},
		GitHub: OAuthProviderConfig{
			ClientID:     os.Getenv("OAUTH_GITHUB_CLIENT_ID"),
			ClientSecret: os.Getenv("OAUTH_GITHUB_CLIENT_SECRET"),
			RedirectURL:  getEnv("OAUTH_GITHUB_REDIRECT_URL", "http://localhost:8081/v1/auth/oauth/github/callback"),
		},
		Apple: OAuthProviderConfig{
			ClientID:     os.Getenv("OAUTH_APPLE_CLIENT_ID"),
			ClientSecret: os.Getenv("OAUTH_APPLE_CLIENT_SECRET"),
			RedirectURL:  getEnv("OAUTH_APPLE_REDIRECT_URL", "http://localhost:8081/v1/auth/oauth/apple/callback"),
		},
	}
}

// splitList splits a comma-separated env value, dropping blanks.
func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
