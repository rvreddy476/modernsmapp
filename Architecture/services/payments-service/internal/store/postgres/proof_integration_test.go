//go:build integration

package postgres

// Payments-side Commerce P0 proofs, against a REAL PostgreSQL.
//
// These cover the failures the review classified as R-1/R-5/M-4 and proofs
// C4 and C8: the webhook inbox, the mark-before-effect loss window, and
// refund durability. Each asserts that the interleaving it targets actually
// occurred, and the durability proofs inject a failure rather than simulating
// one, because "we call these in the right order" is not evidence.
//
//	go test -tags=integration ./internal/store/postgres/... -run TestProof -v -count=1
//
// with PAYMENTS_TEST_DSN pointing at a scratch database.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("PAYMENTS_TEST_DSN")
	if dsn == "" {
		fmt.Println("PAYMENTS_TEST_DSN not set; skipping integration proofs")
		os.Exit(0)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		fmt.Printf("connect: %v\n", err)
		os.Exit(1)
	}
	testPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

// seedIntent creates a succeeded intent worth amountMinor paise.
func seedIntent(t *testing.T, amountMinor int64, owner string) (uuid.UUID, string) {
	t.Helper()
	id := uuid.New()
	providerOrder := "order_" + id.String()[:12]
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO payments.payment_intents
		    (id, payer_id, payee_id, reference_type, reference_id, amount, amount_minor,
		     currency, method, status, provider, provider_ref, provider_order_id,
		     owner_domain, idempotency_key)
		VALUES ($1,$2,$3,'order',$4,$5,$6,'INR','upi','succeeded','razorpay',$7,$7,$8,$9)`,
		id, uuid.New(), uuid.New(), uuid.New(),
		float64(amountMinor)/100.0, amountMinor, providerOrder, owner, "seed-"+id.String())
	if err != nil {
		t.Fatalf("seed intent: %v", err)
	}
	return id, providerOrder
}

// ─── C4: webhook inbox ───────────────────────────────────────────────

// TestProofC4_DuplicateWebhookAppliesOnce proves the inbox suppresses a
// redelivery, and that suppression comes from a DATABASE row.
func TestProofC4_DuplicateWebhookAppliesOnce(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id := uuid.New()
	providerOrder := "order_" + id.String()[:12]
	if _, err := testPool.Exec(ctx, `
		INSERT INTO payments.payment_intents
		    (id, payer_id, payee_id, reference_type, reference_id, amount, amount_minor,
		     currency, method, status, provider, provider_ref, provider_order_id,
		     owner_domain, idempotency_key)
		VALUES ($1,$2,$3,'order',$4,1180,118000,'INR','upi','pending','razorpay',$5,$5,
		        'commerce-service',$6)`,
		id, uuid.New(), uuid.New(), uuid.New(), providerOrder, "seed-"+id.String()); err != nil {
		t.Fatal(err)
	}

	// B2: a `succeeded` transition now REQUIRES the provider's amount and
	// currency, verified inside the same transaction that writes terminal
	// state. This fixture predates that guard and omitted both, so it was
	// refused with ErrWebhookAmountMismatch — a stale test, not a product
	// failure, but one that made the whole payments suite red.
	//
	// The seeded intent above is 118000 minor / INR, so this matches.
	eff := WebhookEffect{
		Provider:          "razorpay",
		EventID:           "evt_" + uuid.NewString(),
		EventType:         "payment.captured",
		ProviderOrderID:   providerOrder,
		ProviderPaymentID: "pay_" + uuid.NewString()[:10],
		NewStatus:         "succeeded",
		AmountMinor:       118000,
		Currency:          "INR",
	}
	if _, err := store.ApplyWebhookAtomically(ctx, eff); err != nil {
		t.Fatalf("first delivery: %v", err)
	}

	// Ten redeliveries of the same event.
	for i := 0; i < 10; i++ {
		if _, err := store.ApplyWebhookAtomically(ctx, eff); !errors.Is(err, ErrDuplicateEvent) {
			t.Fatalf("redelivery %d: got %v, want ErrDuplicateEvent", i, err)
		}
	}

	var status string
	var inbox, outboxRows int
	_ = testPool.QueryRow(ctx, `SELECT status FROM payments.payment_intents WHERE id=$1`, id).Scan(&status)
	_ = testPool.QueryRow(ctx,
		`SELECT count(*) FROM payments.provider_events WHERE event_id=$1`, eff.EventID).Scan(&inbox)
	_ = testPool.QueryRow(ctx,
		`SELECT count(*) FROM payments.outbox_events WHERE payload::text LIKE '%'||$1||'%'`,
		id.String()).Scan(&outboxRows)

	if status != "succeeded" {
		t.Fatalf("status=%q, want succeeded", status)
	}
	if inbox != 1 {
		t.Fatalf("inbox rows=%d, want 1", inbox)
	}
	if outboxRows != 1 {
		t.Fatalf("outbox rows=%d, want exactly 1 — a redelivery must not re-publish", outboxRows)
	}
}

// TestProofC4_InboxRollsBackWithTheEffect is the R-1 / M-4 proof.
//
// The failure it targets: the inbox row was inserted, THEN the status was
// applied, THEN Kafka was published. A crash in between meant the provider's
// retry hit an existing inbox row, was treated as a duplicate, returned 200
// — and the money effect was never recorded anywhere.
//
// The injected failure here is a webhook naming a provider order that does
// not exist. The inbox insert has already happened inside the transaction
// when the intent lookup fails, so if the two were NOT atomic the row would
// survive. It must not.
func TestProofC4_InboxRollsBackWithTheEffect(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	eventID := "evt_orphan_" + uuid.NewString()
	_, err := store.ApplyWebhookAtomically(ctx, WebhookEffect{
		Provider:        "razorpay",
		EventID:         eventID,
		EventType:       "payment.captured",
		ProviderOrderID: "order_does_not_exist",
		NewStatus:       "succeeded",
	})
	if !errors.Is(err, ErrIntentNotFound) {
		t.Fatalf("got %v, want ErrIntentNotFound", err)
	}

	var inbox int
	_ = testPool.QueryRow(ctx,
		`SELECT count(*) FROM payments.provider_events WHERE event_id=$1`, eventID).Scan(&inbox)
	if inbox != 0 {
		t.Fatalf("the inbox row survived a failed effect (%d rows). A provider retry would now be "+
			"suppressed as a duplicate and the capture would be lost permanently — this is exactly "+
			"the R-1 window", inbox)
	}
}

// TestProofC4_BlankEventIDRefused covers review R-5.
//
// Razorpay puts the event id in the `x-razorpay-event-id` HEADER; the old
// code read a body field that does not exist, so the key was almost always
// "". The first event took that key and every later payment was then
// acknowledged as a duplicate without its money being recorded.
func TestProofC4_BlankEventIDRefused(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	if _, err := store.ApplyWebhookAtomically(ctx, WebhookEffect{
		Provider:  "razorpay",
		EventID:   "",
		EventType: "payment.captured",
	}); !errors.Is(err, ErrBlankEventID) {
		t.Fatalf("got %v, want ErrBlankEventID", err)
	}

	// And the database refuses it too, so no code path can insert one.
	_, err := testPool.Exec(ctx,
		`INSERT INTO payments.provider_events (provider, event_id, event_type)
		 VALUES ('razorpay','','payment.captured')`)
	if err == nil {
		t.Fatal("the database accepted a blank provider event id")
	}
}

// TestProofC4_TerminalStateNotReverted proves a late capture cannot
// resurrect a refunded payment.
func TestProofC4_TerminalStateNotReverted(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id, providerOrder := seedIntent(t, 118000, "commerce-service")
	if _, err := testPool.Exec(ctx,
		`UPDATE payments.payment_intents SET status='refunded' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}

	if _, err := store.ApplyWebhookAtomically(ctx, WebhookEffect{
		Provider:        "razorpay",
		EventID:         "evt_late_" + uuid.NewString(),
		EventType:       "payment.captured",
		ProviderOrderID: providerOrder,
		NewStatus:       "succeeded",
	}); err != nil {
		t.Fatalf("late capture should be absorbed, not error: %v", err)
	}

	var status string
	_ = testPool.QueryRow(ctx, `SELECT status FROM payments.payment_intents WHERE id=$1`, id).Scan(&status)
	if status != "refunded" {
		t.Fatalf("status=%q — a late capture must not revert a refunded payment", status)
	}
}

// ─── C8: refund durability ───────────────────────────────────────────

// TestProofC8_RefundIsDurableBeforeProviderContact is the A6 / LB-8 proof.
//
// The old order of operations was: mark the intent refunded, call Razorpay,
// swallow the error. So a provider outage produced a ledger claiming the
// customer had been refunded with no money moving and nothing remembering
// the debt.
func TestProofC8_RefundIsDurableBeforeProviderContact(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id, _ := seedIntent(t, 118000, "commerce-service")
	key := "refund:" + id.String()

	cmd, created, err := store.CreateRefundCommand(ctx, id, 118000, "cancel", key, "commerce-service", "commerce-service")
	if err != nil {
		t.Fatalf("create refund command: %v", err)
	}
	if !created {
		t.Fatal("expected a newly created command")
	}
	if cmd.Status != "pending" {
		t.Fatalf("status=%q, want pending — a refund must be DURABLE before the provider is contacted", cmd.Status)
	}

	// The ledger must NOT claim the money has moved yet.
	var refunded, reserved int64
	var status string
	_ = testPool.QueryRow(ctx,
		`SELECT COALESCE(refunded_amount_minor,0), COALESCE(refund_reserved_minor,0), status
		   FROM payments.payment_intents WHERE id=$1`, id).Scan(&refunded, &reserved, &status)
	if refunded != 0 {
		t.Fatalf("refunded_amount_minor=%d before the provider was called — this is the ledger lying", refunded)
	}
	if reserved != 118000 {
		t.Fatalf("refund_reserved_minor=%d, want 118000 — an in-flight refund must hold capacity", reserved)
	}

	// A retry with the same deterministic key returns the SAME command.
	again, created2, err := store.CreateRefundCommand(ctx, id, 118000, "cancel", key, "commerce-service", "commerce-service")
	if err != nil {
		t.Fatal(err)
	}
	if created2 || again.ID != cmd.ID {
		t.Fatalf("a retry created a second refund command (%v, created=%v)", again.ID, created2)
	}

	// Settlement, by a verified provider signal, is what moves the ledger.
	applied, newStatus, err := store.ApplyProviderRefund(ctx, "razorpay", "rfnd_"+uuid.NewString()[:10], id, 118000, "INR")
	if err != nil || !applied {
		t.Fatalf("settle: applied=%v err=%v", applied, err)
	}
	if newStatus != "refunded" {
		t.Fatalf("status=%q, want refunded", newStatus)
	}
	_ = testPool.QueryRow(ctx,
		`SELECT COALESCE(refunded_amount_minor,0), COALESCE(refund_reserved_minor,0)
		   FROM payments.payment_intents WHERE id=$1`, id).Scan(&refunded, &reserved)
	if refunded != 118000 || reserved != 0 {
		t.Fatalf("after settlement refunded=%d reserved=%d, want 118000 and 0", refunded, reserved)
	}
}

// TestProofC8_DuplicateProviderRefundCreditsOnce proves settlement is keyed
// on the provider's refund id, so one refund re-emitted under a new event id
// cannot double-credit.
func TestProofC8_DuplicateProviderRefundCreditsOnce(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id, _ := seedIntent(t, 200000, "commerce-service")
	refundID := "rfnd_" + uuid.NewString()[:10]

	applied, _, err := store.ApplyProviderRefund(ctx, "razorpay", refundID, id, 100000, "INR")
	if err != nil || !applied {
		t.Fatalf("first: applied=%v err=%v", applied, err)
	}
	for i := 0; i < 5; i++ {
		applied, _, err = store.ApplyProviderRefund(ctx, "razorpay", refundID, id, 100000, "INR")
		if err != nil {
			t.Fatalf("redelivery %d: %v", i, err)
		}
		if applied {
			t.Fatalf("redelivery %d credited the ledger a second time", i)
		}
	}
	var refunded int64
	_ = testPool.QueryRow(ctx,
		`SELECT refunded_amount_minor FROM payments.payment_intents WHERE id=$1`, id).Scan(&refunded)
	if refunded != 100000 {
		t.Fatalf("refunded=%d, want 100000 — a re-emitted refund must credit exactly once", refunded)
	}
}

// TestProofC8_ConcurrentRefundsCannotExceedTheIntent proves the reservation
// makes the cap hold under concurrency.
//
// Without `refund_reserved_minor`, two simultaneous refunds each see the
// full remaining balance — neither has committed when the other reads — and
// together they over-refund the payment.
func TestProofC8_ConcurrentRefundsCannotExceedTheIntent(t *testing.T) {
	const N = 10
	ctx := context.Background()
	store := New(testPool)

	id, _ := seedIntent(t, 100000, "commerce-service") // ₹1,000

	var (
		start  = make(chan struct{})
		wg     sync.WaitGroup
		mu     sync.Mutex
		ok     int
		capped int
	)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			// Each asks for ₹200. Only five can fit inside ₹1,000.
			_, _, err := store.CreateRefundCommand(ctx, id, 20000, "test",
				fmt.Sprintf("refund:%s:%d", id, i), "commerce-service", "commerce-service")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrRefundExceedsRemaining):
				capped++
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if ok != 5 {
		t.Fatalf("accepted %d refunds of ₹200 against a ₹1,000 intent, want exactly 5", ok)
	}
	if capped != 5 {
		t.Fatalf("capped %d, want 5", capped)
	}

	var reserved, amount int64
	_ = testPool.QueryRow(ctx,
		`SELECT COALESCE(refund_reserved_minor,0), amount_minor
		   FROM payments.payment_intents WHERE id=$1`, id).Scan(&reserved, &amount)
	if reserved != amount {
		t.Fatalf("reserved=%d amount=%d — the reservation must exactly consume the balance", reserved, amount)
	}
}

// ─── D4: cross-domain authority ──────────────────────────────────────

// TestProofCrossDomainRefundRefused proves a bare intent UUID confers no
// authority.
//
// payments is shared with food-service. Without an owner check, knowing an
// intent id would be enough for one domain to refund another's payment.
func TestProofCrossDomainRefundRefused(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)

	id, _ := seedIntent(t, 50000, "commerce-service")

	_, _, err := store.CreateRefundCommand(ctx, id, 50000, "steal",
		"refund:"+id.String()+":food", "food-service", "food-service")
	if !errors.Is(err, ErrNotOwnerDomain) {
		t.Fatalf("got %v, want ErrNotOwnerDomain — food-service must not refund a commerce payment", err)
	}

	var refunded, reserved int64
	_ = testPool.QueryRow(ctx,
		`SELECT COALESCE(refunded_amount_minor,0), COALESCE(refund_reserved_minor,0)
		   FROM payments.payment_intents WHERE id=$1`, id).Scan(&refunded, &reserved)
	if refunded != 0 || reserved != 0 {
		t.Fatalf("a refused cross-domain refund still touched the ledger (refunded=%d reserved=%d)",
			refunded, reserved)
	}
}
