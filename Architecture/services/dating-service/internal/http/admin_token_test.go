// Admin-service token tests (admin console Wave 2 — Dating). No database:
// admission is proven by reaching a handler that then refuses a malformed
// path or body (400), refusal by the auth layer's own status and code. The
// audit-actor and stats-count checks are in admin_token_integration_test.go.
package http

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type adminTokenRig struct {
	r        *gin.Engine
	v        *servicetoken.Verifier
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
		"SERVICE_CALLER_NOTIFICATION_SERVICE_OPS": OpSafetyPanicNotify + "," + PermPanicReveal + "," + PermStatsRead,
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
	return &adminTokenRig{
		r:        newAuthRouter(t, testInternalKey, v),
		v:        v,
		admin:    mk(IssuerAdminService, "a1", aPriv),
		notifier: mk("notification-service", "n1", nPriv),
		rogue:    mk(IssuerAdminService, "a1", rPriv),
		actor:    uuid.New(),
	}
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

func bearer(tok string) map[string]string {
	return map[string]string{ServiceAuthHeader: "Bearer " + tok}
}

// Panic detail with a malformed id: admitted → the handler's 400.
const panicDetailBad = InternalAdminPrefix + "/safety/panic/not-a-uuid"

func TestAdminToken_RightScopeAdmitted(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceDating, []string{PermPanicReveal}, rg.actor.String())
	for _, path := range []string{panicDetailBad, "/v1/dating/admin/safety/panic/not-a-uuid"} {
		hdr := bearer(tok)
		if strings.HasPrefix(path, "/v1/dating/admin") {
			hdr[headerInternalKey] = testInternalKey
		}
		if w := serve(rg.r, http.MethodGet, path, "", hdr); w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s, want 400 from the handler (admitted)", path, w.Code, w.Body.String())
		}
	}
}

func TestAdminToken_Refusals(t *testing.T) {
	rg := newAdminTokenRig(t)
	actor := rg.actor.String()
	cases := []struct {
		name     string
		tok      string
		clock    time.Duration
		wantCode string
	}{
		{"wrong audience", rg.mint(t, rg.admin, servicetoken.AudiencePayments, []string{PermPanicReveal}, actor), 0, CodeServiceTokenRejected},
		{"missing act", rg.mint(t, rg.admin, AudienceDating, []string{PermPanicReveal}, ""), 0, CodeAdminActorRequired},
		{"malformed act", rg.mint(t, rg.admin, AudienceDating, []string{PermPanicReveal}, "not-a-uuid"), 0, CodeAdminActorRequired},
		{"nil act", rg.mint(t, rg.admin, AudienceDating, []string{PermPanicReveal}, uuid.Nil.String()), 0, CodeAdminActorRequired},
		{"wrong scope", rg.mint(t, rg.admin, AudienceDating, []string{PermPanicRead}, actor), 0, CodeAdminPermissionScope},
		{"expired", rg.mint(t, rg.admin, AudienceDating, []string{PermPanicReveal}, actor), 2 * time.Minute, CodeServiceTokenRejected},
		{"unknown caller key", rg.mint(t, rg.rogue, AudienceDating, []string{PermPanicReveal}, actor), 0, CodeServiceTokenRejected},
		{"registered non-admin caller", rg.mint(t, rg.notifier, AudienceDating, []string{PermPanicReveal}, actor), 0, CodeServiceTokenRejected},
		{"garbage", "a.b.c", 0, CodeServiceTokenRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.clock != 0 {
				rg.v.SetClock(func() time.Time { return time.Now().Add(tc.clock) })
				defer rg.v.SetClock(time.Now)
			}
			for _, path := range []string{panicDetailBad, "/v1/dating/admin/safety/panic/not-a-uuid"} {
				// The key and a full admin gateway identity ride along; a request
				// with a token is judged by the token alone.
				hdr := gatewayUser(uuid.New(), "superadmin")
				hdr[ServiceAuthHeader] = "Bearer " + tc.tok
				w := serve(rg.r, http.MethodGet, path, "", hdr)
				if w.Code != http.StatusForbidden || errorCode(t, w) != tc.wantCode {
					t.Fatalf("%s: status=%d code=%q body=%s, want 403 %s", path, w.Code, errorCode(t, w), w.Body.String(), tc.wantCode)
				}
			}
		})
	}
}

// The edge cannot reach the token-only family with the key the gateway
// stamps plus a forged actor: no token, no entry, whatever the headers say.
func TestAdminToken_InternalFamilyRefusesKeyAndForgedActor(t *testing.T) {
	rg := newAdminTokenRig(t)
	forged := uuid.New()
	var checked int
	for _, ri := range rg.r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		checked++
		path := strings.NewReplacer(":id", uuid.NewString(), ":userId", uuid.NewString()).Replace(ri.Path)
		hdr := gatewayUser(forged, "superadmin admin moderator")
		hdr["X-Admin-Id"] = forged.String()
		hdr["X-Actor-Id"] = forged.String()
		w := serve(rg.r, ri.Method, path, `{"action":"dismiss"}`, hdr)
		if w.Code != http.StatusUnauthorized || errorCode(t, w) != CodeAdminTokenRequired {
			t.Fatalf("%s %s: status=%d code=%q, want 401 %s", ri.Method, path, w.Code, errorCode(t, w), CodeAdminTokenRequired)
		}
	}
	if checked < 12 {
		t.Fatalf("checked %d internal admin routes, want every one (>= 12)", checked)
	}
}

func TestAdminToken_EveryInternalRouteNeedsItsPermission(t *testing.T) {
	rg := newAdminTokenRig(t)
	want := map[string]string{
		"GET " + InternalAdminPrefix + "/stats":                               PermStatsRead,
		"GET " + InternalAdminPrefix + "/reports":                             PermReportsRead,
		"POST " + InternalAdminPrefix + "/reports/:id/action":                 PermReportsAct,
		"GET " + InternalAdminPrefix + "/safety/panic":                        PermPanicRead,
		"GET " + InternalAdminPrefix + "/safety/panic/:id":                    PermPanicReveal,
		"POST " + InternalAdminPrefix + "/safety/panic/:id/ack":               PermPanicAct,
		"POST " + InternalAdminPrefix + "/safety/panic/:id/resolve":           PermPanicAct,
		"GET " + InternalAdminPrefix + "/photos/pending":                      PermPhotosReview,
		"POST " + InternalAdminPrefix + "/photos/:id/moderation":              PermPhotosReview,
		"GET " + InternalAdminPrefix + "/verification/selfie/pending":         PermSelfieReview,
		"POST " + InternalAdminPrefix + "/verification/selfie/:userId/review": PermSelfieReview,
		"GET " + InternalAdminPrefix + "/audit":                               PermAuditRead,
		"GET " + InternalAdminPrefix + "/risk":                                PermRiskRead,
	}
	seen := 0
	for _, ri := range rg.r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		perm, ok := want[ri.Method+" "+ri.Path]
		if !ok {
			t.Fatalf("undeclared internal admin route %s %s", ri.Method, ri.Path)
		}
		seen++
		path := strings.NewReplacer(":id", uuid.NewString(), ":userId", uuid.NewString()).Replace(ri.Path)
		// Every OTHER permission, but not this one → refused by scope.
		var others []string
		for _, p := range AdminPermissions {
			if p != perm && !(ri.Path == InternalAdminPrefix+"/reports/:id/action" && p == PermUsersBan) {
				others = append(others, p)
			}
		}
		tok := rg.mint(t, rg.admin, AudienceDating, others, rg.actor.String())
		w := serve(rg.r, ri.Method, path, `{"action":"dismiss"}`, bearer(tok))
		if w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminPermissionScope {
			t.Fatalf("%s %s without %s: status=%d code=%q, want 403 %s", ri.Method, ri.Path, perm, w.Code, errorCode(t, w), CodeAdminPermissionScope)
		}
	}
	if seen != len(want) {
		t.Fatalf("saw %d internal admin routes, want %d", seen, len(want))
	}
}

// Suspend and reinstate are enforcement: dating:reports.act is not enough.
func TestAdminToken_SuspendNeedsUsersBan(t *testing.T) {
	rg := newAdminTokenRig(t)
	path := InternalAdminPrefix + "/reports/" + uuid.NewString() + "/action"
	actTok := rg.mint(t, rg.admin, AudienceDating, []string{PermReportsAct}, rg.actor.String())
	banTok := rg.mint(t, rg.admin, AudienceDating, []string{PermUsersBan}, rg.actor.String())
	for _, action := range []string{"suspend", "reinstate"} {
		body := `{"action":"` + action + `","target_user_id":"bad"}`
		if w := serve(rg.r, http.MethodPost, path, body, bearer(actTok)); w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminPermissionScope {
			t.Fatalf("%s with reports.act: status=%d code=%q, want 403 %s", action, w.Code, errorCode(t, w), CodeAdminPermissionScope)
		}
		if w := serve(rg.r, http.MethodPost, path, body, bearer(banTok)); w.Code != http.StatusBadRequest {
			t.Fatalf("%s with users.ban: status=%d body=%s, want 400 from the handler (admitted)", action, w.Code, w.Body.String())
		}
	}
	// And users.ban alone does not cover the moderation actions.
	body := `{"action":"dismiss","target_user_id":"bad"}`
	if w := serve(rg.r, http.MethodPost, path, body, bearer(banTok)); w.Code != http.StatusForbidden {
		t.Fatalf("dismiss with only users.ban: status=%d, want 403", w.Code)
	}
	if w := serve(rg.r, http.MethodPost, path, body, bearer(actTok)); w.Code != http.StatusBadRequest {
		t.Fatalf("dismiss with reports.act: status=%d body=%s, want 400 (admitted)", w.Code, w.Body.String())
	}
}

// The LEGACY path is unchanged for a request with no token: admin scopes
// still reach the handler.
func TestAdminToken_LegacyScopesStillWork(t *testing.T) {
	rg := newAdminTokenRig(t)
	w := serve(rg.r, http.MethodGet, "/v1/dating/admin/safety/panic/not-a-uuid", "", gatewayUser(uuid.New(), "moderator"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("legacy admin: status=%d body=%s, want 400 (admitted)", w.Code, w.Body.String())
	}
}

// A deployment without admin-service registered accepts no admin token.
func TestAdminToken_NoVerifierRefuses(t *testing.T) {
	rg := newAdminTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceDating, []string{PermPanicReveal}, rg.actor.String())
	r := newAuthRouter(t, testInternalKey, nil)
	if w := serve(r, http.MethodGet, panicDetailBad, "", bearer(tok)); w.Code != http.StatusUnauthorized {
		t.Fatalf("no verifier: status=%d, want 401", w.Code)
	}
}
