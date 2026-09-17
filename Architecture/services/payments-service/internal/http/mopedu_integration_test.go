//go:build integration

package http

// Mopedu ride fares through payments-service (migration 012), end to end:
// real handler, real service with the stub gateway, live PostgreSQL
// (payments_it_test). rider-service is registered the way the deploy values
// declare it: ops intent.create, intent.read, refund.create; reference type
// mopedu_ride; application mopedu.
//
// The registry row `mopedu` is NOT upserted here: it must come from 012.
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/http/ -run Mopedu -v -count=1

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

func mopeduItBody(app, key string) []byte {
	b, _ := json.Marshal(map[string]any{
		"payer_id": uuid.New(), "payee_id": uuid.New(), "reference_type": servicetoken.RefMopeduRide, "reference_id": uuid.New(),
		"amount_minor": 12900, "currency": "INR", "method": "upi", "idempotency_key": key, "application_id": app,
	})
	return b
}

func TestMopeduRidePaymentsEndToEnd(t *testing.T) {
	pool := itPool(t)
	ctx := context.Background()
	store := postgres.New(pool)
	svc := service.New(store, &gateway.StubGateway{})

	if got := appItScalar(t, pool, `SELECT count(*)::text FROM payments.applications WHERE key = 'mopedu' AND status = 'active'`); got != "1" {
		t.Fatalf("active mopedu applications = %s, want 1 (migration 012 not applied?)", got)
	}

	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	rider := itRegister(t, v, "rider-service", "r1", moneyOps, []string{servicetoken.RefMopeduRide})
	food := itRegister(t, v, "food-service", "f1", moneyOps, []string{servicetoken.RefFoodOrder})
	r := appItRouter(t, svc, v, false, map[string][]string{
		"rider-service": {"mopedu"},
		"food-service":  {"feast"},
	})
	rows := func(key string) string {
		return appItScalar(t, pool, `SELECT count(*)::text FROM payments.payment_intents WHERE idempotency_key = $1`, key)
	}

	var rideIntent uuid.UUID
	t.Run("rider-service creates a mopedu_ride payment for mopedu: 201, stored and owned by rider-service", func(t *testing.T) {
		key := "mopedu-it-" + uuid.NewString()
		w := do(r, http.MethodPost, internalIntents, mopeduItBody("mopedu", key), rider.header(t, servicetoken.OpIntentCreate))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		var env intentEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Data.ApplicationID != "mopedu" {
			t.Fatalf("echo = %+v", env.Data)
		}
		rideIntent = env.Data.ID
		if got := appItScalar(t, pool, `SELECT application_id || '|' || owner_domain || '|' || reference_type
		                                  FROM payments.payment_intents WHERE id = $1`, rideIntent); got != "mopedu|rider-service|mopedu_ride" {
			t.Fatalf("stored = %q", got)
		}
	})

	t.Run("the transactions route lists it under mopedu, for rider-service only", func(t *testing.T) {
		if rideIntent == uuid.Nil {
			t.Skip("no intent from the first subtest")
		}
		items := appItPages(t, r, appsPath+"/mopedu/transactions?type=payment&limit=200", func() map[string]string {
			return rider.header(t, servicetoken.OpIntentRead)
		})
		found := false
		for _, it := range items {
			if it.ApplicationID != "mopedu" {
				t.Fatalf("item %s belongs to %q", it.ID, it.ApplicationID)
			}
			found = found || it.ID == rideIntent
		}
		if !found {
			t.Fatal("the ride payment is missing from /applications/mopedu/transactions")
		}
		w := do(r, http.MethodGet, appsPath+"/mopedu/transactions", nil, food.header(t, servicetoken.OpIntentRead))
		if w.Code != http.StatusForbidden || errorCode(t, w.Body.Bytes()) != CodeApplicationNotAllowed {
			t.Fatalf("food on mopedu: status = %d body=%s, want 403 %s", w.Code, w.Body.String(), CodeApplicationNotAllowed)
		}
	})

	t.Run("rider-service naming feast is 422 APPLICATION_NOT_ALLOWED and writes nothing", func(t *testing.T) {
		key := "mopedu-it-" + uuid.NewString()
		w := do(r, http.MethodPost, internalIntents, mopeduItBody("feast", key), rider.header(t, servicetoken.OpIntentCreate))
		if w.Code != http.StatusUnprocessableEntity || errorCode(t, w.Body.Bytes()) != CodeApplicationNotAllowed {
			t.Fatalf("status = %d body=%s, want 422 %s", w.Code, w.Body.String(), CodeApplicationNotAllowed)
		}
		if n := rows(key); n != "0" {
			t.Fatalf("intent rows = %s, want 0", n)
		}
	})

	t.Run("food-service with mopedu_ride is refused by its REFTYPES and writes nothing", func(t *testing.T) {
		// A food token that CLAIMS mopedu_ride: only the caller policy
		// (SERVICE_CALLER_FOOD_SERVICE_REFTYPES=food_order) stands in the way.
		claims := itCaller{issuer: food.issuer, signer: food.signer,
			refs: []string{servicetoken.RefFoodOrder, servicetoken.RefMopeduRide}}
		for _, app := range []string{"mopedu", "feast"} {
			key := "mopedu-it-" + uuid.NewString()
			w := do(r, http.MethodPost, internalIntents, mopeduItBody(app, key), claims.header(t, servicetoken.OpIntentCreate))
			if w.Code != http.StatusForbidden {
				t.Fatalf("application %s: status = %d body=%s, want 403", app, w.Code, w.Body.String())
			}
			if n := rows(key); n != "0" {
				t.Fatalf("application %s: intent rows = %s, want 0", app, n)
			}
		}
	})

	t.Run("a legacy internal-key mopedu_ride intent is owned by rider-service and belongs to mopedu", func(t *testing.T) {
		key := "mopedu-it-legacy-" + uuid.NewString()
		body, _ := json.Marshal(map[string]any{
			"payer_id": uuid.New(), "payee_id": uuid.New(), "reference_type": servicetoken.RefMopeduRide, "reference_id": uuid.New(),
			"amount_minor": 12900, "method": "upi", "idempotency_key": key, "application_id": "mopedu",
		})
		w := do(r, http.MethodPost, internalIntents, body, withKey(uuid.Nil))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		if got := appItScalar(t, pool, `SELECT application_id || '|' || owner_domain FROM payments.payment_intents WHERE idempotency_key = $1`, key); got != "mopedu|rider-service" {
			t.Fatalf("stored = %q, want mopedu|rider-service", got)
		}
	})

	t.Run("a refund keeps application mopedu on the command and only rider-service may issue it", func(t *testing.T) {
		if rideIntent == uuid.Nil {
			t.Skip("no intent from the first subtest")
		}
		appItParkRefunds(t, pool, rideIntent)
		if _, err := pool.Exec(ctx, `UPDATE payments.payment_intents SET status = 'succeeded' WHERE id = $1`, rideIntent); err != nil {
			t.Fatal(err)
		}
		path := internalIntents + "/" + rideIntent.String() + "/refund"
		refund := func(app string) []byte {
			b, _ := json.Marshal(map[string]any{"amount_minor": 1000, "reason": "mopedu-it", "idempotency_key": "mopedu-it-refund-" + uuid.NewString(), "application_id": app})
			return b
		}

		w := do(r, http.MethodPost, path, refund("feast"), rider.header(t, servicetoken.OpRefundCreate))
		if w.Code != http.StatusUnprocessableEntity || errorCode(t, w.Body.Bytes()) != CodeApplicationMismatch {
			t.Fatalf("feast refund: status = %d body=%s, want 422 %s", w.Code, w.Body.String(), CodeApplicationMismatch)
		}
		w = do(r, http.MethodPost, path, refund("mopedu"), food.header(t, servicetoken.OpRefundCreate))
		if w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
			t.Fatalf("food refunding a mopedu intent: status = %d body=%s, want 403/404", w.Code, w.Body.String())
		}

		w = do(r, http.MethodPost, path, refund("mopedu"), rider.header(t, servicetoken.OpRefundCreate))
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
		if env.Data.ApplicationID != "mopedu" {
			t.Fatalf("echo = %+v", env.Data)
		}
		if got := appItScalar(t, pool, `SELECT application_id FROM payments.refund_commands WHERE id = $1`, env.Data.CommandID); got != "mopedu" {
			t.Fatalf("command application_id = %q", got)
		}
	})
}
