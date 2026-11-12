package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/premium"
	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/service/premiumtest"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// Momentum Premium tax documents over the real routes and launch boundary.

type invoiceRig struct {
	h     *Handler
	svc   *service.Premium
	store *premiumtest.Store
	pays  *premiumtest.Payments
}

func newInvoiceRig(t *testing.T, enabled bool) *invoiceRig {
	t.Helper()
	store, pays := premiumtest.New(), premiumtest.NewPayments()
	svc := service.NewPremium(store, premium.Prices{premium.Plan30d: 14900}).WithPayments(pays, false).
		WithRoles(&premiumtest.Roles{}).WithSupplier(pcSupplier).WithClock(func() time.Time { return pcNow })
	return &invoiceRig{h: New(nil).WithPremium(svc, enabled), svc: svc, store: store, pays: pays}
}

// paid buys and captures a pass for user and answers its invoice id.
func (r *invoiceRig) paid(t *testing.T, user uuid.UUID) uuid.UUID {
	t.Helper()
	w := serveJSON(r.h, http.MethodPost, "/v1/monetization/premium/checkout", user.String(), `{"plan":"premium_30d"}`)
	var co service.PremiumCheckout
	decodeData(t, w, &co)
	if o, err := r.svc.ApplyPremiumPaymentEvent(context.Background(), premium.Event{EventID: uuid.NewString(), EventType: "payment.succeeded",
		IntentID: co.IntentID.String(), PeriodID: co.PassID, PayerID: user, AmountMinor: co.AmountPaise, Currency: "INR"}); err != nil || o != premium.OutcomeActivated {
		t.Fatalf("activate: %s %v", o, err)
	}
	id, err := r.store.PremiumInvoiceIDForPass(context.Background(), co.PassID)
	if err != nil || id == nil {
		t.Fatalf("no invoice: %v", err)
	}
	return *id
}

func TestPremiumInvoiceRoutesAreOwnerOnly(t *testing.T) {
	r := newInvoiceRig(t, true)
	owner, stranger := uuid.New(), uuid.New()
	id := r.paid(t, owner)

	w := serveJSON(r.h, http.MethodGet, "/v1/monetization/premium/invoices/"+id.String(), owner.String(), "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"number":"MP/26-27/000001"`) {
		t.Fatalf("owner: %d %s", w.Code, w.Body.String())
	}
	// Another buyer's document is 404 NOT_FOUND, never 403, on both routes —
	// the same answer as an id that does not exist or does not parse.
	for _, path := range []string{"/v1/monetization/premium/invoices/" + id.String(), "/v1/monetization/premium/invoices/" + id.String() + "/document",
		"/v1/monetization/premium/invoices/" + uuid.NewString(), "/v1/monetization/premium/invoices/not-a-uuid/document"} {
		wantCode(t, path, serveJSON(r.h, http.MethodGet, path, stranger.String(), ""), http.StatusNotFound, "NOT_FOUND")
	}
	w = serveJSON(r.h, http.MethodGet, "/v1/monetization/premium/invoices", stranger.String(), "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"invoices":[]`) {
		t.Fatalf("stranger's list: %d %s", w.Code, w.Body.String())
	}
	w = serveJSON(r.h, http.MethodGet, "/v1/monetization/premium/invoices", owner.String(), "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), id.String()) {
		t.Fatalf("owner's list: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{"/v1/monetization/premium/invoices", "/v1/monetization/premium/invoices/" + id.String(),
		"/v1/monetization/premium/invoices/" + id.String() + "/document"} {
		wantCode(t, "no user "+path, serveJSON(r.h, http.MethodGet, path, "", ""), http.StatusUnauthorized, "UNAUTHORIZED")
	}
}

func TestPremiumInvoiceDocumentIsAnHTMLAttachment(t *testing.T) {
	r := newInvoiceRig(t, true)
	owner := uuid.New()
	id := r.paid(t, owner)
	w := serveJSON(r.h, http.MethodGet, "/v1/monetization/premium/invoices/"+id.String()+"/document", owner.String(), "")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") ||
		w.Header().Get("Content-Disposition") != `attachment; filename="MP_26-27_000001.html"` ||
		w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("document: %d %v", w.Code, w.Header())
	}
	body := w.Body.String()
	for _, want := range []string{"Tax invoice", "MP/26-27/000001", pcSupplier.LegalName, pcSupplier.GSTIN, "998439", "₹175.82",
		"Whether tax is payable on reverse charge basis: No"} {
		if !strings.Contains(body, want) {
			t.Errorf("document lacks %q", want)
		}
	}
	if strings.Contains(strings.ToLower(body), "provisional") {
		t.Fatal("the customer document mentions the provisional rate")
	}
}

func TestPremiumInvoiceRoutesFollowTheSwitch(t *testing.T) {
	r := newInvoiceRig(t, false)
	for _, path := range []string{"/v1/monetization/premium/invoices", "/v1/monetization/premium/invoices/" + uuid.NewString(),
		"/v1/monetization/premium/invoices/" + uuid.NewString() + "/document", premiumRegisterPath + "?from=2026-11-01&to=2026-11-30"} {
		wantCode(t, path, serveJSON(r.h, http.MethodGet, path, uuid.NewString(), ""), http.StatusServiceUnavailable, "PREMIUM_NOT_ENABLED")
	}
}

const premiumRegisterPath = "/v1/monetization/internal/premium/gst-register"

type registerRig struct {
	v      *servicetoken.Verifier
	admin  *servicetoken.Signer
	food   *servicetoken.Signer // registered, over-granted, not a register caller
	scoped *servicetoken.Signer // admin-service key, used without the op
}

func newRegisterRig(t *testing.T) *registerRig {
	t.Helper()
	aPub, aPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	fPub, fPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SERVICE_CALLERS":                     "admin-service,food-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":    "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": aPub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":    OpPremiumGSTRegister + "," + OpChargeAndCredit,
		"SERVICE_CALLER_FOOD_SERVICE_KID":     "f1",
		"SERVICE_CALLER_FOOD_SERVICE_PUBKEY":  fPub,
		"SERVICE_CALLER_FOOD_SERVICE_OPS":     OpPremiumGSTRegister,
	}
	v, err := ServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	mk := func(iss, kid, priv string) *servicetoken.Signer {
		s, err := servicetoken.NewSignerFromBase64(iss, kid, priv)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	a := mk(IssuerAdminService, "a1", aPriv)
	return &registerRig{v: v, admin: a, food: mk(IssuerFoodService, "f1", fPriv), scoped: a}
}

func getRegister(t *testing.T, h *Handler, query, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, premiumRegisterPath+query, nil)
	req.Header.Set("X-Internal-Service-Key", adminTestInternalKey)
	if token != "" {
		req.Header.Set(ServiceAuthHeader, "Bearer "+token)
	}
	w := httptest.NewRecorder()
	routerFor(h).ServeHTTP(w, req)
	return w
}

// The GSTR-1 register needs a signed admin-service token carrying
// monetization:premium-gst-register: the internal key (which the gateway
// stamps on user traffic) or a user id is not enough.
func TestPremiumGSTRegisterNeedsAnAdminServiceToken(t *testing.T) {
	r := newInvoiceRig(t, true)
	rg := newRegisterRig(t)
	r.paid(t, uuid.New())
	r.h.WithInternalKey(adminTestInternalKey).WithServiceAuth(rg.v)
	q := "?from=2026-11-01&to=2026-11-30"

	wantCode(t, "key only", getRegister(t, r.h, q, ""), http.StatusUnauthorized, CodeServiceCredentialRequired)
	w := serveJSON(r.h, http.MethodGet, premiumRegisterPath+q, uuid.NewString(), "", "X-Internal-Service-Key", adminTestInternalKey)
	wantCode(t, "a signed-in user", w, http.StatusUnauthorized, CodeServiceCredentialRequired)
	wantCode(t, "another service", getRegister(t, r.h, q, mint(t, rg.food, AudienceMonetization, []string{OpPremiumGSTRegister})),
		http.StatusForbidden, CodeServiceTokenRejected)
	wantCode(t, "admin without the op", getRegister(t, r.h, q, mint(t, rg.scoped, AudienceMonetization, []string{OpChargeAndCredit})),
		http.StatusForbidden, CodeAdminPermissionScope)

	tok := mint(t, rg.admin, AudienceMonetization, []string{OpPremiumGSTRegister})
	w = getRegister(t, r.h, q, tok)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"number":"MP/26-27/000001"`) ||
		!strings.Contains(w.Body.String(), `"supply_type":"intra_state"`) {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	wantCode(t, "bad range", getRegister(t, r.h, "?from=2026-11-30&to=2026-11-01", tok), http.StatusBadRequest, "BAD_REQUEST")
}

// The register lives under /internal/: api-gateway's internalroutes refuses
// every path with an `internal` segment at the edge (404), whatever token a
// client holds. Pin the shape so a rename cannot move it onto a public path.
func TestPremiumGSTRegisterIsAnInternalPath(t *testing.T) {
	segs := strings.Split(strings.Trim(premiumRegisterPath, "/"), "/")
	found := false
	for _, s := range segs {
		found = found || s == "internal"
	}
	if !found {
		t.Fatalf("%s must carry an internal segment", premiumRegisterPath)
	}
	for _, rule := range premiumRoutes {
		if strings.Contains(rule.pattern, "gst-register") && !strings.Contains(rule.pattern, "/internal/") {
			t.Fatalf("register rule %s is not internal", rule.pattern)
		}
	}
}
