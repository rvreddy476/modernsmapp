//go:build integration

package http

// Doorstep home services through payments-service (migration 015), end to
// end: real handler, real service with the stub gateway, live PostgreSQL
// (payments_it_test only). doorstep-service is registered the way the deploy
// values declare it: ops intent.create, intent.read, refund.create; reference
// types doorstep_booking and doorstep_extras; application doorstep.
//
// The registry row `doorstep` is NOT upserted here: it must come from 015.
//
//	PAYMENTS_TEST_DSN=.../payments_it_test go test -tags=integration ./internal/http/ -run Doorstep -v -count=1

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

func doorstepItBody(app, ref, method, key string) []byte {
	b, _ := json.Marshal(map[string]any{
		"payer_id": uuid.New(), "payee_id": uuid.New(), "reference_type": ref, "reference_id": uuid.New(),
		"amount_minor": 49900, "currency": "INR", "method": method, "idempotency_key": key, "application_id": app,
	})
	return b
}

func TestDoorstepPaymentsEndToEnd(t *testing.T) {
	pool := itPool(t)
	ctx := context.Background()
	if db := appItScalar(t, pool, `SELECT current_database()`); !strings.HasSuffix(db, "_test") {
		t.Fatalf("refusing to run against database %q: PAYMENTS_TEST_DSN must name a *_test database", db)
	}
	store := postgres.New(pool)
	svc := service.New(store, &gateway.StubGateway{})

	if got := appItScalar(t, pool, `SELECT display_name || '|' || status || '|' ||
	                                       array_to_string(ARRAY(SELECT unnest(enabled_methods) ORDER BY 1), ',')
	                                  FROM payments.applications WHERE key = 'doorstep'`); got != "Doorstep|active|card,upi" {
		t.Fatalf("doorstep application = %q, want Doorstep|active|card,upi (migration 015 not applied?)", got)
	}

	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	doorstepRefs := []string{servicetoken.RefDoorstepBooking, servicetoken.RefDoorstepExtras}
	doorstep := itRegister(t, v, "doorstep-service", "ds1", moneyOps, doorstepRefs)
	food := itRegister(t, v, "food-service", "f1", moneyOps, []string{servicetoken.RefFoodOrder})
	rider := itRegister(t, v, "rider-service", "r1", moneyOps, []string{servicetoken.RefMopeduRide})
	r := appItRouter(t, svc, v, false, map[string][]string{
		"doorstep-service": {"doorstep"},
		"food-service":     {"feast"},
		"rider-service":    {"mopedu"},
	})
	rows := func(key string) string {
		return appItScalar(t, pool, `SELECT count(*)::text FROM payments.payment_intents WHERE idempotency_key = $1`, key)
	}

	intents := map[string]uuid.UUID{}
	for _, ref := range doorstepRefs {
		t.Run("doorstep-service creates a "+ref+" payment for doorstep: 201, stored and owned by doorstep-service", func(t *testing.T) {
			key := "doorstep-it-" + uuid.NewString()
			w := do(r, http.MethodPost, internalIntents, doorstepItBody("doorstep", ref, "upi", key), doorstep.header(t, servicetoken.OpIntentCreate))
			if w.Code != http.StatusCreated {
				t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
			}
			var env intentEnvelope
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatal(err)
			}
			if env.Data.ApplicationID != "doorstep" {
				t.Fatalf("echo = %+v", env.Data)
			}
			intents[ref] = env.Data.ID
			if got := appItScalar(t, pool, `SELECT application_id || '|' || owner_domain || '|' || reference_type
			                                  FROM payments.payment_intents WHERE id = $1`, env.Data.ID); got != "doorstep|doorstep-service|"+ref {
				t.Fatalf("stored = %q", got)
			}
		})
	}

	t.Run("cash is refused and writes nothing (Doorstep takes no cash)", func(t *testing.T) {
		key := "doorstep-it-" + uuid.NewString()
		w := do(r, http.MethodPost, internalIntents, doorstepItBody("doorstep", servicetoken.RefDoorstepBooking, "cash", key), doorstep.header(t, servicetoken.OpIntentCreate))
		if w.Code != http.StatusBadRequest || errorCode(t, w.Body.Bytes()) != "PAYMENT_METHOD_NOT_SUPPORTED" {
			t.Fatalf("status = %d body=%s, want 400 PAYMENT_METHOD_NOT_SUPPORTED", w.Code, w.Body.String())
		}
		if n := rows(key); n != "0" {
			t.Fatalf("intent rows = %s, want 0", n)
		}
	})

	t.Run("the transactions route lists them under doorstep, for doorstep-service only", func(t *testing.T) {
		if len(intents) != 2 {
			t.Skip("no intents from the first subtests")
		}
		items := appItPages(t, r, appsPath+"/doorstep/transactions?type=payment&limit=200", func() map[string]string {
			return doorstep.header(t, servicetoken.OpIntentRead)
		})
		found := 0
		for _, it := range items {
			if it.ApplicationID != "doorstep" {
				t.Fatalf("item %s belongs to %q", it.ID, it.ApplicationID)
			}
			if it.ID == intents[servicetoken.RefDoorstepBooking] || it.ID == intents[servicetoken.RefDoorstepExtras] {
				found++
			}
		}
		if found != 2 {
			t.Fatalf("found %d of the 2 doorstep payments in /applications/doorstep/transactions", found)
		}
		for name, c := range map[string]itCaller{"food": food, "rider": rider} {
			w := do(r, http.MethodGet, appsPath+"/doorstep/transactions", nil, c.header(t, servicetoken.OpIntentRead))
			if w.Code != http.StatusForbidden || errorCode(t, w.Body.Bytes()) != CodeApplicationNotAllowed {
				t.Fatalf("%s on doorstep: status = %d body=%s, want 403 %s", name, w.Code, w.Body.String(), CodeApplicationNotAllowed)
			}
		}
	})

	t.Run("doorstep-service naming another product is 422 APPLICATION_NOT_ALLOWED and writes nothing", func(t *testing.T) {
		for _, app := range []string{"feast", "mstore", "mopedu", "dating"} {
			key := "doorstep-it-" + uuid.NewString()
			w := do(r, http.MethodPost, internalIntents, doorstepItBody(app, servicetoken.RefDoorstepBooking, "upi", key), doorstep.header(t, servicetoken.OpIntentCreate))
			if w.Code != http.StatusUnprocessableEntity || errorCode(t, w.Body.Bytes()) != CodeApplicationNotAllowed {
				t.Fatalf("%s: status = %d body=%s, want 422 %s", app, w.Code, w.Body.String(), CodeApplicationNotAllowed)
			}
			if n := rows(key); n != "0" {
				t.Fatalf("%s: intent rows = %s, want 0", app, n)
			}
		}
	})

	t.Run("other callers are refused doorstep payments and write nothing", func(t *testing.T) {
		cases := map[string]struct {
			caller itCaller
			ref    string
			status int
		}{
			// Their own reference type, naming doorstep: the application allowlist.
			"food naming doorstep":  {food, servicetoken.RefFoodOrder, http.StatusUnprocessableEntity},
			"rider naming doorstep": {rider, servicetoken.RefMopeduRide, http.StatusUnprocessableEntity},
			// A token CLAIMING doorstep_booking: the REFTYPES policy.
			"food claiming doorstep_booking": {itCaller{issuer: food.issuer, signer: food.signer,
				refs: []string{servicetoken.RefFoodOrder, servicetoken.RefDoorstepBooking}}, servicetoken.RefDoorstepBooking, http.StatusForbidden},
			"rider claiming doorstep_extras": {itCaller{issuer: rider.issuer, signer: rider.signer,
				refs: []string{servicetoken.RefMopeduRide, servicetoken.RefDoorstepExtras}}, servicetoken.RefDoorstepExtras, http.StatusForbidden},
		}
		for name, tc := range cases {
			key := "doorstep-it-" + uuid.NewString()
			w := do(r, http.MethodPost, internalIntents, doorstepItBody("doorstep", tc.ref, "upi", key), tc.caller.header(t, servicetoken.OpIntentCreate))
			if w.Code != tc.status {
				t.Fatalf("%s: status = %d body=%s, want %d", name, w.Code, w.Body.String(), tc.status)
			}
			if n := rows(key); n != "0" {
				t.Fatalf("%s: intent rows = %s, want 0", name, n)
			}
		}
	})

	t.Run("doorstep-service claiming a reference type outside its REFTYPES is refused", func(t *testing.T) {
		claims := itCaller{issuer: doorstep.issuer, signer: doorstep.signer,
			refs: []string{servicetoken.RefDoorstepBooking, servicetoken.RefFoodOrder}}
		key := "doorstep-it-" + uuid.NewString()
		w := do(r, http.MethodPost, internalIntents, doorstepItBody("doorstep", servicetoken.RefFoodOrder, "upi", key), claims.header(t, servicetoken.OpIntentCreate))
		if w.Code != http.StatusForbidden || rows(key) != "0" {
			t.Fatalf("status = %d rows=%s, want 403 and 0", w.Code, rows(key))
		}
	})

	t.Run("a legacy internal-key doorstep intent is owned by doorstep-service and belongs to doorstep", func(t *testing.T) {
		for _, ref := range doorstepRefs {
			key := "doorstep-it-legacy-" + uuid.NewString()
			w := do(r, http.MethodPost, internalIntents, doorstepItBody("doorstep", ref, "upi", key), withKey(uuid.Nil))
			if w.Code != http.StatusCreated {
				t.Fatalf("%s: status = %d body=%s", ref, w.Code, w.Body.String())
			}
			if got := appItScalar(t, pool, `SELECT application_id || '|' || owner_domain FROM payments.payment_intents WHERE idempotency_key = $1`, key); got != "doorstep|doorstep-service" {
				t.Fatalf("%s: stored = %q, want doorstep|doorstep-service", ref, got)
			}
		}
	})

	for _, ref := range doorstepRefs {
		t.Run("a "+ref+" refund keeps application doorstep and only doorstep-service may issue it", func(t *testing.T) {
			id := intents[ref]
			if id == uuid.Nil {
				t.Skip("no intent from the first subtests")
			}
			appItParkRefunds(t, pool, id)
			if _, err := pool.Exec(ctx, `UPDATE payments.payment_intents SET status = 'succeeded' WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
			path := internalIntents + "/" + id.String() + "/refund"
			refund := func(app string) []byte {
				b, _ := json.Marshal(map[string]any{"amount_minor": 1000, "reason": "doorstep-it", "idempotency_key": "doorstep-it-refund-" + uuid.NewString(), "application_id": app})
				return b
			}

			w := do(r, http.MethodPost, path, refund("feast"), doorstep.header(t, servicetoken.OpRefundCreate))
			if w.Code != http.StatusUnprocessableEntity || errorCode(t, w.Body.Bytes()) != CodeApplicationMismatch {
				t.Fatalf("feast refund: status = %d body=%s, want 422 %s", w.Code, w.Body.String(), CodeApplicationMismatch)
			}
			for name, c := range map[string]itCaller{"food": food, "rider": rider} {
				w = do(r, http.MethodPost, path, refund("doorstep"), c.header(t, servicetoken.OpRefundCreate))
				if w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
					t.Fatalf("%s refunding a doorstep intent: status = %d body=%s, want 403/404", name, w.Code, w.Body.String())
				}
			}

			w = do(r, http.MethodPost, path, refund("doorstep"), doorstep.header(t, servicetoken.OpRefundCreate))
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
			if env.Data.ApplicationID != "doorstep" {
				t.Fatalf("echo = %+v", env.Data)
			}
			if got := appItScalar(t, pool, `SELECT application_id FROM payments.refund_commands WHERE id = $1`, env.Data.CommandID); got != "doorstep" {
				t.Fatalf("command application_id = %q", got)
			}
		})
	}
}
