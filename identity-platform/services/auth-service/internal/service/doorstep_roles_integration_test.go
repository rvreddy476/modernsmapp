//go:build integration

package service

// Doorstep (4 Oct 2026): the fifth ecosystem role, service_professional, and
// the doorstep admin app reach the auth.user_roles CHECK constraints on BOTH a
// fresh database and one that already exists with the previous lists.
//
//	AUTH_ROLES_POSTGRES_DSN=postgres://.../identity_roles_it_test \
//	  GOWORK=off go test -tags integration ./internal/service/ -run IntegrationDoorstep
//
// Same guard as roles_integration_test.go: the database name must end in _test.

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/identity-auth-service/database"
	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/roles"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// preDoorstepConstraints puts the two CHECKs back exactly as every existing
// environment has them before this change: eleven roles, fourteen apps.
var preDoorstepConstraints = []string{
	`ALTER TABLE auth.user_roles DROP CONSTRAINT IF EXISTS user_roles_role_check`,
	`ALTER TABLE auth.user_roles ADD CONSTRAINT user_roles_role_check CHECK (role IN (
		'superadmin','admin','moderator',
		'finance','support','kyc_reviewer','auditor',
		'seller','restaurant_owner','delivery_partner','rider_partner'))`,
	`ALTER TABLE auth.user_roles DROP CONSTRAINT IF EXISTS user_roles_app_check`,
	`ALTER TABLE auth.user_roles ADD CONSTRAINT user_roles_app_check CHECK (app IS NULL OR app IN (
		'dating','food','commerce','monetization','payments','wallet','social',
		'tube','qa','chat','live','rider','trust_safety','platform'))`,
}

func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

// assertDoorstepAccepted grants service_professional through the service layer
// (audited as doorstep-service) and writes a doorstep-scoped moderator row,
// then checks the database still refuses what it must.
func assertDoorstepAccepted(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	st := store.New(pool)
	svc := New(st, nil, &config.Config{}, nil, nil, nil)
	pro := uuid.New()

	for i := 0; i < 2; i++ { // idempotent on the real key
		if err := svc.GrantEcosystemRole(ctx, "doorstep-service", pro, roles.ServiceProfessional, "professional created"); err != nil {
			t.Fatalf("grant service_professional #%d: %v", i+1, err)
		}
	}
	if got := svc.ResolveRoles(ctx, pro); len(got) != 1 || got[0] != roles.ServiceProfessional {
		t.Fatalf("resolved roles = %v, want [service_professional]", got)
	}
	var audited int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM auth.admin_audit
		WHERE target_id = $1 AND actor_service = 'doorstep-service' AND action = 'role.grant' AND allowed`, pro).Scan(&audited); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if audited != 2 {
		t.Fatalf("doorstep-service grant audit rows = %d, want 2", audited)
	}
	if err := svc.RevokeEcosystemRole(ctx, "doorstep-service", pro, roles.ServiceProfessional, "professional blocked"); err != nil {
		t.Fatalf("revoke service_professional: %v", err)
	}
	if got := svc.ResolveRoles(ctx, pro); len(got) != 0 {
		t.Fatalf("roles after revoke = %v, want none", got)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO auth.user_roles (user_id, role, app, reason) VALUES (gen_random_uuid(), 'moderator', 'doorstep', 'doorstep pilot')`); err != nil {
		t.Fatalf("doorstep-scoped moderator refused: %v", err)
	}
	for _, stmt := range []string{
		// An ecosystem role is never app-scoped.
		`INSERT INTO auth.user_roles (user_id, role, app) VALUES (gen_random_uuid(), 'service_professional', 'doorstep')`,
		// Near-misses stay out.
		`INSERT INTO auth.user_roles (user_id, role) VALUES (gen_random_uuid(), 'service-professional')`,
		`INSERT INTO auth.user_roles (user_id, role, app) VALUES (gen_random_uuid(), 'moderator', 'door_step')`,
	} {
		if _, err := pool.Exec(ctx, stmt); !isCheckViolation(err) {
			t.Fatalf("want 23514, got %v for: %s", err, stmt)
		}
	}
}

// TestIntegrationDoorstepExistingDatabase: an environment that already has the
// pre-Doorstep CHECKs (and a row) is migrated by the next boot of setup.sql.
func TestIntegrationDoorstepExistingDatabase(t *testing.T) {
	pool := rolesPool(t)
	resetRoles(t, pool)
	ctx := context.Background()
	for _, stmt := range preDoorstepConstraints {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("restore pre-Doorstep constraint: %v", err)
		}
	}
	legacy := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO auth.user_roles (user_id, role) VALUES ($1, 'rider_partner')`, legacy); err != nil {
		t.Fatalf("legacy row: %v", err)
	}
	// Prove the starting point really is the old database: it refuses both.
	if _, err := pool.Exec(ctx, `INSERT INTO auth.user_roles (user_id, role) VALUES (gen_random_uuid(), 'service_professional')`); !isCheckViolation(err) {
		t.Fatalf("old role CHECK accepted service_professional (err %v); the fixture is not the old database", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO auth.user_roles (user_id, role, app) VALUES (gen_random_uuid(), 'moderator', 'doorstep')`); !isCheckViolation(err) {
		t.Fatalf("old app CHECK accepted doorstep (err %v); the fixture is not the old database", err)
	}

	for i := 0; i < 2; i++ { // the boot path, twice: idempotent
		if err := store.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
			t.Fatalf("boot %d: %v", i+1, err)
		}
	}
	var kept int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM auth.user_roles WHERE user_id = $1 AND role = 'rider_partner'`, legacy).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("legacy row after migration: %d, %v", kept, err)
	}
	assertDoorstepAccepted(t, pool)
}

// TestIntegrationDoorstepFreshDatabase: a database with no auth.user_roles at
// all gets the new lists from the inline CHECK and the ALTERs.
func TestIntegrationDoorstepFreshDatabase(t *testing.T) {
	pool := rolesPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DROP TABLE auth.user_roles`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("fresh boot: %v", err)
	}
	assertDoorstepAccepted(t, pool)
}
