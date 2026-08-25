package http

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestCanonicalModerationRejectsUnprivilegedBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil, nil).RegisterRoutes(r) // nil service proves the gate runs first.
	body := []byte(`{"decision_id":"` + uuid.NewString() + `","action":"reject","reason":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/posts/"+uuid.NewString()+"/moderation", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestInternalModerationRejectsMissingVerifierBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil, nil).RegisterRoutes(r)
	req := httptest.NewRequest(http.MethodPost, "/v1/posts/internal/moderation", bytes.NewReader([]byte(`{"claims":{},"capability":"bad"}`)))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
}
