package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func datingGrievanceRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// A nil service proves refusals happen before anything is looked up.
	New(nil).RegisterRoutes(r)
	return r
}

func validDatingGrievanceBody() string {
	return `{"report_id":"` + uuid.NewString() + `","reporter_id":"` + uuid.NewString() +
		`","target_id":"` + uuid.NewString() + `","reason":"harassment","details":"x","reported_at":"2026-09-15T10:00:00Z"}`
}

// The api-gateway injects the internal key on every proxied request, so a
// request carrying any gateway-set user identity header is a user, not a
// service, and must be refused before the grievance is created.
func TestDatingReportGrievanceRouteRefusesUserCaller(t *testing.T) {
	r := datingGrievanceRouter()
	for _, header := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"} {
		t.Run(header, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, DatingReportGrievancePath, strings.NewReader(validDatingGrievanceBody()))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Internal-Service-Key", "k")
			req.Header.Set(header, uuid.NewString())
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "USER_CALLER_REFUSED") {
				t.Fatalf("%s: status=%d body=%s, want 403 USER_CALLER_REFUSED", header, w.Code, w.Body.String())
			}
		})
	}
}

func TestDatingReportGrievanceRouteRejectsMalformedIDs(t *testing.T) {
	r := datingGrievanceRouter()
	req := httptest.NewRequest(http.MethodPost, DatingReportGrievancePath,
		strings.NewReader(`{"report_id":"nope","reporter_id":"`+uuid.NewString()+`","target_id":"`+uuid.NewString()+`","reason":"spam"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", w.Code, w.Body.String())
	}
}
