// Admin-service token tests (admin console Wave 2 — Mopedu). No database:
// the real route table runs over a nil store, so an admitted request is one
// the handler answers itself (a 400 for a bad body or id, after it resolved
// the actor), and a recording audit sink proves the actor the audit
// middleware saw. Refusals are proven by the auth layer's own status and
// code. Audit rows in the database and stats counts are in
// admin_token_integration_test.go.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/rider-service/internal/http/middleware"
	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/rider-service/internal/store"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const adminTestInternalKey = "rider-admin-token-test-key"

// recordingAudit captures every audit row the middleware writes.
type recordingAudit struct {
	mu   sync.Mutex
	rows []store.RecordAuditInput
}

func (f *recordingAudit) RecordAudit(_ context.Context, in store.RecordAuditInput) (*store.AuditLog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, in)
	return &store.AuditLog{}, nil
}

func (f *recordingAudit) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = nil
}

func (f *recordingAudit) all() []store.RecordAuditInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.RecordAuditInput(nil), f.rows...)
}

type adminTokenRig struct {
	r        *gin.Engine
	v        *servicetoken.Verifier
	audit    *recordingAudit
	admin    *servicetoken.Signer // admin-service, registered for AdminPermissions
	notifier *servicetoken.Signer // a registered caller that is not admin-service
	rogue    *servicetoken.Signer // claims to be admin-service, unregistered key
	actor    uuid.UUID
}

func newAdminTokenRig(t *testing.T) *adminTokenRig {
	t.Helper()
	aPub, aPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	nPub, nPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	_, rPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SERVICE_CALLERS":                            "admin-service,notification-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":           "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY":        aPub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":           strings.Join(AdminPermissions, ","),
		"SERVICE_CALLER_NOTIFICATION_SERVICE_KID":    "n1",
		"SERVICE_CALLER_NOTIFICATION_SERVICE_PUBKEY": nPub,
		// Deliberately over-granted: even with the op registered, a caller
		// other than admin-service must not reach an admin route.
		"SERVICE_CALLER_NOTIFICATION_SERVICE_OPS": PermPartnersRead + "," + PermStatsRead,
	}
	v, err := ServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	mk := func(iss, kid, priv string) *servicetoken.Signer {
		s, err := servicetoken.NewSignerFromBase64(iss, kid, priv)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	audit := &recordingAudit{}
	return &adminTokenRig{
		r:        adminTokenRouter(v, audit),
		v:        v,
		audit:    audit,
		admin:    mk(IssuerAdminService, "a1", aPriv),
		notifier: mk("notification-service", "n1", nPriv),
		rogue:    mk(IssuerAdminService, "a1", rPriv),
		actor:    uuid.New(),
	}
}

func adminTokenRouter(v *servicetoken.Verifier, audit middleware.AuditWriter) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	New(service.New(store.New(nil), nil, service.Config{}), adminTestInternalKey).
		WithServiceAuth(v).WithAuditWriter(audit).RegisterRoutes(r)
	return r
}

func (rg *adminTokenRig) mint(t *testing.T, s *servicetoken.Signer, aud string, scope []string, actor string) string {
	t.Helper()
	var opts []servicetoken.MintOption
	if actor != "" {
		opts = append(opts, servicetoken.WithActor(actor))
	}
	tok, err := s.Mint(aud, "admin-console", scope, nil, time.Minute, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func adminServe(r *gin.Engine, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func bearer(tok string) map[string]string {
	return map[string]string{ServiceAuthHeader: "Bearer " + tok}
}

// legacyAdmin is what the gateway would send for an admin today, key included.
func legacyAdmin(user uuid.UUID, scopes string) map[string]string {
	return map[string]string{
		"X-Internal-Service-Key": adminTestInternalKey,
		"X-User-Id":              user.String(),
		"X-Scopes":               scopes,
	}
}

// errorCode reads the code of a shared/api error body.
func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code  string `json:"code"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		return ""
	}
	if body.Error.Code != "" {
		return body.Error.Code
	}
	return body.Code
}

// uuidParams fills every :param of a route path with a fresh uuid.
func uuidParams(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, ":") {
			parts[i] = uuid.NewString()
		}
	}
	return strings.Join(parts, "/")
}

// The reject route resolves the actor BEFORE it binds the body, so a bad
// body proves the token was admitted and the handler ran as act, and the
// audit row shows what the middleware recorded — with a forged gateway
// identity and the internal key riding along.
func TestAdminToken_RightScopeAdmittedActorIsAct(t *testing.T) {
	rg := newAdminTokenRig(t)
	forged := uuid.New()
	cases := []struct {
		name, perm, method, path, body, wantCode, wantAction string
	}{
		{"partner reject", PermPartnersApprove, http.MethodPost, "/partners/" + uuid.NewString() + "/reject", `not json`, "INVALID_BODY", "partner.reject"},
		{"partner read", PermPartnersRead, http.MethodGet, "/partners/not-a-uuid", "", "INVALID_ID", "partner.read"},
		{"rating visibility", PermRatingsModerate, http.MethodPost, "/rides/" + uuid.NewString() + "/rating/visibility", `not json`, "INVALID_BODY", "rating.visibility"},
		{"ride cancel", PermRidesCancel, http.MethodPost, "/rides/" + uuid.NewString() + "/cancel", `not json`, "INVALID_BODY", "ride.cancel"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rg.audit.reset()
			hdr := legacyAdmin(forged, "superadmin")
			hdr["X-Admin-Role"] = "rider:admin"
			hdr[ServiceAuthHeader] = "Bearer " + rg.mint(t, rg.admin, AudienceRider, []string{tc.perm}, rg.actor.String())
			w := adminServe(rg.r, tc.method, InternalAdminPrefix+tc.path, tc.body, hdr)
			if w.Code != http.StatusBadRequest || errorCode(t, w) != tc.wantCode {
				t.Fatalf("status=%d code=%q body=%s, want 400 %s", w.Code, errorCode(t, w), w.Body.String(), tc.wantCode)
			}
			rows := rg.audit.all()
			if len(rows) != 1 {
				t.Fatalf("audit rows=%d, want 1", len(rows))
			}
			if rows[0].AdminUserID != rg.actor {
				t.Fatalf("audit actor=%s, want act %s (forged header was %s)", rows[0].AdminUserID, rg.actor, forged)
			}
			if rows[0].Action != tc.wantAction {
				t.Fatalf("audit action=%q, want %q", rows[0].Action, tc.wantAction)
			}
			if rows[0].RequestPath == nil || !strings.HasPrefix(*rows[0].RequestPath, InternalAdminPrefix) {
				t.Fatalf("audit path=%v, want under %s", rows[0].RequestPath, InternalAdminPrefix)
			}
		})
	}
}

func TestAdminToken_Refusals(t *testing.T) {
	rg := newAdminTokenRig(t)
	actor := rg.actor.String()
	path := InternalAdminPrefix + "/partners/not-a-uuid"
	cases := []struct {
		name     string
		tok      string
		clock    time.Duration
		wantCode string
	}{
		{"wrong audience", rg.mint(t, rg.admin, "food", []string{PermPartnersRead}, actor), 0, CodeServiceTokenRejected},
		{"payments audience", rg.mint(t, rg.admin, servicetoken.AudiencePayments, []string{PermPartnersRead}, actor), 0, CodeServiceTokenRejected},
		{"missing act", rg.mint(t, rg.admin, AudienceRider, []string{PermPartnersRead}, ""), 0, CodeAdminActorRequired},
		{"malformed act", rg.mint(t, rg.admin, AudienceRider, []string{PermPartnersRead}, "not-a-uuid"), 0, CodeAdminActorRequired},
		{"nil act", rg.mint(t, rg.admin, AudienceRider, []string{PermPartnersRead}, uuid.Nil.String()), 0, CodeAdminActorRequired},
		{"wrong scope", rg.mint(t, rg.admin, AudienceRider, []string{PermRidesRead}, actor), 0, CodeAdminPermissionScope},
		{"expired", rg.mint(t, rg.admin, AudienceRider, []string{PermPartnersRead}, actor), 2 * time.Minute, CodeServiceTokenRejected},
		{"unknown caller key", rg.mint(t, rg.rogue, AudienceRider, []string{PermPartnersRead}, actor), 0, CodeServiceTokenRejected},
		{"registered non-admin caller", rg.mint(t, rg.notifier, AudienceRider, []string{PermPartnersRead}, actor), 0, CodeServiceTokenRejected},
		{"garbage", "a.b.c", 0, CodeServiceTokenRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.clock != 0 {
				rg.v.SetClock(func() time.Time { return time.Now().Add(tc.clock) })
				defer rg.v.SetClock(time.Now)
			}
			rg.audit.reset()
			hdr := legacyAdmin(uuid.New(), "superadmin")
			hdr[ServiceAuthHeader] = "Bearer " + tc.tok
			w := adminServe(rg.r, http.MethodGet, path, "", hdr)
			if w.Code != http.StatusForbidden || errorCode(t, w) != tc.wantCode {
				t.Fatalf("status=%d code=%q body=%s, want 403 %s", w.Code, errorCode(t, w), w.Body.String(), tc.wantCode)
			}
			if n := len(rg.audit.all()); n != 0 {
				t.Fatalf("a refused request was audited (%d rows)", n)
			}
		})
	}
}

// Without a verifier the family accepts nothing, whatever the token.
func TestAdminToken_NoVerifierRefuses(t *testing.T) {
	rg := newAdminTokenRig(t)
	r := adminTokenRouter(nil, rg.audit)
	tok := rg.mint(t, rg.admin, AudienceRider, []string{PermPartnersRead}, rg.actor.String())
	w := adminServe(r, http.MethodGet, InternalAdminPrefix+"/partners/not-a-uuid", "", bearer(tok))
	if w.Code != http.StatusUnauthorized || errorCode(t, w) != CodeServiceCredentialRequired {
		t.Fatalf("status=%d code=%q, want 401 %s", w.Code, errorCode(t, w), CodeServiceCredentialRequired)
	}
}

// The edge cannot reach the token-only family with the key the gateway stamps
// plus a forged actor: no token, no entry, whatever the headers say.
func TestAdminToken_InternalFamilyRefusesKeyAndForgedActor(t *testing.T) {
	rg := newAdminTokenRig(t)
	forged := uuid.New()
	var checked int
	for _, ri := range rg.r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		checked++
		hdr := legacyAdmin(forged, "superadmin admin moderator")
		hdr["X-Admin-Id"] = forged.String()
		hdr["X-Admin-Role"] = "rider:admin"
		w := adminServe(rg.r, ri.Method, uuidParams(ri.Path), `{"reason":"x"}`, hdr)
		if w.Code != http.StatusUnauthorized || errorCode(t, w) != CodeAdminTokenRequired {
			t.Fatalf("%s %s: status=%d code=%q, want 401 %s", ri.Method, ri.Path, w.Code, errorCode(t, w), CodeAdminTokenRequired)
		}
	}
	if want := len(adminRoutePermissions()); checked != want {
		t.Fatalf("checked %d internal admin routes, want %d", checked, want)
	}
	if n := len(rg.audit.all()); n != 0 {
		t.Fatalf("a request without a token was audited (%d rows)", n)
	}
}

// adminRoutePermissions is the route table under test: method + suffix →
// the permission the route's gate names.
func adminRoutePermissions() map[string]string {
	return map[string]string{
		"GET /stats":                             PermStatsRead,
		"GET /dashboard":                         PermStatsRead,
		"GET /partners":                          PermPartnersRead,
		"GET /partners/:id":                      PermPartnersRead,
		"POST /partners/:id/approve":             PermPartnersApprove,
		"POST /partners/:id/reject":              PermPartnersApprove,
		"POST /partners/:id/suspend":             PermPartnersSuspend,
		"POST /partners/:id/block":               PermPartnersSuspend,
		"GET /documents":                         PermDocumentsReview,
		"POST /documents/:id/verify":             PermDocumentsReview,
		"POST /documents/:id/reject":             PermDocumentsReview,
		"GET /vehicles":                          PermVehiclesReview,
		"POST /vehicles/:id/verify":              PermVehiclesReview,
		"POST /vehicles/:id/reject":              PermVehiclesReview,
		"GET /payments":                          PermPaymentsRead,
		"POST /payments/:id/verify":              PermPaymentsSettle,
		"POST /payments/:id/reject":              PermPaymentsReject,
		"GET /rides":                             PermRidesRead,
		"GET /rides/live":                        PermRidesRead,
		"GET /safety/incidents/:id/alerts":       PermIncidentsReveal,
		"POST /rides/:id/rating/visibility":      PermRatingsModerate,
		"GET /reports/matching-health":           PermReportsRead,
		"GET /reports/partner-quality":           PermReportsRead,
		"GET /reports/supply-demand":             PermReportsRead,
		"GET /reports/safety":                    PermReportsRead,
		"GET /reports/compliance":                PermReportsRead,
		"POST /rides/:id/cancel":                 PermRidesCancel,
		"GET /complaints":                        PermComplaintsAct,
		"POST /complaints/:id/update-status":     PermComplaintsAct,
		"GET /safety-incidents":                  PermIncidentsRead,
		"POST /safety-incidents/:id/acknowledge": PermIncidentsAct,
		"POST /safety-incidents/:id/resolve":     PermIncidentsAct,
		"POST /cities":                           PermCitiesManage,
		"PATCH /cities/:id":                      PermCitiesManage,
		"POST /zones":                            PermCitiesManage,
		"PATCH /zones/:id":                       PermCitiesManage,
		"POST /fare-rules":                       PermFaresManage,
		"PATCH /fare-rules/:id":                  PermFaresManage,
		"GET /coupons":                           PermFaresManage,
		"GET /coupons/redemptions":               PermFaresManage,
		"POST /coupons":                          PermFaresManage,
		"PATCH /coupons/:id":                     PermFaresManage,
		"POST /coupons/:id/deactivate":           PermFaresManage,
		"GET /outstanding":                       PermPaymentsRead,
		"POST /outstanding/:id/waive":            PermPaymentsSettle,
		"GET /audit-logs":                        PermAuditRead,
		"GET /reports/revenue":                   PermReportsRead,
		"GET /reports/cohort-retention":          PermReportsRead,
		"GET /reports/customer-cohort":           PermReportsRead,
		"GET /reports/cron-runs":                 PermReportsRead,
	}
}

// Every route of both families is declared, and every token-only route
// refuses a token carrying every permission but its own.
func TestAdminToken_EveryRouteNeedsItsPermission(t *testing.T) {
	rg := newAdminTokenRig(t)
	want := adminRoutePermissions()
	seen := map[string]int{}
	for _, ri := range rg.r.Routes() {
		var suffix, family string
		switch {
		case strings.HasPrefix(ri.Path, InternalAdminPrefix+"/"):
			suffix, family = strings.TrimPrefix(ri.Path, InternalAdminPrefix), "internal"
		case strings.HasPrefix(ri.Path, "/v1/rider/admin/"):
			suffix, family = strings.TrimPrefix(ri.Path, "/v1/rider/admin"), "legacy"
		default:
			continue
		}
		perm, ok := want[ri.Method+" "+suffix]
		if !ok {
			t.Fatalf("undeclared admin route %s %s", ri.Method, ri.Path)
		}
		seen[family]++
		if family != "internal" {
			continue
		}
		var others []string
		for _, p := range AdminPermissions {
			if p != perm {
				others = append(others, p)
			}
		}
		tok := rg.mint(t, rg.admin, AudienceRider, others, rg.actor.String())
		hdr := bearer(tok)
		hdr["X-Internal-Service-Key"] = adminTestInternalKey
		w := adminServe(rg.r, ri.Method, uuidParams(ri.Path), `{"reason":"x"}`, hdr)
		if w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminPermissionScope {
			t.Fatalf("%s %s without %s: status=%d code=%q, want 403 %s", ri.Method, ri.Path, perm, w.Code, errorCode(t, w), CodeAdminPermissionScope)
		}
	}
	// The legacy family has every route but /stats.
	if seen["internal"] != len(want) || seen["legacy"] != len(want)-1 {
		t.Fatalf("saw internal=%d legacy=%d admin routes, want %d and %d", seen["internal"], seen["legacy"], len(want), len(want)-1)
	}
	if n := len(rg.audit.all()); n != 0 {
		t.Fatalf("a refused request was audited (%d rows)", n)
	}
}

// Suspend and block take a partner off the road: approve is not enough, and
// suspend does not cover approving. Verifying a payment settles money;
// rejecting one does not, and neither permission covers the other.
func TestAdminToken_NarrowsPerAction(t *testing.T) {
	rg := newAdminTokenRig(t)
	id := uuid.NewString()
	cases := []struct {
		path, perm, other string
	}{
		{"/partners/" + id + "/suspend", PermPartnersSuspend, PermPartnersApprove},
		{"/partners/" + id + "/block", PermPartnersSuspend, PermPartnersApprove},
		{"/partners/" + id + "/approve", PermPartnersApprove, PermPartnersSuspend},
		{"/partners/" + id + "/reject", PermPartnersApprove, PermPartnersSuspend},
		{"/payments/" + id + "/verify", PermPaymentsSettle, PermPaymentsReject},
		{"/payments/" + id + "/reject", PermPaymentsReject, PermPaymentsSettle},
	}
	for _, tc := range cases {
		wrong := rg.mint(t, rg.admin, AudienceRider, []string{tc.other}, rg.actor.String())
		if w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+tc.path, `not json`, bearer(wrong)); w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminPermissionScope {
			t.Fatalf("%s with %s: status=%d code=%q, want 403 %s", tc.path, tc.other, w.Code, errorCode(t, w), CodeAdminPermissionScope)
		}
		right := rg.mint(t, rg.admin, AudienceRider, []string{tc.perm}, rg.actor.String())
		// A bad body: the gate admitted the call and the handler ran.
		if w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+tc.path, `not json`, bearer(right)); w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized {
			t.Fatalf("%s with %s: status=%d body=%s, want admitted", tc.path, tc.perm, w.Code, w.Body.String())
		}
	}
	for _, row := range rg.audit.all() {
		if row.AdminUserID != rg.actor {
			t.Fatalf("audit actor %s, want act %s", row.AdminUserID, rg.actor)
		}
	}
}

// The LEGACY family is unchanged: the internal key plus X-User-Id and an
// admin scope reach the handler with the header user as actor; a token does
// not stand in for the key there, and the key alone is not an admin.
func TestAdminToken_LegacyFamilyUnchanged(t *testing.T) {
	rg := newAdminTokenRig(t)
	user := uuid.New()
	rg.audit.reset()
	w := adminServe(rg.r, http.MethodGet, "/v1/rider/admin/partners/not-a-uuid", "", legacyAdmin(user, "admin"))
	if w.Code != http.StatusBadRequest || errorCode(t, w) != "INVALID_ID" {
		t.Fatalf("legacy admin: status=%d code=%q, want 400 INVALID_ID", w.Code, errorCode(t, w))
	}
	rows := rg.audit.all()
	if len(rows) != 1 || rows[0].AdminUserID != user || rows[0].Action != "partner.read" {
		t.Fatalf("legacy audit rows=%+v, want one partner.read by %s", rows, user)
	}

	// The rating route now takes its actor from the guard, not the header:
	// same user either way on the legacy family.
	rg.audit.reset()
	w = adminServe(rg.r, http.MethodPost, "/v1/rider/admin/rides/"+uuid.NewString()+"/rating/visibility", `not json`, legacyAdmin(user, "admin"))
	if w.Code != http.StatusBadRequest || errorCode(t, w) != "INVALID_BODY" {
		t.Fatalf("legacy rating: status=%d code=%q, want 400 INVALID_BODY", w.Code, errorCode(t, w))
	}
	if rows = rg.audit.all(); len(rows) != 1 || rows[0].AdminUserID != user {
		t.Fatalf("legacy rating audit rows=%+v, want one by %s", rows, user)
	}

	tok := rg.mint(t, rg.admin, AudienceRider, []string{PermPartnersRead}, rg.actor.String())
	if w = adminServe(rg.r, http.MethodGet, "/v1/rider/admin/partners/not-a-uuid", "", bearer(tok)); w.Code != http.StatusUnauthorized || errorCode(t, w) != "UNAUTHORIZED" {
		t.Fatalf("legacy with token, no key: status=%d code=%q, want 401 UNAUTHORIZED", w.Code, errorCode(t, w))
	}
	hdr := map[string]string{"X-Internal-Service-Key": adminTestInternalKey, "X-User-Id": user.String()}
	if w = adminServe(rg.r, http.MethodGet, "/v1/rider/admin/partners/not-a-uuid", "", hdr); w.Code != http.StatusForbidden {
		t.Fatalf("legacy key without scope: status=%d, want 403", w.Code)
	}
}

func TestServiceCallersFromEnv(t *testing.T) {
	pub, _, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if v, err := ServiceCallersFromEnv(get(nil)); v != nil || err != nil {
		t.Fatalf("blank: v=%v err=%v, want nil, nil", v, err)
	}
	bad := []map[string]string{
		{"SERVICE_CALLERS": "admin-service"},
		{"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_KID": "a1", "SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub},
		{"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_KID": "a1", "SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": "nope", "SERVICE_CALLER_ADMIN_SERVICE_OPS": PermStatsRead},
		{"SERVICE_CALLERS": ","},
	}
	for i, env := range bad {
		if _, err := ServiceCallersFromEnv(get(env)); err == nil {
			t.Fatalf("case %d: want an error", i)
		}
	}
	good := map[string]string{
		"SERVICE_CALLERS":                     "admin-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":    "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":    strings.Join(AdminPermissions, ", "),
	}
	v, err := ServiceCallersFromEnv(get(good))
	if err != nil || v == nil || v.Callers() != 1 {
		t.Fatalf("good: v=%v err=%v", v, err)
	}
}
