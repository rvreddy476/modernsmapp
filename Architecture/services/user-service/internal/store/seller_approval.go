package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/user-service/internal/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── commerce.seller.approved → the seller's business page (audit US-M3) ─────
//
// The consumer used to set seller_id and then status='active' on whatever
// business_page_id the event named. 'active' is not a lifecycle status, so
// that skipped review, revived disabled and suspended pages, and — because
// commerce stored the id from the seller's own request body unchecked —
// published someone else's draft page linked to the attacker's shop.
//
// Now the page is linked only when it belongs to the seller's own user, and
// approval moves it only out of pending_review (the owner asked for review;
// the seller's KYC approval is that review). Every other status is left as
// it is: a draft stays a draft, and disabled, suspended and rejected pages
// stay exactly where moderation put them.

// SellerApprovalOutcome says what a seller approval did to the named page.
type SellerApprovalOutcome string

const (
	// SellerApprovalPageMissing: no such page. Nothing written.
	SellerApprovalPageMissing SellerApprovalOutcome = "page_missing"
	// SellerApprovalNotOwner: the page belongs to someone else, or the event
	// did not say whose seller this is. Nothing written.
	SellerApprovalNotOwner SellerApprovalOutcome = "not_owner"
	// SellerApprovalLinked: seller_id set; status unchanged.
	SellerApprovalLinked SellerApprovalOutcome = "linked"
	// SellerApprovalLinkedApproved: seller_id set and pending_review → approved.
	SellerApprovalLinkedApproved SellerApprovalOutcome = "linked_approved"
)

// DecideSellerApproval is the whole rule, kept pure so it is tested without
// Postgres. sellerUserID is the commerce seller's user; uuid.Nil (an event
// that did not carry it) never matches an owner.
func DecideSellerApproval(status string, ownerID, sellerUserID uuid.UUID) SellerApprovalOutcome {
	if sellerUserID == uuid.Nil || ownerID != sellerUserID {
		return SellerApprovalNotOwner
	}
	if status == pages.StatusPendingReview && pages.CanTransition(status, pages.StatusApproved) {
		return SellerApprovalLinkedApproved
	}
	return SellerApprovalLinked
}

// ApplySellerApproval links sellerID to pageID and, when the rule allows,
// approves the page, in one transaction under a row lock so a concurrent
// lifecycle decision (a disable, say) cannot be overwritten. actorID is the
// commerce admin who approved the seller; it is recorded as the approver.
func (s *Store) ApplySellerApproval(ctx context.Context, pageID, sellerID, sellerUserID, actorID uuid.UUID) (SellerApprovalOutcome, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var status string
	var ownerID uuid.UUID
	err = tx.QueryRow(ctx,
		`SELECT status, user_id FROM business_pages WHERE id = $1 FOR UPDATE`, pageID).Scan(&status, &ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return SellerApprovalPageMissing, nil
	}
	if err != nil {
		return "", fmt.Errorf("lock page: %w", err)
	}

	outcome := DecideSellerApproval(status, ownerID, sellerUserID)
	if outcome == SellerApprovalNotOwner {
		return outcome, nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE business_pages SET seller_id = $2, updated_at = NOW() WHERE id = $1`, pageID, sellerID); err != nil {
		return "", fmt.Errorf("link seller: %w", err)
	}
	if outcome == SellerApprovalLinkedApproved {
		if err := applyPageStatus(ctx, tx, pageID, pages.StatusApproved, actorID, ""); err != nil {
			return "", fmt.Errorf("approve page: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return outcome, nil
}

// PageOwner returns a page's owner and status for the /internal ownership
// check commerce runs before it stores a seller's business_page_id. A missing
// page is (uuid.Nil, "", nil).
func (s *Store) PageOwner(ctx context.Context, pageID uuid.UUID) (uuid.UUID, string, error) {
	var owner uuid.UUID
	var status string
	err := s.db.QueryRow(ctx, `SELECT user_id, status FROM business_pages WHERE id = $1`, pageID).Scan(&owner, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", nil
	}
	return owner, status, err
}
