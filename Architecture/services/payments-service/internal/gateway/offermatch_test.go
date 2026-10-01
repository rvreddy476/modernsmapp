package gateway

// The bank-offer capture rule, clause by clause, and the Razorpay adapter's
// two offer calls against Razorpay's documented shapes on an httptest server.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func i64(v int64) *int64 { return &v }

// baseCapture is an ACCEPTED offer capture: ₹1,000.00 intent, 10% up to
// ₹50.00 (so ₹50.00 allowed), minimum ₹500.00, paid ₹950.00.
func baseCapture() OfferCapture {
	paid := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	return OfferCapture{
		Operation: "test", Identifier: "pay_ABCDEF123456",
		Application: "mstore", Provider: "razorpay",
		Intent:           Money{Minor: 100000, Currency: "INR"},
		Captured:         Money{Minor: 95000, Currency: "INR"},
		PaidAt:           paid,
		ProviderOfferIDs: []string{"offer_HDFC10pct"},
		Registry: []OfferTerms{{
			OfferID: "11111111-1111-1111-1111-111111111111", Application: "mstore", Provider: "razorpay",
			ProviderOfferID: "offer_HDFC10pct", Known: true, Title: "10% off with HDFC credit cards",
			FundedBy: "bank", Active: true, StartsAt: paid.Add(-24 * time.Hour),
			DiscountType: OfferDiscountPercentage, DiscountValue: 1000, MaxDiscountMinor: i64(5000),
			MinAmountMinor: 50000,
		}},
	}
}

func TestMatchOfferCapture_AcceptRefuseTable(t *testing.T) {
	paid := baseCapture().PaidAt
	cases := []struct {
		name   string
		mutate func(c *OfferCapture)
		want   error // nil = accepted
	}{
		{"accepted at exactly the cap", func(c *OfferCapture) {}, nil},
		{"accepted below the cap", func(c *OfferCapture) { c.Captured.Minor = 97000 }, nil},
		{"unknown offer id", func(c *OfferCapture) { c.ProviderOfferIDs = []string{"offer_NOTOURS999"} }, ErrOfferUnknown},
		{"registry offer of another provider", func(c *OfferCapture) { c.Registry[0].Provider = "cashfree" }, ErrOfferUnknown},
		{"inactive at payment time", func(c *OfferCapture) { c.Registry[0].Active = false }, ErrOfferInactive},
		{"not yet registered at payment time", func(c *OfferCapture) { c.Registry[0].Known = false }, ErrOfferInactive},
		{"not yet started", func(c *OfferCapture) { c.Registry[0].StartsAt = paid.Add(time.Second) }, ErrOfferInactive},
		{"expired", func(c *OfferCapture) { e := paid.Add(-time.Second); c.Registry[0].EndsAt = &e }, ErrOfferExpired},
		{"ends exactly at payment time", func(c *OfferCapture) { e := paid; c.Registry[0].EndsAt = &e }, ErrOfferExpired},
		{"discount one paise over the cap", func(c *OfferCapture) { c.Captured.Minor = 94999 }, ErrOfferDiscountExceedsCap},
		{"capture above the intent", func(c *OfferCapture) { c.Captured.Minor = 100001 }, ErrOfferCaptureNotLower},
		{"capture equal to the intent", func(c *OfferCapture) { c.Captured.Minor = 100000 }, ErrOfferCaptureNotLower},
		{"intent below the minimum", func(c *OfferCapture) {
			c.Intent.Minor, c.Captured.Minor = 49999, 47999
		}, ErrOfferBelowMinimum},
		{"another application's offer", func(c *OfferCapture) { c.Registry[0].Application = "feast" }, ErrOfferWrongApplication},
		{"no offer named", func(c *OfferCapture) { c.ProviderOfferIDs = nil }, ErrOfferNotNamed},
		{"two offers named", func(c *OfferCapture) {
			c.ProviderOfferIDs = []string{"offer_HDFC10pct", "offer_OTHER12345"}
		}, ErrOfferAmbiguous},
		{"currency differs", func(c *OfferCapture) { c.Captured.Currency = "USD" }, ErrOfferCaptureNotLower},
		{"currency blank", func(c *OfferCapture) { c.Captured.Currency = "" }, ErrOfferCaptureNotLower},
		{"no payment time", func(c *OfferCapture) { c.PaidAt = time.Time{} }, ErrOfferNoPaymentTime},
		{"blank payment id", func(c *OfferCapture) { c.Identifier = " " }, ErrOfferNotNamed},
		{"zero capture", func(c *OfferCapture) { c.Captured.Minor = 0 }, ErrOfferCaptureNotLower},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := baseCapture()
			tc.mutate(&c)
			m, err := MatchOfferCapture(c)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				if m.DiscountMinor != c.Intent.Minor-c.Captured.Minor || m.AllowedMinor != 5000 ||
					m.Terms.OfferID != c.Registry[0].OfferID {
					t.Fatalf("match = %+v", m)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if !errors.Is(err, ErrProviderMoneyUnverified) {
				t.Fatalf("a refusal must stay a money refusal: %v", err)
			}
		})
	}
}

func TestAllowedOfferDiscount(t *testing.T) {
	pct := func(bps int64, max *int64) OfferTerms {
		return OfferTerms{DiscountType: OfferDiscountPercentage, DiscountValue: bps, MaxDiscountMinor: max}
	}
	flat := func(v int64, max *int64) OfferTerms {
		return OfferTerms{DiscountType: OfferDiscountFlat, DiscountValue: v, MaxDiscountMinor: max}
	}
	for _, tc := range []struct {
		name   string
		intent int64
		terms  OfferTerms
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
		{"unknown type", 100000, OfferTerms{DiscountType: "bogo", DiscountValue: 1}, 0},
		{"zero intent", 0, pct(1000, nil), 0},
		{"overflow guard", 1 << 62, pct(1000, nil), 0},
	} {
		if got := AllowedOfferDiscount(tc.intent, tc.terms); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

// ─── Razorpay adapter ────────────────────────────────────────────────

type recordedReq struct {
	method, path, rawQuery, body string
}

func offerServer(t *testing.T, reply string) (*RazorpayProvider, *[]recordedReq) {
	t.Helper()
	var (
		mu  sync.Mutex
		got []recordedReq
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, recordedReq{r.Method, r.URL.Path, r.URL.RawQuery, string(b)})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	p := NewRazorpayProvider("rzp_test_key", "secret", "whsec").WithEndpoint(srv.URL+"/v1", srv.Client())
	return p, &got
}

// FLAG OFF / NO OFFERS: CreateOrderWithOffers with nothing to offer sends the
// very bytes CreateOrder sends — no `offers` key at all.
func TestRazorpayCreateOrder_NoOffersIsByteIdentical(t *testing.T) {
	const reply = `{"id":"order_TEST000001","entity":"order","amount":118000,"currency":"INR","receipt":"k","status":"created"}`
	p, got := offerServer(t, reply)
	ctx := context.Background()
	amt := Money{Minor: 118000, Currency: "INR"}
	if _, err := p.CreateOrder(ctx, amt, "idem-1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateOrderWithOffers(ctx, amt, "idem-1", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateOrderWithOffers(ctx, amt, "idem-1", nil, []string{}); err != nil {
		t.Fatal(err)
	}
	reqs := *got
	if len(reqs) != 3 {
		t.Fatalf("requests = %d", len(reqs))
	}
	if reqs[0].body != `{"amount":118000,"currency":"INR","receipt":"idem-1"}` {
		t.Fatalf("CreateOrder body changed: %s", reqs[0].body)
	}
	for i := 1; i < 3; i++ {
		if reqs[i].body != reqs[0].body || strings.Contains(reqs[i].body, "offers") {
			t.Fatalf("request %d = %s, want exactly %s", i, reqs[i].body, reqs[0].body)
		}
	}
}

// With offers: the documented `offers` array, the amount untouched.
func TestRazorpayCreateOrderWithOffers_SendsTheOffersArray(t *testing.T) {
	const reply = `{"id":"order_TEST000002","entity":"order","amount":118000,"currency":"INR","receipt":"k","status":"created","offer_id":null}`
	p, got := offerServer(t, reply)
	order, err := p.CreateOrderWithOffers(context.Background(), Money{Minor: 118000, Currency: "INR"}, "idem-2", nil,
		[]string{"offer_ANZoaxsOww2X53", "offer_HDFC10pctXY"})
	if err != nil {
		t.Fatal(err)
	}
	if order.Amount.Minor != 118000 || order.ProviderOrderID != "order_TEST000002" {
		t.Fatalf("order = %+v", order)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte((*got)[0].body), &body); err != nil {
		t.Fatal(err)
	}
	offers, _ := body["offers"].([]any)
	if len(offers) != 2 || offers[0] != "offer_ANZoaxsOww2X53" || body["amount"].(float64) != 118000 {
		t.Fatalf("body = %s", (*got)[0].body)
	}
	if _, forced := body["force_offer"]; forced {
		t.Fatalf("force_offer must not be sent: %s", (*got)[0].body)
	}
}

// The response is Razorpay's documented sample for
// GET /v1/payments/:id?expand[]=offers
// (razorpay.com/docs/api/payments/fetch-payment-expanded-offers/), verbatim in
// every field the adapter reads.
func TestRazorpayFetchPaymentWithOffers_DecodesTheDocumentedSample(t *testing.T) {
	const sample = `{
	  "id": "pay_G8VaL2Z68LRtDs", "entity": "payment", "amount": 900, "currency": "INR",
	  "status": "captured", "order_id": "order_G8VXfKDWDEOHHd", "invoice_id": null,
	  "international": false, "method": "netbanking", "amount_refunded": 0, "refund_status": null,
	  "captured": true,
	  "offers": {"entity": "collection", "count": 1, "items": [{"id": "offer_G8VXOp0qNuEXzh"}]},
	  "description": "Purchase Shoes", "card_id": null, "bank": "KKBK", "wallet": null, "vpa": null,
	  "email": "gaurav.kumar@example.com", "contact": "+919000090000", "customer_id": "cust_DitrYCFtCIokBO",
	  "notes": [], "fee": 22, "tax": 4, "error_code": null, "error_description": null,
	  "error_source": null, "error_step": null, "error_reason": null,
	  "acquirer_data": {"bank_transaction_id": "0125836177"}, "created_at": 1606985740
	}`
	p, got := offerServer(t, sample)
	po, err := p.FetchPaymentWithOffers(context.Background(), "pay_G8VaL2Z68LRtDs")
	if err != nil {
		t.Fatal(err)
	}
	r := (*got)[0]
	if r.method != http.MethodGet || r.path != "/v1/payments/pay_G8VaL2Z68LRtDs" || r.rawQuery != "expand[]=offers" {
		t.Fatalf("request = %+v", r)
	}
	if len(po.OfferIDs) != 1 || po.OfferIDs[0] != "offer_G8VXOp0qNuEXzh" {
		t.Fatalf("offers = %v", po.OfferIDs)
	}
	if po.Payment.ProviderPaymentID != "pay_G8VaL2Z68LRtDs" || po.Payment.ProviderOrderID != "order_G8VXfKDWDEOHHd" ||
		po.Payment.Amount != (Money{Minor: 900, Currency: "INR"}) || po.Payment.State != StateCaptured {
		t.Fatalf("payment = %+v", po.Payment)
	}
	if !po.PaidAt.Equal(time.Unix(1606985740, 0)) {
		t.Fatalf("paid at = %v", po.PaidAt)
	}
}

// An order id is never sent down a payments route, offers or not.
func TestRazorpayFetchPaymentWithOffers_RefusesAnOrderID(t *testing.T) {
	p, got := offerServer(t, `{}`)
	if _, err := p.FetchPaymentWithOffers(context.Background(), "order_G8VXfKDWDEOHHd"); !errors.Is(err, ErrNotAPaymentID) {
		t.Fatalf("err = %v, want ErrNotAPaymentID", err)
	}
	if len(*got) != 0 {
		t.Fatalf("a request was made: %+v", *got)
	}
}

// A payment `offer_id` (undocumented on the payments entity) is read too, and
// de-duplicated against the collection.
func TestRazorpayFetchPaymentWithOffers_ReadsAnOfferIDField(t *testing.T) {
	p, _ := offerServer(t, `{"id":"pay_X1","order_id":"order_Y1","amount":95000,"currency":"INR","status":"captured",
		"offer_id":"offer_AAAAAA111","offers":{"entity":"collection","count":1,"items":[{"id":"offer_AAAAAA111"}]},"created_at":1}`)
	po, err := p.FetchPaymentWithOffers(context.Background(), "pay_X1")
	if err != nil {
		t.Fatal(err)
	}
	if len(po.OfferIDs) != 1 || po.OfferIDs[0] != "offer_AAAAAA111" {
		t.Fatalf("offers = %v", po.OfferIDs)
	}
}
