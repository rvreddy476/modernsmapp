package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// HIGH 1 (2026-10-30): a reel draft cannot be written in a shape CreatePost
// refuses. The service refuses before any store is touched (there is none
// here), and the routes answer with the create route's codes.
func TestReelDraftWritesAnswerTheCreateCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	New(service.NewForHandlerTests(service.HandlerTestDeps{}), nil).RegisterDraftRoutes(router)
	draft := "/v1/reels/drafts/" + uuid.NewString()
	long := strings.Repeat("a", service.MaxPostTextRunes+1)

	for _, c := range []struct {
		method, path, body, code string
	}{
		{http.MethodPatch, draft, `{"remix_setting":"x"}`, "INVALID_REMIX_SETTING"},
		{http.MethodPatch, draft, `{"comment_access":"strangers"}`, "INVALID_COMMENT_ACCESS"},
		{http.MethodPatch, draft, `{"visibility":"world"}`, "INVALID_VISIBILITY"},
		{http.MethodPatch, draft, `{"caption":"` + long + `"}`, "TEXT_TOO_LONG"},
		{http.MethodPatch, draft, `{"schedule_at":"1970-01-01T00:00:00Z"}`, "INVALID_PUBLISH_AT"},
		{http.MethodPost, "/v1/reels/drafts", `{"visibility":"world"}`, "INVALID_VISIBILITY"},
		{http.MethodPost, "/v1/reels/drafts", `{"caption":"` + long + `"}`, "TEXT_TOO_LONG"},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", uuid.NewString())
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"code":"`+c.code+`"`) {
			t.Errorf("%s %s %.60s: %d %.200s, want 400 %s", c.method, c.path, c.body, w.Code, w.Body.String(), c.code)
		}
	}
}
