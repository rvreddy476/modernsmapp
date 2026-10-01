//go:build integration

package service

// Bank offers end to end: the accept/refuse table over every clause of the
// offer rule, run through BOTH the webhook path and the reconciler path; the
// payment.succeeded keys; refund clamping; and "flag off ⇒ nothing changes".
//
// Real Razorpay adapter against the scripted Razorpay in
// offers_fake_integration_test.go, live PostgreSQL (payments_it_test).
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration -p 1 ./internal/service/ -run Offer -v -count=1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── Fixtures ────────────────────────────────────────────────────────

type offerEnv struct {
	t     *testing.T
	rz    *offerRazorpay
	prov  *gateway.RazorpayProvider
	store *postgres.Store
	svc   *Service
}

func newOfferEnv(t *testing.T, enabled bool) *offerEnv {
	t.Helper()
	rz := newOfferRazorpay(t)
	prov := rz.provider()
	store := postgres.New(recPool)
	return &offerEnv{t: t, rz: rz, prov: prov, store: store,
		svc: New(store, nil).WithProvider(prov).WithOffers(enabled)}
}

func randomOfferID() string {
	return "offer_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:14]
}

var offerTestWriter = postgres.OfferWrite{OperatorID: "offers-it", Credential: "test"}

// register puts an offer in the registry: by default 10% up to ₹50.00 on a
// minimum of ₹500.00, card, bank-funded, live from an hour ago. It is
// deactivated when the test ends so it cannot leak into a later order.
func (e *offerEnv) register(app string, mut func(o *postgres.PaymentOffer)) *postgres.PaymentOffer {
	e.t.Helper()
	max := int64(5000)
	o := postgres.PaymentOffer{
		Application: app, Provider: "razorpay", ProviderOfferID: randomOfferID(),
		Title: "10% off with HDFC credit cards", PaymentMethod: "card",
		DiscountType: gateway.OfferDiscountPercentage, DiscountValue: 1000, MaxDiscountMinor: &max,
		MinAmountMinor: 50000, FundedBy: "bank", StartsAt: time.Now().Add(-time.Hour), Active: true,
	}
	if mut != nil {
		mut(&o)
	}
	out, err := e.store.CreateOffer(context.Background(), o, offerTestWriter)
	if err != nil {
		e.t.Fatalf("register offer: %v", err)
	}
	e.t.Cleanup(func() { e.deactivate(out.ID) })
	return out
}

func (e *offerEnv) deactivate(id uuid.UUID) {
	off := false
	if _, _, err := e.store.UpdateOffer(context.Background(), id, "", postgres.OfferPatch{Active: &off}, offerTestWriter); err != nil {
		e.t.Errorf("deactivate offer: %v", err)
	}
}

// registerAt writes an offer whose FIRST registry version is dated `at`, by
// plain INSERTs (the change log refuses updates, not backdated inserts). It is
// how a test places a payment between an offer's registration and a later
// edit.
func (e *offerEnv) registerAt(app string, at time.Time) *postgres.PaymentOffer {
	e.t.Helper()
	ctx := context.Background()
	id, pid := uuid.New(), randomOfferID()
	if _, err := recPool.Exec(ctx, `
		INSERT INTO payments.payment_offers
		    (id, application, provider, provider_offer_id, title, payment_method, discount_type, discount_value,
		     max_discount_minor, min_amount_minor, funded_by, starts_at, active, created_by, created_at, updated_at)
		VALUES ($1,$2,'razorpay',$3,'Backdated 10%','card','percentage',1000,5000,50000,'bank',$4,TRUE,'offers-it',$4,$4)`,
		id, app, pid, at); err != nil {
		e.t.Fatalf("backdated offer: %v", err)
	}
	if _, err := recPool.Exec(ctx, `
		INSERT INTO payments.payment_offer_changes
		    (offer_id, action, operator_id, credential, title, description, payment_method, discount_type,
		     discount_value, max_discount_minor, min_amount_minor, funded_by, starts_at, ends_at, active, changed_at)
		VALUES ($1,'created','offers-it','test','Backdated 10%','','card','percentage',1000,5000,50000,'bank',$2,NULL,TRUE,$2)`,
		id, at); err != nil {
		e.t.Fatalf("backdated offer change: %v", err)
	}
	e.t.Cleanup(func() { e.deactivate(id) })
	var o postgres.PaymentOffer
	o.ID, o.ProviderOfferID, o.Application = id, pid, app
	return &o
}

// intent opens an MStore card intent through InitiatePayment, so the Create
// Order request is the real one.
func (e *offerEnv) intent(amountMinor int64) *postgres.PaymentIntent {
	e.t.Helper()
	in, err := e.svc.InitiatePayment(context.Background(), InitiateInput{
		PayerID: uuid.New(), PayeeID: uuid.New(), ReferenceType: "order", ReferenceID: uuid.New(),
		AmountMinor: amountMinor, Currency: "INR", Method: "card",
		IdempotencyKey: "offers-it-" + uuid.NewString(), OwnerDomain: "commerce-service", ApplicationID: "mstore",
	})
	if err != nil {
		e.t.Fatalf("initiate: %v", err)
	}
	return in
}

func (e *offerEnv) payment(in *postgres.PaymentIntent, captured int64, paidAt time.Time, offers ...string) fakePayment {
	p := fakePayment{
		ID: "pay_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:14], OrderID: in.ProviderRef,
		Currency: "INR", Status: "captured", Amount: captured, CreatedAt: paidAt.Unix(), Offers: offers,
	}
	e.rz.putPayment(p)
	return p
}

// viaWebhook delivers payment.captured for p the way Razorpay does.
func (e *offerEnv) viaWebhook(p fakePayment) error {
	h, b := signedPaymentWebhook("evt_"+p.ID, "payment.captured", p, time.Now().Unix())
	return deliverWebhook(e.t, e.svc, e.prov, h, b)
}

// viaReconciler makes the intent the one stale intent and runs one tick.
func (e *offerEnv) viaReconciler(in *postgres.PaymentIntent) {
	e.t.Helper()
	ctx := context.Background()
	if _, err := recPool.Exec(ctx,
		`UPDATE payments.payment_intents SET created_at = NOW()
		  WHERE status IN ('pending','processing') AND id <> $1`, in.ID); err != nil {
		e.t.Fatal(err)
	}
	if _, err := recPool.Exec(ctx,
		`UPDATE payments.payment_intents SET created_at = NOW() - INTERVAL '2 hours' WHERE id = $1`, in.ID); err != nil {
		e.t.Fatal(err)
	}
	e.svc.reconcileOnce(ctx, 10*time.Minute)
}

type offerRow struct {
	status                 string
	captured, discount     *int64
	offerID                *string
	succeededEvents, inbox int
	payload                map[string]any
}

func readOfferRow(t *testing.T, in *postgres.PaymentIntent) offerRow {
	t.Helper()
	ctx := context.Background()
	var r offerRow
	if err := recPool.QueryRow(ctx,
		`SELECT status, captured_minor, offer_discount_minor, offer_id::text FROM payments.payment_intents WHERE id = $1`,
		in.ID).Scan(&r.status, &r.captured, &r.discount, &r.offerID); err != nil {
		t.Fatal(err)
	}
	r.succeededEvents = countBy(t,
		`SELECT count(*) FROM payments.outbox_events WHERE event_type = 'payment.succeeded' AND partition_key = $1`,
		in.ReferenceID.String())
	r.inbox = countBy(t, `SELECT count(*) FROM payments.provider_events WHERE provider_order_id = $1`, in.ProviderRef)
	var raw []byte
	err := recPool.QueryRow(ctx,
		`SELECT payload FROM payments.outbox_events WHERE event_type = 'payment.succeeded' AND partition_key = $1`,
		in.ReferenceID.String()).Scan(&raw)
	if err == nil {
		var env struct {
			Payload map[string]any `json:"payload"`
		}
		_ = json.Unmarshal(raw, &env)
		r.payload = env.Payload
	}
	return r
}

// ─── The table ───────────────────────────────────────────────────────

type offerCase struct {
	name     string
	enabled  bool
	intent   int64
	captured int64
	// arrange registers what the case needs and returns the provider offer
	// ids the payment names, and when the customer paid.
	arrange func(e *offerEnv) (named []string, paidAt time.Time)
	// accept: the capture settles the intent with this discount. Otherwise
	// it is refused, for this reason (checked on the webhook path, where the
	// error is returned).
	accept   bool
	discount int64
	reason   error
}

func soon() time.Time { return time.Now().Add(5 * time.Second) }

func offerCases() []offerCase {
	ours := func(mut func(o *postgres.PaymentOffer)) func(e *offerEnv) ([]string, time.Time) {
		return func(e *offerEnv) ([]string, time.Time) {
			return []string{e.register("mstore", mut).ProviderOfferID}, soon()
		}
	}
	return []offerCase{
		{name: "accepted at exactly the cap", enabled: true, intent: 100000, captured: 95000,
			arrange: ours(nil), accept: true, discount: 5000},
		{name: "accepted below the cap", enabled: true, intent: 100000, captured: 97000,
			arrange: ours(nil), accept: true, discount: 3000},
		{name: "flat offer accepted", enabled: true, intent: 100000, captured: 97500,
			arrange: ours(func(o *postgres.PaymentOffer) {
				o.DiscountType, o.DiscountValue, o.MaxDiscountMinor = gateway.OfferDiscountFlat, 2500, nil
			}), accept: true, discount: 2500},
		{name: "unknown offer id", enabled: true, intent: 100000, captured: 95000,
			arrange: func(e *offerEnv) ([]string, time.Time) {
				e.register("mstore", nil) // ours exists, but the payment names another
				return []string{randomOfferID()}, soon()
			}, reason: gateway.ErrOfferUnknown},
		{name: "inactive (deactivated before the payment)", enabled: true, intent: 100000, captured: 95000,
			arrange: func(e *offerEnv) ([]string, time.Time) {
				o := e.register("mstore", nil)
				e.deactivate(o.ID)
				return []string{o.ProviderOfferID}, soon()
			}, reason: gateway.ErrOfferInactive},
		{name: "expired", enabled: true, intent: 100000, captured: 95000,
			arrange: func(e *offerEnv) ([]string, time.Time) {
				ends := time.Now().Add(2 * time.Second)
				return []string{e.register("mstore", func(o *postgres.PaymentOffer) { o.EndsAt = &ends }).ProviderOfferID},
					ends.Add(3 * time.Second)
			}, reason: gateway.ErrOfferExpired},
		{name: "discount one paise over the cap", enabled: true, intent: 100000, captured: 94999,
			arrange: ours(nil), reason: gateway.ErrOfferDiscountExceedsCap},
		{name: "flat discount one paise over", enabled: true, intent: 100000, captured: 97499,
			arrange: ours(func(o *postgres.PaymentOffer) {
				o.DiscountType, o.DiscountValue, o.MaxDiscountMinor = gateway.OfferDiscountFlat, 2500, nil
			}), reason: gateway.ErrOfferDiscountExceedsCap},
		{name: "capture above the intent", enabled: true, intent: 100000, captured: 100001,
			arrange: ours(nil), reason: nil},
		{name: "intent below the minimum", enabled: true, intent: 40000, captured: 38000,
			arrange: ours(nil), reason: gateway.ErrOfferBelowMinimum},
		{name: "another application's offer", enabled: true, intent: 100000, captured: 95000,
			arrange: func(e *offerEnv) ([]string, time.Time) {
				return []string{e.register("feast", nil).ProviderOfferID}, soon()
			}, reason: gateway.ErrOfferWrongApplication},
		{name: "no offer named", enabled: true, intent: 100000, captured: 95000,
			arrange: func(e *offerEnv) ([]string, time.Time) { e.register("mstore", nil); return nil, soon() },
			reason:  gateway.ErrOfferNotNamed},
		{name: "two offers named", enabled: true, intent: 100000, captured: 95000,
			arrange: func(e *offerEnv) ([]string, time.Time) {
				return []string{e.register("mstore", nil).ProviderOfferID, e.register("mstore", nil).ProviderOfferID}, soon()
			}, reason: gateway.ErrOfferAmbiguous},
		{name: "not yet registered when the customer paid", enabled: true, intent: 100000, captured: 95000,
			arrange: func(e *offerEnv) ([]string, time.Time) {
				return []string{e.register("mstore", nil).ProviderOfferID}, time.Now().Add(-30 * time.Minute)
			}, reason: gateway.ErrOfferInactive},
		{name: "active when paid, deactivated since", enabled: true, intent: 100000, captured: 95000,
			arrange: func(e *offerEnv) ([]string, time.Time) {
				o := e.registerAt("mstore", time.Now().Add(-2*time.Hour))
				e.deactivate(o.ID)
				return []string{o.ProviderOfferID}, time.Now().Add(-30 * time.Minute)
			}, accept: true, discount: 5000},
		{name: "flag off: a valid offer changes nothing", enabled: false, intent: 100000, captured: 95000,
			arrange: ours(nil), reason: nil},
	}
}

func TestOfferMatching_AcceptRefuseTable_WebhookAndReconciler(t *testing.T) {
	for _, path := range []string{"webhook", "reconciler"} {
		for _, tc := range offerCases() {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				e := newOfferEnv(t, tc.enabled)
				named, paidAt := tc.arrange(e)
				in := e.intent(tc.intent)
				p := e.payment(in, tc.captured, paidAt, named...)

				var whErr error
				if path == "webhook" {
					whErr = e.viaWebhook(p)
				} else {
					e.viaReconciler(in)
				}
				_, _, expands := e.rz.snapshot()
				row := readOfferRow(t, in)

				if tc.accept {
					if whErr != nil {
						t.Fatalf("refused: %v", whErr)
					}
					if row.status != "succeeded" || row.captured == nil || *row.captured != tc.captured ||
						row.discount == nil || *row.discount != tc.discount || row.offerID == nil {
						t.Fatalf("intent after acceptance = %+v", row)
					}
					if row.succeededEvents != 1 {
						t.Fatalf("payment.succeeded rows = %d, want 1", row.succeededEvents)
					}
					pl := row.payload
					if pl["amount_minor"] != float64(tc.intent) || pl["captured_minor"] != float64(tc.captured) ||
						pl["offer_discount_minor"] != float64(tc.discount) || pl["offer_id"] != *row.offerID ||
						pl["offer_title"] == "" || pl["offer_funded_by"] != "bank" {
						t.Fatalf("payment.succeeded payload = %v", pl)
					}
					if n := countBy(t, `SELECT count(*) FROM payments.payment_audit_log
					                     WHERE intent_id = $1 AND event = 'offer_capture_accepted'`, in.ID); n != 1 {
						t.Fatalf("offer audit rows = %d", n)
					}
					return
				}

				// Refused: exactly the old mismatch outcome — nothing written.
				if row.status != "pending" || row.captured != nil || row.offerID != nil || row.succeededEvents != 0 {
					t.Fatalf("a refused capture wrote state: %+v", row)
				}
				if path == "webhook" {
					if !errors.Is(whErr, postgres.ErrWebhookAmountMismatch) {
						t.Fatalf("webhook err = %v, want ErrWebhookAmountMismatch", whErr)
					}
					if row.inbox != 0 {
						t.Fatalf("a refused capture committed %d inbox row(s)", row.inbox)
					}
					if tc.reason != nil && !errors.Is(whErr, tc.reason) {
						t.Fatalf("refused for %v, want %v", whErr, tc.reason)
					}
				}
				// No offer evidence is ever fetched when offers are off, or for
				// a capture that is not LOWER than the intent.
				if (!tc.enabled || tc.captured >= tc.intent) && len(expands) != 0 {
					t.Fatalf("offer evidence was fetched: %v", expands)
				}
				if tc.enabled && tc.captured < tc.intent && len(expands) == 0 {
					t.Fatalf("a lower capture with offers on never asked the provider for its offers")
				}
			})
		}
	}
}

// The provider's offer evidence must be about THIS capture: the payment the
// webhook names, on the intent's order, captured, for the amount the webhook
// says. Evidence about anything else is refused and the mismatch stands.
func TestOfferEvidence_MustMatchTheEvent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(atProvider *fakePayment, other *postgres.PaymentIntent)
	}{
		{"provider says another amount", func(p *fakePayment, _ *postgres.PaymentIntent) { p.Amount = 96000 }},
		{"payment belongs to another order", func(p *fakePayment, other *postgres.PaymentIntent) { p.OrderID = other.ProviderRef }},
		{"payment is not captured", func(p *fakePayment, _ *postgres.PaymentIntent) { p.Status = "authorized" }},
		{"provider says another currency", func(p *fakePayment, _ *postgres.PaymentIntent) { p.Currency = "USD" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newOfferEnv(t, true)
			o := e.register("mstore", nil)
			in, other := e.intent(100000), e.intent(100000)
			event := fakePayment{
				ID: "pay_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:14], OrderID: in.ProviderRef,
				Currency: "INR", Status: "captured", Amount: 95000, CreatedAt: soon().Unix(),
				Offers: []string{o.ProviderOfferID},
			}
			atProvider := event
			tc.mutate(&atProvider, other)
			e.rz.putPayment(atProvider)
			h, b := signedPaymentWebhook("evt_"+event.ID, "payment.captured", event, time.Now().Unix())
			err := deliverWebhook(t, e.svc, e.prov, h, b)
			if !errors.Is(err, postgres.ErrWebhookAmountMismatch) {
				t.Fatalf("err = %v, want the mismatch to stand", err)
			}
			if row := readOfferRow(t, in); row.status != "pending" || row.succeededEvents != 0 || row.inbox != 0 {
				t.Fatalf("evidence about another capture settled the intent: %+v", row)
			}
			if _, _, expands := e.rz.snapshot(); len(expands) != 1 {
				t.Fatalf("expected exactly one evidence fetch, got %v", expands)
			}
		})
	}
}

// The Create Order request: offers on ⇒ the application's applicable offers
// in `offers`, nothing else changed; offers off ⇒ no `offers` key even with
// live offers registered.
func TestOfferCreateOrder_FlagDecidesTheOffersArray(t *testing.T) {
	on := newOfferEnv(t, true)
	live := on.register("mstore", nil)
	tooHigh := on.register("mstore", func(o *postgres.PaymentOffer) { o.MinAmountMinor = 500000 })
	other := on.register("feast", nil)
	on.intent(100000)
	bodies, _, _ := on.rz.snapshot()
	var req map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &req); err != nil {
		t.Fatal(err)
	}
	offers := fmt.Sprint(req["offers"])
	if !strings.Contains(offers, live.ProviderOfferID) || strings.Contains(offers, tooHigh.ProviderOfferID) ||
		strings.Contains(offers, other.ProviderOfferID) {
		t.Fatalf("offers on: offers = %s", offers)
	}
	if req["amount"] != float64(100000) {
		t.Fatalf("the order amount changed: %v", req["amount"])
	}

	off := newOfferEnv(t, false)
	off.register("mstore", nil)
	off.intent(100000)
	bodies, _, _ = off.rz.snapshot()
	var offReq map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &offReq); err != nil {
		t.Fatal(err)
	}
	if _, has := offReq["offers"]; has || len(offReq) != 3 {
		t.Fatalf("offers off but the order carries more than amount/currency/receipt: %s", bodies[0])
	}
	if !strings.HasPrefix(bodies[0], `{"amount":100000,"currency":"INR","receipt":"offers-it-`) {
		t.Fatalf("offers off: body = %s", bodies[0])
	}
}

// ─── Refund clamping ─────────────────────────────────────────────────

// acceptedOfferIntent is an MStore intent of ₹1,000.00 settled through a 10%
// offer (no cap) at `captured`.
func acceptedOfferIntent(t *testing.T, e *offerEnv, captured int64) (*postgres.PaymentIntent, fakePayment) {
	t.Helper()
	o := e.register("mstore", func(o *postgres.PaymentOffer) { o.MaxDiscountMinor = nil })
	in := e.intent(100000)
	p := e.payment(in, captured, soon(), o.ProviderOfferID)
	if err := e.viaWebhook(p); err != nil {
		t.Fatalf("offer capture refused: %v", err)
	}
	return in, p
}

func (e *offerEnv) refund(in *postgres.PaymentIntent, p fakePayment, orderValue int64, key string) (*postgres.RefundCommand, error) {
	e.t.Helper()
	ctx := context.Background()
	cmd, err := e.svc.RequestRefund(ctx, RefundRequest{
		IntentID: in.ID, AmountMinor: orderValue, Reason: "offers-it",
		ProviderIdempotencyKey: in.IdempotencyKey + "-" + key,
		CallerDomain:           "commerce-service", ApplicationID: "mstore",
	})
	if err != nil {
		return nil, err
	}
	e.svc.attemptRefund(ctx, *cmd)
	got, err := e.store.GetRefundCommand(ctx, cmd.ID)
	if err != nil || got.ProviderRefundID == "" {
		e.t.Fatalf("refund not submitted: %+v %v", got, err)
	}
	h, b := signedRefundWebhook("evt_"+got.ProviderRefundID, got.ProviderRefundID, p.ID, got.ProviderAmount(), time.Now().Unix())
	if err := deliverWebhook(e.t, e.svc, e.prov, h, b); err != nil {
		e.t.Fatalf("refund webhook: %v", err)
	}
	return got, nil
}

func refundEvents(t *testing.T, in *postgres.PaymentIntent, eventType string) []map[string]any {
	t.Helper()
	rows, err := recPool.Query(context.Background(),
		`SELECT payload FROM payments.outbox_events WHERE event_type = $1 AND partition_key = $2 ORDER BY id`,
		eventType, in.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var env struct {
			Payload map[string]any `json:"payload"`
		}
		_ = json.Unmarshal(raw, &env)
		out = append(out, env.Payload)
	}
	return out
}

func intentMoney(t *testing.T, in *postgres.PaymentIntent) (status string, refunded, returned int64) {
	t.Helper()
	if err := recPool.QueryRow(context.Background(),
		`SELECT status, refunded_amount_minor, COALESCE(refunded_captured_minor,0) FROM payments.payment_intents WHERE id = $1`,
		in.ID).Scan(&status, &refunded, &returned); err != nil {
		t.Fatal(err)
	}
	return
}

func TestOfferRefund_FullRefundReturnsTheCapture(t *testing.T) {
	e := newOfferEnv(t, true)
	in, p := acceptedOfferIntent(t, e, 95000)
	cmd, err := e.refund(in, p, 100000, "full")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.AmountMinor != 100000 || cmd.ProviderAmount() != 95000 {
		t.Fatalf("command = order %d, money %d; want 100000 / 95000", cmd.AmountMinor, cmd.ProviderAmount())
	}
	_, refundBodies, _ := e.rz.snapshot()
	if len(refundBodies) != 1 || refundBodies[0] != `{"amount":95000}` {
		t.Fatalf("provider refund requests = %v", refundBodies)
	}
	status, refunded, returned := intentMoney(t, in)
	if status != "refunded" || refunded != 100000 || returned != 95000 {
		t.Fatalf("intent = %s, refunded %d, returned %d", status, refunded, returned)
	}
	pend := refundEvents(t, in, "payment.refund_pending")
	done := refundEvents(t, in, "payment.refunded")
	if len(pend) != 1 || pend[0]["amount_minor"] != float64(100000) || pend[0]["amount_returned_minor"] != float64(95000) {
		t.Fatalf("refund_pending = %v", pend)
	}
	if len(done) != 1 || done[0]["amount_minor"] != float64(100000) || done[0]["amount_returned_minor"] != float64(95000) ||
		done[0]["status"] != "refunded" {
		t.Fatalf("refunded = %v", done)
	}
	if _, err := e.refund(in, p, 1, "more"); err == nil {
		t.Fatalf("a refund beyond the order value was accepted")
	}
}

func TestOfferRefund_PartialsProrateAndEndAtTheCapture(t *testing.T) {
	e := newOfferEnv(t, true)
	in, p := acceptedOfferIntent(t, e, 90001) // 10% offer, discount 9,999 ≤ 10,000 allowed
	want := []struct{ order, money int64 }{{33333, 30000}, {33333, 30000}, {33334, 30001}}
	for i, w := range want {
		cmd, err := e.refund(in, p, w.order, fmt.Sprint(i))
		if err != nil {
			t.Fatalf("refund %d: %v", i, err)
		}
		if cmd.ProviderAmount() != w.money {
			t.Fatalf("refund %d of %d sent %d, want %d", i, w.order, cmd.ProviderAmount(), w.money)
		}
		if i == 0 {
			// The literal rule for a partial: floor(X × captured / intent).
			if got := w.order * 90001 / 100000; got != w.money {
				t.Fatalf("floor rule = %d, sent %d", got, w.money)
			}
		}
	}
	status, refunded, returned := intentMoney(t, in)
	if status != "refunded" || refunded != 100000 || returned != 90001 {
		t.Fatalf("intent = %s, refunded %d, returned %d (want refunded, 100000, 90001)", status, refunded, returned)
	}
	done := refundEvents(t, in, "payment.refunded")
	if len(done) != 3 || done[2]["amount_minor"] != float64(33334) || done[2]["amount_returned_minor"] != float64(30001) {
		t.Fatalf("refunded events = %v", done)
	}
}

func TestOfferRefund_TooSmallToReturnAPaiseIsRefused(t *testing.T) {
	e := newOfferEnv(t, true)
	in, _ := acceptedOfferIntent(t, e, 90000)
	_, err := e.svc.RequestRefund(context.Background(), RefundRequest{
		IntentID: in.ID, AmountMinor: 1, Reason: "offers-it", ProviderIdempotencyKey: in.IdempotencyKey + "-tiny",
		CallerDomain: "commerce-service", ApplicationID: "mstore",
	})
	if !errors.Is(err, postgres.ErrOfferRefundRoundsToZero) {
		t.Fatalf("err = %v, want ErrOfferRefundRoundsToZero", err)
	}
}

// An operator who settles a parked refund of an offer payment by hand records
// the prorated money as returned, and the event says so.
func TestOfferRefund_ManualResolutionCountsTheMoney(t *testing.T) {
	e := newOfferEnv(t, true)
	ctx := context.Background()
	in, _ := acceptedOfferIntent(t, e, 95000)
	cmd, err := e.svc.RequestRefund(ctx, RefundRequest{
		IntentID: in.ID, AmountMinor: 100000, Reason: "offers-it", ProviderIdempotencyKey: in.IdempotencyKey + "-manual",
		CallerDomain: "commerce-service", ApplicationID: "mstore",
	})
	if err != nil {
		t.Fatal(err)
	}
	if parked, err := e.store.ParkRefundCommand(ctx, cmd.ID, RefundFailIntentNotFound, "offers-it"); err != nil || !parked {
		t.Fatalf("park: %v %v", parked, err)
	}
	if _, err := e.svc.ResolveRefundCommand(ctx, postgres.ResolveRefundInput{
		CommandID: cmd.ID, Resolution: postgres.ResolutionRefundedManually, Note: "bank transfer",
		OperatorID: "offers-it", Credential: "test",
	}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if status, refunded, returned := intentMoney(t, in); status != "refunded" || refunded != 100000 || returned != 95000 {
		t.Fatalf("intent = %s, refunded %d, returned %d", status, refunded, returned)
	}
	done := refundEvents(t, in, "payment.refunded")
	if len(done) != 1 || done[0]["amount_minor"] != float64(100000) || done[0]["amount_returned_minor"] != float64(95000) ||
		done[0]["manual"] != true {
		t.Fatalf("refunded = %v", done)
	}
}

// A provider refund of an offer payment that returns more than was captured
// is refused, rolled back, and the provider retries.
func TestOfferRefund_ProviderOverRefundIsRefused(t *testing.T) {
	e := newOfferEnv(t, true)
	in, p := acceptedOfferIntent(t, e, 95000)
	h, b := signedRefundWebhook("evt_rfnd_over_"+p.ID, "rfnd_over_"+p.ID[4:], p.ID, 95001, time.Now().Unix())
	if err := deliverWebhook(t, e.svc, e.prov, h, b); !errors.Is(err, postgres.ErrOfferRefundExceedsCaptured) {
		t.Fatalf("err = %v, want ErrOfferRefundExceedsCaptured", err)
	}
	if status, refunded, returned := intentMoney(t, in); status != "succeeded" || refunded != 0 || returned != 0 {
		t.Fatalf("an over-refund moved the ledger: %s %d %d", status, refunded, returned)
	}
}

// ─── The registry ────────────────────────────────────────────────────

func TestOfferRegistry_ChangeLogIsAppendOnlyAndIdentityImmutable(t *testing.T) {
	e := newOfferEnv(t, true)
	ctx := context.Background()
	o := e.register("mstore", nil)
	title := "12% off"
	if _, changed, err := e.store.UpdateOffer(ctx, o.ID, "", postgres.OfferPatch{Title: &title}, offerTestWriter); err != nil || !changed {
		t.Fatalf("edit: changed=%v err=%v", changed, err)
	}
	if _, changed, err := e.store.UpdateOffer(ctx, o.ID, "", postgres.OfferPatch{Title: &title}, offerTestWriter); err != nil || changed {
		t.Fatalf("a no-op edit wrote a change: changed=%v err=%v", changed, err)
	}
	if _, _, err := e.store.UpdateOffer(ctx, o.ID, "feast", postgres.OfferPatch{Title: &title}, offerTestWriter); !errors.Is(err, postgres.ErrOfferNotFound) {
		t.Fatalf("another application's admin edited it: %v", err)
	}
	changes, err := e.store.OfferChanges(ctx, o.ID)
	if err != nil || len(changes) != 2 || changes[0].Action != "created" || changes[1].Action != "updated" {
		t.Fatalf("changes = %+v, %v", changes, err)
	}
	for _, stmt := range []string{
		`UPDATE payments.payment_offer_changes SET active = FALSE WHERE offer_id = $1`,
		`DELETE FROM payments.payment_offer_changes WHERE offer_id = $1`,
		`DELETE FROM payments.payment_offers WHERE id = $1`,
		`UPDATE payments.payment_offers SET provider_offer_id = 'offer_CHANGED123' WHERE id = $1`,
		`UPDATE payments.payment_offers SET application = 'feast' WHERE id = $1`,
	} {
		if _, err := recPool.Exec(ctx, stmt, o.ID); err == nil {
			t.Fatalf("allowed: %s", stmt)
		}
	}
	if _, err := e.store.CreateOffer(ctx, postgres.PaymentOffer{
		Application: "mstore", ProviderOfferID: o.ProviderOfferID, Title: "dup", PaymentMethod: "any",
		DiscountType: "flat", DiscountValue: 100, FundedBy: "bank", StartsAt: time.Now(), Active: true,
	}, offerTestWriter); !errors.Is(err, postgres.ErrOfferExists) {
		t.Fatalf("duplicate provider offer: %v", err)
	}
}

func TestOfferApplicableList_PublicFieldsAndFlag(t *testing.T) {
	e := newOfferEnv(t, true)
	o := e.register("mstore", nil)
	high := e.register("mstore", func(o *postgres.PaymentOffer) { o.MinAmountMinor = 900000 })
	got, err := e.svc.ApplicableOffers(context.Background(), "mstore", 100000)
	if err != nil {
		t.Fatal(err)
	}
	var seen, seenHigh bool
	for _, p := range got {
		seen = seen || p.ID == o.ID
		seenHigh = seenHigh || p.ID == high.ID
	}
	if !seen || seenHigh {
		t.Fatalf("applicable = %+v", got)
	}
	b, _ := json.Marshal(got)
	for _, private := range []string{"provider_offer_id", "funded_by", "created_by", "application", "active"} {
		if strings.Contains(string(b), `"`+private+`"`) {
			t.Fatalf("public list leaks %s: %s", private, b)
		}
	}
	off := newOfferEnv(t, false)
	if got, err := off.svc.ApplicableOffers(context.Background(), "mstore", 100000); err != nil || len(got) != 0 {
		t.Fatalf("offers off: %v %v", got, err)
	}
}
