//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Profile identity-field writes against live PostgreSQL.
//
//	PROFILE_IT_POSTGRES_DSN=postgres://.../identity_profile_it_test \
//	  go test -tags integration -p 1 ./internal/store/ -run ProfileWrite -v

// profileWriteColumns adds what UpdateProfile's SET and RETURNING touch on top
// of identityBasicsTestSchema's minimal profile.profiles. Definitions match
// auth-service/database/setup.sql, which owns the table.
const profileWriteColumns = `
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

func profileWritePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := identityPool(t)
	if _, err := pool.Exec(context.Background(), profileWriteColumns); err != nil {
		t.Fatalf("install profile write columns: %v", err)
	}
	return pool
}

func dateString(d *time.Time) string {
	if d == nil {
		return ""
	}
	return d.Format("2006-01-02")
}

func TestProfileWrite_RegistrationDOBIsTheEarliestDeclaration(t *testing.T) {
	pool := profileWritePool(t)
	t0 := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	id := seedIdentityUser(t, pool, seedIdentity{
		firstName:  "Asha",
		profileDOB: day("2001-01-01"),
		consents: []seedConsent{
			{dob: nil, at: t0},
			{dob: day("1990-03-17"), at: t0.Add(time.Minute)},
			{dob: day("1985-05-05"), at: t0.Add(time.Hour)},
		},
	})
	got, err := New(pool).GetRegistrationDOB(context.Background(), id)
	if err != nil {
		t.Fatalf("GetRegistrationDOB: %v", err)
	}
	if dateString(got) != "1990-03-17" {
		t.Fatalf("registration DOB = %q, want the earliest declaration 1990-03-17", dateString(got))
	}
}

func TestProfileWrite_NoRegistrationDOBIsNil(t *testing.T) {
	pool := profileWritePool(t)
	id := seedIdentityUser(t, pool, seedIdentity{
		firstName:  "Ravi",
		profileDOB: day("1995-02-02"),
		consents:   []seedConsent{{dob: nil, at: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)}},
	})
	got, err := New(pool).GetRegistrationDOB(context.Background(), id)
	if err != nil {
		t.Fatalf("GetRegistrationDOB: %v", err)
	}
	if got != nil {
		t.Fatalf("got %q, want nil for an account with no declared DOB", dateString(got))
	}
	if got, err := New(pool).GetRegistrationDOB(context.Background(), uuid.New()); err != nil || got != nil {
		t.Fatalf("unknown user: got (%v, %v), want (nil, nil)", got, err)
	}
}

// Before the COALESCE, both of these wrote NULL over dob and first_name: the
// Android form saved with a blank date omits dob, and a handle change never
// carries either field.
func TestProfileWrite_AbsentDOBAndFirstNameAreKept(t *testing.T) {
	pool := profileWritePool(t)
	s := New(pool)
	for name, params := range map[string]UpdateProfileParams{
		"profile edit without dob or first_name": {DisplayName: "Asha", Bio: "hello", ProfileThemeColor: "#1A73E8"},
		"handle-change shaped params":            {DisplayName: "Asha", Username: strPtr("it_handle_" + uuid.NewString()[:8])},
	} {
		t.Run(name, func(t *testing.T) {
			id := seedIdentityUser(t, pool, seedIdentity{firstName: "Asha", profileDOB: day("1990-03-17")})
			got, err := s.UpdateProfile(context.Background(), id, params)
			if err != nil {
				t.Fatalf("UpdateProfile: %v", err)
			}
			if got == nil {
				t.Fatal("UpdateProfile returned no row")
			}
			if dateString(got.DoB) != "1990-03-17" {
				t.Errorf("dob = %q after a write that did not carry it, want 1990-03-17", dateString(got.DoB))
			}
			if got.FirstName == nil || *got.FirstName != "Asha" {
				t.Errorf("first_name = %v after a write that did not carry it, want Asha", got.FirstName)
			}
		})
	}
}

func TestProfileWrite_SuppliedDOBAndFirstNameAreWritten(t *testing.T) {
	pool := profileWritePool(t)
	id := seedIdentityUser(t, pool, seedIdentity{firstName: "Asha", profileDOB: day("1990-03-17")})
	s := New(pool)
	if _, err := s.UpdateProfile(context.Background(), id, UpdateProfileParams{
		DisplayName: "Asha", FirstName: strPtr("Asha K"), DoB: day("1990-06-17"),
	}); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	got, err := s.GetProfile(context.Background(), id)
	if err != nil || got == nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if dateString(got.DoB) != "1990-06-17" || got.FirstName == nil || *got.FirstName != "Asha K" {
		t.Fatalf("stored (dob=%q, first_name=%v), want (1990-06-17, Asha K)", dateString(got.DoB), got.FirstName)
	}
}

func strPtr(s string) *string { return &s }
