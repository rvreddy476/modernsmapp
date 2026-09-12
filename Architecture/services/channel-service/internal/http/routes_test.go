package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/channel-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Communities invite-only pilot (2026-09-12): the HTTP surface contract —
// the whole-product kill switch, the static-before-parameterised route
// ordering, the fail-closed internal routes, and the wire codes.

func init() { gin.SetMode(gin.TestMode) }

// newTestEngine builds the real route table. The service is nil: every test
// here must be answered by middleware or the router, never by a handler
// that would need a database.
func newTestEngine(t *testing.T, h *Handler) *gin.Engine {
	t.Helper()
	r := gin.New()
	h.RegisterRoutes(r)
	return r
}

func routeSet(r *gin.Engine) map[string]bool {
	out := map[string]bool{}
	for _, ri := range r.Routes() {
		out[ri.Method+" "+ri.Path] = true
	}
	return out
}

// Task 6, level 1: COMMUNITIES_ENABLED=false must make every
// /v1/broadcast-channels route answer 404 with the gateway-style JSON body,
// including paths that match no route at all.
func TestCommunitiesDisabled_WholeProductAnswers404(t *testing.T) {
	h := New(nil).WithCommunitiesEnabled(false)
	r := newTestEngine(t, h)

	id := uuid.New().String()
	for _, tc := range []struct{ method, path string }{
		{"GET", "/v1/broadcast-channels/discover"},
		{"GET", "/v1/broadcast-channels/my"},
		{"POST", "/v1/broadcast-channels"},
		{"GET", "/v1/broadcast-channels/" + id},
		{"POST", "/v1/broadcast-channels/" + id + "/subscribe"},
		{"GET", "/v1/broadcast-channels/invites/ABCDEFGHIJ"},
		{"POST", "/v1/broadcast-channels/invites/ABCDEFGHIJ/join"},
		{"DELETE", "/v1/broadcast-channels/" + id + "/members/" + id},
		// A path that matches no route must be gated too, or the gate
		// leaks the shape of the product through 404-vs-405 differences.
		{"GET", "/v1/broadcast-channels/" + id + "/nonexistent-subpath"},
		{"PATCH", "/v1/broadcast-channels/" + id},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s %s: got %d, want 404", tc.method, tc.path, w.Code)
		}
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s %s: body is not the gateway JSON error shape: %q", tc.method, tc.path, w.Body.String())
		}
		if body.Error.Code != "NOT_FOUND" {
			t.Fatalf("%s %s: error code %q, want NOT_FOUND", tc.method, tc.path, body.Error.Code)
		}
	}
}

// The gate must not spill outside the communities prefix: health, metrics
// and the /internal moderation routes keep working while the product is
// off — a shutdown is exactly when you need to read reports and suspend
// rows. Exercised on the middleware directly so no handler (and no
// database) is involved.
func TestCommunitiesDisabled_LeavesOtherPrefixesAlone(t *testing.T) {
	r := gin.New()
	r.Use(communitiesDisabledGate())
	reached := func(c *gin.Context) { c.String(http.StatusOK, "reached") }
	r.GET("/healthz", reached)
	r.GET("/metrics", reached)
	r.GET("/internal/channel-reports", reached)
	r.POST("/internal/channels/:channelId/suspend", reached)
	// A prefix that merely starts with the same characters must not be
	// caught either.
	r.GET("/v1/broadcast-channels-export", reached)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/healthz"},
		{"GET", "/metrics"},
		{"GET", "/internal/channel-reports"},
		{"POST", "/internal/channels/" + uuid.New().String() + "/suspend"},
		{"GET", "/v1/broadcast-channels-export"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != http.StatusOK || w.Body.String() != "reached" {
			t.Fatalf("%s %s was gated by the communities kill switch: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}

	// And the prefix itself IS caught.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", communitiesPrefix, nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("the bare communities prefix must be gated: %d", w.Code)
	}
}

// When communities are enabled the gate is absent entirely.
func TestCommunitiesEnabled_NoGate(t *testing.T) {
	h := New(nil)
	r := newTestEngine(t, h)
	routes := routeSet(r)
	if !routes["GET /v1/broadcast-channels/discover"] {
		t.Fatal("discover route missing with communities enabled")
	}
	// An unmatched path under the prefix falls through to gin's own 404
	// body, not our gate's.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/broadcast-channels/x/nonexistent-subpath", nil))
	if strings.Contains(w.Body.String(), `"code":"NOT_FOUND"`) {
		t.Fatalf("the kill-switch gate is active with communities enabled: %s", w.Body.String())
	}
}

// Task 3: the static `invites` segment must resolve to the invite handlers,
// not be swallowed by /:channelId — the ordering trap
// /v1/channels/subscriptions hit in post-service.
func TestInviteRoutesRegisteredBeforeChannelIDRoutes(t *testing.T) {
	r := newTestEngine(t, New(nil))
	routes := routeSet(r)
	for _, want := range []string{
		"GET /v1/broadcast-channels/invites/:code",
		"POST /v1/broadcast-channels/invites/:code/join",
		"POST /v1/broadcast-channels/:channelId/invite-link",
		"GET /v1/broadcast-channels/:channelId/invite-link",
		"DELETE /v1/broadcast-channels/:channelId/invite-link",
		"DELETE /v1/broadcast-channels/:channelId/members/:userId",
		"POST /v1/broadcast-channels/:channelId/members/:userId/ban",
		"DELETE /v1/broadcast-channels/:channelId/members/:userId/ban",
	} {
		if !routes[want] {
			t.Fatalf("route %q is not registered", want)
		}
	}

	// The routing itself: a malformed code must reach PreviewInvite (which
	// answers INVITE_NOT_FOUND without touching the database), NOT
	// GetChannel (which would answer INVALID_ID on "invites").
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/broadcast-channels/invites/not-a-code", nil))
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "INVITE_NOT_FOUND") {
		t.Fatalf("GET /invites/<junk> was not routed to the invite preview: %d %s", w.Code, w.Body.String())
	}
}

// Task 5 + the fail-closed constraint: this service only authenticates
// anything when INTERNAL_SERVICE_KEY is set, so the moderation routes must
// NOT be registered without it — never served unauthenticated.
func TestInternalRoutesFailClosedWithoutKey(t *testing.T) {
	withoutKey := routeSet(newTestEngine(t, New(nil)))
	for path := range withoutKey {
		if strings.HasPrefix(path, "GET /internal") || strings.HasPrefix(path, "POST /internal") || strings.HasPrefix(path, "DELETE /internal") {
			t.Fatalf("internal route %q registered with no INTERNAL_SERVICE_KEY — it would be served unauthenticated", path)
		}
	}

	withKey := routeSet(newTestEngine(t, New(nil).WithInternalKey("secret")))
	for _, want := range []string{
		"GET /internal/channel-reports",
		"POST /internal/channel-reports/:reportId/review",
		"POST /internal/channels/:channelId/suspend",
		"DELETE /internal/channels/:channelId/suspend",
	} {
		if !withKey[want] {
			t.Fatalf("internal route %q missing when the key IS set", want)
		}
	}
}

// And with the key set, a caller without it is refused before any handler.
func TestInternalRoutesRejectWrongKey(t *testing.T) {
	r := newTestEngine(t, New(nil).WithInternalKey("secret"))
	for _, key := range []string{"", "wrong"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/internal/channel-reports", nil)
		if key != "" {
			req.Header.Set("X-Internal-Service-Key", key)
		}
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("internal key %q: got %d, want 401", key, w.Code)
		}
	}
}

// The pilot and moderation refusals must map to their documented codes.
// They are sentinel errors: the string-matching fallback in
// handleServiceError would turn most of them into a 500.
func TestPolicyErrorWireCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
		wire string
	}{
		{service.ErrPublicCommunityNotAllowed, http.StatusForbidden, "PUBLIC_COMMUNITY_NOT_ALLOWED"},
		{service.ErrCreatorNotAllowlisted, http.StatusForbidden, "COMMUNITY_CREATION_RESTRICTED"},
		{service.ErrInviteRequired, http.StatusForbidden, "INVITE_REQUIRED"},
		{service.ErrInviteNotFound, http.StatusNotFound, "INVITE_NOT_FOUND"},
		{service.ErrInviteNotLive, http.StatusGone, "INVITE_NOT_LIVE"},
		{service.ErrCannotModerateSelf, http.StatusUnprocessableEntity, "CANNOT_MODERATE_SELF"},
		{service.ErrCannotModerateOwner, http.StatusForbidden, "CANNOT_MODERATE_OWNER"},
		{service.ErrCannotModeratePeerAdmin, http.StatusForbidden, "CANNOT_MODERATE_ADMIN"},
		{service.ErrNotAMember, http.StatusNotFound, "NOT_FOUND"},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/x", nil)
		handleServiceError(c, tc.err)
		if w.Code != tc.code {
			t.Fatalf("%v: got %d, want %d", tc.err, w.Code, tc.code)
		}
		if !strings.Contains(w.Body.String(), fmt.Sprintf(`"%s"`, tc.wire)) {
			t.Fatalf("%v: body %s does not carry code %s", tc.err, w.Body.String(), tc.wire)
		}
	}
}

// A wrapped sentinel must still map — service code wraps with %w in places.
func TestPolicyErrorWireCodes_Wrapped(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/x", nil)
	handleServiceError(c, fmt.Errorf("join failed: %w", service.ErrInviteNotLive))
	if w.Code != http.StatusGone {
		t.Fatalf("wrapped ErrInviteNotLive: got %d, want 410", w.Code)
	}
}
