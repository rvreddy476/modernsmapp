package service

import (
	"testing"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// A dispute is shown only to the user who opened it (review 2026-11-07).
// Anyone else, including a caller with no identity, sees what an unknown
// id shows: nothing.
func TestDisputeVisibleOnlyToItsOwner(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	d := &postgres.Dispute{ID: uuid.New(), UserID: owner, TransactionID: uuid.New(), Status: "open"}

	if got := disputeVisibleTo(d, owner); got != d {
		t.Fatal("the owner must see their own dispute")
	}
	if got := disputeVisibleTo(d, other); got != nil {
		t.Fatalf("another user saw the dispute: %+v", got)
	}
	if got := disputeVisibleTo(d, uuid.Nil); got != nil {
		t.Fatalf("a nil caller saw the dispute: %+v", got)
	}
	orphan := &postgres.Dispute{ID: uuid.New()}
	if got := disputeVisibleTo(orphan, uuid.Nil); got != nil {
		t.Fatal("a dispute with no owner must not match a nil caller")
	}
	if got := disputeVisibleTo(nil, owner); got != nil {
		t.Fatal("no dispute, nothing shown")
	}
}
