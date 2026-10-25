package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/mediaclient"
	"github.com/atpost/live-service-v2/internal/service"
)

// stubCovers is media-service for stream covers: every id is owner's
// finished upload, and claims always succeed.
type stubCovers struct{ owner uuid.UUID }

func (s stubCovers) Get(_ context.Context, id uuid.UUID) (*mediaclient.Asset, error) {
	return &mediaclient.Asset{ID: id, UploaderID: s.owner, FileType: "image", ProcessingStatus: "ready", ModerationStatus: "passed"}, nil
}
func (stubCovers) Claim(context.Context, string, string, []uuid.UUID) error   { return nil }
func (stubCovers) Release(context.Context, string, string, []uuid.UUID) error { return nil }

// The cover refusals answer channel-service's codes, so a client handles a
// bad cover the same way everywhere.
func TestCoverErrorCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrCoverNotFound, http.StatusBadRequest, "MEDIA_NOT_FOUND"},
		{service.ErrCoverNotOwned, http.StatusForbidden, "MEDIA_NOT_OWNED"},
		{fmt.Errorf("%w: processing %q", service.ErrCoverNotReady, "pending_upload"), http.StatusBadRequest, "MEDIA_NOT_READY"},
		{fmt.Errorf("%w: claim: %v", service.ErrMediaUnavailable, errors.New("502")), http.StatusServiceUnavailable, "MEDIA_UNAVAILABLE"},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		writeServiceErr(c, tc.err)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
			t.Errorf("%v -> %d %s, want %d %s", tc.err, w.Code, w.Body.String(), tc.status, tc.code)
		}
	}
}
