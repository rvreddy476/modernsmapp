package http

// Doorstep home services (migration 015), against the in-memory fake.
//
// The mappings used for callers that present no verified identity: a
// doorstep_booking or doorstep_extras intent from the user-facing family or a
// legacy internal-key caller must be owned by doorstep-service and attributed
// to the application doorstep, or doorstep-service's own token could neither
// read nor refund it.
//
// And the caller scoping the deploy values declare: doorstep-service holds
// intent.create, intent.read and refund.create on doorstep_booking and
// doorstep_extras, for the application doorstep only; no other caller may
// open a Doorstep payment, and doorstep-service may open no other product's.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

func TestDoorstepOwnerDomainAndApplication(t *testing.T) {
	for _, ref := range []string{servicetoken.RefDoorstepBooking, servicetoken.RefDoorstepExtras} {
		if got := ownerDomainForReference(ref); got != "doorstep-service" {
			t.Fatalf("owner domain for %s = %q, want doorstep-service", ref, got)
		}
		if got := legacyApplications[ref]; got != "doorstep" {
			t.Fatalf("legacy application for %s = %q, want doorstep", ref, got)
		}
	}
	// The wire values the Doorstep contract pins.
	if servicetoken.RefDoorstepBooking != "doorstep_booking" || servicetoken.RefDoorstepExtras != "doorstep_extras" {
		t.Fatalf("reference types = %q, %q", servicetoken.RefDoorstepBooking, servicetoken.RefDoorstepExtras)
	}
	// The existing mappings are unchanged.
	for ref, want := range map[string][2]string{
		servicetoken.RefOrder:              {"commerce-service", "mstore"},
		servicetoken.RefFoodOrder:          {"food-service", "feast"},
		servicetoken.RefDatingPremium:      {"dating-service", "dating"},
		servicetoken.RefMopeduRide:         {"rider-service", "mopedu"},
		servicetoken.RefMopeduSubscription: {"rider-service", "mopedu"},
	} {
		if got := ownerDomainForReference(ref); got != want[0] {
			t.Errorf("owner domain for %s = %q, want %q", ref, got, want[0])
		}
		if got := legacyApplications[ref]; got != want[1] {
			t.Errorf("legacy application for %s = %q, want %q", ref, got, want[1])
		}
	}
}

func doorstepIntentBody(app, ref string) []byte {
	m := map[string]any{
		"payer_id": uuid.New(), "payee_id": uuid.New(), "reference_type": ref, "reference_id": uuid.New(),
		"amount_minor": 49900, "method": "upi", "idempotency_key": "doorstep:" + uuid.NewString(),
	}
	if app != "" {
		m["application_id"] = app
	}
	b, _ := json.Marshal(m)
	return b
}

func TestDoorstepCallerScoping(t *testing.T) {
	fake := newFake()
	for _, key := range []string{"doorstep", "mopedu"} {
		fake.apps[key] = &postgres.Application{Key: key, Status: "active", MerchantDisplayName: "Momentum Merchant",
			EnabledMethods: []string{"card", "upi"}, Settings: []byte(`{}`)}
	}
	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	doorstepRefs := []string{servicetoken.RefDoorstepBooking, servicetoken.RefDoorstepExtras}
	doorstep := newTokenCaller(t, v, "doorstep-service", "ds1", moneyOps, doorstepRefs)
	food := newTokenCaller(t, v, "food-service", "f1", moneyOps, []string{servicetoken.RefFoodOrder})
	rider := newTokenCaller(t, v, "rider-service", "r1", moneyOps, []string{servicetoken.RefMopeduRide})
	r := newRouter(t, fake, routerOpts{internalKey: testInternalKey, verifier: v, callerApps: map[string][]string{
		"doorstep-service": {"doorstep"},
		"food-service":     {"feast"},
		"rider-service":    {"mopedu"},
	}})

	for _, ref := range doorstepRefs {
		t.Run("doorstep-service opens a "+ref+" payment for doorstep, owned by doorstep-service", func(t *testing.T) {
			w := do(r, http.MethodPost, internalIntents, doorstepIntentBody("doorstep", ref), doorstep.header(t, servicetoken.OpIntentCreate))
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
			got := fake.initiations[len(fake.initiations)-1]
			if got.ApplicationID != "doorstep" || got.OwnerDomain != "doorstep-service" || got.ReferenceType != ref {
				t.Fatalf("service input = app %q owner %q ref %q", got.ApplicationID, got.OwnerDomain, got.ReferenceType)
			}
		})
	}

	t.Run("doorstep-service naming no application uses doorstep, its only one", func(t *testing.T) {
		w := do(r, http.MethodPost, internalIntents, doorstepIntentBody("", servicetoken.RefDoorstepBooking), doorstep.header(t, servicetoken.OpIntentCreate))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		if got := fake.initiations[len(fake.initiations)-1].ApplicationID; got != "doorstep" {
			t.Fatalf("application = %q, want doorstep", got)
		}
	})

	// A token that CLAIMS more reference types than its registration: only
	// the caller policy (SERVICE_CALLER_<NAME>_REFTYPES) stands in the way.
	claims := func(c tokenCaller, refs ...string) tokenCaller {
		return tokenCaller{issuer: c.issuer, signer: c.signer, refs: refs}
	}
	refusals := []struct {
		name    string
		headers map[string]string
		body    []byte
		status  int
		code    string
	}{
		// The application allowlist (SERVICE_CALLER_DOORSTEP_SERVICE_APPLICATIONS=doorstep).
		{"doorstep-service may not open a feast payment", doorstep.header(t, servicetoken.OpIntentCreate),
			doorstepIntentBody("feast", servicetoken.RefDoorstepBooking), 422, CodeApplicationNotAllowed},
		{"doorstep-service may not open an mstore payment", doorstep.header(t, servicetoken.OpIntentCreate),
			doorstepIntentBody("mstore", servicetoken.RefDoorstepExtras), 422, CodeApplicationNotAllowed},
		{"doorstep-service may not open a mopedu payment", doorstep.header(t, servicetoken.OpIntentCreate),
			doorstepIntentBody("mopedu", servicetoken.RefDoorstepBooking), 422, CodeApplicationNotAllowed},
		{"food-service may not open a doorstep payment", food.header(t, servicetoken.OpIntentCreate),
			doorstepIntentBody("doorstep", servicetoken.RefFoodOrder), 422, CodeApplicationNotAllowed},
		{"rider-service may not open a doorstep payment", rider.header(t, servicetoken.OpIntentCreate),
			doorstepIntentBody("doorstep", servicetoken.RefMopeduRide), 422, CodeApplicationNotAllowed},
		// The reference-type allowlist (REFTYPES).
		{"doorstep-service claiming food_order is refused", claims(doorstep, servicetoken.RefDoorstepBooking, servicetoken.RefFoodOrder).header(t, servicetoken.OpIntentCreate),
			doorstepIntentBody("doorstep", servicetoken.RefFoodOrder), 403, "FORBIDDEN"},
		{"doorstep-service claiming mopedu_ride is refused", claims(doorstep, servicetoken.RefDoorstepExtras, servicetoken.RefMopeduRide).header(t, servicetoken.OpIntentCreate),
			doorstepIntentBody("doorstep", servicetoken.RefMopeduRide), 403, "FORBIDDEN"},
		{"food-service claiming doorstep_booking is refused for doorstep", claims(food, servicetoken.RefFoodOrder, servicetoken.RefDoorstepBooking).header(t, servicetoken.OpIntentCreate),
			doorstepIntentBody("doorstep", servicetoken.RefDoorstepBooking), 403, "FORBIDDEN"},
		{"food-service claiming doorstep_extras is refused for feast", claims(food, servicetoken.RefFoodOrder, servicetoken.RefDoorstepExtras).header(t, servicetoken.OpIntentCreate),
			doorstepIntentBody("feast", servicetoken.RefDoorstepExtras), 403, "FORBIDDEN"},
		{"rider-service claiming doorstep_booking is refused", claims(rider, servicetoken.RefMopeduRide, servicetoken.RefDoorstepBooking).header(t, servicetoken.OpIntentCreate),
			doorstepIntentBody("doorstep", servicetoken.RefDoorstepBooking), 403, "FORBIDDEN"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			before := len(fake.intents)
			w := do(r, http.MethodPost, internalIntents, tc.body, tc.headers)
			if w.Code != tc.status || errorCode(t, w.Body.Bytes()) != tc.code {
				t.Fatalf("status = %d body=%s, want %d %s", w.Code, w.Body.String(), tc.status, tc.code)
			}
			if len(fake.intents) != before {
				t.Fatal("a refused request created an intent")
			}
		})
	}

	t.Run("the transactions route: doorstep-service reads doorstep only, and only its own intents", func(t *testing.T) {
		const apps = "/v1/payments/internal/applications"
		w := do(r, http.MethodGet, apps+"/doorstep/transactions", nil, doorstep.header(t, servicetoken.OpIntentRead))
		if w.Code != http.StatusOK {
			t.Fatalf("doorstep on doorstep: status = %d body=%s", w.Code, w.Body.String())
		}
		if got := fake.txFilters[len(fake.txFilters)-1]; got.OwnerDomain != "doorstep-service" {
			t.Fatalf("filter owner = %q, want doorstep-service", got.OwnerDomain)
		}
		n := len(fake.txFilters)
		for name, tc := range map[string]struct {
			caller tokenCaller
			app    string
		}{
			"doorstep on feast": {doorstep, "feast"},
			"food on doorstep":  {food, "doorstep"},
			"rider on doorstep": {rider, "doorstep"},
		} {
			w := do(r, http.MethodGet, apps+"/"+tc.app+"/transactions", nil, tc.caller.header(t, servicetoken.OpIntentRead))
			if w.Code != http.StatusForbidden || errorCode(t, w.Body.Bytes()) != CodeApplicationNotAllowed {
				t.Fatalf("%s: status = %d body=%s, want 403 %s", name, w.Code, w.Body.String(), CodeApplicationNotAllowed)
			}
		}
		if len(fake.txFilters) != n {
			t.Fatal("a refused transactions request reached the service")
		}
	})

	t.Run("a legacy internal-key doorstep intent is owned by doorstep-service", func(t *testing.T) {
		for _, ref := range doorstepRefs {
			w := do(r, http.MethodPost, internalIntents, doorstepIntentBody("doorstep", ref), withKey(uuid.Nil))
			if w.Code != http.StatusCreated {
				t.Fatalf("%s: status = %d body=%s", ref, w.Code, w.Body.String())
			}
			if got := fake.initiations[len(fake.initiations)-1]; got.OwnerDomain != "doorstep-service" || got.ApplicationID != "doorstep" {
				t.Fatalf("%s: owner %q app %q, want doorstep-service / doorstep", ref, got.OwnerDomain, got.ApplicationID)
			}
		}
	})
}
