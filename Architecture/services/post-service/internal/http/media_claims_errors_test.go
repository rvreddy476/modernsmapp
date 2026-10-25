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

func answer(t *testing.T, write func(*gin.Context, error) bool, err error) (bool, int, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	ok := write(c, err)
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	return ok, rec.Code, env.Error.Code
}

// The claim refusals answer the same on every route: 503 when media-service
// could not record the reference (retry), 403 for a stranger's asset, 422
// for one that is gone.
func TestMediaClaimsErrorStatuses(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: down", service.ErrMediaClaimsUnavailable), http.StatusServiceUnavailable, "MEDIA_CLAIMS_UNAVAILABLE"},
		{service.ErrMediaNotOwned, http.StatusForbidden, "MEDIA_NOT_OWNED"},
		{&service.ClaimMediaGoneError{}, http.StatusUnprocessableEntity, "MEDIA_NOT_FOUND"},
	}
	for _, tc := range cases {
		ok, status, code := answer(t, writeOwnMediaError, tc.err)
		if !ok || status != tc.status || code != tc.code {
			t.Fatalf("%v: answered=%v %d %s, want %d %s", tc.err, ok, status, code, tc.status, tc.code)
		}
	}
	if ok, _, _ := answer(t, writeOwnMediaError, errors.New("title is required")); ok {
		t.Fatal("an unrelated error was answered as a media refusal")
	}
	if ok, status, code := answer(t, writeCreateGuardError, fmt.Errorf("%w: x", service.ErrMediaClaimsUnavailable)); !ok ||
		status != http.StatusServiceUnavailable || code != "MEDIA_CLAIMS_UNAVAILABLE" {
		t.Fatalf("create: %v %d %s", ok, status, code)
	}
}
