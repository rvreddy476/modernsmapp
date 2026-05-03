package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

const testUserID = "11111111-1111-1111-1111-111111111111"

func TestHasScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		scopes string
		target string
		want   bool
	}{
		{name: "exact match", scopes: "profile admin food:read", target: "admin", want: true},
		{name: "superadmin match", scopes: "superadmin", target: "superadmin", want: true},
		{name: "substring does not match", scopes: "superadmin", target: "admin", want: false},
		{name: "empty scopes", scopes: "", target: "admin", want: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hasScope(tc.scopes, tc.target); got != tc.want {
				t.Fatalf("hasScope(%q, %q) = %v, want %v", tc.scopes, tc.target, got, tc.want)
			}
		})
	}
}

func TestRequireAuthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := New(nil)
	router := gin.New()
	router.GET("/protected", handler.requireAuthenticated(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	t.Run("missing user id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("valid user id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		req.Header.Set("X-User-Id", testUserID)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
		}
	})
}

func TestRequireAdminScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := New(nil)
	router := gin.New()
	router.GET("/admin", handler.requireAdminScope(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	t.Run("requires user id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		req.Header.Set("X-Scopes", "admin")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("requires admin scope", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		req.Header.Set("X-User-Id", testUserID)
		req.Header.Set("X-Scopes", "profile food:read")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
		}
	})

	t.Run("allows admin scope", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		req.Header.Set("X-User-Id", testUserID)
		req.Header.Set("X-Scopes", "profile admin")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
		}
	})

	t.Run("allows superadmin scope", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		req.Header.Set("X-User-Id", testUserID)
		req.Header.Set("X-Scopes", "superadmin")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
		}
	})
}
