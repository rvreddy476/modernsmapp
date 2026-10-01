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

// liveCase gives each Live route a body live-service-v2 would accept.
func liveCase(rt productRoute) string {
	switch rt.operation {
	case opLiveReportResolve:
		return `{"action":"dismiss","reason":"not a violation"}`
	case opLiveStreamStop:
		return `{"reason":"violent content"}`
	case opLiveUserBan, opLiveUserUnban:
		return `{"reason":"repeated harassment"}`
	}
	return ""
}

// liveStepUp is every Live operation that must be declared step-up: stop,
// report actions, and the live ban in both directions.
var liveStepUp = map[string]bool{opLiveStreamStop: true, opLiveReportResolve: true, opLiveUserBan: true, opLiveUserUnban: true}

// liveUpstream is section 1 of the live contract: method, live-service-v2
// path under /v1/livestream/internal/admin, the permission it checks, and the
// audit row's target type.
var liveUpstream = []struct{ method, path, perm, target string }{
	{http.MethodGet, "/streams", "live:streams.read", ""},
	{http.MethodPost, "/streams/:streamId/stop", "live:streams.stop", "live_stream"},
	{http.MethodDelete, "/streams/:streamId/chat/:messageId", "live:chat.moderate", "live_chat_message"},
	{http.MethodGet, "/reports", "live:reports.read", ""},
	{http.MethodPost, "/reports/:reportId/resolve", "live:reports.act", "live_report"},
	{http.MethodPost, "/users/:userId/live-ban", "live:users.ban", "user"},
	{http.MethodDelete, "/users/:userId/live-ban", "live:users.ban", "user"},
	{http.MethodGet, "/bans", "live:users.ban", ""},
}

func TestLiveRoutes_MatchTheContract(t *testing.T) {
	if len(LiveRoutes) != len(liveUpstream) {
		t.Fatalf("LiveRoutes has %d entries, want %d", len(LiveRoutes), len(liveUpstream))
	}
	for i, want := range liveUpstream {
		rt := LiveRoutes[i]
		up := rt.upstream
		if up == "" {
			up = rt.path
		}
		if rt.method != want.method || up != want.path || rt.permission != want.perm || rt.targetType != want.target {
			t.Fatalf("route %d: %s %s %s %s, want %s %s %s %s", i, rt.method, up, rt.permission, rt.targetType, want.method, want.path, want.perm, want.target)
		}
		if rt.twoPerson || rt.mayTwoPerson || rt.idempotent {
			t.Fatalf("%s declares two-person/idempotent", rt.operation)
		}
	}
	if service.LiveAudience != "live" || service.LiveAdminPrefix != "/v1/livestream/internal/admin" {
		t.Fatalf("live client: %q %q", service.LiveAudience, service.LiveAdminPrefix)
	}
}

func TestLiveRoutes_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newProductsRig(t, true)
	for _, rt := range LiveRoutes {
		t.Run(rt.operation+" "+rt.method+" "+rt.path, func(t *testing.T) {
			routeTableCase(t, rg, livePrefix, service.LiveAdminPrefix, "live", "live", liveAll, rt, liveCase(rt))
		})
	}
}

func TestLiveRoutes_StepUp(t *testing.T) {
	rg := newProductsRig(t, true)
	found := map[string]bool{}
	for _, rt := range LiveRoutes {
		want := liveStepUp[rt.operation]
		if rt.stepUp != want {
			t.Fatalf("%s declares step-up=%v, want %v", rt.operation, rt.stepUp, want)
		}
		found[rt.operation] = true
		stepUpCase(t, rg, livePrefix, liveAll, rt, liveCase(rt), want)
	}
	for op := range liveStepUp {
		if !found[op] {
			t.Fatalf("%s is not declared", op)
		}
	}
}

// A refused step-up is audited as denied and nothing is signed.
func TestLiveStepUp_RefusalIsAudited(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, liveAll...)
	for _, rt := range LiveRoutes {
		if !rt.stepUp {
			continue
		}
		path, _ := fill(rt.path)
		w := rg.do(rt.method, livePrefix+path, liveCase(rt), actor, false)
		if w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeStepUpRequired) || len(rg.takeHits()) != 0 {
			t.Fatalf("%s: %d %s", rt.operation, w.Code, w.Body.String())
		}
		if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeDenied || a[0].Operation != rt.operation {
			t.Fatalf("%s audit %+v", rt.operation, a)
		}
	}
}

// Stop, resolve and both ban directions need a reason; resolve also needs an
// action live-service-v2 knows. Refused before the call, audited as denied.
func TestLiveWrites_ReasonAndActionBeforeTheCall(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, liveAll...)
	long := strings.Repeat("x", maxReasonLength+1)
	for _, rt := range LiveRoutes {
		if !liveStepUp[rt.operation] {
			continue
		}
		path, _ := fill(rt.path)
		bad := []string{``, `{}`, `{"reason":""}`, `{"reason":"   "}`, `{"reason":"` + long + `"}`, `not json`}
		if rt.operation == opLiveReportResolve {
			bad = append(bad, `{"reason":"spam"}`, `{"action":"delete_stream","reason":"spam"}`, `{"action":"","reason":"spam"}`, `{"action":"dismiss"}`)
		}
		for _, body := range bad {
			w := rg.do(rt.method, livePrefix+path, body, actor, true)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s %q: %d %s", rt.operation, body, w.Code, w.Body.String())
			}
			if body == `{}` && !hasCode(w, CodeReasonRequired) {
				t.Fatalf("%s: %s, want %s", rt.operation, w.Body.String(), CodeReasonRequired)
			}
			if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeDenied {
				t.Fatalf("%s %q audit %+v", rt.operation, body, a)
			}
		}
		if hits := rg.takeHits(); len(hits) != 0 {
			t.Fatalf("%s: a refused write reached live-service-v2: %+v", rt.operation, hits)
		}
	}
	// Every resolve action is accepted.
	for action := range liveReportActions {
		w := rg.do(http.MethodPost, livePrefix+"/reports/"+uuid.NewString()+"/resolve", `{"action":"`+action+`","reason":"checked"}`, actor, true)
		if w.Code != http.StatusOK || len(rg.takeHits()) != 1 {
			t.Fatalf("resolve %s: %d %s", action, w.Code, w.Body.String())
		}
		if a := rg.takeAudit(); len(a) != 1 || a[0].Payload["action"] != action {
			t.Fatalf("resolve %s audit %+v", action, a)
		}
	}
}

// A resolution that removes a message needs live:chat.moderate as well, one
// that bans the user needs live:users.ban as well: refused 403 and audited as
// denied otherwise, and when held the extra permission joins the token scope.
func TestLiveResolve_ActionNeedsItsOwnPermission(t *testing.T) {
	rg := newProductsRig(t, true)
	type want struct {
		status int
		scope  string // the token's scope when forwarded
		denied string // required_permission on the denial row
	}
	for name, tc := range map[string]struct {
		perms []string
		cases map[string]want
	}{
		"reports.act only": {[]string{permLiveReportsAct}, map[string]want{
			"dismiss":        {http.StatusOK, "live:reports.act", ""},
			"remove_message": {http.StatusForbidden, "", permLiveChatModerate},
			"ban_user":       {http.StatusForbidden, "", permLiveUsersBan},
		}},
		"reports.act + users.ban": {[]string{permLiveReportsAct, permLiveUsersBan}, map[string]want{
			"ban_user":       {http.StatusOK, "live:reports.act,live:users.ban", ""},
			"remove_message": {http.StatusForbidden, "", permLiveChatModerate},
			"dismiss":        {http.StatusOK, "live:reports.act", ""},
		}},
		"reports.act + chat.moderate": {[]string{permLiveReportsAct, permLiveChatModerate}, map[string]want{
			"remove_message": {http.StatusOK, "live:reports.act,live:chat.moderate", ""},
			"ban_user":       {http.StatusForbidden, "", permLiveUsersBan},
		}},
		"everything but reports.act": {[]string{permLiveUsersBan, permLiveChatModerate, permLiveStreamsRead}, map[string]want{
			"dismiss":        {http.StatusForbidden, "", permLiveReportsAct},
			"remove_message": {http.StatusForbidden, "", permLiveReportsAct},
			"ban_user":       {http.StatusForbidden, "", permLiveReportsAct},
		}},
	} {
		actor := uuid.NewString()
		rg.perms.grant(actor, tc.perms...)
		for action, wt := range tc.cases {
			report := uuid.NewString()
			w := rg.do(http.MethodPost, livePrefix+"/reports/"+report+"/resolve", `{"action":"`+action+`","reason":"checked"}`, actor, true)
			hits, audit := rg.takeHits(), rg.takeAudit()
			if w.Code != wt.status {
				t.Fatalf("%s %s: %d %s", name, action, w.Code, w.Body.String())
			}
			if len(audit) != 1 {
				t.Fatalf("%s %s: audit rows %+v", name, action, audit)
			}
			if wt.status == http.StatusForbidden {
				if !hasCode(w, CodePermissionDenied) || len(hits) != 0 {
					t.Fatalf("%s %s: %s hits %d", name, action, w.Body.String(), len(hits))
				}
				if audit[0].Outcome != postgres.AuditOutcomeDenied || audit[0].Payload["required_permission"] != wt.denied {
					t.Fatalf("%s %s: denial audit %+v", name, action, audit[0])
				}
				continue
			}
			if len(hits) != 1 || hits[0].verifyErr != nil || strings.Join(hits[0].verified.Scope, ",") != wt.scope {
				t.Fatalf("%s %s: hits %+v", name, action, hits)
			}
			if audit[0].Outcome != postgres.AuditOutcomeSuccess || audit[0].Payload["action"] != action || audit[0].TargetID != report {
				t.Fatalf("%s %s: audit %+v", name, action, audit[0])
			}
		}
	}
}

// Removing a chat message needs no reason and no step-up; a reason, when
// sent, is forwarded and audited.
func TestLiveChatRemove_NoReasonNeeded(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permLiveChatModerate)
	stream, msg := uuid.NewString(), uuid.NewString()
	w := rg.do(http.MethodDelete, livePrefix+"/streams/"+stream+"/chat/"+msg, "", actor, false)
	hits := rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 1 || hits[0].body != "" ||
		hits[0].path != service.LiveAdminPrefix+"/streams/"+stream+"/chat/"+msg {
		t.Fatalf("%d %s hits %+v", w.Code, w.Body.String(), hits)
	}
	a := rg.takeAudit()
	if len(a) != 1 || a[0].TargetType != "live_chat_message" || a[0].TargetID != msg || a[0].Outcome != postgres.AuditOutcomeSuccess {
		t.Fatalf("audit %+v", a)
	}
	w = rg.do(http.MethodDelete, livePrefix+"/streams/"+stream+"/chat/"+msg, `{"reason":"slur"}`, actor, false)
	hits = rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 1 || hits[0].body != `{"reason":"slur"}` {
		t.Fatalf("%d hits %+v", w.Code, hits)
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Reason != "slur" {
		t.Fatalf("audit %+v", a)
	}
}

// Query strings and bodies reach live-service-v2 as the console sent them,
// and each call writes exactly one audit row naming its target and reason.
func TestLive_BodyAndQueryForwarded_OneAuditRow(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, liveAll...)

	for _, c := range []struct{ path, query string }{
		{"/streams", "status=live"},
		{"/streams", "status=all&limit=20"},
		{"/reports", "status=open"},
		{"/bans", "limit=50"},
	} {
		w := rg.do(http.MethodGet, livePrefix+c.path+"?"+c.query, "", actor, false)
		hits := rg.takeHits()
		if w.Code != http.StatusOK || len(hits) != 1 || hits[0].query != c.query || hits[0].path != service.LiveAdminPrefix+c.path || hits[0].method != http.MethodGet {
			t.Fatalf("%s?%s: %d hits %+v", c.path, c.query, w.Code, hits)
		}
		if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeSuccess {
			t.Fatalf("%s audit %+v", c.path, a)
		}
	}

	for _, rt := range LiveRoutes {
		if !liveStepUp[rt.operation] {
			continue
		}
		path, vals := fill(rt.path)
		body := liveCase(rt)
		w := rg.do(rt.method, livePrefix+path, body, actor, true)
		hits := rg.takeHits()
		if w.Code != http.StatusOK || len(hits) != 1 {
			t.Fatalf("%s: %d hits %d", rt.operation, w.Code, len(hits))
		}
		h := hits[0]
		if h.method != rt.method || h.path != service.LiveAdminPrefix+path || h.body != body {
			t.Fatalf("%s: forwarded %s %s %q", rt.operation, h.method, h.path, h.body)
		}
		a := rg.takeAudit()
		var target string
		for _, v := range vals {
			target = v // every step-up route has exactly one :param
		}
		if len(a) != 1 || a[0].TargetID != target || a[0].TargetType != rt.targetType || a[0].Reason == "" || a[0].App != "live" || a[0].Actor != actor {
			t.Fatalf("%s audit %+v", rt.operation, a)
		}
	}
}

// live-service-v2's own answers pass through: its 4xx/5xx body and status
// reach the console and the audit row is a failure with that status; a
// dropped connection is 502 UPSTREAM_ERROR with status 0 on the row.
func TestLive_UpstreamErrorsMapped(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, liveAll...)
	for _, rt := range LiveRoutes {
		path, _ := fill(rt.path)
		prod := service.LiveAdminPrefix + path
		for _, c := range []struct {
			status int
			body   string
		}{
			{http.StatusNotFound, `{"error":{"code":"STREAM_NOT_FOUND","message":"no such stream"}}`},
			{http.StatusConflict, `{"error":{"code":"STREAM_ALREADY_ENDED","message":"ended"}}`},
			{http.StatusForbidden, `{"error":{"code":"FORBIDDEN","message":"scope"}}`},
			{http.StatusInternalServerError, `{"error":{"code":"INTERNAL_ERROR","message":"boom"}}`},
		} {
			rg.on(rt.method, prod, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			})
			w := rg.do(rt.method, livePrefix+path, liveCase(rt), actor, true)
			rg.takeHits()
			if w.Code != c.status || w.Body.String() != c.body {
				t.Fatalf("%s %d: got %d %s", rt.operation, c.status, w.Code, w.Body.String())
			}
			if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure || a[0].StatusCode != c.status {
				t.Fatalf("%s %d audit %+v", rt.operation, c.status, a)
			}
		}
		rg.on(rt.method, prod, func(w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		})
		w := rg.do(rt.method, livePrefix+path, liveCase(rt), actor, true)
		rg.takeHits()
		if w.Code != http.StatusBadGateway || !hasCode(w, "UPSTREAM_ERROR") {
			t.Fatalf("%s transport error: %d %s", rt.operation, w.Code, w.Body.String())
		}
		if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure || a[0].StatusCode != 0 {
			t.Fatalf("%s transport audit %+v", rt.operation, a)
		}
	}
}

// Without the signing key every Live route answers 503 and is audited.
func TestLive_NoKeyIs503(t *testing.T) {
	rg := newProductsRig(t, false)
	actor := uuid.NewString()
	rg.perms.grant(actor, liveAll...)
	for _, rt := range LiveRoutes {
		path, _ := fill(rt.path)
		w := rg.do(rt.method, livePrefix+path, liveCase(rt), actor, true)
		if w.Code != http.StatusServiceUnavailable || !hasCode(w, CodeProductUnavailable) || len(rg.takeHits()) != 0 {
			t.Fatalf("%s: %d %s", rt.operation, w.Code, w.Body.String())
		}
		if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure {
			t.Fatalf("%s audit %+v", rt.operation, a)
		}
	}
}

// Ids that could climb out of their segment are refused before any call.
func TestLive_PathParamsCannotEscape(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, liveAll...)
	for _, p := range []string{
		livePrefix + "/streams/..%2fusers/stop",
		livePrefix + "/users/a%2fb/live-ban",
	} {
		w := rg.do(http.MethodPost, p, `{"reason":"x"}`, actor, true)
		if w.Code == http.StatusOK || len(rg.takeHits()) != 0 {
			t.Fatalf("%s: %d reached live-service-v2", p, w.Code)
		}
		rg.takeAudit()
	}
}

// The mirror matches the contract's grant table: superadmin and admin hold
// all six, moderators exactly four, nobody else any.
func TestCatalogue_LiveGrants(t *testing.T) {
	cat := adminauth.Catalogue()
	found := false
	for _, a := range cat.Apps {
		found = found || a == adminauth.AppLive
	}
	if !found || len(cat.Permissions[adminauth.AppLive]) != 6 {
		t.Fatalf("live app %v permissions %+v", found, cat.Permissions[adminauth.AppLive])
	}
	join := func(role string) string { return strings.Join(cat.RolePermissions[role][adminauth.AppLive], ",") }
	all := "live:chat.moderate,live:reports.act,live:reports.read,live:streams.read,live:streams.stop,live:users.ban"
	if join(adminauth.RoleSuperadmin) != all || join(adminauth.RoleAdmin) != all {
		t.Fatalf("superadmin %s admin %s", join(adminauth.RoleSuperadmin), join(adminauth.RoleAdmin))
	}
	if got := join(adminauth.RoleModerator); got != "live:chat.moderate,live:reports.act,live:reports.read,live:streams.read" {
		t.Fatalf("moderator %s", got)
	}
	for _, r := range []string{adminauth.RoleFinance, adminauth.RoleSupport, adminauth.RoleKYCReviewer, adminauth.RoleAuditor} {
		if got := join(r); got != "" {
			t.Fatalf("%s holds %s", r, got)
		}
	}
}

// A holder of any live permission sees Live in the navigation, after Chat.
func TestMe_LiveNavigation(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permLiveStreamsRead, permChatReportsRead)
	w := rg.do(http.MethodGet, "/v1/admin/me", "", actor, false)
	if !strings.Contains(w.Body.String(), `{"app":"chat","label":"Chat"},{"app":"live","label":"Live"}`) {
		t.Fatalf("navigation %s", w.Body.String())
	}
}
