//go:build integration

package http

// Momentum Dating Premium through payments-service (migration 011), end to
// end: real handler, real service with the stub gateway, live PostgreSQL
// (payments_it_test). dating-service is registered the way the deploy values
// declare it: ops intent.create, intent.read, refund.create; reference type
// dating_premium; application dating.
//
// The registry row `dating` is NOT upserted here: it must come from 011.
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/http/ -run Dating -v -count=1

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/paymentevents"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func datingItBody(app, key string) []byte {
	b, _ := json.Marshal(map[string]any{
		"payer_id": uuid.New(), "payee_id": uuid.New(), "reference_type": servicetoken.RefDatingPremium, "reference_id": uuid.New(),
		"amount_minor": 49900, "currency": "INR", "method": "upi", "idempotency_key": key, "application_id": app,
	})
	return b
}

// datingItEvent reads the newest outbox envelope of a type for a partition key.
func datingItEvent(t *testing.T, pool *pgxpool.Pool, eventType, partitionKey string) *events.EventEnvelope {
	t.Helper()
	raw := appItScalar(t, pool, `SELECT payload::text FROM payments.outbox_events
	                              WHERE event_type = $1 AND partition_key = $2 ORDER BY id DESC LIMIT 1`, eventType, partitionKey)
	var env events.EventEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("decode %s envelope: %v", eventType, err)
	}
	return &env
}

type datingRecorder struct {
	paymentevents.NopHandler
	refunded     []paymentevents.Refunded
	refundFailed []paymentevents.RefundFailed
}

func (r *datingRecorder) OnRefunded(_ context.Context, _ *events.EventEnvelope, ev paymentevents.Refunded) error {
	r.refunded = append(r.refunded, ev)
	return nil
}
func (r *datingRecorder) OnRefundFailed(_ context.Context, _ *events.EventEnvelope, ev paymentevents.RefundFailed) error {
	r.refundFailed = append(r.refundFailed, ev)
	return nil
}

// datingItDispatch decodes env as a dating consumer would, and proves a feast
// consumer ignores it.
func datingItDispatch(t *testing.T, env *events.EventEnvelope) *datingRecorder {
	t.Helper()
	rec := &datingRecorder{}
	if err := paymentevents.Dispatch(context.Background(), env, rec, paymentevents.ForApplication("dating")); err != nil {
		t.Fatalf("dispatch %s: %v", env.EventType, err)
	}
	other := &datingRecorder{}
	if err := paymentevents.Dispatch(context.Background(), env, other, paymentevents.ForApplication("feast")); err != nil {
		t.Fatal(err)
	}
	if len(other.refunded)+len(other.refundFailed) != 0 {
		t.Fatalf("%s reached a feast consumer: the event does not carry dating", env.EventType)
	}
	return rec
}

func TestDatingPremiumPaymentsEndToEnd(t *testing.T) {
	pool := itPool(t)
	ctx := context.Background()
	store := postgres.New(pool)
	svc := service.New(store, &gateway.StubGateway{})

	if got := appItScalar(t, pool, `SELECT count(*)::text FROM payments.applications WHERE key = 'dating' AND status = 'active'`); got != "1" {
		t.Fatalf("active dating applications = %s, want 1 (migration 011 not applied?)", got)
	}

	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	dating := itRegister(t, v, "dating-service", "d1", moneyOps, []string{servicetoken.RefDatingPremium})
	food := itRegister(t, v, "food-service", "f1", moneyOps, []string{servicetoken.RefFoodOrder})
	r := appItRouter(t, svc, v, false, map[string][]string{
		"dating-service": {"dating"},
		"food-service":   {"feast"},
	})
	rows := func(key string) string {
		return appItScalar(t, pool, `SELECT count(*)::text FROM payments.payment_intents WHERE idempotency_key = $1`, key)
	}

	var passIntent uuid.UUID
	t.Run("dating-service creates a dating_premium payment for dating: 201, stored and owned by dating-service", func(t *testing.T) {
		key := "dating-it-" + uuid.NewString()
		w := do(r, http.MethodPost, internalIntents, datingItBody("dating", key), dating.header(t, servicetoken.OpIntentCreate))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		var env intentEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Data.ApplicationID != "dating" {
			t.Fatalf("echo = %+v", env.Data)
		}
		passIntent = env.Data.ID
		if got := appItScalar(t, pool, `SELECT application_id || '|' || owner_domain || '|' || reference_type
		                                  FROM payments.payment_intents WHERE id = $1`, passIntent); got != "dating|dating-service|dating_premium" {
			t.Fatalf("stored = %q", got)
		}
	})

	t.Run("the transactions route lists it under dating, for dating-service only", func(t *testing.T) {
		if passIntent == uuid.Nil {
			t.Skip("no intent from the first subtest")
		}
		items := appItPages(t, r, appsPath+"/dating/transactions?type=payment&limit=200", func() map[string]string {
			return dating.header(t, servicetoken.OpIntentRead)
		})
		found := false
		for _, it := range items {
			if it.ApplicationID != "dating" {
				t.Fatalf("item %s belongs to %q", it.ID, it.ApplicationID)
			}
			found = found || it.ID == passIntent
		}
		if !found {
			t.Fatal("the dating payment is missing from /applications/dating/transactions")
		}
		w := do(r, http.MethodGet, appsPath+"/dating/transactions", nil, food.header(t, servicetoken.OpIntentRead))
		if w.Code != http.StatusForbidden || errorCode(t, w.Body.Bytes()) != CodeApplicationNotAllowed {
			t.Fatalf("food on dating: status = %d body=%s, want 403 %s", w.Code, w.Body.String(), CodeApplicationNotAllowed)
		}
	})

	t.Run("dating-service naming feast is 422 APPLICATION_NOT_ALLOWED and writes nothing", func(t *testing.T) {
		key := "dating-it-" + uuid.NewString()
		w := do(r, http.MethodPost, internalIntents, datingItBody("feast", key), dating.header(t, servicetoken.OpIntentCreate))
		if w.Code != http.StatusUnprocessableEntity || errorCode(t, w.Body.Bytes()) != CodeApplicationNotAllowed {
			t.Fatalf("status = %d body=%s, want 422 %s", w.Code, w.Body.String(), CodeApplicationNotAllowed)
		}
		if n := rows(key); n != "0" {
			t.Fatalf("intent rows = %s, want 0", n)
		}
	})

	t.Run("food-service with dating_premium is refused by its REFTYPES and writes nothing", func(t *testing.T) {
		// A food token that CLAIMS dating_premium: only the caller policy
		// (SERVICE_CALLER_FOOD_SERVICE_REFTYPES=food_order) stands in the way.
		claims := itCaller{issuer: food.issuer, signer: food.signer,
			refs: []string{servicetoken.RefFoodOrder, servicetoken.RefDatingPremium}}
		for _, app := range []string{"dating", "feast"} {
			key := "dating-it-" + uuid.NewString()
			w := do(r, http.MethodPost, internalIntents, datingItBody(app, key), claims.header(t, servicetoken.OpIntentCreate))
			if w.Code != http.StatusForbidden {
				t.Fatalf("application %s: status = %d body=%s, want 403", app, w.Code, w.Body.String())
			}
			if n := rows(key); n != "0" {
				t.Fatalf("application %s: intent rows = %s, want 0", app, n)
			}
		}
	})

	t.Run("a legacy internal-key dating_premium intent is owned by dating-service and belongs to dating", func(t *testing.T) {
		key := "dating-it-legacy-" + uuid.NewString()
		body, _ := json.Marshal(map[string]any{
			"payer_id": uuid.New(), "payee_id": uuid.New(), "reference_type": servicetoken.RefDatingPremium, "reference_id": uuid.New(),
			"amount_minor": 49900, "method": "upi", "idempotency_key": key, "application_id": "dating",
		})
		w := do(r, http.MethodPost, internalIntents, body, withKey(uuid.Nil))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		if got := appItScalar(t, pool, `SELECT application_id || '|' || owner_domain FROM payments.payment_intents WHERE idempotency_key = $1`, key); got != "dating|dating-service" {
			t.Fatalf("stored = %q, want dating|dating-service", got)
		}
	})

	t.Run("a refund keeps application dating on the command, payment.refund_failed and payment.refunded", func(t *testing.T) {
		if passIntent == uuid.Nil {
			t.Skip("no intent from the first subtest")
		}
		appItParkRefunds(t, pool, passIntent)
		if _, err := pool.Exec(ctx, `UPDATE payments.payment_intents SET status = 'succeeded' WHERE id = $1`, passIntent); err != nil {
			t.Fatal(err)
		}
		path := internalIntents + "/" + passIntent.String() + "/refund"
		refund := func(app string) []byte {
			b, _ := json.Marshal(map[string]any{"amount_minor": 1000, "reason": "dating-it", "idempotency_key": "dating-it-refund-" + uuid.NewString(), "application_id": app})
			return b
		}

		w := do(r, http.MethodPost, path, refund("feast"), dating.header(t, servicetoken.OpRefundCreate))
		if w.Code != http.StatusUnprocessableEntity || errorCode(t, w.Body.Bytes()) != CodeApplicationMismatch {
			t.Fatalf("feast refund: status = %d body=%s, want 422 %s", w.Code, w.Body.String(), CodeApplicationMismatch)
		}
		w = do(r, http.MethodPost, path, refund("dating"), food.header(t, servicetoken.OpRefundCreate))
		if w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
			t.Fatalf("food refunding a dating intent: status = %d body=%s, want 403/404", w.Code, w.Body.String())
		}

		w = do(r, http.MethodPost, path, refund("dating"), dating.header(t, servicetoken.OpRefundCreate))
		if w.Code != http.StatusAccepted {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		var env struct {
			Data struct {
				CommandID     uuid.UUID `json:"command_id"`
				ApplicationID string    `json:"application_id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		if env.Data.ApplicationID != "dating" {
			t.Fatalf("echo = %+v", env.Data)
		}
		if got := appItScalar(t, pool, `SELECT application_id FROM payments.refund_commands WHERE id = $1`, env.Data.CommandID); got != "dating" {
			t.Fatalf("command application_id = %q", got)
		}

		if parked, err := store.ParkRefundCommand(ctx, env.Data.CommandID, "provider_rejected", "integration: dating refund events"); err != nil || !parked {
			t.Fatalf("parked=%v err=%v", parked, err)
		}
		rec := datingItDispatch(t, datingItEvent(t, pool, events.EventPaymentRefundFailed, passIntent.String()))
		if len(rec.refundFailed) != 1 || rec.refundFailed[0].ApplicationID != "dating" || rec.refundFailed[0].CommandID != env.Data.CommandID.String() {
			t.Fatalf("payment.refund_failed decoded = %+v", rec.refundFailed)
		}

		if applied, _, err := store.ApplyProviderRefund(ctx, "razorpay", "rfnd_dating_it_"+uuid.NewString()[:8], passIntent, 1000, "INR"); err != nil || !applied {
			t.Fatalf("applied=%v err=%v", applied, err)
		}
		rec = datingItDispatch(t, datingItEvent(t, pool, events.EventPaymentRefunded, passIntent.String()))
		if len(rec.refunded) != 1 || rec.refunded[0].ApplicationID != "dating" || rec.refunded[0].ID != passIntent.String() {
			t.Fatalf("payment.refunded decoded = %+v", rec.refunded)
		}
	})
}
