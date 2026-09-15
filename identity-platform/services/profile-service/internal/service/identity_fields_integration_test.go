//go:build integration

package service

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The profile DOB rules end to end: real Service, real Store, and the real
// auth.registration_consents table.
//
//	PROFILE_IT_POSTGRES_DSN=postgres://.../identity_profile_it_test \
//	  go test -tags integration -p 1 ./internal/service/ -v
//
// Refuses any database whose name does not end in _test: it writes auth.users.

// Compatible with the store package's integration schema (same definitions),
// since both suites share identity_profile_it_test.
const serviceITSchema = `
CREATE SCHEMA IF NOT EXISTS auth;
CREATE SCHEMA IF NOT EXISTS profile;

CREATE TABLE IF NOT EXISTS auth.users (
    user_id UUID PRIMARY KEY,
    email TEXT,
    phone TEXT,
    account_status TEXT NOT NULL DEFAULT 'active'
);

CREATE TABLE IF NOT EXISTS auth.registration_consents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES auth.users(user_id) ON DELETE CASCADE,
    terms_version TEXT NOT NULL,
    declared_dob DATE,
    accepted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS profile.profiles (
    user_id UUID PRIMARY KEY REFERENCES auth.users(user_id) ON DELETE CASCADE,
    username TEXT,
    display_name TEXT NOT NULL,
    first_name TEXT DEFAULT '',
    last_name TEXT DEFAULT '',
    dob DATE,
    gender TEXT DEFAULT ''
);

ALTER TABLE profile.profiles
    ADD COLUMN IF NOT EXISTS bio                 TEXT DEFAULT '',
    ADD COLUMN IF NOT EXISTS avatar_media_id     UUID,
    ADD COLUMN IF NOT EXISTS cover_media_id      UUID,
    ADD COLUMN IF NOT EXISTS category            TEXT DEFAULT 'personal',
    ADD COLUMN IF NOT EXISTS profession          TEXT DEFAULT '',
    ADD COLUMN IF NOT EXISTS website             TEXT DEFAULT '',
    ADD COLUMN IF NOT EXISTS location            TEXT DEFAULT '',
    ADD COLUMN IF NOT EXISTS badge_flags         INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS preferred_name      TEXT,
    ADD COLUMN IF NOT EXISTS pronouns            TEXT,
    ADD COLUMN IF NOT EXISTS is_verified         BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS verification_level  TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS status_text         TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS status_emoji        TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS status_expires_at   TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS profile_theme_color TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS intro_media_url     TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS intro_media_type    TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cta_label           TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cta_url             TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS member_since_badge  BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS timezone            TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS follower_count      INT     NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS following_count     INT     NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS friend_count        INT     NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS post_count          INT     NOT NULL DEFAULT 0;
`

func serviceITPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PROFILE_IT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PROFILE_IT_POSTGRES_DSN not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		// Deliberately not printing err: it can echo the connection string.
		t.Fatal("PROFILE_IT_POSTGRES_DSN is not a valid DSN")
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: its name must end in _test", cfg.ConnConfig.Database)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal("connect to the test database failed")
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), serviceITSchema); err != nil {
		t.Fatalf("install schema: %v", err)
	}
	return pool
}

type itConsent struct {
	dob *time.Time
	at  time.Time
}

func seedITAccount(t *testing.T, pool *pgxpool.Pool, profileDOB *time.Time, consents ...itConsent) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO auth.users (user_id, email) VALUES ($1, $2)`,
		id, "it-"+id.String()+"@example.test"); err != nil {
		t.Fatalf("seed auth.users: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM auth.users WHERE user_id = $1`, id)
	})
	if _, err := pool.Exec(ctx,
		`INSERT INTO profile.profiles (user_id, display_name, first_name, dob) VALUES ($1, 'IT User', 'Asha', $2)`,
		id, profileDOB); err != nil {
		t.Fatalf("seed profile.profiles: %v", err)
	}
	for _, c := range consents {
		if _, err := pool.Exec(ctx,
			`INSERT INTO auth.registration_consents (user_id, terms_version, declared_dob, accepted_at) VALUES ($1, '2026-08-01', $2, $3)`,
			id, c.dob, c.at); err != nil {
			t.Fatalf("seed auth.registration_consents: %v", err)
		}
	}
	return id
}

func itDate(s string) *time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return &d
}

func itService(pool *pgxpool.Pool) (*Service, *store.Store) {
	st := store.New(pool)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(st, nil, nil, nil, logger).WithProfileWriteStore(st, func() time.Time { return refNow }), st
}

func itStoredDOB(t *testing.T, st *store.Store, id uuid.UUID) string {
	t.Helper()
	p, err := st.GetProfile(context.Background(), id)
	if err != nil || p == nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p.DoB == nil {
		return ""
	}
	return p.DoB.Format("2006-01-02")
}

func itUpdateDOB(svc *Service, id uuid.UUID, dob *time.Time) error {
	_, err := svc.UpdateProfile(context.Background(), id, store.UpdateProfileParams{DisplayName: strOf("IT User"), DoB: dob})
	return err
}

func TestIntegration_DOBChangeIsBoundByTheRegistrationRecord(t *testing.T) {
	pool := serviceITPool(t)
	svc, st := itService(pool)
	t0 := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	id := seedITAccount(t, pool, itDate("1990-03-17"), itConsent{dob: itDate("1990-03-17"), at: t0})

	if got := fieldCode(t, itUpdateDOB(svc, id, itDate("1992-01-01"))); got != CodeDOBMismatchRegistration {
		t.Fatalf("a 21-month move: got %q, want %s", got, CodeDOBMismatchRegistration)
	}
	if got := itStoredDOB(t, st, id); got != "1990-03-17" {
		t.Fatalf("stored DOB = %q after a refusal, want 1990-03-17", got)
	}

	if err := itUpdateDOB(svc, id, itDate("1990-06-17")); err != nil {
		t.Fatalf("a three-month correction was refused: %v", err)
	}
	if got := itStoredDOB(t, st, id); got != "1990-06-17" {
		t.Fatalf("stored DOB = %q, want 1990-06-17", got)
	}

	// Anchored to the registration value, not the current profile value: a
	// second step of the same size would creep past a year, so it is refused.
	if got := fieldCode(t, itUpdateDOB(svc, id, itDate("1991-06-17"))); got != CodeDOBMismatchRegistration {
		t.Fatalf("creeping from the edited value: got %q, want %s", got, CodeDOBMismatchRegistration)
	}
}

func TestIntegration_LaterReacceptanceDoesNotMoveTheAnchor(t *testing.T) {
	pool := serviceITPool(t)
	svc, st := itService(pool)
	t0 := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	id := seedITAccount(t, pool, itDate("1990-03-17"),
		itConsent{dob: itDate("1990-03-17"), at: t0},
		itConsent{dob: itDate("2000-01-01"), at: t0.Add(time.Hour)},
	)
	if got := fieldCode(t, itUpdateDOB(svc, id, itDate("1999-12-01"))); got != CodeDOBMismatchRegistration {
		t.Fatalf("got %q, want %s: the earliest declaration is the anchor", got, CodeDOBMismatchRegistration)
	}
	if got := itStoredDOB(t, st, id); got != "1990-03-17" {
		t.Fatalf("stored DOB = %q, want 1990-03-17", got)
	}
}

// OAuth sign-ups record no registration DOB. They get the format, range and
// age rules only.
func TestIntegration_AccountWithoutRegistrationDOBGetsTheAgeRulesOnly(t *testing.T) {
	pool := serviceITPool(t)
	svc, st := itService(pool)
	id := seedITAccount(t, pool, nil)

	if got := fieldCode(t, itUpdateDOB(svc, id, itDate("2010-01-01"))); got != CodeDOBUnderMinimumAge {
		t.Fatalf("under 18: got %q, want %s", got, CodeDOBUnderMinimumAge)
	}
	if err := itUpdateDOB(svc, id, itDate("1970-01-01")); err != nil {
		t.Fatalf("first DOB on an account without a registration record was refused: %v", err)
	}
	if got := itStoredDOB(t, st, id); got != "1970-01-01" {
		t.Fatalf("stored DOB = %q, want 1970-01-01", got)
	}
}

func TestIntegration_AbsentDOBIsKept(t *testing.T) {
	pool := serviceITPool(t)
	svc, st := itService(pool)
	id := seedITAccount(t, pool, itDate("1990-03-17"))
	if err := itUpdateDOB(svc, id, nil); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if got := itStoredDOB(t, st, id); got != "1990-03-17" {
		t.Fatalf("stored DOB = %q after a write without dob, want 1990-03-17", got)
	}
}
