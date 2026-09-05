//go:build integration

package postgres

// A2 / R4-LB-2 — one provider reference belongs to exactly one intent, under
// concurrency, against a live PostgreSQL.
//
// The existing ambiguity proofs (proof_n_integration_test.go) cover references
// that are ALREADY duplicated when the code looks. Review 4's finding is the
// one they cannot reach: under READ COMMITTED, `ApplyWebhookAtomically` counts
// matches in one statement and locks in another, and `SetProviderOrder` has no
// cross-intent serialisation — so a duplicate can appear BETWEEN those two
// statements and the locking lookup then selects an arbitrary row.
//
// These tests barrier real goroutines against real transactions. They require
// the gated invariant, so each one installs it first and says so if it is
// absent: an application-only guard cannot pass these, which is the point.
//
//	COMMERCE/PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/store/postgres/... -v

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// requireUniquenessInvariant installs gated 999 if it is not already present.
//
// It is deliberately NOT part of the boot schema the suite applies: the whole
// argument of A2 is that this is a contract operation. Installing it here, in
// the tests that depend on it, keeps that distinction visible.
func requireUniquenessInvariant(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	var present bool
	if err := testPool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM pg_indexes
		                WHERE schemaname='payments' AND tablename='payment_intents'
		                  AND indexname='uq_payment_intents_provider_reference')`).Scan(&present); err != nil {
		t.Fatalf("checking for the uniqueness invariant: %v", err)
	}
	if present {
		return
	}
	if _, err := testPool.Exec(ctx, `
		CREATE UNIQUE INDEX uq_payment_intents_provider_reference
		    ON payments.payment_intents (
		        provider,
		        (COALESCE(NULLIF(provider_order_id,''), NULLIF(provider_ref,'')))
		    )
		    WHERE COALESCE(NULLIF(provider_order_id,''), NULLIF(provider_ref,'')) IS NOT NULL`,
	); err != nil {
		t.Fatalf("installing the A2 invariant (this is gated/999's index): %v", err)
	}
}

// seedUnattached creates a pending intent with NO provider reference — the
// state SetProviderOrder acts on.
func seedUnattached(t *testing.T, amountMinor int64) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO payments.payment_intents
		    (id, payer_id, payee_id, reference_type, reference_id, amount, amount_minor,
		     currency, method, status, provider, owner_domain, idempotency_key)
		VALUES ($1,$2,$3,'order',$4,$5,$6,'INR','upi','pending','razorpay','commerce',$7)`,
		id, uuid.New(), uuid.New(), uuid.New(),
		float64(amountMinor)/100.0, amountMinor, "a2-"+id.String())
	if err != nil {
		t.Fatalf("seed unattached intent: %v", err)
	}
	return id
}

func refOf(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var ref string
	if err := testPool.QueryRow(context.Background(),
		`SELECT COALESCE(NULLIF(provider_order_id,''), COALESCE(provider_ref,''))
		   FROM payments.payment_intents WHERE id=$1`, id).Scan(&ref); err != nil {
		t.Fatalf("read ref: %v", err)
	}
	return ref
}

// ─── Race 1: two intents attaching the same reference ────────────────

// THE R4-LB-2 race. Two requests attach one provider reference to two
// different intents at the same moment.
//
// Without the database invariant both UPDATEs succeed: each targets a
// different row, each sees that row's reference is blank, and nothing compares
// them. The result is one PSP order owning two local orders — and the next
// genuine capture settles whichever one PostgreSQL happens to return.
func TestA2ConcurrentAttachOfOneReferenceYieldsExactlyOne(t *testing.T) {
	requireUniquenessInvariant(t)
	store := New(testPool)
	ctx := context.Background()

	const rounds = 25
	for round := 0; round < rounds; round++ {
		ref := fmt.Sprintf("order_race_%s", uuid.NewString()[:12])
		a := seedUnattached(t, 118000)
		b := seedUnattached(t, 118000)

		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, id := range []uuid.UUID{a, b} {
			wg.Add(1)
			go func(i int, id uuid.UUID) {
				defer wg.Done()
				<-start // barrier: both attempts leave together
				errs[i] = store.SetProviderOrder(ctx, id, ref)
			}(i, id)
		}
		close(start)
		wg.Wait()

		winners, conflicts := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				winners++
			case errors.Is(err, ErrProviderOrderConflict):
				conflicts++
			default:
				t.Fatalf("round %d: unexpected error %v", round, err)
			}
		}
		if winners != 1 || conflicts != 1 {
			t.Fatalf("round %d: winners=%d conflicts=%d, want 1/1 — one provider order "+
				"now owns two local intents, and the next capture settles an arbitrary one",
				round, winners, conflicts)
		}

		// And the database agrees: exactly one row holds it.
		var holders int
		if err := testPool.QueryRow(ctx, `
			SELECT count(*) FROM payments.payment_intents
			 WHERE provider='razorpay'
			   AND COALESCE(NULLIF(provider_order_id,''), NULLIF(provider_ref,'')) = $1`,
			ref).Scan(&holders); err != nil {
			t.Fatal(err)
		}
		if holders != 1 {
			t.Fatalf("round %d: %d intents hold reference %s", round, holders, ref)
		}
	}
}

// The loser's refusal must be STABLE and typed, not a raw driver error: the
// caller branches on it to leave the intent pending for the reconciler.
func TestA2LoserReceivesAStableConflict(t *testing.T) {
	requireUniquenessInvariant(t)
	store := New(testPool)
	ctx := context.Background()

	ref := "order_stable_" + uuid.NewString()[:12]
	a, b := seedUnattached(t, 50000), seedUnattached(t, 50000)

	if err := store.SetProviderOrder(ctx, a, ref); err != nil {
		t.Fatalf("first attach: %v", err)
	}
	err := store.SetProviderOrder(ctx, b, ref)
	if !errors.Is(err, ErrProviderOrderConflict) {
		t.Fatalf("second attach returned %v, want ErrProviderOrderConflict", err)
	}
	if got := refOf(t, b); got != "" {
		t.Fatalf("the loser attached %q; it must stay blank so the reconciler owns it", got)
	}
}

// A genuine RETRY — the same intent, the same reference — must still converge,
// not be mistaken for a conflict. Otherwise the deterministic idempotency key
// stops working the moment the invariant is installed.
func TestA2RetryOfTheSameAttachStillConverges(t *testing.T) {
	requireUniquenessInvariant(t)
	store := New(testPool)
	ctx := context.Background()

	ref := "order_retry_" + uuid.NewString()[:12]
	id := seedUnattached(t, 77000)
	for i := 0; i < 3; i++ {
		if err := store.SetProviderOrder(ctx, id, ref); err != nil {
			t.Fatalf("attach attempt %d: %v — a retry must converge, not conflict", i+1, err)
		}
	}
	if got := refOf(t, id); got != ref {
		t.Fatalf("reference = %q, want %q", got, ref)
	}
}

// ─── Race 2: an attach racing a webhook ──────────────────────────────

// The precise interleaving review 4 named: a webhook is applying a capture for
// a reference while a second attach of that same reference is in flight.
//
// Repeated many times to cross statement ordering — the phantom only appears
// when the second attach commits between the webhook's count and its locking
// lookup, which is a narrow window.
func TestA2AttachRacingWebhookCannotSettleAnArbitraryIntent(t *testing.T) {
	requireUniquenessInvariant(t)
	store := New(testPool)
	ctx := context.Background()

	const rounds = 40
	for round := 0; round < rounds; round++ {
		ref := fmt.Sprintf("order_wh_race_%s", uuid.NewString()[:12])
		owner := seedUnattached(t, 118000)
		intruder := seedUnattached(t, 118000)

		// The owner legitimately holds the reference.
		if err := store.SetProviderOrder(ctx, owner, ref); err != nil {
			t.Fatalf("round %d: seeding the owner: %v", round, err)
		}

		start := make(chan struct{})
		var wg sync.WaitGroup
		var attachErr, webhookErr error

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			attachErr = store.SetProviderOrder(ctx, intruder, ref)
		}()
		go func() {
			defer wg.Done()
			<-start
			_, webhookErr = store.ApplyWebhookAtomically(ctx, WebhookEffect{
				Provider:          "razorpay",
				EventID:           "evt_a2_" + uuid.NewString(),
				EventType:         "payment.captured",
				ProviderOrderID:   ref,
				ProviderPaymentID: "pay_" + uuid.NewString()[:10],
				NewStatus:         "succeeded",
				AmountMinor:       118000,
				Currency:          "INR",
			})
		}()
		close(start)
		wg.Wait()

		// The intruder must never acquire the reference.
		if !errors.Is(attachErr, ErrProviderOrderConflict) {
			t.Fatalf("round %d: the intruding attach returned %v, want ErrProviderOrderConflict",
				round, attachErr)
		}
		if got := refOf(t, intruder); got != "" {
			t.Fatalf("round %d: the intruder acquired reference %q", round, got)
		}

		// The webhook either settled THE OWNER or refused. What it must never
		// do is settle the intruder.
		var intruderStatus, ownerStatus string
		if err := testPool.QueryRow(ctx,
			`SELECT status FROM payments.payment_intents WHERE id=$1`, intruder).Scan(&intruderStatus); err != nil {
			t.Fatal(err)
		}
		if err := testPool.QueryRow(ctx,
			`SELECT status FROM payments.payment_intents WHERE id=$1`, owner).Scan(&ownerStatus); err != nil {
			t.Fatal(err)
		}
		if intruderStatus != "pending" {
			t.Fatalf("round %d: the capture settled the WRONG intent (intruder is %q). "+
				"webhookErr=%v", round, intruderStatus, webhookErr)
		}
		if webhookErr == nil && ownerStatus != "succeeded" {
			t.Fatalf("round %d: the webhook reported success but the owner is %q",
				round, ownerStatus)
		}
	}
}

// ─── The invariant is the thing being relied on ──────────────────────

// A direct duplicate write must be refused by PostgreSQL itself, with no
// application code involved. This is the assertion that distinguishes "the
// application happens to be careful" from "the database holds the invariant".
func TestA2DatabaseRefusesADuplicateDirectWrite(t *testing.T) {
	requireUniquenessInvariant(t)
	ctx := context.Background()

	ref := "order_direct_" + uuid.NewString()[:12]
	a, b := seedUnattached(t, 10000), seedUnattached(t, 10000)

	if _, err := testPool.Exec(ctx,
		`UPDATE payments.payment_intents SET provider_order_id=$2 WHERE id=$1`, a, ref); err != nil {
		t.Fatalf("first direct write: %v", err)
	}
	if _, err := testPool.Exec(ctx,
		`UPDATE payments.payment_intents SET provider_order_id=$2 WHERE id=$1`, b, ref); err == nil {
		t.Fatal("PostgreSQL accepted a second intent holding the same provider reference; " +
			"the application guard is then the only thing between a genuine capture and " +
			"the wrong customer's order")
	}

	// And the LEGACY column is covered by the same expression index: a writer
	// that populates only provider_ref must not slip past it.
	if _, err := testPool.Exec(ctx,
		`UPDATE payments.payment_intents SET provider_ref=$2 WHERE id=$1`, b, ref); err == nil {
		t.Fatal("a duplicate written through the legacy provider_ref column was accepted; " +
			"the index must cover the EFFECTIVE reference, not one column")
	}
}

// The APPLICATION guard, on a database where gated 999 has not run yet.
//
// That is every production database today, and it is the reason the
// application refusal is kept rather than replaced by the index: between now
// and the maintenance window, the only thing standing between a duplicated
// reference and a capture settling the wrong order is this check.
//
// It drops the invariant to create the duplicate the invariant would prevent,
// then restores it — because the property under test is what the code does
// when the database has NOT been tightened.
func TestA2ApplicationRefusesAnAmbiguousCaptureWithoutTheIndex(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	// Remove the invariant for the duration.
	_, _ = testPool.Exec(ctx, `DROP INDEX IF EXISTS payments.uq_payment_intents_provider_reference`)
	var seeded []uuid.UUID
	t.Cleanup(func() {
		// Clear the duplicate rather than delete the rows: audit and inbox
		// rows reference them, so a DELETE is refused and the invariant then
		// cannot be reinstalled for the rest of the suite.
		for _, id := range seeded {
			_, _ = testPool.Exec(context.Background(),
				`UPDATE payments.payment_intents SET provider_order_id=NULL, provider_ref=NULL WHERE id=$1`, id)
		}
		requireUniquenessInvariant(t)
	})

	ref := "order_ambig_" + uuid.NewString()[:12]
	a, b := seedUnattached(t, 118000), seedUnattached(t, 118000)
	seeded = []uuid.UUID{a, b}
	for _, id := range []uuid.UUID{a, b} {
		if _, err := testPool.Exec(ctx,
			`UPDATE payments.payment_intents SET provider_order_id=$2 WHERE id=$1`, id, ref); err != nil {
			t.Fatalf("seeding the duplicate: %v", err)
		}
	}

	_, err := store.ApplyWebhookAtomically(ctx, WebhookEffect{
		Provider: "razorpay", EventID: "evt_ambig_" + uuid.NewString(),
		EventType: "payment.captured", ProviderOrderID: ref,
		ProviderPaymentID: "pay_ambig", NewStatus: "succeeded",
		AmountMinor: 118000, Currency: "INR",
	})
	if err == nil {
		t.Fatal("a capture settled while its provider reference matched two intents; " +
			"one of two customers' orders was marked paid arbitrarily")
	}
	if !errors.Is(err, ErrAmbiguousRefundTarget) {
		t.Fatalf("got %v, want the ambiguity refusal", err)
	}

	for _, id := range []uuid.UUID{a, b} {
		var status string
		if err := testPool.QueryRow(ctx,
			`SELECT status FROM payments.payment_intents WHERE id=$1`, id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "pending" {
			t.Fatalf("intent %s moved to %q on an ambiguous capture", id, status)
		}
	}
}
