package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// Nothing writes these columns with a claim yet, so the creates refuse a
// media id before touching the database (the Store here has none: a create
// that got past the refusal would panic).
func TestCreatesRefuseAnUnclaimedMediaID(t *testing.T) {
	s := &Store{}
	id := uuid.New()
	if _, err := s.CreateFundraiser(context.Background(), &Fundraiser{CoverMediaID: &id}); !errors.Is(err, ErrMediaClaimRequired) {
		t.Fatalf("fundraiser with a cover = %v, want ErrMediaClaimRequired", err)
	}
	if err := s.CreateInvoice(context.Background(), &Invoice{PDFMediaID: &id}); !errors.Is(err, ErrMediaClaimRequired) {
		t.Fatalf("invoice with a PDF = %v, want ErrMediaClaimRequired", err)
	}
}

func TestMediaHolderLockWaitsAndIsTransactionScoped(t *testing.T) {
	if lockMediaHolderSQL != `SELECT pg_advisory_xact_lock($1)` {
		t.Fatalf("lock statement is %q; want the waiting, exclusive, transaction-scoped advisory lock", lockMediaHolderSQL)
	}
}

func TestMediaHolderLockKeyIsPerRowAndTable(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if mediaHolderLockKey(HolderFundraiserCover, a) != mediaHolderLockKey(HolderFundraiserCover, a) {
		t.Fatal("unstable key")
	}
	if mediaHolderLockKey(HolderFundraiserCover, a) == mediaHolderLockKey(HolderFundraiserCover, b) ||
		mediaHolderLockKey(HolderFundraiserCover, a) == mediaHolderLockKey(HolderInvoicePDF, a) {
		t.Fatal("two holders share a lock key")
	}
}

func TestEveryHolderHasItsQueries(t *testing.T) {
	for _, h := range []MediaHolderTable{HolderFundraiserCover, HolderInvoicePDF} {
		q, ok := holderSQL[h]
		if !ok || q.page == "" || q.one == "" {
			t.Fatalf("%s has no page/re-read query", h)
		}
	}
	if _, err := (&Store{}).MediaHolderRows(context.Background(), "nope", uuid.Nil, 1, false); err == nil {
		t.Fatal("an unknown holder was read")
	}
}
