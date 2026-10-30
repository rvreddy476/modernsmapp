package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/feed-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Audit L8: /v1/feed/mute never returns a raw store error, and refuses a
// malformed target with a fixed 400.

func TestMuteTarget_InvalidTargetIs400WithoutStoreText(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := New(service.New(nil, nil, nil))
	r := gin.New()
	r.POST("/v1/feed/mute", h.MuteTarget)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/feed/mute", strings.NewReader(`{"target_type":"user","target_id":"'; drop table"}`))
	req.Header.Set("X-User-Id", uuid.New().String())
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "INVALID_MUTE") {
		t.Fatalf("code=%d body=%s, want 400 INVALID_MUTE", w.Code, w.Body.String())
	}
}

func TestMuteError_StoreErrorIsGeneric500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/feed/mute", nil)
	muteError(c, errors.New(`ERROR: relation "feed_mutes" does not exist (SQLSTATE 42P01)`))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), "feed_mutes") || strings.Contains(w.Body.String(), "SQLSTATE") {
		t.Fatalf("raw database error reached the client: %s", w.Body.String())
	}
}
