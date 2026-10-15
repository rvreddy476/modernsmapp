package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// A report the reporter can fix is a 400 with a usable message, never a
// 500. Every case fails validation before the store, so a service with no
// database is enough (it would panic if a case reached the store).
func TestFileReportRejectsBadEntityIDsWith400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := New(service.New(nil, nil))
	r := gin.New()
	r.POST("/v1/reports", h.FileReport)

	cases := []struct {
		name, body, wantMsg string
	}{
		{"post with a non-UUID id", `{"entity_type":"post","entity_id":"dance","reason":"spam"}`, "Invalid entity ID"},
		{"sound with a non-UUID id", `{"entity_type":"sound","entity_id":"dance","reason":"spam"}`, "Invalid entity ID"},
		{"hashtag with spaces", `{"entity_type":"hashtag","entity_id":"two words","reason":"spam"}`, "invalid hashtag"},
		{"hashtag too long", `{"entity_type":"hashtag","entity_id":"` + strings.Repeat("a", service.MaxReportHashtagLength+1) + `","reason":"spam"}`, "invalid hashtag"},
		{"reason outside the allowlist", `{"entity_type":"hashtag","entity_id":"dance","reason":"bored"}`, "invalid reason"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/reports", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-User-Id", uuid.NewString())
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantMsg) {
				t.Fatalf("body %s does not say %q", rec.Body.String(), tc.wantMsg)
			}
		})
	}
}
