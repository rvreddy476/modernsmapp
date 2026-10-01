package http

import (
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The two MStore money-policy route families: bank offers on payments
// (/v1/admin/payments/offers) and coupons on commerce (/v1/admin/commerce/coupons).

// couponOfferRoute is one of the new routes with where it lands.
type couponOfferRoute struct {
	rt                    productRoute
	appPrefix, prodPrefix string
	aud, app              string
	all                   []string
}

func couponOfferRoutes(t *testing.T) []couponOfferRoute {
	t.Helper()
	var out []couponOfferRoute
	for _, rt := range PaymentsOfferRoutes {
		out = append(out, couponOfferRoute{rt, payPrefix, service.PaymentsAdminPrefix, "payments", "payments", payAll})
	}
	for _, rt := range CommerceRoutes {
		if rt.permission == permCouponsManage {
			out = append(out, couponOfferRoute{rt, commercePrefix, service.CommerceAdminPrefix, "commerce", "commerce", commerceAll})
		}
	}
	if len(out) != 6 {
		t.Fatalf("coupon/offer routes = %d, want 6 (offers GET/POST/PATCH, coupons GET/POST/PATCH)", len(out))
	}
	return out
}

const couponOfferBody = `{"code":"DIWALI10","discount_type":"percentage","discount_value":1000,"max_discount_minor":20000,"reason":"festival","nested":{"k":[1,2.50,"x"]}}`

func couponOfferCase(rt productRoute) string {
	if rt.method == http.MethodGet {
		return ""
	}
	return couponOfferBody
}

// The declarations themselves: permission, step-up on writes only, no
// two-person, the operations and targets the audit trail names.
func TestCouponsOffers_Declarations(t *testing.T) {
	want := map[string]struct {
		method, path, perm, target string
		stepUp                     bool
	}{
		opPayOffersList:  {http.MethodGet, "/offers", permPayOffersManage, "", false},
		opPayOfferCreate: {http.MethodPost, "/offers", permPayOffersManage, "", true},
		opPayOfferUpdate: {http.MethodPatch, "/offers/:offerId", permPayOffersManage, "payment_offer", true},
		opCouponsList:    {http.MethodGet, "/coupons", permCouponsManage, "", false},
		opCouponCreate:   {http.MethodPost, "/coupons", permCouponsManage, "", true},
		opCouponUpdate:   {http.MethodPatch, "/coupons/:couponId", permCouponsManage, "coupon", true},
	}
	if permPayOffersManage != "payments:offers.manage" || permCouponsManage != "commerce:coupons.manage" {
		t.Fatalf("permissions %q %q", permPayOffersManage, permCouponsManage)
	}
	for _, r := range couponOfferRoutes(t) {
		w, ok := want[r.rt.operation]
		if !ok {
			t.Fatalf("unexpected route %s", r.rt.operation)
		}
		if r.rt.method != w.method || r.rt.path != w.path || r.rt.permission != w.perm || r.rt.targetType != w.target ||
			r.rt.stepUp != w.stepUp || r.rt.twoPerson || r.rt.mayTwoPerson || r.rt.admitsHeldAs || r.rt.decide != nil {
			t.Fatalf("%s declared %+v", r.rt.operation, r.rt)
		}
		if !adminauth.KnownPermission(r.rt.permission) {
			t.Fatalf("%s is not in the catalogue mirror", r.rt.permission)
		}
	}
	rg := newProductsRig(t, true)
	for _, k := range []string{"GET " + payPrefix + "/offers", "POST " + payPrefix + "/offers", "PATCH " + payPrefix + "/offers/:offerId",
		"GET " + commercePrefix + "/coupons", "POST " + commercePrefix + "/coupons", "PATCH " + commercePrefix + "/coupons/:couponId"} {
		m, p, _ := strings.Cut(k, " ")
		req, ok := rg.gate.Requirement(m, p)
		if !ok {
			t.Fatalf("%s not registered", k)
		}
		if req.AdmitsHeldAs || req.Decide != nil {
			t.Fatalf("%s is confinable: %+v", k, req)
		}
	}
}

// Permission denied for an admin holding everything else of the app; the
// holder is forwarded with a token scoped to exactly the permission, acting
// as that admin, to the product's admin family, audited once.
func TestCouponsOffers_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newProductsRig(t, true)
	for _, r := range couponOfferRoutes(t) {
		t.Run(r.rt.operation, func(t *testing.T) {
			routeTableCase(t, rg, r.appPrefix, r.prodPrefix, r.aud, r.app, r.all, r.rt, couponOfferCase(r.rt))
		})
	}
}

// Step-up is required on create and edit and never on the list.
func TestCouponsOffers_StepUpOnWritesNotReads(t *testing.T) {
	rg := newProductsRig(t, true)
	for _, r := range couponOfferRoutes(t) {
		stepUpCase(t, rg, r.appPrefix, r.all, r.rt, couponOfferCase(r.rt), r.rt.method != http.MethodGet)
		if r.rt.method != http.MethodGet {
			// A stale step-up is refused too, before the product.
			actor := uuid.NewString()
			rg.perms.grant(actor, r.rt.permission)
			path, _ := fill(r.rt.path)
			w := rg.do(r.rt.method, r.appPrefix+path, couponOfferBody, actor, true, stepUpAgo(adminauth.StepUpWindow+60e9))
			if !hasCode(w, adminauth.CodeStepUpRequired) || len(rg.takeHits()) != 0 {
				t.Fatalf("%s with a stale step-up: %d %s", r.rt.operation, w.Code, w.Body.String())
			}
			if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeDenied || a[0].Operation != r.rt.operation {
				t.Fatalf("%s stale step-up audit %+v", r.rt.operation, a)
			}
		}
	}
}

// Bank offers are not confinable: an MStore-scoped admin holding the
// confined payments view of mstore, or any other payments permission, is
// refused before payments.
func TestPaymentsOffers_NotReachableByAConfinedOrOtherPaymentsAdmin(t *testing.T) {
	rg := newProductsRig(t, true)
	confined := uuid.NewString()
	rg.perms.grant(confined, confinedPaymentsPermission("commerce", permPayOffersManage), confinedPaymentsPermission("commerce", permPayRefundIssue))
	other := uuid.NewString()
	rg.perms.grant(other, permPayApplicationsManage, permPayRefundIssue, permCouponsManage)
	for _, actor := range []string{confined, other} {
		for _, rt := range PaymentsOfferRoutes {
			path, _ := fill(rt.path)
			w := rg.do(rt.method, payPrefix+path+"?application_id=mstore", couponOfferCase(rt), actor, true)
			if w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
				t.Fatalf("%s by %s: %d %s", rt.operation, actor, w.Code, w.Body.String())
			}
		}
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("refused calls reached payments: %+v", hits)
	}
}

// One audit row per write: the operation, the app, the target (the id for an
// edit), the reason from the body, success, the product's status.
func TestCouponsOffers_OneAuditRowPerWrite(t *testing.T) {
	rg := newProductsRig(t, true)
	const id = "7c0e9a52-3a4b-4c55-9d0e-2f1a6b7c8d9e"
	for _, c := range []struct {
		method, path, perm, app, op, wantType, wantID string
	}{
		{http.MethodPost, payPrefix + "/offers", permPayOffersManage, "payments", opPayOfferCreate, "route", payPrefix + "/offers"},
		{http.MethodPatch, payPrefix + "/offers/" + id, permPayOffersManage, "payments", opPayOfferUpdate, "payment_offer", id},
		{http.MethodPost, commercePrefix + "/coupons", permCouponsManage, "commerce", opCouponCreate, "route", commercePrefix + "/coupons"},
		{http.MethodPatch, commercePrefix + "/coupons/" + id, permCouponsManage, "commerce", opCouponUpdate, "coupon", id},
	} {
		actor := uuid.NewString()
		rg.perms.grant(actor, c.perm)
		w := rg.do(c.method, c.path, couponOfferBody, actor, true)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", c.op, w.Code, w.Body.String())
		}
		if hits := rg.takeHits(); len(hits) != 1 {
			t.Fatalf("%s: %d product calls, want 1", c.op, len(hits))
		}
		a := rg.takeAudit()
		if len(a) != 1 {
			t.Fatalf("%s: %d audit rows, want 1: %+v", c.op, len(a), a)
		}
		e := a[0]
		if e.Actor != actor || e.App != c.app || e.Operation != c.op || e.TargetType != c.wantType || e.TargetID != c.wantID ||
			e.Reason != "festival" || e.Outcome != postgres.AuditOutcomeSuccess || e.StatusCode != http.StatusOK {
			t.Fatalf("%s: audit %+v, want target %s/%s", c.op, e, c.wantType, c.wantID)
		}
	}
}

// The console's body reaches the product byte for byte, its query is passed
// through, and the product's answer (meta included) comes back unchanged.
func TestCouponsOffers_BodyQueryAndAnswerForwardedUnchanged(t *testing.T) {
	rg := newProductsRig(t, true)
	const list = `{"data":[{"id":"c1","code":"DIWALI10","funded_by":"platform"}],"meta":{"platform_coupons_enabled":false,"total":1}}`
	rg.on(http.MethodGet, service.CommerceAdminPrefix+"/coupons", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(list))
	})
	actor := uuid.NewString()
	rg.perms.grant(actor, permCouponsManage)
	w := rg.do(http.MethodGet, commercePrefix+"/coupons?funded_by=seller&limit=50", "", actor, false)
	hits := rg.takeHits()
	if w.Code != http.StatusOK || w.Body.String() != list || len(hits) != 1 || hits[0].query != "funded_by=seller&limit=50" || hits[0].body != "" {
		t.Fatalf("coupon list: %d %s hits=%+v", w.Code, w.Body.String(), hits)
	}
	rg.takeAudit()

	for _, r := range couponOfferRoutes(t) {
		if r.rt.method == http.MethodGet {
			continue
		}
		actor := uuid.NewString()
		rg.perms.grant(actor, r.rt.permission)
		path, _ := fill(r.rt.path)
		w := rg.do(r.rt.method, r.appPrefix+path+"?dry_run=1", couponOfferBody, actor, true, withIdempotency("idem-1"))
		hits := rg.takeHits()
		rg.takeAudit()
		if w.Code != http.StatusOK || len(hits) != 1 {
			t.Fatalf("%s: %d hits=%d", r.rt.operation, w.Code, len(hits))
		}
		h := hits[0]
		if h.body != couponOfferBody || h.method != r.rt.method || h.query != "dry_run=1" || h.path != r.prodPrefix+path || h.idempotencyKey != "idem-1" {
			t.Fatalf("%s: forwarded %+v", r.rt.operation, h)
		}
	}
}

// Product errors come back as the product answered them and are audited as
// failures with the product's status; a product that never answers is a 502
// with status 0 on the row; no signing key is a 503; malformed JSON and an
// unsafe id never reach the product.
func TestCouponsOffers_UpstreamErrorsMapped(t *testing.T) {
	rg := newProductsRig(t, true)
	const conflict = `{"error":{"code":"COUPON_CODE_TAKEN","message":"code exists"}}`
	for _, r := range couponOfferRoutes(t) {
		path, _ := fill(r.rt.path)
		actor := uuid.NewString()
		rg.perms.grant(actor, r.rt.permission)

		// 422 / 409 / 404 pass through.
		for _, status := range []int{http.StatusUnprocessableEntity, http.StatusConflict, http.StatusNotFound} {
			rg.on(r.rt.method, r.prodPrefix+path, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(conflict))
			})
			w := rg.do(r.rt.method, r.appPrefix+path, couponOfferCase(r.rt), actor, true)
			rg.takeHits()
			if w.Code != status || w.Body.String() != conflict {
				t.Fatalf("%s %d: got %d %s", r.rt.operation, status, w.Code, w.Body.String())
			}
			if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure || a[0].StatusCode != status {
				t.Fatalf("%s %d audit %+v", r.rt.operation, status, a)
			}
		}

		// The product drops the connection.
		rg.on(r.rt.method, r.prodPrefix+path, func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		})
		w := rg.do(r.rt.method, r.appPrefix+path, couponOfferCase(r.rt), actor, true)
		rg.takeHits()
		if w.Code != http.StatusBadGateway || !hasCode(w, "UPSTREAM_ERROR") || strings.Contains(w.Body.String(), "internal") {
			t.Fatalf("%s transport error: %d %s", r.rt.operation, w.Code, w.Body.String())
		}
		if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure || a[0].StatusCode != 0 {
			t.Fatalf("%s transport error audit %+v", r.rt.operation, a)
		}

		if r.rt.method != http.MethodGet {
			w := rg.do(r.rt.method, r.appPrefix+path, `{"code":`, actor, true)
			if w.Code != http.StatusBadRequest || !hasCode(w, CodeInvalidBody) || len(rg.takeHits()) != 0 {
				t.Fatalf("%s malformed body: %d %s", r.rt.operation, w.Code, w.Body.String())
			}
			rg.takeAudit()
		}
		if r.rt.method == http.MethodPatch {
			bad := strings.TrimSuffix(path, path[strings.LastIndex(path, "/")+1:]) + "a%2Fb"
			w := rg.do(r.rt.method, r.appPrefix+bad, couponOfferBody, actor, true)
			if w.Code < 400 || len(rg.takeHits()) != 0 {
				t.Fatalf("%s unsafe id: %d %s", r.rt.operation, w.Code, w.Body.String())
			}
			rg.takeAudit()
		}
	}

	off := newProductsRig(t, false)
	for _, r := range couponOfferRoutes(t) {
		actor := uuid.NewString()
		off.perms.grant(actor, r.rt.permission)
		path, _ := fill(r.rt.path)
		w := off.do(r.rt.method, r.appPrefix+path, couponOfferCase(r.rt), actor, true)
		if w.Code != http.StatusServiceUnavailable || !hasCode(w, CodeProductUnavailable) || len(off.takeHits()) != 0 {
			t.Fatalf("%s without a key: %d %s", r.rt.operation, w.Code, w.Body.String())
		}
		if a := off.takeAudit(); len(a) != 1 || a[0].StatusCode != 0 {
			t.Fatalf("%s without a key audit %+v", r.rt.operation, a)
		}
	}
}

// The mirror holds both permissions the way identity grants them: coupons
// are merchandising (admin only), offers are money policy (finance too).
func TestCatalogue_CouponsAndOffersHolders(t *testing.T) {
	cat := adminauth.Catalogue()
	roles := func(app, perm string) string {
		for _, p := range cat.Permissions[app] {
			if p.Permission == perm {
				return strings.Join(p.Roles, ",")
			}
		}
		return "absent"
	}
	if got := roles("commerce", permCouponsManage); got != "superadmin,admin" {
		t.Fatalf("%s holders %s", permCouponsManage, got)
	}
	if got := roles("payments", permPayOffersManage); got != "superadmin,admin,finance" {
		t.Fatalf("%s holders %s", permPayOffersManage, got)
	}
}
