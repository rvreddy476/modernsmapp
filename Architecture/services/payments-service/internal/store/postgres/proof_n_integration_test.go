//go:build integration

package postgres

// Correction-pass proofs for the payments half of review §6:
//
//	N3  a zero-row provider-order attach is a CONFLICT, not a silent success
//	N4  the idempotency key is fingerprinted before any PSP call
//	N5  refund attribution must resolve to exactly one intent, unambiguously
//
// N2 (the reconciler passing amount/currency) is proven at the service layer
// in reconcile_n2_test.go, because the defect is in what the reconciler
// SENDS, not in what the store does with it.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func newIntentReq(key, owner string, amountMinor int64) PaymentIntent {
	return PaymentIntent{
		PayerID: uuid.New(), PayeeID: uuid.New(),
		ReferenceType: "order", ReferenceID: uuid.New(),
		Amount: float64(amountMinor) / 100.0, AmountMinorRaw: amountMinor,
		Currency: "INR", Method: "upi",
		OwnerDomain: owner, IdempotencyKey: key,
	}
}

// ─── N4: the idempotency key is fingerprinted ────────────────────────
//
// `idempotency_key` is unique across every domain sharing payments-service,
// and the conflict path returned the existing row while comparing nothing.
// Reusing a key disclosed another domain's intent AND let the new request's
// amount drive provider order creation against the old row.

func TestProofN4_KeyReusedByAnotherDomainIsRefused(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	key := "idem-" + uuid.NewString()

	if _, err := store.CreateIntent(ctx, newIntentReq(key, "commerce", 118000)); err != nil {
		t.Fatalf("first create: %v", err)
	}
	// food-service reuses the key. Before N4 this returned commerce's intent.
	_, err := store.CreateIntent(ctx, newIntentReq(key, "food", 118000))
	if !errors.Is(err, ErrIdempotencyFingerprint) {
		t.Fatalf("got %v, want ErrIdempotencyFingerprint — a key belonging to another domain "+
			"must not disclose or reuse its intent", err)
	}
}

func TestProofN4_KeyReusedWithADifferentAmountIsRefused(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	key := "idem-" + uuid.NewString()

	first := newIntentReq(key, "commerce", 118000)
	if _, err := store.CreateIntent(ctx, first); err != nil {
		t.Fatalf("first create: %v", err)
	}
	// Same key, ten times the amount. Before N4 the existing row came back
	// and InitiatePayment would attach a provider order for the NEW amount.
	second := first
	second.AmountMinorRaw = 1180000
	second.Amount = 11800
	_, err := store.CreateIntent(ctx, second)
	if !errors.Is(err, ErrIdempotencyFingerprint) {
		t.Fatalf("got %v, want ErrIdempotencyFingerprint for a changed amount", err)
	}
}

func TestProofN4_KeyReusedForADifferentReferenceIsRefused(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	key := "idem-" + uuid.NewString()

	first := newIntentReq(key, "commerce", 118000)
	if _, err := store.CreateIntent(ctx, first); err != nil {
		t.Fatalf("first create: %v", err)
	}
	second := first
	second.ReferenceID = uuid.New() // a different order
	if _, err := store.CreateIntent(ctx, second); !errors.Is(err, ErrIdempotencyFingerprint) {
		t.Fatalf("got %v, want ErrIdempotencyFingerprint for a different reference", err)
	}
}

func TestProofN4_KeyReusedForADifferentPayerIsRefused(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	key := "idem-" + uuid.NewString()

	first := newIntentReq(key, "commerce", 118000)
	if _, err := store.CreateIntent(ctx, first); err != nil {
		t.Fatalf("first create: %v", err)
	}
	second := first
	second.PayerID = uuid.New()
	if _, err := store.CreateIntent(ctx, second); !errors.Is(err, ErrIdempotencyFingerprint) {
		t.Fatalf("got %v, want ErrIdempotencyFingerprint for a different payer", err)
	}
}

func TestProofN4_KeyReusedForADifferentMethodIsRefused(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	key := "idem-" + uuid.NewString()

	first := newIntentReq(key, "commerce", 118000)
	if _, err := store.CreateIntent(ctx, first); err != nil {
		t.Fatalf("first create: %v", err)
	}
	second := first
	second.Method = "card"
	if _, err := store.CreateIntent(ctx, second); !errors.Is(err, ErrIdempotencyFingerprint) {
		t.Fatalf("got %v, want ErrIdempotencyFingerprint for a different method", err)
	}
}

// The positive half: a GENUINE retry — same key, same everything — still
// returns the same intent. Fingerprinting must not break idempotency, which
// is the entire point of the key.
func TestProofN4_GenuineRetryStillReturnsTheSameIntent(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	key := "idem-" + uuid.NewString()
	req := newIntentReq(key, "commerce", 118000)

	first, err := store.CreateIntent(ctx, req)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	second, err := store.CreateIntent(ctx, req)
	if err != nil {
		t.Fatalf("a genuine retry must succeed: %v", err)
	}
	if !second.WasExisting {
		t.Fatal("the retry reported a NEW intent")
	}
	if second.Intent.ID != first.Intent.ID {
		t.Fatalf("retry returned %s, want %s", second.Intent.ID, first.Intent.ID)
	}
	if n := countRows(t,
		`SELECT count(*) FROM payments.payment_intents WHERE idempotency_key=$1`, key); n != 1 {
		t.Fatalf("intent rows = %d, want 1", n)
	}
}

// ─── N4 negative control ─────────────────────────────────────────────
func TestProofNegativeControl_N4_UnfingerprintedConflictLeaksTheOtherDomain(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	key := "idem-" + uuid.NewString()

	first, err := store.CreateIntent(ctx, newIntentReq(key, "commerce", 118000))
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	// The OLD conflict path, reproduced directly: the same ON CONFLICT that
	// CreateIntent runs, with none of the comparisons that follow it.
	var gotID uuid.UUID
	var gotOwner string
	var gotAmount int64
	err = testPool.QueryRow(ctx,
		`INSERT INTO payments.payment_intents
		    (payer_id, payee_id, reference_type, reference_id, amount, amount_minor, currency,
		     method, status, idempotency_key, owner_domain, metadata)
		 VALUES ($1,$2,'order',$3,11800,1180000,'INR','upi','pending',$4,'food','{}')
		 ON CONFLICT (idempotency_key)
		 DO UPDATE SET updated_at = payments.payment_intents.updated_at
		 RETURNING id, COALESCE(owner_domain,''), COALESCE(amount_minor,0)`,
		uuid.New(), uuid.New(), uuid.New(), key).Scan(&gotID, &gotOwner, &gotAmount)
	if err != nil {
		t.Fatalf("control insert: %v", err)
	}
	if gotID != first.Intent.ID || gotOwner != "commerce" {
		t.Fatalf("negative control did not reproduce the defect: got intent %s owned by %q, "+
			"expected the conflict to hand back commerce's intent %s", gotID, gotOwner, first.Intent.ID)
	}
	t.Logf("negative control reproduced the original defect: food-service's request returned "+
		"commerce's intent %s (owner %q, %d minor) with no fingerprint comparison",
		gotID, gotOwner, gotAmount)
}

// ─── N3: a zero-row attach is a conflict ─────────────────────────────

func TestProofN3_AttachingADifferentProviderOrderIsAConflict(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	res, err := store.CreateIntent(ctx, newIntentReq("idem-"+uuid.NewString(), "commerce", 118000))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	first := "order_" + uuid.NewString()[:10]
	if err := store.SetProviderOrder(ctx, res.Intent.ID, first); err != nil {
		t.Fatalf("first attach: %v", err)
	}

	// A second, DIFFERENT provider order. This is the ambiguous-retry
	// outcome: two PSP orders exist for one intent. It used to update zero
	// rows and return nil, so the caller was handed an id the database does
	// not hold and the duplicate went undetected.
	err = store.SetProviderOrder(ctx, res.Intent.ID, "order_second")
	if !errors.Is(err, ErrProviderOrderConflict) {
		t.Fatalf("got %v, want ErrProviderOrderConflict", err)
	}

	var stored string
	if err := testPool.QueryRow(ctx,
		`SELECT COALESCE(provider_order_id,'') FROM payments.payment_intents WHERE id=$1`,
		res.Intent.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != first {
		t.Fatalf("provider_order_id = %q, want %q — the original must not be repointed", stored, first)
	}
}

// The converged retry: the SAME reference attaches idempotently. This is the
// case the deterministic key produces, and it must not be reported as a
// conflict.
func TestProofN3_ReattachingTheSameProviderOrderSucceeds(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	res, err := store.CreateIntent(ctx, newIntentReq("idem-"+uuid.NewString(), "commerce", 118000))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	ref := "order_" + uuid.NewString()[:10]
	if err := store.SetProviderOrder(ctx, res.Intent.ID, ref); err != nil {
		t.Fatalf("first attach: %v", err)
	}
	if err := store.SetProviderOrder(ctx, res.Intent.ID, ref); err != nil {
		t.Fatalf("re-attaching the same reference must be idempotent, got %v", err)
	}
}

func TestProofN3_AttachingToAMissingIntentIsAnError(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	if err := store.SetProviderOrder(ctx, uuid.New(), "order_x"); !errors.Is(err, ErrIntentNotFound) {
		t.Fatalf("got %v, want ErrIntentNotFound", err)
	}
}

// ─── N5: refund attribution must be unambiguous ──────────────────────

// The headline case from review §3 N5: the event's order id belongs to
// intent A and its payment id to intent B. The OR-with-newest-wins selector
// credited whichever row was touched last.
func TestProofN5_DisagreeingIdentifiersAreRefused(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	intentA, orderA := seedIntent(t, 118000, "commerce")
	intentB, _ := seedIntent(t, 90000, "commerce")
	paymentB := "pay_" + uuid.NewString()[:12]
	if _, err := testPool.Exec(ctx,
		`UPDATE payments.payment_intents SET provider_payment_id=$2 WHERE id=$1`,
		intentB, paymentB); err != nil {
		t.Fatalf("attach payment id to B: %v", err)
	}

	eventID := "evt_n5_" + uuid.NewString()
	_, _, err := store.ApplyRefundWebhookAtomically(ctx, WebhookEffect{
		Provider: "razorpay", EventID: eventID, EventType: "refund.processed",
		ProviderOrderID:   orderA,   // intent A
		ProviderPaymentID: paymentB, // intent B
		AmountMinor:       10000,
	}, "rfnd_"+uuid.NewString()[:12])
	if !errors.Is(err, ErrAmbiguousRefundTarget) {
		t.Fatalf("got %v, want ErrAmbiguousRefundTarget", err)
	}

	// Neither intent moved, and the inbox row rolled back so the provider
	// keeps retrying rather than being told the event was handled.
	for _, id := range []uuid.UUID{intentA, intentB} {
		if n := countRows(t,
			`SELECT count(*) FROM payments.payment_intents
			  WHERE id=$1 AND COALESCE(refunded_amount_minor,0) > 0`, id); n != 0 {
			t.Fatalf("intent %s was credited by an ambiguous refund", id)
		}
	}
	if n := countRows(t,
		`SELECT count(*) FROM payments.provider_events WHERE event_id=$1`, eventID); n != 0 {
		t.Fatal("the inbox row survived an ambiguous refund; the redelivery would be answered 200")
	}
}

// Duplicate legacy references: one provider order id on two rows. The old
// selector picked the most recently updated; there is no correct choice, so
// it must refuse.
func TestProofN5_DuplicateProviderOrderReferencesAreRefused(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	// A2: gated 999 now installs a unique invariant that makes this duplicate
	// impossible to create. The APPLICATION refusal is still the property
	// under test, because it is what protects every database where that gated
	// step has not run yet — which is all of them until the maintenance
	// window. So the invariant is dropped for the duration and restored after.
	_, _ = testPool.Exec(ctx, `DROP INDEX IF EXISTS payments.uq_payment_intents_provider_reference`)

	_, sharedRef := seedIntent(t, 50000, "commerce")
	// A second intent carrying the SAME provider reference, which the legacy
	// provider_ref column permits.
	dup := uuid.New()
	t.Cleanup(func() {
		// Clear the duplicate before restoring the invariant; the rows are
		// referenced by audit and inbox rows, so they cannot simply be
		// deleted.
		_, _ = testPool.Exec(context.Background(),
			`UPDATE payments.payment_intents SET provider_order_id=NULL, provider_ref=NULL WHERE id=$1`, dup)
		requireUniquenessInvariant(t)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO payments.payment_intents
		    (id, payer_id, payee_id, reference_type, reference_id, amount, amount_minor,
		     currency, method, status, provider, provider_ref, provider_order_id,
		     owner_domain, idempotency_key)
		VALUES ($1,$2,$3,'order',$4,500,50000,'INR','upi','succeeded','razorpay',$5,$5,
		        'commerce',$6)`,
		dup, uuid.New(), uuid.New(), uuid.New(), sharedRef, "seed-"+dup.String()); err != nil {
		t.Fatalf("seeding a duplicate reference: %v", err)
	}

	_, _, err := store.ApplyRefundWebhookAtomically(ctx, WebhookEffect{
		Provider: "razorpay", EventID: "evt_n5d_" + uuid.NewString(),
		EventType: "refund.processed", ProviderOrderID: sharedRef, AmountMinor: 10000,
	}, "rfnd_"+uuid.NewString()[:12])
	if !errors.Is(err, ErrAmbiguousRefundTarget) {
		t.Fatalf("got %v, want ErrAmbiguousRefundTarget for a reference matching two intents", err)
	}
}

// The positive half: agreeing identifiers still settle. Refusing ambiguity
// must not refuse the ordinary case.
func TestProofN5_AgreeingIdentifiersSettle(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	intentID, providerOrder := seedIntent(t, 118000, "commerce")
	paymentID := "pay_" + uuid.NewString()[:12]
	if _, err := testPool.Exec(ctx,
		`UPDATE payments.payment_intents SET provider_payment_id=$2 WHERE id=$1`,
		intentID, paymentID); err != nil {
		t.Fatal(err)
	}

	applied, status, err := store.ApplyRefundWebhookAtomically(ctx, WebhookEffect{
		Provider: "razorpay", EventID: "evt_n5ok_" + uuid.NewString(),
		EventType:       "refund.processed",
		ProviderOrderID: providerOrder, ProviderPaymentID: paymentID,
		AmountMinor: 118000, Currency: "INR",
	}, "rfnd_"+uuid.NewString()[:12])
	if err != nil {
		t.Fatalf("agreeing identifiers must settle: %v", err)
	}
	if !applied || status != "refunded" {
		t.Fatalf("applied=%v status=%q, want true/refunded", applied, status)
	}
}
