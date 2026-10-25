package http

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/channel-service/internal/service"
	"github.com/gin-gonic/gin"
)

// The channel-update media refusals (service/update_media.go) are 4xx with
// post-service's codes, and an unavailable media-service is a retryable 503.
// They arrive wrapped (the service adds the id and status), and
// "media does not exist" would otherwise fall to the message switch.
func TestUpdateMediaErrorWireCodes(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
		wire string
	}{
		{service.ErrMediaNotFound, http.StatusBadRequest, "MEDIA_NOT_FOUND"},
		{service.ErrMediaNotOwned, http.StatusForbidden, "MEDIA_NOT_OWNED"},
		{fmt.Errorf("%w: x is %q", service.ErrMediaNotReady, "pending_upload"), http.StatusBadRequest, "MEDIA_NOT_READY"},
		{fmt.Errorf("%w: status 502", service.ErrMediaUnavailable), http.StatusServiceUnavailable, "MEDIA_UNAVAILABLE"},
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
