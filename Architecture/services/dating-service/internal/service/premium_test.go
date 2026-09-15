// Premium integration tests (lane P2): purchases, the payment consumer's
// effects, refunds, the NULL-expiry rule and the expiry reminder. Skipped
// without TEST_PG_DSN; refuses a database not named *_test. Every test uses
// fresh user and event ids, so the suite re-runs on a reused database in any
// order.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/dating-service/database"
	"github.com/atpost/dating-service/internal/payments"
	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// fakePremiumPayments stands in for payments-service: it echoes the amount and
// reference, and returns the same intent for the same purchase (as payments'
// idempotency key does).
type fakePremiumPayments struct {
	mu        sync.Mutex
	calls     []payments.CreateIntentInput
	intents   map[uuid.UUID]uuid.UUID
	noSession bool
	err       error
}

func (f *fakePremiumPayments) CreateIntent(_ context.Context, in payments.CreateIntentInput) (*payments.Intent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, in)
	if f.err != nil {
		return nil, f.err
	}
	if f.intents == nil {
		f.intents = map[uuid.UUID]uuid.UUID{}
	}
	id, ok := f.intents[in.PurchaseID]
	if !ok {
		id = uuid.New()
		f.intents[in.PurchaseID] = id
	}
	ref := "order_" + strings.ReplaceAll(id.String(), "-", "")[:14]
	intent := &payments.Intent{ID: id, Status: "pending", AmountMinor: in.AmountMinor, Currency: "INR", Method: in.Method,
		ProviderRef: ref, ReferenceType: payments.RefTypeDatingPremium, ReferenceID: in.PurchaseID, PayerID: in.PayerID}
	if !f.noSession {
		intent.ClientSession = map[string]string{"provider": "razorpay", "order_id": ref, "key_id": "rzp_test_pub"}
	}
	return intent, nil
}

type premiumRig struct {
	svc      *Service
	st       *store.Store
	rec      *recordingWriter
	pay      *fakePremiumPayments
	consumer *payments.Consumer
}

func newPremiumRig(t *testing.T) *premiumRig {
	t.Helper()
	svc, st, rec := newD3Svc(t)
	pay := &fakePremiumPayments{}
	svc.SetPremiumPayments(pay, "")
	return &premiumRig{svc: svc, st: st, rec: rec, pay: pay, consumer: payments.NewHandlerForTest(st, svc.OnPremiumPaymentApplied)}
}

func (r *premiumRig) buy(t *testing.T, user uuid.UUID, product string) *store.PremiumPurchase {
	t.Helper()
	out, created, err := r.svc.CreatePremiumPurchase(context.Background(), user, PremiumPurchaseInput{Product: product, IdempotencyKey: "k-" + uuid.NewString()})
	if err != nil || !created {
		t.Fatalf("buy %s: created=%v err=%v", product, created, err)
	}
	return out.Purchase
}

// deliver sends one payments-service event through the real consumer filters
// into the store. mutate edits the payload before it is sent.
func (r *premiumRig) deliver(t *testing.T, eventType, eventID string, p *store.PremiumPurchase, mutate func(map[string]any)) {
	t.Helper()
	payload := map[string]any{
		"id":             p.PaymentIntentID.String(),
		"payer_id":       p.UserID.String(),
		"payee_id":       payments.DefaultPayeeID.String(),
		"reference_type": "dating_premium",
		"reference_id":   p.ID.String(),
		"amount_minor":   p.AmountMinor,
		"currency":       "INR",
		"method":         "upi",
		"status":         "succeeded",
		"application_id": "dating",
	}
	switch eventType {
	case events.EventPaymentFailed:
		payload["status"] = "failed"
	case events.EventPaymentRefunded:
		payload = map[string]any{
			"id": p.PaymentIntentID.String(), "intent_id": p.PaymentIntentID.String(), "provider": "razorpay",
			"provider_refund_id": "rfnd_" + eventID, "amount_minor": p.AmountMinor, "status": "refunded",
			"reference_type": "dating_premium", "reference_id": p.ID.String(), "application_id": "dating",
		}
	}
	if mutate != nil {
		mutate(payload)
	}
	raw, _ := json.Marshal(payload)
	if err := r.consumer.Handle(context.Background(), &events.EventEnvelope{EventID: eventID, EventType: eventType, Payload: raw}); err != nil {
		t.Fatalf("deliver %s: %v", eventType, err)
	}
}

func (r *premiumRig) purchase(t *testing.T, p *store.PremiumPurchase) *store.PremiumPurchase {
	t.Helper()
	got, err := r.st.GetPremiumPurchaseForUser(context.Background(), p.UserID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (r *premiumRig) passExpiry(t *testing.T, user uuid.UUID) *time.Time {
	t.Helper()
	ent, err := r.st.GetPremiumEntitlement(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	return ent.PassExpiresAt
}

func (r *premiumRig) isPremium(t *testing.T, user uuid.UUID) bool {
	t.Helper()
	ok, err := r.st.IsPremium(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func (r *premiumRig) boostBalance(t *testing.T, user uuid.UUID) int {
	t.Helper()
	ent, err := r.st.GetPremiumEntitlement(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	return ent.BoostBalance
}

func (r *premiumRig) inboxOutcome(t *testing.T, eventID string) string {
	t.Helper()
	var out string
	d9QueryRow(t, r.st, `SELECT COALESCE(outcome, '') || '|' || COALESCE(detail, '') FROM dating_payment_inbox WHERE event_id = $1`, &out, eventID)
	return out
}

func approx(t *testing.T, what string, got *time.Time, want time.Time) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: no expiry, want %v", what, want)
	}
	if d := got.Sub(want); d > time.Minute || d < -time.Minute {
		t.Fatalf("%s: expires %v, want about %v (off by %v)", what, got.UTC(), want.UTC(), d)
	}
}

func evID() string { return "evt_" + uuid.NewString() }

const day = 24 * time.Hour

func TestPremiumPurchase_PricedFromCatalogueWithSession(t *testing.T) {
	r := newPremiumRig(t)
	user := uuid.New()
	out, created, err := r.svc.CreatePremiumPurchase(context.Background(), user, PremiumPurchaseInput{Product: "pass_90d", IdempotencyKey: "k-" + uuid.NewString(), Method: "card"})
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	p := out.Purchase
	if p.AmountMinor != 99900 || p.Currency != "INR" || p.Method != "card" || p.Status != payments.StatusConfirming || p.PaymentIntentID == nil {
		t.Fatalf("purchase = %+v", p)
	}
	if len(r.pay.calls) != 1 || r.pay.calls[0].AmountMinor != 99900 || r.pay.calls[0].PurchaseID != p.ID || r.pay.calls[0].PayerID != user {
		t.Fatalf("payments calls = %+v", r.pay.calls)
	}
	if out.ClientSession == nil || out.ClientSession.KeyID != "rzp_test_pub" || out.ClientSession.OrderID != *p.ProviderRef {
		t.Fatalf("client_session = %+v", out.ClientSession)
	}
	if r.isPremium(t, user) {
		t.Fatalf("creating a purchase granted premium")
	}
}

func TestPremiumPurchase_IdempotentPerUserAndKey(t *testing.T) {
	r := newPremiumRig(t)
	ctx := context.Background()
	user, other := uuid.New(), uuid.New()
	key := "k-" + uuid.NewString()
	first, created, err := r.svc.CreatePremiumPurchase(ctx, user, PremiumPurchaseInput{Product: "boost", IdempotencyKey: key})
	if err != nil || !created {
		t.Fatalf("first: %v %v", created, err)
	}
	again, created, err := r.svc.CreatePremiumPurchase(ctx, user, PremiumPurchaseInput{Product: "boost", IdempotencyKey: key})
	if err != nil || created || again.Purchase.ID != first.Purchase.ID || *again.Purchase.PaymentIntentID != *first.Purchase.PaymentIntentID {
		t.Fatalf("repeat: created=%v err=%v purchase=%+v", created, err, again)
	}
	if again.ClientSession == nil {
		t.Fatalf("a repeat while unpaid must relay the checkout session again")
	}
	theirs, created, err := r.svc.CreatePremiumPurchase(ctx, other, PremiumPurchaseInput{Product: "boost", IdempotencyKey: key})
	if err != nil || !created || theirs.Purchase.ID == first.Purchase.ID {
		t.Fatalf("another user's same key must be a different purchase: %v %v", created, err)
	}
	if _, _, err := r.svc.CreatePremiumPurchase(ctx, user, PremiumPurchaseInput{Product: "pass_30d", IdempotencyKey: key}); !errors.Is(err, store.ErrIdempotencyKeyReused) {
		t.Fatalf("same key, other product: %v", err)
	}
	// Once paid, a repeat returns the purchase without calling payments.
	r.deliver(t, events.EventPaymentSucceeded, evID(), first.Purchase, nil)
	calls := len(r.pay.calls)
	paid, _, err := r.svc.CreatePremiumPurchase(ctx, user, PremiumPurchaseInput{Product: "boost", IdempotencyKey: key})
	if err != nil || paid.Purchase.Status != payments.StatusPaid || paid.ClientSession != nil || len(r.pay.calls) != calls {
		t.Fatalf("repeat after payment: %+v err=%v calls=%d->%d", paid, err, calls, len(r.pay.calls))
	}
}

func TestPremiumPurchase_RefusalsWriteNothing(t *testing.T) {
	r := newPremiumRig(t)
	ctx := context.Background()
	user := uuid.New()
	cases := map[string]struct {
		in   PremiumPurchaseInput
		want error
	}{
		"unknown product": {PremiumPurchaseInput{Product: "monthly_399", IdempotencyKey: "k"}, ErrPremiumProductUnknown},
		"bad method":      {PremiumPurchaseInput{Product: "boost", IdempotencyKey: "k", Method: "cod"}, ErrPremiumMethodInvalid},
		"no key":          {PremiumPurchaseInput{Product: "boost"}, ErrPremiumIdempotencyKeyRequired},
		"long key":        {PremiumPurchaseInput{Product: "boost", IdempotencyKey: strings.Repeat("k", 129)}, ErrPremiumIdempotencyKeyRequired},
	}
	for name, c := range cases {
		if _, _, err := r.svc.CreatePremiumPurchase(ctx, user, c.in); !errors.Is(err, c.want) {
			t.Fatalf("%s: err = %v, want %v", name, err, c.want)
		}
	}
	unconfigured, st, _ := newD3Svc(t)
	if _, _, err := unconfigured.CreatePremiumPurchase(ctx, user, PremiumPurchaseInput{Product: "boost", IdempotencyKey: "k"}); !errors.Is(err, ErrPremiumUnavailable) {
		t.Fatalf("no payments client: %v", err)
	}
	if list, _ := st.ListPremiumPurchasesForUser(ctx, user); len(list) != 0 {
		t.Fatalf("a refused purchase wrote %d rows", len(list))
	}
}

func TestPremiumPurchase_StubSessionRefusedOutsideLocalDev(t *testing.T) {
	r := newPremiumRig(t)
	ctx := context.Background()
	r.pay.noSession = true
	if _, _, err := r.svc.CreatePremiumPurchase(ctx, uuid.New(), PremiumPurchaseInput{Product: "boost", IdempotencyKey: "k-" + uuid.NewString()}); !errors.Is(err, ErrPremiumCheckoutUnavailable) {
		t.Fatalf("ENV unset: %v", err)
	}
	r.svc.SetPremiumPayments(r.pay, "dev")
	out, _, err := r.svc.CreatePremiumPurchase(ctx, uuid.New(), PremiumPurchaseInput{Product: "boost", IdempotencyKey: "k-" + uuid.NewString()})
	if err != nil || out.ClientSession != nil {
		t.Fatalf("ENV=dev: out=%+v err=%v", out, err)
	}
}

// Mutation guard: a pass is granted only by an applied signed event, and a
// purchase row marked paid some other way still reads as confirming.
func TestPremiumPass_GrantedOnlyBySignedEvent(t *testing.T) {
	r := newPremiumRig(t)
	ctx := context.Background()
	user := uuid.New()
	p := r.buy(t, user, "pass_30d")
	if r.isPremium(t, user) || r.passExpiry(t, user) != nil {
		t.Fatalf("premium before any payment event")
	}
	d8Exec(t, r.st, `UPDATE dating_premium_purchases SET status = 'paid' WHERE id = $1`, p.ID)
	st, err := r.svc.PremiumPurchasePayment(ctx, user, p.ID)
	if err != nil || st.Status != payments.CustomerStatusConfirming {
		t.Fatalf("a paid row without an applied event reads %+v err=%v", st, err)
	}
	d8Exec(t, r.st, `UPDATE dating_premium_purchases SET status = 'confirming' WHERE id = $1`, p.ID)

	r.deliver(t, events.EventPaymentSucceeded, evID(), p, nil)
	if !r.isPremium(t, user) {
		t.Fatalf("not premium after payment.succeeded")
	}
	approx(t, "30-day pass", r.passExpiry(t, user), time.Now().Add(30*day))
	st, err = r.svc.PremiumPurchasePayment(ctx, user, p.ID)
	if err != nil || st.Status != payments.CustomerStatusPaid || st.RefundStatus != nil {
		t.Fatalf("status after capture = %+v err=%v", st, err)
	}
	me, err := r.svc.MyPremium(ctx, user)
	if err != nil || !me.IsPremium || me.Pass == nil || me.Pass.Product != "pass_30d" || len(me.Entitlements) != len(payments.PassFeatures) {
		t.Fatalf("me = %+v err=%v", me, err)
	}
	for _, e := range me.Entitlements {
		if !e.Active || e.ExpiresAt == nil || !e.ExpiresAt.Equal(me.Pass.ExpiresAt) {
			t.Fatalf("entitlement %+v", e)
		}
	}
}

func TestPremiumPass_StacksFromCurrentExpiry(t *testing.T) {
	r := newPremiumRig(t)
	user := uuid.New()
	a, b := r.buy(t, user, "pass_30d"), r.buy(t, user, "pass_90d")
	r.deliver(t, events.EventPaymentSucceeded, evID(), a, nil)
	r.deliver(t, events.EventPaymentSucceeded, evID(), b, nil)
	approx(t, "30 + 90 days", r.passExpiry(t, user), time.Now().Add(120*day))
	// An expired pass restarts from now, not from the old expiry.
	d8Exec(t, r.st, `UPDATE dating_premium_subscriptions SET expires_at = now() - interval '10 days' WHERE user_id = $1`, user)
	c := r.buy(t, user, "pass_30d")
	r.deliver(t, events.EventPaymentSucceeded, evID(), c, nil)
	approx(t, "after lapse", r.passExpiry(t, user), time.Now().Add(30*day))
}

// Mutation guard: an event for another application changes nothing, even
// with the right reference type, amount and payer.
func TestPremiumEvent_WrongApplicationNotApplied(t *testing.T) {
	r := newPremiumRig(t)
	user := uuid.New()
	p := r.buy(t, user, "boost")
	id := evID()
	r.deliver(t, events.EventPaymentSucceeded, id, p, func(m map[string]any) { m["application_id"] = "feast" })
	if r.boostBalance(t, user) != 0 || r.purchase(t, p).Status != payments.StatusConfirming {
		t.Fatalf("a feast event granted a dating Boost")
	}
	if n := d8QueryInt(t, r.st, `SELECT COUNT(*)::int FROM dating_payment_inbox WHERE event_id = $1`, id); n != 0 {
		t.Fatalf("a foreign-application event reached the inbox")
	}
	r.deliver(t, events.EventPaymentSucceeded, evID(), p, func(m map[string]any) { m["reference_type"] = "food_order" })
	r.deliver(t, events.EventPaymentSucceeded, evID(), p, func(m map[string]any) { delete(m, "application_id") })
	if r.boostBalance(t, user) != 0 {
		t.Fatalf("a food_order or unstated-application event granted a Boost")
	}
}

// Mutation guard: amount, currency and payer must match the purchase.
func TestPremiumEvent_MismatchGrantsNothing(t *testing.T) {
	r := newPremiumRig(t)
	user := uuid.New()
	p := r.buy(t, user, "pass_30d")
	for name, mutate := range map[string]func(map[string]any){
		"amount":   func(m map[string]any) { m["amount_minor"] = 4900 },
		"currency": func(m map[string]any) { m["currency"] = "USD" },
		"payer":    func(m map[string]any) { m["payer_id"] = uuid.NewString() },
	} {
		id := evID()
		r.deliver(t, events.EventPaymentSucceeded, id, p, mutate)
		if got := r.inboxOutcome(t, id); !strings.HasPrefix(got, "amount_mismatch|") {
			t.Fatalf("%s: inbox outcome %q", name, got)
		}
		if r.isPremium(t, user) || r.purchase(t, p).Status != payments.StatusConfirming {
			t.Fatalf("%s mismatch granted the pass", name)
		}
	}
	// The recorded mismatches do not poison the purchase: the right event grants.
	r.deliver(t, events.EventPaymentSucceeded, evID(), p, nil)
	if !r.isPremium(t, user) {
		t.Fatalf("a matching capture after mismatches did not grant")
	}
}

// Mutation guard: the inbox applies an event id once.
func TestPremiumEvent_DuplicateAppliedOnce(t *testing.T) {
	r := newPremiumRig(t)
	user := uuid.New()
	boost, pass := r.buy(t, user, "boost"), r.buy(t, user, "pass_30d")
	bID, pID := evID(), evID()
	for i := 0; i < 3; i++ {
		r.deliver(t, events.EventPaymentSucceeded, bID, boost, nil)
		r.deliver(t, events.EventPaymentSucceeded, pID, pass, nil)
	}
	if got := r.boostBalance(t, user); got != 1 {
		t.Fatalf("boost balance after 3 deliveries = %d, want 1", got)
	}
	approx(t, "pass after 3 deliveries", r.passExpiry(t, user), time.Now().Add(30*day))
	// A different event id for an already paid purchase does not grant twice.
	r.deliver(t, events.EventPaymentSucceeded, evID(), boost, nil)
	if got := r.boostBalance(t, user); got != 1 {
		t.Fatalf("a second capture event granted again: balance %d", got)
	}
	// Captures are also stopped by the purchase status, so the inbox is proven
	// on a partial refund, which the status alone would apply again: the same
	// refund event three times takes back 10 days once (13300 of 39900 paise).
	pass = r.purchase(t, pass)
	rID := evID()
	for i := 0; i < 3; i++ {
		r.deliver(t, events.EventPaymentRefunded, rID, pass, func(m map[string]any) {
			m["amount_minor"] = 13300
			m["status"] = "partially_refunded"
		})
	}
	if got := r.purchase(t, pass); got.RefundedMinor != 13300 || got.Status != payments.StatusPartiallyRefunded {
		t.Fatalf("after one partial refund delivered 3 times: refunded %d, status %s", got.RefundedMinor, got.Status)
	}
	approx(t, "pass after a repeated partial refund", r.passExpiry(t, user), time.Now().Add(20*day))
}

// Mutation guard: a full refund takes back exactly that purchase's time.
func TestPremiumRefund_FullRevokesThatPurchasesTime(t *testing.T) {
	r := newPremiumRig(t)
	ctx := context.Background()
	user := uuid.New()
	a, b := r.buy(t, user, "pass_30d"), r.buy(t, user, "pass_90d")
	r.deliver(t, events.EventPaymentSucceeded, evID(), a, nil)
	r.deliver(t, events.EventPaymentSucceeded, evID(), b, nil)
	a = r.purchase(t, a)
	r.deliver(t, events.EventPaymentRefunded, evID(), a, nil)
	approx(t, "after refunding the 30-day pass", r.passExpiry(t, user), time.Now().Add(90*day))
	if got := r.purchase(t, a); got.Status != payments.StatusRefunded || got.RefundedMinor != 39900 {
		t.Fatalf("refunded purchase = %+v", got)
	}
	st, _ := r.svc.PremiumPurchasePayment(ctx, user, a.ID)
	if st.Status != payments.CustomerStatusPaid || st.RefundStatus == nil || *st.RefundStatus != payments.RefundStatusRefunded {
		t.Fatalf("status after refund = %+v", st)
	}
	b = r.purchase(t, b)
	r.deliver(t, events.EventPaymentRefunded, evID(), b, nil)
	if r.isPremium(t, user) {
		t.Fatalf("still premium after both passes were refunded")
	}
	me, _ := r.svc.MyPremium(ctx, user)
	if me.IsPremium || me.Pass == nil || me.Pass.Active {
		t.Fatalf("me after refunds = %+v", me)
	}
}

func TestPremiumRefund_PartialShortensPassProRata(t *testing.T) {
	r := newPremiumRig(t)
	user := uuid.New()
	p := r.buy(t, user, "pass_30d")
	r.deliver(t, events.EventPaymentSucceeded, evID(), p, nil)
	p = r.purchase(t, p)
	r.deliver(t, events.EventPaymentRefunded, evID(), p, func(m map[string]any) {
		m["amount_minor"] = 19950
		m["status"] = "partially_refunded"
	})
	approx(t, "half refunded", r.passExpiry(t, user), time.Now().Add(15*day))
	if got := r.purchase(t, p); got.Status != payments.StatusPartiallyRefunded || got.RefundedMinor != 19950 {
		t.Fatalf("after partial refund = %+v", got)
	}
	// The rest of the money: the rest of the time.
	r.deliver(t, events.EventPaymentRefunded, evID(), p, func(m map[string]any) {
		m["amount_minor"] = 19950
		m["status"] = "refunded"
	})
	approx(t, "fully refunded", r.passExpiry(t, user), time.Now())
	if r.isPremium(t, user) {
		t.Fatalf("premium after a full refund in two parts")
	}
	// More than was paid is a mismatch with no effect.
	id := evID()
	r.deliver(t, events.EventPaymentRefunded, id, p, func(m map[string]any) { m["amount_minor"] = 1 })
	if got := r.inboxOutcome(t, id); !strings.HasPrefix(got, "refund_ignored|") {
		t.Fatalf("refund on a refunded purchase: %q", got)
	}
}

func TestPremiumRefund_Boost(t *testing.T) {
	r := newPremiumRig(t)
	ctx := context.Background()
	user := uuid.New()
	kept, refunded, spent := r.buy(t, user, "boost"), r.buy(t, user, "boost"), r.buy(t, user, "boost")
	for _, p := range []*store.PremiumPurchase{kept, refunded, spent} {
		r.deliver(t, events.EventPaymentSucceeded, evID(), p, nil)
	}
	if got := r.boostBalance(t, user); got != 3 {
		t.Fatalf("balance = %d, want 3", got)
	}
	// Partial refund: the token stays.
	r.deliver(t, events.EventPaymentRefunded, evID(), r.purchase(t, kept), func(m map[string]any) {
		m["amount_minor"] = 1000
		m["status"] = "partially_refunded"
	})
	if got := r.boostBalance(t, user); got != 3 {
		t.Fatalf("partial refund changed the balance to %d", got)
	}
	// Full refund of an unused token: taken back.
	r.deliver(t, events.EventPaymentRefunded, evID(), r.purchase(t, refunded), nil)
	if got := r.boostBalance(t, user); got != 2 {
		t.Fatalf("full refund left balance %d, want 2", got)
	}
	// Spend both remaining tokens, then refund the third purchase in full.
	for i := 0; i < 2; i++ {
		res, err := r.svc.RequestBoost(ctx, user)
		if err != nil || !res.Granted || res.Source != "boost_token" {
			t.Fatalf("spend %d: %+v %v", i, res, err)
		}
	}
	id := evID()
	r.deliver(t, events.EventPaymentRefunded, id, r.purchase(t, spent), nil)
	if got := r.boostBalance(t, user); got != 0 {
		t.Fatalf("balance after refunding a spent token = %d", got)
	}
	if got := r.inboxOutcome(t, id); got != "refunded|boost token already used; nothing to take back" {
		t.Fatalf("inbox = %q", got)
	}
	if _, err := r.svc.RequestBoost(ctx, user); err == nil {
		t.Fatalf("a boost was granted with no token and no pass")
	}
}

// Mutation guard: a purchase's payment status is its buyer's alone.
func TestPremiumPaymentStatus_OwnerOnly(t *testing.T) {
	r := newPremiumRig(t)
	ctx := context.Background()
	owner, stranger := uuid.New(), uuid.New()
	p := r.buy(t, owner, "boost")
	if _, err := r.svc.PremiumPurchasePayment(ctx, stranger, p.ID); !errors.Is(err, store.ErrPurchaseNotFound) {
		t.Fatalf("stranger read: %v", err)
	}
	if _, err := r.svc.PremiumPurchasePayment(ctx, owner, uuid.New()); !errors.Is(err, store.ErrPurchaseNotFound) {
		t.Fatalf("missing purchase: %v", err)
	}
	st, err := r.svc.PremiumPurchasePayment(ctx, owner, p.ID)
	if err != nil || st.Status != payments.CustomerStatusConfirming || st.AmountMinor != 4900 {
		t.Fatalf("owner read = %+v err=%v", st, err)
	}
}

func TestPremiumPayment_FailedThenCaptured(t *testing.T) {
	r := newPremiumRig(t)
	ctx := context.Background()
	user := uuid.New()
	p := r.buy(t, user, "pass_30d")
	r.deliver(t, events.EventPaymentFailed, evID(), p, nil)
	st, _ := r.svc.PremiumPurchasePayment(ctx, user, p.ID)
	if st.Status != payments.CustomerStatusFailed || r.isPremium(t, user) {
		t.Fatalf("after failure: %+v", st)
	}
	var failures int
	for _, e := range r.rec.events(t) {
		if e.EventType == events.EventDatingPremiumPaymentFailure {
			failures++
		}
	}
	if failures != 1 {
		t.Fatalf("payment_failure events = %d", failures)
	}
	// The user retried the same intent and it captured: granted.
	r.deliver(t, events.EventPaymentSucceeded, evID(), p, nil)
	if st, _ = r.svc.PremiumPurchasePayment(ctx, user, p.ID); st.Status != payments.CustomerStatusPaid || !r.isPremium(t, user) {
		t.Fatalf("capture after failure: %+v", st)
	}
}

// Mutation guard: a NULL expires_at is never an active pass, and the schema
// migration closes such rows and restores NOT NULL.
func TestPremiumEntitlement_NullExpiryNeverActive(t *testing.T) {
	r := newPremiumRig(t)
	ctx := context.Background()
	user := uuid.New()
	t.Cleanup(func() {
		// Restore the constraint even when the test failed half way.
		c, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		tx, err := r.st.BeginTx(c)
		if err != nil {
			return
		}
		_, _ = tx.Exec(c, `UPDATE dating_premium_subscriptions SET expires_at = started_at WHERE expires_at IS NULL`)
		_, _ = tx.Exec(c, `ALTER TABLE dating_premium_subscriptions ALTER COLUMN expires_at SET NOT NULL`)
		_ = tx.Commit(c)
	})
	d8Exec(t, r.st, `ALTER TABLE dating_premium_subscriptions ALTER COLUMN expires_at DROP NOT NULL`)
	d8Exec(t, r.st, `INSERT INTO dating_premium_subscriptions (user_id, plan, started_at, expires_at, source)
        VALUES ($1, 'monthly_399', now() - interval '1 day', NULL, 'legacy')`, user)
	if r.isPremium(t, user) {
		t.Fatalf("a NULL expires_at counted as an active pass")
	}
	if me, err := r.svc.MyPremium(ctx, user); err != nil || me.IsPremium {
		t.Fatalf("me with NULL expiry = %+v err=%v", me, err)
	}
	if _, err := r.svc.RequestBoost(ctx, user); err == nil {
		t.Fatalf("a NULL-expiry row earned the daily boost")
	}
	// Re-apply the whole schema, as a boot does (no arguments: simple protocol).
	d8Exec(t, r.st, database.SetupSQL)
	if n := d8QueryInt(t, r.st, `SELECT COUNT(*)::int FROM dating_premium_subscriptions WHERE user_id = $1 AND expires_at IS NOT NULL AND expires_at <= now()`, user); n != 1 {
		t.Fatalf("migration did not close the NULL-expiry row")
	}
	if n := d8QueryInt(t, r.st, `SELECT COUNT(*)::int FROM pg_attribute WHERE attrelid = 'dating_premium_subscriptions'::regclass AND attname = 'expires_at' AND attnotnull`); n != 1 {
		t.Fatalf("expires_at is not NOT NULL after the schema")
	}
}

func TestPremiumExpiryReminder_OncePerPurchase(t *testing.T) {
	r := newPremiumRig(t)
	ctx := context.Background()
	user := uuid.New()
	remindersFor := func() []string {
		var ids []string
		for _, e := range r.rec.events(t) {
			if e.EventType != events.EventDatingPremiumExpiringSoon {
				continue
			}
			var p struct {
				UserID     string `json:"user_id"`
				PurchaseID string `json:"purchase_id"`
			}
			_ = json.Unmarshal(e.Payload, &p)
			if p.UserID == user.String() {
				ids = append(ids, p.PurchaseID)
			}
		}
		return ids
	}
	first := r.buy(t, user, "pass_30d")
	r.deliver(t, events.EventPaymentSucceeded, evID(), first, nil)
	if _, err := r.svc.SendPremiumExpiryReminders(ctx, 1000); err != nil {
		t.Fatal(err)
	}
	if got := remindersFor(); len(got) != 0 {
		t.Fatalf("reminded 30 days ahead: %v", got)
	}
	d8Exec(t, r.st, `UPDATE dating_premium_subscriptions SET expires_at = now() + interval '2 days' WHERE user_id = $1`, user)
	for i := 0; i < 3; i++ {
		if _, err := r.svc.SendPremiumExpiryReminders(ctx, 1000); err != nil {
			t.Fatal(err)
		}
	}
	if got := remindersFor(); len(got) != 1 || got[0] != first.ID.String() {
		t.Fatalf("reminders after 3 sweeps = %v, want exactly [%s]", got, first.ID)
	}
	// A new pass pushes expiry out; when that nears expiry it gets its own.
	second := r.buy(t, user, "pass_30d")
	r.deliver(t, events.EventPaymentSucceeded, evID(), second, nil)
	_, _ = r.svc.SendPremiumExpiryReminders(ctx, 1000)
	if got := remindersFor(); len(got) != 1 {
		t.Fatalf("reminded a pass 32 days out: %v", got)
	}
	d8Exec(t, r.st, `UPDATE dating_premium_subscriptions SET expires_at = now() + interval '1 day' WHERE user_id = $1`, user)
	_, _ = r.svc.SendPremiumExpiryReminders(ctx, 1000)
	_, _ = r.svc.SendPremiumExpiryReminders(ctx, 1000)
	if got := remindersFor(); len(got) != 2 || got[1] != second.ID.String() {
		t.Fatalf("reminders = %v, want [%s %s]", got, first.ID, second.ID)
	}
}

func TestPremiumPurgeAndExport(t *testing.T) {
	r := newPremiumRig(t)
	ctx := context.Background()
	user := uuid.New()
	seedActiveProfile(t, r.st, user)
	p := r.buy(t, user, "boost")
	r.deliver(t, events.EventPaymentSucceeded, evID(), p, nil)
	raw, err := r.svc.BuildExportPayload(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	var export UserDataExport
	if err := json.Unmarshal(raw, &export); err != nil {
		t.Fatal(err)
	}
	if len(export.PremiumPurchases) != 1 || export.PremiumPurchases[0].ID != p.ID || export.BoostBalance == nil || *export.BoostBalance != 1 {
		t.Fatalf("export premium = %+v balance=%v", export.PremiumPurchases, export.BoostBalance)
	}
	p = r.purchase(t, p)
	if err := r.svc.PurgeProfile(ctx, user); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if list, _ := r.st.ListPremiumPurchasesForUser(ctx, user); len(list) != 0 || r.boostBalance(t, user) != 0 {
		t.Fatalf("purchases or balance survived the purge")
	}
	// A late refund for the purged purchase is recorded, not retried.
	id := evID()
	r.deliver(t, events.EventPaymentRefunded, id, p, nil)
	if got := r.inboxOutcome(t, id); !strings.HasPrefix(got, "purchase_not_found|") {
		t.Fatalf("late refund after purge: %q", got)
	}
}
