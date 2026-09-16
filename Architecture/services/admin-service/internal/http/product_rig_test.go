package http

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// foodAll and trustAll are the products' AdminPermissions lists.
var (
	foodAll = []string{
		permFoodStatsRead, permFoodRestaurantApprove, permFoodRestaurantSuspend, permFoodRiderApprove, permFoodRiderSuspend,
		permFoodDocumentsReview, permFoodPayoutAccountsRead, permFoodOrdersRead, permFoodOrdersCancel, permFoodRefundIssue,
		permFoodRefundsRead, permFoodSettlementRead, permFoodSettlementGenerate, permFoodSettlementMarkPaid, permFoodMenuModerate,
		permFoodReviewsModerate, permFoodTicketsAct, permFoodCouponsManage, permFoodServiceAreasManage, permFoodReportsRead,
		permFoodFraudRead, permFoodAuditRead,
	}
	trustAll = []string{
		permTrustStatsRead, permTrustReportsRead, permTrustReportsAct, permTrustAppealsRead, permTrustAppealsAct,
		permTrustGrievancesRead, permTrustGrievancesAct, permTrustStrikesRead, permTrustStrikesManage,
		permTrustVerificationReview, permTrustMediaLabelsRead, permTrustKeywordFiltersRead, permTrustAuditRead,
	}
	monAll = []string{
		permMonStatsRead, permMonFraudReview, permMonWalletFreeze, permMonWalletUnfreeze, permMonWalletRebuild,
		permMonFundRead, permMonFundRates, permMonCreatorsSuspend, permMonFundSettle, permMonFundReverse,
		permMonFundBudget, permMonDisputesRead, permMonDisputesAct, permMonRefundIssue, permMonPayoutsRead,
		permMonAuditRead,
	}
	payAll = []string{
		permPayStatsRead, permPayRefundsRead, permPayRefundIssue, permPayIntentsRead, permPayReconciliationRead,
		permPayApplicationsRead, permPayApplicationsManage, permPayAuditRead,
	}
)

// productHit is one call a stub product saw, with the token verified by a
// verifier for that product's audience holding only admin-service's public key.
type productHit struct {
	aud, method, path, query, body string
	idempotencyKey, requestID      string
	userHdr, keyHdr                string
	verified                       *servicetoken.Verified
	verifyErr                      error
}

type productsRig struct {
	r       *gin.Engine
	gate    *Gate
	rec     *fakeRecorder
	perms   *fakePerms
	holders *fakeHolders
	store   *memStore

	mu      sync.Mutex
	hits    []productHit
	respond map[string]func(w http.ResponseWriter, r *http.Request) // "METHOD /path"
}

func newProductsRig(t *testing.T, withKey bool, thresholdPaise int64) *productsRig {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rg := &productsRig{
		rec: &fakeRecorder{}, perms: &fakePerms{byUser: map[string]adminauth.Permissions{}},
		holders: &fakeHolders{}, store: newMemStore(), respond: map[string]func(http.ResponseWriter, *http.Request){},
	}
	stub := func(aud string, perms []string) string {
		v := servicetoken.NewVerifier(aud)
		if err := v.RegisterBase64("admin-service", "a1", pub, perms, nil); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			verified, verr := v.Verify(strings.TrimPrefix(r.Header.Get("X-Service-Authorization"), "Bearer "), "", "")
			rg.mu.Lock()
			rg.hits = append(rg.hits, productHit{
				aud: aud, method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: string(b),
				idempotencyKey: r.Header.Get("Idempotency-Key"), requestID: r.Header.Get("X-Request-Id"),
				userHdr: r.Header.Get("X-User-Id"), keyHdr: r.Header.Get("X-Internal-Service-Key"),
				verified: verified, verifyErr: verr,
			})
			custom := rg.respond[r.Method+" "+r.URL.Path]
			rg.mu.Unlock()
			if verr != nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if custom != nil {
				custom(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	foodURL, commerceURL, trustURL := stub("food", foodAll), stub("commerce", commerceAll), stub("trust_safety", trustAll)
	monURL, payURL := stub("monetization", monAll), stub("payments", payAll)

	var signer *servicetoken.Signer
	if withKey {
		signer, err = servicetoken.NewSignerFromBase64("admin-service", "a1", priv)
		if err != nil {
			t.Fatal(err)
		}
	}
	gin.SetMode(gin.TestMode)
	rg.r = gin.New()
	rg.r.Use(middleware.RequestID())
	rg.gate = NewGate(rg.perms, rg.rec, true)
	h := New(&stubAdminService{}, rg.gate, approvals.NewService(rg.store, rg.holders))
	h.WithFood(service.NewFoodClient(foodURL, signer), thresholdPaise).
		WithCommerce(service.NewCommerceClient(commerceURL, signer)).
		WithTrustSafety(service.NewTrustSafetyClient(trustURL, signer)).
		WithMonetization(service.NewMonetizationClient(monURL, signer)).
		WithPayments(service.NewPaymentsClient(payURL, signer))
	if err := h.RegisterAllRoutes(rg.r); err != nil {
		t.Fatalf("route table refused: %v", err)
	}
	return rg
}

func (rg *productsRig) do(method, path, body, actor string, stepUp bool, opts ...reqOpt) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "req-product-1")
	req.Header.Set("X-User-Id", actor)
	req.Header.Set("X-Admin-MFA", "true")
	if stepUp {
		req.Header.Set("X-Step-Up-At", strconv.FormatInt(time.Now().Unix(), 10))
	}
	for _, o := range opts {
		o(req)
	}
	w := httptest.NewRecorder()
	rg.r.ServeHTTP(w, req)
	return w
}

func withIdempotency(key string) reqOpt {
	return func(r *http.Request) { r.Header.Set("Idempotency-Key", key) }
}

func (rg *productsRig) takeHits() []productHit {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	out := rg.hits
	rg.hits = nil
	return out
}

func (rg *productsRig) takeAudit() []postgres.AdminAuditEntry {
	rg.rec.mu.Lock()
	defer rg.rec.mu.Unlock()
	out := rg.rec.entries
	rg.rec.entries = nil
	return out
}

func (rg *productsRig) on(method, path string, fn func(w http.ResponseWriter, r *http.Request)) {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	rg.respond[method+" "+path] = fn
}

// fill replaces every :param in a route path with a fresh uuid, returning the
// filled path and the values by name.
func fill(path string) (string, map[string]string) {
	vals := map[string]string{}
	segs := strings.Split(path, "/")
	for i, s := range segs {
		if strings.HasPrefix(s, ":") {
			v := uuid.NewString()
			vals[s[1:]] = v
			segs[i] = v
		}
	}
	return strings.Join(segs, "/"), vals
}

// routeTableCase checks one product route: an admin holding every other
// permission of the app is refused before the product is called and audited
// as denied; an admin holding exactly the permission is forwarded with a
// token for the product's audience, scoped to exactly that permission, acting
// as that admin, and audited once.
func routeTableCase(t *testing.T, rg *productsRig, appPrefix, productPrefix, aud, app string, all []string, rt productRoute,
	body string, opts ...reqOpt) {
	t.Helper()
	path, _ := fill(rt.path)
	excluded := map[string]bool{rt.permission: true}
	for _, a := range rt.alternatives {
		excluded[a] = true
	}
	other := uuid.NewString()
	for _, p := range all {
		if !excluded[p] {
			rg.perms.grant(other, p)
		}
	}
	w := rg.do(rt.method, appPrefix+path, body, other, true, opts...)
	if w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
		t.Fatalf("%s without %s: status=%d body=%s, want 403 %s", rt.operation, rt.permission, w.Code, w.Body.String(), CodePermissionDenied)
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("%s: a refused call reached the product: %+v", rt.operation, hits)
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeDenied || a[0].Actor != other || a[0].App != app {
		t.Fatalf("%s refusal audit = %+v", rt.operation, a)
	}

	actor := uuid.NewString()
	rg.perms.grant(actor, rt.permission)
	w = rg.do(rt.method, appPrefix+path, body, actor, true, opts...)
	if w.Code < 200 || w.Code > 299 {
		t.Fatalf("%s with %s: status=%d body=%s", rt.operation, rt.permission, w.Code, w.Body.String())
	}
	hits := rg.takeHits()
	if len(hits) != 1 {
		t.Fatalf("%s: product hits = %d, want 1: %+v", rt.operation, len(hits), hits)
	}
	h := hits[0]
	if h.aud != aud || h.verifyErr != nil {
		t.Fatalf("%s: reached %q, verify err %v", rt.operation, h.aud, h.verifyErr)
	}
	v := h.verified
	if v.Issuer != "admin-service" || len(v.Scope) != 1 || v.Scope[0] != rt.permission || v.Actor != actor {
		t.Fatalf("%s: token iss=%q scope=%v act=%q, want admin-service [%s] %s", rt.operation, v.Issuer, v.Scope, v.Actor, rt.permission, actor)
	}
	upstream := rt.upstream
	if upstream == "" {
		upstream = rt.path
	}
	if !strings.HasPrefix(h.path, productPrefix+"/") || !pathMatches(productPrefix+upstream, h.path) {
		t.Fatalf("%s: product path %q, want %s%s", rt.operation, h.path, productPrefix, upstream)
	}
	if h.userHdr != "" || h.keyHdr != "" || h.requestID != "req-product-1" {
		t.Fatalf("%s: X-User-Id=%q key sent=%v request id=%q", rt.operation, h.userHdr, h.keyHdr != "", h.requestID)
	}
	a := rg.takeAudit()
	if len(a) != 1 || a[0].Actor != actor || a[0].App != app || a[0].Operation != rt.operation ||
		a[0].Outcome != postgres.AuditOutcomeSuccess || a[0].RequestID != "req-product-1" {
		t.Fatalf("%s: audit = %+v, want 1 success row", rt.operation, a)
	}
	if rt.targetType != "" && a[0].TargetType != rt.targetType {
		t.Fatalf("%s: audit target type %q, want %q", rt.operation, a[0].TargetType, rt.targetType)
	}
}

// pathMatches compares a gin template with a concrete path.
func pathMatches(template, path string) bool {
	ts, ps := strings.Split(template, "/"), strings.Split(path, "/")
	if len(ts) != len(ps) {
		return false
	}
	for i := range ts {
		if !strings.HasPrefix(ts[i], ":") && ts[i] != ps[i] {
			return false
		}
	}
	return true
}

func stepUpCase(t *testing.T, rg *productsRig, appPrefix string, all []string, rt productRoute, body string, wantStepUp bool, opts ...reqOpt) {
	t.Helper()
	path, _ := fill(rt.path)
	actor := uuid.NewString()
	rg.perms.grant(actor, all...)
	w := rg.do(rt.method, appPrefix+path, body, actor, false, opts...)
	hits := rg.takeHits()
	rg.takeAudit()
	if wantStepUp {
		if w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeStepUpRequired) {
			t.Fatalf("%s without step-up: status=%d body=%s, want 403 %s", rt.operation, w.Code, w.Body.String(), adminauth.CodeStepUpRequired)
		}
		if len(hits) != 0 {
			t.Fatalf("%s reached the product without step-up", rt.operation)
		}
		return
	}
	if w.Code < 200 || w.Code > 299 {
		t.Fatalf("%s needs no step-up: status=%d body=%s", rt.operation, w.Code, w.Body.String())
	}
}

func decodeApproval(t *testing.T, w *httptest.ResponseRecorder) ApprovalView {
	t.Helper()
	var env struct {
		Data struct {
			Approval ApprovalView `json:"approval"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || env.Data.Approval.ID == "" {
		t.Fatalf("no approval in %d %s (%v)", w.Code, w.Body.String(), err)
	}
	return env.Data.Approval
}
