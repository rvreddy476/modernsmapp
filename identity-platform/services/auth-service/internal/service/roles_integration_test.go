//go:build integration

package service

// Live PostgreSQL tests for per-application roles (admin console A1).
//
//	AUTH_ROLES_POSTGRES_DSN=postgres://.../identity_roles_it_test \
//	  go test -tags integration ./internal/service/ -run Integration
//
// Its own environment variable, for the reason signup_journey_test.go gives:
// the store suite installs a narrower hand-written auth.users into
// POSTGRES_DSN, and packages run concurrently. This suite applies the REAL
// database/setup.sql through BootstrapSchema, and refuses to run against any
// database whose name does not end in "_test" — it drops and recreates
// auth.user_roles.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/database"
	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func rolesPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("AUTH_ROLES_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AUTH_ROLES_POSTGRES_DSN not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: invalid")
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run: database %q does not end in _test", cfg.ConnConfig.Database)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := store.BootstrapSchema(context.Background(), pool, database.SetupSQL); err != nil {
		t.Fatalf("install schema: %v", err)
	}
	return pool
}

// resetRoles gives each test an empty role table on the real, migrated DDL.
func resetRoles(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `TRUNCATE auth.user_roles`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func auditCount(t *testing.T, pool *pgxpool.Pool, target uuid.UUID, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM auth.admin_audit WHERE target_id = $1 AND action = $2`, target, action).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	return n
}

// TestIntegrationLegacyRowsMigrate: a database created by the OLD DDL — PK
// (user_id, role), three-role CHECK, no app column — keeps its rows as
// platform-wide grants after setup.sql runs again, and the old PK is replaced.
func TestIntegrationLegacyRowsMigrate(t *testing.T) {
	pool := rolesPool(t)
	ctx := context.Background()
	u := uuid.New()
	for _, stmt := range []string{
		`DROP TABLE auth.user_roles`,
		`CREATE TABLE auth.user_roles (
			user_id UUID NOT NULL,
			role TEXT NOT NULL CHECK (role IN ('superadmin','admin','moderator')),
			granted_by UUID,
			granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (user_id, role))`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("legacy ddl: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO auth.user_roles (user_id, role) VALUES ($1,'admin'),($1,'moderator')`, u); err != nil {
		t.Fatalf("legacy rows: %v", err)
	}
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Idempotent: a second boot is a no-op.
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("second boot: %v", err)
	}

	st := store.New(pool)
	got, err := st.RolesForUser(ctx, u)
	if err != nil || len(got) != 2 {
		t.Fatalf("legacy roles after migration = %v, %v", got, err)
	}
	var nullApps int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM auth.user_roles WHERE user_id=$1 AND app IS NULL AND expires_at IS NULL`, u).Scan(&nullApps)
	if nullApps != 2 {
		t.Fatalf("legacy rows must be platform-wide, never-expiring; got %d", nullApps)
	}
	var pk int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE conname = 'user_roles_pkey'`).Scan(&pk)
	if pk != 0 {
		t.Fatal("old (user_id, role) primary key still present")
	}
	svc := New(st, nil, &config.Config{}, nil, nil, nil)
	if roles := svc.ResolveRoles(ctx, u); strings.Join(roles, " ") != "admin moderator" {
		t.Fatalf("scopes source changed for a legacy admin: %v", roles)
	}
	// The ecosystem grant path still idempotent on the new key.
	for i := 0; i < 2; i++ {
		if err := st.GrantRole(ctx, u, uuid.Nil, "seller"); err != nil {
			t.Fatalf("ecosystem grant %d: %v", i, err)
		}
	}
}

func TestIntegrationScopedGrantsAndExpiry(t *testing.T) {
	pool := rolesPool(t)
	resetRoles(t, pool)
	ctx := context.Background()
	st := store.New(pool)
	super, u := uuid.New(), uuid.New()
	if err := st.GrantRole(ctx, super, uuid.Nil, "superadmin"); err != nil {
		t.Fatal(err)
	}
	svc := New(st, nil, &config.Config{}, nil, nil, nil)

	for _, app := range []string{"dating", "food"} {
		if err := svc.GrantRole(ctx, super, RoleChangeRequest{TargetID: u, Role: "moderator", App: app, Reason: "pilot " + app}); err != nil {
			t.Fatalf("grant %s: %v", app, err)
		}
	}
	// Same (user, role, app) twice renews rather than duplicates.
	exp := time.Now().Add(time.Hour)
	if err := svc.GrantRole(ctx, super, RoleChangeRequest{TargetID: u, Role: "moderator", App: "dating", Reason: "renew", ExpiresAt: &exp}); err != nil {
		t.Fatalf("renew: %v", err)
	}
	var rows int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM auth.user_roles WHERE user_id=$1`, u).Scan(&rows)
	if rows != 2 {
		t.Fatalf("rows=%d want 2", rows)
	}
	if n := auditCount(t, pool, u, "role.grant"); n != 3 {
		t.Fatalf("audit rows=%d want 3", n)
	}

	// App-scoped rows never reach the token's scopes source.
	if platform, _ := st.RolesForUser(ctx, u); len(platform) != 0 {
		t.Fatalf("app-scoped moderator leaked into RolesForUser: %v", platform)
	}
	perms, err := svc.PermissionsForUser(ctx, u)
	if err != nil || !perms.Has("dating:reports.act") || !perms.Has("food:reviews.moderate") || perms.Has("qa:reports.act") {
		t.Fatalf("perms=%+v err=%v", perms, err)
	}

	// Expire the food grant in place: it grants nothing, but is still listed.
	if _, err := pool.Exec(ctx, `UPDATE auth.user_roles SET expires_at = NOW() - interval '1 second' WHERE user_id=$1 AND app='food'`, u); err != nil {
		t.Fatal(err)
	}
	grants, _ := st.RoleGrantsForUser(ctx, u)
	if len(grants) != 1 || grants[0].App != "dating" {
		t.Fatalf("active grants=%+v", grants)
	}
	perms, _ = svc.PermissionsForUser(ctx, u)
	if perms.Has("food:reviews.moderate") {
		t.Fatal("expired food grant still resolves")
	}
	listed, _ := st.ListUserRoles(ctx, u)
	if len(listed) != 2 {
		t.Fatalf("listed=%d want 2", len(listed))
	}
	for _, r := range listed {
		if (r.App != nil && *r.App == "food") == r.Active {
			t.Fatalf("active flag wrong for %+v", r)
		}
	}
	// An expired platform superadmin is not a superadmin.
	if _, err := pool.Exec(ctx, `UPDATE auth.user_roles SET expires_at = NOW() - interval '1 second' WHERE user_id=$1`, super); err != nil {
		t.Fatal(err)
	}
	if svc.IsSuperadmin(ctx, super) {
		t.Fatal("expired superadmin still authorised")
	}
}

func TestIntegrationDatabaseRefusesBadScopes(t *testing.T) {
	pool := rolesPool(t)
	resetRoles(t, pool)
	ctx := context.Background()
	for _, stmt := range []string{
		`INSERT INTO auth.user_roles (user_id, role, app) VALUES (gen_random_uuid(), 'superadmin', 'dating')`,
		`INSERT INTO auth.user_roles (user_id, role, app) VALUES (gen_random_uuid(), 'seller', 'commerce')`,
		`INSERT INTO auth.user_roles (user_id, role, app) VALUES (gen_random_uuid(), 'moderator', 'casino')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err == nil {
			t.Fatalf("database accepted: %s", stmt)
		}
	}
}

// TestIntegrationAuditFailureRollsBackGrant: ChangeRole with an audit row that
// violates admin_audit_actor_present (no actor at all) must leave no role row.
func TestIntegrationAuditFailureRollsBackGrant(t *testing.T) {
	pool := rolesPool(t)
	resetRoles(t, pool)
	ctx := context.Background()
	st := store.New(pool)
	u := uuid.New()
	_, err := st.ChangeRole(ctx,
		store.RoleChange{UserID: u, Role: "moderator", App: "dating", Reason: "x"},
		store.RoleAudit{ActorID: uuid.Nil, TargetID: u, Action: "role.grant", Detail: "x"}, nil)
	if err == nil {
		t.Fatal("audit insert should have failed")
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM auth.user_roles WHERE user_id=$1`, u).Scan(&n)
	if n != 0 {
		t.Fatal("role row committed although its audit row failed")
	}
}

func TestIntegrationLastSuperadminAndSelfGrant(t *testing.T) {
	pool := rolesPool(t)
	resetRoles(t, pool)
	ctx := context.Background()
	st := store.New(pool)
	a, b := uuid.New(), uuid.New()
	if err := st.GrantRole(ctx, a, uuid.Nil, "superadmin"); err != nil {
		t.Fatal(err)
	}
	svc := New(st, nil, &config.Config{}, nil, nil, nil)

	if err := svc.RevokeRole(ctx, a, RoleChangeRequest{TargetID: a, Role: "superadmin", Reason: "leaving"}); !errors.Is(err, ErrLastSuperadmin) {
		t.Fatalf("revoke last: %v", err)
	}
	if !svc.IsSuperadmin(ctx, a) {
		t.Fatal("last superadmin was removed")
	}
	if n := auditCount(t, pool, a, "role.revoke"); n != 1 { // the denied attempt only
		t.Fatalf("revoke audit rows=%d want 1 (denied)", n)
	}
	if err := svc.GrantRole(ctx, a, RoleChangeRequest{TargetID: a, Role: "moderator", App: "dating", Reason: "me"}); !errors.Is(err, ErrSelfGrant) {
		t.Fatalf("self-grant: %v", err)
	}

	if err := svc.GrantRole(ctx, a, RoleChangeRequest{TargetID: b, Role: "superadmin", Reason: "second holder"}); err != nil {
		t.Fatalf("grant second: %v", err)
	}
	exp := time.Now().Add(time.Hour)
	// b may put an expiry on a while b is durable...
	if err := svc.GrantRole(ctx, b, RoleChangeRequest{TargetID: a, Role: "superadmin", Reason: "temp", ExpiresAt: &exp}); err != nil {
		t.Fatalf("expire a with b durable: %v", err)
	}
	// ...but a cannot then expire b, the last durable holder.
	if err := svc.GrantRole(ctx, a, RoleChangeRequest{TargetID: b, Role: "superadmin", Reason: "temp", ExpiresAt: &exp}); !errors.Is(err, ErrLastSuperadmin) {
		t.Fatalf("expire last durable: %v", err)
	}
	if err := svc.RevokeRole(ctx, b, RoleChangeRequest{TargetID: a, Role: "superadmin", Reason: "done"}); err != nil {
		t.Fatalf("revoke temporary holder: %v", err)
	}
}

func TestIntegrationEnvBootstrapAudit(t *testing.T) {
	pool := rolesPool(t)
	ctx := context.Background()
	st := store.New(pool)
	envSuper := uuid.New()
	svc := New(st, nil, &config.Config{
		ScopeSuperadminUserIDs: map[string]struct{}{envSuper.String(): {}, "not-a-uuid": {}},
	}, nil, nil, nil)
	for i := 0; i < 3; i++ {
		if err := svc.RecordEnvBootstrap(ctx); err != nil {
			t.Fatalf("bootstrap %d: %v", i, err)
		}
	}
	if n := auditCount(t, pool, envSuper, "role.bootstrap"); n != 1 {
		t.Fatalf("bootstrap audit rows=%d want exactly 1", n)
	}
	err := svc.RevokeRole(ctx, uuid.New(), RoleChangeRequest{TargetID: envSuper, Role: "admin", Reason: "x"})
	if !errors.Is(err, ErrNotSuperadmin) {
		t.Fatalf("non-superadmin revoke: %v", err)
	}
	other := uuid.New()
	resetRoles(t, pool)
	if err := st.GrantRole(ctx, other, uuid.Nil, "superadmin"); err != nil {
		t.Fatal(err)
	}
	err = svc.RevokeRole(ctx, other, RoleChangeRequest{TargetID: envSuper, Role: "superadmin", Reason: "x"})
	if !errors.Is(err, ErrEnvBootstrapRole) || !strings.Contains(err.Error(), "SUPERADMIN_USER_IDS") {
		t.Fatalf("env revoke: %v", err)
	}
}
