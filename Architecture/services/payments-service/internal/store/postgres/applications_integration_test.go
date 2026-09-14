//go:build integration

package postgres

// Migration 010, against a live PostgreSQL (payments_it_test): the backfill,
// the application copied onto every child row, the idempotency fingerprint, and
// application_id on every payment event, decoded with the consumers' own
// package (shared/paymentevents) so the wire contract is what is proven.
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/store/postgres/ -run Application -v -count=1

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/atpost/shared/events"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
)

type appSeed struct {
	id      uuid.UUID
	ref     uuid.UUID
	order   string
	payment string
}

// appSeedIntent writes an intent row directly. app "" leaves application_id
// NULL, the state an old replica or a pre-010 row is in.
func appSeedIntent(t *testing.T, status, owner, refType, app string, amountMinor int64) appSeed {
	t.Helper()
	s := appSeed{id: uuid.New(), ref: uuid.New()}
	s.order, s.payment = "order_"+s.id.String()[:12], "pay_"+s.id.String()[:12]
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO payments.payment_intents
		    (id, payer_id, payee_id, reference_type, reference_id, amount, amount_minor,
		     currency, method, status, provider, provider_ref, provider_order_id, provider_payment_id,
		     owner_domain, idempotency_key, application_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'INR','upi',$8,'razorpay',$9,$9,$10,NULLIF($11,''),$12,NULLIF($13,''))`,
		s.id, uuid.New(), uuid.New(), refType, s.ref, float64(amountMinor)/100.0, amountMinor, status,
		s.order, s.payment, owner, "app-it-"+s.id.String(), app); err != nil {
		t.Fatalf("seed intent: %v", err)
	}
	return s
}

func appColumn(t *testing.T, query string, args ...any) string {
	t.Helper()
	var v string
	if err := testPool.QueryRow(context.Background(), query, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return v
}

func appCount(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := testPool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// ─── Backfill ────────────────────────────────────────────────────────

func TestApplicationBackfillMapsByOwnerThenReferenceTypeAndCountsTheRest(t *testing.T) {
	ctx := context.Background()
	unmappable := `SELECT count(*) FROM payments.payment_intents
	                WHERE application_id IS NULL
	                  AND payments.legacy_application_for(owner_domain, reference_type) IS NULL`
	baseline := appCount(t, unmappable)

	byOwnerCommerce := appSeedIntent(t, "succeeded", "commerce-service", "food_order", "", 1000) // owner wins
	byOwnerFood := appSeedIntent(t, "succeeded", "food-service", "order", "", 1000)
	byRefFood := appSeedIntent(t, "succeeded", "legacy:food_order", "food_order", "", 1000)
	byRefOrder := appSeedIntent(t, "succeeded", "", "order", "", 1000)
	orphan := appSeedIntent(t, "succeeded", "unknown", "demo_ref", "", 1000)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM payments.payment_intents WHERE id = $1`, orphan.id)
	})

	// Child rows with no application, one per table.
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO payments.refund_commands (intent_id, amount_minor, provider_idempotency_key, requested_by, status)
		  VALUES ($1, 100, $2, 'app-it', 'succeeded')`, []any{byOwnerFood.id, "app-it-bf-" + uuid.NewString()}},
		{`INSERT INTO payments.refund_required (intent_id, provider, provider_payment_id, event_id, reason)
		  VALUES ($1, 'razorpay', $2, $3, 'app-it')`, []any{byOwnerFood.id, "pay_bf_" + uuid.NewString(), "evt_" + uuid.NewString()}},
		{`INSERT INTO payments.provider_refunds_applied (provider, provider_refund_id, intent_id, amount_minor)
		  VALUES ('razorpay', $1, $2, 100)`, []any{"rfnd_bf_" + uuid.NewString(), byOwnerCommerce.id}},
		{`INSERT INTO payments.refunds_applied (refund_provider_ref, intent_id, amount_minor)
		  VALUES ($1, $2, 100)`, []any{"rfnd_legacy_bf_" + uuid.NewString(), byRefFood.id}},
		{`INSERT INTO payments.payment_holds (payment_intent_id, hold_amount, release_condition)
		  VALUES ($1, 100, 'manual')`, []any{byRefOrder.id}},
	} {
		if _, err := testPool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed child row: %v", err)
		}
	}

	rows, err := testPool.Query(ctx, `SELECT table_name, backfilled, unmapped FROM payments.backfill_application_ids()`)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	report := map[string][2]int64{}
	for rows.Next() {
		var name string
		var filled, left int64
		if err := rows.Scan(&name, &filled, &left); err != nil {
			t.Fatal(err)
		}
		report[name] = [2]int64{filled, left}
	}
	rows.Close()
	t.Logf("backfill report: %v", report)

	for _, tc := range []struct {
		name string
		id   uuid.UUID
		want string
	}{
		{"owner commerce-service (reference food_order)", byOwnerCommerce.id, "mstore"},
		{"owner food-service (reference order)", byOwnerFood.id, "feast"},
		{"unknown owner, reference food_order", byRefFood.id, "feast"},
		{"no owner, reference order", byRefOrder.id, "mstore"},
		{"unknown owner, unknown reference", orphan.id, ""},
	} {
		got := appColumn(t, `SELECT COALESCE(application_id,'') FROM payments.payment_intents WHERE id = $1`, tc.id)
		if got != tc.want {
			t.Errorf("%s: application_id = %q, want %q", tc.name, got, tc.want)
		}
	}
	for _, tc := range []struct{ table, fk, want string }{
		{"refund_commands", "intent_id", "feast"},
		{"refund_required", "intent_id", "feast"},
		{"provider_refunds_applied", "intent_id", "mstore"},
		{"refunds_applied", "intent_id", "feast"},
		{"payment_holds", "payment_intent_id", "mstore"},
	} {
		ids := map[string]uuid.UUID{"refund_commands": byOwnerFood.id, "refund_required": byOwnerFood.id,
			"provider_refunds_applied": byOwnerCommerce.id, "refunds_applied": byRefFood.id, "payment_holds": byRefOrder.id}
		got := appColumn(t, `SELECT COALESCE(string_agg(DISTINCT COALESCE(application_id,'<null>'), ','),'')
		                       FROM payments.`+tc.table+` WHERE `+tc.fk+` = $1`, ids[tc.table])
		if got != tc.want {
			t.Errorf("%s: application_id = %q, want %q (copied from its intent)", tc.table, got, tc.want)
		}
	}

	if got := report["payment_intents"][1]; got != baseline+1 {
		t.Fatalf("payment_intents unmapped = %d, want %d (baseline %d + the one unmappable row)", got, baseline+1, baseline)
	}
	if got := appCount(t, `SELECT unmapped FROM payments.application_backfill_report
	                        WHERE table_name = 'payment_intents' ORDER BY id DESC LIMIT 1`); got != baseline+1 {
		t.Fatalf("recorded unmapped = %d, want %d", got, baseline+1)
	}
	counts, err := New(testPool).UnmappedApplicationCounts(ctx)
	if err != nil || counts["payment_intents"] != baseline+1 {
		t.Fatalf("UnmappedApplicationCounts = %v, %v; want payment_intents %d", counts, err, baseline+1)
	}
}

// ─── Child rows ──────────────────────────────────────────────────────

func TestApplicationIsCopiedFromTheIntentOntoEveryChildRow(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	paid := appSeedIntent(t, "succeeded", "food-service", "food_order", "feast", 45000)
	key := func() string { return "app-it-refund-" + uuid.NewString() }
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`UPDATE payments.refund_commands SET next_attempt_at = NOW() + INTERVAL '30 days'
			  WHERE intent_id = $1 AND status IN ('pending','submitted')`, paid.id)
	})

	t.Run("a refund naming another application is refused and reserves nothing", func(t *testing.T) {
		_, _, err := store.CreateRefundCommand(ctx, paid.id, 1000, "mismatch", key(), "food-service", "food-service", "mstore")
		if !errors.Is(err, ErrApplicationMismatch) {
			t.Fatalf("err = %v, want ErrApplicationMismatch", err)
		}
		if n := appCount(t, `SELECT count(*) FROM payments.refund_commands WHERE intent_id = $1`, paid.id); n != 0 {
			t.Fatalf("refund commands = %d, want 0", n)
		}
		if r := appCount(t, `SELECT refund_reserved_minor FROM payments.payment_intents WHERE id = $1`, paid.id); r != 0 {
			t.Fatalf("reserved = %d, want 0", r)
		}
		if _, _, err := store.CreateRefundCommand(ctx, paid.id, 1000, "blank", key(), "food-service", "food-service", ""); !errors.Is(err, ErrApplicationRequired) {
			t.Fatalf("blank application: err = %v, want ErrApplicationRequired", err)
		}
	})
	t.Run("an intent with no application cannot be refunded", func(t *testing.T) {
		legacy := appSeedIntent(t, "succeeded", "food-service", "food_order", "", 1000)
		t.Cleanup(func() {
			_, _ = testPool.Exec(context.Background(), `DELETE FROM payments.payment_intents WHERE id = $1`, legacy.id)
		})
		if _, _, err := store.CreateRefundCommand(ctx, legacy.id, 1000, "legacy", key(), "food-service", "food-service", "feast"); !errors.Is(err, ErrApplicationMismatch) {
			t.Fatalf("err = %v, want ErrApplicationMismatch", err)
		}
	})
	t.Run("refund_commands", func(t *testing.T) {
		cmd, created, err := store.CreateRefundCommand(ctx, paid.id, 1000, "ok", key(), "food-service", "food-service", "feast")
		if err != nil || !created || cmd.ApplicationID != "feast" {
			t.Fatalf("command = %+v created=%v err=%v", cmd, created, err)
		}
		if got := appColumn(t, `SELECT application_id FROM payments.refund_commands WHERE id = $1`, cmd.ID); got != "feast" {
			t.Fatalf("stored application_id = %q", got)
		}
	})
	t.Run("provider_refunds_applied", func(t *testing.T) {
		rid := "rfnd_app_" + uuid.NewString()[:10]
		if applied, _, err := store.ApplyProviderRefund(ctx, "razorpay", rid, paid.id, 1000, "INR"); err != nil || !applied {
			t.Fatalf("applied=%v err=%v", applied, err)
		}
		if got := appColumn(t, `SELECT application_id FROM payments.provider_refunds_applied WHERE provider_refund_id = $1`, rid); got != "feast" {
			t.Fatalf("application_id = %q", got)
		}
	})
	t.Run("refunds_applied (legacy)", func(t *testing.T) {
		rid := "rfnd_legacy_app_" + uuid.NewString()[:10]
		if fresh, err := store.RecordRefundIfFresh(ctx, rid, paid.id, 500); err != nil || !fresh {
			t.Fatalf("fresh=%v err=%v", fresh, err)
		}
		if got := appColumn(t, `SELECT application_id FROM payments.refunds_applied WHERE refund_provider_ref = $1`, rid); got != "feast" {
			t.Fatalf("application_id = %q", got)
		}
	})
	t.Run("payment_holds", func(t *testing.T) {
		if err := store.CreateHold(ctx, paid.id, 500, "INR", "manual"); err != nil {
			t.Fatal(err)
		}
		if got := appColumn(t, `SELECT application_id FROM payments.payment_holds WHERE payment_intent_id = $1 LIMIT 1`, paid.id); got != "feast" {
			t.Fatalf("application_id = %q", got)
		}
	})
	t.Run("refund_required (a late capture on a failed intent)", func(t *testing.T) {
		failed := appSeedIntent(t, "failed", "food-service", "food_order", "feast", 45000)
		if _, err := store.ApplyWebhookAtomically(ctx, WebhookEffect{
			Provider: "razorpay", EventID: "evt_late_" + uuid.NewString(), EventType: "payment.captured",
			ProviderOrderID: failed.order, ProviderPaymentID: failed.payment, NewStatus: "succeeded",
			AmountMinor: 45000, Currency: "INR",
		}); err != nil {
			t.Fatal(err)
		}
		if got := appColumn(t, `SELECT application_id FROM payments.refund_required WHERE intent_id = $1`, failed.id); got != "feast" {
			t.Fatalf("application_id = %q", got)
		}
	})
}

// ─── CreateIntent ────────────────────────────────────────────────────

func TestCreateIntentRequiresAndFingerprintsTheApplication(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	req := func(key, app string) PaymentIntent {
		r := newIntentReq(key, "food-service", 45000)
		r.ReferenceType, r.ApplicationID, r.Channel = "food_order", app, "feast_rider_android"
		return r
	}

	if _, err := store.CreateIntent(ctx, req("app-it-"+uuid.NewString(), "")); !errors.Is(err, ErrApplicationRequired) {
		t.Fatalf("no application: err = %v, want ErrApplicationRequired", err)
	}
	if _, err := store.CreateIntent(ctx, req("app-it-"+uuid.NewString(), "zz_not_registered")); !errors.Is(err, ErrApplicationNotFound) {
		t.Fatalf("unregistered application: err = %v, want ErrApplicationNotFound", err)
	}
	bad := req("app-it-"+uuid.NewString(), "feast")
	bad.Channel = "Rider App"
	if _, err := store.CreateIntent(ctx, bad); !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("malformed channel: err = %v, want ErrInvalidChannel", err)
	}

	key := "app-it-" + uuid.NewString()
	first, err := store.CreateIntent(ctx, req(key, "feast"))
	if err != nil || first.Intent.ApplicationID != "feast" || first.Intent.Channel != "feast_rider_android" {
		t.Fatalf("create = %+v, %v", first, err)
	}
	other := req(key, "mstore")
	other.PayerID, other.PayeeID, other.ReferenceID = first.Intent.PayerID, first.Intent.PayeeID, first.Intent.ReferenceID
	if _, err := store.CreateIntent(ctx, other); !errors.Is(err, ErrIdempotencyFingerprint) {
		t.Fatalf("same key, another application: err = %v, want ErrIdempotencyFingerprint", err)
	}

	// A row an old replica wrote mid-rollout (no application) is adopted by the
	// retry that matches it in every other dimension.
	legacyKey := "app-it-" + uuid.NewString()
	retry := req(legacyKey, "feast")
	if _, err := testPool.Exec(ctx, `
		INSERT INTO payments.payment_intents
		    (payer_id, payee_id, reference_type, reference_id, amount, amount_minor, currency, method,
		     status, idempotency_key, owner_domain)
		VALUES ($1,$2,'food_order',$3,450,45000,'INR','upi','pending',$4,'food-service')`,
		retry.PayerID, retry.PayeeID, retry.ReferenceID, legacyKey); err != nil {
		t.Fatal(err)
	}
	adopted, err := store.CreateIntent(ctx, retry)
	if err != nil || !adopted.WasExisting || adopted.Intent.ApplicationID != "feast" {
		t.Fatalf("adopt = %+v, %v", adopted, err)
	}
	if got := appColumn(t, `SELECT application_id FROM payments.payment_intents WHERE idempotency_key = $1`, legacyKey); got != "feast" {
		t.Fatalf("stored application_id after adoption = %q", got)
	}
}

// ─── Events ──────────────────────────────────────────────────────────

type appRecorder struct {
	paymentevents.NopHandler
	succeeded    []paymentevents.Succeeded
	failed       []paymentevents.Failed
	refunded     []paymentevents.Refunded
	refundFailed []paymentevents.RefundFailed
}

func (r *appRecorder) OnSucceeded(_ context.Context, _ *events.EventEnvelope, ev paymentevents.Succeeded) error {
	r.succeeded = append(r.succeeded, ev)
	return nil
}
func (r *appRecorder) OnFailed(_ context.Context, _ *events.EventEnvelope, ev paymentevents.Failed) error {
	r.failed = append(r.failed, ev)
	return nil
}
func (r *appRecorder) OnRefunded(_ context.Context, _ *events.EventEnvelope, ev paymentevents.Refunded) error {
	r.refunded = append(r.refunded, ev)
	return nil
}
func (r *appRecorder) OnRefundFailed(_ context.Context, _ *events.EventEnvelope, ev paymentevents.RefundFailed) error {
	r.refundFailed = append(r.refundFailed, ev)
	return nil
}

// appOutboxEvent reads the newest outbox envelope of a type for a partition key.
func appOutboxEvent(t *testing.T, eventType, partitionKey string) *events.EventEnvelope {
	t.Helper()
	raw := appColumn(t, `SELECT payload::text FROM payments.outbox_events
	                      WHERE event_type = $1 AND partition_key = $2 ORDER BY id DESC LIMIT 1`, eventType, partitionKey)
	var env events.EventEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("decode %s envelope: %v", eventType, err)
	}
	return &env
}

// dispatchFeast decodes env through paymentevents, filtered to feast, and
// proves the same event is ignored by a consumer filtering to mstore.
func dispatchFeast(t *testing.T, env *events.EventEnvelope) *appRecorder {
	t.Helper()
	rec := &appRecorder{}
	if err := paymentevents.Dispatch(context.Background(), env, rec, paymentevents.ForApplication("feast")); err != nil {
		t.Fatalf("dispatch %s: %v", env.EventType, err)
	}
	other := &appRecorder{}
	if err := paymentevents.Dispatch(context.Background(), env, other, paymentevents.ForApplication("mstore")); err != nil {
		t.Fatal(err)
	}
	if n := len(other.succeeded) + len(other.failed) + len(other.refunded) + len(other.refundFailed); n != 0 {
		t.Fatalf("%s reached an mstore consumer: the event does not carry feast", env.EventType)
	}
	return rec
}

func TestEveryPaymentEventCarriesTheApplication(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	paid := appSeedIntent(t, "pending", "food-service", "food_order", "feast", 45000)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(),
			`UPDATE payments.refund_commands SET next_attempt_at = NOW() + INTERVAL '30 days'
			  WHERE intent_id = $1 AND status IN ('pending','submitted')`, paid.id)
	})

	t.Run(events.EventPaymentSucceeded, func(t *testing.T) {
		if _, err := store.ApplyWebhookAtomically(ctx, WebhookEffect{
			Provider: "razorpay", EventID: "evt_app_ok_" + uuid.NewString(), EventType: "payment.captured",
			ProviderOrderID: paid.order, ProviderPaymentID: paid.payment, NewStatus: "succeeded",
			AmountMinor: 45000, Currency: "INR",
		}); err != nil {
			t.Fatal(err)
		}
		rec := dispatchFeast(t, appOutboxEvent(t, events.EventPaymentSucceeded, paid.ref.String()))
		if len(rec.succeeded) != 1 || rec.succeeded[0].ApplicationID != "feast" || rec.succeeded[0].ID != paid.id.String() {
			t.Fatalf("decoded = %+v", rec.succeeded)
		}
	})
	t.Run(events.EventPaymentFailed, func(t *testing.T) {
		lost := appSeedIntent(t, "pending", "food-service", "food_order", "feast", 45000)
		if _, err := store.ApplyWebhookAtomically(ctx, WebhookEffect{
			Provider: "razorpay", EventID: "reconcile:failed:" + lost.id.String(), EventType: "reconcile.failed",
			ProviderOrderID: lost.order, NewStatus: "failed",
		}); err != nil {
			t.Fatal(err)
		}
		rec := dispatchFeast(t, appOutboxEvent(t, events.EventPaymentFailed, lost.ref.String()))
		if len(rec.failed) != 1 || rec.failed[0].ApplicationID != "feast" {
			t.Fatalf("decoded = %+v", rec.failed)
		}
	})
	t.Run(events.EventPaymentRefunded+" (provider refund)", func(t *testing.T) {
		if applied, _, err := store.ApplyProviderRefund(ctx, "razorpay", "rfnd_app_ev_"+uuid.NewString()[:8], paid.id, 5000, "INR"); err != nil || !applied {
			t.Fatalf("applied=%v err=%v", applied, err)
		}
		rec := dispatchFeast(t, appOutboxEvent(t, events.EventPaymentRefunded, paid.id.String()))
		if len(rec.refunded) != 1 || rec.refunded[0].ApplicationID != "feast" || rec.refunded[0].Manual {
			t.Fatalf("decoded = %+v", rec.refunded)
		}
	})

	var cmdID uuid.UUID
	t.Run(EventPaymentRefundPending, func(t *testing.T) {
		cmd, _, err := store.CreateRefundCommand(ctx, paid.id, 10000, "app events", "app-it-ev-"+uuid.NewString(),
			"food-service", "food-service", "feast")
		if err != nil {
			t.Fatal(err)
		}
		cmdID = cmd.ID
		env := appOutboxEvent(t, EventPaymentRefundPending, paid.id.String())
		var payload map[string]any
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["application_id"] != "feast" || payload["command_id"] != cmd.ID.String() {
			t.Fatalf("payload = %v", payload)
		}
	})
	t.Run(events.EventPaymentRefundFailed, func(t *testing.T) {
		if parked, err := store.ParkRefundCommand(ctx, cmdID, "provider_rejected", "integration: application events"); err != nil || !parked {
			t.Fatalf("parked=%v err=%v", parked, err)
		}
		rec := dispatchFeast(t, appOutboxEvent(t, events.EventPaymentRefundFailed, paid.id.String()))
		if len(rec.refundFailed) != 1 || rec.refundFailed[0].ApplicationID != "feast" || rec.refundFailed[0].CommandID != cmdID.String() {
			t.Fatalf("decoded = %+v", rec.refundFailed)
		}
	})
	t.Run(events.EventPaymentRefunded+" (manual resolution)", func(t *testing.T) {
		if _, err := store.ResolveRefundCommand(ctx, ResolveRefundInput{
			CommandID: cmdID, Resolution: ResolutionRefundedManually, Note: "integration: application events",
			OperatorID: "app-it", Credential: "internal_key",
		}); err != nil {
			t.Fatal(err)
		}
		rec := dispatchFeast(t, appOutboxEvent(t, events.EventPaymentRefunded, paid.id.String()))
		if len(rec.refunded) != 1 || rec.refunded[0].ApplicationID != "feast" || !rec.refunded[0].Manual {
			t.Fatalf("decoded = %+v", rec.refunded)
		}
		if got := appColumn(t, `SELECT application_id FROM payments.provider_refunds_applied WHERE provider_refund_id = $1`,
			"manual:"+cmdID.String()); got != "feast" {
			t.Fatalf("manual ledger row application_id = %q", got)
		}
	})
}
