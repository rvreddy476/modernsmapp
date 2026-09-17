//go:build integration

package service

// Live PostgreSQL tests for the admin console token path (B4 Access page).
// Same database and rules as roles_integration_test.go:
//
//	AUTH_ROLES_POSTGRES_DSN=postgres://.../identity_roles_it_test \
//	  go test -tags integration ./internal/service/ -run Integration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// auditRows returns (actor_id, detail) of every audit row for target+action.
func auditRows(t *testing.T, pool *pgxpool.Pool, target uuid.UUID, action string) []struct {
	Actor  *uuid.UUID
	Detail string
} {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT actor_id, COALESCE(detail,'') FROM auth.admin_audit WHERE target_id = $1 AND action = $2 ORDER BY created_at`, target, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []struct {
		Actor  *uuid.UUID
		Detail string
	}
	for rows.Next() {
		var r struct {
			Actor  *uuid.UUID
			Detail string
		}
		if err := rows.Scan(&r.Actor, &r.Detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// TestIntegrationConsoleGrantRevokeAudited: a console grant with NO session
// on the context writes the row and exactly one audit row whose actor_id is
// act and whose detail marks admin-service and the jti; revoke likewise, and
// it revokes the target's sessions.
func TestIntegrationConsoleGrantRevokeAudited(t *testing.T) {
	pool := rolesPool(t)
	resetRoles(t, pool)
	ctx := context.Background()
	st := store.New(pool)
	super, target := uuid.New(), uuid.New()
	itEnrolled(t, pool, super, target)
	if err := st.GrantRole(ctx, super, uuid.Nil, "superadmin"); err != nil {
		t.Fatal(err)
	}
	svc := New(st, nil, &config.Config{}, nil, nil, nil)
	actor := ConsoleActor{UserID: super, JTI: "it-jti-1"}

	if err := svc.ConsoleGrantRole(ctx, actor, RoleChangeRequest{TargetID: target, Role: "moderator", App: "dating", Reason: "pilot"}); err != nil {
		t.Fatalf("console grant: %v", err)
	}
	var gb uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT granted_by FROM auth.user_roles WHERE user_id=$1 AND role='moderator' AND app='dating'`, target).Scan(&gb); err != nil || gb != super {
		t.Fatalf("row granted_by=%v err=%v", gb, err)
	}
	rows := auditRows(t, pool, target, "role.grant")
	if len(rows) != 1 || rows[0].Actor == nil || *rows[0].Actor != super ||
		!strings.HasPrefix(rows[0].Detail, "via=admin-service jti=it-jti-1 role=moderator app=dating") {
		t.Fatalf("grant audit rows = %+v", rows)
	}
	perms, err := svc.PermissionsForUser(ctx, target)
	if err != nil || !perms.Has("dating:reports.act") {
		t.Fatalf("perms=%+v err=%v", perms, err)
	}

	sid := itSession(t, st, target)
	if err := svc.ConsoleRevokeRole(ctx, ConsoleActor{UserID: super, JTI: "it-jti-2"}, RoleChangeRequest{TargetID: target, Role: "moderator", App: "dating", Reason: "done"}); err != nil {
		t.Fatalf("console revoke: %v", err)
	}
	rows = auditRows(t, pool, target, "role.revoke")
	if len(rows) != 1 || *rows[0].Actor != super || !strings.Contains(rows[0].Detail, "jti=it-jti-2") || !strings.Contains(rows[0].Detail, "sessions_revoked=1") {
		t.Fatalf("revoke audit rows = %+v", rows)
	}
	var revoked bool
	_ = pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM auth.sessions WHERE session_id=$1`, sid).Scan(&revoked)
	if !revoked {
		t.Fatal("target session survived the revoke")
	}
	if n, _ := st.RoleGrantsForUser(ctx, target); len(n) != 0 {
		t.Fatalf("grants after revoke: %+v", n)
	}
}

// TestIntegrationConsoleBootstrapAndGuards: superadmin is untouchable by a
// non-superadmin act (nothing written, one denied audit row); the last
// superadmin is protected; env holders are unrevocable.
func TestIntegrationConsoleBootstrapAndGuards(t *testing.T) {
	pool := rolesPool(t)
	resetRoles(t, pool)
	ctx := context.Background()
	st := store.New(pool)
	admin, super, target, envSuper := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	itEnrolled(t, pool, admin, super, target)
	if err := st.GrantRole(ctx, admin, uuid.Nil, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.GrantRole(ctx, super, uuid.Nil, "superadmin"); err != nil {
		t.Fatal(err)
	}
	svc := New(st, nil, &config.Config{ScopeSuperadminUserIDs: map[string]struct{}{envSuper.String(): {}}}, nil, nil, nil)

	err := svc.ConsoleGrantRole(ctx, ConsoleActor{UserID: admin, JTI: "j"}, RoleChangeRequest{TargetID: target, Role: "superadmin", Reason: "power"})
	if !errors.Is(err, ErrSuperadminChangeRequiresSuperadmin) {
		t.Fatalf("admin granting superadmin: %v", err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM auth.user_roles WHERE user_id=$1`, target).Scan(&n)
	if n != 0 {
		t.Fatal("superadmin row written by a non-superadmin actor")
	}
	rows := auditRows(t, pool, target, "role.grant")
	if len(rows) != 1 || *rows[0].Actor != admin || !strings.Contains(rows[0].Detail, "denied: superadmin change by a non-superadmin") {
		t.Fatalf("denied audit = %+v", rows)
	}
	// The admin actor may still grant an ordinary role.
	if err := svc.ConsoleGrantRole(ctx, ConsoleActor{UserID: admin, JTI: "j2"}, RoleChangeRequest{TargetID: target, Role: "moderator", App: "qa", Reason: "qa pilot"}); err != nil {
		t.Fatalf("admin granting moderator: %v", err)
	}
	// Last durable superadmin (the env holder is not in the DB; `super` is
	// the only row) — with the env allowlist counted, revoking super is
	// allowed, so test the guard with an env-less service.
	plain := New(st, nil, &config.Config{}, nil, nil, nil)
	if err := plain.ConsoleRevokeRole(ctx, ConsoleActor{UserID: super, JTI: "j3"}, RoleChangeRequest{TargetID: super, Role: "superadmin", Reason: "leaving"}); !errors.Is(err, ErrLastSuperadmin) {
		t.Fatalf("last superadmin: %v", err)
	}
	if !plain.IsSuperadmin(ctx, super) {
		t.Fatal("last superadmin removed")
	}
	// Env allowlist holder: 409-class error, nothing written.
	err = svc.ConsoleRevokeRole(ctx, ConsoleActor{UserID: super, JTI: "j4"}, RoleChangeRequest{TargetID: envSuper, Role: "superadmin", Reason: "x"})
	if !errors.Is(err, ErrEnvBootstrapRole) {
		t.Fatalf("env revoke: %v", err)
	}
	// Self-grant.
	if err := svc.ConsoleGrantRole(ctx, ConsoleActor{UserID: super, JTI: "j5"}, RoleChangeRequest{TargetID: super, Role: "admin", App: "dating", Reason: "me"}); !errors.Is(err, ErrSelfGrant) {
		t.Fatalf("self-grant: %v", err)
	}
}

// TestIntegrationConsoleForceLogout: every live session is revoked, one
// audit row with actor = act and the via marker.
func TestIntegrationConsoleForceLogout(t *testing.T) {
	pool := rolesPool(t)
	resetRoles(t, pool)
	ctx := context.Background()
	st := store.New(pool)
	actor, target := uuid.New(), uuid.New()
	itEnrolled(t, pool, actor, target)
	s1, s2 := itSession(t, st, target), itSession(t, st, target)
	svc := New(st, nil, &config.Config{}, nil, nil, nil)

	n, err := svc.ConsoleForceLogout(ctx, ConsoleActor{UserID: actor, JTI: "fl-1"}, target, "compromised")
	if err != nil || n != 2 {
		t.Fatalf("force logout: n=%d err=%v", n, err)
	}
	for _, sid := range []uuid.UUID{s1, s2} {
		var revoked bool
		_ = pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL AND NOT is_active FROM auth.sessions WHERE session_id=$1`, sid).Scan(&revoked)
		if !revoked {
			t.Fatalf("session %s still live", sid)
		}
	}
	rows := auditRows(t, pool, target, "session.force_logout")
	if len(rows) != 1 || *rows[0].Actor != actor || rows[0].Detail != "via=admin-service jti=fl-1 reason=compromised sessions_revoked=2" {
		t.Fatalf("audit = %+v", rows)
	}
}

// TestIntegrationConsoleReads: holders carry mfa_enrolled and page; the
// audit filter selects by actor and window; search finds by email prefix,
// handle prefix and id, and returns only the masked email and the handle.
func TestIntegrationConsoleReads(t *testing.T) {
	pool := rolesPool(t)
	resetRoles(t, pool)
	ctx := context.Background()
	st := store.New(pool)
	super, a, b := uuid.New(), uuid.New(), uuid.New()
	itEnrolled(t, pool, super, a)
	// b: no TOTP, and a profile handle.
	if _, err := pool.Exec(ctx, `INSERT INTO auth.users (user_id, email, password_hash, two_factor_enabled, account_status)
		VALUES ($1, 'zed.holder@console.test', 'unused', FALSE, 'active')`, b); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO profile.profiles (user_id, username, display_name) VALUES ($1, 'zedhandle', 'Zed')
		ON CONFLICT (user_id) DO UPDATE SET username = EXCLUDED.username`, b); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM auth.users WHERE user_id = ANY($1)`, []uuid.UUID{super, a, b})
	})
	if err := st.GrantRole(ctx, super, uuid.Nil, "superadmin"); err != nil {
		t.Fatal(err)
	}
	svc := New(st, nil, &config.Config{}, nil, nil, nil)
	actor := ConsoleActor{UserID: super, JTI: "r"}
	if err := svc.ConsoleGrantRole(ctx, actor, RoleChangeRequest{TargetID: a, Role: "moderator", App: "dating", Reason: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.ConsoleGrantRole(ctx, actor, RoleChangeRequest{TargetID: b, Role: "finance", App: "food", Reason: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := st.GrantRole(ctx, b, uuid.Nil, "seller"); err != nil {
		t.Fatal(err)
	}

	// Holders: admin roles only by default, with MFA status; sellers only by name.
	out, err := svc.ConsoleListRoleHolders(ctx, store.RoleHolderFilter{})
	if err != nil {
		t.Fatal(err)
	}
	mfa := map[uuid.UUID]bool{}
	for _, h := range out.Holders {
		if h.Role == "seller" {
			t.Fatal("seller listed among admin roles by default")
		}
		mfa[h.UserID] = h.MFAEnrolled
	}
	if len(out.Holders) != 3 || !mfa[super] || !mfa[a] || mfa[b] {
		t.Fatalf("holders = %+v", out.Holders)
	}
	out, _ = svc.ConsoleListRoleHolders(ctx, store.RoleHolderFilter{Role: "seller"})
	if len(out.Holders) != 1 || out.Holders[0].UserID != b {
		t.Fatalf("seller holders = %+v", out.Holders)
	}
	out, _ = svc.ConsoleListRoleHolders(ctx, store.RoleHolderFilter{Scope: store.AppExact, App: "food"})
	if len(out.Holders) != 1 || out.Holders[0].Role != "finance" {
		t.Fatalf("food holders = %+v", out.Holders)
	}
	out, _ = svc.ConsoleListRoleHolders(ctx, store.RoleHolderFilter{Scope: store.AppPlatformWide})
	if len(out.Holders) != 1 || out.Holders[0].Role != "superadmin" {
		t.Fatalf("platform-wide holders = %+v", out.Holders)
	}
	page1, _ := svc.ConsoleListRoleHolders(ctx, store.RoleHolderFilter{Limit: 2})
	page2, _ := svc.ConsoleListRoleHolders(ctx, store.RoleHolderFilter{Limit: 2, Offset: 2})
	if len(page1.Holders) != 2 || len(page2.Holders) != 1 {
		t.Fatalf("paging: %d + %d", len(page1.Holders), len(page2.Holders))
	}

	// Audit: by actor, by target, by window.
	entries, err := svc.ConsoleListAudit(ctx, store.AuditFilter{Actor: &super})
	if err != nil || len(entries) != 2 {
		t.Fatalf("audit by actor: %d %v", len(entries), err)
	}
	entries, _ = svc.ConsoleListAudit(ctx, store.AuditFilter{Target: &b, Action: "role.grant"})
	if len(entries) != 1 || !strings.Contains(entries[0].Detail, "role=finance app=food") {
		t.Fatalf("audit by target: %+v", entries)
	}
	past := time.Now().Add(-time.Hour)
	entries, _ = svc.ConsoleListAudit(ctx, store.AuditFilter{Actor: &super, To: &past})
	if len(entries) != 0 {
		t.Fatalf("audit window: %+v", entries)
	}

	// Search: prefix on email, prefix on handle (with or without @), by id.
	res, err := svc.ConsoleSearchUsers(ctx, "zed.h", 0)
	if err != nil || len(res) != 1 || res[0].UserID != b.String() || res[0].EmailMasked != "z***@console.test" || res[0].Handle != "zedhandle" {
		t.Fatalf("email search: %+v %v", res, err)
	}
	res, _ = svc.ConsoleSearchUsers(ctx, "@zedh", 0)
	if len(res) != 1 || res[0].UserID != b.String() {
		t.Fatalf("handle search: %+v", res)
	}
	res, _ = svc.ConsoleSearchUsers(ctx, a.String(), 0)
	if len(res) != 1 || res[0].UserID != a.String() || res[0].Handle != "" {
		t.Fatalf("id search: %+v", res)
	}
	res, _ = svc.ConsoleSearchUsers(ctx, "%", 0)
	if len(res) != 0 {
		t.Fatalf("LIKE metacharacter matched everything: %d", len(res))
	}
	// No personal data beyond the masked email and handle.
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), "zed.holder") || strings.Contains(string(raw), "console.test\"") && !strings.Contains(string(raw), "***@") {
		t.Fatalf("search leaked the raw email: %s", raw)
	}
	var m []map[string]any
	raw, _ = json.Marshal(mustSearch(t, svc, ctx, "zed.h"))
	_ = json.Unmarshal(raw, &m)
	if len(m) != 1 || len(m[0]) != 3 {
		t.Fatalf("search result shape: %s", raw)
	}
}

func mustSearch(t *testing.T, svc *Service, ctx context.Context, q string) []UserSearchResult {
	t.Helper()
	res, err := svc.ConsoleSearchUsers(ctx, q, 0)
	if err != nil {
		t.Fatal(err)
	}
	return res
}
