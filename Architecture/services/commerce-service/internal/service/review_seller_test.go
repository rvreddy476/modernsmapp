package service

import (
	"errors"
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Audit 2026-10-30 L2: the comparison is sellers.id to sellers.id.
func TestCheckReviewSeller(t *testing.T) {
	seller := uuid.New()
	r := &postgres.Review{SellerID: seller}
	if err := checkReviewSeller(r, seller); err != nil {
		t.Fatalf("the review's seller was refused: %v", err)
	}
	for name, id := range map[string]uuid.UUID{"another seller": uuid.New(), "nil": uuid.Nil} {
		if err := checkReviewSeller(r, id); !errors.Is(err, ErrNotReviewSeller) {
			t.Fatalf("%s: err = %v, want ErrNotReviewSeller", name, err)
		}
	}
	// A review with no seller recorded is nobody's to answer.
	if err := checkReviewSeller(&postgres.Review{}, uuid.Nil); !errors.Is(err, ErrNotReviewSeller) {
		t.Fatalf("nil seller on both sides matched: %v", err)
	}
}
