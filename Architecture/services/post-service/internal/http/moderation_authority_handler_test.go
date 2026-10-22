package http

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Security sweep #12 (21 Oct 2026): the legacy user-reachable
// POST /v1/posts/:postId/moderation granted moderator rights from the
// X-Scopes header alone. It is retired: even a request carrying every
// moderator scope and the internal key finds no route. The admin console's
// decision write is the token-only /v1/posts/internal/admin family.
func TestLegacyModerationRouteIsRetired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil, nil).WithInternalKey(adminTestKey).RegisterRoutes(r) // nil service: reaching a handler would panic.
	for _, ri := range r.Routes() {
		if ri.Method == http.MethodPost && ri.Path == "/v1/posts/:postId/moderation" {
			t.Fatalf("POST %s is still registered (%s)", ri.Path, ri.Handler)
		}
	}
	body := []byte(`{"decision_id":"` + uuid.NewString() + `","action":"reject","reason":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/posts/"+uuid.NewString()+"/moderation", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", adminTestKey)
	req.Header.Set("X-User-Id", uuid.NewString())
	req.Header.Set("X-Scopes", "moderator admin superadmin")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s, want 404", res.Code, res.Body.String())
	}
}

// The internal moderation command refuses before the service without a
// trust-safety token (the full ladder is in internal_write_tokens_test.go).
func TestInternalModerationRejectsMissingTokenBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil, nil).RegisterRoutes(r)
	req := httptest.NewRequest(http.MethodPost, "/v1/posts/internal/moderation", bytes.NewReader([]byte(`{"claims":{},"capability":"bad"}`)))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}
