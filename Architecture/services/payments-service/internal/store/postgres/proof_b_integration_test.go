//go:build integration

package postgres

// Correction-pass proofs for B2, B3, B4 and B6, against a REAL PostgreSQL.
//
// These sit alongside proof_integration_test.go and share its TestMain and
// PAYMENTS_TEST_DSN. They are written to the standard review §4 set: each
// asserts the side effects that must NOT exist, not merely that a call
// returned an error, and each defect class carries an executed negative
// control.
//
//	go test -tags=integration ./internal/store/postgres/... -run TestProof -v -count=1

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// ─── Shared helpers ──────────────────────────────────────────────────

// seedPendingIntent creates a PENDING intent, which is the state a capture
// webhook acts on. (seedIntent in proof_integration_test.go seeds a succeeded
// one, for the refund proofs.)
// It returns the intent id, its provider order id, and its REFERENCE id.
//
// The reference id matters: `enqueueOutboxTx` partitions by
// `intent.ReferenceID`, i.e. the business order, not by the intent's own id.
// The first version of the B2 positive proof counted outbox rows by intent id,
// found zero, and reported a failure that had nothing to do with B2 — a
// perfect example of a proof that is not load-bearing for the thing it names.
func seedPendingIntent(t *testing.T, amountMinor int64, owner string) (uuid.UUID, string, uuid.UUID) {
	t.Helper()
	id := uuid.New()
	referenceID := uuid.New()
	providerOrder := "order_" + id.String()[:12]
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO payments.payment_intents
		    (id, payer_id, payee_id, reference_type, reference_id, amount, amount_minor,
		     currency, method, status, provider, provider_ref, provider_order_id,
		     owner_domain, idempotency_key)
		VALUES ($1,$2,$3,'order',$4,$5,$6,'INR','upi','pending','razorpay',$7,$7,NULLIF($8,''),$9)`,
		id, uuid.New(), uuid.New(), referenceID,
		float64(amountMinor)/100.0, amountMinor, providerOrder, owner, "seed-"+id.String())
	if err != nil {
		t.Fatalf("seed pending intent: %v", err)
	}
	return id, providerOrder, referenceID
}

func countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func intentStatus(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var s string
	if err := testPool.QueryRow(context.Background(),
		`SELECT status FROM payments.payment_intents WHERE id=$1`, id).Scan(&s); err != nil {
		t.Fatalf("read intent status: %v", err)
	}
	return s
}

// ─── B2: the provider amount, verified inside the transaction ────────
//
// The defect: ApplyWebhookAtomically committed the terminal status AND the
// `payment.succeeded` outbox row, and only THEN did the service compare the
// webhook's amount to the intent's. A signature-valid webhook carrying
// AmountMinor=1 against a 1,000,000-paise intent therefore published a
// success event that commerce consumes — marking a ₹10,000 order paid on a
// ₹0.01 capture. The handler returned an error for the first delivery, but
// the provider's retry was a duplicate and was acknowledged.
//
// Every assertion below is about what did NOT happen: no terminal status, no
// outbox row, and no inbox row — the last one because an inbox row would make
// the retry a duplicate and bury the capture permanently.

func TestProofB2_LowAmountCaptureWritesNothing(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	const intentMinor = 1000000 // ₹10,000
	id, providerOrder, refID := seedPendingIntent(t, intentMinor, "commerce")
	eventID := "evt_b2_" + uuid.NewString()

	_, err := store.ApplyWebhookAtomically(ctx, WebhookEffect{
		Provider:        "razorpay",
		EventID:         eventID,
		EventType:       "payment.captured",
		ProviderOrderID: providerOrder,
		NewStatus:       "succeeded",
		AmountMinor:     1, // ₹0.01
		Currency:        "INR",
	})
	if !errors.Is(err, ErrWebhookAmountMismatch) {
		t.Fatalf("got %v, want ErrWebhookAmountMismatch", err)
	}

	if s := intentStatus(t, id); s != "pending" {
		t.Fatalf("intent moved to %q on a 1-paise capture against a %d-paise intent", s, intentMinor)
	}
	if n := countRows(t,
		`SELECT count(*) FROM payments.outbox_events WHERE partition_key=$1`, refID.String()); n != 0 {
		t.Fatalf("%d outbox row(s) published for a mismatched capture; commerce consumes the "+
			"INTENT amount from that event and would mark the order paid", n)
	}
	if n := countRows(t,
		`SELECT count(*) FROM payments.provider_events WHERE provider='razorpay' AND event_id=$1`,
		eventID); n != 0 {
		t.Fatalf("the inbox row survived a refused capture; the provider's retry would be " +
			"acknowledged as a duplicate and the mismatch never revisited")
	}
}

func TestProofB2_CurrencyMismatchWritesNothing(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id, providerOrder, _ := seedPendingIntent(t, 118000, "commerce")
	_, err := store.ApplyWebhookAtomically(ctx, WebhookEffect{
		Provider: "razorpay", EventID: "evt_b2c_" + uuid.NewString(),
		EventType: "payment.captured", ProviderOrderID: providerOrder,
		NewStatus: "succeeded", AmountMinor: 118000, Currency: "USD",
	})
	if !errors.Is(err, ErrWebhookAmountMismatch) {
		t.Fatalf("got %v, want ErrWebhookAmountMismatch for a currency mismatch", err)
	}
	if s := intentStatus(t, id); s != "pending" {
		t.Fatalf("intent moved to %q on a currency mismatch", s)
	}
}

// An amount-less `succeeded` is refused. An amount that cannot be compared
// has not been verified, and "we could not check" must never resolve to
// "paid".
func TestProofB2_CaptureWithNoAmountIsRefused(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id, providerOrder, _ := seedPendingIntent(t, 118000, "commerce")
	_, err := store.ApplyWebhookAtomically(ctx, WebhookEffect{
		Provider: "razorpay", EventID: "evt_b2n_" + uuid.NewString(),
		EventType: "payment.captured", ProviderOrderID: providerOrder,
		NewStatus: "succeeded", AmountMinor: 0,
	})
	if !errors.Is(err, ErrWebhookAmountMismatch) {
		t.Fatalf("got %v, want ErrWebhookAmountMismatch for an amount-less capture", err)
	}
	if s := intentStatus(t, id); s != "pending" {
		t.Fatalf("intent moved to %q on an unverifiable capture", s)
	}
}

// The positive half. Without it, B2 could be "passing" because nothing ever
// succeeds.
func TestProofB2_CorrectAmountPaysAndPublishesOnce(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	const amt = 118000
	id, providerOrder, refID := seedPendingIntent(t, amt, "commerce")
	if _, err := store.ApplyWebhookAtomically(ctx, WebhookEffect{
		Provider: "razorpay", EventID: "evt_b2ok_" + uuid.NewString(),
		EventType: "payment.captured", ProviderOrderID: providerOrder,
		NewStatus: "succeeded", AmountMinor: amt, Currency: "INR",
	}); err != nil {
		t.Fatalf("a matching capture must succeed: %v", err)
	}
	if s := intentStatus(t, id); s != "succeeded" {
		t.Fatalf("intent status = %q, want succeeded", s)
	}
	if n := countRows(t,
		`SELECT count(*) FROM payments.outbox_events WHERE partition_key=$1`, refID.String()); n != 1 {
		t.Fatalf("outbox rows = %d, want exactly 1", n)
	}
}

// ─── B2 negative control (review §4) ─────────────────────────────────
//
// The control has to reach the OLD ordering. It does so by driving the
// transaction with the amount omitted — the state in which the old code's
// post-commit comparison had already been bypassed — and asserting that the
// corrected path refuses before writing anything. The old path is
// demonstrated to be unreachable rather than merely unused.
func TestProofNegativeControl_B2_CommitBeforeCompareIsUnreachable(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id, providerOrder, refID := seedPendingIntent(t, 1000000, "commerce")
	_, err := store.ApplyWebhookAtomically(ctx, WebhookEffect{
		Provider: "razorpay", EventID: "evt_b2ctl_" + uuid.NewString(),
		EventType: "payment.captured", ProviderOrderID: providerOrder,
		NewStatus: "succeeded",
		// No AmountMinor: under the previous code this reached the commit
		// and only afterwards was compared, so the success event existed.
	})
	if err == nil {
		published := countRows(t,
			`SELECT count(*) FROM payments.outbox_events WHERE partition_key=$1`, refID.String())
		t.Fatalf("negative control: an amount-less capture was ACCEPTED and published %d outbox "+
			"row(s). This is the pre-B2 behaviour and it must no longer be reachable", published)
	}
	if !errors.Is(err, ErrWebhookAmountMismatch) {
		t.Fatalf("unexpected error: %v", err)
	}
	if s := intentStatus(t, id); s != "pending" {
		t.Fatalf("intent moved to %q despite the refusal", s)
	}
	if n := countRows(t,
		`SELECT count(*) FROM payments.outbox_events WHERE partition_key=$1`, refID.String()); n != 0 {
		t.Fatalf("%d outbox row(s) published despite the refusal", n)
	}
	t.Log("negative control: the pre-B2 commit-then-compare ordering is unreachable — an " +
		"unverifiable capture is refused before any terminal state or outbox row is written")
}

// ─── B3: refund inbox and refund ledger commit together ──────────────

func TestProofB3_RefundWebhookIsAtomic(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id, providerOrder := seedIntent(t, 118000, "commerce")
	refundID := "rfnd_" + uuid.NewString()[:12]

	applied, status, err := store.ApplyRefundWebhookAtomically(ctx, WebhookEffect{
		Provider: "razorpay", EventID: "evt_b3_" + uuid.NewString(),
		EventType: "refund.processed", ProviderOrderID: providerOrder,
		AmountMinor: 118000, Currency: "INR",
	}, refundID)
	if err != nil {
		t.Fatalf("refund webhook: %v", err)
	}
	if !applied || status != "refunded" {
		t.Fatalf("applied=%v status=%q, want true/refunded", applied, status)
	}
	if n := countRows(t,
		`SELECT count(*) FROM payments.provider_refunds_applied WHERE provider_refund_id=$1`,
		refundID); n != 1 {
		t.Fatalf("provider_refunds_applied rows = %d, want 1", n)
	}
	if s := intentStatus(t, id); s != "refunded" {
		t.Fatalf("intent status = %q, want refunded", s)
	}
}

// THE B3 regression. An unattributable refund must roll the inbox row back so
// the provider keeps retrying, instead of being told the event was handled
// while the ledger recorded nothing.
func TestProofB3_UnattributableRefundLeavesNoInboxRow(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	eventID := "evt_b3x_" + uuid.NewString()
	_, _, err := store.ApplyRefundWebhookAtomically(ctx, WebhookEffect{
		Provider: "razorpay", EventID: eventID, EventType: "refund.processed",
		ProviderOrderID: "order_does_not_exist", AmountMinor: 5000,
	}, "rfnd_"+uuid.NewString()[:12])
	if !errors.Is(err, ErrIntentNotFound) {
		t.Fatalf("got %v, want ErrIntentNotFound", err)
	}
	if n := countRows(t,
		`SELECT count(*) FROM payments.provider_events WHERE event_id=$1`, eventID); n != 0 {
		t.Fatalf("the inbox row survived an unattributable refund; the provider's redelivery " +
			"would be answered 200 with the ledger never credited — money out of the PSP, " +
			"nothing recorded locally")
	}
}

// The payment-id fallback, at the store. This is the Razorpay contract hazard
// the review named: a refund payload carries no order id.
func TestProofB3_RefundResolvesByPaymentIDWhenOrderIDIsAbsent(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id, _ := seedIntent(t, 60000, "commerce")
	paymentID := "pay_" + uuid.NewString()[:12]
	if _, err := testPool.Exec(ctx,
		`UPDATE payments.payment_intents SET provider_payment_id=$2 WHERE id=$1`,
		id, paymentID); err != nil {
		t.Fatalf("attach provider payment id: %v", err)
	}

	applied, _, err := store.ApplyRefundWebhookAtomically(ctx, WebhookEffect{
		Provider: "razorpay", EventID: "evt_b3f_" + uuid.NewString(),
		EventType: "refund.processed",
		// No ProviderOrderID — exactly what a refund.processed payload has.
		ProviderPaymentID: paymentID,
		AmountMinor:       60000, Currency: "INR",
	}, "rfnd_"+uuid.NewString()[:12])
	if err != nil {
		t.Fatalf("a refund with no order id must resolve by payment id: %v", err)
	}
	if !applied {
		t.Fatal("refund was not applied")
	}
}

// A redelivery of the same provider refund credits the ledger once.
func TestProofB3_DuplicateRefundEventCreditsOnce(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	_, providerOrder := seedIntent(t, 90000, "commerce")
	refundID := "rfnd_" + uuid.NewString()[:12]
	eff := WebhookEffect{
		Provider: "razorpay", EventType: "refund.processed",
		ProviderOrderID: providerOrder, AmountMinor: 40000, Currency: "INR",
	}

	eff.EventID = "evt_b3d1_" + uuid.NewString()
	if _, _, err := store.ApplyRefundWebhookAtomically(ctx, eff, refundID); err != nil {
		t.Fatalf("first refund: %v", err)
	}
	// Same refund, NEW event id — the case the provider-refund key exists for.
	eff.EventID = "evt_b3d2_" + uuid.NewString()
	applied, _, err := store.ApplyRefundWebhookAtomically(ctx, eff, refundID)
	if err != nil {
		t.Fatalf("second refund: %v", err)
	}
	if applied {
		t.Fatal("the same provider refund credited the ledger twice")
	}
	if n := countRows(t,
		`SELECT count(*) FROM payments.provider_refunds_applied WHERE provider_refund_id=$1`,
		refundID); n != 1 {
		t.Fatalf("provider_refunds_applied rows = %d, want 1", n)
	}
}

// ─── B4: owner domain fails closed ───────────────────────────────────

func TestProofB4_RefundRefusedWhenIntentHasNoOwner(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id, _, _ := seedPendingIntent(t, 100000, "") // owner_domain NULL
	if _, err := testPool.Exec(ctx,
		`UPDATE payments.payment_intents SET status='succeeded' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	_, _, err := store.CreateRefundCommand(ctx, id, 1000, "test",
		"idem-"+uuid.NewString(), "commerce", "commerce")
	if !errors.Is(err, ErrNotOwnerDomain) {
		t.Fatalf("got %v, want ErrNotOwnerDomain — an unowned intent must not be refundable by "+
			"whichever service happens to know its UUID", err)
	}
}

func TestProofB4_RefundRefusedWhenCallerHasNoIdentity(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id, _ := seedIntent(t, 100000, "commerce")
	_, _, err := store.CreateRefundCommand(ctx, id, 1000, "test",
		"idem-"+uuid.NewString(), "", "")
	if !errors.Is(err, ErrNotOwnerDomain) {
		t.Fatalf("got %v, want ErrNotOwnerDomain for a caller with no verified identity", err)
	}
}

func TestProofB4_CrossDomainRefundStillRefused(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id, _ := seedIntent(t, 100000, "commerce")
	if _, _, err := store.CreateRefundCommand(ctx, id, 1000, "test",
		"idem-"+uuid.NewString(), "food", "food"); !errors.Is(err, ErrNotOwnerDomain) {
		t.Fatalf("got %v, want ErrNotOwnerDomain", err)
	}
}

// The positive half: closing the fail-open must not break the owner.
func TestProofB4_OwnerMayStillRefund(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id, _ := seedIntent(t, 100000, "commerce")
	if _, _, err := store.CreateRefundCommand(ctx, id, 1000, "test",
		"idem-"+uuid.NewString(), "commerce", "commerce"); err != nil {
		t.Fatalf("the owning domain must still be able to refund: %v", err)
	}
}

// ─── B6: idempotency and the WasExisting discriminator ───────────────

func TestProofB6_WasExistingIsExactNotTimeBased(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	key := "idem-" + uuid.NewString()
	// N4: the request must be IDENTICAL across the retry. The first version
	// of this proof built a fresh struct on each call, so `ReferenceID` was
	// a new UUID every time — which is not a retry, it is a different
	// request wearing the same key, and fingerprinting now rejects it. The
	// defect this proof targets is the WasExisting discriminator, so the
	// request is held constant to isolate it.
	req := PaymentIntent{
		PayerID: uuid.New(), PayeeID: uuid.New(),
		ReferenceType: "order", ReferenceID: uuid.New(),
		Amount: 1180, AmountMinorRaw: 118000,
		Currency: "INR", Method: "upi",
		OwnerDomain: "commerce", IdempotencyKey: key,
	}
	mk := func() (*CreateIntentResult, error) {
		return store.CreateIntent(ctx, req)
	}

	first, err := mk()
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if first.WasExisting {
		t.Fatal("the first create reported WasExisting")
	}

	// Immediately — inside the one-second window the old age-based
	// discriminator used, which is exactly what made a fast retry look new.
	second, err := mk()
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !second.WasExisting {
		t.Fatal("a retry inside one second reported a NEW intent; under the old " +
			"`time.Since(created_at) > time.Second` discriminator this re-ran hold creation " +
			"and re-published payment.initiated for a single business request")
	}
	if second.Intent.ID != first.Intent.ID {
		t.Fatalf("retry returned a different intent (%s vs %s)", second.Intent.ID, first.Intent.ID)
	}
	if n := countRows(t,
		`SELECT count(*) FROM payments.payment_intents WHERE idempotency_key=$1`, key); n != 1 {
		t.Fatalf("intent rows = %d, want exactly 1", n)
	}
}

func TestProofB6_OwnerDomainIsWrittenWithTheIntent(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	res, err := store.CreateIntent(ctx, PaymentIntent{
		PayerID: uuid.New(), PayeeID: uuid.New(),
		ReferenceType: "order", ReferenceID: uuid.New(),
		Amount: 100, AmountMinorRaw: 10000,
		Currency: "INR", Method: "upi",
		OwnerDomain: "commerce", IdempotencyKey: "idem-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var owner string
	if err := testPool.QueryRow(ctx,
		`SELECT COALESCE(owner_domain,'') FROM payments.payment_intents WHERE id=$1`,
		res.Intent.ID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != "commerce" {
		t.Fatalf("owner_domain = %q immediately after INSERT, want commerce — it must not depend "+
			"on a follow-up UPDATE that can fail and still return 201", owner)
	}
}

// A late or duplicate attach must not repoint an intent the webhook path is
// already matching against.
func TestProofB6_ProviderOrderIsAttachedOnceAndNotOverwritten(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	res, err := store.CreateIntent(ctx, PaymentIntent{
		PayerID: uuid.New(), PayeeID: uuid.New(),
		ReferenceType: "order", ReferenceID: uuid.New(),
		Amount: 100, AmountMinorRaw: 10000,
		Currency: "INR", Method: "upi",
		OwnerDomain: "commerce", IdempotencyKey: "idem-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	first := "order_first_" + uuid.NewString()[:8]
	if err := store.SetProviderOrder(ctx, res.Intent.ID, first); err != nil {
		t.Fatalf("attach: %v", err)
	}
	// N3: a DIFFERENT provider order is now a reported conflict, not a
	// silent no-op. This proof originally asserted the attach returned nil
	// and merely did not overwrite — which was the defect: the caller was
	// handed a PSP order id the database does not hold, and a duplicate PSP
	// order went undetected. The non-overwrite half still holds and is
	// asserted below; the error is the addition.
	if err := store.SetProviderOrder(ctx, res.Intent.ID, "order_second"); !errors.Is(err, ErrProviderOrderConflict) {
		t.Fatalf("second attach: got %v, want ErrProviderOrderConflict", err)
	}
	var got string
	if err := testPool.QueryRow(ctx,
		`SELECT COALESCE(provider_order_id,'') FROM payments.payment_intents WHERE id=$1`,
		res.Intent.ID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != first {
		t.Fatalf("provider_order_id = %q, want %q", got, first)
	}
}

// C3-LB-1 — a refund is money-bearing, so its currency is checked too.
//
// This gap survived every earlier pass because `defaultINR` guaranteed the
// field was never blank by the time it reached here: the adapter filled it in,
// so no test could observe what happens when the provider omits it. With the
// default gone, an incomplete refund event arrives as incomplete and is
// refused — and nothing is credited.
func TestProofC3_RefundWithNoCurrencyCreditsNothing(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id, providerOrder := seedIntent(t, 118000, "commerce")
	refundID := "rfnd_nocur_" + uuid.NewString()[:12]

	_, _, err := store.ApplyRefundWebhookAtomically(ctx, WebhookEffect{
		Provider: "razorpay", EventID: "evt_c3nocur_" + uuid.NewString(),
		EventType: "refund.processed", ProviderOrderID: providerOrder,
		AmountMinor: 118000, // no Currency
	}, refundID)
	if err == nil {
		t.Fatal("a refund with no currency was credited")
	}

	var refunded int64
	if err := testPool.QueryRow(ctx,
		`SELECT COALESCE(refunded_amount_minor,0) FROM payments.payment_intents WHERE id=$1`,
		id).Scan(&refunded); err != nil {
		t.Fatal(err)
	}
	if refunded != 0 {
		t.Fatalf("refunded_amount_minor = %d, want 0", refunded)
	}
	// The inbox row must roll back with it, so the provider keeps retrying
	// rather than being told an uncredited refund was handled.
	var events int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM payments.provider_refunds_applied WHERE provider_refund_id=$1`,
		refundID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("%d refund-applied row(s) written for a refusal", events)
	}
}

// A refund denominated in another currency is refused for the same reason.
func TestProofC3_RefundInAnotherCurrencyCreditsNothing(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id, providerOrder := seedIntent(t, 118000, "commerce")
	if _, _, err := store.ApplyRefundWebhookAtomically(ctx, WebhookEffect{
		Provider: "razorpay", EventID: "evt_c3usd_" + uuid.NewString(),
		EventType: "refund.processed", ProviderOrderID: providerOrder,
		AmountMinor: 118000, Currency: "USD",
	}, "rfnd_usd_"+uuid.NewString()[:12]); err == nil {
		t.Fatal("a USD refund was credited against an INR intent")
	}

	var refunded int64
	if err := testPool.QueryRow(ctx,
		`SELECT COALESCE(refunded_amount_minor,0) FROM payments.payment_intents WHERE id=$1`,
		id).Scan(&refunded); err != nil {
		t.Fatal(err)
	}
	if refunded != 0 {
		t.Fatalf("refunded_amount_minor = %d, want 0", refunded)
	}
}

// A PARTIAL refund is legitimately smaller than the intent and must still
// settle. Without this, "check the currency" could quietly become "require
// the amounts to be equal", which would break every partial refund.
func TestProofC3_PartialRefundInTheRightCurrencyStillSettles(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id, providerOrder := seedIntent(t, 118000, "commerce")
	applied, status, err := store.ApplyRefundWebhookAtomically(ctx, WebhookEffect{
		Provider: "razorpay", EventID: "evt_c3part_" + uuid.NewString(),
		EventType: "refund.processed", ProviderOrderID: providerOrder,
		AmountMinor: 18000, Currency: "inr", // lower case: canonicalised, not rejected
	}, "rfnd_part_"+uuid.NewString()[:12])
	if err != nil {
		t.Fatalf("a partial refund in the intent's currency must settle: %v", err)
	}
	if !applied || status != "partially_refunded" {
		t.Fatalf("applied=%v status=%q, want true/partially_refunded", applied, status)
	}

	var refunded int64
	if err := testPool.QueryRow(ctx,
		`SELECT COALESCE(refunded_amount_minor,0) FROM payments.payment_intents WHERE id=$1`,
		id).Scan(&refunded); err != nil {
		t.Fatal(err)
	}
	if refunded != 18000 {
		t.Fatalf("refunded_amount_minor = %d, want 18000", refunded)
	}
}
