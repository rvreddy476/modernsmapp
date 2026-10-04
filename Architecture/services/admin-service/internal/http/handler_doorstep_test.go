package http

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
)

// doorstepCase gives each Doorstep route a body doorstep-service would
// accept. The stub takes anything; what matters is that writes carry JSON.
func doorstepCase(rt productRoute) (string, []reqOpt) {
	switch rt.operation {
	case opDoorstepRefundIssue:
		return `{"amount_paise":12500,"reason":"professional never arrived"}`, []reqOpt{withIdempotency("refund-" + uuid.NewString())}
	case opDoorstepIncidentResolve:
		return `{"resolution":"spoke to both sides"}`, nil
	case "doorstep.document.decide":
		return `{"decision":"approve","reason":"certificate is genuine"}`, nil
	case "doorstep.professional.skill.verify":
		return `{"verified":true,"reason":"trade certificate seen"}`, nil
	case "doorstep.pro_price.approve":
		return `{"reason":"in line with the market"}`, nil
	case "doorstep.pro_price.reject":
		return `{"reason":"far above the suggested price"}`, nil
	case "doorstep.ticket.status":
		return `{"status":"resolved","note":"refunded"}`, nil
	case "doorstep.price.create":
		return `{"service_id":"` + uuid.NewString() + `","option_id":"` + uuid.NewString() + `","city_code":"HYD","price_paise":49900,"effective_from":"2026-10-05"}`, nil
	}
	if rt.method == http.MethodGet {
		return "", nil
	}
	return `{"reason":"checked"}`, nil
}

// doorstepStepUp lists the operations that must be declared step-up: the
// professional detail, the document queue and decisions (reveals),
// suspend / reinstate / block, booking cancel, every money setting (including
// approving a professional's price, which makes it live), and the
// refund (which is two-person as well).
var doorstepStepUp = map[string]bool{
	"doorstep.professional.tax_registration.read":  true,
	"doorstep.professional.tax_registration.write": true,
	"doorstep.professional.read":                   true,
	"doorstep.documents.list":                      true,
	"doorstep.document.decide":                     true,
	opDoorstepDocumentView:                         true,
	"doorstep.professional.suspend":                true, "doorstep.professional.reinstate": true, "doorstep.professional.block": true,
	"doorstep.booking.cancel": true,
	"doorstep.price.create":   true, "doorstep.rate_card.create": true, "doorstep.rate_card.update": true,
	"doorstep.cancellation_rule.create": true, "doorstep.cancellation_rule.update": true,
	"doorstep.commission_rule.create": true, "doorstep.commission_rule.update": true,
	"doorstep.pro_price.approve": true,
	opDoorstepRefundIssue:        true,
}

// The console paths the admin console lane calls, under /v1/admin/doorstep,
// with the contract's x-permission for each. Kept literal so a route that is
// dropped or renamed fails here even where the contract file is absent.
var doorstepContract = []string{
	"GET /stats doorstep:stats.read",
	"GET /cities doorstep:catalogue.read", "POST /cities doorstep:config.write", "PATCH /cities/:code doorstep:config.write",
	"GET /zones doorstep:catalogue.read", "POST /zones doorstep:config.write", "PATCH /zones/:id doorstep:config.write",
	"GET /categories doorstep:catalogue.read", "POST /categories doorstep:catalogue.write", "PATCH /categories/:id doorstep:catalogue.write",
	"GET /skills doorstep:catalogue.read", "POST /skills doorstep:catalogue.write",
	"GET /services doorstep:catalogue.read", "POST /services doorstep:catalogue.write",
	"GET /services/:id doorstep:catalogue.read", "PATCH /services/:id doorstep:catalogue.write",
	"POST /services/:id/options doorstep:catalogue.write", "PATCH /options/:id doorstep:catalogue.write",
	"POST /services/:id/addon-groups doorstep:catalogue.write", "PATCH /addon-groups/:id doorstep:catalogue.write",
	"POST /addon-groups/:id/addons doorstep:catalogue.write", "PATCH /addons/:id doorstep:catalogue.write",
	"GET /prices doorstep:catalogue.read", "POST /prices doorstep:catalogue.write",
	"GET /rate-cards doorstep:catalogue.read", "POST /rate-cards doorstep:catalogue.write", "PATCH /rate-cards/:id doorstep:catalogue.write",
	"GET /slot-configs doorstep:catalogue.read", "POST /slot-configs doorstep:config.write", "PATCH /slot-configs/:id doorstep:config.write",
	"GET /cancellation-rules doorstep:catalogue.read", "POST /cancellation-rules doorstep:config.write", "PATCH /cancellation-rules/:id doorstep:config.write",
	"GET /commission-rules doorstep:catalogue.read", "POST /commission-rules doorstep:config.write", "PATCH /commission-rules/:id doorstep:config.write",
	"GET /professionals doorstep:pros.read", "GET /professionals/:id doorstep:pros.read",
	"GET /professionals/:id/tax-registration doorstep:pros.approve", "POST /professionals/:id/tax-registration doorstep:pros.approve",
	"POST /professionals/:id/approve doorstep:pros.approve", "POST /professionals/:id/reject doorstep:pros.approve",
	"POST /professionals/:id/suspend doorstep:pros.suspend", "POST /professionals/:id/reinstate doorstep:pros.suspend",
	"POST /professionals/:id/block doorstep:pros.suspend", "POST /professionals/:id/skills/:code/verify doorstep:pros.approve",
	"GET /documents doorstep:documents.review", "GET /documents/:id/view doorstep:documents.review", "POST /documents/:id/decide doorstep:documents.review",
	"GET /bookings doorstep:bookings.read", "GET /bookings/:id doorstep:bookings.read",
	"POST /bookings/:id/cancel doorstep:bookings.cancel", "POST /bookings/:id/redispatch doorstep:bookings.redispatch",
	"POST /bookings/:id/refund doorstep:refunds.issue",
	"GET /incidents doorstep:incidents.read", "POST /incidents/:id/acknowledge doorstep:incidents.act", "POST /incidents/:id/resolve doorstep:incidents.act",
	"GET /tickets doorstep:tickets.act", "POST /tickets/:id/status doorstep:tickets.act",
	"GET /ratings doorstep:ratings.moderate", "POST /ratings/:id/hide doorstep:ratings.moderate",
	"GET /settlements doorstep:settlements.read", "GET /audit-logs doorstep:audit.read",
	"GET /pro-prices doorstep:prices.review", "POST /pro-prices/:id/approve doorstep:prices.review",
	"POST /pro-prices/:id/reject doorstep:prices.review",
}

func doorstepTable() []string {
	out := make([]string, 0, len(DoorstepRoutes))
	for _, rt := range DoorstepRoutes {
		out = append(out, rt.method+" "+rt.path+" "+rt.permission)
	}
	sort.Strings(out)
	return out
}

func sameSet(t *testing.T, what string, got, want []string) {
	t.Helper()
	g, w := map[string]bool{}, map[string]bool{}
	for _, s := range got {
		g[s] = true
	}
	for _, s := range want {
		w[s] = true
	}
	for s := range w {
		if !g[s] {
			t.Errorf("%s: missing %s", what, s)
		}
	}
	for s := range g {
		if !w[s] {
			t.Errorf("%s: not in the contract: %s", what, s)
		}
	}
	if len(got) != len(g) {
		t.Errorf("%s: a route is declared twice", what)
	}
}

// Every admin route in the contract is in the table, at the same path, with
// its x-permission, and nothing else is.
func TestDoorstepRoutes_CoverEveryContractRoute(t *testing.T) {
	sameSet(t, "literal list", doorstepTable(), doorstepContract)

	// The contract itself, when the repository is checked out around this
	// module (a module-only build skips this half; the literal list stands).
	f, err := os.Open(filepath.Join("..", "..", "..", "..", "..", "contracts", "doorstep", "openapi.yaml"))
	if err != nil {
		t.Skipf("contract not reachable from here: %v", err)
	}
	defer f.Close()
	const prefix = "  /v1/doorstep/internal/admin"
	var fromContract []string
	path, method := "", ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "  /"):
			path, method = "", ""
			if strings.HasPrefix(line, prefix+"/") {
				p := strings.TrimSuffix(strings.TrimPrefix(line, prefix), ":")
				segs := strings.Split(p, "/")
				for i, s := range segs {
					if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
						segs[i] = ":" + s[1:len(s)-1]
					}
				}
				path = strings.Join(segs, "/")
			}
		case path != "" && len(line) > 4 && line[:4] == "    " && line[4] != ' ' && strings.HasSuffix(line, ":"):
			method = strings.ToUpper(strings.TrimSuffix(strings.TrimSpace(line), ":"))
		case path != "" && method != "" && strings.HasPrefix(strings.TrimSpace(line), "x-permission:"):
			perm := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "x-permission:"))
			fromContract = append(fromContract, method+" "+path+" "+perm)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(fromContract) == 0 {
		t.Fatal("no admin routes read from the contract")
	}
	sameSet(t, "openapi.yaml", doorstepTable(), fromContract)
}

func TestDoorstepRoutes_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newProductsRig(t, true)
	// The document view relays image bytes: doorstep-service answers one.
	rg.onTemplate(http.MethodGet, service.DoorstepAdminPrefix+"/documents/:id/view", serveImage("image/jpeg", kycImage(2048), true))
	seen := map[string]bool{}
	for _, rt := range DoorstepRoutes {
		if seen[rt.operation] {
			t.Fatalf("operation %s is declared twice", rt.operation)
		}
		seen[rt.operation] = true
		if !strings.HasPrefix(rt.permission, "doorstep:") {
			t.Fatalf("%s carries %q, not a doorstep permission", rt.operation, rt.permission)
		}
		wantTwo := rt.operation == opDoorstepRefundIssue
		if rt.twoPerson != wantTwo || rt.mayTwoPerson {
			t.Fatalf("%s declares two-person=%v may=%v, want %v (only the refund moves money out)", rt.operation, rt.twoPerson, rt.mayTwoPerson, wantTwo)
		}
		if rt.twoPerson && (!rt.stepUp || !rt.idempotent) {
			t.Fatalf("%s is two-person but not step-up and idempotent", rt.operation)
		}
		body, opts := doorstepCase(rt)
		t.Run(rt.operation+" "+rt.method+" "+rt.path, func(t *testing.T) {
			routeTableCase(t, rg, doorstepPrefix, service.DoorstepAdminPrefix, "doorstep", "doorstep", doorstepAll, rt, body, opts...)
		})
	}
}

func TestDoorstepRoutes_StepUp(t *testing.T) {
	rg := newProductsRig(t, true)
	found := map[string]bool{}
	for _, rt := range DoorstepRoutes {
		want := doorstepStepUp[rt.operation]
		if rt.stepUp != want {
			t.Fatalf("%s declares step-up=%v, want %v", rt.operation, rt.stepUp, want)
		}
		if rt.stepUp {
			found[rt.operation] = true
		}
		body, opts := doorstepCase(rt)
		stepUpCase(t, rg, doorstepPrefix, doorstepAll, rt, body, want, opts...)
	}
	for op := range doorstepStepUp {
		if !found[op] {
			t.Fatalf("%s is not declared step-up", op)
		}
	}
}

// A holder of another app's permissions never reaches a Doorstep route: a
// Mopedu admin with every rider permission is refused on each route before
// doorstep-service is called, and each refusal is audited as denied.
func TestDoorstepRoutes_OtherAppsPermissionsAreRefused(t *testing.T) {
	rg := newProductsRig(t, true)
	mopedu := uuid.NewString()
	rg.perms.grant(mopedu, riderAll...)
	for _, rt := range DoorstepRoutes {
		path, _ := fill(rt.path)
		body, opts := doorstepCase(rt)
		w := rg.do(rt.method, doorstepPrefix+path, body, mopedu, true, opts...)
		if w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
			t.Fatalf("%s with rider permissions: %d %s", rt.operation, w.Code, w.Body.String())
		}
		if hits := rg.takeHits(); len(hits) != 0 {
			t.Fatalf("%s: a Mopedu admin reached doorstep: %+v", rt.operation, hits)
		}
		if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeDenied || a[0].App != doorstepAuditApp || a[0].Actor != mopedu {
			t.Fatalf("%s: audit %+v", rt.operation, a)
		}
	}
}

// The professional detail opens document media ids and the payout account:
// without a fresh step-up it is refused before doorstep is called, and the
// refusal is audited with the real actor.
func TestDoorstepProfessionalDetail_IsAStepUpRead(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permDoorstepProsRead)
	pro := uuid.NewString()
	w := rg.do(http.MethodGet, doorstepPrefix+"/professionals/"+pro, "", actor, false)
	if w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeStepUpRequired) {
		t.Fatalf("detail without step-up: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("the detail was fetched without step-up: %+v", hits)
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeDenied || a[0].Actor != actor || a[0].Operation != "doorstep.professional.read" {
		t.Fatalf("refusal audit %+v", a)
	}
	w = rg.do(http.MethodGet, doorstepPrefix+"/professionals/"+pro, "", actor, true)
	hits := rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 1 || hits[0].aud != "doorstep" || hits[0].verified.Actor != actor ||
		hits[0].verified.Scope[0] != permDoorstepProsRead || hits[0].path != service.DoorstepAdminPrefix+"/professionals/"+pro {
		t.Fatalf("detail with step-up: %d hits=%+v", w.Code, hits)
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Actor != actor || a[0].TargetType != "doorstep_professional" || a[0].TargetID != pro {
		t.Fatalf("audit %+v", a)
	}
}

// A refund is step-up and two-person, always: with a second holder of
// doorstep:refunds.issue the first admin's call is stored (202) and never
// reaches doorstep; the approver's decision replays it as the approver, with
// a token scoped to that permission, the same body and the same
// Idempotency-Key.
func TestDoorstepRefund_IsStepUpTwoPersonAndReplaysTheKey(t *testing.T) {
	rg := newProductsRig(t, true)
	rg.holders.n = 1
	requester, approver := uuid.NewString(), uuid.NewString()
	rg.perms.grant(requester, permDoorstepRefundsIssue)
	rg.perms.grant(approver, permDoorstepRefundsIssue)
	booking := uuid.NewString()
	path := doorstepPrefix + "/bookings/" + booking + "/refund"
	// Keys in sorted order: the stored body is replayed as canonical JSON.
	body := `{"amount_paise":12500,"payment":"extras","reason":"professional never arrived"}`

	if w := rg.do(http.MethodPost, path, body, requester, false, withIdempotency("rf-1")); !hasCode(w, adminauth.CodeStepUpRequired) {
		t.Fatalf("without step-up: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("reached doorstep without step-up: %+v", hits)
	}
	rg.takeAudit()

	if w := rg.do(http.MethodPost, path, body, requester, true); w.Code != http.StatusBadRequest || !hasCode(w, CodeIdempotencyKeyRequired) {
		t.Fatalf("without a key: %d %s", w.Code, w.Body.String())
	}
	rg.takeAudit()

	w := rg.do(http.MethodPost, path, body, requester, true, withIdempotency("rf-1"))
	if w.Code != http.StatusAccepted {
		t.Fatalf("first call: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("the first admin's call reached doorstep: %+v", hits)
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomePending || a[0].Actor != requester ||
		a[0].TargetType != "doorstep_booking" || a[0].TargetID != booking {
		t.Fatalf("pending audit %+v", a)
	}
	a := decodeApproval(t, w)
	if a.App != doorstepAuditApp || a.Operation != opDoorstepRefundIssue || a.RequiredPermission != permDoorstepRefundsIssue ||
		a.RequestedBy != requester || a.TargetID != booking || !strings.Contains(a.Summary, "Refund Doorstep booking") ||
		!strings.Contains(a.Summary, "₹125.00") {
		t.Fatalf("approval %+v", a)
	}

	w = rg.do(http.MethodPost, "/v1/admin/approvals/"+a.ID+"/approve", `{"reason":"checked"}`, approver, true)
	if w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	hits := rg.takeHits()
	if len(hits) != 1 || hits[0].aud != "doorstep" || hits[0].method != http.MethodPost ||
		hits[0].path != service.DoorstepAdminPrefix+"/bookings/"+booking+"/refund" ||
		hits[0].verified.Actor != approver || len(hits[0].verified.Scope) != 1 || hits[0].verified.Scope[0] != permDoorstepRefundsIssue ||
		hits[0].body != body || hits[0].idempotencyKey != "rf-1" {
		t.Fatalf("replay: %+v", hits)
	}
}

// The refund body is checked before anything is stored or called: the
// contract requires a positive whole amount_paise and payment booking/extras.
func TestDoorstepRefund_BadBodiesAreRefusedBeforeAnything(t *testing.T) {
	rg := newProductsRig(t, true)
	rg.holders.n = 1
	actor := uuid.NewString()
	rg.perms.grant(actor, permDoorstepRefundsIssue)
	path := doorstepPrefix + "/bookings/" + uuid.NewString() + "/refund"
	for _, body := range []string{
		`{"reason":"no amount"}`,
		`{"amount_paise":0,"reason":"r"}`,
		`{"amount_paise":-5,"reason":"r"}`,
		`{"amount_paise":12.5,"reason":"r"}`,
		`{"amount_paise":"lots","reason":"r"}`,
		`{"amount_paise":100000000000,"reason":"r"}`,
		`{"amount_paise":100,"reason":"r","payment":"tip"}`,
		``,
	} {
		w := rg.do(http.MethodPost, path, body, actor, true, withIdempotency("k"))
		if w.Code != http.StatusBadRequest || !hasCode(w, CodeInvalidBody) {
			t.Fatalf("%q: %d %s", body, w.Code, w.Body.String())
		}
		if hits := rg.takeHits(); len(hits) != 0 {
			t.Fatalf("%q reached doorstep: %+v", body, hits)
		}
		rg.takeAudit()
	}
	w := rg.do(http.MethodPost, path, `{"amount_paise":100}`, actor, true, withIdempotency("k"))
	if w.Code != http.StatusBadRequest || !hasCode(w, CodeReasonRequired) {
		t.Fatalf("no reason: %d %s", w.Code, w.Body.String())
	}
	w = rg.do(http.MethodPost, doorstepPrefix+"/bookings/not-a-uuid/refund", `{"amount_paise":100,"reason":"r"}`, actor, true, withIdempotency("k"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad booking id: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("reached doorstep: %+v", hits)
	}
}

// Resolving an incident that lifts a suspension needs a step-up; a plain
// resolve does not.
func TestDoorstepIncidentResolve_LiftingASuspensionIsStepUp(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permDoorstepIncidentsAct)
	path := doorstepPrefix + "/incidents/" + uuid.NewString() + "/resolve"
	lift := `{"resolution":"cleared after a call","lift_suspension":true}`
	if w := rg.do(http.MethodPost, path, lift, actor, false); !hasCode(w, adminauth.CodeStepUpRequired) {
		t.Fatalf("lift without step-up: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("reached doorstep: %+v", hits)
	}
	rg.takeAudit()
	if w := rg.do(http.MethodPost, path, lift, actor, true); w.Code != http.StatusOK {
		t.Fatalf("lift with step-up: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 1 || hits[0].body != lift {
		t.Fatalf("hits %+v", hits)
	}
	rg.takeAudit()
	if w := rg.do(http.MethodPost, path, `{"resolution":"no action needed"}`, actor, false); w.Code != http.StatusOK {
		t.Fatalf("plain resolve: %d %s", w.Code, w.Body.String())
	}
	rg.takeHits()
	rg.takeAudit()
	// A holder without the permission is refused before the body is judged.
	other := uuid.NewString()
	rg.perms.grant(other, permDoorstepIncidentsRead)
	if w := rg.do(http.MethodPost, path, `not json`, other, true); w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
		t.Fatalf("no permission: %d %s", w.Code, w.Body.String())
	}
}

// A skill decision is audited against the professional, with the code.
func TestDoorstepSkillVerify_AuditsTheProfessional(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permDoorstepProsApprove)
	pro := uuid.NewString()
	w := rg.do(http.MethodPost, doorstepPrefix+"/professionals/"+pro+"/skills/ac_service/verify", `{"verified":true}`, actor, false)
	hits := rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 1 || hits[0].path != service.DoorstepAdminPrefix+"/professionals/"+pro+"/skills/ac_service/verify" {
		t.Fatalf("%d %+v", w.Code, hits)
	}
	a := rg.takeAudit()
	if len(a) != 1 || a[0].Actor != actor || a[0].TargetType != "doorstep_professional" || a[0].TargetID != pro ||
		a[0].Payload["skill_code"] != "ac_service" {
		t.Fatalf("audit %+v", a)
	}
}

// Without the signing key every Doorstep route answers 503 and is audited.
func TestDoorstepRoutes_NoKeyIs503(t *testing.T) {
	rg := newProductsRig(t, false)
	actor := uuid.NewString()
	rg.perms.grant(actor, permDoorstepBookingsRead)
	w := rg.do(http.MethodGet, doorstepPrefix+"/bookings", "", actor, false)
	if w.Code != http.StatusServiceUnavailable || !hasCode(w, CodeProductUnavailable) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure || a[0].Actor != actor {
		t.Fatalf("audit %+v", a)
	}
}

// An upstream that is not built yet answers with its own status, passed
// through and audited (the console handles 404/501 as "not yet").
func TestDoorstepRoutes_UnbuiltUpstreamPassesThrough(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permDoorstepSettlementsRead)
	rg.on(http.MethodGet, service.DoorstepAdminPrefix+"/settlements", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":{"code":"NOT_IMPLEMENTED","message":"not yet"}}`))
	})
	w := rg.do(http.MethodGet, doorstepPrefix+"/settlements", "", actor, false)
	if w.Code != http.StatusNotImplemented || !hasCode(w, "NOT_IMPLEMENTED") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Actor != actor || a[0].Operation != "doorstep.settlements.list" {
		t.Fatalf("audit %+v", a)
	}
}

// Doorstep appears in /me only for a holder of a doorstep permission, right
// after Mopedu.
func TestMe_DoorstepNavigation(t *testing.T) {
	rg := newProductsRig(t, true)
	for name, tc := range map[string]struct {
		perms []string
		want  bool
	}{
		"doorstep read": {[]string{permDoorstepBookingsRead}, true},
		"mopedu only":   {[]string{permRiderRidesRead}, false},
		"platform-wide": {[]string{"*:audit.read"}, false},
	} {
		actor := uuid.NewString()
		rg.perms.grant(actor, tc.perms...)
		w := rg.do(http.MethodGet, "/v1/admin/me", "", actor, false)
		if got := strings.Contains(w.Body.String(), `{"app":"doorstep","label":"Doorstep"}`); got != tc.want {
			t.Fatalf("%s: Doorstep in navigation = %v, want %v (%s)", name, got, tc.want, w.Body.String())
		}
	}
	both := uuid.NewString()
	rg.perms.grant(both, permRiderRidesRead, permDoorstepStatsRead, permTrustAppealsAct)
	w := rg.do(http.MethodGet, "/v1/admin/me", "", both, false)
	if !strings.Contains(w.Body.String(), `{"app":"rider","label":"Mopedu"},{"app":"doorstep","label":"Doorstep"},{"app":"trust_safety"`) {
		t.Fatalf("navigation order %s", w.Body.String())
	}
}
