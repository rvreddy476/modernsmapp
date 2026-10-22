package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/servicetoken"
)

// Security sweep #17: POST /v1/monetization/internal/charge-and-credit moved
// money between two arbitrary wallets on the shared internal key alone. It
// now needs a food-service token carrying monetization:charge-and-credit.
//
// Over the real RegisterRoutes wiring with writes on and no service behind
// the handler: an admitted request is proven by the handler's OWN validation
// answer (an empty body is 400 INVALID_REQUEST before any ledger call); a
// refusal by the gate's status and code.

const chargePath = "/v1/monetization/internal/charge-and-credit"

type chargeRig struct {
	v     *servicetoken.Verifier
	food  *servicetoken.Signer // food-service, registered for the op
	other *servicetoken.Signer // a registered caller that is not food-service, over-granted
	rogue *servicetoken.Signer // claims to be food-service, unregistered key
}

func newChargeRig(t *testing.T) *chargeRig {
	t.Helper()
	fPub, fPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	oPub, oPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	_, rPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SERVICE_CALLERS":                        "food-service,commerce-service",
		"SERVICE_CALLER_FOOD_SERVICE_KID":        "f1",
		"SERVICE_CALLER_FOOD_SERVICE_PUBKEY":     fPub,
		"SERVICE_CALLER_FOOD_SERVICE_OPS":        OpChargeAndCredit,
		"SERVICE_CALLER_COMMERCE_SERVICE_KID":    "c1",
		"SERVICE_CALLER_COMMERCE_SERVICE_PUBKEY": oPub,
		// Deliberately granted the op: a caller the route does not name
		// must still be refused.
		"SERVICE_CALLER_COMMERCE_SERVICE_OPS": OpChargeAndCredit,
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
	return &chargeRig{
		v:     v,
		food:  mk(IssuerFoodService, "f1", fPriv),
		other: mk("commerce-service", "c1", oPriv),
		rogue: mk(IssuerFoodService, "f1", rPriv),
	}
}

func mint(t *testing.T, s *servicetoken.Signer, aud string, scope []string) string {
	t.Helper()
	tok, err := s.Mint(aud, "charge", scope, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func postCharge(t *testing.T, h *Handler, token string, withKey bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, chargePath, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	if withKey {
		req.Header.Set("X-Internal-Service-Key", adminTestInternalKey)
	}
	if token != "" {
		req.Header.Set(ServiceAuthHeader, "Bearer "+token)
	}
	w := httptest.NewRecorder()
	routerFor(h).ServeHTTP(w, req)
	return w
}

func wantCode(t *testing.T, what string, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status || !strings.Contains(w.Body.String(), `"`+code+`"`) {
		t.Fatalf("%s: %d %s, want %d %s", what, w.Code, w.Body.String(), status, code)
	}
}

func TestChargeAndCreditNeedsAFoodServiceToken(t *testing.T) {
	rg := newChargeRig(t)
	h := New(nil).WithInternalKey(adminTestInternalKey).WithServiceAuth(rg.v).WithWritesEnabled(true)

	// The internal key alone — the whole credential before the sweep.
	wantCode(t, "key, no token", postCharge(t, h, "", true), http.StatusUnauthorized, CodeServiceCredentialRequired)

	// Wrong issuer, even one registered for the op.
	wantCode(t, "another service", postCharge(t, h, mint(t, rg.other, AudienceMonetization, []string{OpChargeAndCredit}), true),
		http.StatusForbidden, CodeServiceTokenRejected)
	// The op missing from the token's scope.
	wantCode(t, "scope without the op", postCharge(t, h, mint(t, rg.food, AudienceMonetization, []string{"monetization:stats.read"}), true),
		http.StatusForbidden, CodeAdminPermissionScope)
	// A key that is not the registered one.
	wantCode(t, "forged food-service token", postCharge(t, h, mint(t, rg.rogue, AudienceMonetization, []string{OpChargeAndCredit}), true),
		http.StatusForbidden, CodeServiceTokenRejected)
	// A token minted for another audience.
	wantCode(t, "payments audience", postCharge(t, h, mint(t, rg.food, servicetoken.AudiencePayments, []string{OpChargeAndCredit}), true),
		http.StatusForbidden, CodeServiceTokenRejected)
	wantCode(t, "garbage token", postCharge(t, h, "not.a.token", true), http.StatusForbidden, CodeServiceTokenRejected)

	// The right token reaches the handler, which answers its own 400 for
	// the empty body before any ledger call.
	wantCode(t, "food-service token", postCharge(t, h, mint(t, rg.food, AudienceMonetization, []string{OpChargeAndCredit}), true),
		http.StatusBadRequest, "INVALID_REQUEST")

	// The token does not replace the internal key in front of it.
	if w := postCharge(t, h, mint(t, rg.food, AudienceMonetization, []string{OpChargeAndCredit}), false); w.Code != http.StatusUnauthorized {
		t.Fatalf("token without the internal key: %d %s, want 401", w.Code, w.Body.String())
	}
}

// A deployment with no SERVICE_CALLERS accepts no token at all.
func TestChargeAndCreditWithoutAVerifierRefusesEveryToken(t *testing.T) {
	rg := newChargeRig(t)
	h := New(nil).WithInternalKey(adminTestInternalKey).WithWritesEnabled(true)
	wantCode(t, "no verifier", postCharge(t, h, mint(t, rg.food, AudienceMonetization, []string{OpChargeAndCredit}), true),
		http.StatusUnauthorized, CodeServiceCredentialRequired)
}
