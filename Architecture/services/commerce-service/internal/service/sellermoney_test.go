package service

// Two things the seller's surfaces got wrong, and one the KYC step did.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// seller_subtotal reported 0 on a ₹929 order.
//
// The arithmetic was never wrong: it summed `order_items.final_price`, the
// NUMERIC column migration 007 stopped maintaining. Adding up a column
// nobody writes gives zero however carefully you add it up.
//
// This also pins the multi-seller split, because the number is what ONE
// seller is owed for the order, not the order's total.
func TestSellerSubtotalSumsTheSellersOwnLinesInPaise(t *testing.T) {
	mine, theirs := uuid.New(), uuid.New()
	items := []*postgres.OrderItem{
		// The shape every P0 line has: paise set, rupee mirror at 0.
		{SellerID: mine, FinalPriceMinor: 88000, FinalPrice: 0},
		{SellerID: mine, FinalPriceMinor: 4900, FinalPrice: 0},
		{SellerID: theirs, FinalPriceMinor: 250000, FinalPrice: 0},
	}

	lines, subtotalMinor := sellerLines(items, mine)

	if len(lines) != 2 {
		t.Fatalf("got %d lines for this seller, want 2 — a seller must not see another "+
			"seller's items in a shared order", len(lines))
	}
	if subtotalMinor != 92900 {
		t.Fatalf("seller subtotal = %d paise, want 92900 (₹929.00). Summing final_price "+
			"instead of final_price_minor is what reported ₹0 on a ₹929 order", subtotalMinor)
	}
}

// A line written before migration 007 still counts, through the rupee mirror.
func TestSellerSubtotalStillCountsAPre007Line(t *testing.T) {
	me := uuid.New()
	_, subtotalMinor := sellerLines([]*postgres.OrderItem{{SellerID: me, FinalPrice: 929}}, me)
	if subtotalMinor != 92900 {
		t.Fatalf("seller subtotal = %d paise, want 92900 from the rupee mirror", subtotalMinor)
	}
}

// A seller with no lines in the order gets nothing — this is the check
// GetSellerOrderDetail turns into ErrNotOrderOwner.
func TestASellerWithNoLinesGetsNothing(t *testing.T) {
	lines, subtotal := sellerLines([]*postgres.OrderItem{{SellerID: uuid.New(), FinalPriceMinor: 100}}, uuid.New())
	if len(lines) != 0 || subtotal != 0 {
		t.Fatalf("got %d lines / %d paise for a seller with no items in the order", len(lines), subtotal)
	}
}

// An unknown document_type is refused BEFORE anything touches Postgres, and
// the refusal names the vocabulary.
//
// The store has no chance to be reached here: the service has a nil store,
// so if validation did not run first this test would panic rather than
// return — which is precisely the ordering the fix depends on. Previously
// the value travelled all the way to the CHECK constraint and came back as
// an unmapped 500 with no hint of what was acceptable.
func TestAnUnknownDocumentTypeIsRefusedBeforeReachingPostgres(t *testing.T) {
	svc := &Service{} // no store, no media client: nothing downstream is reachable

	err := svc.SaveDocuments(context.Background(), uuid.New(), []postgres.SellerDocument{
		{DocumentType: "pan_card", MediaID: uuid.New()},
		{DocumentType: "drivers_license", MediaID: uuid.New()},
	})

	if !errors.Is(err, ErrInvalidDocumentType) {
		t.Fatalf("err = %v, want ErrInvalidDocumentType", err)
	}
	if !strings.Contains(err.Error(), "drivers_license") {
		t.Errorf("the error does not say which value was rejected: %v", err)
	}
	for _, want := range []string{"pan_card", "aadhaar", "cancelled_cheque"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not offer %q as an alternative: %v", want, err)
		}
	}
}
