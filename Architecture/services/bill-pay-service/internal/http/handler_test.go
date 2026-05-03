package http

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

func TestRespondServiceError_Translates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	type want struct {
		status int
		code   string
	}
	cases := map[string]want{
		"invalid: bad input":   {400, "INVALID_REQUEST"},
		"forbidden: blocked":   {403, "FORBIDDEN"},
		"not_found: nope":      {404, "NOT_FOUND"},
		"setu transport error": {502, "PAY_FAILED"},
	}
	for msg, w := range cases {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/", strings.NewReader("{}"))
		respondServiceError(c, errorString(msg), 502, "PAY_FAILED")
		if rec.Code != w.status {
			t.Errorf("for %q expected status %d; got %d", msg, w.status, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), w.code) {
			t.Errorf("for %q expected code %q in body; body=%s", msg, w.code, rec.Body.String())
		}
	}
}

// errorString is a tiny helper to satisfy `error` from a string in tests.
type errorString string

func (e errorString) Error() string { return string(e) }

// Compile-time check that api.JSONWithContext is reachable from this test
// file (ensures the import graph is valid).
var (
	_ = api.JSONWithContext
)
