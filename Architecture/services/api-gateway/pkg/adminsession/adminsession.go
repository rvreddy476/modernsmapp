// Package adminsession holds the edge rules for the admin console's own
// session (identity commit 1ad9187e).
//
// The console signs in on its own host and receives host-only, SameSite=Strict
// cookies: admin_access_token and admin_refresh_token (HttpOnly) and
// admin_csrf_token (readable). Its access tokens carry `sk: "admin"`; consumer
// tokens do not. A consumer session that completed TOTP carries admin_mfa=true
// too, so admin_mfa alone cannot tell the two apart — the session kind can.
//
// Rules, per path family:
//
//   - /v1/admin (after the same normalisation the internal-route refusal
//     uses): the token comes ONLY from the admin_access_token cookie. A Bearer
//     header, the consumer access_token cookie and query tokens are ignored.
//     No admin cookie is 401 ADMIN_SESSION_REQUIRED; a verified token whose
//     sk is not "admin" is 401 WRONG_SESSION. Writes (anything but GET, HEAD,
//     OPTIONS) also need X-CSRF-Token equal to the admin_csrf_token cookie,
//     compared in constant time, or 403 CSRF_FAILED.
//   - every other path: token sources are unchanged, and a token with
//     sk="admin" is refused with 401 WRONG_SESSION, so an admin session can
//     never stand in for a consumer one. /v1/auth/admin-session is such a
//     path: identity reads its own cookies there and the console sends no
//     Bearer header, so nothing about it changes.
//
// This lives in a tracked package because new files under cmd/server/ are
// git-ignored by the root `server` rule.
package adminsession

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/atpost/api-gateway/pkg/internalroutes"
)

const (
	AccessCookie = "admin_access_token"
	CSRFCookie   = "admin_csrf_token"
	CSRFHeader   = "X-CSRF-Token"

	// KindAdmin is the `sk` claim value on an admin console access token.
	KindAdmin = "admin"

	CodeSessionRequired = "ADMIN_SESSION_REQUIRED"
	CodeWrongSession    = "WRONG_SESSION"
	CodeCSRFFailed      = "CSRF_FAILED"
)

// IsAdminPath reports whether one path interpretation is /v1/admin or below.
// Segments compare case-insensitively with `;params` dropped, matching how the
// internal-route check reads a segment.
func IsAdminPath(p string) bool {
	segs := strings.Split(p, "/")
	if len(segs) < 3 || segs[0] != "" {
		return false
	}
	return segment(segs[1]) == "v1" && segment(segs[2]) == "admin"
}

func segment(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = s[:i]
	}
	return s
}

// IsAdminRequest reports whether ANY interpretation of the request path is an
// admin path. Erring towards "admin" is the safe direction: it can only make
// a request need the admin session, never excuse it from one.
func IsAdminRequest(r *http.Request) bool {
	return internalroutes.AnyRequestInterpretation(r, IsAdminPath)
}

// AccessToken returns the admin_access_token cookie value, or "".
func AccessToken(r *http.Request) string {
	c, err := r.Cookie(AccessCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// NeedsCSRF reports whether the method is a write that must carry the token.
func NeedsCSRF(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// CSRFValid reports whether X-CSRF-Token equals the admin_csrf_token cookie.
// Both must be present and non-empty; the comparison is constant time.
func CSRFValid(r *http.Request) bool {
	c, err := r.Cookie(CSRFCookie)
	if err != nil || c.Value == "" {
		return false
	}
	header := r.Header.Get(CSRFHeader)
	if header == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(header), []byte(c.Value)) == 1
}

// Refuse writes the edge error envelope.
func Refuse(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + message + `"}}`))
}
