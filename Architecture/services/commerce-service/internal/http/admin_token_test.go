// Admin-service token tests (admin console Wave 2 — MStore). No database:
// admission is proven by reaching a handler that then refuses a malformed
// path id (400), refusal by the gate's own status and code. A handler that
// got past a gate into the nil store would panic, so a clean 4xx is also
// proof nothing was written. Audit-actor and stats checks are in
// admin_token_integration_test.go.
package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const tokenRigKey = "rig-internal-key"

type adminTokenRig struct {
	r        *gin.Engine
	h        *Handler
	v        *servicetoken.Verifier
	admin    *servicetoken.Signer // admin-service, registered for AdminPermissions
	payments *servicetoken.Signer // a registered caller that is not admin-service
	rogue    *servicetoken.Signer // claims to be admin-service, unregistered key
	actor    uuid.UUID
}

func newAdminTokenRig(t *testing.T) *adminTokenRig {
	t.Helper()
	aPub, aPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	pPub, pPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	_, rPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SERVICE_CALLERS":                        "admin-service, payments-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":       "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY":    aPub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":       strings.Join(AdminPermissions, ","),
		"SERVICE_CALLER_PAYMENTS_SERVICE_KID":    "p1",
		"SERVICE_CALLER_PAYMENTS_SERVICE_PUBKEY": pPub,
		// Deliberately over-granted: even with the op registered, a caller
		// other than admin-service must not reach an admin route.
		"SERVICE_CALLER_PAYMENTS_SERVICE_OPS": strings.Join(AdminPermissions, ","),
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
	h, r := tokenEngine(v)
	return &adminTokenRig{
		r: r, h: h, v: v,
		admin:    mk(IssuerAdminService, "a1", aPriv),
		payments: mk("payments-service", "p1", pPriv),
		rogue:    mk(IssuerAdminService, "a1", rPriv),
		actor:    uuid.New(),
	}
}

// tokenEngine is the production route table with the gateway-trust
// middleware installed the way cmd/server installs it, over a nil store.
func tokenEngine(v *servicetoken.Verifier) (*Handler, *gin.Engine) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequireGatewayTrust(tokenRigKey))
	h := New(service.New(postgres.New(nil), nil, "")).WithInternalKey(tokenRigKey).WithServiceVerifier(v)
	h.RegisterRoutes(r)
	return h, r
}

func (rg *adminTokenRig) mint(t *testing.T, s *servicetoken.Signer, aud string, scope []string, actor string) string {
	t.Helper()
	var opts []servicetoken.MintOption
	if actor != "" {
		opts = append(opts, servicetoken.WithActor(actor))
	}
	tok, err := s.Mint(aud, "admin-console", scope, nil, time.Minute, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func bearer(tok string) map[string]string {
	return map[string]string{ServiceAuthHeader: "Bearer " + tok}
}

func tokenErrorCode(w *httptest.ResponseRecorder) string {
	body := w.Body.String()
	for _, code := range []string{CodeAdminTokenRequired, CodeAdminPermissionScope, CodeAdminActorRequired,
		CodeServiceTokenRejected, CodeServiceCredentialRequired, CodeActorRequired} {
		if strings.Contains(body, `"`+code+`"`) {
			return code
		}
	}
	return ""
}

// Approve with a malformed seller id: admitted → the handler's 400.
const approveBad = InternalAdminPrefix + "/sellers/not-a-uuid/approve"

func TestAdminToken_RightScopeAdmittedWithoutTheKey(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceCommerce, []string{PermSellerApprove}, rg.actor.String())
	w := serve(rg.r, http.MethodPost, approveBad, bearer(tok))
	if w.Code != http.StatusBadRequest || tokenErrorCode(w) != "" {
		t.Fatalf("status=%d body=%s, want 400 from the handler (admitted)", w.Code, w.Body.String())
	}
}

func TestAdminToken_Refusals(t *testing.T) {
	rg := newAdminTokenRig(t)
	actor := rg.actor.String()
	scope := []string{PermSellerApprove}
	cases := []struct {
		name     string
		tok      string
		clock    time.Duration
		wantCode string
	}{
		{"wrong audience", rg.mint(t, rg.admin, servicetoken.AudiencePayments, scope, actor), 0, CodeServiceTokenRejected},
		{"dating audience", rg.mint(t, rg.admin, "dating", scope, actor), 0, CodeServiceTokenRejected},
		{"missing act", rg.mint(t, rg.admin, AudienceCommerce, scope, ""), 0, CodeAdminActorRequired},
		{"malformed act", rg.mint(t, rg.admin, AudienceCommerce, scope, "not-a-uuid"), 0, CodeAdminActorRequired},
		{"nil act", rg.mint(t, rg.admin, AudienceCommerce, scope, uuid.Nil.String()), 0, CodeAdminActorRequired},
		{"wrong scope", rg.mint(t, rg.admin, AudienceCommerce, []string{PermSellersRead}, actor), 0, CodeAdminPermissionScope},
		{"expired", rg.mint(t, rg.admin, AudienceCommerce, scope, actor), 2 * time.Minute, CodeServiceTokenRejected},
		{"unknown caller key", rg.mint(t, rg.rogue, AudienceCommerce, scope, actor), 0, CodeServiceTokenRejected},
		{"registered non-admin caller", rg.mint(t, rg.payments, AudienceCommerce, scope, actor), 0, CodeServiceTokenRejected},
		{"garbage", "a.b.c", 0, CodeServiceTokenRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.clock != 0 {
				rg.v.SetClock(func() time.Time { return time.Now().Add(tc.clock) })
				defer rg.v.SetClock(time.Now)
			}
			// The key and a real-looking actor ride along; a request with a
			// token is judged by the token alone.
			hdr := bearer(tc.tok)
			hdr[InternalServiceKeyHeader] = tokenRigKey
			hdr["X-User-Id"] = uuid.NewString()
			w := serve(rg.r, http.MethodPost, approveBad, hdr)
			if w.Code != http.StatusForbidden || tokenErrorCode(w) != tc.wantCode {
				t.Fatalf("status=%d code=%q body=%s, want 403 %s", w.Code, tokenErrorCode(w), w.Body.String(), tc.wantCode)
			}
		})
	}
}

// The edge cannot reach the token-only family with the key the gateway
// stamps plus a forged actor: no token, no entry, whatever the headers say.
func TestAdminToken_InternalFamilyRefusesKeyAndForgedActor(t *testing.T) {
	rg := newAdminTokenRig(t)
	forged := uuid.NewString()
	checked := 0
	for _, ri := range rg.r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		checked++
		hdr := map[string]string{
			InternalServiceKeyHeader: tokenRigKey,
			"X-User-Id":              forged,
			"X-Admin-Id":             forged,
			"X-Scopes":               "superadmin admin",
		}
		w := serve(rg.r, ri.Method, fillParams(ri.Path), hdr)
		if w.Code != http.StatusUnauthorized || tokenErrorCode(w) != CodeAdminTokenRequired {
			t.Fatalf("%s %s: status=%d code=%q, want 401 %s", ri.Method, ri.Path, w.Code, tokenErrorCode(w), CodeAdminTokenRequired)
		}
	}
	if checked != len(adminRoutePerms) {
		t.Fatalf("checked %d internal admin routes, want %d", checked, len(adminRoutePerms))
	}
}

var adminRoutePerms = map[string]string{
	"GET " + InternalAdminPrefix + "/stats":                                               PermStatsRead,
	"GET " + InternalAdminPrefix + "/sellers/queue":                                       PermSellersRead,
	"GET " + InternalAdminPrefix + "/sellers/:sellerId":                                   PermSellersRead,
	"POST " + InternalAdminPrefix + "/sellers/:sellerId/approve":                          PermSellerApprove,
	"POST " + InternalAdminPrefix + "/sellers/:sellerId/reject":                           PermSellerApprove,
	"POST " + InternalAdminPrefix + "/sellers/:sellerId/request-changes":                  PermSellerApprove,
	"POST " + InternalAdminPrefix + "/sellers/:sellerId/suspend":                          PermSellerSuspend,
	"POST " + InternalAdminPrefix + "/sellers/:sellerId/unsuspend":                        PermSellerSuspend,
	"POST " + InternalAdminPrefix + "/sellers/:sellerId/kyc/verify":                       PermKYCVerify,
	"GET " + InternalAdminPrefix + "/products/queue":                                      PermProductsModerate,
	"GET " + InternalAdminPrefix + "/products/:productId/submissions":                     PermProductsModerate,
	"POST " + InternalAdminPrefix + "/products/:productId/approve":                        PermProductsModerate,
	"POST " + InternalAdminPrefix + "/products/:productId/reject":                         PermProductsModerate,
	"POST " + InternalAdminPrefix + "/products/:productId/request-changes":                PermProductsModerate,
	"GET " + InternalAdminPrefix + "/payouts/pending":                                     PermPayoutsRead,
	"POST " + InternalAdminPrefix + "/cod-remittances/:remittanceId/settle":               PermCODSettle,
	"GET " + InternalAdminPrefix + "/attribute-definitions":                               PermCatalogueEdit,
	"POST " + InternalAdminPrefix + "/attribute-definitions":                              PermCatalogueEdit,
	"GET " + InternalAdminPrefix + "/attribute-definitions/:defId":                        PermCatalogueEdit,
	"PATCH " + InternalAdminPrefix + "/attribute-definitions/:defId":                      PermCatalogueEdit,
	"GET " + InternalAdminPrefix + "/attribute-definitions/:defId/impact":                 PermCatalogueEdit,
	"GET " + InternalAdminPrefix + "/attribute-definitions/:defId/enum-values":            PermCatalogueEdit,
	"POST " + InternalAdminPrefix + "/attribute-definitions/:defId/enum-values":           PermCatalogueEdit,
	"PUT " + InternalAdminPrefix + "/attribute-definitions/:defId/enum-values/order":      PermCatalogueEdit,
	"PATCH " + InternalAdminPrefix + "/attribute-definitions/:defId/enum-values/:valueId": PermCatalogueEdit,
	"GET " + InternalAdminPrefix + "/categories/:categoryId/attributes":                   PermCatalogueEdit,
	"PUT " + InternalAdminPrefix + "/categories/:categoryId/attributes":                   PermCatalogueEdit,
	"POST " + InternalAdminPrefix + "/categories":                                         PermCatalogueEdit,
	"PATCH " + InternalAdminPrefix + "/categories/:categoryId":                            PermCatalogueEdit,
	"GET " + InternalAdminPrefix + "/attribute-schema":                                    PermCatalogueEdit,
	"POST " + InternalAdminPrefix + "/attribute-schema/publish":                           PermCatalogueEdit,
	"GET " + InternalAdminPrefix + "/banners":                                             PermBannersEdit,
	"POST " + InternalAdminPrefix + "/banners":                                            PermBannersEdit,
	"PUT " + InternalAdminPrefix + "/banners/:bannerId":                                   PermBannersEdit,
	"DELETE " + InternalAdminPrefix + "/banners/:bannerId":                                PermBannersEdit,
	"GET " + InternalAdminPrefix + "/jobs/dead-letter":                                    PermJobsRead,
	"GET " + InternalAdminPrefix + "/compliance-gaps":                                     PermComplianceRead,
	"POST " + InternalAdminPrefix + "/compliance-gaps/sweep":                              PermComplianceSweep,
}

func fillParams(path string) string {
	id := uuid.NewString()
	return strings.NewReplacer(":sellerId", id, ":productId", id, ":remittanceId", id, ":defId", id,
		":valueId", id, ":categoryId", id, ":bannerId", id).Replace(path)
}

// Every route in the family is declared with its permission, and a token
// holding every OTHER permission is refused by scope.
func TestAdminToken_EveryInternalRouteNeedsItsPermission(t *testing.T) {
	rg := newAdminTokenRig(t)
	seen := 0
	for _, ri := range rg.r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		perm, ok := adminRoutePerms[ri.Method+" "+ri.Path]
		if !ok {
			t.Fatalf("undeclared internal admin route %s %s", ri.Method, ri.Path)
		}
		seen++
		var others []string
		for _, p := range AdminPermissions {
			if p != perm {
				others = append(others, p)
			}
		}
		tok := rg.mint(t, rg.admin, AudienceCommerce, others, rg.actor.String())
		w := serve(rg.r, ri.Method, fillParams(ri.Path), bearer(tok))
		if w.Code != http.StatusForbidden || tokenErrorCode(w) != CodeAdminPermissionScope {
			t.Fatalf("%s %s without %s: status=%d code=%q, want 403 %s", ri.Method, ri.Path, perm, w.Code, tokenErrorCode(w), CodeAdminPermissionScope)
		}
	}
	if seen != len(adminRoutePerms) {
		t.Fatalf("saw %d internal admin routes, want %d", seen, len(adminRoutePerms))
	}
	for _, p := range AdminPermissions {
		found := false
		for _, want := range adminRoutePerms {
			found = found || want == p
		}
		if !found {
			t.Fatalf("permission %s is registered for admin-service but gates no route", p)
		}
	}
}

// The actor a handler records is the token's act; a forged X-User-Id on the
// same request is not visible to it.
func TestAdminToken_ActorIsActNotTheHeader(t *testing.T) {
	rg := newAdminTokenRig(t)
	r := gin.New()
	r.POST("/probe", rg.h.requireAdminToken(PermSellerApprove), func(c *gin.Context) {
		id, ok := requireActor(c)
		if !ok {
			return
		}
		c.String(http.StatusOK, id.String()+"|"+actorID(c).String()+"|"+c.GetHeader("X-User-Id"))
	})
	hdr := bearer(rg.mint(t, rg.admin, AudienceCommerce, []string{PermSellerApprove}, rg.actor.String()))
	hdr["X-User-Id"] = uuid.NewString()
	w := serve(r, http.MethodPost, "/probe", hdr)
	want := rg.actor.String() + "|" + rg.actor.String() + "|"
	if w.Code != http.StatusOK || w.Body.String() != want {
		t.Fatalf("status=%d body=%q, want 200 %q", w.Code, w.Body.String(), want)
	}
}

// The existing key routes are unchanged: key + actor still reaches the
// handler, and a token alone does not open them.
func TestAdminToken_LegacyKeyRoutesUnchanged(t *testing.T) {
	rg := newAdminTokenRig(t)
	legacy := "/v1/commerce/internal/sellers/not-a-uuid/approve"
	w := serve(rg.r, http.MethodPost, legacy, map[string]string{
		InternalServiceKeyHeader: tokenRigKey, "X-User-Id": uuid.NewString(),
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("legacy key route: status=%d body=%s, want 400 (admitted)", w.Code, w.Body.String())
	}
	tok := rg.mint(t, rg.admin, AudienceCommerce, AdminPermissions, rg.actor.String())
	if w := serve(rg.r, http.MethodPost, legacy, bearer(tok)); w.Code != http.StatusUnauthorized {
		t.Fatalf("legacy key route with only a token: status=%d, want 401", w.Code)
	}
}

// The gateway-trust exemption covers the token family only.
func TestAdminToken_GatewayTrustExemptionIsNarrow(t *testing.T) {
	rg := newAdminTokenRig(t)
	for _, p := range []string{
		"/v1/commerce/cart",
		InternalAdminPrefix + "/../../cart",
		InternalAdminPrefix + "/./stats",
		InternalAdminPrefix + "//stats",
		"/v1/commerce/internal/adminx/stats",
	} {
		if w := serve(rg.r, http.MethodGet, p, map[string]string{"X-User-Id": uuid.NewString()}); w.Code != http.StatusUnauthorized ||
			!strings.Contains(w.Body.String(), "did not come through the API gateway") {
			t.Fatalf("%s without the key: status=%d body=%s, want 401 from gateway trust", p, w.Code, w.Body.String())
		}
	}
}

// A deployment without admin-service registered accepts no admin token.
func TestAdminToken_NoVerifierRefuses(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceCommerce, []string{PermSellerApprove}, rg.actor.String())
	_, r := tokenEngine(nil)
	if w := serve(r, http.MethodPost, approveBad, bearer(tok)); w.Code != http.StatusUnauthorized ||
		tokenErrorCode(w) != CodeServiceCredentialRequired {
		t.Fatalf("no verifier: status=%d body=%s, want 401 %s", w.Code, w.Body.String(), CodeServiceCredentialRequired)
	}
}

func TestServiceCallersFromEnv(t *testing.T) {
	pub, _, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if v, err := ServiceCallersFromEnv(get(nil)); v != nil || err != nil {
		t.Fatalf("blank SERVICE_CALLERS: v=%v err=%v, want nil, nil", v, err)
	}
	bad := []map[string]string{
		{"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub, "SERVICE_CALLER_ADMIN_SERVICE_OPS": PermStatsRead},
		{"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_KID": "a1", "SERVICE_CALLER_ADMIN_SERVICE_OPS": PermStatsRead},
		{"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_KID": "a1", "SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub},
		{"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_KID": "a1", "SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": "!!", "SERVICE_CALLER_ADMIN_SERVICE_OPS": PermStatsRead},
		{"SERVICE_CALLERS": " , "},
	}
	for i, m := range bad {
		if _, err := ServiceCallersFromEnv(get(m)); err == nil {
			t.Fatalf("case %d: want a configuration error", i)
		}
	}
}
