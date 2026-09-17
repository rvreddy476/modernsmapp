package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
)

const (
	accessTarget     = "4d3b2a1c-0f9e-4d8c-8b7a-6f5e4d3c2b1a"
	accessGrantPath  = accessPrefix + "/users/" + accessTarget + "/roles"
	accessRevokePath = accessPrefix + "/users/" + accessTarget + "/roles/superadmin"
	superGrantBody   = `{"role":"superadmin","reason":"second founder-level admin"}`
	modGrantBody     = `{"role":"moderator","app":"dating","reason":"new dating moderator"}`
)

// accessCase gives each route a body identity would accept.
func accessCase(rt productRoute) string {
	switch rt.operation {
	case opAccessRoleGrant:
		return modGrantBody
	case opAccessRoleRevoke:
		return `{"app":"dating","reason":"left the team"}`
	case opAccessSessionsRevoke:
		return `{"reason":"device reported stolen"}`
	}
	return ""
}

var accessStepUp = map[string]bool{opAccessRoleGrant: true, opAccessRoleRevoke: true, opAccessSessionsRevoke: true}

// Every forwarded route requires exactly its permission, refuses an admin
// holding every OTHER platform permission before identity is called, and
// signs a token for audience "identity" scoped to that permission, acting as
// the admin, with one audit row per call.
func TestAccessRoutes_EachRequiresItsPermission_AndSignsForIdentity(t *testing.T) {
	rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
	if len(AccessRoutes) != 8 {
		t.Fatalf("AccessRoutes has %d entries, want 8 (identity's 7 console routes plus the catalogue)", len(AccessRoutes))
	}
	seen := map[string]bool{}
	for _, rt := range AccessRoutes {
		if seen[rt.operation] {
			t.Fatalf("operation %s is declared twice", rt.operation)
		}
		seen[rt.operation] = true
		if !strings.HasPrefix(rt.permission, "platform:") {
			t.Fatalf("%s carries %q, not a platform permission", rt.operation, rt.permission)
		}
		if rt.operation == opAccessCatalogue {
			continue // served locally; its own test below
		}
		t.Run(rt.operation+" "+rt.method+" "+rt.path, func(t *testing.T) {
			routeTableCase(t, rg, accessPrefix, service.IdentityAdminPrefix, "identity", accessAuditApp, identityAll, rt, accessCase(rt))
		})
	}
}

// The token's audience is identity and its jti is fresh on every call, so
// identity's single-use check never sees the same jti twice.
func TestAccessRoutes_TokenAudienceActorScope_AndFreshJTIPerCall(t *testing.T) {
	rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
	actor := uuid.NewString()
	rg.perms.grant(actor, permPlatformRolesRead)
	for i := 0; i < 2; i++ {
		if w := rg.do(http.MethodGet, accessPrefix+"/roles?role=moderator&app=dating", "", actor, false); w.Code != http.StatusOK {
			t.Fatalf("call %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	hits := rg.takeHits()
	if len(hits) != 2 {
		t.Fatalf("identity hits = %d, want 2", len(hits))
	}
	for _, h := range hits {
		// The stub's verifier accepts audience "identity" only, so verifyErr nil
		// is the audience check.
		if h.verifyErr != nil || h.aud != "identity" || h.verified.Issuer != "admin-service" ||
			len(h.verified.Scope) != 1 || h.verified.Scope[0] != permPlatformRolesRead || h.verified.Actor != actor ||
			h.verified.JTI == "" || h.query != "role=moderator&app=dating" || h.path != service.IdentityAdminPrefix+"/roles" {
			t.Fatalf("hit %+v", h)
		}
	}
	if hits[0].verified.JTI == hits[1].verified.JTI {
		t.Fatalf("two consecutive calls carried the same jti %q; identity would refuse the second as a replay", hits[0].verified.JTI)
	}
	if a := rg.takeAudit(); len(a) != 2 || a[0].Operation != opAccessRolesList || a[1].Outcome != postgres.AuditOutcomeSuccess {
		t.Fatalf("audit %+v", a)
	}
}

// Every write is step-up; every read is not.
func TestAccessRoutes_StepUp(t *testing.T) {
	rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
	for _, rt := range AccessRoutes {
		want := accessStepUp[rt.operation]
		if rt.stepUp != want {
			t.Fatalf("%s declares step-up=%v, want %v", rt.operation, rt.stepUp, want)
		}
		if (rt.method == http.MethodGet) == want {
			t.Fatalf("%s: a %s route with step-up=%v", rt.operation, rt.method, want)
		}
		stepUpCase(t, rg, accessPrefix, identityAll, rt, accessCase(rt), want)
	}
}

// A non-superadmin role change is step-up only: it forwards at once, as
// sent, and never asks identity for a second holder.
func TestAccessGrant_OtherRolesAreStepUpOnly(t *testing.T) {
	rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
	rg.holders.n = 1
	actor := uuid.NewString()
	rg.perms.grant(actor, permPlatformRolesManage)
	w := rg.do(http.MethodPost, accessGrantPath, modGrantBody, actor, true)
	hits := rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 1 || hits[0].method != http.MethodPost ||
		hits[0].path != service.IdentityAdminPrefix+"/users/"+accessTarget+"/roles" || hits[0].body != modGrantBody ||
		hits[0].verified.Scope[0] != permPlatformRolesManage || hits[0].verified.Actor != actor {
		t.Fatalf("grant moderator: %d %s hits=%+v", w.Code, w.Body.String(), hits)
	}
	if rg.holders.calls != 0 {
		t.Fatalf("a moderator grant asked identity for a second holder")
	}
	a := rg.takeAudit()
	if len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeSuccess || a[0].TargetType != accessTargetUser || a[0].TargetID != accessTarget ||
		a[0].Reason != "new dating moderator" || a[0].Payload["role"] != "moderator" || a[0].Payload["app"] != "dating" || a[0].Payload["approval"] != nil {
		t.Fatalf("audit %+v", a)
	}

	// Revoke: the JSON body {app, reason} reaches identity as sent, on the
	// same path, and the audit target is the user (not the role segment).
	rg.rec.entries = nil
	w = rg.do(http.MethodDelete, accessPrefix+"/users/"+accessTarget+"/roles/moderator", `{"app":"dating","reason":"left"}`, actor, true)
	hits = rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 1 || hits[0].method != http.MethodDelete ||
		hits[0].path != service.IdentityAdminPrefix+"/users/"+accessTarget+"/roles/moderator" || hits[0].body != `{"app":"dating","reason":"left"}` {
		t.Fatalf("revoke moderator: %d hits=%+v", w.Code, hits)
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].TargetType != accessTargetUser || a[0].TargetID != accessTarget || a[0].Reason != "left" ||
		a[0].Payload["role"] != "moderator" || a[0].Payload["app"] != "dating" {
		t.Fatalf("revoke audit %+v", a)
	}
	if rg.holders.calls != 0 {
		t.Fatalf("a moderator revoke asked identity for a second holder")
	}
}

// Granting superadmin is two-person: with another TOTP-enrolled holder of
// platform:roles.manage the first call creates a pending approval and
// identity is not called; the second holder's approval executes the stored
// request exactly once, as the approver.
func TestAccessGrantSuperadmin_PendingWithASecondHolder_ExecutedByApprover(t *testing.T) {
	for name, tc := range map[string]struct {
		method, path, body, upstreamPath, upstreamMethod string
		op                                               string
		wantBody                                         map[string]string
	}{
		"grant": {http.MethodPost, accessGrantPath, superGrantBody, "/users/" + accessTarget + "/roles", http.MethodPost, opAccessRoleGrant,
			map[string]string{"role": "superadmin", "reason": "second founder-level admin"}},
		"revoke": {http.MethodDelete, accessRevokePath, `{"reason":"stepped down"}`, "/users/" + accessTarget + "/roles/superadmin", http.MethodDelete, opAccessRoleRevoke,
			map[string]string{"reason": "stepped down"}},
	} {
		t.Run(name, func(t *testing.T) {
			rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
			rg.holders.n = 1
			rg.perms.grant(adminA, permPlatformRolesManage)
			rg.perms.grant(adminB, permPlatformRolesManage)

			w := rg.do(tc.method, tc.path, tc.body, adminA, true)
			if w.Code != http.StatusAccepted {
				t.Fatalf("first call: %d %s, want 202", w.Code, w.Body.String())
			}
			if hits := rg.takeHits(); len(hits) != 0 {
				t.Fatalf("the first superadmin's call reached identity: %+v", hits)
			}
			if rg.holders.lastPerm != permPlatformRolesManage || rg.holders.lastExclude != adminA {
				t.Fatalf("holders asked for %q excluding %q", rg.holders.lastPerm, rg.holders.lastExclude)
			}
			appr := decodeApproval(t, w)
			if appr.Status != approvals.StatusPending || appr.App != accessAuditApp || appr.Operation != tc.op ||
				appr.RequiredPermission != permPlatformRolesManage || appr.TargetType != accessTargetUser || appr.TargetID != accessTarget ||
				!strings.Contains(appr.Summary, "superadmin") {
				t.Fatalf("approval %+v", appr)
			}
			if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomePending || a[0].Payload["approval"] != "requested" ||
				a[0].Payload["role"] != "superadmin" || a[0].TargetID != accessTarget {
				t.Fatalf("pending audit %+v", a)
			}

			// The requester cannot approve; the second holder can, once.
			if w := rg.do(http.MethodPost, "/v1/admin/approvals/"+appr.ID+"/approve", `{"reason":"agreed"}`, adminA, true); w.Code != http.StatusForbidden || !hasCode(w, CodeSelfApproval) {
				t.Fatalf("self-approval: %d %s", w.Code, w.Body.String())
			}
			rg.takeAudit()
			w = rg.do(http.MethodPost, "/v1/admin/approvals/"+appr.ID+"/approve", `{"reason":"agreed"}`, adminB, true)
			hits := rg.takeHits()
			if w.Code != http.StatusOK || len(hits) != 1 {
				t.Fatalf("approve: %d %s hits=%+v", w.Code, w.Body.String(), hits)
			}
			h := hits[0]
			if h.method != tc.upstreamMethod || h.path != service.IdentityAdminPrefix+tc.upstreamPath || h.verified.Actor != adminB ||
				h.verified.Scope[0] != permPlatformRolesManage || h.aud != "identity" {
				t.Fatalf("identity saw %+v", h)
			}
			var sent map[string]string
			if err := json.Unmarshal([]byte(h.body), &sent); err != nil || len(sent) != len(tc.wantBody) {
				t.Fatalf("identity body %q", h.body)
			}
			for k, v := range tc.wantBody {
				if sent[k] != v {
					t.Fatalf("identity body %q, want %v", h.body, tc.wantBody)
				}
			}
			if a := rg.takeAudit(); len(a) != 1 || a[0].Actor != adminB || a[0].Operation != tc.op || a[0].Outcome != postgres.AuditOutcomeSuccess ||
				a[0].Payload["approval"] != "approved" || a[0].Payload["requester"] != adminA || a[0].TargetID != accessTarget {
				t.Fatalf("approved audit %+v", a)
			}
			got, _ := rg.store.GetApproval(context.Background(), appr.ID)
			if got.Status != approvals.StatusExecuted {
				t.Fatalf("stored status %s", got.Status)
			}
		})
	}
}

// Without another TOTP-enrolled holder of platform:roles.manage the
// sole-holder path runs at once, as the requester, and says so in the audit
// row.
func TestAccessGrantSuperadmin_SoleHolderExecutesAndIsRecorded(t *testing.T) {
	rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
	rg.holders.n = 0
	rg.perms.grant(adminA, permPlatformRolesManage)
	w := rg.do(http.MethodPost, accessGrantPath, superGrantBody, adminA, true)
	hits := rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 1 || hits[0].verified.Actor != adminA || hits[0].path != service.IdentityAdminPrefix+"/users/"+accessTarget+"/roles" ||
		!strings.Contains(hits[0].body, `"role":"superadmin"`) || !strings.Contains(hits[0].body, `"reason":"second founder-level admin"`) {
		t.Fatalf("sole holder: %d %s hits=%+v", w.Code, w.Body.String(), hits)
	}
	if rg.holders.calls != 1 || rg.holders.lastPerm != permPlatformRolesManage {
		t.Fatalf("holders calls=%d perm=%q", rg.holders.calls, rg.holders.lastPerm)
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeSuccess || a[0].Payload["approval"] != "sole_holder" || a[0].Payload["role"] != "superadmin" {
		t.Fatalf("audit %+v", a)
	}

	// A superadmin change needs a reason before anything is looked up.
	w = rg.do(http.MethodPost, accessGrantPath, `{"role":"superadmin"}`, adminA, true)
	if w.Code != http.StatusBadRequest || !hasCode(w, CodeReasonRequired) || len(rg.takeHits()) != 0 {
		t.Fatalf("no reason: %d %s", w.Code, w.Body.String())
	}
	// Without a step-up the two-person path is never reached.
	rg.takeAudit()
	w = rg.do(http.MethodPost, accessGrantPath, superGrantBody, adminA, false)
	if w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeStepUpRequired) || len(rg.takeHits()) != 0 || rg.holders.calls != 1 {
		t.Fatalf("no step-up: %d %s holders=%d", w.Code, w.Body.String(), rg.holders.calls)
	}
}

// Identity's own refusals reach the console unchanged: status and code.
func TestAccessRoutes_IdentityErrorCodesPassThrough(t *testing.T) {
	rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
	actor := uuid.NewString()
	rg.perms.grant(actor, permPlatformRolesManage, permPlatformSessionsRevoke)
	for code, status := range map[string]int{
		"SUPERADMIN_REQUIRED": http.StatusForbidden, "SELF_GRANT_REFUSED": http.StatusForbidden,
		"LAST_SUPERADMIN": http.StatusConflict, "ENV_BOOTSTRAP_ROLE": http.StatusConflict,
		"REASON_REQUIRED": http.StatusBadRequest, "INVALID_APP": http.StatusBadRequest,
		"ROLE_NOT_SCOPABLE": http.StatusBadRequest, "INVALID_EXPIRY": http.StatusBadRequest,
	} {
		rg.on(http.MethodPost, service.IdentityAdminPrefix+"/users/"+accessTarget+"/roles", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"from identity"}}`))
		})
		w := rg.do(http.MethodPost, accessGrantPath, modGrantBody, actor, true)
		if w.Code != status || !hasCode(w, code) {
			t.Fatalf("%s: console answered %d %s", code, w.Code, w.Body.String())
		}
		rg.takeHits()
		if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure || a[0].StatusCode != status {
			t.Fatalf("%s: audit %+v", code, a)
		}
	}
}

// Without the signing key every Access route answers 503 and is audited.
func TestAccessRoutes_NoKeyIs503(t *testing.T) {
	rg := newProductsRig(t, false, DefaultRefundTwoPersonThresholdPaise)
	actor := uuid.NewString()
	rg.perms.grant(actor, permPlatformRolesRead)
	w := rg.do(http.MethodGet, accessPrefix+"/roles", "", actor, false)
	if w.Code != http.StatusServiceUnavailable || !hasCode(w, CodeProductUnavailable) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure {
		t.Fatalf("audit %+v", a)
	}
}

// The catalogue is admin-service's mirror of identity's: it needs
// platform:roles.read, is not forwarded, and names every role, app and the
// role → permissions map; superadmin is platform-only.
func TestAccessCatalogue_FromTheMirror(t *testing.T) {
	rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
	other := uuid.NewString()
	rg.perms.grant(other, permPlatformRolesManage, permPlatformUsersSearch)
	if w := rg.do(http.MethodGet, accessPrefix+"/catalogue", "", other, false); w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
		t.Fatalf("catalogue without roles.read: %d %s", w.Code, w.Body.String())
	}
	rg.takeAudit()
	actor := uuid.NewString()
	rg.perms.grant(actor, permPlatformRolesRead)
	w := rg.do(http.MethodGet, accessPrefix+"/catalogue", "", actor, false)
	if w.Code != http.StatusOK || len(rg.takeHits()) != 0 {
		t.Fatalf("catalogue: %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data adminauth.CatalogueResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	cat := env.Data
	if !strings.Contains(cat.Source, "mirror") || len(cat.Roles) != 7 || cat.Roles[0].Role != adminauth.RoleSuperadmin || !cat.Roles[0].PlatformOnly ||
		cat.Roles[1].PlatformOnly || len(cat.Apps) != 13 || cat.Apps[12] != adminauth.AppPlatform {
		t.Fatalf("catalogue %+v", cat)
	}
	if got := cat.RolePermissions[adminauth.RoleAuditor]["platform"]; strings.Join(got, ",") != "platform:audit.read,platform:roles.read" {
		t.Fatalf("auditor platform permissions %v", got)
	}
	if got := cat.RolePermissions[adminauth.RoleModerator]["payments"]; len(got) != 0 {
		t.Fatalf("moderator holds payments permissions %v", got)
	}
	if got := cat.RolePermissions[adminauth.RoleAdmin]["platform"]; strings.Contains(strings.Join(got, ","), "roles.manage") {
		t.Fatalf("admin holds roles.manage: %v", got)
	}
	var manage adminauth.PermissionInfo
	for _, pi := range cat.Permissions["platform"] {
		if pi.Permission == permPlatformRolesManage {
			manage = pi
		}
	}
	if strings.Join(manage.Roles, ",") != adminauth.RoleSuperadmin {
		t.Fatalf("roles.manage holders %v", manage.Roles)
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Operation != opAccessCatalogue || a[0].Outcome != postgres.AuditOutcomeSuccess {
		t.Fatalf("audit %+v", a)
	}
}

// Every permission a console route declares is in the mirror, so the page can
// only offer roles whose grants actually open something here.
func TestCatalogue_CoversEveryDeclaredPermission(t *testing.T) {
	rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
	var missing []string
	for _, ri := range rg.r.Routes() {
		req, ok := rg.gate.Requirement(ri.Method, ri.Path)
		if !ok || req.Permission == "" {
			continue
		}
		if !adminauth.KnownPermission(req.Permission) {
			missing = append(missing, ri.Method+" "+ri.Path+" "+req.Permission)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("permissions declared on routes but absent from the catalogue mirror:\n%s", strings.Join(missing, "\n"))
	}
}

// Access appears in /me for a holder of any platform:roles.* /
// sessions.revoke / users.search permission, and for nobody else.
func TestMe_AccessOnlyWithAnAccessPermission(t *testing.T) {
	rg := newProductsRig(t, true, DefaultRefundTwoPersonThresholdPaise)
	for name, tc := range map[string]struct {
		perms []string
		want  bool
	}{
		"roles.read":      {[]string{permPlatformRolesRead}, true},
		"roles.manage":    {[]string{permPlatformRolesManage}, true},
		"sessions.revoke": {[]string{permPlatformSessionsRevoke}, true},
		"users.search":    {[]string{permPlatformUsersSearch}, true},
		"users.read only": {[]string{"platform:users.read"}, false},
		"cross-app audit": {[]string{"*:audit.read"}, false},
		"feast only":      {[]string{permFoodOrdersRead}, false},
	} {
		actor := uuid.NewString()
		rg.perms.grant(actor, tc.perms...)
		w := rg.do(http.MethodGet, "/v1/admin/me", "", actor, false)
		got := strings.Contains(w.Body.String(), `{"app":"access","label":"Access"}`)
		if got != tc.want {
			t.Fatalf("%s: Access in navigation = %v, want %v (%s)", name, got, tc.want, w.Body.String())
		}
		if tc.want && !strings.Contains(w.Body.String(), `{"app":"platform","label":"Platform"},{"app":"access","label":"Access"}`) {
			t.Fatalf("%s: Access does not follow Platform: %s", name, w.Body.String())
		}
	}
}
