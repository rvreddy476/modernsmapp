package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type failingModuleService struct {
	stubProfileService
	err error
}

func (s *failingModuleService) UpsertModuleProfile(context.Context, uuid.UUID, string, store.UpsertModuleProfileParams) (*store.ModuleProfile, error) {
	return nil, s.err
}

func TestUpsertModuleProfileHidesDriverError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	driver := errors.New(`failed to upsert module profile: ERROR: duplicate key value violates unique constraint "module_profiles_pkey" (SQLSTATE 23505)`)
	h := &Handler{svc: &failingModuleService{err: driver}, log: slog.Default()}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`))
	c.Request.Header.Set("X-User-Id", uuid.NewString())
	c.Params = gin.Params{{Key: "module", Value: "postbook"}}
	h.UpsertMyModuleProfile(c)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	for _, leak := range []string{"SQLSTATE", "duplicate key", "module_profiles_pkey", "failed to upsert"} {
		if strings.Contains(body, leak) {
			t.Fatalf("body leaks %q: %s", leak, body)
		}
	}
	if !strings.Contains(body, "internal error") {
		t.Fatalf("body = %s, want generic message", body)
	}
}

func TestUpsertModuleProfileKeepsInvalidModuleMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{svc: &failingModuleService{err: errors.New("invalid module: must be postbook, posttube, or postgram")}, log: slog.Default()}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`))
	c.Request.Header.Set("X-User-Id", uuid.NewString())
	c.Params = gin.Params{{Key: "module", Value: "x"}}
	h.UpsertMyModuleProfile(c)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid module") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}
