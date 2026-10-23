package http

// Creator memberships (migration 016), against the in-memory fake.
//
// The mappings used for callers that present no verified identity: a
// creator_membership intent from the user-facing family or a legacy
// internal-key caller must be owned by monetization-service and attributed to
// the application memberships, or monetization-service's own token could
// neither read nor refund it.
//
// And the caller scoping the deploy values declare: monetization-service
// holds intent.create, intent.read and refund.create on creator_membership,
// for the application memberships only; no other caller may open, read or
// refund a membership payment, and monetization-service may open no other
// product's.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

func TestMembershipsOwnerDomainAndApplication(t *testing.T) {
	if got := ownerDomainForReference(servicetoken.RefCreatorMembership); got != "monetization-service" {
		t.Fatalf("owner domain for creator_membership = %q, want monetization-service", got)
	}
	if got := legacyApplications[servicetoken.RefCreatorMembership]; got != "memberships" {
		t.Fatalf("legacy application for creator_membership = %q, want memberships", got)
	}
	// The wire value the memberships contract pins.
	if servicetoken.RefCreatorMembership != "creator_membership" {
		t.Fatalf("reference type = %q", servicetoken.RefCreatorMembership)
	}
	// The existing mappings are unchanged.
	for ref, want := range map[string][2]string{
		servicetoken.RefOrder:              {"commerce-service", "mstore"},
		servicetoken.RefFoodOrder:          {"food-service", "feast"},
		servicetoken.RefDatingPremium:      {"dating-service", "dating"},
		servicetoken.RefMopeduRide:         {"rider-service", "mopedu"},
		servicetoken.RefMopeduSubscription: {"rider-service", "mopedu"},
		servicetoken.RefDoorstepBooking:    {"doorstep-service", "doorstep"},
		servicetoken.RefDoorstepExtras:     {"doorstep-service", "doorstep"},
	} {
		if got := ownerDomainForReference(ref); got != want[0] {
			t.Errorf("owner domain for %s = %q, want %q", ref, got, want[0])
		}
		if got := legacyApplications[ref]; got != want[1] {
			t.Errorf("legacy application for %s = %q, want %q", ref, got, want[1])
		}
	}
}

func membershipIntentBody(app, ref string) []byte {
	m := map[string]any{
		"payer_id": uuid.New(), "payee_id": uuid.New(), "reference_type": ref, "reference_id": uuid.New(),
		"amount_minor": 9900, "method": "upi", "idempotency_key": "membership:" + uuid.NewString(),
	}
	if app != "" {
		m["application_id"] = app
	}
	b, _ := json.Marshal(m)
	return b
}

func TestMembershipsCallerScoping(t *testing.T) {
	fake := newFake()
	for _, key := range []string{"memberships", "doorstep"} {
		fake.apps[key] = &postgres.Application{Key: key, Status: "active", MerchantDisplayName: "Momentum Merchant",
			EnabledMethods: []string{"card", "upi"}, Settings: []byte(`{}`)}
	}
	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	membershipRefs := []string{servicetoken.RefCreatorMembership}
	monetization := newTokenCaller(t, v, "monetization-service", "m1", moneyOps, membershipRefs)
	food := newTokenCaller(t, v, "food-service", "f1", moneyOps, []string{servicetoken.RefFoodOrder})
	doorstep := newTokenCaller(t, v, "doorstep-service", "ds1", moneyOps, []string{servicetoken.RefDoorstepBooking})
	r := newRouter(t, fake, routerOpts{internalKey: testInternalKey, verifier: v, callerApps: map[string][]string{
		"monetization-service": {"memberships"},
		"food-service":         {"feast"},
		"doorstep-service":     {"doorstep"},
	}})

	var ownIntent uuid.UUID
	t.Run("monetization-service opens a creator_membership payment for memberships, owned by monetization-service", func(t *testing.T) {
		w := do(r, http.MethodPost, internalIntents, membershipIntentBody("memberships", servicetoken.RefCreatorMembership), monetization.header(t, servicetoken.OpIntentCreate))
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
		got := fake.initiations[len(fake.initiations)-1]
		if got.ApplicationID != "memberships" || got.OwnerDomain != "monetization-service" || got.ReferenceType != servicetoken.RefCreatorMembership {
			t.Fatalf("service input = app %q owner %q ref %q", got.ApplicationID, got.OwnerDomain, got.ReferenceType)
		}
		ownIntent = env.Data.ID
	})

	t.Run("monetization-service naming no application uses memberships, its only one", func(t *testing.T) {
		w := do(r, http.MethodPost, internalIntents, membershipIntentBody("", servicetoken.RefCreatorMembership), monetization.header(t, servicetoken.OpIntentCreate))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		if got := fake.initiations[len(fake.initiations)-1].ApplicationID; got != "memberships" {
			t.Fatalf("application = %q, want memberships", got)
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
		// The application allowlist (SERVICE_CALLER_MONETIZATION_SERVICE_APPLICATIONS=memberships).
		{"monetization-service may not open a feast payment", monetization.header(t, servicetoken.OpIntentCreate),
			membershipIntentBody("feast", servicetoken.RefCreatorMembership), 422, CodeApplicationNotAllowed},
		{"monetization-service may not open a doorstep payment", monetization.header(t, servicetoken.OpIntentCreate),
			membershipIntentBody("doorstep", servicetoken.RefCreatorMembership), 422, CodeApplicationNotAllowed},
		{"food-service may not open a memberships payment", food.header(t, servicetoken.OpIntentCreate),
			membershipIntentBody("memberships", servicetoken.RefFoodOrder), 422, CodeApplicationNotAllowed},
		{"doorstep-service may not open a memberships payment", doorstep.header(t, servicetoken.OpIntentCreate),
			membershipIntentBody("memberships", servicetoken.RefDoorstepBooking), 422, CodeApplicationNotAllowed},
		// The reference-type allowlist (REFTYPES).
		{"monetization-service claiming food_order is refused", claims(monetization, servicetoken.RefCreatorMembership, servicetoken.RefFoodOrder).header(t, servicetoken.OpIntentCreate),
			membershipIntentBody("memberships", servicetoken.RefFoodOrder), 403, "FORBIDDEN"},
		{"monetization-service claiming doorstep_booking is refused", claims(monetization, servicetoken.RefCreatorMembership, servicetoken.RefDoorstepBooking).header(t, servicetoken.OpIntentCreate),
			membershipIntentBody("memberships", servicetoken.RefDoorstepBooking), 403, "FORBIDDEN"},
		{"food-service claiming creator_membership is refused for its own application", claims(food, servicetoken.RefFoodOrder, servicetoken.RefCreatorMembership).header(t, servicetoken.OpIntentCreate),
			membershipIntentBody("feast", servicetoken.RefCreatorMembership), 403, "FORBIDDEN"},
		{"doorstep-service claiming creator_membership is refused for its own application", claims(doorstep, servicetoken.RefDoorstepBooking, servicetoken.RefCreatorMembership).header(t, servicetoken.OpIntentCreate),
			membershipIntentBody("doorstep", servicetoken.RefCreatorMembership), 403, "FORBIDDEN"},
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

	t.Run("another caller can neither read nor refund a membership payment", func(t *testing.T) {
		if ownIntent == uuid.Nil {
			t.Skip("no intent was created above")
		}
		path := internalIntents + "/" + ownIntent.String()
		refund, _ := json.Marshal(map[string]any{"amount_minor": 9900, "reason": "test", "idempotency_key": "r:" + uuid.NewString(), "application_id": "memberships"})
		for name, c := range map[string]tokenCaller{
			"food":     claims(food, servicetoken.RefFoodOrder, servicetoken.RefCreatorMembership),
			"doorstep": claims(doorstep, servicetoken.RefDoorstepBooking, servicetoken.RefCreatorMembership),
		} {
			if w := do(r, http.MethodGet, path, nil, c.header(t, servicetoken.OpIntentRead)); w.Code != http.StatusNotFound {
				t.Fatalf("%s read: status = %d body=%s, want 404", name, w.Code, w.Body.String())
			}
			before := len(fake.refunds)
			if w := do(r, http.MethodPost, path+"/refund", refund, c.header(t, servicetoken.OpRefundCreate)); w.Code != http.StatusNotFound {
				t.Fatalf("%s refund: status = %d body=%s, want 404", name, w.Code, w.Body.String())
			}
			if len(fake.refunds) != before {
				t.Fatalf("%s: a refused refund reached the service", name)
			}
		}
		// The owner still can.
		if w := do(r, http.MethodGet, path, nil, monetization.header(t, servicetoken.OpIntentRead)); w.Code != http.StatusOK {
			t.Fatalf("owner read: status = %d body=%s", w.Code, w.Body.String())
		}
		if w := do(r, http.MethodPost, path+"/refund", refund, monetization.header(t, servicetoken.OpRefundCreate)); w.Code != http.StatusAccepted {
			t.Fatalf("owner refund: status = %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("the transactions route: monetization-service reads memberships only, and only its own intents", func(t *testing.T) {
		const apps = "/v1/payments/internal/applications"
		w := do(r, http.MethodGet, apps+"/memberships/transactions", nil, monetization.header(t, servicetoken.OpIntentRead))
		if w.Code != http.StatusOK {
			t.Fatalf("monetization on memberships: status = %d body=%s", w.Code, w.Body.String())
		}
		if got := fake.txFilters[len(fake.txFilters)-1]; got.OwnerDomain != "monetization-service" {
			t.Fatalf("filter owner = %q, want monetization-service", got.OwnerDomain)
		}
		n := len(fake.txFilters)
		for name, tc := range map[string]struct {
			caller tokenCaller
			app    string
		}{
			"monetization on feast":   {monetization, "feast"},
			"food on memberships":     {food, "memberships"},
			"doorstep on memberships": {doorstep, "memberships"},
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

	t.Run("a legacy internal-key membership intent is owned by monetization-service", func(t *testing.T) {
		w := do(r, http.MethodPost, internalIntents, membershipIntentBody("memberships", servicetoken.RefCreatorMembership), withKey(uuid.Nil))
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		if got := fake.initiations[len(fake.initiations)-1]; got.OwnerDomain != "monetization-service" || got.ApplicationID != "memberships" {
			t.Fatalf("owner %q app %q, want monetization-service / memberships", got.OwnerDomain, got.ApplicationID)
		}
		var env intentEnvelope
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		// So another domain's token cannot reach it either.
		if w := do(r, http.MethodGet, internalIntents+"/"+env.Data.ID.String(), nil, food.header(t, servicetoken.OpIntentRead)); w.Code != http.StatusNotFound {
			t.Fatalf("food read of a legacy membership intent: status = %d, want 404", w.Code)
		}
		if w := do(r, http.MethodGet, internalIntents+"/"+env.Data.ID.String(), nil, monetization.header(t, servicetoken.OpIntentRead)); w.Code != http.StatusOK {
			t.Fatalf("monetization read of a legacy membership intent: status = %d body=%s", w.Code, w.Body.String())
		}
	})
}
