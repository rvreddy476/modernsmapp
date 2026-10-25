package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/user-service/internal/service"
	"github.com/gin-gonic/gin"
)

func TestWriteMediaErrorCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrMediaNotFound, http.StatusBadRequest, "MEDIA_NOT_FOUND"},
		{service.ErrMediaNotOwned, http.StatusForbidden, "MEDIA_NOT_OWNED"},
		{fmt.Errorf("%w: x is %q", service.ErrMediaNotReady, "pending_upload"), http.StatusBadRequest, "MEDIA_NOT_READY"},
		{service.ErrMediaInvalid, http.StatusBadRequest, "INVALID_MEDIA_ID"},
		{service.ErrPageDisabled, http.StatusConflict, "PAGE_DISABLED"},
		{fmt.Errorf("%w: status 502", service.ErrMediaUnavailable), http.StatusServiceUnavailable, "MEDIA_UNAVAILABLE"},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPatch, "/", nil)
		if !writeMediaError(c, tc.err) {
			t.Fatalf("%v: not handled", tc.err)
		}
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != tc.status || body.Error.Code != tc.code {
			t.Errorf("%v: got %d %q, want %d %q", tc.err, w.Code, body.Error.Code, tc.status, tc.code)
		}
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPatch, "/", nil)
	if writeMediaError(c, errors.New("CHANNEL_NOT_FOUND")) {
		t.Fatal("claimed an error it does not own")
	}
}
