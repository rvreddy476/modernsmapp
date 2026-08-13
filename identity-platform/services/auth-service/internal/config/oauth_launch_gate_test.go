package config

import "testing"

func TestOAuthNewAccountGateDefaultsClosed(t *testing.T) {
	t.Setenv("OAUTH_NEW_ACCOUNT_ENABLED", "")
	if Load().OAuthNewAccountEnabled {
		t.Fatal("new OAuth accounts must be disabled when the launch flag is absent")
	}
}

func TestOAuthNewAccountGateRequiresExplicitTrue(t *testing.T) {
	t.Setenv("OAUTH_NEW_ACCOUNT_ENABLED", "true")
	if !Load().OAuthNewAccountEnabled {
		t.Fatal("explicit true should enable the future consent-complete OAuth flow")
	}
}
