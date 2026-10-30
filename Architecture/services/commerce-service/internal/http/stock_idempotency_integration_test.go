//go:build integration

package http

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Android audit 2026-10-30 M3, through the route: a +50 retried with the same
// Idempotency-Key applies once; a request without the header still works.
func TestStockAdjustRouteHonoursIdempotencyKey(t *testing.T) {
	e := newContractEnv(t)
	path := "/v1/commerce/seller/variants/" + ctV1.String() + "/stock"
	total := func() int {
		var n int
		if err := e.pool.QueryRow(t.Context(), `SELECT total_qty FROM inventory_items WHERE variant_id=$1`, ctV1).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := total()
	key := map[string]string{"Idempotency-Key": "stock-" + uuid.NewString()}
	body := map[string]any{"delta": 50, "reason": "purchase"}

	w := e.do(ctReq{method: http.MethodPatch, path: path, user: ctSellerUser, body: body, headers: key})
	if w.Code != http.StatusOK || w.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("first = %d %q\n%s", w.Code, w.Header().Get("Idempotent-Replayed"), w.Body.String())
	}
	w = e.do(ctReq{method: http.MethodPatch, path: path, user: ctSellerUser, body: body, headers: key})
	if w.Code != http.StatusOK || w.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("retry = %d %q\n%s", w.Code, w.Header().Get("Idempotent-Replayed"), w.Body.String())
	}
	if got := total(); got != before+50 {
		t.Fatalf("total_qty = %d, want %d (+50 once)", got, before+50)
	}
	// Same key, different delta: 409.
	w = e.do(ctReq{method: http.MethodPatch, path: path, user: ctSellerUser,
		body: map[string]any{"delta": 5, "reason": "purchase"}, headers: key})
	if w.Code != http.StatusConflict {
		t.Fatalf("same key, different request = %d\n%s", w.Code, w.Body.String())
	}
	// No header: applies, as before.
	w = e.do(ctReq{method: http.MethodPatch, path: path, user: ctSellerUser, body: map[string]any{"delta": -10, "reason": "recount"}})
	if w.Code != http.StatusOK {
		t.Fatalf("keyless = %d\n%s", w.Code, w.Body.String())
	}
	if got := total(); got != before+40 {
		t.Fatalf("total_qty = %d, want %d", got, before+40)
	}
}
