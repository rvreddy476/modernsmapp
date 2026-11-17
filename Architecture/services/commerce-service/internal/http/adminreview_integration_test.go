//go:build integration

package http

// The admin review guards at the edge, against real rows: the status, the
// code and the unchanged row for each refused decision, on both route
// families (the internal-key routes and the admin-service token routes).
//
//	COMMERCE_TEST_DSN=.../commerce_it_test go test -tags=integration ./internal/http/ -run AdminReviewIT -v

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func reviewITCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.Error.Code
}

func reviewITSellerStatus(t *testing.T, sellerID uuid.UUID) (status string, reviews int) {
	t.Helper()
	ctx := context.Background()
	if err := edgePool.QueryRow(ctx, `SELECT status FROM sellers WHERE id=$1`, sellerID).Scan(&status); err != nil {
		t.Fatalf("read seller: %v", err)
	}
	if err := edgePool.QueryRow(ctx,
		`SELECT count(*) FROM seller_onboarding_reviews WHERE seller_id=$1`, sellerID).Scan(&reviews); err != nil {
		t.Fatalf("count reviews: %v", err)
	}
	return status, reviews
}

func reviewITProduct(t *testing.T, sellerID uuid.UUID, approvalStatus string) uuid.UUID {
	t.Helper()
	productID := uuid.New()
	auditExec(t, `INSERT INTO products (id,seller_id,title,slug,status,approval_status)
	      VALUES ($1,$2,'Review Guard Product',$3,'draft',$4)`,
		productID, sellerID, "review-it-"+productID.String()[:12], approvalStatus)
	seedOfferFor(t, productID)
	return productID
}

func reviewITProductStatus(t *testing.T, productID uuid.UUID) (approval string, log int) {
	t.Helper()
	ctx := context.Background()
	if err := edgePool.QueryRow(ctx, `SELECT approval_status FROM products WHERE id=$1`, productID).Scan(&approval); err != nil {
		t.Fatalf("read product: %v", err)
	}
	if err := edgePool.QueryRow(ctx,
		`SELECT count(*) FROM product_moderation_log WHERE product_id=$1`, productID).Scan(&log); err != nil {
		t.Fatalf("count moderation log: %v", err)
	}
	return approval, log
}

func TestAdminReviewIT_ApprovingASellerNotInTheQueueIs409AndWritesNothing(t *testing.T) {
	r := auditEngine(t)
	actor := uuid.New()
	for _, status := range []string{"draft", "approved", "rejected", "suspended"} {
		t.Run(status, func(t *testing.T) {
			sellerID := seedAuditSeller(t, status, nil)
			w := call(t, r, http.MethodPost, "/v1/commerce/internal/sellers/"+sellerID.String()+"/approve",
				actor, map[string]any{"notes": "must be refused"})
			if w.Code != http.StatusConflict || reviewITCode(t, w) != CodeSellerTransitionNotAllowed {
				t.Fatalf("approve a %s seller: %d %s, want 409 %s", status, w.Code, w.Body.String(), CodeSellerTransitionNotAllowed)
			}
			if got, reviews := reviewITSellerStatus(t, sellerID); got != status || reviews != 0 {
				t.Fatalf("the refused approval wrote: status=%s reviews=%d, want %s and 0", got, reviews, status)
			}
		})
	}
}

func TestAdminReviewIT_TheTokenRouteRefusesADraftTheSameWay(t *testing.T) {
	it := newTokenIT(t)
	sellerID := seedAuditSeller(t, "draft", nil)
	base := InternalAdminPrefix + "/sellers/" + sellerID.String()
	for _, tc := range []struct{ path, perm string }{
		{"/approve", PermSellerApprove},
		{"/reject", PermSellerApprove},
		{"/request-changes", PermSellerApprove},
		{"/suspend", PermSellerSuspend},
		{"/unsuspend", PermSellerSuspend},
	} {
		code, body := it.do(t, http.MethodPost, base+tc.path, tc.perm)
		var parsed struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal([]byte(body), &parsed)
		if code != http.StatusConflict || parsed.Error.Code != CodeSellerTransitionNotAllowed {
			t.Fatalf("%s on a draft seller: %d %s, want 409 %s", tc.path, code, body, CodeSellerTransitionNotAllowed)
		}
	}
	if got, reviews := reviewITSellerStatus(t, sellerID); got != "draft" || reviews != 0 {
		t.Fatalf("the draft moved: status=%s reviews=%d", got, reviews)
	}
}

func TestAdminReviewIT_AnUnknownSellerOrProductIs404(t *testing.T) {
	r := auditEngine(t)
	actor := uuid.New()
	w := call(t, r, http.MethodPost, "/v1/commerce/internal/sellers/"+uuid.NewString()+"/approve", actor, map[string]any{})
	if w.Code != http.StatusNotFound || reviewITCode(t, w) != CodeSellerNotFound {
		t.Fatalf("approve an unknown seller: %d %s, want 404 %s", w.Code, w.Body.String(), CodeSellerNotFound)
	}
	w = call(t, r, http.MethodPost, "/v1/commerce/internal/products/"+uuid.NewString()+"/approve", actor, map[string]any{})
	if w.Code != http.StatusNotFound || reviewITCode(t, w) != "PRODUCT_NOT_FOUND" {
		t.Fatalf("approve an unknown product: %d %s, want 404 PRODUCT_NOT_FOUND", w.Code, w.Body.String())
	}
}

func TestAdminReviewIT_ApprovingAProductNeverSubmittedIs409AndWritesNothing(t *testing.T) {
	r := auditEngine(t)
	sellerID := seedAuditSeller(t, "approved", nil)
	productID := reviewITProduct(t, sellerID, "draft")

	w := call(t, r, http.MethodPost, "/v1/commerce/internal/products/"+productID.String()+"/approve",
		uuid.New(), map[string]any{"notes": "must be refused"})
	if w.Code != http.StatusConflict || reviewITCode(t, w) != CodeProductTransitionNotAllowed {
		t.Fatalf("approve a draft product: %d %s, want 409 %s", w.Code, w.Body.String(), CodeProductTransitionNotAllowed)
	}
	if got, log := reviewITProductStatus(t, productID); got != "draft" || log != 0 {
		t.Fatalf("the refused approval wrote: approval_status=%s log=%d", got, log)
	}
}

func TestAdminReviewIT_ApprovingAProductOfAnUnapprovedSellerIs409AndWritesNothing(t *testing.T) {
	r := auditEngine(t)
	sellerID := seedAuditSeller(t, "suspended", nil)
	productID := reviewITProduct(t, sellerID, "submitted")

	w := call(t, r, http.MethodPost, "/v1/commerce/internal/products/"+productID.String()+"/approve",
		uuid.New(), map[string]any{"notes": "must be refused"})
	if w.Code != http.StatusConflict || reviewITCode(t, w) != CodeProductSellerNotApproved {
		t.Fatalf("approve a product of a suspended seller: %d %s, want 409 %s", w.Code, w.Body.String(), CodeProductSellerNotApproved)
	}
	if got, log := reviewITProductStatus(t, productID); got != "submitted" || log != 0 {
		t.Fatalf("the refused approval wrote: approval_status=%s log=%d", got, log)
	}
}

func TestAdminReviewIT_ASubmittedSellerAndProductAreApprovedOnceThenRefused(t *testing.T) {
	r := auditEngine(t)
	actor := uuid.New()
	sellerID := seedAuditSeller(t, "submitted", nil)
	sellerPath := "/v1/commerce/internal/sellers/" + sellerID.String() + "/approve"

	if w := call(t, r, http.MethodPost, sellerPath, actor, map[string]any{"notes": "ok"}); w.Code != http.StatusNoContent {
		t.Fatalf("approve a submitted seller: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, r, http.MethodPost, sellerPath, actor, map[string]any{"notes": "again"}); w.Code != http.StatusConflict ||
		reviewITCode(t, w) != CodeSellerTransitionNotAllowed {
		t.Fatalf("approve an approved seller: %d %s, want 409 %s", w.Code, w.Body.String(), CodeSellerTransitionNotAllowed)
	}
	if got, reviews := reviewITSellerStatus(t, sellerID); got != "approved" || reviews != 1 {
		t.Fatalf("after one approval and one refusal: status=%s reviews=%d, want approved and 1", got, reviews)
	}

	productID := reviewITProduct(t, sellerID, "submitted")
	productPath := "/v1/commerce/internal/products/" + productID.String() + "/approve"
	if w := call(t, r, http.MethodPost, productPath, actor, map[string]any{}); w.Code != http.StatusNoContent {
		t.Fatalf("approve a submitted product: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, r, http.MethodPost, productPath, actor, map[string]any{}); w.Code != http.StatusConflict ||
		reviewITCode(t, w) != CodeProductTransitionNotAllowed {
		t.Fatalf("approve an approved product: %d %s, want 409 %s", w.Code, w.Body.String(), CodeProductTransitionNotAllowed)
	}
	if got, log := reviewITProductStatus(t, productID); got != "approved" || log != 1 {
		t.Fatalf("after one approval and one refusal: approval_status=%s log=%d, want approved and 1", got, log)
	}
}

// POST /onboarding/start answers with the stored row: status `draft`, the
// same as GET /onboarding/status. It answered an empty status before.
func TestAdminReviewIT_OnboardingStartAnswersTheStoredStatus(t *testing.T) {
	r := auditEngine(t)
	user := uuid.New()
	w := call(t, r, http.MethodPost, "/v1/commerce/onboarding/start", user, map[string]any{
		"store_name": "Start Status " + uuid.NewString()[:6],
		"email":      "start-" + uuid.NewString()[:6] + "@example.test",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("onboarding/start: %d %s", w.Code, w.Body.String())
	}
	type answer struct {
		Data struct {
			ID             uuid.UUID `json:"id"`
			Status         string    `json:"status"`
			OnboardingStep int       `json:"onboarding_step"`
			SellScope      string    `json:"sell_scope"`
		} `json:"data"`
	}
	var started, read, again answer
	if err := json.Unmarshal(w.Body.Bytes(), &started); err != nil {
		t.Fatalf("start answer: %v\n%s", err, w.Body.String())
	}
	if started.Data.Status != "draft" || started.Data.OnboardingStep != 1 || started.Data.SellScope != "all_categories" {
		t.Fatalf("start answered status=%q step=%d sell_scope=%q, want draft / 1 / all_categories\n%s",
			started.Data.Status, started.Data.OnboardingStep, started.Data.SellScope, w.Body.String())
	}

	w = call(t, r, http.MethodGet, "/v1/commerce/onboarding/status", user, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &read); err != nil || w.Code != http.StatusOK {
		t.Fatalf("status read: %d %v\n%s", w.Code, err, w.Body.String())
	}
	if read.Data != started.Data {
		t.Fatalf("start answered %+v, the status read answers %+v", started.Data, read.Data)
	}

	// The idempotent second start returns the same stored row.
	w = call(t, r, http.MethodPost, "/v1/commerce/onboarding/start", user, map[string]any{
		"store_name": "ignored", "email": "ignored@example.test",
	})
	if err := json.Unmarshal(w.Body.Bytes(), &again); err != nil || w.Code/100 != 2 {
		t.Fatalf("second start: %d %v\n%s", w.Code, err, w.Body.String())
	}
	if again.Data != started.Data {
		t.Fatalf("the second start answered %+v, want the first row %+v", again.Data, started.Data)
	}
}
