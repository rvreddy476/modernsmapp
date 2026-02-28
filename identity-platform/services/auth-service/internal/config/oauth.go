package config

import "os"

// OAuthProviderConfig holds credentials for a single OAuth provider.
type OAuthProviderConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
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
