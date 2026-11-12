package http

import (
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/monetization-service/internal/premium"
	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/service/premiumtest"
	"github.com/google/uuid"
)

// Momentum Premium over the real routes and the real launch boundary.

func premiumHandler(prices premium.Prices, enabled bool) (*Handler, *premiumtest.Store, *premiumtest.Payments) {
	store, pays := premiumtest.New(), premiumtest.NewPayments()
	p := service.NewPremium(store, prices).WithPayments(pays, false).WithRoles(&premiumtest.Roles{})
	return New(nil).WithPremium(p, enabled), store, pays
}

var premiumPaths = []struct{ method, path, body string }{
	{http.MethodGet, "/v1/monetization/premium/plans", ""},
	{http.MethodPost, "/v1/monetization/premium/checkout", `{"plan":"premium_30d"}`},
	{http.MethodGet, "/v1/monetization/premium/me", ""},
	{http.MethodGet, "/v1/monetization/premium/passes/" + uuid.NewString(), ""},
}

// PREMIUM_ENABLED off (the default) closes every premium route with 503,
// whatever the writes and memberships flags say, before the handler runs.
func TestPremiumSwitchOffAnswers503(t *testing.T) {
	h, store, pays := premiumHandler(premium.Prices{premium.Plan30d: 14900}, false)
	h.WithWritesEnabled(true).WithMembershipsEnabled(true)
	for _, p := range premiumPaths {
		wantCode(t, p.method+" "+p.path, serveJSON(h, p.method, p.path, uuid.NewString(), p.body),
			http.StatusServiceUnavailable, "PREMIUM_NOT_ENABLED")
	}
	if len(store.Periods) != 0 || len(pays.Calls) != 0 {
		t.Fatal("a closed switch must open nothing")
	}
	// Enabled without a service is still closed.
	h = New(nil).WithPremium(nil, true)
	wantCode(t, "enabled, no service", serveJSON(h, http.MethodGet, "/v1/monetization/premium/me", uuid.NewString(), ""),
		http.StatusServiceUnavailable, "PREMIUM_NOT_ENABLED")
}

func TestPremiumMaintenanceClosesIt(t *testing.T) {
	h, _, _ := premiumHandler(premium.Prices{premium.Plan30d: 14900}, true)
	h.WithMaintenance(true).WithInternalKey(adminTestInternalKey)
	w := serveJSON(h, http.MethodGet, "/v1/monetization/premium/me", uuid.NewString(), "", "X-Internal-Service-Key", adminTestInternalKey)
	wantCode(t, "maintenance", w, http.StatusServiceUnavailable, "MAINTENANCE")
}

func TestPremiumRoutesWhenOn(t *testing.T) {
	h, store, _ := premiumHandler(premium.Prices{premium.Plan30d: 14900}, true)
	user := uuid.NewString()

	w := serveJSON(h, http.MethodGet, "/v1/monetization/premium/plans", user, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"price_paise":14900`) ||
		!strings.Contains(w.Body.String(), `"price_paise":null`) {
		t.Fatalf("plans: %d %s", w.Code, w.Body.String())
	}

	w = serveJSON(h, http.MethodPost, "/v1/monetization/premium/checkout", user, `{"plan":"premium_30d"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("checkout: %d %s", w.Code, w.Body.String())
	}
	var co service.PremiumCheckout
	decodeData(t, w, &co)
	if co.PassID == uuid.Nil || co.AmountPaise != 17582 || co.Status != "pending_payment" || co.ClientSession == nil {
		t.Fatalf("checkout %+v", co)
	}

	// Checkout opened no access.
	w = serveJSON(h, http.MethodGet, "/v1/monetization/premium/me", user, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"active":false`) || !strings.Contains(w.Body.String(), `"ends_at":null`) {
		t.Fatalf("me: %d %s", w.Code, w.Body.String())
	}
	w = serveJSON(h, http.MethodGet, "/v1/monetization/premium/passes/"+co.PassID.String(), user, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"confirming"`) {
		t.Fatalf("pass: %d %s", w.Code, w.Body.String())
	}
	wantCode(t, "stranger's pass", serveJSON(h, http.MethodGet, "/v1/monetization/premium/passes/"+co.PassID.String(), uuid.NewString(), ""),
		http.StatusNotFound, "NOT_FOUND")

	// The 365-day plan has no price: fail closed.
	wantCode(t, "unpriced plan", serveJSON(h, http.MethodPost, "/v1/monetization/premium/checkout", user, `{"plan":"premium_365d"}`),
		http.StatusServiceUnavailable, "PREMIUM_PRICE_NOT_SET")
	wantCode(t, "unknown plan", serveJSON(h, http.MethodPost, "/v1/monetization/premium/checkout", user, `{"plan":"premium_1d"}`),
		http.StatusBadRequest, "UNKNOWN_PLAN")
	if len(store.Periods) != 1 {
		t.Fatalf("refusals must open nothing: %d rows", len(store.Periods))
	}
}

// The amount is the server's: a body that carries one is refused.
func TestPremiumCheckoutRefusesAClientAmount(t *testing.T) {
	h, store, _ := premiumHandler(premium.Prices{premium.Plan30d: 14900}, true)
	for _, body := range []string{`{"plan":"premium_30d","amount_paise":100}`, `{}`, `not json`, ``} {
		wantCode(t, body, serveJSON(h, http.MethodPost, "/v1/monetization/premium/checkout", uuid.NewString(), body),
			http.StatusBadRequest, "BAD_REQUEST")
	}
	if len(store.Periods) != 0 {
		t.Fatal("nothing may be opened")
	}
}

func TestPremiumNeedsAUser(t *testing.T) {
	h, _, _ := premiumHandler(premium.Prices{premium.Plan30d: 14900}, true)
	for _, p := range premiumPaths {
		wantCode(t, p.path, serveJSON(h, p.method, p.path, "", p.body), http.StatusUnauthorized, "UNAUTHORIZED")
	}
}

// There is no client route that confirms or activates a pass: anything under
// /premium that is not listed falls to the boundary's fail-closed default.
func TestPremiumHasNoConfirmRoute(t *testing.T) {
	h, _, _ := premiumHandler(premium.Prices{premium.Plan30d: 14900}, true)
	for _, path := range []string{
		"/v1/monetization/premium/passes/" + uuid.NewString() + "/verify",
		"/v1/monetization/premium/passes/" + uuid.NewString() + "/confirm",
		"/v1/monetization/premium/activate",
	} {
		if w := serveJSON(h, http.MethodPost, path, uuid.NewString(), `{}`); w.Code < 400 {
			t.Fatalf("%s answered %d", path, w.Code)
		}
	}
}
