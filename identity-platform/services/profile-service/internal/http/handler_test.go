package http

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/identity-platform/profile-service/internal/store"
)

type stubProfileService struct {
	getFn    func(id uuid.UUID) (*store.Profile, error)
	updateFn func(id uuid.UUID, displayName, bio string, avatarMediaID *uuid.UUID, firstName, lastName, gender *string, dob *time.Time) (*store.Profile, error)
}

func (s *stubProfileService) GetProfile(ctx context.Context, userID uuid.UUID) (*store.Profile, error) {
	if s.getFn == nil {
		return nil, nil
	}
	return s.getFn(userID)
}

func (s *stubProfileService) UpdateProfile(ctx context.Context, userID uuid.UUID, displayName, bio string, avatarMediaID *uuid.UUID, firstName, lastName, gender *string, dob *time.Time) (*store.Profile, error) {
	if s.updateFn == nil {
		return nil, nil
	}
	return s.updateFn(userID, displayName, bio, avatarMediaID, firstName, lastName, gender, dob)
}

func TestGetProfileInvalidID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubProfileService{}, nil)
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })

	req := httptest.NewRequest(http.MethodGet, "/v1/profiles/not-a-uuid", nil)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, resp.Code)
	}
}

func TestUpdateMeInvalidHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&stubProfileService{}, nil)
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })

	req := httptest.NewRequest(http.MethodPut, "/v1/profiles/me", bytes.NewBufferString(`{"display_name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, resp.Code)
	}
}
