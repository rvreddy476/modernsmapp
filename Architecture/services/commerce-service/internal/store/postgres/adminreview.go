package postgres

// The state guards of the admin review actions on sellers and products.
//
// Every admin decision used to be `UPDATE ... WHERE id = $1`: the row moved
// whatever state it was in. So an admin could approve a `draft` shop that had
// never submitted its application (no KYC document, no GSTIN or PAN, no payout
// account), approve a product its seller had never sent for review, and reach
// `approved` by two side doors: request-changes on a draft (the queue then
// offers approve), and suspend followed by unsuspend.
//
// Each action now names the states it may move FROM, and the UPDATE carries
// that set in its own WHERE clause. Postgres re-checks the clause after it
// takes the row lock, so of two concurrent decisions on one row exactly one
// writes; the other finds the row already moved and is refused.
//
// A refused action tells "no such row" (404) from "wrong state" (409) with a
// second read in the same transaction. Nothing is written on either.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The seller review actions (seller_onboarding_reviews.action).
const (
	SellerActionApprove        = "approve"
	SellerActionReject         = "reject"
	SellerActionRequestChanges = "request_changes"
	SellerActionSuspend        = "suspend"
	SellerActionUnsuspend      = "unsuspend"
)

// sellerInReview is the review queue (ListSellerQueue shows exactly these):
// the application was submitted, which SubmitApplication allows only when it
// is complete, and has not been decided.
//
// `changes_required` is in the set because it is reachable only from this set
// (request_changes below) and there is no route by which a seller resubmits
// from it: SubmitSellerApplication accepts `draft` alone. The reviewer's next
// decision is the only way out of it.
var sellerInReview = []string{"submitted", "under_review", "changes_required"}

// sellerActionFrom is what each action may move a seller FROM.
//
//	approve, reject, request_changes   the review queue only. Never a draft
//	                                   (never submitted), never a rejected,
//	                                   approved or suspended seller.
//	suspend                            a live shop only. Suspending a draft
//	                                   and lifting the suspension was a way
//	                                   to approve it.
//	unsuspend                          a suspended shop only.
var sellerActionFrom = map[string][]string{
	SellerActionApprove:        sellerInReview,
	SellerActionReject:         sellerInReview,
	SellerActionRequestChanges: sellerInReview,
	SellerActionSuspend:        {"approved"},
	SellerActionUnsuspend:      {"suspended"},
}

// SellerActionAllowedFrom returns a copy of the states an action may move a
// seller from; nil for an unknown action.
func SellerActionAllowedFrom(action string) []string {
	return append([]string(nil), sellerActionFrom[action]...)
}

// An admin action on a seller id that does not exist is ErrSellerNotFound
// (sellerdocs_admin.go).

// ErrSellerTransitionNotAllowed is an admin action on a seller that is not in
// a state the action may move from. It arrives as a *SellerTransitionError.
var ErrSellerTransitionNotAllowed = errors.New("the seller is not in a state this action applies to")

// SellerTransitionError names the refused action and the state the seller is
// actually in.
type SellerTransitionError struct {
	Action      string
	Status      string
	AllowedFrom []string
}

func (e *SellerTransitionError) Error() string {
	return fmt.Sprintf("%s: %s refused, seller status is %q", ErrSellerTransitionNotAllowed, e.Action, e.Status)
}
func (e *SellerTransitionError) Unwrap() error { return ErrSellerTransitionNotAllowed }

// sellerMoveSQL is the one guarded seller UPDATE. $1 is the seller id and $2
// the states the action may move from; `set` uses $3 onwards.
func sellerMoveSQL(set string) string {
	return `UPDATE sellers SET ` + set + `
		  WHERE id = $1 AND status = ANY($2::text[]) RETURNING user_id`
}

// moveSeller runs one admin action's guarded UPDATE inside tx and returns the
// seller's user id. args are the values of $3 onwards in `set`.
func moveSeller(ctx context.Context, tx pgx.Tx, action string, sellerID uuid.UUID, set string, args ...any) (uuid.UUID, error) {
	from := sellerActionFrom[action]
	if len(from) == 0 {
		return uuid.Nil, fmt.Errorf("commerce: unknown seller review action %q", action)
	}
	var userID uuid.UUID
	err := tx.QueryRow(ctx, sellerMoveSQL(set), append([]any{sellerID, from}, args...)...).Scan(&userID)
	if err == nil {
		return userID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	var status string
	readErr := tx.QueryRow(ctx, `SELECT status FROM sellers WHERE id = $1`, sellerID).Scan(&status)
	return uuid.Nil, sellerMoveRefusal(action, status, readErr)
}

// sellerMoveRefusal explains a guarded UPDATE that matched no row, from the
// read that followed it: no such seller, or a seller in the wrong state.
func sellerMoveRefusal(action, status string, readErr error) error {
	if errors.Is(readErr, pgx.ErrNoRows) {
		return ErrSellerNotFound
	}
	if readErr != nil {
		return readErr
	}
	return &SellerTransitionError{Action: action, Status: status, AllowedFrom: SellerActionAllowedFrom(action)}
}

// ─── Products ───────────────────────────────────────────────────────────

// The product moderation actions (product_moderation_log.action).
const (
	ProductActionApprove        = "approve"
	ProductActionReject         = "reject"
	ProductActionRequestChanges = "request_changes"
)

// productActionFrom is what each action may move a product's approval_status
// FROM.
//
//	approve                   the moderation queue only (ListProductQueue):
//	                          the seller sent it for review, which
//	                          SubmitProduct allows only for a complete
//	                          listing of an approved shop.
//	reject, request_changes   the queue, or a live listing (the takedown: a
//	                          rejected listing leaves the storefront and the
//	                          search index). Never a draft the seller has not
//	                          submitted, and never a listing already sent
//	                          back, which is in the seller's hands.
var productActionFrom = map[string][]string{
	ProductActionApprove:        {"submitted", "under_review"},
	ProductActionReject:         {"submitted", "under_review", "approved"},
	ProductActionRequestChanges: {"submitted", "under_review", "approved"},
}

// ProductActionAllowedFrom returns a copy of the approval states an action
// may move a product from; nil for an unknown action.
func ProductActionAllowedFrom(action string) []string {
	return append([]string(nil), productActionFrom[action]...)
}

// productActionNeedsApprovedSeller: only an approval puts goods on sale, so
// only an approval asks whether the shop itself is approved. A seller can be
// suspended or sent back for changes while a listing waits in the queue.
func productActionNeedsApprovedSeller(action string) bool {
	return action == ProductActionApprove
}

// ErrProductTransitionNotAllowed is a moderation action on a product whose
// approval_status the action may not move from. It arrives as a
// *ProductTransitionError.
var ErrProductTransitionNotAllowed = errors.New("the product is not in a state this action applies to")

// ProductTransitionError names the refused action and the product's state.
type ProductTransitionError struct {
	Action         string
	ApprovalStatus string
	AllowedFrom    []string
}

func (e *ProductTransitionError) Error() string {
	return fmt.Sprintf("%s: %s refused, approval_status is %q", ErrProductTransitionNotAllowed, e.Action, e.ApprovalStatus)
}
func (e *ProductTransitionError) Unwrap() error { return ErrProductTransitionNotAllowed }

// ErrProductSellerNotApproved is an approval of a product whose shop is not
// approved. Distinct from ErrSellerNotApproved, which is the seller's own
// submit being told their shop is still in review. It arrives as a
// *ProductSellerNotApprovedError.
var ErrProductSellerNotApproved = errors.New("the product's seller is not approved")

// ProductSellerNotApprovedError carries the shop's status.
type ProductSellerNotApprovedError struct{ SellerStatus string }

func (e *ProductSellerNotApprovedError) Error() string {
	return fmt.Sprintf("%s: seller status is %q", ErrProductSellerNotApproved, e.SellerStatus)
}
func (e *ProductSellerNotApprovedError) Unwrap() error { return ErrProductSellerNotApproved }

// productMoveSQL is the one guarded moderation UPDATE. $1 is the product id
// and $2 the approval states the action may move from; `set` uses $3 onwards.
func productMoveSQL(action, set string) string {
	q := `UPDATE products SET ` + set + `
		  WHERE id = $1 AND approval_status = ANY($2::text[])`
	if productActionNeedsApprovedSeller(action) {
		q += `
		    AND EXISTS (SELECT 1 FROM sellers s
		                 WHERE s.id = products.seller_id AND s.status = 'approved')`
	}
	return q
}

// movedOneRow: the guarded UPDATE is by primary key, so it moved the row
// (1) or it was refused (0). Anything but 1 is a refusal.
func movedOneRow(rowsAffected int64) bool { return rowsAffected == 1 }

// moveProduct runs one moderation action's guarded UPDATE inside tx. args
// are the values of $3 onwards in `set`.
func moveProduct(ctx context.Context, tx pgx.Tx, action string, productID uuid.UUID, set string, args ...any) error {
	from := productActionFrom[action]
	if len(from) == 0 {
		return fmt.Errorf("commerce: unknown product moderation action %q", action)
	}
	tag, err := tx.Exec(ctx, productMoveSQL(action, set), append([]any{productID, from}, args...)...)
	if err != nil {
		return err
	}
	if movedOneRow(tag.RowsAffected()) {
		return nil
	}
	var approval, sellerStatus string
	readErr := tx.QueryRow(ctx, `
		SELECT p.approval_status, COALESCE(s.status, '')
		  FROM products p LEFT JOIN sellers s ON s.id = p.seller_id
		 WHERE p.id = $1`, productID).Scan(&approval, &sellerStatus)
	return productMoveRefusal(action, approval, sellerStatus, readErr)
}

// productMoveRefusal explains a guarded UPDATE that matched no row: no such
// product, a product in the wrong state, or (approval only) a shop that is
// not approved. The product's own state is reported first.
func productMoveRefusal(action, approval, sellerStatus string, readErr error) error {
	if errors.Is(readErr, pgx.ErrNoRows) {
		return ErrProductNotFound
	}
	if readErr != nil {
		return readErr
	}
	if contains(productActionFrom[action], approval) &&
		productActionNeedsApprovedSeller(action) && sellerStatus != "approved" {
		return &ProductSellerNotApprovedError{SellerStatus: sellerStatus}
	}
	return &ProductTransitionError{Action: action, ApprovalStatus: approval, AllowedFrom: ProductActionAllowedFrom(action)}
}
