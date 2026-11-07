package internalroutes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// wantStamp is the whole decision, prefix by prefix, written out again so a
// change to StampPolicy is a change to this table too. `true` means the
// upstream's user routes answer 401 without the key today (wiring re-read in
// each service's cmd/server, 7 Nov 2026); each is debt until that upstream
// takes a distinct gateway credential instead.
var wantStamp = map[string]bool{
	// Do not need the key on user routes.
	"/v1/auth":          false,
	"/v1/audio":         false,
	"/v1/audio-library": false,
	"/v1/media":         false,
	"/v1/subtitles":     false,
	"/v1/ws":            false,
	"/v1/calls":         false,
	"/v1/chat":          false,
	"/v1/wallet":        false,
	"/v1/billpay":       false,
	// Flipped 7 Nov 2026: the upstream never switches its key gate on.
	"/v1/admin/flags": false,
	"/v1/flags":       false,
	"/v1/onboarding":  false,
	"/v1/pages":       false,
	"/v1/users":       false,
	"/v1/suggestions": false,
	// Still gate every user route on the key.
	"/v1/profiles":                 true,
	"/v1/links":                    true,
	"/v1/users/me/settings":        true,
	"/v1/users/me/modules":         true,
	"/v1/users/me/region":          true,
	"/v1/users/me/keyword-filters": true,
	"/v1/reports":                  true,
	"/v1/appeals":                  true,
	"/v1/grievances":               true,
	"/v1/verification-requests":    true,
	"/v1/graph":                    true,
	"/v1/permissions":              true,
	"/v1/channels":                 true,
	"/v1/uploads":                  true,
	"/v1/videos":                   true,
	"/v1/reels":                    true,
	"/v1/stories":                  true,
	"/v1/saved":                    true,
	"/v1/hashtags":                 true,
	"/v1/comments":                 true,
	"/v1/playlists":                true,
	"/v1/video-series":             true,
	"/v1/series":                   true,
	"/v1/creators":                 true,
	"/v1/feedback":                 true,
	"/v1/posts":                    true,
	"/v1/crossposts":               true,
	"/v1/credits":                  true,
	"/v1/feed":                     true,
	"/v1/notifications":            true,
	"/v1/unread":                   true,
	"/v1/realtime":                 true,
	"/v1/discover":                 true,
	"/v1/search":                   true,
	"/v1/groups":                   true,
	"/v1/reviewer":                 true,
	"/v1/analytics":                true,
	"/v1/ai":                       true,
	"/v1/admin":                    true,
	"/v1/apps":                     true,
	"/v1/oauth":                    true,
	"/v1/monetization":             true,
	"/v1/livestream":               true,
	"/v1/memories":                 true,
	"/v1/broadcast-channels":       true,
	"/v1/communities":              true,
	"/v1/qa":                       true,
	"/v1/dating":                   true,
	"/v1/food":                     true,
	"/v1/rider":                    true,
	"/v1/partner":                  true,
	"/v1/doorstep":                 true,
	"/v1/commerce":                 true,
}

func TestStampPolicyMatchesTheReviewedTable(t *testing.T) {
	for prefix, want := range wantStamp {
		if _, ok := StampPolicy[prefix]; !ok {
			t.Errorf("%s: in the reviewed table but has no StampPolicy entry", prefix)
			continue
		}
		if got := ShouldStamp(prefix); got != want {
			t.Errorf("%s: ShouldStamp = %t, want %t", prefix, got, want)
		}
	}
	for prefix := range StampPolicy {
		if _, ok := wantStamp[prefix]; !ok {
			t.Errorf("%s: StampPolicy entry missing from the reviewed table; decide it here too", prefix)
		}
	}
}

// ApplyKey over every classified prefix: a client-forged key in any spelling
// never survives, and only the reviewed `true` prefixes carry the real one.
func TestApplyKeyOverEveryPrefix(t *testing.T) {
	for prefix, want := range wantStamp {
		req := httptest.NewRequest(http.MethodGet, prefix+"/x", nil)
		req.Header.Set(InternalKeyHeader, "forged")
		req.Header["x-internal-service-key"] = []string{"forged-lower"}
		req.Header["X-INTERNAL-SERVICE-KEY"] = []string{"forged-upper"}
		ApplyKey(req, ShouldStamp(prefix), "real")
		var seen []string
		for name, vals := range req.Header {
			if strings.EqualFold(name, InternalKeyHeader) {
				seen = append(seen, vals...)
			}
		}
		switch {
		case want && (len(seen) != 1 || seen[0] != "real"):
			t.Errorf("%s: upstream key values %v, want [real]", prefix, seen)
		case !want && len(seen) != 0:
			t.Errorf("%s: upstream still carries key values %v, want none", prefix, seen)
		}
	}
}

var serviceOnlyRefused = []string{
	"/v1/search/users/bulk-sync",
	"/v1/search/users/bulk-sync/",
	"/v1/search//users/bulk-sync",
	"/v1/search/USERS/Bulk-Sync",
	"/v1/search/users%2Fbulk-sync",
	"/v1/search/users%252Fbulk-sync",
	"/v1/search/x/../users/bulk-sync",
	"/v1/search/users/bulk-sync;x=1",
	"/v1/search/users/%62ulk-sync",
	"/v1/graph/connection-request/filter",
	"/v1/graph/connection-request/filter?x=1",
	"/v1/graph/Connection-Request/FILTER",
	"/v1/dating/moderation/scan",
	"/v1/dating/moderation%2Fscan",
	"/v1/dating/profile/11111111-1111-4111-8111-111111111111/preview",
	"/v1/dating/profile/x/preview/",
	"/v1/monetization/affiliate/links/11111111-1111-4111-8111-111111111111",
	"/v1/suggestions/batch",
	"/v1/suggestions/Batch",
}

var serviceOnlyAllowed = []string{
	"/v1/search/users",
	"/v1/search/users/bulk-syncs",
	"/v1/search?q=/v1/search/users/bulk-sync",
	"/v1/graph/connection-request/unfilter",
	"/v1/graph/connection-requests/filtered",
	"/v1/graph/connection-request",
	"/v1/dating/moderation",
	"/v1/dating/profile/me",
	"/v1/dating/profile",
	"/v1/monetization/affiliate/links",
	"/v1/monetization/affiliate/conversions",
	"/v1/monetization/affiliate/ABC123",
	"/v1/suggestions/people",
	"/v1/suggestions/batches",
}

func TestServiceOnlyRoutesAreRefusedOverTheWire(t *testing.T) {
	var reached []string
	srv := httptest.NewServer(Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = append(reached, r.RequestURI)
		w.WriteHeader(http.StatusOK)
	})))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	for _, p := range serviceOnlyRefused {
		if got := sendRaw(t, addr, p); got != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, got)
		}
	}
	if len(reached) != 0 {
		t.Fatalf("service-only routes reached the upstream: %v", reached)
	}
	for _, p := range serviceOnlyAllowed {
		if got := sendRaw(t, addr, p); got != http.StatusOK {
			t.Errorf("%s: status %d, want 200 (a user route)", p, got)
		}
	}
}

// Every listed pattern is itself refused, so an entry cannot be dead text.
func TestEveryServiceOnlyPatternIsRefused(t *testing.T) {
	for _, route := range ServiceOnlyRoutes {
		p := strings.ReplaceAll(route.Pattern, "*", "11111111-1111-4111-8111-111111111111")
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
			reached := false
			h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
			req := httptest.NewRequest(method, p, nil)
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)
			if res.Code != http.StatusNotFound || reached {
				t.Errorf("%s %s: status %d reached %t, want 404 and not reached", method, p, res.Code, reached)
			}
		}
		if route.Why == "" {
			t.Errorf("%s: no reason recorded", route.Pattern)
		}
	}
}
