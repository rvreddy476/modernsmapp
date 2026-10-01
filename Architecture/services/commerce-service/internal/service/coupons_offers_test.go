package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/payments"
	"github.com/atpost/commerce-service/internal/store/postgres"
)

// The table is payments-service's own TestAllowedOfferDiscount
// (internal/gateway/offermatch_test.go, commit 13f600f5), case for case: the
// figure a buyer is shown must be the figure payments will allow.
func TestEstimatedOfferDiscountMatchesPayments(t *testing.T) {
	i64 := func(v int64) *int64 { return &v }
	pct := func(bps int64, max *int64) payments.Offer {
		return payments.Offer{DiscountType: "percentage", DiscountValue: bps, MaxDiscountMinor: max}
	}
	flat := func(v int64, max *int64) payments.Offer {
		return payments.Offer{DiscountType: "flat", DiscountValue: v, MaxDiscountMinor: max}
	}
	for _, tc := range []struct {
		name   string
		intent int64
		offer  payments.Offer
		want   int64
	}{
		{"10% of 1000.00", 100000, pct(1000, nil), 10000},
		{"10% floors", 99999, pct(1000, nil), 9999},
		{"12.5% floors", 1001, pct(1250, nil), 125},
		{"10% capped", 100000, pct(1000, i64(5000)), 5000},
		{"100%", 100000, pct(10000, nil), 100000},
		{"over 100% allows nothing", 100000, pct(10001, nil), 0},
		{"flat", 100000, flat(2500, nil), 2500},
		{"flat capped", 100000, flat(2500, i64(2000)), 2000},
		{"unknown type", 100000, payments.Offer{DiscountType: "bogo", DiscountValue: 1}, 0},
		{"zero intent", 0, pct(1000, nil), 0},
		{"overflow guard", 1 << 62, pct(1000, nil), 0},
	} {
		if got := EstimatedOfferDiscount(tc.intent, tc.offer); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

type fakeOffers struct {
	calls int
	items []payments.Offer
	err   error
}

func (f *fakeOffers) ListOffers(_ context.Context, amount int64) ([]payments.Offer, error) {
	f.calls++
	if amount != 0 {
		panic("the cache must ask for the unfiltered list")
	}
	return f.items, f.err
}

func TestPaymentOffers_FiltersSortsAndCaches(t *testing.T) {
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	ended := now.Add(-time.Minute).Format(time.RFC3339)
	later := now.Add(time.Hour).Format(time.RFC3339)
	capV := int64(150000)
	src := &fakeOffers{items: []payments.Offer{
		{ID: "a", Title: "Flat ₹100", DiscountType: "flat", DiscountValue: 10000},
		{ID: "b", Title: "10% HDFC", DiscountType: "percentage", DiscountValue: 1000, MaxDiscountMinor: &capV, EndsAt: &later},
		{ID: "c", Title: "Big spenders", DiscountType: "flat", DiscountValue: 90000, MinAmountMinor: 5000000},
		{ID: "d", Title: "Ended", DiscountType: "flat", DiscountValue: 99999, EndsAt: &ended},
	}}
	s := (&Service{}).withOffersReader(src).WithClock(func() time.Time { return now })

	got := s.PaymentOffers(context.Background(), 250000)
	if len(got) != 2 || got[0].ID != "b" || got[0].EstimatedDiscountMinor != 25000 ||
		got[1].ID != "a" || got[1].EstimatedDiscountMinor != 10000 {
		t.Fatalf("offers for ₹2,500 = %+v", got)
	}
	big := s.PaymentOffers(context.Background(), 6000000)
	if len(big) != 3 || big[0].ID != "b" || big[0].EstimatedDiscountMinor != 150000 || big[1].ID != "c" {
		t.Fatalf("offers for ₹60,000 = %+v", big)
	}
	if src.calls != 1 {
		t.Fatalf("payments asked %d times; the answer is cached", src.calls)
	}
}

func TestPaymentOffers_FailSoft(t *testing.T) {
	src := &fakeOffers{err: errors.New("payments down")}
	s := (&Service{}).withOffersReader(src)
	if got := s.PaymentOffers(context.Background(), 1000); got == nil || len(got) != 0 {
		t.Fatalf("got %v, want an empty list", got)
	}
	none := &Service{}
	if got := none.PaymentOffers(context.Background(), 1000); got == nil || len(got) != 0 {
		t.Fatalf("no payments client: got %v", got)
	}
}

func TestOrderDetailPaymentOfferView(t *testing.T) {
	title := "10% off with HDFC"
	disc, captured := int64(12500), int64(121300)
	final := int64(133800)
	paid := &postgres.Order{PaymentStatus: "paid", FinalAmountMinor: final}
	view, amount := paymentOfferView(paid, &postgres.OrderPaymentOffer{Title: &title, DiscountMinor: &disc, CapturedMinor: &captured})
	if view == nil || view.Title != title || view.DiscountMinor != 12500 || amount == nil || *amount != 121300 {
		t.Fatalf("offer: %+v %v", view, amount)
	}
	view, amount = paymentOfferView(paid, &postgres.OrderPaymentOffer{})
	if view != nil || amount == nil || *amount != final {
		t.Fatalf("paid, no offer: %+v %v", view, amount)
	}
	pending := &postgres.Order{PaymentStatus: "pending", FinalAmountMinor: final}
	if view, amount = paymentOfferView(pending, &postgres.OrderPaymentOffer{}); view != nil || amount != nil {
		t.Fatalf("unpaid: %+v %v", view, amount)
	}
	if note := invoiceBankOfferNote(&postgres.OrderPaymentOffer{DiscountMinor: &disc}); note != "Bank offer applied at payment: −₹125" {
		t.Fatalf("note = %q", note)
	}
	if note := invoiceBankOfferNote(&postgres.OrderPaymentOffer{}); note != "" {
		t.Fatalf("no offer, note = %q", note)
	}
}
