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

// riderCase gives each Mopedu route a body rider-service would accept. The
// stub takes anything; what matters is that writes carry JSON.
func riderCase(rt productRoute) string {
	switch rt.operation {
	case "rider.ride.rating.visibility":
		return `{"hidden":true,"reason":"abusive text"}`
	case "rider.complaint.status":
		return `{"status":"resolved","resolution_notes":"called the customer"}`
	case "rider.city.create", "rider.city.update":
		return `{"name":"Hyderabad","state":"Telangana","is_active":true}`
	case "rider.zone.create", "rider.zone.update":
		return `{"name":"Gachibowli","is_active":true}`
	case "rider.fare_rule.create", "rider.fare_rule.update":
		return `{"base_fare_paise":2500,"per_km_paise":900,"reason":"fuel price change"}`
	case "rider.fare_window.create", "rider.fare_window.update":
		return `{"city_id":"` + uuid.NewString() + `","name":"Weekday morning peak","days_of_week":31,"start_minute":480,"end_minute":600,"multiplier_bps":12500,"priority":10,"reason":"commute demand"}`
	case "rider.coupon.create", "rider.coupon.update":
		return `{"code":"FIRST50","discount_type":"percent","percent_bps":5000,"max_discount_paise":5000,"first_ride_only":true,"reason":"launch offer"}`
	case opRiderRefundIssue:
		return `{"amount_paise":12500,"reason":"partner never arrived"}`
	case opRiderOutstandingWaive:
		return `{"reason":"cancelled by the partner, fee charged in error"}`
	}
	if rt.method == http.MethodGet {
		return ""
	}
	return `{"reason":"checked"}`
}

// riderStepUp lists the operations that must be declared step-up: partner
// suspend and block, the document queue and both document decisions (a KYC
// reveal), payment verify and reject, ride cancel, contact alerts (phone
// numbers), every pricing write (fare rules, fare windows, coupons) and the
// two money-out routes (which are two-person as well).
var riderStepUp = map[string]bool{
	"rider.partner.suspend": true, "rider.partner.block": true,
	"rider.documents.list": true, "rider.document.verify": true, "rider.document.reject": true,
	"rider.payment.verify": true, "rider.payment.reject": true,
	"rider.ride.cancel":            true,
	"rider.incident.alerts.reveal": true,
	"rider.fare_rule.create":       true, "rider.fare_rule.update": true,
	"rider.fare_window.create": true, "rider.fare_window.update": true, "rider.fare_window.deactivate": true,
	"rider.coupon.create": true, "rider.coupon.update": true, "rider.coupon.deactivate": true,
	opRiderRefundIssue: true, opRiderOutstandingWaive: true,
}

// riderTwoPerson lists the operations that give money back to a customer:
// two-person, always, whatever the amount.
var riderTwoPerson = map[string]bool{opRiderRefundIssue: true, opRiderOutstandingWaive: true}

func TestRiderRoutes_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newProductsRig(t, true)
	if len(RiderRoutes) != 58 {
		t.Fatalf("RiderRoutes has %d entries, want 58 (rider's 57 admin routes plus /stats)", len(RiderRoutes))
	}
	seen := map[string]bool{}
	for _, rt := range RiderRoutes {
		if seen[rt.operation] {
			t.Fatalf("operation %s is declared twice", rt.operation)
		}
		seen[rt.operation] = true
		if !strings.HasPrefix(rt.permission, "rider:") {
			t.Fatalf("%s carries %q, not a rider permission", rt.operation, rt.permission)
		}
		if rt.twoPerson != riderTwoPerson[rt.operation] || rt.mayTwoPerson {
			t.Fatalf("%s declares two-person=%v may=%v, want %v (only the refund and the waiver move money)", rt.operation, rt.twoPerson, rt.mayTwoPerson, riderTwoPerson[rt.operation])
		}
		if rt.twoPerson && !rt.stepUp {
			t.Fatalf("%s is two-person but not step-up", rt.operation)
		}
		t.Run(rt.operation+" "+rt.method+" "+rt.path, func(t *testing.T) {
			routeTableCase(t, rg, riderPrefix, service.RiderAdminPrefix, "rider", "rider", riderAll, rt, riderCase(rt))
		})
	}
}

func TestRiderRoutes_StepUp(t *testing.T) {
	rg := newProductsRig(t, true)
	found := map[string]bool{}
	for _, rt := range RiderRoutes {
		want := riderStepUp[rt.operation]
		if rt.stepUp != want {
			t.Fatalf("%s declares step-up=%v, want %v", rt.operation, rt.stepUp, want)
		}
		if rt.stepUp {
			found[rt.operation] = true
		}
		stepUpCase(t, rg, riderPrefix, riderAll, rt, riderCase(rt), want)
	}
	for op := range riderStepUp {
		if !found[op] {
			t.Fatalf("%s is not declared step-up", op)
		}
	}
}

// The document queue is a KYC reveal: without a fresh step-up it is refused
// before rider is called, and the refusal is audited.
func TestRiderDocuments_ListIsAStepUpRead(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permRiderDocumentsReview)
	w := rg.do(http.MethodGet, riderPrefix+"/documents", "", actor, false)
	if w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeStepUpRequired) {
		t.Fatalf("documents without step-up: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("the document queue was fetched without step-up: %+v", hits)
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome == postgres.AuditOutcomeSuccess {
		t.Fatalf("refusal audit %+v", a)
	}
	w = rg.do(http.MethodGet, riderPrefix+"/documents", "", actor, true)
	hits := rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 1 || hits[0].verified.Scope[0] != permRiderDocumentsReview || hits[0].path != service.RiderAdminPrefix+"/documents" {
		t.Fatalf("documents with step-up: %d hits=%+v", w.Code, hits)
	}
}

// A holder of another app's permissions never reaches a rider route: a
// Feast admin with every food permission is refused on each Mopedu route
// before rider-service is called, and each refusal is audited as denied.
func TestRiderRoutes_OtherAppsPermissionsAreRefused(t *testing.T) {
	rg := newProductsRig(t, true)
	feast := uuid.NewString()
	rg.perms.grant(feast, foodAll...)
	for _, rt := range RiderRoutes {
		path, _ := fill(rt.path)
		w := rg.do(rt.method, riderPrefix+path, riderCase(rt), feast, true)
		if w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
			t.Fatalf("%s with food permissions: %d %s", rt.operation, w.Code, w.Body.String())
		}
		if hits := rg.takeHits(); len(hits) != 0 {
			t.Fatalf("%s: a Feast admin reached rider: %+v", rt.operation, hits)
		}
		if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeDenied || a[0].App != riderAuditApp {
			t.Fatalf("%s: audit %+v", rt.operation, a)
		}
	}
}

// Stats are rider's own /stats, forwarded with rider:stats.read.
func TestRiderStats_ForwardedFromRider(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permRiderStatsRead)
	rg.on(http.MethodGet, service.RiderAdminPrefix+"/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"pending_partners":3,"live_rides":7}}`))
	})
	w := rg.do(http.MethodGet, riderPrefix+"/stats", "", actor, false)
	hits := rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 1 || hits[0].aud != "rider" || hits[0].verified.Scope[0] != permRiderStatsRead ||
		!strings.Contains(w.Body.String(), `"live_rides":7`) {
		t.Fatalf("stats: %d %s hits=%+v", w.Code, w.Body.String(), hits)
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Operation != "rider.stats" || a[0].Outcome != postgres.AuditOutcomeSuccess {
		t.Fatalf("audit %+v", a)
	}
}

// Without the signing key every Mopedu route answers 503 and is audited.
func TestRiderRoutes_NoKeyIs503(t *testing.T) {
	rg := newProductsRig(t, false)
	actor := uuid.NewString()
	rg.perms.grant(actor, permRiderRidesRead)
	w := rg.do(http.MethodGet, riderPrefix+"/rides", "", actor, false)
	if w.Code != http.StatusServiceUnavailable || !hasCode(w, CodeProductUnavailable) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure {
		t.Fatalf("audit %+v", a)
	}
}

// A ride refund and an outstanding-fee waiver are two-person: with a second
// holder of rider:payments.settle the first admin's step-up call is stored
// as a pending approval (202) and never reaches rider; the approver's
// decision replays it with a token scoped to that permission, acting as the
// approver, at the product path the requester filled.
func TestRiderMoney_RefundAndWaiveAreTwoPerson(t *testing.T) {
	for _, tc := range []struct {
		op, body, summary string
	}{
		{opRiderRefundIssue, `{"amount_paise":12500,"reason":"partner never arrived"}`, "Refund Mopedu ride"},
		{opRiderRefundIssue, `{"reason":"whole fare back"}`, "amount not stated"},
		{opRiderOutstandingWaive, `{"reason":"fee charged in error"}`, "Waive Mopedu cancellation fee"},
	} {
		rg := newProductsRig(t, true)
		rg.holders.n = 1
		requester, approver := uuid.NewString(), uuid.NewString()
		rg.perms.grant(requester, permRiderPaymentsSettle)
		rg.perms.grant(approver, permRiderPaymentsSettle)
		var rt productRoute
		for _, r := range RiderRoutes {
			if r.operation == tc.op {
				rt = r
			}
		}
		path, ids := fill(rt.path)
		if w := rg.do(rt.method, riderPrefix+path, tc.body, requester, false); !hasCode(w, adminauth.CodeStepUpRequired) {
			t.Fatalf("%s without step-up: %d %s", tc.op, w.Code, w.Body.String())
		}
		rg.takeAudit()
		w := rg.do(rt.method, riderPrefix+path, tc.body, requester, true)
		if w.Code != http.StatusAccepted {
			t.Fatalf("%s first call: %d %s", tc.op, w.Code, w.Body.String())
		}
		if hits := rg.takeHits(); len(hits) != 0 {
			t.Fatalf("%s: the first admin's call reached rider: %+v", tc.op, hits)
		}
		if audit := rg.takeAudit(); len(audit) != 1 || audit[0].Outcome != postgres.AuditOutcomePending || audit[0].TargetID != ids["id"] {
			t.Fatalf("%s pending audit %+v", tc.op, audit)
		}
		a := decodeApproval(t, w)
		if a.App != riderAuditApp || a.Operation != tc.op || a.RequiredPermission != permRiderPaymentsSettle ||
			a.RequestedBy != requester || a.TargetID != ids["id"] || !strings.Contains(a.Summary, tc.summary) {
			t.Fatalf("%s approval %+v", tc.op, a)
		}
		if strings.HasPrefix(tc.body, `{"amount_paise"`) && !strings.Contains(a.Summary, "₹125.00") {
			t.Fatalf("%s summary lacks the amount: %q", tc.op, a.Summary)
		}

		w = rg.do(http.MethodPost, "/v1/admin/approvals/"+a.ID+"/approve", `{"reason":"checked"}`, approver, true)
		if w.Code != http.StatusOK {
			t.Fatalf("%s approve: %d %s", tc.op, w.Code, w.Body.String())
		}
		hits := rg.takeHits()
		if len(hits) != 1 || hits[0].aud != "rider" || hits[0].method != rt.method || hits[0].path != service.RiderAdminPrefix+path ||
			hits[0].verified.Actor != approver || len(hits[0].verified.Scope) != 1 || hits[0].verified.Scope[0] != permRiderPaymentsSettle ||
			hits[0].body != tc.body {
			t.Fatalf("%s replay: %+v", tc.op, hits)
		}
	}
}

// A stated refund amount must be a positive whole number of paise; anything
// else is refused before the approval is stored and before rider is called.
func TestRiderRefund_BadAmountsAreRefusedBeforeAnything(t *testing.T) {
	rg := newProductsRig(t, true)
	rg.holders.n = 1
	actor := uuid.NewString()
	rg.perms.grant(actor, permRiderPaymentsSettle)
	path := riderPrefix + "/rides/" + uuid.NewString() + "/refund"
	for _, body := range []string{
		`{"amount_paise":0,"reason":"r"}`,
		`{"amount_paise":-5,"reason":"r"}`,
		`{"amount_paise":12.5,"reason":"r"}`,
		`{"amount_paise":"lots","reason":"r"}`,
		`{"amount_paise":100000000000,"reason":"r"}`,
	} {
		w := rg.do(http.MethodPost, path, body, actor, true)
		if w.Code != http.StatusBadRequest || !hasCode(w, CodeInvalidBody) {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
		if hits := rg.takeHits(); len(hits) != 0 {
			t.Fatalf("%s reached rider: %+v", body, hits)
		}
		rg.takeAudit()
	}
	// No reason: refused by the two-person submit, nothing stored.
	w := rg.do(http.MethodPost, path, `{"amount_paise":100}`, actor, true)
	if w.Code != http.StatusBadRequest || !hasCode(w, CodeReasonRequired) {
		t.Fatalf("no reason: %d %s", w.Code, w.Body.String())
	}
	w = rg.do(http.MethodPost, riderPrefix+"/rides/not-a-uuid/refund", `{"reason":"r"}`, actor, true)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad ride id: %d %s", w.Code, w.Body.String())
	}
}

// Mopedu appears in /me only for a holder of a rider permission.
func TestMe_MopeduOnlyWithARiderPermission(t *testing.T) {
	rg := newProductsRig(t, true)
	for name, tc := range map[string]struct {
		perms []string
		want  bool
	}{
		"rider read":       {[]string{permRiderRidesRead}, true},
		"rider new string": {[]string{permRiderPartnersRead}, true},
		"feast only":       {[]string{permFoodOrdersRead}, false},
		"chat only":        {[]string{permChatReportsAct}, false},
		"platform-wide":    {[]string{"*:audit.read"}, false},
	} {
		actor := uuid.NewString()
		rg.perms.grant(actor, tc.perms...)
		w := rg.do(http.MethodGet, "/v1/admin/me", "", actor, false)
		got := strings.Contains(w.Body.String(), `{"app":"rider","label":"Mopedu"}`)
		if got != tc.want {
			t.Fatalf("%s: Mopedu in navigation = %v, want %v (%s)", name, got, tc.want, w.Body.String())
		}
	}
}
