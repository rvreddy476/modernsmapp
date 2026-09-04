package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
)

// Tube channels (2026-09-05): the wire codes the Android composer keys on.

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

func TestWriteChannelErrorCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		err     error
		status  int
		code    string
		handled bool
	}{
		{service.ErrInvalidChannelName, http.StatusBadRequest, "INVALID_NAME", true},
		{service.ErrInvalidHandle, http.StatusBadRequest, "INVALID_HANDLE", true},
		{service.ErrInvalidChannelAbout, http.StatusBadRequest, "INVALID_ABOUT", true},
		{postgres.ErrChannelExists, http.StatusConflict, "CHANNEL_EXISTS", true},
		{postgres.ErrHandleTaken, http.StatusConflict, "HANDLE_TAKEN", true},
		{errors.New("boom"), 0, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/channels", nil)
			handled := writeChannelError(c, tc.err)
			if handled != tc.handled {
				t.Fatalf("handled=%v want %v", handled, tc.handled)
			}
			if !tc.handled {
				return
			}
			if rec.Code != tc.status || errorCode(t, rec) != tc.code {
				t.Fatalf("got %d %s want %d %s", rec.Code, errorCode(t, rec), tc.status, tc.code)
			}
		})
	}
}

// POST /v1/posts with a long video and no channel is 403 CHANNEL_REQUIRED
// with the founder's sentence, through the same guard writer every other
// create-time refusal uses.
func TestCreateGuardMapsChannelRequired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/posts", nil)
	if !writeCreateGuardError(c, service.ErrChannelRequired) {
		t.Fatal("ErrChannelRequired not handled by writeCreateGuardError")
	}
	if rec.Code != http.StatusForbidden || errorCode(t, rec) != "CHANNEL_REQUIRED" {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Create your channel before posting a video") {
		t.Fatalf("message missing: %s", rec.Body.String())
	}
}

// The channel routes exist, and the static ones are not swallowed by :ref.
func TestChannelRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&Handler{}).RegisterRoutes(r)
	want := map[string]bool{
		"POST /v1/channels":                 false,
		"GET /v1/channels/me":               false,
		"PATCH /v1/channels/me":             false,
		"GET /v1/channels/handle-available": false,
		"GET /v1/channels/batch":            false,
		"GET /v1/channels/:ref":             false,
	}
	for _, info := range r.Routes() {
		key := info.Method + " " + info.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, seen := range want {
		if !seen {
			t.Errorf("route %q not registered", route)
		}
	}
}
