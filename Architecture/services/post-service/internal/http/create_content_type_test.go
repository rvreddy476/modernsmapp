package http

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// POST /v1/posts with a content_type this service does not create is a 400
// INVALID_CONTENT_TYPE, not the 500 INTERNAL_ERROR it used to be. The
// handler has no service behind it: the refusal must come before any
// lookup or write.
func TestCreatePostUnknownContentTypeIs400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, ct := range []string{"text", "story", "VIDEO"} {
		t.Run(ct, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			body := fmt.Sprintf(`{"text":"hello","visibility":"public","content_type":%q}`, ct)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/posts", strings.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Request.Header.Set("X-User-Id", uuid.NewString())
			c.Request.Header.Set("Idempotency-Key", uuid.NewString())

			(&Handler{}).CreatePost(c)

			if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "INVALID_CONTENT_TYPE" {
				t.Fatalf("content_type %q: got %d %s, want 400 INVALID_CONTENT_TYPE", ct, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "must be post, poll, flick, or long_video") {
				t.Fatalf("message does not name the accepted types: %s", rec.Body.String())
			}
		})
	}
}

// The service's own refusal (any caller that skips the handler check) maps
// to the same 400 through the shared create-guard writer.
func TestCreateGuardMapsInvalidContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/posts", nil)
	err := fmt.Errorf("%w %q: must be post, poll, flick, or long_video", service.ErrInvalidContentType, "text")
	if !writeCreateGuardError(c, err) {
		t.Fatal("ErrInvalidContentType not handled by writeCreateGuardError")
	}
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "INVALID_CONTENT_TYPE" {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}
