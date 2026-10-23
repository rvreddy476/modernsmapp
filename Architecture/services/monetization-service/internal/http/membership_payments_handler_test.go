package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/monetization-service/internal/membership"
	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/service/membershiptest"
	"github.com/atpost/shared/events"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Paid memberships over the real routes (phase 1 contract, 23 Oct 2026).

func decodeData(t *testing.T, w *httptest.ResponseRecorder, out any) {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("body %s: %v", w.Body.String(), err)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		t.Fatalf("data %s: %v", env.Data, err)
	}
}

func TestSubscribeAnswersTheCheckoutContract(t *testing.T) {
	h, fake, pays := membershipRig()
	creator, fan := uuid.New(), uuid.New()
	tier := fake.AddTier(creator, "Fan", 4_900)
	body := `{"tier_id":"` + tier.ID.String() + `"}`
	w := serveJSON(h, http.MethodPost, "/v1/monetization/subscribe/"+creator.String(), fan.String(), body, "Idempotency-Key", "k-1")
	if w.Code != http.StatusCreated {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var co struct {
		SubscriptionID uuid.UUID `json:"subscription_id"`
		PaymentID      uuid.UUID `json:"payment_id"`
		Status         string    `json:"status"`
		AmountPaise    int64     `json:"amount_paise"`
		Currency       string    `json:"currency"`
		ClientSession  struct {
			Provider            string `json:"provider"`
			OrderID             string `json:"order_id"`
			KeyID               string `json:"key_id"`
			MerchantDisplayName string `json:"merchant_display_name"`
		} `json:"client_session"`
	}
	decodeData(t, w, &co)
	if co.SubscriptionID == uuid.Nil || co.PaymentID == uuid.Nil || co.Status != "pending_payment" || co.AmountPaise != 4_900 ||
		co.Currency != "INR" || co.ClientSession.Provider != "razorpay" || co.ClientSession.OrderID == "" || co.ClientSession.KeyID == "" ||
		co.ClientSession.MerchantDisplayName == "" {
		t.Fatalf("checkout = %+v", co)
	}
	// The header is honoured: the same key is the same checkout.
	w2 := serveJSON(h, http.MethodPost, "/v1/monetization/subscribe/"+creator.String(), fan.String(), body, "Idempotency-Key", "k-1")
	if !strings.Contains(w2.Body.String(), co.PaymentID.String()) || pays.Creates != 1 {
		t.Fatalf("replay: %s", w2.Body.String())
	}

	// The payer polls; anyone else gets 404.
	path := "/v1/monetization/memberships/payments/" + co.PaymentID.String()
	if w := serveJSON(h, http.MethodGet, path, fan.String(), ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"confirming"`) {
		t.Fatalf("poll: %d %s", w.Code, w.Body.String())
	}
	wantCode(t, "stranger poll", serveJSON(h, http.MethodGet, path, uuid.NewString(), ""), http.StatusNotFound, "NOT_FOUND")

	// The callback is advisory: no access after it.
	w = serveJSON(h, http.MethodPost, path+"/verify", fan.String(), `{"razorpay_order_id":"o","razorpay_payment_id":"p","razorpay_signature":"s"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", w.Code, w.Body.String())
	}
	ent := serveJSON(h, http.MethodGet, "/v1/monetization/entitlements?creator_id="+creator.String(), fan.String(), "")
	if !strings.Contains(ent.Body.String(), `"allowed":false`) {
		t.Fatalf("verify granted access: %s", ent.Body.String())
	}
	// The signed event grants it.
	p := fake.Payment(co.PaymentID)
	if _, err := h.svc.ApplyMembershipPaymentEvent(context.Background(), membership.Event{EventID: "e1", EventType: events.EventPaymentSucceeded,
		IntentID: p.IntentID, PaymentID: p.ID, PayerID: fan, AmountMinor: 4_900, Currency: "INR"}); err != nil {
		t.Fatal(err)
	}
	ent = serveJSON(h, http.MethodGet, "/v1/monetization/entitlements?creator_id="+creator.String(), fan.String(), "")
	if !strings.Contains(ent.Body.String(), `"allowed":true`) {
		t.Fatalf("event did not grant access: %s", ent.Body.String())
	}
	// My memberships.
	w = serveJSON(h, http.MethodGet, "/v1/monetization/memberships", fan.String(), "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"renew_available":true`) || !strings.Contains(w.Body.String(), `"creator":{"id":"`+creator.String()) {
		t.Fatalf("my memberships: %d %s", w.Code, w.Body.String())
	}
	// Renew: 201, kind renewal.
	w = serveJSON(h, http.MethodPost, "/v1/monetization/subscriptions/"+co.SubscriptionID.String()+"/renew", fan.String(), "")
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"kind":"renewal"`) {
		t.Fatalf("renew: %d %s", w.Code, w.Body.String())
	}
	wantCode(t, "renew another's", serveJSON(h, http.MethodPost, "/v1/monetization/subscriptions/"+co.SubscriptionID.String()+"/renew", uuid.NewString(), ""),
		http.StatusForbidden, "FORBIDDEN")
}

func TestUpgradeOverTheWireOpensACheckout(t *testing.T) {
	h, fake, _ := membershipRig()
	creator, fan := uuid.New(), uuid.New()
	bronze := fake.AddTier(creator, "Bronze", 4_900)
	silver := fake.AddTier(creator, "Silver", 9_900)
	co, err := h.svc.Subscribe(context.Background(), fan, creator, bronze.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	p := fake.Payment(co.PaymentID)
	h.svc.ApplyMembershipPaymentEvent(context.Background(), membership.Event{EventID: "e1", EventType: events.EventPaymentSucceeded,
		IntentID: p.IntentID, PaymentID: p.ID, PayerID: fan, AmountMinor: 4_900, Currency: "INR"})
	w := serveJSON(h, http.MethodPost, "/v1/monetization/subscriptions/"+co.SubscriptionID.String()+"/upgrade", fan.String(), `{"tier_id":"`+silver.ID.String()+`"}`)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"kind":"upgrade"`) || !strings.Contains(w.Body.String(), `"status":"pending_payment"`) {
		t.Fatalf("upgrade: %d %s", w.Code, w.Body.String())
	}
}

func TestEligibilityRouteIsTheCreatorsOwn(t *testing.T) {
	h, _, _ := membershipRig()
	creator := uuid.New()
	path := "/v1/monetization/creators/" + creator.String() + "/membership-eligibility"
	w := serveJSON(h, http.MethodGet, path, creator.String(), "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"eligible":true`) || !strings.Contains(w.Body.String(), `"missing":[]`) {
		t.Fatalf("self: %d %s", w.Code, w.Body.String())
	}
	wantCode(t, "someone else", serveJSON(h, http.MethodGet, path, uuid.NewString(), ""), http.StatusForbidden, "FORBIDDEN")
}

func TestFirstTierRefusedWhenNotEligible(t *testing.T) {
	fake := membershiptest.New()
	elig := membershiptest.Eligible()
	elig.Followers = 12
	svc := service.New(nil, nil).WithMembershipStore(fake).WithMembershipPayments(membershiptest.NewPayments(), false).
		WithEligibilitySources(service.EligibilitySources{BirthDates: elig, Followers: elig, Standing: elig})
	h := New(svc).WithMembershipsEnabled(true)
	w := serveJSON(h, http.MethodPost, "/v1/monetization/tiers", uuid.NewString(), `{"name":"Fan","price_paise":4900}`)
	wantCode(t, "not eligible", w, http.StatusForbidden, "MEMBERSHIPS_NOT_ELIGIBLE")
	// No PAN in this store, and too few followers.
	if !strings.Contains(w.Body.String(), `"missing":["pan","followers"]`) {
		t.Fatalf("missing list: %s", w.Body.String())
	}
}

// The switch matrix at the HTTP boundary: MEMBERSHIPS_ENABLED opens the
// membership and entitlement routes with writes off; tips, the creator fund
// and payouts stay behind writes; with memberships off a membership route is
// closed even with writes on; maintenance closes everything but its own.
func TestMembershipsSwitchAtTheBoundary(t *testing.T) {
	type probe struct{ method, pattern string }
	membershipRoute := probe{http.MethodPost, "/v1/monetization/subscribe/:creatorId"}
	renewRoute := probe{http.MethodPost, "/v1/monetization/subscriptions/:id/renew"}
	entitlementRoute := probe{http.MethodGet, "/v1/monetization/entitlements"}
	internalEntitlement := probe{http.MethodPost, "/v1/monetization/internal/entitlements/check"}
	tips := probe{http.MethodPost, "/v1/monetization/tips"}
	fund := probe{http.MethodPost, "/v1/monetization/creator-fund/apply"}
	payouts := probe{http.MethodPost, "/v1/monetization/payouts"}

	cases := []struct {
		name                       string
		writes, memberships, maint bool
		open                       []probe
		closed                     map[probe]string
	}{
		{"memberships on, writes off", false, true, false,
			[]probe{membershipRoute, renewRoute, entitlementRoute, internalEntitlement},
			map[probe]string{tips: "MONETIZATION_NOT_LAUNCHED", fund: "MONETIZATION_NOT_LAUNCHED", payouts: "MONETIZATION_NOT_LAUNCHED"}},
		{"memberships off, writes on", true, false, false,
			[]probe{entitlementRoute, tips, fund, payouts},
			map[probe]string{membershipRoute: "MEMBERSHIPS_NOT_ENABLED", renewRoute: "MEMBERSHIPS_NOT_ENABLED"}},
		{"both off", false, false, false, nil,
			map[probe]string{membershipRoute: "MEMBERSHIPS_NOT_ENABLED", entitlementRoute: "MONETIZATION_NOT_LAUNCHED", tips: "MONETIZATION_NOT_LAUNCHED"}},
		{"both on", true, true, false, []probe{membershipRoute, entitlementRoute, tips, fund}, nil},
		{"maintenance", true, true, true, nil,
			map[probe]string{membershipRoute: "MAINTENANCE", entitlementRoute: "MAINTENANCE", tips: "MAINTENANCE"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := New(nil).WithInternalKey("k").WithWritesEnabled(tc.writes).WithMembershipsEnabled(tc.memberships).WithMaintenance(tc.maint)
			for _, p := range tc.open {
				path := strings.NewReplacer(":creatorId", uuid.NewString(), ":id", uuid.NewString()).Replace(p.pattern)
				w := serveWithBoundary(t, h, p.method, p.pattern, path, func(c *gin.Context) { c.Status(http.StatusNoContent) })
				if w.Code != http.StatusNoContent {
					t.Fatalf("%s %s must be open: %d %s", p.method, p.pattern, w.Code, w.Body.String())
				}
			}
			for p, code := range tc.closed {
				path := strings.NewReplacer(":creatorId", uuid.NewString(), ":id", uuid.NewString()).Replace(p.pattern)
				w := serveWithBoundary(t, h, p.method, p.pattern, path, func(c *gin.Context) { t.Fatalf("%s %s reached its handler", p.method, p.pattern) })
				if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), `"`+code+`"`) {
					t.Fatalf("%s %s: %d %s, want 503 %s", p.method, p.pattern, w.Code, w.Body.String(), code)
				}
			}
		})
	}
}

// Every registered membership route is in the switch's list, and nothing in
// the list is unregistered: the gate cannot silently miss a route.
func TestMembershipRouteListMatchesTheRouter(t *testing.T) {
	h, _, _ := membershipRig()
	r := routerFor(h)
	registered := map[string]bool{}
	for _, ri := range r.Routes() {
		registered[ri.Method+" "+ri.Path] = true
	}
	for _, rule := range append(append([]betaRule{}, membershipRoutes...), entitlementRoutes...) {
		if !registered[rule.method+" "+rule.pattern] {
			t.Errorf("%s %s is in the switch list but not registered", rule.method, rule.pattern)
		}
	}
	for _, want := range []string{"POST /v1/monetization/subscriptions/:id/renew", "GET /v1/monetization/memberships",
		"GET /v1/monetization/memberships/payments/:paymentId", "POST /v1/monetization/memberships/payments/:paymentId/verify",
		"GET /v1/monetization/creators/:creatorId/membership-eligibility"} {
		if !registered[want] {
			t.Errorf("%s not registered", want)
		}
	}
}

func TestSupportRefundRouteNeedsTheAdminTokenAndMemberships(t *testing.T) {
	rg := newAdminTokenRig(t)
	fake := membershiptest.New()
	fake.AllPANVerified = true
	pays := membershiptest.NewPayments()
	elig := membershiptest.Eligible()
	svc := service.New(nil, nil).WithMembershipStore(fake).WithMembershipPayments(pays, false).
		WithEligibilitySources(service.EligibilitySources{BirthDates: elig, Followers: elig, Standing: elig})
	creator, fan := uuid.New(), uuid.New()
	tier := fake.AddTier(creator, "Fan", 4_900)
	co, err := svc.Subscribe(context.Background(), fan, creator, tier.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	p := fake.Payment(co.PaymentID)
	svc.ApplyMembershipPaymentEvent(context.Background(), membership.Event{EventID: "e1", EventType: events.EventPaymentSucceeded,
		IntentID: p.IntentID, PaymentID: p.ID, PayerID: fan, AmountMinor: 4_900, Currency: "INR"})
	path := InternalAdminPrefix + "/memberships/payments/" + co.PaymentID.String() + "/refund"

	off := routerFor(New(svc).WithServiceAuth(rg.v).WithWritesEnabled(true))
	tok := rg.allPermsToken(t)
	if w := adminServe(off, http.MethodPost, path, `{"reason":"double charge"}`, bearer(tok)); w.Code != http.StatusServiceUnavailable || errorCode(t, w) != "MEMBERSHIPS_NOT_ENABLED" {
		t.Fatalf("memberships off: %d %s", w.Code, w.Body.String())
	}
	on := routerFor(New(svc).WithServiceAuth(rg.v).WithMembershipsEnabled(true))
	if w := adminServe(on, http.MethodPost, path, `{"reason":"x"}`, map[string]string{"X-Internal-Service-Key": "k", "X-Scopes": "admin"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", w.Code)
	}
	if w := adminServe(on, http.MethodPost, path, `{"reason":"x"}`, bearer(rg.mint(t, rg.admin, AudienceMonetization, []string{PermFundRead}, rg.actor.String()))); w.Code != http.StatusForbidden {
		t.Fatalf("wrong permission: %d %s", w.Code, w.Body.String())
	}
	if w := adminServe(on, http.MethodPost, path, `{}`, bearer(tok)); w.Code != http.StatusBadRequest {
		t.Fatalf("no reason: %d %s", w.Code, w.Body.String())
	}
	w := adminServe(on, http.MethodPost, path, `{"reason":"double charge"}`, bearer(tok))
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"refund_status":"pending"`) {
		t.Fatalf("refund: %d %s", w.Code, w.Body.String())
	}
	if keys := pays.RefundKeys(); len(keys) != 1 || keys[0] != "membership-refund:"+co.PaymentID.String() {
		t.Fatalf("refund keys %v", keys)
	}
}
