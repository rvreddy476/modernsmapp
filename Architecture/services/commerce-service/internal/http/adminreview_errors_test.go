package http

// What an admin (through admin-service) is told when a review action is
// refused by its state guard: 404 for a row that does not exist, 409 with a
// stable code for a row in the wrong state. Before the guards every one of
// these was a 204 and the row moved.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
)

type reviewErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Details struct {
			Action         string   `json:"action"`
			Status         string   `json:"status"`
			ApprovalStatus string   `json:"approval_status"`
			SellerStatus   string   `json:"seller_status"`
			AllowedFrom    []string `json:"allowed_from"`
		} `json:"details"`
	} `json:"error"`
}

func reviewAnswer(t *testing.T, err error) (int, reviewErrorBody) {
	t.Helper()
	code, raw := status(t, err)
	var body reviewErrorBody
	if jerr := json.Unmarshal([]byte(raw), &body); jerr != nil {
		t.Fatalf("answer is not the error envelope: %v\n%s", jerr, raw)
	}
	return code, body
}

func TestApprovingASellerInTheWrongStateIsAConflictThatNamesTheState(t *testing.T) {
	// Wrapped, as a service layer may wrap it.
	err := fmt.Errorf("approve seller: %w", &postgres.SellerTransitionError{
		Action: postgres.SellerActionApprove, Status: "draft",
		AllowedFrom: postgres.SellerActionAllowedFrom(postgres.SellerActionApprove),
	})
	code, body := reviewAnswer(t, err)
	if code != http.StatusConflict || body.Error.Code != CodeSellerTransitionNotAllowed {
		t.Fatalf("got %d %s, want 409 %s", code, body.Error.Code, CodeSellerTransitionNotAllowed)
	}
	if CodeSellerTransitionNotAllowed != "SELLER_TRANSITION_NOT_ALLOWED" {
		t.Fatalf("the code is %q: admin-service and the console key on SELLER_TRANSITION_NOT_ALLOWED", CodeSellerTransitionNotAllowed)
	}
	d := body.Error.Details
	if d.Action != "approve" || d.Status != "draft" || len(d.AllowedFrom) != 3 {
		t.Fatalf("details = %+v, want the action, the seller's status and the states approve applies to", d)
	}
}

func TestAnAdminActionOnAnUnknownSellerIsNotFound(t *testing.T) {
	code, body := reviewAnswer(t, fmt.Errorf("approve seller: %w", postgres.ErrSellerNotFound))
	if code != http.StatusNotFound || body.Error.Code != "SELLER_NOT_FOUND" {
		t.Fatalf("got %d %s, want 404 SELLER_NOT_FOUND", code, body.Error.Code)
	}
}

func TestApprovingAProductInTheWrongStateIsAConflictThatNamesTheState(t *testing.T) {
	err := &postgres.ProductTransitionError{
		Action: postgres.ProductActionApprove, ApprovalStatus: "draft",
		AllowedFrom: postgres.ProductActionAllowedFrom(postgres.ProductActionApprove),
	}
	code, body := reviewAnswer(t, err)
	if code != http.StatusConflict || body.Error.Code != CodeProductTransitionNotAllowed {
		t.Fatalf("got %d %s, want 409 %s", code, body.Error.Code, CodeProductTransitionNotAllowed)
	}
	if CodeProductTransitionNotAllowed != "PRODUCT_TRANSITION_NOT_ALLOWED" {
		t.Fatalf("the code is %q, want PRODUCT_TRANSITION_NOT_ALLOWED", CodeProductTransitionNotAllowed)
	}
	d := body.Error.Details
	if d.Action != "approve" || d.ApprovalStatus != "draft" || len(d.AllowedFrom) != 2 {
		t.Fatalf("details = %+v", d)
	}
}

func TestApprovingAProductOfAnUnapprovedSellerIsItsOwnConflict(t *testing.T) {
	code, body := reviewAnswer(t, &postgres.ProductSellerNotApprovedError{SellerStatus: "suspended"})
	if code != http.StatusConflict || body.Error.Code != CodeProductSellerNotApproved {
		t.Fatalf("got %d %s, want 409 %s", code, body.Error.Code, CodeProductSellerNotApproved)
	}
	if CodeProductSellerNotApproved != "PRODUCT_SELLER_NOT_APPROVED" {
		t.Fatalf("the code is %q, want PRODUCT_SELLER_NOT_APPROVED", CodeProductSellerNotApproved)
	}
	if body.Error.Details.SellerStatus != "suspended" {
		t.Fatalf("details = %+v, want the shop's status", body.Error.Details)
	}
}

func TestAnAdminActionOnAnUnknownProductIsNotFound(t *testing.T) {
	code, body := reviewAnswer(t, postgres.ErrProductNotFound)
	if code != http.StatusNotFound || body.Error.Code != "PRODUCT_NOT_FOUND" {
		t.Fatalf("got %d %s, want 404 PRODUCT_NOT_FOUND", code, body.Error.Code)
	}
}

// The seller's own second submit used to be an unmapped sentinel: 500.
func TestASecondApplicationSubmitIsAConflictNotAnOutage(t *testing.T) {
	code, body := reviewAnswer(t, postgres.ErrAlreadySubmitted)
	if code != http.StatusConflict || body.Error.Code != "APPLICATION_ALREADY_SUBMITTED" {
		t.Fatalf("got %d %s, want 409 APPLICATION_ALREADY_SUBMITTED", code, body.Error.Code)
	}
}

// The seller-facing refusal keeps its own code: the admin one must not have
// taken it over.
func TestTheSellersOwnNotApprovedRefusalIsUnchanged(t *testing.T) {
	code, body := reviewAnswer(t, postgres.ErrSellerNotApproved)
	if code != http.StatusConflict || body.Error.Code != "SELLER_NOT_APPROVED" {
		t.Fatalf("got %d %s, want 409 SELLER_NOT_APPROVED", code, body.Error.Code)
	}
}

func TestAnUnrelatedErrorIsNotAReviewRefusal(t *testing.T) {
	code, body := reviewAnswer(t, errors.New("something else entirely"))
	if code != http.StatusInternalServerError {
		t.Fatalf("got %d %s, want the default 500", code, body.Error.Code)
	}
}
