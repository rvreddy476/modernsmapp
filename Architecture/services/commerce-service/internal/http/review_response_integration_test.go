//go:build integration

package http

import (
	"net/http"
	"testing"
)

// Audit 2026-10-30 L2: the seller of the reviewed product can respond (the
// route used to compare the caller's user id with reviews.seller_id, a
// sellers.id, and refuse everyone); another seller and a non-seller cannot.
func TestSellerRespondsToTheirOwnProductsReview(t *testing.T) {
	e := newContractEnv(t)
	path := "/v1/commerce/reviews/" + ctReview.String() + "/seller-response"
	body := map[string]any{"response": "Thanks for the kind words."}

	if w := e.post(path, ctOtherSellerU, body); w.Code != http.StatusForbidden {
		t.Fatalf("another seller answered %d, want 403\n%s", w.Code, w.Body.String())
	}
	if w := e.post(path, ctBuyer, body); w.Code != http.StatusForbidden {
		t.Fatalf("a non-seller answered %d, want 403\n%s", w.Code, w.Body.String())
	}
	w := e.post(path, ctSellerUser, body)
	if w.Code != http.StatusOK {
		t.Fatalf("the review's seller answered %d, want 200\n%s", w.Code, w.Body.String())
	}
	var got string
	if err := e.pool.QueryRow(t.Context(), `SELECT COALESCE(seller_response,'') FROM reviews WHERE id=$1`, ctReview).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "Thanks for the kind words." {
		t.Fatalf("seller_response = %q", got)
	}
}
