//go:build integration

package service

// Live PostgreSQL tests for admin sessions (admin console A2). Same database
// and guard as roles_integration_test.go: AUTH_ROLES_POSTGRES_DSN, name must
// end in "_test".

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func itUser(t *testing.T, pool *pgxpool.Pool, totp bool, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO auth.users (user_id, email, password_hash, two_factor_enabled, account_status)
		VALUES ($1, $2, 'unused', $3, $4)`, id, id.String()+"@a2.test", totp, status); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// itEnrolled makes id an active, TOTP-enrolled account: since A2 the role
// gate re-reads the actor's enrolment live.
func itEnrolled(t *testing.T, pool *pgxpool.Pool, ids ...uuid.UUID) {
	t.Helper()
	for _, id := range ids {
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO auth.users (user_id, email, password_hash, two_factor_enabled, account_status)
			VALUES ($1, $2, 'unused', TRUE, 'active') ON CONFLICT (user_id) DO NOTHING`, id, id.String()+"@a2.test"); err != nil {
			t.Fatalf("insert enrolled user: %v", err)
		}
	}
}

func itSession(t *testing.T, st *store.Store, user uuid.UUID) uuid.UUID {
	t.Helper()
	now := time.Now()
	sess := &store.Session{
		ID: uuid.New(), UserID: user, RefreshToken: uuid.NewString(),
		CreatedAt: now, ExpiresAt: now.Add(time.Hour), AuthTime: &now, AMR: []string{AMRPassword},
	}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return sess.ID
}

func liveSessionCount(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM auth.sessions WHERE user_id = $1 AND revoked_at IS NULL AND is_active`, user).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func itService(t *testing.T, pool *pgxpool.Pool, cfg *config.Config) (*Service, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	if cfg.AccessTokenTTL == 0 {
		cfg.AccessTokenTTL = 15 * time.Minute
	}
	return New(store.New(pool), nil, cfg, slog.Default(), redis.NewClient(&redis.Options{Addr: mr.Addr()}), nil), mr
}

// auth_time and amr persist on the session row and come back on every read.
func TestIntegrationSessionAuthColumns(t *testing.T) {
	pool := rolesPool(t)
	ctx := context.Background()
	st := store.New(pool)
	u := itUser(t, pool, true, "active")
	sid := itSession(t, st, u)

	got, err := st.GetSessionByID(ctx, sid)
	if err != nil || got == nil || got.AuthTime == nil || len(got.AMR) != 1 || got.AMR[0] != AMRPassword {
		t.Fatalf("session=%+v err=%v", got, err)
	}
	for i := 0; i < 2; i++ { // idempotent
		if err := st.AddSessionAMR(ctx, sid, AMROTP); err != nil {
			t.Fatal(err)
		}
	}
	list, err := st.ListActiveSessions(ctx, u)
	if err != nil || len(list) != 1 || strings.Join(list[0].AMR, ",") != "pwd,otp" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if err := st.RevokeSession(ctx, sid); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSessionAMR(ctx, sid, AMROTP); !errors.Is(err, store.ErrSessionNotLive) {
		t.Fatalf("amr on a revoked session: got %v", err)
	}
}

// A revoke, and a renewal that shortens the expiry, revoke every live
// session of the target in the role-change transaction and mark each one in
// Redis; a fresh grant and an extension do not.
func TestIntegrationRoleChangeRevokesSessions(t *testing.T) {
	pool := rolesPool(t)
	resetRoles(t, pool)
	ctx := stepped()
	st := store.New(pool)
	super := itUser(t, pool, true, "active")
	target := itUser(t, pool, false, "active")
	if err := st.GrantRole(context.Background(), super, uuid.Nil, "superadmin"); err != nil {
		t.Fatal(err)
	}
	svc, mr := itService(t, pool, &config.Config{})

	s1, s2 := itSession(t, st, target), itSession(t, st, target)
	req := RoleChangeRequest{TargetID: target, Role: "moderator", App: "dating", Reason: "pilot"}
	if err := svc.GrantRole(ctx, super, req); err != nil {
		t.Fatal(err)
	}
	if liveSessionCount(t, pool, target) != 2 {
		t.Fatal("a fresh grant revoked sessions")
	}

	far := time.Now().Add(72 * time.Hour)
	req.ExpiresAt = &far // nil -> expiry: shortening
	if err := svc.GrantRole(ctx, super, req); err != nil {
		t.Fatal(err)
	}
	if n := liveSessionCount(t, pool, target); n != 0 {
		t.Fatalf("expiry shortening left %d live sessions", n)
	}
	for _, sid := range []uuid.UUID{s1, s2} {
		if v, _ := mr.Get("sess_revoked:" + sid.String()); v != "1" {
			t.Fatalf("session %s not marked in redis", sid)
		}
	}
	var detail string
	_ = pool.QueryRow(context.Background(), `SELECT detail FROM auth.admin_audit WHERE target_id=$1 AND action='role.grant'
		ORDER BY created_at DESC LIMIT 1`, target).Scan(&detail)
	if !strings.Contains(detail, "sessions_revoked=2") {
		t.Fatalf("audit detail %q lacks sessions_revoked=2", detail)
	}

	itSession(t, st, target)
	farther := far.Add(24 * time.Hour)
	req.ExpiresAt = &farther // extension
	if err := svc.GrantRole(ctx, super, req); err != nil {
		t.Fatal(err)
	}
	if liveSessionCount(t, pool, target) != 1 {
		t.Fatal("an expiry extension revoked sessions")
	}
	nearer := time.Now().Add(time.Hour)
	req.ExpiresAt = &nearer // earlier: shortening
	if err := svc.GrantRole(ctx, super, req); err != nil {
		t.Fatal(err)
	}
	if liveSessionCount(t, pool, target) != 0 {
		t.Fatal("moving the expiry earlier did not revoke sessions")
	}

	s4 := itSession(t, st, target)
	if err := svc.RevokeRole(ctx, super, req); err != nil {
		t.Fatal(err)
	}
	if liveSessionCount(t, pool, target) != 0 {
		t.Fatal("revoke did not revoke sessions")
	}
	if v, _ := mr.Get("sess_revoked:" + s4.String()); v != "1" {
		t.Fatal("revoked session not marked in redis")
	}
	itSession(t, st, target)
	if err := svc.RevokeRole(ctx, super, req); err != nil { // nothing left to revoke
		t.Fatal(err)
	}
	if liveSessionCount(t, pool, target) != 1 {
		t.Fatal("a no-op revoke revoked sessions")
	}

	// Without step-up nothing changes at all.
	s6 := itSession(t, st, target)
	noStep := WithSessionAuth(context.Background(), SessionAuth{AdminMFA: true})
	if err := svc.GrantRole(noStep, super, req); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("grant without step-up: got %v", err)
	}
	if v, _ := mr.Get("sess_revoked:" + s6.String()); v != "" {
		t.Fatal("refused change touched sessions")
	}
}

// Force logout: sessions and audit row commit together; an audit failure
// leaves every session live.
func TestIntegrationForceLogoutAuditedInTx(t *testing.T) {
	pool := rolesPool(t)
	resetRoles(t, pool)
	st := store.New(pool)
	super := itUser(t, pool, true, "active")
	target := itUser(t, pool, false, "active")
	if err := st.GrantRole(context.Background(), super, uuid.Nil, "superadmin"); err != nil {
		t.Fatal(err)
	}
	itSession(t, st, target)
	itSession(t, st, target)

	// Audit row violating admin_audit_actor_present: the revocation rolls back.
	if _, err := st.RevokeAllSessionsAudited(context.Background(), target,
		store.RoleAudit{Action: "session.force_logout", TargetID: target, Detail: "x"}); err == nil {
		t.Fatal("audit insert should have failed")
	}
	if liveSessionCount(t, pool, target) != 2 {
		t.Fatal("sessions revoked although the audit row failed")
	}

	svc, _ := itService(t, pool, &config.Config{})
	n, err := svc.ForceLogout(stepped(), super, target, "account compromised")
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if liveSessionCount(t, pool, target) != 0 || auditCount(t, pool, target, "session.force_logout") != 1 {
		t.Fatal("force logout did not revoke and audit")
	}
	var detail string
	_ = pool.QueryRow(context.Background(), `SELECT detail FROM auth.admin_audit WHERE target_id=$1 AND action='session.force_logout'`, target).Scan(&detail)
	if detail != "reason=account compromised sessions_revoked=2" {
		t.Fatalf("detail=%q", detail)
	}
}

// Holder count over real rows: others only, TOTP only, active accounts and
// active grants only, env superadmins included.
func TestIntegrationCountOtherHolders(t *testing.T) {
	pool := rolesPool(t)
	resetRoles(t, pool)
	ctx := context.Background()
	st := store.New(pool)
	requester := itUser(t, pool, true, "active")
	other := itUser(t, pool, true, "active")
	noTOTP := itUser(t, pool, false, "active")
	deactivated := itUser(t, pool, true, "deactivated")
	expired := itUser(t, pool, true, "active")
	support := itUser(t, pool, true, "active")
	envSuper := itUser(t, pool, true, "active")

	grant := func(u uuid.UUID, role, app string, exp *time.Time) {
		t.Helper()
		if _, err := st.ChangeRole(ctx, store.RoleChange{UserID: u, GrantedBy: envSuper, Role: role, App: app, ExpiresAt: exp, Reason: "it"},
			store.RoleAudit{ActorID: envSuper, TargetID: u, Action: "role.grant", Detail: "it"}, nil); err != nil {
			t.Fatalf("grant: %v", err)
		}
	}
	for _, u := range []uuid.UUID{requester, other, noTOTP, deactivated} {
		grant(u, "finance", "payments", nil)
	}
	soon := time.Now().Add(2 * time.Second)
	grant(expired, "finance", "payments", &soon)
	grant(support, "support", "payments", nil)

	svc, _ := itService(t, pool, &config.Config{ScopeSuperadminUserIDs: map[string]struct{}{envSuper.String(): {}}})
	time.Sleep(2500 * time.Millisecond) // let the short grant expire

	n, err := svc.CountOtherHolders(ctx, "payments:refund.issue", requester)
	if err != nil || n != 2 {
		t.Fatalf("count=%d err=%v, want 2 (other + env superadmin)", n, err)
	}
	if n, _ := svc.CountOtherHolders(ctx, "payments:refunds.read", requester); n != 3 {
		t.Fatalf("refunds.read others=%d, want 3 (other, support, env superadmin)", n)
	}
}
