package postgres

// The state guards of the admin review actions, as far as they can be checked
// without a database: which states each action may move from, that the one
// UPDATE each action runs carries that set (and, for a product approval, the
// approved-seller condition), and how a refused UPDATE is explained.
//
// That the guard holds against real rows and against two concurrent admins is
// adminreview_integration_test.go (CI only).

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Every value sellers.status may hold (migration 001's CHECK).
var allSellerStatuses = []string{
	"draft", "submitted", "under_review", "changes_required",
	"approved", "rejected", "suspended", "disabled",
}

// Every value products.approval_status may hold (migration 046's CHECK).
var allProductApprovalStatuses = []string{
	"draft", "submitted", "under_review", "pending", "approved", "rejected",
	"changes_requested", "flagged", "hidden", "archived",
}

func TestSellerActionsMoveOnlyFromTheirOwnStates(t *testing.T) {
	queue := map[string]bool{"submitted": true, "under_review": true, "changes_required": true}
	want := map[string]map[string]bool{
		SellerActionApprove:        queue,
		SellerActionReject:         queue,
		SellerActionRequestChanges: queue,
		SellerActionSuspend:        {"approved": true},
		SellerActionUnsuspend:      {"suspended": true},
	}
	if len(sellerActionFrom) != len(want) {
		t.Fatalf("sellerActionFrom has %d actions, this test knows %d: add the new one here", len(sellerActionFrom), len(want))
	}
	for action, allowed := range want {
		from := SellerActionAllowedFrom(action)
		for _, status := range allSellerStatuses {
			if got := contains(from, status); got != allowed[status] {
				t.Errorf("%s from %q: allowed=%v, want %v", action, status, got, allowed[status])
			}
		}
		if len(from) != len(allowed) {
			t.Errorf("%s may move from %v, want exactly %d states", action, from, len(allowed))
		}
	}
}

// The finding itself, spelled out: no action reaches `approved` from a seller
// that never submitted, directly or through another action.
func TestNoSellerActionApprovesADraft(t *testing.T) {
	for action := range sellerActionFrom {
		if contains(SellerActionAllowedFrom(action), "draft") {
			t.Errorf("%s may move a draft seller: a shop that never submitted its application "+
				"(no KYC document, no GSTIN or PAN, no payout account) must not be actionable", action)
		}
	}
	for _, s := range []string{"draft", "approved", "rejected", "suspended", "disabled"} {
		if contains(SellerActionAllowedFrom(SellerActionApprove), s) {
			t.Errorf("approve may move a %s seller", s)
		}
	}
	// The two side doors: suspend must start from approved, or suspend then
	// unsuspend approves anything.
	if got := SellerActionAllowedFrom(SellerActionSuspend); len(got) != 1 || got[0] != "approved" {
		t.Errorf("suspend may move from %v, want approved only", got)
	}
	if got := SellerActionAllowedFrom(SellerActionUnsuspend); len(got) != 1 || got[0] != "suspended" {
		t.Errorf("unsuspend may move from %v, want suspended only", got)
	}
}

func TestProductActionsMoveOnlyFromTheirOwnStates(t *testing.T) {
	want := map[string]map[string]bool{
		ProductActionApprove:        {"submitted": true, "under_review": true},
		ProductActionReject:         {"submitted": true, "under_review": true, "approved": true},
		ProductActionRequestChanges: {"submitted": true, "under_review": true, "approved": true},
	}
	if len(productActionFrom) != len(want) {
		t.Fatalf("productActionFrom has %d actions, this test knows %d: add the new one here", len(productActionFrom), len(want))
	}
	for action, allowed := range want {
		from := ProductActionAllowedFrom(action)
		for _, status := range allProductApprovalStatuses {
			if got := contains(from, status); got != allowed[status] {
				t.Errorf("%s from %q: allowed=%v, want %v", action, status, got, allowed[status])
			}
		}
		if len(from) != len(allowed) {
			t.Errorf("%s may move from %v, want exactly %d states", action, from, len(allowed))
		}
	}
}

func TestOnlyAProductApprovalNeedsAnApprovedSeller(t *testing.T) {
	if !productActionNeedsApprovedSeller(ProductActionApprove) {
		t.Error("approving a product does not ask whether its seller is approved")
	}
	for _, a := range []string{ProductActionReject, ProductActionRequestChanges} {
		if productActionNeedsApprovedSeller(a) {
			t.Errorf("%s asks for an approved seller: a suspended shop's listing could not be taken down", a)
		}
	}
}

func TestAllowedFromIsACopy(t *testing.T) {
	SellerActionAllowedFrom(SellerActionApprove)[0] = "draft"
	ProductActionAllowedFrom(ProductActionApprove)[0] = "draft"
	if contains(sellerActionFrom[SellerActionApprove], "draft") || contains(productActionFrom[ProductActionApprove], "draft") {
		t.Fatal("a caller could widen a guard by writing into the slice it was handed")
	}
	if SellerActionAllowedFrom("promote") != nil || ProductActionAllowedFrom("promote") != nil {
		t.Fatal("an unknown action has an allowed-from set")
	}
}

// oneLine folds whitespace so the assertions below do not depend on layout.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestTheSellerUpdateCarriesTheStateGuard(t *testing.T) {
	q := oneLine(sellerMoveSQL(`status='approved', updated_at=$3`))
	want := "UPDATE sellers SET status='approved', updated_at=$3 WHERE id = $1 AND status = ANY($2::text[]) RETURNING user_id"
	if q != want {
		t.Fatalf("seller UPDATE is\n  %s\nwant\n  %s\nThe state guard must be in the statement itself: "+
			"a check-then-write lets two admins both win", q, want)
	}
}

func TestTheProductUpdateCarriesTheStateGuardAndApprovalTheSellerGuard(t *testing.T) {
	const set = `approval_status='approved', updated_at=$3`
	base := "UPDATE products SET " + set + " WHERE id = $1 AND approval_status = ANY($2::text[])"
	sellerGuard := " AND EXISTS (SELECT 1 FROM sellers s WHERE s.id = products.seller_id AND s.status = 'approved')"

	if got := oneLine(productMoveSQL(ProductActionApprove, set)); got != base+sellerGuard {
		t.Errorf("product approval UPDATE is\n  %s\nwant\n  %s", got, base+sellerGuard)
	}
	for _, a := range []string{ProductActionReject, ProductActionRequestChanges} {
		if got := oneLine(productMoveSQL(a, set)); got != base {
			t.Errorf("%s UPDATE is\n  %s\nwant\n  %s", a, got, base)
		}
	}
}

// An UPDATE that matched no row is a refusal, never a success.
func TestOnlyOneMovedRowIsASuccess(t *testing.T) {
	if !movedOneRow(1) {
		t.Error("an UPDATE that moved the row is not a success")
	}
	for _, n := range []int64{0, -1, 2} {
		if movedOneRow(n) {
			t.Errorf("an UPDATE that affected %d rows reads as a successful decision", n)
		}
	}
}

func TestARefusedSellerActionIsNotFoundOrWrongState(t *testing.T) {
	if err := sellerMoveRefusal(SellerActionApprove, "", pgx.ErrNoRows); !errors.Is(err, ErrSellerNotFound) {
		t.Errorf("no such seller: %v, want ErrSellerNotFound", err)
	}
	if err := sellerMoveRefusal(SellerActionApprove, "", fmt.Errorf("read: %w", pgx.ErrNoRows)); !errors.Is(err, ErrSellerNotFound) {
		t.Errorf("wrapped no-rows: %v, want ErrSellerNotFound", err)
	}
	boom := errors.New("connection reset")
	if err := sellerMoveRefusal(SellerActionApprove, "", boom); !errors.Is(err, boom) ||
		errors.Is(err, ErrSellerNotFound) || errors.Is(err, ErrSellerTransitionNotAllowed) {
		t.Errorf("a failed read became %v: an outage must not read as a refusal", err)
	}

	err := sellerMoveRefusal(SellerActionApprove, "draft", nil)
	var te *SellerTransitionError
	if !errors.As(err, &te) || !errors.Is(err, ErrSellerTransitionNotAllowed) {
		t.Fatalf("a draft seller: %v, want a *SellerTransitionError", err)
	}
	if errors.Is(err, ErrSellerNotFound) {
		t.Error("a seller in the wrong state reads as not found")
	}
	if te.Action != SellerActionApprove || te.Status != "draft" ||
		strings.Join(te.AllowedFrom, ",") != "submitted,under_review,changes_required" {
		t.Errorf("refusal carries %+v", te)
	}
}

func TestARefusedProductActionIsNotFoundWrongStateOrUnapprovedSeller(t *testing.T) {
	if err := productMoveRefusal(ProductActionApprove, "", "", pgx.ErrNoRows); !errors.Is(err, ErrProductNotFound) {
		t.Errorf("no such product: %v, want ErrProductNotFound", err)
	}
	boom := errors.New("connection reset")
	if err := productMoveRefusal(ProductActionApprove, "", "", boom); !errors.Is(err, boom) ||
		errors.Is(err, ErrProductNotFound) || errors.Is(err, ErrProductTransitionNotAllowed) {
		t.Errorf("a failed read became %v", err)
	}

	// Never submitted: the product's own state is the refusal, whatever the
	// seller is.
	for _, sellerStatus := range []string{"approved", "draft"} {
		err := productMoveRefusal(ProductActionApprove, "draft", sellerStatus, nil)
		var te *ProductTransitionError
		if !errors.As(err, &te) || te.ApprovalStatus != "draft" || te.Action != ProductActionApprove ||
			strings.Join(te.AllowedFrom, ",") != "submitted,under_review" {
			t.Errorf("a draft product of a %s seller: %v (%+v), want a *ProductTransitionError", sellerStatus, err, te)
		}
		if errors.Is(err, ErrProductSellerNotApproved) || errors.Is(err, ErrProductNotFound) {
			t.Errorf("a draft product of a %s seller reads as %v", sellerStatus, err)
		}
	}

	// In the queue, and the shop is not approved.
	for _, sellerStatus := range []string{"suspended", "changes_required", "rejected", "draft", ""} {
		err := productMoveRefusal(ProductActionApprove, "submitted", sellerStatus, nil)
		var se *ProductSellerNotApprovedError
		if !errors.As(err, &se) || !errors.Is(err, ErrProductSellerNotApproved) || se.SellerStatus != sellerStatus {
			t.Errorf("a submitted product of a %q seller: %v, want a *ProductSellerNotApprovedError", sellerStatus, err)
		}
		if errors.Is(err, ErrSellerNotApproved) {
			t.Error("the admin refusal is the seller-facing ErrSellerNotApproved: the seller's submit and the admin's approval would share one message")
		}
	}

	// A reject never asks about the seller.
	err := productMoveRefusal(ProductActionReject, "draft", "suspended", nil)
	if !errors.Is(err, ErrProductTransitionNotAllowed) || errors.Is(err, ErrProductSellerNotApproved) {
		t.Errorf("rejecting a draft of a suspended seller: %v, want the product's own state", err)
	}

	// The row moved between the UPDATE and the read (a concurrent decision):
	// still a refusal, never a success.
	if err := productMoveRefusal(ProductActionApprove, "submitted", "approved", nil); !errors.Is(err, ErrProductTransitionNotAllowed) {
		t.Errorf("an UPDATE that matched nothing became %v", err)
	}
}
