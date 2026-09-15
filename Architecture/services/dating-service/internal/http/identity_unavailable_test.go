package http

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/gin-gonic/gin"
)

// TestRespondServiceError_IdentityUnavailableIs503: a profile save identity
// could not verify surfaces as 503 IDENTITY_UNAVAILABLE, not a 500 with the
// wrapped cause.
func TestRespondServiceError_IdentityUnavailableIs503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/dating/profile", nil)

	cause := fmt.Errorf("%w: %v", service.ErrIdentityUnavailable, service.ErrIdentityTransient)
	respondServiceError(c, cause, http.StatusInternalServerError, "UPSERT_FAILED")

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, `"IDENTITY_UNAVAILABLE"`) || strings.Contains(body, "transient") {
		t.Fatalf("body = %s, want code IDENTITY_UNAVAILABLE and no internal cause", body)
	}
}
