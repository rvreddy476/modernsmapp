package http

// Admin token family (1 Oct 2026). No database: the storetest memory store
// records the audit rows, so "admitted" is proven by the handler's answer
// and its audit row, "refused" by the gate's status and code and the
// absence of any audit row.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/service"
	"github.com/atpost/live-service-v2/internal/store/postgres"
	"github.com/atpost/live-service-v2/internal/storetest"
	"github.com/atpost/shared/servicetoken"
)

type adminRig struct {
	r        *gin.Engine
	store    *storetest.MemStore
	admin    *servicetoken.Signer // admin-service, registered for AdminPermissions
	payments *servicetoken.Signer // a registered caller that is not admin-service
	rogue    *servicetoken.Signer // claims admin-service, unregistered key
	actor    uuid.UUID
	host     uuid.UUID
	stream   *postgres.LiveStream
}

func newAdminRig(t *testing.T) *adminRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	aPub, aPriv, _ := servicetoken.GenerateKeypair()
	pPub, pPriv, _ := servicetoken.GenerateKeypair()
	_, rPriv, _ := servicetoken.GenerateKeypair()
	env := map[string]string{
		"SERVICE_CALLERS":                        "admin-service, payments-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":       "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY":    aPub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":       strings.Join(AdminPermissions, ","),
		"SERVICE_CALLER_PAYMENTS_SERVICE_KID":    "p1",
		"SERVICE_CALLER_PAYMENTS_SERVICE_PUBKEY": pPub,
		// Deliberately over-granted: a caller other than admin-service must
		// still not reach an admin route.
		"SERVICE_CALLER_PAYMENTS_SERVICE_OPS": strings.Join(AdminPermissions, ","),
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
	store := storetest.New()
	host := uuid.New()
	st := store.AddStream(host)
	svc := service.New(store, nil, nil, nil, service.Config{})
	h := New(svc).WithServiceVerifier(v).WithInternalKey("k")
	r := gin.New()
	h.RegisterRoutes(r)
	return &adminRig{
		r: r, store: store,
		admin:    mk(IssuerAdminService, "a1", aPriv),
		payments: mk("payments-service", "p1", pPriv),
		rogue:    mk(IssuerAdminService, "a1", rPriv),
		actor:    uuid.New(), host: host, stream: st,
	}
}

func (a *adminRig) token(t *testing.T, s *servicetoken.Signer, perms ...string) string {
	t.Helper()
	tok, err := s.Mint(AudienceLive, "admin-console", perms, nil, time.Minute, servicetoken.WithActor(a.actor.String()))
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (a *adminRig) do(method, path, tok string, body any) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok != "" {
		req.Header.Set(ServiceAuthHeader, "Bearer "+tok)
	}
	req.Header.Set("X-User-Id", uuid.NewString()) // must be ignored
	rec := httptest.NewRecorder()
	a.r.ServeHTTP(rec, req)
	return rec
}

func errCode(rec *httptest.ResponseRecorder) string {
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	return env.Error.Code
}

type adminRoute struct {
	method, path, perm string
	body               any
	audit              string // the audit action an admitted call writes ("" = read)
}

func (a *adminRig) routes() []adminRoute {
	sid := a.stream.ID.String()
	return []adminRoute{
		{http.MethodGet, InternalAdminPrefix + "/streams?status=all", PermStreamsRead, nil, ""},
		{http.MethodGet, InternalAdminPrefix + "/reports?status=open", PermReportsRead, nil, ""},
		{http.MethodGet, InternalAdminPrefix + "/bans?limit=10&offset=0", PermUsersBan, nil, ""},
		{http.MethodPost, InternalAdminPrefix + "/users/" + uuid.NewString() + "/live-ban", PermUsersBan, map[string]string{"reason": "abuse"}, service.AuditUserLiveBan},
		{http.MethodDelete, InternalAdminPrefix + "/users/" + uuid.NewString() + "/live-ban", PermUsersBan, nil, service.AuditUserLiveUnban},
		{http.MethodDelete, InternalAdminPrefix + "/streams/" + sid + "/chat/" + uuid.NewString(), PermChatModerate, map[string]string{"reason": "x"}, ""},
		{http.MethodPost, InternalAdminPrefix + "/reports/" + uuid.NewString() + "/resolve", PermReportsAct, map[string]string{"action": "dismiss", "reason": "x"}, ""},
		// last: it ends the stream
		{http.MethodPost, InternalAdminPrefix + "/streams/" + sid + "/stop", PermStreamsStop, map[string]string{"reason": "violence"}, service.AuditStreamStop},
	}
}

// TestAdminRoutesPerPermission: each route admits only a token carrying its
// own permission, and writes its audit row only when admitted.
func TestAdminRoutesPerPermission(t *testing.T) {
	a := newAdminRig(t)
	for _, rt := range a.routes() {
		before := len(a.store.Audits)
		// No token.
		if rec := a.do(rt.method, rt.path, "", rt.body); rec.Code != http.StatusUnauthorized || errCode(rec) != CodeAdminTokenRequired {
			t.Fatalf("%s %s without a token: %d %s", rt.method, rt.path, rec.Code, rec.Body.String())
		}
		// Every OTHER permission, but not this one.
		var others []string
		for _, p := range AdminPermissions {
			if p != rt.perm {
				others = append(others, p)
			}
		}
		if rec := a.do(rt.method, rt.path, a.token(t, a.admin, others...), rt.body); rec.Code != http.StatusForbidden || errCode(rec) != CodeAdminPermissionScope {
			t.Fatalf("%s %s without %s: %d %s", rt.method, rt.path, rt.perm, rec.Code, rec.Body.String())
		}
		// Right permission, wrong caller / forged key.
		if rec := a.do(rt.method, rt.path, a.token(t, a.payments, rt.perm), rt.body); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s from payments-service: %d", rt.method, rt.path, rec.Code)
		}
		if rec := a.do(rt.method, rt.path, a.token(t, a.rogue, rt.perm), rt.body); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s with a forged key: %d", rt.method, rt.path, rec.Code)
		}
		if len(a.store.Audits) != before {
			t.Fatalf("%s %s: a refused call wrote an audit row", rt.method, rt.path)
		}
		// Admitted.
		rec := a.do(rt.method, rt.path, a.token(t, a.admin, rt.perm), rt.body)
		if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
			t.Fatalf("%s %s with %s refused: %d %s", rt.method, rt.path, rt.perm, rec.Code, rec.Body.String())
		}
		if rt.audit != "" {
			if rec.Code != http.StatusOK || len(a.store.Audits) != before+1 {
				t.Fatalf("%s %s: %d, audits %d->%d", rt.method, rt.path, rec.Code, before, len(a.store.Audits))
			}
			got := a.store.Audits[len(a.store.Audits)-1]
			if got.Action != rt.audit || got.ActorID != a.actor {
				t.Fatalf("%s %s audit = %+v (actor must be the token's act, not X-User-Id)", rt.method, rt.path, got)
			}
		}
	}
	if s := a.store.Stream(a.stream.ID); s.Status != postgres.StatusEnded || *s.EndedReason != service.ReasonAdminStopped {
		t.Fatalf("stop did not end the stream: %+v", s)
	}
}

// TestResolveNeedsTheActionsOwnPermission: ban_user also needs
// live:users.ban; remove_message also needs live:chat.moderate.
func TestResolveNeedsTheActionsOwnPermission(t *testing.T) {
	a := newAdminRig(t)
	author := uuid.New()
	msg, _ := a.store.InsertChatMessage(context.Background(), a.stream.ID, author, "scam")
	newReport := func() *postgres.Report {
		rep, err := a.store.CreateReport(context.Background(), postgres.NewReport{StreamID: a.stream.ID, ReporterID: uuid.New(), MessageID: &msg.ID, TargetUserID: author, Reason: "scam"}, 100, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	path := func(r *postgres.Report) string { return InternalAdminPrefix + "/reports/" + r.ID.String() + "/resolve" }

	r1 := newReport()
	rec := a.do(http.MethodPost, path(r1), a.token(t, a.admin, PermReportsAct), map[string]string{"action": "ban_user", "reason": "x"})
	if rec.Code != http.StatusForbidden || errCode(rec) != CodeAdminPermissionScope || !strings.Contains(rec.Body.String(), PermUsersBan) {
		t.Fatalf("ban_user without users.ban: %d %s", rec.Code, rec.Body.String())
	}
	rec = a.do(http.MethodPost, path(r1), a.token(t, a.admin, PermReportsAct), map[string]string{"action": "remove_message", "reason": "x"})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), PermChatModerate) {
		t.Fatalf("remove_message without chat.moderate: %d %s", rec.Code, rec.Body.String())
	}
	if len(a.store.Audits) != 0 {
		t.Fatalf("refused resolutions wrote audit rows: %+v", a.store.Audits)
	}
	if banned, _ := a.store.IsBannedFromStream(context.Background(), a.stream.ID, author); banned {
		t.Fatal("a refused ban_user banned")
	}
	// chat.moderate alone (without reports.act) never reaches the handler.
	rec = a.do(http.MethodPost, path(r1), a.token(t, a.admin, PermChatModerate, PermUsersBan), map[string]string{"action": "ban_user", "reason": "x"})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), PermReportsAct) {
		t.Fatalf("without reports.act: %d %s", rec.Code, rec.Body.String())
	}
	// Both permissions: admitted.
	rec = a.do(http.MethodPost, path(r1), a.token(t, a.admin, PermReportsAct, PermUsersBan), map[string]string{"action": "ban_user", "reason": "x"})
	if rec.Code != http.StatusOK {
		t.Fatalf("ban_user with both: %d %s", rec.Code, rec.Body.String())
	}
	if banned, _ := a.store.IsBannedFromStream(context.Background(), a.stream.ID, author); !banned {
		t.Fatal("ban_user did not ban")
	}
	r2 := newReport()
	rec = a.do(http.MethodPost, path(r2), a.token(t, a.admin, PermReportsAct, PermChatModerate), map[string]string{"action": "remove_message", "reason": "x"})
	if rec.Code != http.StatusOK {
		t.Fatalf("remove_message with both: %d %s", rec.Code, rec.Body.String())
	}
	if len(a.store.Audits) != 2 || a.store.Audits[0].Action != service.AuditReportResolve {
		t.Fatalf("audits = %+v", a.store.Audits)
	}
	// dismiss needs nothing extra.
	r3 := newReport()
	if rec := a.do(http.MethodPost, path(r3), a.token(t, a.admin, PermReportsAct), map[string]string{"action": "dismiss", "reason": "x"}); rec.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body.String())
	}
}

// TestAdminServiceCallersFromEnv: blank = none; a half-configured caller is
// an error, never allow-all.
func TestAdminServiceCallersFromEnv(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if v, err := ServiceCallersFromEnv(get(nil)); v != nil || err != nil {
		t.Fatalf("blank: %v %v", v, err)
	}
	pub, _, _ := servicetoken.GenerateKeypair()
	for _, m := range []map[string]string{
		{"SERVICE_CALLERS": "admin-service"},
		{"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_KID": "a1", "SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub},
	} {
		if _, err := ServiceCallersFromEnv(get(m)); err == nil {
			t.Fatalf("accepted %v", m)
		}
	}
	// No verifier configured: the family answers 401.
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New(storetest.New(), nil, nil, nil, service.Config{})).RegisterRoutes(r)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, InternalAdminPrefix+"/streams", nil)
	req.Header.Set(ServiceAuthHeader, "Bearer x.y.z")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || errCode(rec) != CodeServiceCredentialRequired {
		t.Fatalf("no verifier: %d %s", rec.Code, rec.Body.String())
	}
}
