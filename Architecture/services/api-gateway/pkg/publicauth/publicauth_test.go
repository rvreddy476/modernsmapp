package publicauth

import "testing"

func TestPublicAuthPaths(t *testing.T) {
	for _, p := range []string{
		"/v1/auth/refresh",
		"/v1/auth/login",
		"/v1/auth/register",
		"/v1/auth/logout",
		"/v1/auth/2fa/verify",
		"/v1/auth/forgot-password",
		"/v1/auth/passkeys/login/finish",
		"/v1/auth/oauth/google",
		"/v1/auth/oauth/google/callback",
		"/v1/auth/oauth/complete-signup",
		"/v1/auth/admin-session/refresh",
	} {
		if !IsPublic(p) {
			t.Errorf("%s: want public", p)
		}
	}
}

func TestProtectedAuthPathsStayStrict(t *testing.T) {
	for _, p := range []string{
		"/v1/auth/me",
		"/v1/auth/me/capabilities",
		"/v1/auth/logout-all",
		"/v1/auth/sessions",
		"/v1/auth/step-up",
		"/v1/auth/2fa/setup",
		"/v1/auth/passkeys",
		"/v1/auth/passkeys/register/begin",
		"/v1/auth/admin-session",
		"/v1/auth/admin-session/step-up",
		"/v1/auth/admin/roles",
		"/v1/auth/oauth",          // the family root is not a route
		"/v1/auth/oauthx/google",  // near-prefix
		"/v1/auth/refresh-all",    // near-prefix
		"/v1/auth/refresh/",       // trailing slash is a different path
		"/v1/feed/home",           // other services
		"/v1/admin/auth/refresh",  // other families
		"/v1/users/v1/auth/login", // embedded
		"",
	} {
		if IsPublic(p) {
			t.Errorf("%s: must not be public", p)
		}
	}
}
