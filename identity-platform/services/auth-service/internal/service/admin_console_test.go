package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// consoleActor is an admin-service token's act + jti.
func consoleActor(id uuid.UUID) ConsoleActor { return ConsoleActor{UserID: id, JTI: "jti-1"} }

// TestConsoleGrant_NoSessionOneAuditRowActorIsAct: the token path needs no
// session claims on the context (admin-service enforced MFA and step-up
// before signing), writes exactly one audit row IN THE TRANSACTION whose
// actor is act, marked as coming through admin-service with the jti.
func TestConsoleGrant_NoSessionOneAuditRowActorIsAct(t *testing.T) {
	ctx := context.Background() // no SessionAuth at all
	super, target := uuid.New(), uuid.New()
	st := newRoleTestStore()
	st.roles[super] = []string{"superadmin"}
	svc := newRoleSvc(t, st)

	if err := svc.ConsoleGrantRole(ctx, consoleActor(super), grantReq(target, "moderator", "dating")); err != nil {
		t.Fatalf("console grant: %v", err)
	}
	if len(st.changes) != 1 || len(st.audits) != 1 || st.audits[0] != "role.grant:ok" {
		t.Fatalf("changes=%d audits=%v", len(st.changes), st.audits)
	}
	ch, au := st.changes[0], st.txAudits[0]
	if ch.GrantedBy != super || ch.UserID != target || ch.App != "dating" {
		t.Fatalf("change = %+v", ch)
	}
	if au.ActorID != super || au.TargetID != target || au.Action != "role.grant" {
		t.Fatalf("audit = %+v", au)
	}
	if !strings.HasPrefix(au.Detail, ConsoleVia+" jti=jti-1 ") || !strings.Contains(au.Detail, "role=moderator app=dating") {
		t.Fatalf("audit detail does not mark the path: %q", au.Detail)
	}

	// The session path is unchanged: the same request without session
	// claims is still refused there.
	if err := svc.GrantRole(ctx, super, grantReq(target, "moderator", "food")); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("session path without claims: got %v want ErrMFARequired", err)
	}
}

// TestConsoleGrant_NonSuperadminActorMayGrantNonSuperadminRoles: the token
// path trusts admin-service's permission check for ordinary roles (a
// platform admin holding roles.manage would be admitted by admin-service),
// and does not demand superadmin as the session path does.
func TestConsoleGrant_NonSuperadminActorMayGrantNonSuperadminRoles(t *testing.T) {
	actor, target := uuid.New(), uuid.New()
	st := newRoleTestStore()
	st.roles[actor] = []string{"admin"}
	svc := newRoleSvc(t, st)
	if err := svc.ConsoleGrantRole(context.Background(), consoleActor(actor), grantReq(target, "moderator", "dating")); err != nil {
		t.Fatalf("admin actor grant moderator: %v", err)
	}
	if len(st.changes) != 1 {
		t.Fatal("no change written")
	}
}

// TestConsoleGrant_SuperadminNeedsSuperadminActor (bootstrap safety): the
// superadmin role is granted or revoked through admin-service only by an
// actor who is a superadmin NOW, whatever the token's scope says.
func TestConsoleGrant_SuperadminNeedsSuperadminActor(t *testing.T) {
	ctx := context.Background()
	admin, super, target := uuid.New(), uuid.New(), uuid.New()
	st := newRoleTestStore()
	st.roles[admin] = []string{"admin"}
	st.roles[super] = []string{"superadmin"}
	svc := newRoleSvc(t, st)

	err := svc.ConsoleGrantRole(ctx, consoleActor(admin), grantReq(target, "superadmin", ""))
	if !errors.Is(err, ErrSuperadminChangeRequiresSuperadmin) {
		t.Fatalf("non-superadmin granting superadmin: got %v", err)
	}
	if len(st.changes) != 0 || len(st.audits) != 1 || st.audits[0] != "role.grant:denied" {
		t.Fatalf("refused grant must write nothing and one denied audit: changes=%d audits=%v", len(st.changes), st.audits)
	}
	err = svc.ConsoleRevokeRole(ctx, consoleActor(admin), grantReq(super, "superadmin", ""))
	if !errors.Is(err, ErrSuperadminChangeRequiresSuperadmin) {
		t.Fatalf("non-superadmin revoking superadmin: got %v", err)
	}
	if len(st.changes) != 0 {
		t.Fatal("revoke written by a non-superadmin")
	}
	// A superadmin actor may.
	if err := svc.ConsoleGrantRole(ctx, consoleActor(super), grantReq(target, "superadmin", "")); err != nil {
		t.Fatalf("superadmin granting superadmin: %v", err)
	}
	if len(st.changes) != 1 || st.changes[0].Role != "superadmin" {
		t.Fatalf("changes = %+v", st.changes)
	}
	// An env allowlist superadmin counts as one.
	envSuper := uuid.New()
	st2 := newRoleTestStore()
	svc2 := New(st2, nil, &config.Config{ScopeSuperadminUserIDs: map[string]struct{}{envSuper.String(): {}}}, nil, nil, nil)
	if err := svc2.ConsoleGrantRole(ctx, consoleActor(envSuper), grantReq(target, "superadmin", "")); err != nil {
		t.Fatalf("env superadmin granting superadmin: %v", err)
	}
}

// TestConsoleGrant_SelfGrantRefused: act == target is refused and audited.
func TestConsoleGrant_SelfGrantRefused(t *testing.T) {
	super := uuid.New()
	st := newRoleTestStore()
	st.roles[super] = []string{"superadmin"}
	svc := newRoleSvc(t, st)
	err := svc.ConsoleGrantRole(context.Background(), consoleActor(super), grantReq(super, "admin", "dating"))
	if !errors.Is(err, ErrSelfGrant) {
		t.Fatalf("self-grant: got %v", err)
	}
	if len(st.changes) != 0 || len(st.audits) != 1 || st.audits[0] != "role.grant:denied" {
		t.Fatalf("changes=%d audits=%v", len(st.changes), st.audits)
	}
}

// TestConsoleRevoke_LastSuperadminAndEnvBootstrap: the guards of the
// session path apply unchanged.
func TestConsoleRevoke_LastSuperadminAndEnvBootstrap(t *testing.T) {
	ctx := context.Background()
	super, envSuper := uuid.New(), uuid.New()
	st := newRoleTestStore()
	st.roles[super] = []string{"superadmin"}
	svc := newRoleSvc(t, st)
	if err := svc.ConsoleRevokeRole(ctx, consoleActor(super), grantReq(super, "superadmin", "")); !errors.Is(err, ErrLastSuperadmin) {
		t.Fatalf("last superadmin: got %v", err)
	}
	if len(st.changes) != 0 {
		t.Fatal("last superadmin removed")
	}
	if len(st.audits) != 1 || st.audits[0] != "role.revoke:denied" {
		t.Fatalf("audits=%v", st.audits)
	}

	st = newRoleTestStore()
	st.roles[super] = []string{"superadmin"}
	svc = New(st, nil, &config.Config{ScopeSuperadminUserIDs: map[string]struct{}{envSuper.String(): {}}}, nil, nil, nil)
	err := svc.ConsoleRevokeRole(ctx, consoleActor(super), grantReq(envSuper, "admin", ""))
	if !errors.Is(err, ErrEnvBootstrapRole) || !strings.Contains(err.Error(), "SUPERADMIN_USER_IDS") {
		t.Fatalf("env bootstrap revoke: got %v", err)
	}
	if len(st.changes) != 0 {
		t.Fatal("env bootstrap role revoked")
	}
}

// TestConsoleGrant_Validation: catalogue validation is the session path's.
func TestConsoleGrant_Validation(t *testing.T) {
	super, target := uuid.New(), uuid.New()
	st := newRoleTestStore()
	st.roles[super] = []string{"superadmin"}
	svc := newRoleSvc(t, st)
	past := time.Now().Add(-time.Minute)
	for _, tc := range []struct {
		req  RoleChangeRequest
		want error
	}{
		{RoleChangeRequest{TargetID: target, Role: "king", Reason: "x"}, ErrInvalidRole},
		{RoleChangeRequest{TargetID: target, Role: "moderator", App: "casino", Reason: "x"}, ErrInvalidApp},
		{RoleChangeRequest{TargetID: target, Role: "superadmin", App: "dating", Reason: "x"}, ErrRoleNotScopable},
		{RoleChangeRequest{TargetID: target, Role: "moderator", App: "dating"}, ErrReasonRequired},
		{RoleChangeRequest{TargetID: target, Role: "moderator", App: "dating", Reason: "x", ExpiresAt: &past}, ErrInvalidExpiry},
	} {
		if err := svc.ConsoleGrantRole(context.Background(), consoleActor(super), tc.req); !errors.Is(err, tc.want) {
			t.Fatalf("%+v: got %v want %v", tc.req, err, tc.want)
		}
	}
	if len(st.changes) != 0 {
		t.Fatal("invalid request written")
	}
	if err := svc.ConsoleGrantRole(context.Background(), ConsoleActor{}, grantReq(target, "moderator", "dating")); !errors.Is(err, ErrNotSuperadmin) {
		t.Fatalf("nil actor: got %v", err)
	}
}

// TestConsoleRevoke_RevokesTargetSessions: sessions revoked in the store's
// transaction are marked sess_revoked:<sid> for the gateway.
func TestConsoleRevoke_RevokesTargetSessions(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	super, target := uuid.New(), uuid.New()
	s1, s2 := uuid.New(), uuid.New()
	st := newRoleTestStore()
	st.roles[super] = []string{"superadmin"}
	st.liveSessions[target] = []uuid.UUID{s1, s2}
	svc := New(st, nil, &config.Config{AccessTokenTTL: 15 * time.Minute}, nil, rdb, nil)
	if err := svc.ConsoleRevokeRole(context.Background(), consoleActor(super), grantReq(target, "moderator", "dating")); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []uuid.UUID{s1, s2} {
		if v, _ := mr.Get("sess_revoked:" + sid.String()); v != "1" {
			t.Fatalf("session %s not marked revoked", sid)
		}
	}
}

// forceLogoutStore records the audited force logout.
type forceLogoutStore struct {
	*roleTestStore
	audit    store.RoleAudit
	sessions []uuid.UUID
}

func (f *forceLogoutStore) RevokeAllSessionsAudited(_ context.Context, _ uuid.UUID, audit store.RoleAudit) ([]uuid.UUID, error) {
	f.audit = audit
	return f.sessions, nil
}

// TestConsoleForceLogout_AuditsWithActorAndVia: force logout through
// admin-service revokes the sessions, audits with actor = act marked via
// admin-service, and needs a reason.
func TestConsoleForceLogout_AuditsWithActorAndVia(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	actor, target := uuid.New(), uuid.New()
	s1 := uuid.New()
	st := &forceLogoutStore{roleTestStore: newRoleTestStore(), sessions: []uuid.UUID{s1}}
	svc := New(st, nil, &config.Config{AccessTokenTTL: 15 * time.Minute}, nil, rdb, nil)

	if _, err := svc.ConsoleForceLogout(context.Background(), consoleActor(actor), target, "  "); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("blank reason: got %v", err)
	}
	n, err := svc.ConsoleForceLogout(context.Background(), consoleActor(actor), target, "compromised laptop")
	if err != nil || n != 1 {
		t.Fatalf("force logout: n=%d err=%v", n, err)
	}
	if st.audit.ActorID != actor || st.audit.TargetID != target || st.audit.Action != "session.force_logout" ||
		st.audit.Detail != ConsoleVia+" jti=jti-1 reason=compromised laptop" {
		t.Fatalf("audit = %+v", st.audit)
	}
	if v, _ := mr.Get("sess_revoked:" + s1.String()); v != "1" {
		t.Fatal("session not marked revoked")
	}
}

// consoleReadStore answers the console reads.
type consoleReadStore struct {
	*roleTestStore
	holders []store.RoleHolder
	filter  store.RoleHolderFilter
	hits    []store.UserSearchHit
	auditF  store.AuditFilter
}

func (c *consoleReadStore) ListRoleHolders(_ context.Context, f store.RoleHolderFilter) ([]store.RoleHolder, error) {
	c.filter = f
	return c.holders, nil
}
func (c *consoleReadStore) SearchUsers(_ context.Context, _ string, _ int) ([]store.UserSearchHit, error) {
	return c.hits, nil
}
func (c *consoleReadStore) ListAdminAuditFiltered(_ context.Context, f store.AuditFilter) ([]store.AdminAuditEntry, error) {
	c.auditF = f
	return []store.AdminAuditEntry{}, nil
}

// TestConsoleReads: holders include env allowlist holders on the first
// unscoped page only; paging is clamped; search masks the email and
// returns nothing else; short queries are refused.
func TestConsoleReads(t *testing.T) {
	ctx := context.Background()
	envSuper, u := uuid.New(), uuid.New()
	st := &consoleReadStore{roleTestStore: newRoleTestStore(),
		holders: []store.RoleHolder{{UserID: u, Role: "moderator", MFAEnrolled: true}},
		hits:    []store.UserSearchHit{{UserID: u, Email: "raghu@example.com", Handle: "raghu"}, {UserID: envSuper, Email: ""}},
	}
	st.noTOTP[envSuper] = true
	svc := New(st, nil, &config.Config{ScopeSuperadminUserIDs: map[string]struct{}{envSuper.String(): {}}}, nil, nil, nil)

	out, err := svc.ConsoleListRoleHolders(ctx, store.RoleHolderFilter{Limit: 999, Offset: -5})
	if err != nil {
		t.Fatal(err)
	}
	if st.filter.Limit != 50 || st.filter.Offset != 0 {
		t.Fatalf("paging not clamped: %+v", st.filter)
	}
	if len(out.Holders) != 1 || len(out.EnvHolders) != 1 || out.EnvHolders[0].UserID != envSuper ||
		out.EnvHolders[0].Role != "superadmin" || out.EnvHolders[0].Source != "env:SUPERADMIN_USER_IDS" || out.EnvHolders[0].MFAEnrolled {
		t.Fatalf("holders = %+v", out)
	}
	out, _ = svc.ConsoleListRoleHolders(ctx, store.RoleHolderFilter{Offset: 50})
	if len(out.EnvHolders) != 0 {
		t.Fatal("env holders repeated on a later page")
	}
	out, _ = svc.ConsoleListRoleHolders(ctx, store.RoleHolderFilter{Scope: store.AppExact, App: "dating"})
	if len(out.EnvHolders) != 0 {
		t.Fatal("env holders (platform-wide) listed under an app filter")
	}
	out, _ = svc.ConsoleListRoleHolders(ctx, store.RoleHolderFilter{Role: "moderator"})
	if len(out.EnvHolders) != 0 {
		t.Fatal("env superadmin listed as a raw moderator")
	}
	if _, err := svc.ConsoleListRoleHolders(ctx, store.RoleHolderFilter{Role: "king"}); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("bad role filter: %v", err)
	}

	res, err := svc.ConsoleSearchUsers(ctx, "rag", 0)
	if err != nil || len(res) != 2 {
		t.Fatalf("search: %v %v", res, err)
	}
	if res[0].EmailMasked != "r***@example.com" || res[0].Handle != "raghu" || res[0].UserID != u.String() || res[1].EmailMasked != "" {
		t.Fatalf("results = %+v", res)
	}
	for _, q := range []string{"", "r", "@r", " "} {
		if _, err := svc.ConsoleSearchUsers(ctx, q, 0); !errors.Is(err, ErrSearchQueryTooShort) {
			t.Fatalf("query %q: %v", q, err)
		}
	}
	if _, err := svc.ConsoleSearchUsers(ctx, u.String(), 0); err != nil {
		t.Fatalf("uuid query: %v", err)
	}

	if _, err := svc.ConsoleListAudit(ctx, store.AuditFilter{Limit: 0}); err != nil || st.auditF.Limit != 50 {
		t.Fatalf("audit paging: %+v %v", st.auditF, err)
	}
}

func TestMaskEmail(t *testing.T) {
	for in, want := range map[string]string{
		"raghu@example.com": "r***@example.com",
		"a@b.c":             "a***@b.c",
		"":                  "",
		"no-at-sign":        "***",
		"@example.com":      "***",
		"ಕನ್ನಡ@example.com": "ಕ***@example.com",
	} {
		if got := MaskEmail(in); got != want {
			t.Errorf("MaskEmail(%q) = %q want %q", in, got, want)
		}
	}
}
