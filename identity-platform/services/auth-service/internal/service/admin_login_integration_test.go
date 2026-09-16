//go:build integration

package service

// Live PostgreSQL tests for the admin console session kind (B3). Same database
// and guard as the A2 tests: AUTH_ROLES_POSTGRES_DSN, name must end in "_test".

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-auth-service/pkg/accesstoken"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func itKindSession(t *testing.T, st *store.Store, user uuid.UUID, kind, refresh string, amr []string) *store.Session {
	t.Helper()
	now := time.Now()
	sess := &store.Session{
		ID: uuid.New(), UserID: user, RefreshToken: hashToken(refresh),
		CreatedAt: now, ExpiresAt: now.Add(2 * time.Hour).Truncate(time.Microsecond), AuthTime: &now, AMR: amr, Kind: kind,
	}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	return sess
}

func itRevoked(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) bool {
	t.Helper()
	var revoked bool
	if err := pool.QueryRow(context.Background(),
		`SELECT revoked_at IS NOT NULL FROM auth.sessions WHERE session_id = $1`, id).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	return revoked
}

// The kind column defaults to consumer, round-trips admin, and rejects
// anything else.
func TestIntegrationSessionKindColumn(t *testing.T) {
	pool := rolesPool(t)
	ctx := context.Background()
	st := store.New(pool)
	u := itUser(t, pool, true, "active")

	legacy := itSession(t, st, u) // Kind unset
	got, err := st.GetSessionByID(ctx, legacy)
	if err != nil || got.Kind != store.SessionKindConsumer {
		t.Fatalf("default kind = %q (%v)", got.Kind, err)
	}
	admin := itKindSession(t, st, u, store.SessionKindAdmin, uuid.NewString(), []string{AMRPassword, AMROTP})
	got, err = st.GetSessionByID(ctx, admin.ID)
	if err != nil || !got.IsAdmin() {
		t.Fatalf("admin kind did not round-trip: %+v (%v)", got, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE auth.sessions SET kind = 'bogus' WHERE session_id = $1`, admin.ID); err == nil {
		t.Fatal("the kind CHECK constraint accepted an unknown kind")
	}
}

// On the real store: a consumer refresh token does not authenticate the admin
// refresh and vice versa; the admin refresh keeps the absolute expiry; a role
// revoke ends the admin session on its next refresh.
func TestIntegrationAdminRefreshKinds(t *testing.T) {
	pool := rolesPool(t)
	resetRoles(t, pool)
	ctx := context.Background()
	st := store.New(pool)
	u := itUser(t, pool, true, "active")
	if _, err := pool.Exec(ctx, `INSERT INTO auth.user_roles (user_id, role) VALUES ($1, 'superadmin')`, u); err != nil {
		t.Fatalf("grant: %v", err)
	}
	svc, _ := itService(t, pool, &config.Config{JWTSecret: "it-secret", RefreshTokenTTL: 30 * 24 * time.Hour, AdminSessionTTL: 2 * time.Hour})

	consumer := itKindSession(t, st, u, store.SessionKindConsumer, "consumer-refresh-"+uuid.NewString(), []string{AMRPassword, AMROTP})
	consumerToken := "consumer-refresh-token-" + uuid.NewString()
	if _, err := pool.Exec(ctx, `UPDATE auth.sessions SET refresh_token_hash = $1 WHERE session_id = $2`, hashToken(consumerToken), consumer.ID); err != nil {
		t.Fatal(err)
	}
	adminToken := "admin-refresh-token-" + uuid.NewString()
	admin := itKindSession(t, st, u, store.SessionKindAdmin, adminToken, []string{AMRPassword, AMROTP})

	if _, err := svc.AdminRefreshSession(ctx, consumerToken, "", ""); !errors.Is(err, ErrWrongSessionKind) {
		t.Fatalf("admin refresh with a consumer token: %v", err)
	}
	if _, err := svc.RefreshSession(ctx, adminToken, "", ""); !errors.Is(err, ErrWrongSessionKind) {
		t.Fatalf("consumer refresh with an admin token: %v", err)
	}
	if itRevoked(t, pool, consumer.ID) || itRevoked(t, pool, admin.ID) {
		t.Fatal("a refused cross-kind refresh revoked a session")
	}

	rotated, err := svc.AdminRefreshSession(ctx, adminToken, "", "")
	if err != nil {
		t.Fatalf("admin refresh: %v", err)
	}
	after, _ := st.GetSessionByID(ctx, admin.ID)
	if !after.ExpiresAt.Equal(admin.ExpiresAt) {
		t.Fatalf("admin refresh moved expiry %v -> %v", admin.ExpiresAt, after.ExpiresAt)
	}
	c := &accesstoken.Claims{}
	if _, err := jwt.ParseWithClaims(rotated.Tokens.AccessToken, c, func(*jwt.Token) (interface{}, error) { return []byte("it-secret"), nil }); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !c.AdminMFA || c.SessionKind != accesstoken.SessionKindAdmin {
		t.Fatalf("refreshed admin token admin_mfa=%v sk=%q", c.AdminMFA, c.SessionKind)
	}

	resetRoles(t, pool)
	if _, err := svc.AdminRefreshSession(ctx, rotated.Tokens.RefreshToken, "", ""); !errors.Is(err, ErrAdminAccessLost) {
		t.Fatalf("refresh after revoke: %v", err)
	}
	if !itRevoked(t, pool, admin.ID) {
		t.Fatal("admin session survived the revoke")
	}
	if itRevoked(t, pool, consumer.ID) {
		t.Fatal("ending the admin session revoked the consumer session")
	}
}
