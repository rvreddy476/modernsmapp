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
	}
	if rt.method == http.MethodGet {
		return ""
	}
	return `{"reason":"checked"}`
}

// riderStepUp lists the operations that must be declared step-up: partner
// suspend and block, the document queue and both document decisions (a KYC
// reveal), payment verify and reject, ride cancel, contact alerts (phone
// numbers), fare-rule changes.
var riderStepUp = map[string]bool{
	"rider.partner.suspend": true, "rider.partner.block": true,
	"rider.documents.list": true, "rider.document.verify": true, "rider.document.reject": true,
	"rider.payment.verify": true, "rider.payment.reject": true,
	"rider.ride.cancel":            true,
	"rider.incident.alerts.reveal": true,
	"rider.fare_rule.create":       true, "rider.fare_rule.update": true,
}

func TestRiderRoutes_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newProductsRig(t, true)
	if len(RiderRoutes) != 43 {
		t.Fatalf("RiderRoutes has %d entries, want 43 (rider's 42 admin routes plus /stats)", len(RiderRoutes))
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
		if rt.twoPerson || rt.mayTwoPerson {
			t.Fatalf("%s is two-person; no money leaves rider-service", rt.operation)
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
