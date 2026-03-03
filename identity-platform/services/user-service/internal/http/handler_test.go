package http

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/atpost/identity-user-service/internal/store"
)

type stubUserService struct {
	getUserFn     func(id uuid.UUID) (*store.User, error)
	getSettingsFn func(id uuid.UUID) (*store.UserSettings, error)
	updateFn      func(settings *store.UserSettings) (*store.UserSettings, error)
}

func (s *stubUserService) GetUser(ctx context.Context, id uuid.UUID) (*store.User, error) {
	if s.getUserFn == nil {
		return nil, nil
	}
	return s.getUserFn(id)
}

func (s *stubUserService) GetSettings(ctx context.Context, id uuid.UUID) (*store.UserSettings, error) {
	if s.getSettingsFn == nil {
		return nil, nil
	}
	return s.getSettingsFn(id)
}

func (s *stubUserService) UpdateSettings(ctx context.Context, settings *store.UserSettings) (*store.UserSettings, error) {
	if s.updateFn == nil {
		return nil, nil
	}
	return s.updateFn(settings)
}

func TestGetUserInvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubUserService{}, nil)
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })

	req := httptest.NewRequest(http.MethodGet, "/v1/users/not-a-uuid", nil)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, resp.Code)
	}
}

func TestGetMeMissingHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubUserService{}, nil)
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })

	req := httptest.NewRequest(http.MethodGet, "/v1/users/me", nil)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, resp.Code)
	}
}

func TestUpdateMySettingsInvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubUserService{}, nil)
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })

	req := httptest.NewRequest(http.MethodPut, "/v1/users/me/settings", bytes.NewBufferString("{bad-json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", uuid.New().String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, resp.Code)
	}
}
