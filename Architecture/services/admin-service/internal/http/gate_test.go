package http

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
)

func TestMissingPermissionIsRefusedAndAudited(t *testing.T) {
	rg := newRig(t, rigOpts{})
	rg.perms.grant(adminA, permProductsModerate) // can moderate products, cannot approve sellers
	w := rg.do(http.MethodPost, "/v1/admin/commerce/sellers/s-1/approve", `{}`, adminA)
	if w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
		t.Fatalf("status %d body %s, want 403 PERMISSION_DENIED", w.Code, w.Body.String())
	}
	if hits, _, _, _ := rg.seen.snapshot(); hits != 0 {
		t.Fatal("a refused request reached commerce")
	}
	e := rg.onlyEntry(t)
	if e.Actor != adminA || e.App != "commerce" || e.Operation != "seller.approve" ||
		e.Outcome != postgres.AuditOutcomeDenied || e.StatusCode != http.StatusForbidden ||
		e.Payload["code"] != CodePermissionDenied || e.Payload["required_permission"] != permSellerApprove {
		t.Fatalf("denial audit %+v", e)
	}
}

func TestAnAppScopedPermissionDoesNotReachAnotherApp(t *testing.T) {
	rg := newRig(t, rigOpts{})
	// A Dating-scoped admin: every dating permission, nothing in commerce.
	rg.perms.grant(adminA, "dating:reports.act", "dating:users.ban", "dating:photos.review")
	for _, wr := range commerceWriteRoutes {
		rg.rec.entries = nil
		w := rg.do(wr.method, wr.path, wr.body, adminA)
		if w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
			t.Fatalf("%s: status %d, want 403", wr.operation, w.Code)
		}
	}
	if hits, _, _, _ := rg.seen.snapshot(); hits != 0 {
		t.Fatal("a dating admin reached commerce")
	}
	// The same shape of permission in the right app passes.
	rg.perms.grant(adminB, "commerce:seller.approve")
	if w := rg.do(http.MethodPost, "/v1/admin/commerce/sellers/s-1/approve", `{}`, adminB); w.Code != http.StatusOK {
		t.Fatalf("commerce-scoped approver: status %d %s", w.Code, w.Body.String())
	}
}

func TestIdentityErrorFailsClosed(t *testing.T) {
	rg := newRig(t, rigOpts{})
	rg.perms.grant(adminA, commerceAll...)
	rg.perms.err = errIdentityDown
	for _, path := range []string{"/v1/admin/commerce/sellers/s-1/approve", "/v1/admin/me"} {
		rg.rec.entries = nil
		method := http.MethodPost
		if strings.HasSuffix(path, "/me") {
			method = http.MethodGet
		}
		w := rg.do(method, path, `{}`, adminA)
		if w.Code != http.StatusServiceUnavailable || !hasCode(w, CodePermissionsUnavailable) {
			t.Fatalf("%s: status %d body %s, want 503 PERMISSIONS_UNAVAILABLE", path, w.Code, w.Body.String())
		}
		if e := rg.onlyEntry(t); e.Outcome != postgres.AuditOutcomeDenied {
			t.Fatalf("%s: audit %+v", path, e)
		}
	}
	if hits, _, _, _ := rg.seen.snapshot(); hits != 0 {
		t.Fatal("an identity failure let a request through")
	}
}

func TestIdentityErrorFailsClosedThroughTheRealClient(t *testing.T) {
	// The HTTP client against a dead identity, behind the cache: still refused.
	dead := adminauth.NewCachedPermissions(adminauth.NewIdentityClient("http://127.0.0.1:1", "k"), time.Second)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	rec := &fakeRecorder{}
	h := New(&stubAdminService{}, NewGate(dead, rec, true), approvals.NewService(newMemStore(), &fakeHolders{}))
	h.RegisterMeRoute(r)
	w := (&rig{r: r}).do(http.MethodGet, "/v1/admin/me", "", adminA)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", w.Code)
	}
}

func TestARouteWithoutADeclarationRefusesBoot(t *testing.T) {
	rg := newRig(t, rigOpts{}) // the real table verifies
	if err := rg.gate.VerifyDeclared(rg.r); err != nil {
		t.Fatalf("real route table: %v", err)
	}
	rg.r.GET("/v1/admin/sneaky", func(c *gin.Context) {})
	rg.r.POST("/v1/admin/commerce/sellers/:sellerId/delete", func(c *gin.Context) {})
	err := rg.gate.VerifyDeclared(rg.r)
	if err == nil || !strings.Contains(err.Error(), "GET /v1/admin/sneaky") || !strings.Contains(err.Error(), "POST /v1/admin/commerce/sellers/:sellerId/delete") {
		t.Fatalf("undeclared routes accepted: %v", err)
	}
}

func TestADeclarationMustBeWellFormed(t *testing.T) {
	g := NewGate(&fakePerms{}, &fakeRecorder{}, true)
	r := gin.New()
	for name, req := range map[string]Requirement{
		"no permission":        {Operation: "x"},
		"malformed permission": {Operation: "x", Permission: "commerce"},
		"no operation":         {Permission: "commerce:seller.approve"},
		"mfa skip on a write":  {Operation: "x", Permission: "commerce:seller.approve", AllowWithoutMFA: true},
		"two-person any-admin": {Operation: "x", Access: AccessAnyAdmin, App: "platform", TwoPerson: true},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("%s: accepted", name)
				}
			}()
			g.Handle(r, http.MethodPost, "/v1/admin/"+strings.ReplaceAll(name, " ", "-"), req, func(c *gin.Context) {})
		}()
	}
}

func TestATwoPersonRouteNeedsAnExecutor(t *testing.T) {
	g := NewGate(&fakePerms{}, &fakeRecorder{}, true)
	g.Handle(gin.New(), http.MethodPost, "/v1/admin/money/move",
		Requirement{Operation: "money.move", Permission: "payments:refund.issue", StepUp: true, TwoPerson: true}, func(c *gin.Context) {})
	if err := g.VerifyExecutors(func(string, string) bool { return false }); err == nil {
		t.Fatal("a two-person route with no executor was accepted")
	}
}

func TestEveryGatedRouteNeedsMFA(t *testing.T) {
	rg := newRig(t, rigOpts{})
	rg.perms.grant(adminA, commerceAll...)
	rg.perms.grant(adminA, "platform:users.read", "*:audit.read", "trust_safety:reports.read")
	checked := 0
	for _, ri := range rg.r.Routes() {
		req, ok := rg.gate.Requirement(ri.Method, ri.Path)
		if !ok || req.Access == AccessSelfService || req.Access == AccessRetired || req.AllowWithoutMFA {
			continue
		}
		rg.rec.entries = nil
		path := strings.NewReplacer(":sellerId", "s-1", ":productId", "p-1", ":remittanceId", "7b0c6c1e-0f3a-4d59-9a57-3c1f7d1c2b10",
			":id", "7b0c6c1e-0f3a-4d59-9a57-3c1f7d1c2b10", "*rest", "categories").Replace(ri.Path)
		w := rg.do(ri.Method, path, `{"reason":"r"}`, adminA, noMFA)
		if w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeMFARequired) {
			t.Fatalf("%s %s without MFA: status %d body %s", ri.Method, path, w.Code, w.Body.String())
		}
		if e := rg.onlyEntry(t); e.Outcome != postgres.AuditOutcomeDenied || e.Payload["code"] != adminauth.CodeMFARequired {
			t.Fatalf("%s %s: audit %+v", ri.Method, path, e)
		}
		checked++
	}
	if checked < 20 {
		t.Fatalf("only %d routes checked", checked)
	}
	if hits, _, _, _ := rg.seen.snapshot(); hits != 0 {
		t.Fatal("a request without MFA reached commerce")
	}
	// A non-boolean X-Admin-MFA is not MFA.
	w := rg.do(http.MethodPost, "/v1/admin/commerce/sellers/s-1/approve", `{}`, adminA, func(r *http.Request) { r.Header.Set("X-Admin-MFA", "1") })
	if !hasCode(w, adminauth.CodeMFARequired) {
		t.Fatalf("X-Admin-MFA: 1 was accepted: %d", w.Code)
	}
}

func TestMFAFlagOffAdmitsWithoutTheHeader(t *testing.T) {
	off := false
	rg := newRig(t, rigOpts{requireMFA: &off})
	rg.perms.grant(adminA, commerceAll...)
	if w := rg.do(http.MethodPost, "/v1/admin/commerce/sellers/s-1/approve", `{}`, adminA, noMFA); w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	// Step-up is not governed by the MFA flag.
	if w := rg.do(http.MethodPost, "/v1/admin/commerce/sellers/s-1/kyc/verify", `{}`, adminA, noMFA, noStepUp); !hasCode(w, adminauth.CodeStepUpRequired) {
		t.Fatalf("step-up skipped with the MFA flag off: %d", w.Code)
	}
}

func TestStepUpRoutesNeedAFreshStepUp(t *testing.T) {
	stepUpRoutes := []struct{ method, path, body string }{
		{http.MethodPost, "/v1/admin/commerce/sellers/s-1/kyc/verify", `{}`},
		{http.MethodGet, "/v1/admin/commerce/payouts/pending", ``},
		{http.MethodPost, "/v1/admin/commerce/cod-remittances/7b0c6c1e-0f3a-4d59-9a57-3c1f7d1c2b10/settle", `{"reason":"paid"}`},
	}
	future := func(r *http.Request) { r.Header.Set("X-Step-Up-At", "99999999999") }
	garbage := func(r *http.Request) { r.Header.Set("X-Step-Up-At", "yesterday") }
	for _, sr := range stepUpRoutes {
		for name, opt := range map[string]reqOpt{
			"missing": noStepUp, "stale": stepUpAgo(301 * time.Second), "future": future, "garbage": garbage,
		} {
			rg := newRig(t, rigOpts{})
			rg.perms.grant(adminA, commerceAll...)
			w := rg.do(sr.method, sr.path, sr.body, adminA, opt)
			if w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeStepUpRequired) {
				t.Fatalf("%s %s step-up: status %d body %s", sr.path, name, w.Code, w.Body.String())
			}
			if hits, _, _, _ := rg.seen.snapshot(); hits != 0 {
				t.Fatalf("%s %s reached commerce", sr.path, name)
			}
			if e := rg.onlyEntry(t); e.Outcome != postgres.AuditOutcomeDenied || e.Payload["code"] != adminauth.CodeStepUpRequired {
				t.Fatalf("%s %s audit %+v", sr.path, name, e)
			}
		}
		rg := newRig(t, rigOpts{})
		rg.perms.grant(adminA, commerceAll...)
		if w := rg.do(sr.method, sr.path, sr.body, adminA, stepUpAgo(290*time.Second)); w.Code != http.StatusOK {
			t.Fatalf("%s with a step-up 290s old: status %d %s", sr.path, w.Code, w.Body.String())
		}
	}
	// A route without step_up does not ask for one.
	rg := newRig(t, rigOpts{})
	rg.perms.grant(adminA, commerceAll...)
	if w := rg.do(http.MethodPost, "/v1/admin/commerce/sellers/s-1/approve", `{}`, adminA, noStepUp); w.Code != http.StatusOK {
		t.Fatalf("seller approve asked for step-up: %d", w.Code)
	}
}

func TestRetiredTakedownAndSuspendAnswerGone(t *testing.T) {
	rg := newRig(t, rigOpts{})
	for _, rt := range []struct{ method, path string }{
		{http.MethodPost, "/v1/admin/takedown"},
		{http.MethodPost, "/v1/admin/users/7b0c6c1e-0f3a-4d59-9a57-3c1f7d1c2b10/suspend"},
		{http.MethodDelete, "/v1/admin/users/7b0c6c1e-0f3a-4d59-9a57-3c1f7d1c2b10/suspend"},
	} {
		w := rg.do(rt.method, rt.path, `{"entity_type":"post","entity_id":"x","reason":"r"}`, adminA)
		if w.Code != http.StatusGone || !hasCode(w, CodeRetired) || !strings.Contains(w.Body.String(), "Wave 2") {
			t.Fatalf("%s %s: status %d body %s", rt.method, rt.path, w.Code, w.Body.String())
		}
	}
}

func TestMeShape(t *testing.T) {
	rg := newRig(t, rigOpts{})
	rg.perms.grant(adminA, "commerce:seller.approve", "commerce:kyc.verify", "dating:reports.act", "platform:users.read")
	stepUp := time.Now().Add(-100 * time.Second).Unix()
	w := rg.do(http.MethodGet, "/v1/admin/me", "", adminA, func(r *http.Request) {
		r.Header.Set("X-Step-Up-At", jsonInt(stepUp))
		r.Header.Set("X-Auth-Time", jsonInt(stepUp-600))
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"user_id", "permissions", "mfa", "step_up_valid_until", "step_up_window_seconds", "navigation"} {
		if _, ok := env.Data[k]; !ok {
			t.Fatalf("missing %q in %s", k, w.Body.String())
		}
	}
	var meEnv struct {
		Data MeResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &meEnv); err != nil {
		t.Fatalf("decode: %v in %s", err, w.Body.String())
	}
	me := meEnv.Data
	if false {
	}
	if me.UserID != adminA || !me.MFA.Required || !me.MFA.Verified || me.MFA.AuthTime == nil || me.StepUpWindowSeconds != 300 {
		t.Fatalf("me %+v", me)
	}
	if me.StepUpValidUntil == nil || me.StepUpValidUntil.Unix() != stepUp+300 {
		t.Fatalf("step_up_valid_until %v, want %d", me.StepUpValidUntil, stepUp+300)
	}
	if len(me.Permissions.Apps["commerce"]) != 2 || len(me.Permissions.Apps["dating"]) != 1 || len(me.Permissions.Platform) != 1 {
		t.Fatalf("permissions %+v", me.Permissions)
	}
	var nav []string
	for _, n := range me.Navigation {
		nav = append(nav, n.App+"="+n.Label)
	}
	if strings.Join(nav, ",") != "dating=Dating,commerce=MStore,platform=Platform" {
		t.Fatalf("navigation %v", nav)
	}

	// Reachable without MFA, reporting it; no step-up means null; no permissions means no menu.
	w = rg.do(http.MethodGet, "/v1/admin/me", "", nobody, noMFA, noStepUp)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"verified":false`) ||
		!strings.Contains(w.Body.String(), `"step_up_valid_until":null`) || !strings.Contains(w.Body.String(), `"navigation":[]`) {
		t.Fatalf("me without MFA: %d %s", w.Code, w.Body.String())
	}
}

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }
