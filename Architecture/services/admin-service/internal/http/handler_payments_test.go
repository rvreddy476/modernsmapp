package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const payPrefix = "/v1/admin/payments"

func payCase(rt productRoute) string {
	switch rt.operation {
	case opPayRefundResolve:
		return `{"resolution":"test_data","note":"seeded by QA"}`
	case "payments.application.update":
		return `{"display_name":"Feast","reason":"rename"}`
	}
	return ""
}

// refundAmount makes the stub answer GET /refunds/:id with an INR amount.
func refundAmount(rg *productsRig, commandID string, paise int64) {
	rg.on(http.MethodGet, service.PaymentsAdminPrefix+"/refunds/"+commandID, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"` + commandID + `","amount_minor":` + strconv.FormatInt(paise, 10) + `,"currency":"INR"}}`))
	})
}

// resolveHits splits the product hits into refund reads and resolve calls.
func resolveHits(hits []productHit) (reads, resolves []productHit) {
	for _, h := range hits {
		if strings.HasSuffix(h.path, "/resolve") {
			resolves = append(resolves, h)
		} else {
			reads = append(reads, h)
		}
	}
	return
}

func queryApp(h productHit) (string, bool) {
	q, _ := url.ParseQuery(h.query)
	v, ok := q[paymentsQueryAppID]
	if !ok {
		return "", false
	}
	return strings.Join(v, ","), true
}

func TestPaymentsRoutes_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
	if len(PaymentsRoutes) != 11 {
		t.Fatalf("PaymentsRoutes has %d entries, want 11", len(PaymentsRoutes))
	}
	for _, rt := range PaymentsRoutes {
		t.Run(rt.operation+" "+rt.method+" "+rt.path, func(t *testing.T) {
			routeTableCase(t, rg, payPrefix, service.PaymentsAdminPrefix, "payments", "payments", payAll, rt, payCase(rt))
		})
	}
}

func TestPaymentsRoutes_StepUp(t *testing.T) {
	rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
	want := map[string]bool{opPayRefundResolve: true, "payments.application.update": true}
	for _, rt := range PaymentsRoutes {
		if rt.stepUp != want[rt.operation] {
			t.Fatalf("%s declared stepUp=%v", rt.operation, rt.stepUp)
		}
		stepUpCase(t, rg, payPrefix, payAll, rt, payCase(rt), want[rt.operation])
	}
}

func TestPaymentsResolve_GateFollowsTheResolution_DecidedBeforeTheCall(t *testing.T) {
	cases := []struct {
		name       string
		resolution string
		paise      int64
		canRead    bool
		stepUp     bool
		wantStatus int
		wantCall   bool
	}{
		{"test_data moves no money: step-up only", "test_data", 900000, true, true, http.StatusOK, true},
		{"test_data without step-up", "test_data", 900000, true, false, http.StatusForbidden, false},
		{"written_off above threshold", "written_off", 900000, true, true, http.StatusAccepted, false},
		{"refunded_manually above threshold", "refunded_manually", 900000, true, true, http.StatusAccepted, false},
		{"refunded_manually below threshold", "refunded_manually", 100, true, true, http.StatusOK, true},
		{"written_off below threshold without step-up", "written_off", 100, true, false, http.StatusForbidden, false},
		{"amount unknown counts as above", "written_off", 100, false, true, http.StatusAccepted, false},
		{"unknown resolution", "refund_it", 100, true, true, http.StatusBadRequest, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rg := newProductsRig(t, true, 500000)
			rg.holders.n = 1
			actor := uuid.NewString()
			rg.perms.grant(actor, permPayRefundIssue)
			if tc.canRead {
				rg.perms.grant(actor, permPayRefundsRead)
			}
			id := uuid.NewString()
			refundAmount(rg, id, tc.paise)
			w := rg.do(http.MethodPost, payPrefix+"/refunds/"+id+"/resolve", `{"resolution":"`+tc.resolution+`","note":"checked the bank"}`, actor, tc.stepUp)
			if w.Code != tc.wantStatus {
				t.Fatalf("status %d %s, want %d", w.Code, w.Body.String(), tc.wantStatus)
			}
			_, resolves := resolveHits(rg.takeHits())
			if tc.wantCall != (len(resolves) == 1) || len(resolves) > 1 {
				t.Fatalf("resolve calls = %d, want call=%v", len(resolves), tc.wantCall)
			}
			if tc.wantCall && (!strings.Contains(resolves[0].body, `"resolution":"`+tc.resolution+`"`) || resolves[0].verified.Scope[0] != permPayRefundIssue) {
				t.Fatalf("resolve %+v", resolves[0])
			}
			if a := rg.takeAudit(); len(a) != 1 {
				t.Fatalf("audit rows %d, want 1", len(a))
			}
		})
	}
}

func TestPaymentsResolve_ThresholdBoundary(t *testing.T) {
	for _, tc := range []struct {
		paise     int64
		twoPerson bool
	}{{499999, false}, {500000, true}, {500001, true}} {
		rg := newProductsRig(t, true, 500000)
		rg.holders.n = 1
		actor := uuid.NewString()
		rg.perms.grant(actor, permPayRefundIssue, permPayRefundsRead)
		id := uuid.NewString()
		refundAmount(rg, id, tc.paise)
		w := rg.do(http.MethodPost, payPrefix+"/refunds/"+id+"/resolve", `{"resolution":"refunded_manually","note":"paid by NEFT"}`, actor, true)
		_, resolves := resolveHits(rg.takeHits())
		audit := rg.takeAudit()
		if tc.twoPerson {
			if w.Code != http.StatusAccepted || len(resolves) != 0 || len(audit) != 1 || audit[0].Outcome != postgres.AuditOutcomePending {
				t.Fatalf("%d: want pending, got %d %s", tc.paise, w.Code, w.Body.String())
			}
			continue
		}
		if w.Code != http.StatusOK || len(resolves) != 1 || len(rg.store.rows) != 0 {
			t.Fatalf("%d: want direct resolve, got %d %s", tc.paise, w.Code, w.Body.String())
		}
	}
}

func TestPaymentsResolve_ConfinedRequester_SecondHolderExecutesOnceInTheSameApplication(t *testing.T) {
	rg := newProductsRig(t, true, 500000)
	rg.holders.n = 1
	requester, approver, outsider := uuid.NewString(), uuid.NewString(), uuid.NewString()
	issueFeast := confinedPaymentsPermission("food", permPayRefundIssue)
	rg.perms.grant(requester, issueFeast, confinedPaymentsPermission("food", permPayRefundsRead))
	rg.perms.grant(approver, issueFeast)
	rg.perms.grant(outsider, confinedPaymentsPermission("commerce", permPayRefundIssue))
	id := uuid.NewString()
	refundAmount(rg, id, 750000)

	w := rg.do(http.MethodPost, payPrefix+"/refunds/"+id+"/resolve", `{"resolution":"written_off","note":"customer unreachable"}`, requester, true)
	a := decodeApproval(t, w)
	if a.App != "payments" || a.Operation != opPayRefundResolve || a.RequiredPermission != issueFeast || a.TargetID != id ||
		!strings.Contains(a.Summary, "₹7,500.00") || !strings.Contains(a.Summary, "written off") || !strings.Contains(a.Summary, "in feast") {
		t.Fatalf("approval %+v", a)
	}
	if rg.holders.lastPerm != issueFeast || rg.holders.lastExclude != requester {
		t.Fatalf("holders counted for %q excluding %q", rg.holders.lastPerm, rg.holders.lastExclude)
	}
	reads, resolves := resolveHits(rg.takeHits())
	if len(resolves) != 0 || len(reads) != 1 {
		t.Fatalf("reads=%d resolves=%d", len(reads), len(resolves))
	}
	if app, _ := queryApp(reads[0]); app != "feast" {
		t.Fatalf("amount read without the confinement: %q", reads[0].query)
	}
	rg.takeAudit()

	if w := rg.do(http.MethodPost, "/v1/admin/approvals/"+a.ID+"/approve", `{"reason":"not mine"}`, outsider, true); w.Code == http.StatusOK {
		t.Fatalf("an MStore-confined admin approved a Feast resolve: %s", w.Body.String())
	}
	w = rg.do(http.MethodPost, "/v1/admin/approvals/"+a.ID+"/approve", `{"reason":"checked"}`, approver, true)
	if w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	_, resolves = resolveHits(rg.takeHits())
	if len(resolves) != 1 || resolves[0].verified.Actor != approver || resolves[0].verified.Scope[0] != permPayRefundIssue ||
		!strings.Contains(resolves[0].body, `"written_off"`) {
		t.Fatalf("execution %+v", resolves)
	}
	if app, _ := queryApp(resolves[0]); app != "feast" {
		t.Fatalf("executed without application_id=feast: %q", resolves[0].query)
	}
	if w := rg.do(http.MethodPost, "/v1/admin/approvals/"+a.ID+"/approve", `{"reason":"again"}`, approver, true); !strings.Contains(w.Body.String(), `"replayed":true`) {
		t.Fatalf("second approve: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatal("executed twice")
	}
}

func TestPaymentsConfinement_AppScopedAdminAlwaysSendsTheirApplication(t *testing.T) {
	rg := newProductsRig(t, true, 500000)
	for _, rt := range PaymentsRoutes {
		t.Run(rt.operation, func(t *testing.T) {
			feast := uuid.NewString()
			rg.perms.grant(feast, confinedPaymentsPermission("food", rt.permission))
			path, _ := fill(rt.path)
			if rt.operation == "payments.application.update" {
				path = "/applications/feast"
			}
			w := rg.do(rt.method, payPrefix+path, payCase(rt), feast, true)
			if w.Code != http.StatusOK {
				t.Fatalf("confined call: %d %s", w.Code, w.Body.String())
			}
			hits := rg.takeHits()
			if len(hits) != 1 {
				t.Fatalf("hits %d", len(hits))
			}
			if app, ok := queryApp(hits[0]); !ok || app != "feast" {
				t.Fatalf("query %q, want application_id=feast", hits[0].query)
			}
			v := hits[0].verified
			if v.Actor != feast || len(v.Scope) != 1 || v.Scope[0] != rt.permission {
				t.Fatalf("token act=%s scope=%v, want %s", v.Actor, v.Scope, rt.permission)
			}
			a := rg.takeAudit()
			if len(a) != 1 || a[0].App != "payments" || a[0].Payload["application_id"] != "feast" ||
				a[0].Payload["held_as"] != confinedPaymentsPermission("food", rt.permission) {
				t.Fatalf("audit %+v", a)
			}
		})
	}
}

func TestPaymentsConfinement_ClientApplicationIDNeverWidens(t *testing.T) {
	rg := newProductsRig(t, true, 500000)
	const path = payPrefix + "/refunds/needs-attention"
	feast := uuid.NewString()
	rg.perms.grant(feast, confinedPaymentsPermission("food", permPayRefundsRead))

	// Its own id is accepted; another is refused before payments, audited.
	if w := rg.do(http.MethodGet, path+"?application_id=feast&limit=5", "", feast, false); w.Code != http.StatusOK {
		t.Fatalf("own id: %d", w.Code)
	}
	if hits := rg.takeHits(); len(hits) != 1 || !strings.Contains(hits[0].query, "limit=5") {
		t.Fatalf("own id hits %+v", hits)
	}
	rg.takeAudit()
	for _, q := range []string{"?application_id=mstore", "?application_id=dating", "?application_id=feast&application_id=mstore", "?application_id=Feast%27"} {
		w := rg.do(http.MethodGet, path+q, "", feast, false)
		if w.Code != http.StatusForbidden && w.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s", q, w.Code, w.Body.String())
		}
		if q == "?application_id=mstore" && !hasCode(w, CodeApplicationDeny) {
			t.Fatalf("%s: %s", q, w.Body.String())
		}
		if hits := rg.takeHits(); len(hits) != 0 {
			t.Fatalf("%s reached payments: %+v", q, hits)
		}
		if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeDenied {
			t.Fatalf("%s audit %+v", q, a)
		}
	}

	// A product permission that is not a payments view admits nothing.
	plain := uuid.NewString()
	rg.perms.grant(plain, permFoodRefundIssue, permFoodRefundsRead)
	if w := rg.do(http.MethodGet, path, "", plain, false); !hasCode(w, CodePermissionDenied) {
		t.Fatalf("plain Feast admin: %d %s", w.Code, w.Body.String())
	}

	// Confined to two applications: must pick one of them.
	both := uuid.NewString()
	rg.perms.grant(both, confinedPaymentsPermission("food", permPayRefundsRead), confinedPaymentsPermission("commerce", permPayRefundsRead))
	if w := rg.do(http.MethodGet, path, "", both, false); !hasCode(w, CodeApplicationNeed) {
		t.Fatalf("two applications, none chosen: %d %s", w.Code, w.Body.String())
	}
	if w := rg.do(http.MethodGet, path+"?application_id=mstore", "", both, false); w.Code != http.StatusOK {
		t.Fatalf("two applications, mstore: %d", w.Code)
	}
	if w := rg.do(http.MethodGet, path+"?application_id=dating", "", both, false); !hasCode(w, CodeApplicationDeny) {
		t.Fatalf("two applications, dating: %d", w.Code)
	}
	hits := rg.takeHits()
	if len(hits) != 1 {
		t.Fatalf("hits %+v", hits)
	}
	if app, _ := queryApp(hits[0]); app != "mstore" {
		t.Fatalf("query %q", hits[0].query)
	}
}

func TestPaymentsConfinement_PlatformAdminMayOmitOrNarrow(t *testing.T) {
	rg := newProductsRig(t, true, 500000)
	admin := uuid.NewString()
	// A platform-wide grant resolves to both the payments permission and every
	// app's permissions; the payments one decides: unconfined.
	rg.perms.grant(admin, permPayRefundsRead, confinedPaymentsPermission("food", permPayRefundsRead))
	const path = payPrefix + "/refunds/needs-attention"
	if w := rg.do(http.MethodGet, path, "", admin, false); w.Code != http.StatusOK {
		t.Fatalf("omit: %d", w.Code)
	}
	hits := rg.takeHits()
	if _, ok := queryApp(hits[0]); ok {
		t.Fatalf("platform admin was confined: %q", hits[0].query)
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Payload["held_as"] != nil || a[0].Payload["application_confined"] != nil {
		t.Fatalf("audit %+v", a)
	}
	if w := rg.do(http.MethodGet, path+"?application_id=mstore", "", admin, false); w.Code != http.StatusOK {
		t.Fatalf("narrow: %d", w.Code)
	}
	if app, _ := queryApp(rg.takeHits()[0]); app != "mstore" {
		t.Fatal("narrowing id not passed through")
	}
}

func TestMe_MoneyNavigation(t *testing.T) {
	rg := newProductsRig(t, true, 500000)
	type nav struct {
		App          string   `json:"app"`
		Applications []string `json:"applications"`
	}
	read := func(perms ...string) map[string]nav {
		actor := uuid.NewString()
		rg.perms.grant(actor, perms...)
		w := rg.do(http.MethodGet, "/v1/admin/me", "", actor, false)
		var env struct {
			Data struct {
				Navigation []nav `json:"navigation"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		out := map[string]nav{}
		for _, n := range env.Data.Navigation {
			out[n.App] = n
		}
		return out
	}
	if n := read(permFoodOrdersRead, "dating:reports.act"); len(n) != 2 || n["monetization"].App != "" || n["payments"].App != "" {
		t.Fatalf("no money permission: %+v", n)
	}
	if n := read(permMonFundRead); n["monetization"].App != "monetization" || n["payments"].App != "" {
		t.Fatalf("monetization holder: %+v", n)
	}
	if n := read(permPayRefundsRead); n["payments"].App != "payments" || n["payments"].Applications != nil || n["monetization"].App != "" {
		t.Fatalf("payments holder: %+v", n)
	}
	if n := read(confinedPaymentsPermission("food", permPayRefundsRead)); n["payments"].App != "payments" ||
		len(n["payments"].Applications) != 1 || n["payments"].Applications[0] != "feast" {
		t.Fatalf("confined Feast holder: %+v", n)
	}
}

func TestPaymentsConfinedPermissions_AreValidAndOwnedByTheProductApp(t *testing.T) {
	for _, m := range paymentsApplications {
		for _, perm := range payAll {
			cp := confinedPaymentsPermission(m.app, perm)
			if !adminauth.ValidPermission(cp) || adminauth.AppOf(cp) != m.app {
				t.Fatalf("%s -> %s", perm, cp)
			}
		}
	}
}

func TestGate_HeldAsAdmitsOnlyOnDeclaredRoutesAndOnlyAnotherApp(t *testing.T) {
	perms := &fakePerms{byUser: map[string]adminauth.Permissions{}}
	rec := &fakeRecorder{}
	g := NewGate(perms, rec, true)
	r := gin.New()
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	decide := func(held string) func(*gin.Context, adminauth.Permissions) (Decision, error) {
		return func(*gin.Context, adminauth.Permissions) (Decision, error) { return Decision{HeldAs: held}, nil }
	}
	g.Handle(r, http.MethodGet, "/v1/admin/payments/declared", Requirement{Operation: "a", Permission: permPayStatsRead, Decide: decide("food:payments_stats.read"), AdmitsHeldAs: true}, ok)
	g.Handle(r, http.MethodGet, "/v1/admin/payments/undeclared", Requirement{Operation: "b", Permission: permPayStatsRead, Decide: decide("food:payments_stats.read")}, ok)
	g.Handle(r, http.MethodGet, "/v1/admin/payments/same-app", Requirement{Operation: "c", Permission: permPayStatsRead, Decide: decide("payments:audit.read"), AdmitsHeldAs: true}, ok)
	g.Handle(r, http.MethodGet, "/v1/admin/payments/platform", Requirement{Operation: "d", Permission: permPayStatsRead, Decide: decide("platform:users.read"), AdmitsHeldAs: true}, ok)

	actor := uuid.NewString()
	perms.grant(actor, "food:payments_stats.read", "payments:audit.read", "platform:users.read")
	call := func(path string) int {
		req, _ := http.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-User-Id", actor)
		req.Header.Set("X-Admin-MFA", "true")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	if c := call("/v1/admin/payments/declared"); c != http.StatusOK {
		t.Fatalf("declared: %d", c)
	}
	for _, p := range []string{"/v1/admin/payments/undeclared", "/v1/admin/payments/same-app", "/v1/admin/payments/platform"} {
		if c := call(p); c != http.StatusInternalServerError {
			t.Fatalf("%s: %d, want 500", p, c)
		}
	}
	// Holding neither: refused.
	other := uuid.NewString()
	perms.grant(other, "food:orders.read")
	req, _ := http.NewRequest(http.MethodGet, "/v1/admin/payments/declared", nil)
	req.Header.Set("X-User-Id", other)
	req.Header.Set("X-Admin-MFA", "true")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("no permission: %d", w.Code)
	}
}
