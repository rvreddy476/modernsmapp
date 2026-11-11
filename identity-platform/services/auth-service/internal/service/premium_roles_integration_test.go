//go:build integration

package service

// Live PostgreSQL tests for the timed role `premium` (Momentum Premium,
// 11 Nov 2026): the CHECK constraints admit it on a fresh AND an existing
// database, refuse it without an expiry, and store.ChangeRole's upsert
// replaces expires_at and revokes sessions when the period shrinks. Same
// database and rules as roles_integration_test.go:
//
//	AUTH_ROLES_POSTGRES_DSN=postgres://.../identity_roles_it_test \
//	  GOWORK=off go test -tags integration ./internal/service/ -run IntegrationPremium

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/database"
	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/roles"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// prePremiumConstraints puts the role CHECK back as every existing
// environment has it before this change, and removes the timed-role CHECK.
var prePremiumConstraints = []string{
	`ALTER TABLE auth.user_roles DROP CONSTRAINT IF EXISTS user_roles_timed_role_check`,
	`ALTER TABLE auth.user_roles DROP CONSTRAINT IF EXISTS user_roles_role_check`,
	`ALTER TABLE auth.user_roles ADD CONSTRAINT user_roles_role_check CHECK (role IN (
		'superadmin','admin','moderator',
		'finance','support','kyc_reviewer','auditor',
		'seller','restaurant_owner','delivery_partner','rider_partner',
		'service_professional'))`,
}

func premiumExpiry(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) *time.Time {
	t.Helper()
	var exp *time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT expires_at FROM auth.user_roles WHERE user_id = $1 AND role = 'premium' AND app IS NULL`, user).Scan(&exp); err != nil {
		t.Fatalf("read premium row: %v", err)
	}
	return exp
}

func sessionRevoked(t *testing.T, pool *pgxpool.Pool, sid uuid.UUID) bool {
	t.Helper()
	var revoked bool
	if err := pool.QueryRow(context.Background(),
		`SELECT revoked_at IS NOT NULL AND NOT is_active FROM auth.sessions WHERE session_id = $1`, sid).Scan(&revoked); err != nil {
		t.Fatalf("read session: %v", err)
	}
	return revoked
}

// assertPremiumLifecycle drives grant, extend, shorten, scopes and revoke
// through the real service and store.
func assertPremiumLifecycle(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	st := store.New(pool)
	svc := New(st, nil, &config.Config{}, nil, nil, nil)
	member := uuid.New()
	itEnrolled(t, pool, member)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM auth.users WHERE user_id = $1`, member)
	})

	// Grant, then extend: the upsert must replace expires_at.
	first := time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second)
	later := first.Add(30 * 24 * time.Hour)
	if err := svc.GrantEcosystemRole(ctx, "monetization-service", member, roles.Premium, "pass PP-1", &first); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if got := premiumExpiry(t, pool, member); got == nil || !got.Equal(first) {
		t.Fatalf("expires_at after grant = %v, want %v", got, first)
	}
	sid := itSession(t, st, member)
	if err := svc.GrantEcosystemRole(ctx, "monetization-service", member, roles.Premium, "pass PP-2", &later); err != nil {
		t.Fatalf("extend: %v", err)
	}
	if got := premiumExpiry(t, pool, member); got == nil || !got.Equal(later) {
		t.Fatalf("expires_at after extension = %v, want %v — the extension never landed", got, later)
	}
	if sessionRevoked(t, pool, sid) {
		t.Fatal("an extension revoked the member's session")
	}
	if got := svc.resolveScopes(ctx, member); got != "premium" {
		t.Fatalf("scopes = %q, want premium", got)
	}

	// Audit: service actor, in the same transaction, with the expiry.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM auth.admin_audit
		WHERE target_id = $1 AND actor_id IS NULL AND actor_service = 'monetization-service'
		  AND action = 'role.grant' AND allowed AND detail LIKE '%expires_at=%'`, member).Scan(&n); err != nil || n != 2 {
		t.Fatalf("service-attributed grant audit rows = %d, %v; want 2", n, err)
	}

	// Shorten: the session is revoked in the same transaction.
	shorter := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	if err := svc.GrantEcosystemRole(ctx, "monetization-service", member, roles.Premium, "refund", &shorter); err != nil {
		t.Fatalf("shorten: %v", err)
	}
	if got := premiumExpiry(t, pool, member); got == nil || !got.Equal(shorter) {
		t.Fatalf("expires_at after shortening = %v, want %v", got, shorter)
	}
	if !sessionRevoked(t, pool, sid) {
		t.Fatal("shortening premium left the member's session live")
	}

	// Expired rows never reach the token.
	if _, err := pool.Exec(ctx, `UPDATE auth.user_roles SET expires_at = NOW() - INTERVAL '1 minute'
		WHERE user_id = $1 AND role = 'premium'`, member); err != nil {
		t.Fatal(err)
	}
	if got := svc.resolveScopes(ctx, member); strings.Contains(got, "premium") {
		t.Fatalf("scopes = %q with an expired premium row", got)
	}

	// Revoking the lapsed row keeps sessions; revoking a live one does not.
	sid2 := itSession(t, st, member)
	if err := svc.RevokeEcosystemRole(ctx, "monetization-service", member, roles.Premium, "period ended"); err != nil {
		t.Fatalf("revoke lapsed: %v", err)
	}
	if sessionRevoked(t, pool, sid2) {
		t.Fatal("revoking an already-lapsed premium row signed the member out")
	}
	if err := svc.GrantEcosystemRole(ctx, "monetization-service", member, roles.Premium, "renewed", &later); err != nil {
		t.Fatalf("re-grant: %v", err)
	}
	if err := svc.RevokeEcosystemRole(ctx, "monetization-service", member, roles.Premium, "chargeback"); err != nil {
		t.Fatalf("revoke live: %v", err)
	}
	if !sessionRevoked(t, pool, sid2) {
		t.Fatal("revoking a live premium row left the session live")
	}
	if got := svc.ResolveRoles(ctx, member); len(got) != 0 {
		t.Fatalf("roles after revoke = %v", got)
	}

	// The database itself refuses premium without an expiry, and app-scoped.
	for _, stmt := range []string{
		`INSERT INTO auth.user_roles (user_id, role) VALUES (gen_random_uuid(), 'premium')`,
		`INSERT INTO auth.user_roles (user_id, role, app, expires_at) VALUES (gen_random_uuid(), 'premium', 'social', NOW() + INTERVAL '1 day')`,
	} {
		if _, err := pool.Exec(ctx, stmt); !isCheckViolation(err) {
			t.Fatalf("want 23514, got %v for: %s", err, stmt)
		}
	}
}

// TestIntegrationPremiumExistingDatabase: an environment with the
// pre-premium CHECK is migrated by the next boot of setup.sql, twice.
func TestIntegrationPremiumExistingDatabase(t *testing.T) {
	pool := rolesPool(t)
	resetRoles(t, pool)
	ctx := context.Background()
	for _, stmt := range prePremiumConstraints {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("restore pre-premium constraint: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO auth.user_roles (user_id, role, expires_at)
		VALUES (gen_random_uuid(), 'premium', NOW() + INTERVAL '1 day')`); !isCheckViolation(err) {
		t.Fatalf("old role CHECK accepted premium (err %v); the fixture is not the old database", err)
	}
	for i := 0; i < 2; i++ {
		if err := store.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
			t.Fatalf("boot %d: %v", i+1, err)
		}
	}
	assertPremiumLifecycle(t, pool)
}

// TestIntegrationPremiumFreshDatabase: a database with no auth.user_roles
// gets premium from the inline CHECK and the ALTERs.
func TestIntegrationPremiumFreshDatabase(t *testing.T) {
	pool := rolesPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DROP TABLE auth.user_roles`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("fresh boot: %v", err)
	}
	assertPremiumLifecycle(t, pool)
}
