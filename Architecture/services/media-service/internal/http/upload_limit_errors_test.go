package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/media-service/internal/service"
	"github.com/gin-gonic/gin"
)

// The upload-path refusals a client must tell apart (lane media-upload):
// status, code, Retry-After and the details it can show.
func TestUploadLimitErrorsAnswerTheirCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		err    error
		status int
		code   string
		retry  string
	}{
		{fmt.Errorf("%w: 5h", service.ErrVideoTooLong), http.StatusRequestEntityTooLarge, CodeVideoTooLong, ""},
		{&service.UploadQuotaError{Limit: service.QuotaLimitDailyBytes, Max: 10, Used: 9, Asked: 2}, http.StatusTooManyRequests, CodeUploadQuotaExceeded, "3600"},
		{&service.UploadQuotaError{Limit: service.QuotaLimitDailySeconds, Max: 10, Used: 9, Asked: 2}, http.StatusTooManyRequests, CodeUploadQuotaExceeded, "3600"},
		{fmt.Errorf("wrapped: %w", &service.UploadQuotaError{Limit: service.QuotaLimitInflightLong, Max: 2, Used: 2}), http.StatusTooManyRequests, CodeLongVideoInProgress, "600"},
		{fmt.Errorf("%w: redis", service.ErrUploadLimiterUnavailable), http.StatusServiceUnavailable, CodeUploadLimiterUnavailable, "30"},
		{service.ErrResumableExpired, http.StatusGone, CodeUploadExpired, ""},
		{fmt.Errorf("%w: 1 of 3", service.ErrResumableIncomplete), http.StatusConflict, CodeUploadIncomplete, ""},
		{service.ErrResumableNotFound, http.StatusNotFound, "NOT_FOUND", ""},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		if !writeUploadLimitError(ctx, c.err) {
			t.Fatalf("%v: not answered", c.err)
		}
		var body struct {
			Error struct {
				Code    string         `json:"code"`
				Message string         `json:"message"`
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%v: body %s", c.err, w.Body.String())
		}
		if w.Code != c.status || body.Error.Code != c.code || w.Header().Get("Retry-After") != c.retry || body.Error.Message == "" {
			t.Fatalf("%v: %d %q retry %q, want %d %q retry %q", c.err, w.Code, body.Error.Code,
				w.Header().Get("Retry-After"), c.status, c.code, c.retry)
		}
		if c.code == CodeUploadQuotaExceeded && body.Error.Details["limit"] == nil {
			t.Fatalf("%v: no details.limit: %s", c.err, w.Body.String())
		}
	}
	// Anything else is left to the route's own mapping.
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	if writeUploadLimitError(ctx, errors.New("other")) {
		t.Fatal("an unrelated error was answered")
	}
}
