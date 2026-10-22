package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/gin-gonic/gin"
)

// A reel draft keeps its kind (migration 069), so a long video reaches the
// long-video guards on the draft publish. They answer with the create
// route's codes, through PublishDraft's wrapping, and never as a 500.
func TestReelDraftPublishErrorsAreTheCreateRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrChannelRequired, http.StatusForbidden, "CHANNEL_REQUIRED"},
		{service.ErrTitleRequired, http.StatusBadRequest, "TITLE_REQUIRED"},
		{service.ErrTitleTooLong, http.StatusBadRequest, "TITLE_TOO_LONG"},
		{service.ErrMediaNotReady, http.StatusBadRequest, "MEDIA_NOT_READY"},
		{service.ErrInvalidHashtag, http.StatusBadRequest, "INVALID_HASHTAG"},
		{service.ErrTooManyHashtags, http.StatusBadRequest, "TOO_MANY_HASHTAGS"},
		{errors.New("db down"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	} {
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		ctx.Request = httptest.NewRequest("POST", "/v1/reels/drafts/x/publish", nil)
		writeReelDraftPublishError(ctx, fmt.Errorf("publish draft as post: %w", c.err))
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != c.status || body.Error.Code != c.code {
			t.Errorf("%v: %d %q, want %d %q", c.err, rec.Code, body.Error.Code, c.status, c.code)
		}
	}
}
