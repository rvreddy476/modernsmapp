package http

// Momentum Premium (migration 019), against the in-memory fake: a
// platform_premium intent is owned by monetization-service and attributed to
// the application premium; monetization-service holding both of its
// applications opens premium payments, and no other caller can.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
)

func TestPremiumOwnerDomainAndApplication(t *testing.T) {
	if servicetoken.RefPlatformPremium != "platform_premium" {
		t.Fatalf("reference type = %q", servicetoken.RefPlatformPremium)
	}
	if got := ownerDomainForReference(servicetoken.RefPlatformPremium); got != "monetization-service" {
		t.Fatalf("owner domain for platform_premium = %q, want monetization-service", got)
	}
	if got := legacyApplications[servicetoken.RefPlatformPremium]; got != "premium" {
		t.Fatalf("legacy application for platform_premium = %q, want premium", got)
	}
	if got := legacyDomainForApplication("premium"); got != "monetization-service" {
		t.Fatalf("domain for premium = %q", got)
	}
	// Memberships is unchanged by monetization-service owning a second application.
	if got := legacyDomainForApplication("memberships"); got != "monetization-service" {
		t.Fatalf("domain for memberships = %q", got)
	}
	if got := legacyApplications[servicetoken.RefCreatorMembership]; got != "memberships" {
		t.Fatalf("creator_membership application = %q", got)
	}
}

func TestPremiumCallerScoping(t *testing.T) {
	fake := newFake()
	for _, key := range []string{"memberships", "premium", "feast"} {
		fake.apps[key] = &postgres.Application{Key: key, Status: "active", MerchantDisplayName: "Momentum Merchant",
			EnabledMethods: []string{"card", "upi"}, Settings: []byte(`{}`)}
	}
	v := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	monetization := newTokenCaller(t, v, "monetization-service", "m1", moneyOps,
		[]string{servicetoken.RefCreatorMembership, servicetoken.RefPlatformPremium})
	food := newTokenCaller(t, v, "food-service", "f1", moneyOps, []string{servicetoken.RefFoodOrder})
	r := newRouter(t, fake, routerOpts{internalKey: testInternalKey, verifier: v, callerApps: map[string][]string{
		"monetization-service": {"memberships", "premium"},
		"food-service":         {"feast"},
	}})

	w := do(r, http.MethodPost, internalIntents, membershipIntentBody("premium", servicetoken.RefPlatformPremium), monetization.header(t, servicetoken.OpIntentCreate))
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var env intentEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	got := fake.initiations[len(fake.initiations)-1]
	if env.Data.ApplicationID != "premium" || got.ApplicationID != "premium" || got.OwnerDomain != "monetization-service" ||
		got.ReferenceType != servicetoken.RefPlatformPremium {
		t.Fatalf("app %q owner %q ref %q", got.ApplicationID, got.OwnerDomain, got.ReferenceType)
	}

	before := len(fake.intents)
	w = do(r, http.MethodPost, internalIntents, membershipIntentBody("premium", servicetoken.RefFoodOrder), food.header(t, servicetoken.OpIntentCreate))
	if w.Code != 422 || errorCode(t, w.Body.Bytes()) != CodeApplicationNotAllowed {
		t.Fatalf("food-service opening premium: %d %s", w.Code, w.Body.String())
	}
	claims := tokenCaller{issuer: food.issuer, signer: food.signer, refs: []string{servicetoken.RefFoodOrder, servicetoken.RefPlatformPremium}}
	w = do(r, http.MethodPost, internalIntents, membershipIntentBody("feast", servicetoken.RefPlatformPremium), claims.header(t, servicetoken.OpIntentCreate))
	if w.Code != http.StatusForbidden {
		t.Fatalf("food-service claiming platform_premium: %d %s", w.Code, w.Body.String())
	}
	if len(fake.intents) != before {
		t.Fatal("a refused request created an intent")
	}
}
