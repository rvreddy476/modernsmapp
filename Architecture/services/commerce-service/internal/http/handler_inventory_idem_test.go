package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Android audit 2026-10-30 M3: an over-long Idempotency-Key is refused at the
// edge, before any stock work (the nil service would panic if reached).
func TestAdjustStock_RefusesAnOverlongIdempotencyKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PATCH("/v1/commerce/seller/variants/:variantId/stock", (&Handler{}).AdjustStock)
	req := httptest.NewRequest(http.MethodPatch, "/v1/commerce/seller/variants/"+uuid.NewString()+"/stock",
		strings.NewReader(`{"delta":50,"reason":"purchase"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", uuid.NewString())
	req.Header.Set("Idempotency-Key", strings.Repeat("k", maxStockIdempotencyKeyLen+1))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "INVALID_IDEMPOTENCY_KEY") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}
