// Package publicauth names the auth-service routes that need no session.
//
// Audit H1 (gateway half): the edge answered 401 to ANY credential that failed
// verification, before routing. On /v1/auth/refresh that is backwards: the
// request exists to replace an expired access token, yet the expired
// access_token cookie riding along with it got the refresh refused, and the
// Android client then signed the user out. The same 401 blocked login and
// refresh for a browser holding a cookie the gateway could no longer verify
// (for example after an RS256 key change), and a revoked session's cookie
// blocked signing in again until it expired.
//
// On the paths below a bad, expired, revoked or wrong-kind credential is
// therefore treated as no credential at all: the request goes through
// anonymously and auth-service decides on the body or its own cookies. That
// grants nothing a caller could not already get by sending no token. Every
// other path keeps the strict 401.
//
// Matching is exact (or on a segment boundary for the OAuth family), so a
// sibling such as /v1/auth/refresh-all or /v1/auth/me is never public.
//
// Lives under pkg/ because a new file under cmd/server/ is git-ignored.
package publicauth

import "strings"

// Paths are auth-service's public routes (identity-platform auth-service
// internal/http handler.go RegisterRoutes and RegisterDocsRoutes,
// admin_login.go RegisterAdminSessionRoutes, webauthn_routes_on.go). Keep in
// step with those: a route missing here still works, it just keeps the
// strict 401 for a stale credential.
var Paths = []string{
	"/v1/auth/partner/phone/request",
	"/v1/auth/partner/phone/verify",
	"/v1/auth/request-otp",
	"/v1/auth/verify-otp",
	"/v1/auth/register",
	"/v1/auth/login",
	"/v1/auth/refresh",
	"/v1/auth/logout",
	"/v1/auth/health",
	"/v1/auth/2fa/verify",
	"/v1/auth/anomaly/verify-email",
	"/v1/auth/anomaly/verify-2fa",
	"/v1/auth/.well-known/jwks.json",
	"/v1/auth/forgot-password",
	"/v1/auth/reset-password",
	"/v1/auth/verify-email",
	"/v1/auth/resend-verification",
	"/v1/auth/verify-phone",
	"/v1/auth/passkeys/login/begin",
	"/v1/auth/passkeys/login/finish",
	"/v1/auth/openapi.json",
	"/v1/auth/docs",
	// The admin console's own sign-in: identity reads the admin cookies
	// itself; the gateway demands nothing here.
	"/v1/auth/admin-session/login",
	"/v1/auth/admin-session/verify-2fa",
	"/v1/auth/admin-session/refresh",
	"/v1/auth/admin-session/logout",
}

// PrefixFamilies are public route families: /v1/auth/oauth/:provider,
// .../callback, .../token, complete-signup and verify-signup are all public.
var PrefixFamilies = []string{
	"/v1/auth/oauth",
}

// IsPublic reports whether path is a public auth route.
func IsPublic(path string) bool {
	for _, p := range Paths {
		if path == p {
			return true
		}
	}
	for _, p := range PrefixFamilies {
		if strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
