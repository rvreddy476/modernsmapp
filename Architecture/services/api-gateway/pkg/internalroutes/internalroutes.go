// Package internalroutes keeps service-only routes and the internal service
// key away from edge traffic.
//
// Admin console plan, Wave 0 lane A3. Until this package existed the gateway
// did two things that together made every `/v1/<svc>/internal/*` route a
// moderator surface:
//
//  1. requireAdminForInternalPaths admitted any `admin`, `moderator` or
//     `superadmin` scope to any path containing `/internal/`; and
//  2. injectInternalKeyMiddleware stamped X-Internal-Service-Key on EVERY
//     proxied request, anonymous ones included.
//
// So a moderator's browser could call wallet `/v1/wallet/internal/debit`
// for any user, commerce seller approval / KYC verify / COD settle,
// monetization `charge-and-credit`, search reindex, media delete and feed
// debug — all of which trust "the key is present" as proof of a service
// caller.
//
// Now:
//
//   - IsInternalPath refuses every edge request with an `internal` path
//     segment, whatever the caller's scopes, after decoding and cleaning the
//     path several ways so an encoded slash, `..`, `//` or case change cannot
//     smuggle one through. There is no moderator exception: admin
//     capabilities come back through admin-service under `/v1/admin`, which
//     calls product services in-cluster.
//   - The key is stamped only for upstreams whose USER-FACING routes still
//     authenticate the gateway by it (StampPolicy). Every route prefix must be
//     classified, or the gateway refuses to boot, so a new route is a
//     decision rather than a default.
//
// The inventory that justified an empty allowlist (2026-09-16): no Android,
// web (atpost-web), script, runbook, seeder or backend service reaches a
// `/v1/<svc>/internal/*` path through the gateway. In-cluster callers
// (admin-service → commerce, food → monetization, feed → analytics,
// bill-pay/rider → wallet, notification → dating, media → commerce/profiles)
// use direct service URLs and are unaffected. Two integration tests under
// Architecture/tools/integration still exercise the old admin gate and need
// updating (reviewer enqueue; payments prepaid expects 403, now 404).
//
// Lives under pkg/ because a new file under cmd/server/ is git-ignored.
package internalroutes

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
)

// maxDecodeRounds bounds repeated percent-decoding. Three rounds cover
// double and triple encoding (`%25252F`), which is more than any upstream in
// this stack decodes; the loop also stops as soon as a round changes nothing.
const maxDecodeRounds = 3

// IsInternalPath reports whether an edge request path reaches, or could be
// made to reach, a service-only route.
//
// A path is internal when ANY of its interpretations has a segment equal to
// `internal`, compared case-insensitively. The interpretations are the
// decoded path the gateway routes on, the raw escaped path, each repeatedly
// percent-decoded, with backslashes treated as separators, and each of those
// passed through path.Clean. Checking every form rather than choosing one is
// deliberate: the gateway, the reverse proxy and each upstream framework
// decode and clean differently, and the only safe answer is to refuse a path
// that is internal under any of them.
//
// Trade-off, accepted: a legitimate path whose parameter is literally
// `internal` (for example a username) is refused too.
func IsInternalPath(decodedPath, rawPath string) bool {
	for _, candidate := range interpretations(decodedPath, rawPath) {
		if hasInternalSegment(candidate) {
			return true
		}
	}
	return false
}

// IsInternalRequest applies IsInternalPath to a request's URL.
func IsInternalRequest(r *http.Request) bool {
	return AnyRequestInterpretation(r, hasInternalSegment)
}

// AnyRequestInterpretation reports whether match holds for ANY interpretation
// of the request path — the same set IsInternalRequest examines: the decoded
// and raw paths, the escaped path and the on-the-wire RequestURI, each
// repeatedly percent-decoded, with backslashes as separators, and cleaned.
// Other edge gates that must classify a path the way every upstream might
// (the admin session gate) use this so the normalisation lives in one place.
func AnyRequestInterpretation(r *http.Request, match func(string) bool) bool {
	if r == nil || r.URL == nil {
		return false
	}
	for _, pair := range [][2]string{
		{r.URL.Path, r.URL.RawPath},
		{"", r.URL.EscapedPath()},
		// RequestURI is what arrived on the wire, before Go parsed it. A
		// client-crafted form Go normalises away still gets looked at.
		{"", requestURIPath(r.RequestURI)},
	} {
		for _, candidate := range interpretations(pair[0], pair[1]) {
			if match(candidate) {
				return true
			}
		}
	}
	return false
}

func requestURIPath(uri string) string {
	if i := strings.IndexAny(uri, "?#"); i >= 0 {
		uri = uri[:i]
	}
	return uri
}

func interpretations(decodedPath, rawPath string) []string {
	var out []string
	add := func(p string) {
		if p == "" {
			return
		}
		p = strings.ReplaceAll(p, `\`, "/")
		out = append(out, p, path.Clean("/"+p))
	}
	for _, start := range []string{decodedPath, rawPath} {
		current := start
		add(current)
		for i := 0; i < maxDecodeRounds; i++ {
			next, err := url.PathUnescape(current)
			if err != nil {
				// One malformed escape makes the strict decoder give up on
				// the whole string; decode the well-formed ones anyway so
				// `%zz/%69nternal` is still examined.
				next = lenientUnescape(current)
			}
			if next == current {
				break
			}
			current = next
			add(current)
		}
	}
	return out
}

// lenientUnescape decodes every well-formed %XX and leaves the rest as is.
func lenientUnescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

func hasInternalSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		seg = strings.ToLower(strings.TrimSpace(seg))
		// `;` starts a path parameter in some frameworks (`internal;x=1`).
		if i := strings.IndexByte(seg, ';'); i >= 0 {
			seg = seg[:i]
		}
		if seg == "internal" {
			return true
		}
	}
	return false
}

// Refuse answers 404 for an internal path. 404 rather than 403: an edge client
// should not learn that a service-only route exists, the same reasoning as
// routepolicy's forbidden prefixes and the dormant-product gate.
func Refuse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"Not found"}}`))
}

// Middleware refuses every internal path before anything downstream sees it.
// It deliberately does not read scopes: no token opens these routes.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsInternalRequest(r) {
			Refuse(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// InternalKeyHeader is the credential services accept from an in-cluster
// caller.
const InternalKeyHeader = "X-Internal-Service-Key"

// StampPolicy lists, per gateway route prefix, whether the upstream still
// needs the internal key on user-facing traffic.
//
// true — the upstream installs RequireInternalKey (or an equivalent gateway
// trust check) on its WHOLE engine or public group, so without the key every
// user request would answer 401. These are kept working as they are; each is
// a candidate to move to a gateway-signed identity or service token later.
//
// false — the upstream's user routes do not read the key; it only guards the
// service's own `/internal` group (now refused at the edge) or is not
// checked at all. Stamping it there handed the credential to anyone who
// could reach a route on that host.
var StampPolicy = map[string]bool{
	// feature-flag-service: engine-wide RequireInternalKey.
	"/v1/admin/flags": true,
	"/v1/flags":       true,

	// identity-auth: the key guards only /v1/auth/internal/* (refused at the
	// edge and by routepolicy). Login, refresh, me, sessions do not read it.
	"/v1/auth": false,

	// identity-profile: engine-wide RequireInternalServiceKey.
	"/v1/profiles": true,

	// Architecture user-service: engine-wide RequireInternalKey.
	"/v1/onboarding": true,
	"/v1/pages":      true,
	"/v1/links":      true,
	"/v1/users":      true,

	// identity-user: engine-wide RequireInternalServiceKey.
	"/v1/users/me/settings": true,
	"/v1/users/me/modules":  true,
	"/v1/users/me/region":   true,

	// trust-safety-service: engine-wide RequireInternalKey.
	"/v1/users/me/keyword-filters": true,
	"/v1/reports":                  true,
	"/v1/appeals":                  true,
	"/v1/grievances":               true,
	"/v1/verification-requests":    true,

	// graph-service: engine-wide RequireInternalKey.
	"/v1/graph": true,

	// post-service: engine-wide RequireInternalKey.
	"/v1/channels":     true,
	"/v1/uploads":      true,
	"/v1/videos":       true,
	"/v1/reels":        true,
	"/v1/stories":      true,
	"/v1/saved":        true,
	"/v1/hashtags":     true,
	"/v1/comments":     true,
	"/v1/playlists":    true,
	"/v1/video-series": true,
	"/v1/series":       true,
	"/v1/creators":     true,
	"/v1/feedback":     true,
	"/v1/posts":        true,
	"/v1/crossposts":   true,

	// feed-service: engine-wide RequireInternalKey.
	"/v1/feed": true,

	// media-service: the key guards only /v1/media/internal/* and the
	// /internal/v1/media/* face and dating-photo routes, which have no edge
	// route. Uploads and delivery do not read it. Not stamping also closes
	// anonymous edge access to anything on media that trusts the key alone.
	// The captions group is registered at the root of media-service beside
	// /v1/media and reads the key nowhere, so it is classified the same way.
	"/v1/audio":     false,
	"/v1/media":     false,
	"/v1/subtitles": false,

	// notification-service: engine-wide RequireInternalKey (SSE included).
	"/v1/notifications": true,
	"/v1/unread":        true,
	"/v1/realtime":      true,

	// search-service: engine-wide RequireInternalKey.
	"/v1/discover": true,
	"/v1/search":   true,

	// group-service: engine-wide RequireInternalKey.
	"/v1/groups": true,

	// reviewer-service: engine-wide RequireInternalKey.
	"/v1/reviewer": true,

	// chat ws-gateway: authenticates the socket by JWT; never reads the key.
	"/v1/ws": false,

	// call-service: JWT middleware; the key is only sent outbound.
	"/v1/calls": false,

	// chat message-service: JWT middleware on /v1/chat; the key is read only
	// on /internal/v1/chat/*, which has no edge route.
	"/v1/chat": false,

	// analytics-service, ai-service: engine-wide RequireInternalKey.
	"/v1/analytics": true,
	"/v1/ai":        true,

	// admin-service: engine-wide RequireInternalKey.
	"/v1/admin": true,
	"/v1/apps":  true,
	"/v1/oauth": true,

	// monetization-service: engine-wide RequireInternalKey.
	"/v1/monetization": true,

	// suggestion-service: engine-wide RequireInternalKey.
	"/v1/suggestions": true,

	// live-service (engine-wide) and live-service-v2 (/v1/livestream group).
	"/v1/live":       true,
	"/v1/livestream": true,

	// memories, channel, community, qa, dating, food, rider: engine-wide or
	// whole-public-group RequireInternalKey.
	"/v1/memories":           true,
	"/v1/broadcast-channels": true,
	"/v1/communities":        true,
	"/v1/qa":                 true,
	"/v1/dating":             true,
	"/v1/food":               true,
	"/v1/rider":              true,

	// wallet-service: the key guards only /v1/wallet/internal/* (debit,
	// refund, balance for any user) — exactly the routes this lane closes.
	// User wallet routes do not read it.
	"/v1/wallet": false,

	// bill-pay-service: the key guards only /v1/billpay/internal/* (the Setu
	// webhook, mock-only and undeployed; it needs its own signed ingress like
	// the payments webhook before launch). User routes do not read it.
	"/v1/billpay": false,

	// commerce-service: RequireGatewayTrust on the whole engine.
	"/v1/commerce": true,
}

// ShouldStamp reports whether requests routed by prefix carry the key.
// An unclassified prefix never gets it.
func ShouldStamp(prefix string) bool {
	return StampPolicy[prefix]
}

// GuardStampPolicy fails when a route prefix has no stamping decision, so
// adding a route without deciding whether its upstream gets the internal key
// refuses boot instead of silently choosing.
func GuardStampPolicy(prefixes []string) error {
	var missing []string
	for _, p := range prefixes {
		if _, ok := StampPolicy[p]; !ok {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("internalroutes: route prefixes %s have no internal-key stamping decision; "+
		"add each to StampPolicy (true only if the upstream's user-facing routes require X-Internal-Service-Key)",
		strings.Join(missing, ", "))
}

// ApplyKey sets or removes the internal key on an outbound proxied request.
// It always removes first, so a copy that survived an earlier layer can never
// reach an upstream that should not see it.
func ApplyKey(req *http.Request, stamp bool, secret string) {
	req.Header.Del(InternalKeyHeader)
	for name := range req.Header {
		if http.CanonicalHeaderKey(name) == InternalKeyHeader {
			delete(req.Header, name)
		}
	}
	if stamp && secret != "" {
		req.Header.Set(InternalKeyHeader, secret)
	}
}
