package config

import "testing"

// The session cookies (access/refresh) carry Secure in production whatever
// COOKIE_SECURE says, as the admin cookies already do: a deploy that forgot
// the variable must not send session tokens over plain HTTP.
func TestSessionCookiesAreSecureInProduction(t *testing.T) {
	for _, key := range []string{"APP_ENV", "ENVIRONMENT", "ENV"} {
		t.Setenv(key, "")
	}
	t.Setenv("APP_ENV", "production")
	t.Setenv("COOKIE_SECURE", "false")

	if cfg := Load(); !cfg.CookieSecure {
		t.Fatal("production with COOKIE_SECURE=false loaded CookieSecure=false; session cookies would be sent over HTTP")
	}
}

func TestSessionCookieSecureFollowsTheFlagOutsideProduction(t *testing.T) {
	for _, key := range []string{"APP_ENV", "ENVIRONMENT", "ENV"} {
		t.Setenv(key, "")
	}
	t.Setenv("APP_ENV", "development")

	t.Setenv("COOKIE_SECURE", "false")
	if Load().CookieSecure {
		t.Fatal("development with COOKIE_SECURE=false must allow plain-HTTP cookies for local runs")
	}
	t.Setenv("COOKIE_SECURE", "true")
	if !Load().CookieSecure {
		t.Fatal("COOKIE_SECURE=true must set Secure")
	}
}
