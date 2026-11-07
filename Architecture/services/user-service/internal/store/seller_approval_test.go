package store

import (
	"testing"

	"github.com/atpost/user-service/internal/pages"
	"github.com/google/uuid"
)

// US-M3: seller approval links only the seller's own page and moves it only
// out of pending_review. Every other status stays where it was.
func TestDecideSellerApproval(t *testing.T) {
	owner := uuid.New()
	for _, tc := range []struct {
		name         string
		status       string
		sellerUserID uuid.UUID
		want         SellerApprovalOutcome
	}{
		{"own page under review is approved", pages.StatusPendingReview, owner, SellerApprovalLinkedApproved},
		{"own draft is linked, stays a draft", pages.StatusDraft, owner, SellerApprovalLinked},
		{"own approved page is linked", pages.StatusApproved, owner, SellerApprovalLinked},
		{"own disabled page stays disabled", pages.StatusDisabled, owner, SellerApprovalLinked},
		{"own suspended page stays suspended", pages.StatusSuspended, owner, SellerApprovalLinked},
		{"own rejected page stays rejected", pages.StatusRejected, owner, SellerApprovalLinked},
		{"legacy active is not a review state", "active", owner, SellerApprovalLinked},
		{"someone else's page under review", pages.StatusPendingReview, uuid.New(), SellerApprovalNotOwner},
		{"someone else's draft", pages.StatusDraft, uuid.New(), SellerApprovalNotOwner},
		{"event without the seller's user", pages.StatusPendingReview, uuid.Nil, SellerApprovalNotOwner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecideSellerApproval(tc.status, owner, tc.sellerUserID); got != tc.want {
				t.Fatalf("DecideSellerApproval(%q) = %q, want %q", tc.status, got, tc.want)
			}
		})
	}
}

// A page owned by nobody (uuid.Nil) must not match an event that also
// carries no user.
func TestDecideSellerApprovalNilOwnerNeverMatches(t *testing.T) {
	if got := DecideSellerApproval(pages.StatusPendingReview, uuid.Nil, uuid.Nil); got != SellerApprovalNotOwner {
		t.Fatalf("got %q, want %q", got, SellerApprovalNotOwner)
	}
}
