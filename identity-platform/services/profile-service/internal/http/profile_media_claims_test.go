package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/identity-profile-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type attachableAuthority struct{}

func (attachableAuthority) RequireAttachable(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}

// mediaWriteService answers the avatar and cover writes with err.
type mediaWriteService struct {
	stubProfileService
	err error
}

func (s *mediaWriteService) UpdateAvatar(context.Context, uuid.UUID, uuid.UUID) error { return s.err }
func (s *mediaWriteService) UpdateCover(context.Context, uuid.UUID, uuid.UUID) error  { return s.err }

func putProfileMedia(t *testing.T, route string, svcErr error) (int, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&mediaWriteService{err: svcErr}, nil).WithMediaAuthority(attachableAuthority{})
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })
	body, _ := json.Marshal(map[string]string{"media_id": uuid.NewString()})
	req := httptest.NewRequest(http.MethodPut, route, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", uuid.NewString())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(resp.Body.Bytes(), &env)
	return resp.Code, env.Error.Code
}

// A refused claim is the caller's answer, not a 500: the image vanished (409,
// the same as never attachable), media-service could not record the use
// (503, retry), or there is no profile (404). Nothing was written in any.
func TestProfileMediaClaimRefusalsAreAnswered(t *testing.T) {
	for _, route := range []string{"/v1/profiles/me/avatar", "/v1/profiles/me/cover"} {
		for name, tc := range map[string]struct {
			err    error
			status int
			code   string
		}{
			"ok":            {nil, http.StatusOK, ""},
			"media gone":    {service.ErrMediaNotFound, http.StatusConflict, "MEDIA_NOT_ATTACHABLE"},
			"media down":    {fmt.Errorf("%w: claim: eof", service.ErrMediaUnavailable), http.StatusServiceUnavailable, "MEDIA_AUTHORITY_UNAVAILABLE"},
			"no profile":    {service.ErrProfileNotFound, http.StatusNotFound, "NOT_FOUND"},
			"store failure": {errors.New("connection reset"), http.StatusInternalServerError, "INTERNAL_ERROR"},
		} {
			status, code := putProfileMedia(t, route, tc.err)
			if status != tc.status || code != tc.code {
				t.Errorf("%s %s: got %d %q, want %d %q", route, name, status, code, tc.status, tc.code)
			}
		}
	}
}
