//go:build integration

package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-shared/store/schemaguard"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Internal identity read (dating lane D2), against live PostgreSQL.
//
//	PROFILE_IT_POSTGRES_DSN=postgres://.../identity_profile_it_test \
//	  go test -tags integration ./internal/store/ -run IdentityBasics -v
//
// The suite refuses any database whose name does not end in _test: it writes
// auth.users rows, and identity_db is the live identity database.

const identityBasicsTestSchema = `
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

CREATE TABLE IF NOT EXISTS profile.hidden_profiles (
    user_id UUID PRIMARY KEY,
    reason TEXT NOT NULL,
    hidden_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

func identityPool(t *testing.T) *pgxpool.Pool {
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
	if _, err := pool.Exec(context.Background(), identityBasicsTestSchema); err != nil {
		t.Fatalf("install schema: %v", err)
	}
	return pool
}

type seedConsent struct {
	dob *time.Time
	at  time.Time
}

type seedIdentity struct {
	status     string
	firstName  string
	profileDOB *time.Time
	consents   []seedConsent
	hidden     bool
}

func day(s string) *time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return &d
}

func seedIdentityUser(t *testing.T, pool *pgxpool.Pool, u seedIdentity) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	status := u.status
	if status == "" {
		status = "active"
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO auth.users (user_id, email, phone, account_status) VALUES ($1, $2, $3, $4)`,
		id, "it-"+id.String()+"@example.test", "+910000000000", status); err != nil {
		t.Fatalf("seed auth.users: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM profile.hidden_profiles WHERE user_id = $1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM auth.users WHERE user_id = $1`, id)
	})
	if _, err := pool.Exec(ctx,
		`INSERT INTO profile.profiles (user_id, display_name, first_name, last_name, dob) VALUES ($1, 'IT User', $2, 'Surname', $3)`,
		id, u.firstName, u.profileDOB); err != nil {
		t.Fatalf("seed profile.profiles: %v", err)
	}
	for _, c := range u.consents {
		if _, err := pool.Exec(ctx,
			`INSERT INTO auth.registration_consents (user_id, terms_version, declared_dob, accepted_at) VALUES ($1, '2026-08-01', $2, $3)`,
			id, c.dob, c.at); err != nil {
			t.Fatalf("seed auth.registration_consents: %v", err)
		}
	}
	if u.hidden {
		if _, err := pool.Exec(ctx,
			`INSERT INTO profile.hidden_profiles (user_id, reason) VALUES ($1, 'user.deactivated')`, id); err != nil {
			t.Fatalf("seed profile.hidden_profiles: %v", err)
		}
	}
	return id
}

func assertIdentity(t *testing.T, got *IdentityBasics, id uuid.UUID, firstName, dob, source string) {
	t.Helper()
	if got == nil {
		t.Fatal("got nil, want a row")
	}
	if got.UserID != id {
		t.Errorf("UserID = %s, want %s", got.UserID, id)
	}
	if got.FirstName != firstName {
		t.Errorf("FirstName = %q, want %q", got.FirstName, firstName)
	}
	gotDOB := ""
	if got.DoB != nil {
		gotDOB = got.DoB.Format("2006-01-02")
	}
	if gotDOB != dob {
		t.Errorf("DoB = %q, want %q", gotDOB, dob)
	}
	if got.DoBSource != source {
		t.Errorf("DoBSource = %q, want %q", got.DoBSource, source)
	}
}

func TestIdentityBasics_RegistrationDOBWinsOverEditedProfileDOB(t *testing.T) {
	pool := identityPool(t)
	t0 := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	id := seedIdentityUser(t, pool, seedIdentity{
		firstName:  "Asha",
		profileDOB: day("2001-01-01"), // edited later through PUT /v1/profiles/me
		consents: []seedConsent{
			{dob: day("1990-03-17"), at: t0},
			// A later re-acceptance must not move the registration DOB.
			{dob: day("1985-05-05"), at: t0.Add(time.Hour)},
		},
	})
	got, err := New(pool).GetIdentityBasics(context.Background(), id)
	if err != nil {
		t.Fatalf("GetIdentityBasics: %v", err)
	}
	assertIdentity(t, got, id, "Asha", "1990-03-17", DoBSourceRegistration)
}

func TestIdentityBasics_SkipsConsentRowsWithoutDOB(t *testing.T) {
	pool := identityPool(t)
	t0 := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	id := seedIdentityUser(t, pool, seedIdentity{
		firstName:  "Ravi",
		profileDOB: day("1999-09-09"),
		consents: []seedConsent{
			{dob: nil, at: t0},
			{dob: day("1992-07-01"), at: t0.Add(time.Minute)},
		},
	})
	got, err := New(pool).GetIdentityBasics(context.Background(), id)
	if err != nil {
		t.Fatalf("GetIdentityBasics: %v", err)
	}
	assertIdentity(t, got, id, "Ravi", "1992-07-01", DoBSourceRegistration)
}

func TestIdentityBasics_FallsBackToProfileDOBWhenNoConsentRecord(t *testing.T) {
	pool := identityPool(t)
	id := seedIdentityUser(t, pool, seedIdentity{firstName: "Meera", profileDOB: day("1995-02-02")})
	got, err := New(pool).GetIdentityBasics(context.Background(), id)
	if err != nil {
		t.Fatalf("GetIdentityBasics: %v", err)
	}
	assertIdentity(t, got, id, "Meera", "1995-02-02", DoBSourceProfile)
}

func TestIdentityBasics_NoDOBAnywhereIsNoneNotAnError(t *testing.T) {
	pool := identityPool(t)
	id := seedIdentityUser(t, pool, seedIdentity{firstName: ""})
	got, err := New(pool).GetIdentityBasics(context.Background(), id)
	if err != nil {
		t.Fatalf("GetIdentityBasics: %v", err)
	}
	assertIdentity(t, got, id, "", "", DoBSourceNone)
}

func TestIdentityBasics_UnknownUserIsNil(t *testing.T) {
	pool := identityPool(t)
	got, err := New(pool).GetIdentityBasics(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("GetIdentityBasics: %v", err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil for an unknown user", got)
	}
}

func TestIdentityBasics_AccountLifecycle(t *testing.T) {
	pool := identityPool(t)
	t0 := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	consent := []seedConsent{{dob: day("1990-03-17"), at: t0}}

	for _, status := range []string{"deactivated", "pending_deletion", "purged"} {
		t.Run("withheld/"+status, func(t *testing.T) {
			id := seedIdentityUser(t, pool, seedIdentity{status: status, firstName: "X", consents: consent})
			got, err := New(pool).GetIdentityBasics(context.Background(), id)
			if err != nil {
				t.Fatalf("GetIdentityBasics: %v", err)
			}
			if got != nil {
				t.Fatalf("account_status=%s served %+v, want nil", status, got)
			}
		})
	}
	t.Run("withheld/hidden-marker", func(t *testing.T) {
		id := seedIdentityUser(t, pool, seedIdentity{firstName: "X", consents: consent, hidden: true})
		got, err := New(pool).GetIdentityBasics(context.Background(), id)
		if err != nil {
			t.Fatalf("GetIdentityBasics: %v", err)
		}
		if got != nil {
			t.Fatalf("hidden profile served %+v, want nil", got)
		}
	})
	for _, status := range []string{"active", "suspended", "pending_verification"} {
		t.Run("served/"+status, func(t *testing.T) {
			id := seedIdentityUser(t, pool, seedIdentity{status: status, firstName: "Y", consents: consent})
			got, err := New(pool).GetIdentityBasics(context.Background(), id)
			if err != nil {
				t.Fatalf("GetIdentityBasics: %v", err)
			}
			assertIdentity(t, got, id, "Y", "1990-03-17", DoBSourceRegistration)
		})
	}
}

func TestIdentityBasics_SchemaRequirementsCoverTheQuery(t *testing.T) {
	pool := identityPool(t)
	// Only the tables this read touches; the rest of SchemaRequirements
	// belongs to other queries and is not installed by this suite.
	touched := map[string]bool{
		"auth.users":                 true,
		"auth.registration_consents": true,
		"profile.profiles":           true,
		"profile.hidden_profiles":    true,
	}
	var reqs []schemaguard.Requirement
	for _, r := range SchemaRequirements {
		if touched[r.Table] {
			reqs = append(reqs, r)
			delete(touched, r.Table)
		}
	}
	if len(touched) != 0 {
		t.Fatalf("SchemaRequirements does not declare %v, which GetIdentityBasics reads", touched)
	}
	if err := schemaguard.Verify(context.Background(), pool, "profile-service-it", reqs); err != nil {
		t.Fatalf("schemaguard: %v", err)
	}
}
