package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// The buyer's invoice e-mail reads its facts through an internal route that
// only a notification-service token reaches, and only for the event's buyer.

const premiumInvoiceMailPath = "/v1/monetization/internal/premium/invoices/"

type invoiceMailRig struct {
	v     *servicetoken.Verifier
	notif *servicetoken.Signer
	admin *servicetoken.Signer // registered and granted the op, but not a mail caller
}

func newInvoiceMailRig(t *testing.T) *invoiceMailRig {
	t.Helper()
	nPub, nPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	aPub, aPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SERVICE_CALLERS":                            "notification-service,admin-service",
		"SERVICE_CALLER_NOTIFICATION_SERVICE_KID":    "n1",
		"SERVICE_CALLER_NOTIFICATION_SERVICE_PUBKEY": nPub,
		"SERVICE_CALLER_NOTIFICATION_SERVICE_OPS":    OpPremiumInvoiceMail + "," + OpPremiumGSTRegister,
		"SERVICE_CALLER_ADMIN_SERVICE_KID":           "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY":        aPub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":           OpPremiumInvoiceMail,
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
	return &invoiceMailRig{v: v, notif: mk(IssuerNotificationService, "n1", nPriv), admin: mk(IssuerAdminService, "a1", aPriv)}
}

func getInvoiceMail(h *Handler, invoiceID, query, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, premiumInvoiceMailPath+invoiceID+query, nil)
	req.Header.Set("X-Internal-Service-Key", adminTestInternalKey)
	if token != "" {
		req.Header.Set(ServiceAuthHeader, "Bearer "+token)
	}
	w := httptest.NewRecorder()
	routerFor(h).ServeHTTP(w, req)
	return w
}

func TestPremiumInvoiceMailReadIsTheBuyersOnly(t *testing.T) {
	r := newInvoiceRig(t, true)
	rg := newInvoiceMailRig(t)
	owner, stranger := uuid.New(), uuid.New()
	id := r.paid(t, owner).String()
	r.h.WithInternalKey(adminTestInternalKey).WithServiceAuth(rg.v)
	tok := mint(t, rg.notif, AudienceMonetization, []string{OpPremiumInvoiceMail})

	w := getInvoiceMail(r.h, id, "?user_id="+owner.String(), tok)
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, `"number":"MP/26-27/000001"`) || !strings.Contains(body, `"kind":"invoice"`) ||
		!strings.Contains(body, `"user_id":"`+owner.String()+`"`) || !strings.Contains(body, `"description":"Momentum Premium — 30 days"`) ||
		!strings.Contains(body, `"total_paise":17582`) {
		t.Fatalf("owner: %d %s", w.Code, body)
	}
	for _, leak := range []string{"buyer_name", "supplier", "gstin", "cgst"} {
		if strings.Contains(strings.ToLower(body), leak) {
			t.Errorf("the mail read carries %q: %s", leak, body)
		}
	}
	// Another user's id, or none, is 404 — the e-mail of user B can never be
	// built from user A's invoice.
	for name, q := range map[string]string{"stranger": "?user_id=" + stranger.String(), "no user": "", "bad user": "?user_id=nope",
		"nil user": "?user_id=" + uuid.Nil.String()} {
		wantCode(t, name, getInvoiceMail(r.h, id, q, tok), http.StatusNotFound, "NOT_FOUND")
	}
	wantCode(t, "unknown invoice", getInvoiceMail(r.h, uuid.NewString(), "?user_id="+owner.String(), tok), http.StatusNotFound, "NOT_FOUND")

	// Credentials: the key alone, a signed-in user, another service granted
	// the op, and the right service without it are all refused.
	q := "?user_id=" + owner.String()
	wantCode(t, "key only", getInvoiceMail(r.h, id, q, ""), http.StatusUnauthorized, CodeServiceCredentialRequired)
	wantCode(t, "a signed-in user", serveJSON(r.h, http.MethodGet, premiumInvoiceMailPath+id+q, owner.String(), "",
		"X-Internal-Service-Key", adminTestInternalKey), http.StatusUnauthorized, CodeServiceCredentialRequired)
	wantCode(t, "another service", getInvoiceMail(r.h, id, q, mint(t, rg.admin, AudienceMonetization, []string{OpPremiumInvoiceMail})),
		http.StatusForbidden, CodeServiceTokenRejected)
	wantCode(t, "without the op", getInvoiceMail(r.h, id, q, mint(t, rg.notif, AudienceMonetization, []string{OpPremiumGSTRegister})),
		http.StatusForbidden, CodeAdminPermissionScope)
}

func TestPremiumInvoiceMailReadFollowsTheSwitchAndStaysInternal(t *testing.T) {
	r := newInvoiceRig(t, false)
	rg := newInvoiceMailRig(t)
	r.h.WithInternalKey(adminTestInternalKey).WithServiceAuth(rg.v)
	w := getInvoiceMail(r.h, uuid.NewString(), "?user_id="+uuid.NewString(), mint(t, rg.notif, AudienceMonetization, []string{OpPremiumInvoiceMail}))
	wantCode(t, "premium off", w, http.StatusServiceUnavailable, "PREMIUM_NOT_ENABLED")
	if !strings.Contains(premiumInvoiceMailPath, "/internal/") {
		t.Fatal("the mail read must stay on an /internal/ path the gateway refuses")
	}
}
