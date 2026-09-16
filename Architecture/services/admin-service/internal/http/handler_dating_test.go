package http

import (
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

// datingAll is every Dating permission the routes use.
var datingAll = []string{
	permDatingStatsRead, permDatingReportsRead, permDatingReportsAct, permDatingUsersBan,
	permDatingPhotosReview, permDatingSelfieReview, permDatingPanicRead, permDatingPanicAct,
	permDatingPanicReveal, permDatingRiskRead, permDatingAuditRead,
}

// datingHit is what the stub dating-service saw on one call, with the token
// verified by a real verifier holding only admin-service's public key.
type datingHit struct {
	path, query, body string
	verified          *servicetoken.Verified
	verifyErr         error
	userHdr, keyHdr   string
	requestID         string
}

type datingRig struct {
	r     *gin.Engine
	gate  *Gate
	rec   *fakeRecorder
	perms *fakePerms
	mu    sync.Mutex
	hits  []datingHit
}

func newDatingRig(t *testing.T, withKey bool) *datingRig {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	verifier := servicetoken.NewVerifier("dating")
	if err := verifier.RegisterBase64("admin-service", "a1", pub, datingAll, nil); err != nil {
		t.Fatal(err)
	}
	rg := &datingRig{rec: &fakeRecorder{}, perms: &fakePerms{byUser: map[string]adminauth.Permissions{}}}
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		raw := strings.TrimPrefix(r.Header.Get("X-Service-Authorization"), "Bearer ")
		v, verr := verifier.Verify(raw, "", "")
		rg.mu.Lock()
		rg.hits = append(rg.hits, datingHit{
			path: r.URL.Path, query: r.URL.RawQuery, body: string(b), verified: v, verifyErr: verr,
			userHdr: r.Header.Get("X-User-Id"), keyHdr: r.Header.Get("X-Internal-Service-Key"),
			requestID: r.Header.Get("X-Request-Id"),
		})
		rg.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	t.Cleanup(stub.Close)

	var signer *servicetoken.Signer
	if withKey {
		signer, err = service.SignerFromEnv(func(k string) string {
			return map[string]string{"ADMIN_SERVICE_TOKEN_KEY": priv, "ADMIN_SERVICE_TOKEN_KID": "a1"}[k]
		})
		if err != nil || signer == nil {
			t.Fatalf("signer: %v", err)
		}
	}
	gin.SetMode(gin.TestMode)
	rg.r = gin.New()
	rg.r.Use(middleware.RequestID())
	rg.gate = NewGate(rg.perms, rg.rec, true)
	h := New(&stubAdminService{}, rg.gate, approvals.NewService(newMemStore(), &fakeHolders{}))
	h.WithDating(service.NewDatingClient(stub.URL, signer))
	if err := h.RegisterAllRoutes(rg.r); err != nil {
		t.Fatalf("route table refused: %v", err)
	}
	return rg
}

func (rg *datingRig) do(method, path, body, actor string, stepUp bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "req-dating-1")
	req.Header.Set("X-User-Id", actor)
	req.Header.Set("X-Admin-MFA", "true")
	if stepUp {
		req.Header.Set("X-Step-Up-At", strconv.FormatInt(time.Now().Unix(), 10))
	}
	w := httptest.NewRecorder()
	rg.r.ServeHTTP(w, req)
	return w
}

func (rg *datingRig) takeHits() []datingHit {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	out := rg.hits
	rg.hits = nil
	return out
}

func (rg *datingRig) takeAudit() []postgres.AdminAuditEntry {
	rg.rec.mu.Lock()
	defer rg.rec.mu.Unlock()
	out := rg.rec.entries
	rg.rec.entries = nil
	return out
}

// concrete fills a route's params and gives each a body it accepts.
func concrete(rt datingRoute) (path, body string) {
	path = "/v1/admin/dating" + strings.NewReplacer(
		":reportId", uuid.NewString(), ":photoId", uuid.NewString(), ":userId", uuid.NewString(),
		":incidentId", uuid.NewString()).Replace(rt.path)
	switch rt.operation {
	case "dating.report.act":
		body = `{"action":"dismiss"}`
	case "dating.report.enforce":
		body = `{"action":"suspend","reason":"repeated harassment"}`
	case "dating.photo.decide":
		body = `{"status":"approved"}`
	case "dating.selfie.decide":
		body = `{"decision":"approve"}`
	case "dating.panic.resolve":
		body = `{"note":"called the user"}`
	}
	return path, body
}

func TestDatingRoutes_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newDatingRig(t, true)
	if len(DatingRoutes) != 14 {
		t.Fatalf("DatingRoutes has %d entries, want 14", len(DatingRoutes))
	}
	for _, rt := range DatingRoutes {
		t.Run(rt.operation, func(t *testing.T) {
			path, body := concrete(rt)
			// An admin holding every OTHER Dating permission is refused.
			other := uuid.NewString()
			for _, p := range datingAll {
				if p != rt.permission {
					rg.perms.grant(other, p)
				}
			}
			w := rg.do(rt.method, path, body, other, true)
			if w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
				t.Fatalf("without %s: status=%d body=%s, want 403 %s", rt.permission, w.Code, w.Body.String(), CodePermissionDenied)
			}
			if hits := rg.takeHits(); len(hits) != 0 {
				t.Fatalf("a refused call reached dating: %+v", hits)
			}
			if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeDenied || a[0].Actor != other {
				t.Fatalf("refusal audit = %+v, want 1 denied row by %s", a, other)
			}

			// Holding exactly the permission: forwarded with a token scoped to it.
			actor := uuid.NewString()
			rg.perms.grant(actor, rt.permission)
			w = rg.do(rt.method, path, body, actor, true)
			if w.Code != http.StatusOK {
				t.Fatalf("with %s: status=%d body=%s, want 200", rt.permission, w.Code, w.Body.String())
			}
			hits := rg.takeHits()
			if len(hits) != 1 {
				t.Fatalf("dating hits = %d, want 1", len(hits))
			}
			h := hits[0]
			if h.verifyErr != nil {
				t.Fatalf("dating could not verify the token: %v", h.verifyErr)
			}
			v := h.verified
			if v.Issuer != "admin-service" || len(v.Scope) != 1 || v.Scope[0] != rt.permission || v.Actor != actor {
				t.Fatalf("token iss=%q scope=%v act=%q, want admin-service [%s] %s", v.Issuer, v.Scope, v.Actor, rt.permission, actor)
			}
			if !strings.HasPrefix(h.path, "/v1/dating/internal/admin/") {
				t.Fatalf("dating path %q is outside the token-only family", h.path)
			}
			if h.userHdr != "" || h.keyHdr != "" {
				t.Fatalf("admin-service sent X-User-Id=%q / internal key present=%v; the token is the only credential", h.userHdr, h.keyHdr != "")
			}
			if h.requestID != "req-dating-1" {
				t.Fatalf("request id forwarded = %q", h.requestID)
			}
			a := rg.takeAudit()
			if len(a) != 1 || a[0].Actor != actor || a[0].App != "dating" || a[0].Operation != rt.operation ||
				a[0].Outcome != postgres.AuditOutcomeSuccess || a[0].RequestID != "req-dating-1" {
				t.Fatalf("audit = %+v, want 1 success row by %s for dating/%s", a, actor, rt.operation)
			}
		})
	}
}

// The token a dating verifier sees carries audience "dating": the verifier in
// the rig is built for "dating", and a payments verifier refuses the same token.
func TestDatingToken_AudienceIsDating(t *testing.T) {
	pub, priv, _ := servicetoken.GenerateKeypair()
	var got string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = strings.TrimPrefix(r.Header.Get("X-Service-Authorization"), "Bearer ")
		w.WriteHeader(http.StatusOK)
	}))
	defer stub.Close()
	signer, _ := servicetoken.NewSignerFromBase64("admin-service", "a1", priv)
	dc := service.NewDatingClient(stub.URL, signer)
	if _, _, err := dc.Call(t.Context(), http.MethodGet, "/stats", nil, permDatingStatsRead, adminA, nil); err != nil {
		t.Fatal(err)
	}
	for aud, wantOK := range map[string]bool{"dating": true, "payments": false, "food": false} {
		v := servicetoken.NewVerifier(aud)
		_ = v.RegisterBase64("admin-service", "a1", pub, datingAll, nil)
		_, err := v.Verify(got, permDatingStatsRead, "")
		if (err == nil) != wantOK {
			t.Fatalf("audience %q: err=%v, want ok=%v", aud, err, wantOK)
		}
	}
	parts := strings.Split(got, ".")
	if len(parts) != 3 {
		t.Fatal("not a JWT")
	}
}

func TestDatingRoutes_StepUp(t *testing.T) {
	rg := newDatingRig(t, true)
	rg.perms.grant(adminA, datingAll...)
	for _, rt := range DatingRoutes {
		path, body := concrete(rt)
		w := rg.do(rt.method, path, body, adminA, false)
		if rt.stepUp {
			if w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeStepUpRequired) {
				t.Fatalf("%s without step-up: status=%d body=%s, want 403 %s", rt.operation, w.Code, w.Body.String(), adminauth.CodeStepUpRequired)
			}
			if hits := rg.takeHits(); len(hits) != 0 {
				t.Fatalf("%s reached dating without step-up", rt.operation)
			}
		} else if w.Code != http.StatusOK {
			t.Fatalf("%s needs no step-up: status=%d body=%s", rt.operation, w.Code, w.Body.String())
		}
		rg.takeHits()
		rg.takeAudit()
	}
	// The two that must: panic GPS and enforcement.
	for _, op := range []string{"dating.panic.reveal", "dating.report.enforce"} {
		found := false
		for _, rt := range DatingRoutes {
			if rt.operation == op {
				found = rt.stepUp
			}
		}
		if !found {
			t.Fatalf("%s is not declared step-up", op)
		}
	}
}

func TestDatingReportActions_SplitByPermission(t *testing.T) {
	rg := newDatingRig(t, true)
	rg.perms.grant(adminA, datingAll...)
	report := uuid.NewString()
	cases := []struct {
		path, body, code string
	}{
		{"/action", `{"action":"suspend"}`, CodeUseEnforcementRoute},
		{"/action", `{"action":"reinstate"}`, CodeUseEnforcementRoute},
		{"/action", `{"action":"delete_everything"}`, CodeInvalidAction},
		{"/enforce", `{"action":"dismiss","reason":"x"}`, CodeInvalidAction},
		{"/enforce", `{"action":"suspend"}`, CodeReasonRequired},
	}
	for _, tc := range cases {
		w := rg.do(http.MethodPost, "/v1/admin/dating/reports/"+report+tc.path, tc.body, adminA, true)
		if w.Code != http.StatusBadRequest || !hasCode(w, tc.code) {
			t.Fatalf("%s %s: status=%d body=%s, want 400 %s", tc.path, tc.body, w.Code, w.Body.String(), tc.code)
		}
		if hits := rg.takeHits(); len(hits) != 0 {
			t.Fatalf("%s %s reached dating", tc.path, tc.body)
		}
	}
	// A moderator (reports.act, no users.ban) cannot enforce at all.
	rg.perms.grant(adminB, permDatingReportsAct)
	w := rg.do(http.MethodPost, "/v1/admin/dating/reports/"+report+"/enforce", `{"action":"suspend","reason":"x"}`, adminB, true)
	if w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
		t.Fatalf("moderator enforce: status=%d body=%s, want 403", w.Code, w.Body.String())
	}
	// Forwarded body carries only the whitelisted fields.
	w = rg.do(http.MethodPost, "/v1/admin/dating/reports/"+report+"/enforce",
		`{"action":"suspend","reason":"harassment","extra":"x","target_user_id":"`+nobody+`"}`, adminA, true)
	if w.Code != http.StatusOK {
		t.Fatalf("enforce: status=%d body=%s", w.Code, w.Body.String())
	}
	hits := rg.takeHits()
	if len(hits) != 1 || hits[0].path != "/v1/dating/internal/admin/reports/"+report+"/action" ||
		strings.Contains(hits[0].body, "extra") || !strings.Contains(hits[0].body, `"action":"suspend"`) {
		t.Fatalf("forwarded %+v", hits)
	}
	if hits[0].verified == nil || hits[0].verified.Scope[0] != permDatingUsersBan {
		t.Fatalf("enforce token scope = %+v, want [%s]", hits[0].verified, permDatingUsersBan)
	}
}

func TestDatingRoutes_QueryWhitelist(t *testing.T) {
	rg := newDatingRig(t, true)
	rg.perms.grant(adminA, permDatingPanicRead)
	w := rg.do(http.MethodGet, "/v1/admin/dating/panic?status=open&limit=5&include_location=true", "", adminA, false)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	hits := rg.takeHits()
	if len(hits) != 1 || hits[0].path != "/v1/dating/internal/admin/safety/panic" || strings.Contains(hits[0].query, "include_location") ||
		!strings.Contains(hits[0].query, "status=open") {
		t.Fatalf("forwarded %+v", hits)
	}
}

func TestDatingRoutes_NoKeyIs503AndAudited(t *testing.T) {
	rg := newDatingRig(t, false)
	rg.perms.grant(adminA, permDatingStatsRead)
	w := rg.do(http.MethodGet, "/v1/admin/dating/stats", "", adminA, false)
	if w.Code != http.StatusServiceUnavailable || !hasCode(w, CodeProductUnavailable) {
		t.Fatalf("status=%d body=%s, want 503 %s", w.Code, w.Body.String(), CodeProductUnavailable)
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatal("reached dating without a key")
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure || a[0].StatusCode != 0 {
		t.Fatalf("audit = %+v, want 1 failure row with status 0", a)
	}
}

func TestMe_DatingNavigationOnlyWithADatingPermission(t *testing.T) {
	rg := newDatingRig(t, true)
	rg.perms.grant(adminA, "commerce:seller.approve")
	rg.perms.grant(adminB, "commerce:seller.approve", permDatingStatsRead)
	for actor, want := range map[string]bool{adminA: false, adminB: true} {
		w := rg.do(http.MethodGet, "/v1/admin/me", "", actor, false)
		if w.Code != http.StatusOK {
			t.Fatalf("me: %d", w.Code)
		}
		if got := strings.Contains(w.Body.String(), `"app":"dating"`); got != want {
			t.Fatalf("actor %s: dating in navigation = %v, want %v (%s)", actor, got, want, w.Body.String())
		}
	}
}

func TestSignerFromEnv(t *testing.T) {
	_, priv, _ := servicetoken.GenerateKeypair()
	cases := map[string]struct {
		env     map[string]string
		wantNil bool
		wantErr bool
	}{
		"unset":      {env: map[string]string{}, wantNil: true},
		"no kid":     {env: map[string]string{"ADMIN_SERVICE_TOKEN_KEY": priv}, wantErr: true},
		"bad key":    {env: map[string]string{"ADMIN_SERVICE_TOKEN_KEY": "bm90LWEta2V5", "ADMIN_SERVICE_TOKEN_KID": "a1"}, wantErr: true},
		"configured": {env: map[string]string{"ADMIN_SERVICE_TOKEN_KEY": priv, "ADMIN_SERVICE_TOKEN_KID": "a1"}},
	}
	for name, tc := range cases {
		s, err := service.SignerFromEnv(func(k string) string { return tc.env[k] })
		if (err != nil) != tc.wantErr || (s == nil) != (tc.wantNil || tc.wantErr) {
			t.Fatalf("%s: signer=%v err=%v", name, s != nil, err)
		}
	}
}
