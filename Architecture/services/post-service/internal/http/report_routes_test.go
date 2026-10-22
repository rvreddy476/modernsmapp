package http

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

// Security sweep #23 (21 Oct 2026): post-service's own POST /v1/reports had
// no dedupe and no volume cap, and a comment report through it auto-hid the
// comment. The gateway sends /v1/reports to trust-safety-service; this copy
// is deleted so a repointed prefix cannot reach it. The moderator-only
// review routes stay.
func TestPostServiceHasNoReportSubmitRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(nil, nil).WithInternalKey(gateTestKey)
	h.RegisterRoutes(r)
	h.RegisterReportRoutes(r)
	review := false
	for _, ri := range r.Routes() {
		if ri.Method == http.MethodPost && ri.Path == "/v1/reports" {
			t.Fatalf("POST /v1/reports is still registered (%s)", ri.Handler)
		}
		if ri.Method == http.MethodGet && ri.Path == "/v1/admin/reports" {
			review = true
		}
	}
	if !review {
		t.Fatal("GET /v1/admin/reports is gone; only the submit route was meant to go")
	}
	if got := serveGate(r, http.MethodPost, "/v1/reports", `{"target_type":"comment","target_id":"x","reason":"spam"}`, callerUser).Code; got != http.StatusNotFound {
		t.Fatalf("POST /v1/reports: status=%d, want 404", got)
	}
}
