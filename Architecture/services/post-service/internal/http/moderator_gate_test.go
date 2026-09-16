package http

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const gateTestKey = "gate-test-internal-key"

// newGateRouter mirrors main.go's registration order with a nil service, so any
// request that gets past a gate panics into gin.Recovery (500). A 500 therefore
// proves the gate admitted the caller; 401/403 prove it refused before the
// service was touched.
func newGateRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	h := New(nil, nil).WithInternalKey(gateTestKey)
	h.RegisterRoutes(r)
	h.RegisterReelDiscoveryRoutes(r)
	h.RegisterReportRoutes(r)
	return r
}

type gateCaller struct {
	name   string
	userID string
	scopes string
	key    string
}

var (
	callerAnon      = gateCaller{name: "anonymous", key: gateTestKey}
	callerUser      = gateCaller{name: "user", userID: uuid.NewString(), scopes: "user", key: gateTestKey}
	callerModerator = gateCaller{name: "moderator", userID: uuid.NewString(), scopes: "user moderator", key: gateTestKey}
	callerAdmin     = gateCaller{name: "admin", userID: uuid.NewString(), scopes: "admin", key: gateTestKey}
	callerScopeOnly = gateCaller{name: "scope-without-user", scopes: "moderator", key: gateTestKey}
)

func serveGate(r *gin.Engine, method, path, body string, who gateCaller) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if who.key != "" {
		req.Header.Set("X-Internal-Service-Key", who.key)
	}
	if who.userID != "" {
		req.Header.Set("X-User-Id", who.userID)
	}
	if who.scopes != "" {
		req.Header.Set("X-Scopes", who.scopes)
	}
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	return res
}

func TestModeratorRoutesMatrix(t *testing.T) {
	r := newGateRouter()
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/v1/reels/moderation/flagged", ""},
		{http.MethodGet, "/v1/reels/" + uuid.NewString() + "/moderation", ""},
		{http.MethodGet, "/v1/admin/reports", ""},
		{http.MethodPatch, "/v1/admin/reports/" + uuid.NewString(), `{"status":"reviewed"}`},
		{http.MethodGet, "/v1/admin/comments/moderation", ""},
		{http.MethodPatch, "/v1/admin/comments/" + uuid.NewString() + "/moderation", `{"status":"hidden"}`},
	}
	for _, rt := range routes {
		rt := rt
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			if got := serveGate(r, rt.method, rt.path, rt.body, callerAnon).Code; got != http.StatusUnauthorized {
				t.Errorf("anonymous: status=%d want 401", got)
			}
			if got := serveGate(r, rt.method, rt.path, rt.body, callerScopeOnly).Code; got != http.StatusUnauthorized {
				t.Errorf("scope without user: status=%d want 401", got)
			}
			if got := serveGate(r, rt.method, rt.path, rt.body, callerUser).Code; got != http.StatusForbidden {
				t.Errorf("user: status=%d want 403", got)
			}
			for _, who := range []gateCaller{callerModerator, callerAdmin} {
				got := serveGate(r, rt.method, rt.path, rt.body, who).Code
				if got == http.StatusUnauthorized || got == http.StatusForbidden {
					t.Errorf("%s: status=%d, gate refused a moderator", who.name, got)
				}
			}
		})
	}
}

func TestReviewStateRoutesActorRules(t *testing.T) {
	r := newGateRouter()
	postID := uuid.NewString()
	routes := []struct{ path, body string }{
		{"/v1/posts/internal/review-status", `{"post_id":"` + postID + `","status":"approved"}`},
		{"/v1/posts/internal/visibility", `{"post_id":"` + postID + `","visibility":"public"}`},
	}
	for _, rt := range routes {
		rt := rt
		t.Run(rt.path, func(t *testing.T) {
			if got := serveGate(r, http.MethodPost, rt.path, rt.body, callerUser).Code; got != http.StatusForbidden {
				t.Errorf("user: status=%d want 403", got)
			}
			if got := serveGate(r, http.MethodPost, rt.path, rt.body, callerScopeOnly).Code; got != http.StatusUnauthorized {
				t.Errorf("scope without user: status=%d want 401", got)
			}
			bad := gateCaller{name: "wrong key", key: "not-the-key"}
			if got := serveGate(r, http.MethodPost, rt.path, rt.body, bad).Code; got != http.StatusUnauthorized {
				t.Errorf("wrong key: status=%d want 401", got)
			}
			for _, who := range []gateCaller{callerModerator, callerAnon /* key only = service */} {
				got := serveGate(r, http.MethodPost, rt.path, rt.body, who).Code
				if got == http.StatusUnauthorized || got == http.StatusForbidden {
					t.Errorf("%s: status=%d, legitimate caller refused", who.name, got)
				}
			}
		})
	}
}

// Without a configured internal key there is no service credential to check,
// so a user-less call must be refused rather than recorded as a service.
func TestReviewStateRoutesRefuseServiceWithoutConfiguredKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil, nil).RegisterRoutes(r)
	body := `{"post_id":"` + uuid.NewString() + `","status":"approved"}`
	for _, path := range []string{"/v1/posts/internal/review-status", "/v1/posts/internal/visibility"} {
		if got := serveGate(r, http.MethodPost, path, body, gateCaller{}).Code; got != http.StatusUnauthorized {
			t.Errorf("%s: status=%d want 401", path, got)
		}
	}
}
