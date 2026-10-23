//go:build integration

package http

// Creator memberships through payments-service (migration 016), end to end:
// real handler, real service with the stub gateway, live PostgreSQL
// (payments_it_test only). monetization-service is registered the way the
// deploy values declare it: ops intent.create, intent.read, refund.create;
// reference type creator_membership; application memberships.
//
// The registry row `memberships` is NOT upserted here: it must come from 016.
//
//	PAYMENTS_TEST_DSN=.../payments_it_test go test -tags=integration ./internal/http/ -run Memberships -v -count=1

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

func membershipItBody(app, ref, method, key string) []byte {
	b, _ := json.Marshal(map[string]any{
		"payer_id": uuid.New(), "payee_id": uuid.New(), "reference_type": ref, "reference_id": uuid.New(),
		"amount_minor": 9900, "currency": "INR", "method": method, "idempotency_key": key, "application_id": app,
	})
	return b
}

func TestMembershipsPaymentsEndToEnd(t *testing.T) {
	pool := itPool(t)
	ctx := context.Background()
	if db := appItScalar(t, pool, `SELECT current_database()`); !strings.HasSuffix(db, "_test") {
		t.Fatalf("refusing to run against database %q: PAYMENTS_TEST_DSN must name a *_test database", db)
	}
	store := postgres.New(pool)
	svc := service.New(store, &gateway.StubGateway{})

	if got := appItScalar(t, pool, `SELECT display_name || '|' || status || '|' || merchant_display_name || '|' ||
	                                       array_to_string(ARRAY(SELECT unnest(enabled_methods) ORDER BY 1), ',')
	                                  FROM payments.applications WHERE key = 'memberships'`); got != "Memberships|active|Momentum Merchant|card,upi" {
		t.Fatalf("memberships application = %q, want Memberships|active|Momentum Merchant|card,upi (migration 016 not applied?)", got)
	}

	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	monetization := itRegister(t, v, "monetization-service", "m1", moneyOps, []string{servicetoken.RefCreatorMembership})
	food := itRegister(t, v, "food-service", "f1", moneyOps, []string{servicetoken.RefFoodOrder})
	doorstep := itRegister(t, v, "doorstep-service", "ds1", moneyOps, []string{servicetoken.RefDoorstepBooking})
	r := appItRouter(t, svc, v, false, map[string][]string{
		"monetization-service": {"memberships"},
		"food-service":         {"feast"},
		"doorstep-service":     {"doorstep"},
	})
	rows := func(key string) string {
		return appItScalar(t, pool, `SELECT count(*)::text FROM payments.payment_intents WHERE idempotency_key = $1`, key)
	}

	var intent uuid.UUID
	t.Run("monetization-service creates a creator_membership payment for memberships: 201, stored and owned by monetization-service", func(t *testing.T) {
		key := "membership-it-" + uuid.NewString()
		w := do(r, http.MethodPost, internalIntents, membershipItBody("memberships", servicetoken.RefCreatorMembership, "upi", key), monetization.header(t, servicetoken.OpIntentCreate))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		var env intentEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Data.ApplicationID != "memberships" {
			t.Fatalf("echo = %+v", env.Data)
		}
		intent = env.Data.ID
		if got := appItScalar(t, pool, `SELECT application_id || '|' || owner_domain || '|' || reference_type
		                                  FROM payments.payment_intents WHERE id = $1`, intent); got != "memberships|monetization-service|creator_membership" {
			t.Fatalf("stored = %q", got)
		}
	})

	t.Run("cash is refused and writes nothing", func(t *testing.T) {
		key := "membership-it-" + uuid.NewString()
		w := do(r, http.MethodPost, internalIntents, membershipItBody("memberships", servicetoken.RefCreatorMembership, "cash", key), monetization.header(t, servicetoken.OpIntentCreate))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d body=%s, want 400", w.Code, w.Body.String())
		}
		if n := rows(key); n != "0" {
			t.Fatalf("intent rows = %s, want 0", n)
		}
	})

	t.Run("monetization-service naming another product is 422 APPLICATION_NOT_ALLOWED and writes nothing", func(t *testing.T) {
		for _, app := range []string{"feast", "mstore", "mopedu", "dating", "doorstep"} {
			key := "membership-it-" + uuid.NewString()
			w := do(r, http.MethodPost, internalIntents, membershipItBody(app, servicetoken.RefCreatorMembership, "upi", key), monetization.header(t, servicetoken.OpIntentCreate))
			if w.Code != http.StatusUnprocessableEntity || errorCode(t, w.Body.Bytes()) != CodeApplicationNotAllowed {
				t.Fatalf("%s: status = %d body=%s, want 422 %s", app, w.Code, w.Body.String(), CodeApplicationNotAllowed)
			}
			if n := rows(key); n != "0" {
				t.Fatalf("%s: intent rows = %s, want 0", app, n)
			}
		}
	})

	t.Run("other callers are refused membership payments and write nothing", func(t *testing.T) {
		cases := map[string]struct {
			caller itCaller
			app    string
			ref    string
			status int
		}{
			// Their own reference type, naming memberships: the application allowlist.
			"food naming memberships":     {food, "memberships", servicetoken.RefFoodOrder, http.StatusUnprocessableEntity},
			"doorstep naming memberships": {doorstep, "memberships", servicetoken.RefDoorstepBooking, http.StatusUnprocessableEntity},
			// A token CLAIMING creator_membership: the REFTYPES policy.
			"food claiming creator_membership": {itCaller{issuer: food.issuer, signer: food.signer,
				refs: []string{servicetoken.RefFoodOrder, servicetoken.RefCreatorMembership}}, "feast", servicetoken.RefCreatorMembership, http.StatusForbidden},
			"doorstep claiming creator_membership": {itCaller{issuer: doorstep.issuer, signer: doorstep.signer,
				refs: []string{servicetoken.RefDoorstepBooking, servicetoken.RefCreatorMembership}}, "doorstep", servicetoken.RefCreatorMembership, http.StatusForbidden},
		}
		for name, tc := range cases {
			key := "membership-it-" + uuid.NewString()
			w := do(r, http.MethodPost, internalIntents, membershipItBody(tc.app, tc.ref, "upi", key), tc.caller.header(t, servicetoken.OpIntentCreate))
			if w.Code != tc.status {
				t.Fatalf("%s: status = %d body=%s, want %d", name, w.Code, w.Body.String(), tc.status)
			}
			if n := rows(key); n != "0" {
				t.Fatalf("%s: intent rows = %s, want 0", name, n)
			}
		}
	})

	t.Run("a legacy internal-key membership intent is owned by monetization-service and belongs to memberships", func(t *testing.T) {
		key := "membership-it-legacy-" + uuid.NewString()
		w := do(r, http.MethodPost, internalIntents, membershipItBody("memberships", servicetoken.RefCreatorMembership, "upi", key), withKey(uuid.Nil))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		if got := appItScalar(t, pool, `SELECT application_id || '|' || owner_domain FROM payments.payment_intents WHERE idempotency_key = $1`, key); got != "memberships|monetization-service" {
			t.Fatalf("stored = %q, want memberships|monetization-service", got)
		}
	})

	t.Run("a membership refund keeps application memberships and only monetization-service may issue it", func(t *testing.T) {
		if intent == uuid.Nil {
			t.Skip("no intent from the first subtest")
		}
		appItParkRefunds(t, pool, intent)
		if _, err := pool.Exec(ctx, `UPDATE payments.payment_intents SET status = 'succeeded' WHERE id = $1`, intent); err != nil {
			t.Fatal(err)
		}
		path := internalIntents + "/" + intent.String() + "/refund"
		refund := func(app string) []byte {
			b, _ := json.Marshal(map[string]any{"amount_minor": 1000, "reason": "membership-it", "idempotency_key": "membership-it-refund-" + uuid.NewString(), "application_id": app})
			return b
		}

		w := do(r, http.MethodPost, path, refund("feast"), monetization.header(t, servicetoken.OpRefundCreate))
		if w.Code != http.StatusUnprocessableEntity || errorCode(t, w.Body.Bytes()) != CodeApplicationMismatch {
			t.Fatalf("feast refund: status = %d body=%s, want 422 %s", w.Code, w.Body.String(), CodeApplicationMismatch)
		}
		for name, c := range map[string]itCaller{"food": food, "doorstep": doorstep} {
			w = do(r, http.MethodPost, path, refund("memberships"), c.header(t, servicetoken.OpRefundCreate))
			if w.Code != http.StatusForbidden && w.Code != http.StatusNotFound {
				t.Fatalf("%s refunding a membership intent: status = %d body=%s, want 403/404", name, w.Code, w.Body.String())
			}
		}

		w = do(r, http.MethodPost, path, refund("memberships"), monetization.header(t, servicetoken.OpRefundCreate))
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
		if env.Data.ApplicationID != "memberships" {
			t.Fatalf("echo = %+v", env.Data)
		}
		if got := appItScalar(t, pool, `SELECT application_id FROM payments.refund_commands WHERE id = $1`, env.Data.CommandID); got != "memberships" {
			t.Fatalf("command application_id = %q", got)
		}
	})
}
