package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPublicReviewerRoutesDefaultClosedBeforeServiceCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(nil).RegisterRoutes(router)

	cases := []struct{ method, path string }{
		{http.MethodPost, "/v1/reviewer/opt-in"},
		{http.MethodGet, "/v1/reviewer/me"},
		{http.MethodGet, "/v1/reviewer/me/stats"},
		{http.MethodPost, "/v1/reviewer/verify-kyc"},
		{http.MethodPost, "/v1/reviewer/online"},
		{http.MethodGet, "/v1/reviewer/queue"},
		{http.MethodGet, "/v1/reviewer/assignments/next"},
		{http.MethodPost, "/v1/reviewer/assignments/11111111-1111-1111-1111-111111111111/heartbeat"},
		{http.MethodPost, "/v1/reviewer/assignments/11111111-1111-1111-1111-111111111111/decision"},
		{http.MethodGet, "/v1/reviewer/content/11111111-1111-1111-1111-111111111111/feedback"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s %s = %d, want 503", tc.method, tc.path, res.Code)
		}
		if got := res.Body.String(); !contains(got, "REVIEWER_PROGRAM_UNAVAILABLE") {
			t.Fatalf("%s %s body %q lacks stable error code", tc.method, tc.path, got)
		}
	}
}

func contains(s, want string) bool {
	for i := 0; i+len(want) <= len(s); i++ {
		if s[i:i+len(want)] == want {
			return true
		}
	}
	return false
}
