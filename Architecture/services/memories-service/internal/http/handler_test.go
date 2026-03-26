package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestParseUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("missing header", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

		got, ok := parseUserID(c)
		if ok {
			t.Fatalf("expected parseUserID to fail")
		}
		if got != uuid.Nil {
			t.Fatalf("expected nil uuid, got %s", got)
		}
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected %d, got %d", http.StatusUnauthorized, rec.Code)
		}
	})

	t.Run("invalid header", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-User-Id", "not-a-uuid")
		c.Request = req

		got, ok := parseUserID(c)
		if ok {
			t.Fatalf("expected parseUserID to fail")
		}
		if got != uuid.Nil {
			t.Fatalf("expected nil uuid, got %s", got)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected %d, got %d", http.StatusBadRequest, rec.Code)
		}
	})

	t.Run("valid header", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		userID := uuid.New()
		req.Header.Set("X-User-Id", userID.String())
		c.Request = req

		got, ok := parseUserID(c)
		if !ok {
			t.Fatalf("expected parseUserID to succeed")
		}
		if got != userID {
			t.Fatalf("expected %s, got %s", userID, got)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("expected recorder to remain untouched, got %d", rec.Code)
		}
	})
}

func TestParsePagination(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		query  string
		limit  int
		offset int
	}{
		{name: "defaults", query: "", limit: 20, offset: 0},
		{name: "valid overrides", query: "limit=50&offset=25", limit: 50, offset: 25},
		{name: "limit capped", query: "limit=101&offset=9", limit: 20, offset: 9},
		{name: "negative offset rejected", query: "limit=5&offset=-1", limit: 5, offset: 0},
		{name: "invalid values ignored", query: "limit=abc&offset=xyz", limit: 20, offset: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?"+tt.query, nil)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = req

			limit, offset := parsePagination(c)
			if limit != tt.limit || offset != tt.offset {
				t.Fatalf("parsePagination() = (%d, %d), want (%d, %d)", limit, offset, tt.limit, tt.offset)
			}
		})
	}
}
