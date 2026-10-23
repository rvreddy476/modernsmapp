package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/service/membershiptest"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// Creator memberships over the real routes, with the real service rules and
// an in-memory store (23 Oct 2026).

func membershipHandler() (*Handler, *membershiptest.Store) {
	h, fake, _ := membershipRig()
	return h, fake
}

// membershipRig is the handler over the real service with memberships on,
// an in-memory store (every PAN verified), a fake payments-service and
// eligibility sources that pass.
func membershipRig() (*Handler, *membershiptest.Store, *membershiptest.Payments) {
	fake := membershiptest.New()
	fake.AllPANVerified = true
	pays := membershiptest.NewPayments()
	elig := membershiptest.Eligible()
	svc := service.New(nil, nil).WithMembershipStore(fake).WithMembershipPayments(pays, false).
		WithEligibilitySources(service.EligibilitySources{BirthDates: elig, Followers: elig, Standing: elig})
	return New(svc).WithWritesEnabled(true).WithMembershipsEnabled(true), fake, pays
}

func serveJSON(h *Handler, method, path, userID, body string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if userID != "" {
		req.Header.Set("X-User-Id", userID)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	routerFor(h).ServeHTTP(w, req)
	return w
}

// A PATCH carrying only price_paise changes only the price. Before the fix
// the absent name and perks were written as empty and is_active as true.
func TestPatchTierChangesOnlyTheFieldsSent(t *testing.T) {
	h, fake := membershipHandler()
	creator := uuid.New()
	tier := fake.AddTier(creator, "Gold", 9_900)
	tier.Perks = json.RawMessage(`["Badge","Early access"]`)
	tier.IsActive = false

	w := serveJSON(h, http.MethodPatch, "/v1/monetization/tiers/"+tier.ID.String(), creator.String(), `{"price_paise":14900}`)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	got, _ := fake.GetCreatorTier(context.Background(), tier.ID)
	if got.PricePaise != 14_900 || got.Name != "Gold" || got.IsActive || string(got.Perks) != `["Badge","Early access"]` {
		t.Fatalf("tier after a price-only PATCH: %+v perks=%s", got, got.Perks)
	}

	// Explicit fields are applied; null is "leave it".
	w = serveJSON(h, http.MethodPatch, "/v1/monetization/tiers/"+tier.ID.String(), creator.String(), `{"name":"Platinum","perks":null}`)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	got, _ = fake.GetCreatorTier(context.Background(), tier.ID)
	if got.Name != "Platinum" || string(got.Perks) != `["Badge","Early access"]` || got.PricePaise != 14_900 {
		t.Fatalf("tier after a name PATCH: %+v perks=%s", got, got.Perks)
	}

	// Someone else's tier.
	w = serveJSON(h, http.MethodPatch, "/v1/monetization/tiers/"+tier.ID.String(), uuid.NewString(), `{"name":"Mine"}`)
	wantCode(t, "foreign tier", w, http.StatusNotFound, "NOT_FOUND")
}

func TestTierRefusalsOnTheWire(t *testing.T) {
	h, fake := membershipHandler()
	creator := uuid.New().String()
	post := func(body string) *httptest.ResponseRecorder {
		return serveJSON(h, http.MethodPost, "/v1/monetization/tiers", creator, body)
	}
	wantCode(t, "₹1", post(`{"name":"Fan","price_paise":100}`), http.StatusBadRequest, "TIER_PRICE_OUT_OF_RANGE")
	wantCode(t, "negative", post(`{"name":"Fan","price_paise":-4900}`), http.StatusBadRequest, "TIER_PRICE_OUT_OF_RANGE")
	wantCode(t, "zero", post(`{"name":"Fan","price_paise":0}`), http.StatusBadRequest, "TIER_PRICE_OUT_OF_RANGE")
	wantCode(t, "above ₹2,999", post(`{"name":"Fan","price_paise":300000}`), http.StatusBadRequest, "TIER_PRICE_OUT_OF_RANGE")
	wantCode(t, "no name", post(`{"price_paise":4900}`), http.StatusBadRequest, "TIER_NAME_INVALID")
	wantCode(t, "11 perks", post(`{"name":"Fan","price_paise":4900,"perks":["1","2","3","4","5","6","7","8","9","10","11"]}`), http.StatusBadRequest, "TIER_PERKS_INVALID")
	for i := 0; i < service.MaxActiveTiers; i++ {
		if w := post(`{"name":"Fan","price_paise":4900,"perks":["Badge"]}`); w.Code != http.StatusCreated {
			t.Fatalf("tier %d: %d %s", i+1, w.Code, w.Body.String())
		}
	}
	wantCode(t, "fourth", post(`{"name":"Fan","price_paise":4900}`), http.StatusConflict, "TIER_LIMIT")
	if n := len(fake.Tiers); n != service.MaxActiveTiers {
		t.Fatalf("%d tiers stored", n)
	}

	var any *postgres.CreatorTier
	for _, tr := range fake.Tiers {
		any = tr
	}
	wantCode(t, "PATCH price below ₹29",
		serveJSON(h, http.MethodPatch, "/v1/monetization/tiers/"+any.ID.String(), creator, `{"price_paise":1000}`),
		http.StatusBadRequest, "TIER_PRICE_OUT_OF_RANGE")
}

func TestSubscribeWhileALiveSubscriptionExistsIs409(t *testing.T) {
	h, fake := membershipHandler()
	creator, fan := uuid.New(), uuid.New()
	tier := fake.AddTier(creator, "Fan", 4_900)
	fake.AddSubscription(postgres.Subscription{SubscriberID: fan, CreatorID: creator, TierID: tier.ID, Status: "paused",
		CurrentPeriodEnd: time.Now().Add(time.Hour)})
	w := serveJSON(h, http.MethodPost, "/v1/monetization/subscribe/"+creator.String(), fan.String(), `{"tier_id":"`+tier.ID.String()+`"}`)
	wantCode(t, "paused subscription exists", w, http.StatusConflict, "ALREADY_SUBSCRIBED")
	if len(fake.Payments) != 0 {
		t.Fatalf("a payment was opened: %v", fake.Payments)
	}
}

// The public bulk route answers only for the caller.
func TestBulkEntitlementsAnswersOnlyForTheCaller(t *testing.T) {
	h, fake := membershipHandler()
	creator, caller, other := uuid.New(), uuid.New(), uuid.New()
	tier := fake.AddTier(creator, "Fan", 4_900)
	fake.AddSubscription(postgres.Subscription{SubscriberID: other, CreatorID: creator, TierID: tier.ID, Status: "active",
		CurrentPeriodEnd: time.Now().Add(time.Hour)})

	body := `{"checks":[{"subscriber_id":"` + other.String() + `","creator_id":"` + creator.String() + `"}]}`
	wantCode(t, "another subscriber", serveJSON(h, http.MethodPost, "/v1/monetization/entitlements/check", caller.String(), body),
		http.StatusForbidden, "FORBIDDEN")

	// Mixed: one own row, one foreign row — still refused whole.
	body = `{"checks":[{"creator_id":"` + creator.String() + `"},{"subscriber_id":"` + other.String() + `","creator_id":"` + creator.String() + `"}]}`
	wantCode(t, "mixed rows", serveJSON(h, http.MethodPost, "/v1/monetization/entitlements/check", caller.String(), body),
		http.StatusForbidden, "FORBIDDEN")

	// No X-User-Id at all.
	body = `{"checks":[{"creator_id":"` + creator.String() + `"}]}`
	if w := serveJSON(h, http.MethodPost, "/v1/monetization/entitlements/check", "", body); w.Code != http.StatusUnauthorized {
		t.Fatalf("no caller: %d %s", w.Code, w.Body.String())
	}

	// The caller's own rows (blank or explicit) are answered, for the caller.
	body = `{"checks":[{"creator_id":"` + creator.String() + `"},{"subscriber_id":"` + caller.String() + `","creator_id":"` + creator.String() + `"}]}`
	w := serveJSON(h, http.MethodPost, "/v1/monetization/entitlements/check", caller.String(), body)
	if w.Code != http.StatusOK {
		t.Fatalf("own rows: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Results []service.Entitlement `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Data.Results) != 2 {
		t.Fatalf("results: %s", w.Body.String())
	}
	for _, r := range resp.Data.Results {
		if r.SubscriberID != caller || r.Allowed {
			t.Fatalf("row answered for %v allowed=%v, want the caller, not allowed", r.SubscriberID, r.Allowed)
		}
	}
}

// The any-subscriber form lives under /internal/ and needs a post-service
// token carrying the entitlements op.
func TestInternalBulkEntitlementsNeedsAServiceToken(t *testing.T) {
	fake := membershiptest.New()
	creator, fan := uuid.New(), uuid.New()
	tier := fake.AddTier(creator, "Fan", 4_900)
	fake.AddSubscription(postgres.Subscription{SubscriberID: fan, CreatorID: creator, TierID: tier.ID, Status: "active",
		CurrentPeriodEnd: time.Now().Add(time.Hour)})

	pPub, pPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SERVICE_CALLERS":                    "post-service",
		"SERVICE_CALLER_POST_SERVICE_KID":    "p1",
		"SERVICE_CALLER_POST_SERVICE_PUBKEY": pPub,
		"SERVICE_CALLER_POST_SERVICE_OPS":    OpEntitlementsCheck,
	}
	v, err := ServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64(IssuerPostService, "p1", pPriv)
	if err != nil {
		t.Fatal(err)
	}
	h := New(service.New(nil, nil).WithMembershipStore(fake)).WithServiceAuth(v).WithWritesEnabled(true)

	const path = "/v1/monetization/internal/entitlements/check"
	body := `{"checks":[{"subscriber_id":"` + fan.String() + `","creator_id":"` + creator.String() + `"}]}`
	wantCode(t, "no token", serveJSON(h, http.MethodPost, path, "", body), http.StatusUnauthorized, CodeServiceCredentialRequired)
	wantCode(t, "wrong op", serveJSON(h, http.MethodPost, path, "", body, ServiceAuthHeader, "Bearer "+mint(t, signer, AudienceMonetization, []string{"monetization:stats.read"})),
		http.StatusForbidden, CodeAdminPermissionScope)

	w := serveJSON(h, http.MethodPost, path, "", body, ServiceAuthHeader, "Bearer "+mint(t, signer, AudienceMonetization, []string{OpEntitlementsCheck}))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"allowed":true`) {
		t.Fatalf("post-service token: %d %s", w.Code, w.Body.String())
	}
	wantCode(t, "row without subscriber",
		serveJSON(h, http.MethodPost, path, "", `{"checks":[{"creator_id":"`+creator.String()+`"}]}`, ServiceAuthHeader, "Bearer "+mint(t, signer, AudienceMonetization, []string{OpEntitlementsCheck})),
		http.StatusBadRequest, "INVALID_REQUEST")
}

func TestUpgradeAnswersWithTheTierChange(t *testing.T) {
	h, fake := membershipHandler()
	creator, fan := uuid.New(), uuid.New()
	bronze := fake.AddTier(creator, "Bronze", 4_900)
	silver := fake.AddTier(creator, "Silver", 9_900)
	now := time.Now()
	sub := fake.AddSubscription(postgres.Subscription{SubscriberID: fan, CreatorID: creator, TierID: silver.ID, TierName: "Silver",
		PricePaise: 9_900, Status: "active", CurrentPeriodStart: now.Add(-time.Hour), CurrentPeriodEnd: now.Add(29 * 24 * time.Hour)})
	w := serveJSON(h, http.MethodPost, "/v1/monetization/subscriptions/"+sub.ID.String()+"/upgrade", fan.String(), `{"new_tier_id":"`+bronze.ID.String()+`"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"scheduled"`) || !strings.Contains(w.Body.String(), `"charged_paise":0`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
