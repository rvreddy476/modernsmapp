package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-auth-service/internal/servicetoken/tokentest"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const consolePrefix = "/v1/auth/internal/admin"

// consoleRig is a router whose console family trusts one admin-service key.
type consoleRig struct {
	r     *gin.Engine
	h     *Handler
	svc   *stubAuthService
	kp    tokentest.Keypair
	actor uuid.UUID
}

func newConsoleRig(t *testing.T, rdb *redis.Client) *consoleRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	kp := tokentest.NewKeypair()
	svc := &stubAuthService{}
	cfg := &config.Config{InternalServiceKey: permsInternalKey, AdminServiceTokenPubKey: kp.PubB64, AdminServiceTokenKID: "a1"}
	h := New(svc, cfg, nil, rdb)
	r := gin.New()
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())
	return &consoleRig{r: r, h: h, svc: svc, kp: kp, actor: uuid.New()}
}

// token mints an admin-service token for the rig's actor with the given
// scope, applying any tweaks.
func (rg *consoleRig) token(scope string, tweak ...func(*tokentest.Options)) string {
	o := tokentest.Options{Issuer: IssuerAdminService, KID: "a1", Subject: "admin-console", Audience: AudienceIdentity,
		Scope: []string{scope}, Actor: rg.actor.String()}
	for _, f := range tweak {
		f(&o)
	}
	return tokentest.Mint(rg.kp, o)
}

func (rg *consoleRig) do(method, path, tok string, body any, headers map[string]string) *httptest.ResponseRecorder {
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, consolePrefix+path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok != "" {
		req.Header.Set(ServiceAuthHeader, "Bearer "+tok)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp := httptest.NewRecorder()
	rg.r.ServeHTTP(resp, req)
	return resp
}

func wantCode(t *testing.T, resp *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if resp.Code != status || (code != "" && !strings.Contains(resp.Body.String(), `"`+code+`"`)) {
		t.Fatalf("got %d %s; want %d %s", resp.Code, resp.Body.String(), status, code)
	}
}

// TestConsoleGrant_AdmittedWithSignedActor: a valid admin-service token
// reaches the service with the actor taken from the SIGNED act claim and
// the token's jti, and the request body becomes the role change.
func TestConsoleGrant_AdmittedWithSignedActor(t *testing.T) {
	rg := newConsoleRig(t, nil)
	target := uuid.New()
	var got service.ConsoleActor
	var gotReq service.RoleChangeRequest
	rg.svc.console.grantFn = func(a service.ConsoleActor, req service.RoleChangeRequest) error {
		got, gotReq = a, req
		return nil
	}
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	resp := rg.do(http.MethodPost, "/users/"+target.String()+"/roles", rg.token(PermRolesManage),
		map[string]any{"role": "moderator", "app": "dating", "reason": "pilot", "expires_at": exp}, nil)
	wantCode(t, resp, http.StatusOK, "granted")
	if got.UserID != rg.actor || got.JTI == "" {
		t.Fatalf("actor = %+v want %s with a jti", got, rg.actor)
	}
	if gotReq.TargetID != target || gotReq.Role != "moderator" || gotReq.App != "dating" || gotReq.Reason != "pilot" ||
		gotReq.ExpiresAt == nil || !gotReq.ExpiresAt.Equal(exp) {
		t.Fatalf("req = %+v", gotReq)
	}
}

// TestConsole_ActorComesFromTokenNotHeaders: the actor header a user request
// would carry is refused outright, and an X-Admin-Id (or any other header)
// never replaces the signed actor.
func TestConsole_ActorComesFromTokenNotHeaders(t *testing.T) {
	rg := newConsoleRig(t, nil)
	target := uuid.New()
	for _, hdr := range gatewayIdentityHeaders {
		t.Run("user identity refused: "+hdr, func(t *testing.T) {
			called := false
			rg.svc.console.grantFn = func(service.ConsoleActor, service.RoleChangeRequest) error { called = true; return nil }
			resp := rg.do(http.MethodPost, "/users/"+target.String()+"/roles", rg.token(PermRolesManage),
				map[string]any{"role": "moderator", "reason": "x"}, map[string]string{hdr: uuid.NewString()})
			wantCode(t, resp, http.StatusForbidden, CodeUserCallerRefused)
			if called {
				t.Fatal("service reached with a user identity header present")
			}
		})
	}
	t.Run("internal key plus X-Admin-Id is not a token", func(t *testing.T) {
		resp := rg.do(http.MethodPost, "/users/"+target.String()+"/roles", "",
			map[string]any{"role": "moderator", "reason": "x"},
			map[string]string{"X-Internal-Service-Key": permsInternalKey, "X-Admin-Id": uuid.NewString()})
		wantCode(t, resp, http.StatusUnauthorized, CodeAdminTokenRequired)
	})
	t.Run("X-Admin-Id beside a token is ignored", func(t *testing.T) {
		var got service.ConsoleActor
		rg.svc.console.grantFn = func(a service.ConsoleActor, _ service.RoleChangeRequest) error { got = a; return nil }
		resp := rg.do(http.MethodPost, "/users/"+target.String()+"/roles", rg.token(PermRolesManage),
			map[string]any{"role": "moderator", "reason": "x"}, map[string]string{"X-Admin-Id": uuid.NewString()})
		wantCode(t, resp, http.StatusOK, "granted")
		if got.UserID != rg.actor {
			t.Fatalf("actor = %s want the signed %s", got.UserID, rg.actor)
		}
	})
}

// TestConsole_TokenRefusals: every way a token can be wrong is refused
// before the service is reached.
func TestConsole_TokenRefusals(t *testing.T) {
	rg := newConsoleRig(t, nil)
	other := tokentest.NewKeypair()
	target := uuid.New()
	path := "/users/" + target.String() + "/roles"
	cases := []struct {
		name   string
		tok    string
		status int
		code   string
	}{
		{"wrong audience", rg.token(PermRolesManage, func(o *tokentest.Options) { o.Audience = "dating" }), 403, CodeServiceTokenRejected},
		{"missing act", rg.token(PermRolesManage, func(o *tokentest.Options) { o.Actor = "" }), 403, CodeAdminActorRequired},
		{"act not a uuid", rg.token(PermRolesManage, func(o *tokentest.Options) { o.Actor = "root" }), 403, CodeAdminActorRequired},
		{"act nil uuid", rg.token(PermRolesManage, func(o *tokentest.Options) { o.Actor = uuid.Nil.String() }), 403, CodeAdminActorRequired},
		{"wrong scope (read for a write)", rg.token(PermRolesRead), 403, CodeAdminPermissionScope},
		{"scope outside identity's list", rg.token("dating:users.ban"), 403, CodeAdminPermissionScope},
		{"expired", rg.token(PermRolesManage, func(o *tokentest.Options) { o.Now = time.Now().Add(-3 * time.Minute) }), 403, CodeServiceTokenRejected},
		{"unknown caller (issuer)", rg.token(PermRolesManage, func(o *tokentest.Options) { o.Issuer = "food-service" }), 403, CodeServiceTokenRejected},
		{"unknown kid", rg.token(PermRolesManage, func(o *tokentest.Options) { o.KID = "a2" }), 403, CodeServiceTokenRejected},
		{"foreign key", tokentest.Mint(other, tokentest.Options{Issuer: IssuerAdminService, KID: "a1", Audience: AudienceIdentity, Scope: []string{PermRolesManage}, Actor: rg.actor.String()}), 403, CodeServiceTokenRejected},
		{"no jti", rg.token(PermRolesManage, func(o *tokentest.Options) { o.NoJTI = true }), 403, CodeServiceTokenRejected},
		{"alg none", rg.token(PermRolesManage, func(o *tokentest.Options) { o.Alg = "none" }), 403, CodeServiceTokenRejected},
		{"garbage", "not.a.token", 403, CodeServiceTokenRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			rg.svc.console.grantFn = func(service.ConsoleActor, service.RoleChangeRequest) error { called = true; return nil }
			resp := rg.do(http.MethodPost, path, tc.tok, map[string]any{"role": "moderator", "reason": "x"}, nil)
			wantCode(t, resp, tc.status, tc.code)
			if called {
				t.Fatal("service reached with a refused token")
			}
		})
	}
	t.Run("no token", func(t *testing.T) {
		wantCode(t, rg.do(http.MethodPost, path, "", map[string]any{"role": "moderator", "reason": "x"}, nil), 401, CodeAdminTokenRequired)
	})
	t.Run("no verifier configured", func(t *testing.T) {
		rg.h.SetAdminTokenVerifier(nil)
		defer func() {
			v, _ := AdminServiceVerifier(&config.Config{AdminServiceTokenPubKey: rg.kp.PubB64, AdminServiceTokenKID: "a1"})
			rg.h.SetAdminTokenVerifier(v)
		}()
		wantCode(t, rg.do(http.MethodPost, path, rg.token(PermRolesManage), map[string]any{"role": "moderator", "reason": "x"}, nil), 401, CodeServiceTokenUnavailable)
	})
}

// TestConsole_ReplayRefused: with Redis, a jti is good once.
func TestConsole_ReplayRefused(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	rg := newConsoleRig(t, redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	tok := rg.token(PermRolesRead)
	wantCode(t, rg.do(http.MethodGet, "/roles", tok, nil, nil), 200, "holders")
	wantCode(t, rg.do(http.MethodGet, "/roles", tok, nil, nil), 403, CodeServiceTokenReplayed)
	// A fresh token for the same actor is fine.
	wantCode(t, rg.do(http.MethodGet, "/roles", rg.token(PermRolesRead), nil, nil), 200, "holders")
	// The record lives for the token's lifetime, not forever.
	if ttl := mr.TTL("admin_svc_jti:" + jtiOf(t, tok)); ttl <= 0 || ttl > 61*time.Second {
		t.Fatalf("jti ttl = %v", ttl)
	}
}

func jtiOf(t *testing.T, tok string) string {
	t.Helper()
	var claims struct {
		JTI string `json:"jti"`
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[1])
	if err != nil || json.Unmarshal(raw, &claims) != nil {
		t.Fatal("cannot read jti")
	}
	return claims.JTI
}

// TestConsole_RouteTable: each route needs its own permission and maps the
// request onto the service call; the audit route takes either read scope.
func TestConsole_RouteTable(t *testing.T) {
	rg := newConsoleRig(t, nil)
	target := uuid.New()

	t.Run("list holders filters", func(t *testing.T) {
		var got store.RoleHolderFilter
		rg.svc.console.holdersFn = func(f store.RoleHolderFilter) (service.ConsoleRoleHolders, error) {
			got = f
			return service.ConsoleRoleHolders{Holders: []store.RoleHolder{{UserID: target, Role: "moderator", MFAEnrolled: true, Active: true}}, EnvHolders: []service.EnvRoleHolder{}}, nil
		}
		resp := rg.do(http.MethodGet, "/roles?role=moderator&app=dating&limit=10&offset=20", rg.token(PermRolesRead), nil, nil)
		wantCode(t, resp, 200, "mfa_enrolled")
		if got.Role != "moderator" || got.Scope != store.AppExact || got.App != "dating" || got.Limit != 10 || got.Offset != 20 {
			t.Fatalf("filter = %+v", got)
		}
		rg.do(http.MethodGet, "/roles?app=platform-wide", rg.token(PermRolesRead), nil, nil)
		if got.Scope != store.AppPlatformWide || got.App != "" {
			t.Fatalf("platform-wide filter = %+v", got)
		}
		wantCode(t, rg.do(http.MethodGet, "/roles", rg.token(PermRolesManage), nil, nil), 403, CodeAdminPermissionScope)
	})

	t.Run("user roles", func(t *testing.T) {
		rg.svc.console.userRolesFn = func(u uuid.UUID) ([]store.UserRole, error) {
			if u != target {
				t.Fatalf("target %s", u)
			}
			return []store.UserRole{{UserID: u, Role: "admin", Active: true}}, nil
		}
		wantCode(t, rg.do(http.MethodGet, "/users/"+target.String()+"/roles", rg.token(PermRolesRead), nil, nil), 200, "admin")
		wantCode(t, rg.do(http.MethodGet, "/users/not-a-uuid/roles", rg.token(PermRolesRead), nil, nil), 400, "BAD_REQUEST")
	})

	t.Run("revoke takes role from the path and app/reason from query or body", func(t *testing.T) {
		var got service.RoleChangeRequest
		rg.svc.console.revokeFn = func(_ service.ConsoleActor, req service.RoleChangeRequest) error { got = req; return nil }
		wantCode(t, rg.do(http.MethodDelete, "/users/"+target.String()+"/roles/moderator?app=dating&reason=done", rg.token(PermRolesManage), nil, nil), 200, "revoked")
		if got.TargetID != target || got.Role != "moderator" || got.App != "dating" || got.Reason != "done" {
			t.Fatalf("query revoke = %+v", got)
		}
		wantCode(t, rg.do(http.MethodDelete, "/users/"+target.String()+"/roles/admin", rg.token(PermRolesManage), map[string]any{"reason": "body wins"}, nil), 200, "revoked")
		if got.Role != "admin" || got.App != "" || got.Reason != "body wins" {
			t.Fatalf("body revoke = %+v", got)
		}
		wantCode(t, rg.do(http.MethodDelete, "/users/"+target.String()+"/roles/admin", rg.token(PermRolesRead), nil, nil), 403, CodeAdminPermissionScope)
	})

	t.Run("force logout needs sessions.revoke", func(t *testing.T) {
		rg.svc.console.forceLogoutFn = func(a service.ConsoleActor, u uuid.UUID, reason string) (int, error) {
			if a.UserID != rg.actor || u != target || reason != "compromised" {
				t.Fatalf("force logout %+v %s %q", a, u, reason)
			}
			return 2, nil
		}
		wantCode(t, rg.do(http.MethodPost, "/users/"+target.String()+"/sessions/revoke", rg.token(PermSessionsRevoke), map[string]any{"reason": "compromised"}, nil), 200, "sessions_revoked")
		wantCode(t, rg.do(http.MethodPost, "/users/"+target.String()+"/sessions/revoke", rg.token(PermRolesManage), map[string]any{"reason": "x"}, nil), 403, CodeAdminPermissionScope)
	})

	t.Run("audit accepts roles.read or audit.read and validates filters", func(t *testing.T) {
		var got store.AuditFilter
		rg.svc.console.auditFn = func(f store.AuditFilter) ([]store.AdminAuditEntry, error) {
			got = f
			return []store.AdminAuditEntry{}, nil
		}
		actor := uuid.New()
		q := "/audit?actor=" + actor.String() + "&action=role.grant&from=2026-09-01T00:00:00Z&to=2026-09-17T00:00:00Z&limit=5"
		wantCode(t, rg.do(http.MethodGet, q, rg.token(PermRolesRead), nil, nil), 200, "entries")
		if got.Actor == nil || *got.Actor != actor || got.Target != nil || got.Action != "role.grant" || got.From == nil || got.To == nil || got.Limit != 5 {
			t.Fatalf("filter = %+v", got)
		}
		wantCode(t, rg.do(http.MethodGet, "/audit", rg.token(PermAuditRead), nil, nil), 200, "entries")
		wantCode(t, rg.do(http.MethodGet, "/audit?actor=nope", rg.token(PermRolesRead), nil, nil), 400, "BAD_REQUEST")
		wantCode(t, rg.do(http.MethodGet, "/audit?from=yesterday", rg.token(PermRolesRead), nil, nil), 400, "BAD_REQUEST")
		wantCode(t, rg.do(http.MethodGet, "/audit", rg.token(PermUsersSearch), nil, nil), 403, CodeAdminPermissionScope)
	})

	t.Run("search returns only id, masked email and handle", func(t *testing.T) {
		rg.svc.console.searchFn = func(q string, limit int) ([]service.UserSearchResult, error) {
			if q != "rag" {
				t.Fatalf("q = %q", q)
			}
			return []service.UserSearchResult{{UserID: target.String(), EmailMasked: "r***@example.com", Handle: "raghu"}}, nil
		}
		resp := rg.do(http.MethodGet, "/users/search?q=rag", rg.token(PermUsersSearch), nil, nil)
		wantCode(t, resp, 200, "email_masked")
		var env struct {
			Data struct {
				Results []map[string]any `json:"results"`
			} `json:"data"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil || len(env.Data.Results) != 1 {
			t.Fatalf("body %s", resp.Body.String())
		}
		if len(env.Data.Results[0]) != 3 {
			t.Fatalf("search result has extra fields: %v", env.Data.Results[0])
		}
		wantCode(t, rg.do(http.MethodGet, "/users/search?q=rag", rg.token(PermRolesRead), nil, nil), 403, CodeAdminPermissionScope)
	})

	t.Run("service errors map to the role vocabulary", func(t *testing.T) {
		for _, tc := range []struct {
			err    error
			status int
			code   string
		}{
			{service.ErrSuperadminChangeRequiresSuperadmin, 403, CodeSuperadminRequired},
			{service.ErrSelfGrant, 403, "SELF_GRANT_REFUSED"},
			{service.ErrLastSuperadmin, 409, "LAST_SUPERADMIN"},
			{&service.EnvBootstrapRoleError{Role: "superadmin", EnvVar: "SUPERADMIN_USER_IDS"}, 409, "ENV_BOOTSTRAP_ROLE"},
			{service.ErrInvalidApp, 400, "INVALID_APP"},
			{service.ErrReasonRequired, 400, "REASON_REQUIRED"},
			{errors.New("boom"), 500, "INTERNAL_ERROR"},
		} {
			rg.svc.console.grantFn = func(service.ConsoleActor, service.RoleChangeRequest) error { return tc.err }
			wantCode(t, rg.do(http.MethodPost, "/users/"+target.String()+"/roles", rg.token(PermRolesManage), map[string]any{"role": "moderator", "reason": "x"}, nil), tc.status, tc.code)
		}
		rg.svc.console.searchFn = func(string, int) ([]service.UserSearchResult, error) { return nil, service.ErrSearchQueryTooShort }
		wantCode(t, rg.do(http.MethodGet, "/users/search?q=r", rg.token(PermUsersSearch), nil, nil), 400, "BAD_REQUEST")
	})
}

// TestAdminServiceVerifier_Config: no key → no verifier; key without kid or
// an unreadable key → error (New panics on it).
func TestAdminServiceVerifier_Config(t *testing.T) {
	if v, err := AdminServiceVerifier(&config.Config{}); v != nil || err != nil {
		t.Fatalf("empty config: %v %v", v, err)
	}
	kp := tokentest.NewKeypair()
	if _, err := AdminServiceVerifier(&config.Config{AdminServiceTokenPubKey: kp.PubB64}); err == nil {
		t.Fatal("key without kid accepted")
	}
	if _, err := AdminServiceVerifier(&config.Config{AdminServiceTokenPubKey: "@@@", AdminServiceTokenKID: "a1"}); err == nil {
		t.Fatal("unreadable key accepted")
	}
	v, err := AdminServiceVerifier(&config.Config{AdminServiceTokenPubKey: kp.PubB64, AdminServiceTokenKID: "a1"})
	if err != nil || v == nil || v.Callers() != 1 {
		t.Fatalf("good config: %v %v", v, err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("New did not panic on a bad key")
		}
	}()
	New(&stubAuthService{}, &config.Config{AdminServiceTokenPubKey: "@@@", AdminServiceTokenKID: "a1"}, nil, nil)
}
