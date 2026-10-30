package edgeheaders

import (
	"net/http"
	"strings"
)

// Audit L3: the reverse proxy forwarded the whole Cookie header, so every
// upstream (post, media, feed and the rest) received the 30-day refresh
// credential that browsers and the Android cookie jar send on every call.
// Only auth-service redeems it; anywhere else it is a credential that header
// logging or a compromised upstream can leak.

// AuthRoutePrefix is the one route prefix whose upstream (auth-service)
// redeems refresh cookies.
const AuthRoutePrefix = "/v1/auth"

// RefreshCookieNames are the refresh credentials auth-service sets: the
// consumer one and the admin console's.
var RefreshCookieNames = []string{"refresh_token", "admin_refresh_token"}

// StripRefreshCookies removes the refresh cookies from an outbound request
// unless routePrefix is auth-service's. Every other cookie is kept, in order.
func StripRefreshCookies(req *http.Request, routePrefix string) {
	if routePrefix == AuthRoutePrefix {
		return
	}
	lines := req.Header.Values("Cookie")
	if len(lines) == 0 {
		return
	}
	var kept []string
	changed := false
	for _, line := range lines {
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name := part
			if i := strings.IndexByte(part, '='); i >= 0 {
				name = strings.TrimSpace(part[:i])
			}
			if isRefreshCookie(name) {
				changed = true
				continue
			}
			kept = append(kept, part)
		}
	}
	if !changed {
		return
	}
	req.Header.Del("Cookie")
	if len(kept) > 0 {
		req.Header.Set("Cookie", strings.Join(kept, "; "))
	}
}

func isRefreshCookie(name string) bool {
	for _, n := range RefreshCookieNames {
		if name == n {
			return true
		}
	}
	return false
}
