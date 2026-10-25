package http

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/qa-service/internal/mediaclaims"
	"github.com/gin-gonic/gin"
)

// The gate's refusals reach the client as the shared media codes, wrapped or
// not, and never as a 500 or whatever substring rule matches the message.
func TestMediaRefusalsMapToTheSharedCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, tc := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"not found":   {mediaclaims.ErrMediaNotFound, http.StatusBadRequest, "MEDIA_NOT_FOUND"},
		"not owned":   {mediaclaims.ErrMediaNotOwned, http.StatusForbidden, "MEDIA_NOT_OWNED"},
		"not ready":   {fmt.Errorf("%w: x is pending_upload", mediaclaims.ErrMediaNotReady), http.StatusBadRequest, "MEDIA_NOT_READY"},
		"unavailable": {fmt.Errorf("%w: claim: down", mediaclaims.ErrMediaUnavailable), http.StatusServiceUnavailable, "MEDIA_UNAVAILABLE"},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPatch, "/", nil)
		respondServiceError(c, tc.err, http.StatusInternalServerError, "CREATE_FAILED")
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPatch, "/", nil)
	if writeMediaError(c, errors.New("forbidden: you cannot ask questions in this community")) {
		t.Fatal("an unrelated error was taken as a media refusal")
	}
}
