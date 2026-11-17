//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Paid creator memberships (monetization-service membership_payments_handler.go,
// contract pinned 23 Oct 2026). They run on their own switch,
// MEMBERSHIPS_ENABLED, independent of the money-writes flag: see
// monetization_payout_e2e_test.go.

// tierBody is a tier that passes validation: a name, a price inside
// ₹29..₹2,999 in paise, INR.
func tierBody(pricePaise int64) map[string]any {
	return map[string]any{
		"name":        "Supporters",
		"price_paise": pricePaise,
		"currency":    "INR",
		"perks":       []string{"Members-only posts"},
	}
}

// TestMembershipClosedAtLaunch guards the switch: with memberships off nobody
// creates or lists tiers, joins, leaves, or reads a membership, signed in or
// not, at the service or through the gateway. Every membership route is 503
// MEMBERSHIPS_NOT_ENABLED, whatever the money-writes flag says. The
// entitlement reads follow whichever of the two switches is on, so on a
// default stack they are 503 MONETIZATION_NOT_LAUNCHED.
func TestMembershipClosedAtLaunch(t *testing.T) {
	urls, ctx := monetizationForFlow(t)
	if membershipsOpen() {
		t.Skipf("%s=1: this stack runs with memberships open", membershipsOpenEnv)
	}
	creatorID, fanID := uuid.New(), uuid.New()
	creator := "/v1/monetization/creators/" + creatorID.String()
	subscribe := "/v1/monetization/subscribe/" + creatorID.String()
	subscription := "/v1/monetization/subscriptions/" + uuid.NewString()

	for _, tc := range []struct {
		as     uuid.UUID
		method string
		path   string
		body   any
	}{
		{creatorID, "POST", "/v1/monetization/tiers", tierBody(9900)},
		{creatorID, "GET", "/v1/monetization/tiers", nil},
		{creatorID, "PATCH", "/v1/monetization/tiers/" + uuid.NewString(), map[string]any{"price_paise": 19900}},
		{creatorID, "GET", creator + "/membership-eligibility", nil},
		{creatorID, "GET", "/v1/monetization/members", nil},
		{fanID, "GET", creator + "/tiers", nil},
		{fanID, "POST", subscribe, map[string]any{"tier_id": uuid.NewString()}},
		{fanID, "DELETE", subscribe, nil},
		{fanID, "GET", "/v1/monetization/memberships", nil},
		{fanID, "POST", subscription + "/cancel", nil},
		{fanID, "POST", subscription + "/renew", nil},
	} {
		wantMoneyRefusal(t, ctx, urls, tc.as, tc.method, tc.path, tc.body,
			http.StatusServiceUnavailable, membershipsClosedCode)
	}

	if !monetizationWrites() {
		wantMoneyRefusal(t, ctx, urls, fanID, "GET",
			"/v1/monetization/entitlements?creator_id="+creatorID.String(), nil,
			http.StatusServiceUnavailable, moneyClosedCode)
	}

	// The public support read still answers, and counts no tiers.
	var support creatorSupport
	env := NewHTTPClient(urls.APIGateway, fanID).MustDo(t, ctx, "GET", creator+"/support", nil)
	if wantData(t, env, "GET "+creator+"/support", http.StatusOK, &support) && support.MembershipTiers != 0 {
		t.Errorf("a creator who cannot create a tier has membership_tiers=%d", support.MembershipTiers)
	}
}

// TestMembershipEligibilityGate is the on-path for a stack that runs with
// memberships open (ATPOST_E2E_MEMBERSHIPS=1). It stops where the product
// needs facts this suite cannot make true: offering memberships needs a
// creator who is 18 or older, PAN verified, has 500 followers and no active
// strike, and a join completes only on payments-service's signed payment
// event. What it guards is the gate in front of all that:
//
//  1. a creator's tier list is readable and empty
//  2. the creator's own eligibility read says not eligible, and why; nobody
//     else may read it (403 FORBIDDEN)
//  3. a tier with a price outside ₹29..₹2,999 is 400 TIER_PRICE_OUT_OF_RANGE
//  4. a well-formed first tier from a creator who is not eligible is 403
//     MEMBERSHIPS_NOT_ELIGIBLE, and no tier exists afterwards
//  5. a creator cannot join their own membership (400 CANNOT_SUBSCRIBE_TO_SELF)
//
// The old flow here (create a tier, gate a post on it, subscribe, read the
// post un-redacted) predates that contract: it sent "price" where the route
// reads "price_paise", and expected subscribe to grant access with no payment.
func TestMembershipEligibilityGate(t *testing.T) {
	urls, ctx := monetizationForFlow(t)
	if !membershipsOpen() {
		t.Skipf("%s is not 1: memberships are closed on this stack (TestMembershipClosedAtLaunch covers that)", membershipsOpenEnv)
	}
	creatorID, fanID := uuid.New(), uuid.New()
	creator := NewHTTPClient(urls.Monetization, creatorID)
	fan := NewHTTPClient(urls.Monetization, fanID)
	eligibilityPath := "/v1/monetization/creators/" + creatorID.String() + "/membership-eligibility"

	noTiers := func(when string) {
		t.Helper()
		var tiers []json.RawMessage
		env := creator.MustDo(t, ctx, "GET", "/v1/monetization/tiers", nil)
		if wantData(t, env, "GET /v1/monetization/tiers "+when, http.StatusOK, &tiers) && len(tiers) != 0 {
			t.Errorf("%s: the creator has %d tier(s), want none", when, len(tiers))
		}
	}

	// 1. No tiers yet.
	noTiers("before anything")

	// 2. Eligibility: the creator's own, and only theirs.
	var eligibility struct {
		Eligible bool     `json:"eligible"`
		Missing  []string `json:"missing"`
	}
	env := creator.MustDo(t, ctx, "GET", eligibilityPath, nil)
	if wantData(t, env, "creator GET "+eligibilityPath, http.StatusOK, &eligibility) {
		if eligibility.Eligible || len(eligibility.Missing) == 0 {
			t.Errorf("a brand-new account: want eligible=false with the missing items named, got eligible=%t missing=%v",
				eligibility.Eligible, eligibility.Missing)
		}
	}
	env = fan.MustDo(t, ctx, "GET", eligibilityPath, nil)
	wantRefusal(t, env, "someone else reading a creator's eligibility", http.StatusForbidden, "FORBIDDEN")

	// 3. Validation comes before eligibility and names the field.
	env = creator.MustDo(t, ctx, "POST", "/v1/monetization/tiers", tierBody(999))
	wantRefusal(t, env, "tier under the minimum price", http.StatusBadRequest, "TIER_PRICE_OUT_OF_RANGE")

	// 4. The first tier needs eligibility.
	env = creator.MustDo(t, ctx, "POST", "/v1/monetization/tiers", tierBody(9900))
	wantRefusal(t, env, "first tier from a creator who is not eligible", http.StatusForbidden, "MEMBERSHIPS_NOT_ELIGIBLE")
	noTiers("after the refused tier")

	// 5. No joining yourself.
	env = creator.WithHeader("Idempotency-Key", uuid.NewString()).MustDo(t, ctx, "POST",
		"/v1/monetization/subscribe/"+creatorID.String(), map[string]any{"tier_id": uuid.NewString()})
	wantRefusal(t, env, "creator joining their own membership", http.StatusBadRequest, "CANNOT_SUBSCRIBE_TO_SELF")
}
