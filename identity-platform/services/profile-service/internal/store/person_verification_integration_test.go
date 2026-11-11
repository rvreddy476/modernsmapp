//go:build integration

package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/identity-shared/verification"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Person verification SQL against live PostgreSQL (CI only):
//
//	PROFILE_IT_POSTGRES_DSN=postgres://.../identity_profile_it_test \
//	  go test -tags integration -p 1 ./internal/store/ -run PersonVerification -v
//
// The two tables match database/setup.sql; usr.users matches
// auth-service/database/setup.sql.
const personVerificationTestSchema = `
CREATE SCHEMA IF NOT EXISTS usr;
CREATE TABLE IF NOT EXISTS usr.users (
    id UUID PRIMARY KEY REFERENCES auth.users(user_id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'active',
    is_verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS profile.person_verification_attempts (
    id                     UUID PRIMARY KEY,
    user_id                UUID NOT NULL REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    state_hash             TEXT NOT NULL UNIQUE,
    code_verifier_sealed   BYTEA,
    status                 TEXT NOT NULL DEFAULT 'started'
        CHECK (status IN ('started', 'identity_checked', 'approved', 'refused')),
    refusal_code           TEXT NOT NULL DEFAULT '',
    identity_hash          TEXT,
    name_hash              TEXT,
    reference_photo_sealed BYTEA,
    similarity             NUMERIC(5, 2),
    state_used_at          TIMESTAMPTZ,
    expires_at             TIMESTAMPTZ NOT NULL,
    completed_at           TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS profile.verified_persons (
    identity_hash TEXT PRIMARY KEY,
    user_id       UUID NOT NULL UNIQUE REFERENCES profile.profiles(user_id) ON DELETE CASCADE,
    name_hash     TEXT NOT NULL,
    verified_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

func personPool(t *testing.T) (*pgxpool.Pool, *Store) {
	t.Helper()
	pool := profileWritePool(t)
	if _, err := pool.Exec(context.Background(), personVerificationTestSchema); err != nil {
		t.Fatalf("install person verification schema: %v", err)
	}
	return pool, New(pool)
}

func seedPerson(t *testing.T, pool *pgxpool.Pool, displayName string) uuid.UUID {
	t.Helper()
	id := seedIdentityUser(t, pool, seedIdentity{firstName: "IT"})
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE profile.profiles SET display_name = $2 WHERE user_id = $1`, id, displayName); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO usr.users (id) VALUES ($1)`, id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM profile.person_verification_attempts WHERE user_id = $1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM profile.verified_persons WHERE user_id = $1`, id)
	})
	return id
}

// checkedAttempt creates an attempt at the selfie step.
func checkedAttempt(t *testing.T, s *Store, user uuid.UUID, identityHash string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	a := PersonAttempt{ID: uuid.New(), UserID: user, StateHash: uuid.NewString(), CodeVerifierSealed: []byte("sealed"), ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := s.CreatePersonAttempt(ctx, a); err != nil {
		t.Fatal(err)
	}
	got, err := s.ConsumePersonAttemptState(ctx, user, a.StateHash, now)
	if err != nil || got == nil {
		t.Fatalf("consume: %v %v", got, err)
	}
	if again, _ := s.ConsumePersonAttemptState(ctx, user, a.StateHash, now); again != nil {
		t.Fatal("a state must be consumable once")
	}
	if err := s.MarkPersonIdentityChecked(ctx, a.ID, identityHash, "name:"+identityHash, []byte("photo"), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func hashName(string) string { return "" }

func TestPersonVerification_ApproveSetsBothTablesAndRefusesASecondAccount(t *testing.T) {
	pool, s := personPool(t)
	ctx := context.Background()
	a := seedPerson(t, pool, "Asha Rao")
	b := seedPerson(t, pool, "Asha Rao")
	ih := "ih-" + uuid.NewString()
	nameOK := func(string) string { return "name:" + ih }

	attA := checkedAttempt(t, s, a, ih)
	p, err := s.ApprovePerson(ctx, ApprovePersonInput{AttemptID: attA, UserID: a, IdentityHash: ih, NameHash: "name:" + ih, HashName: nameOK, Similarity: 97})
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsVerified || p.VerificationLevel != verification.LevelPerson || p.BadgeFlags&verification.BadgePersonVerified == 0 {
		t.Fatalf("profile = %+v", p)
	}
	var usrVerified bool
	var photo []byte
	_ = pool.QueryRow(ctx, `SELECT is_verified FROM usr.users WHERE id = $1`, a).Scan(&usrVerified)
	_ = pool.QueryRow(ctx, `SELECT reference_photo_sealed FROM profile.person_verification_attempts WHERE id = $1`, attA).Scan(&photo)
	if !usrVerified || photo != nil {
		t.Fatal("usr.users must be verified and the photo wiped")
	}

	attB := checkedAttempt(t, s, b, ih)
	if _, err := s.ApprovePerson(ctx, ApprovePersonInput{AttemptID: attB, UserID: b, IdentityHash: ih, NameHash: "name:" + ih, HashName: nameOK}); !errors.Is(err, ErrDuplicatePerson) {
		t.Fatalf("second account = %v, want ErrDuplicatePerson", err)
	}
	attC := checkedAttempt(t, s, b, "other-"+ih)
	if _, err := s.ApprovePerson(ctx, ApprovePersonInput{AttemptID: attC, UserID: b, IdentityHash: "other-" + ih, NameHash: "x", HashName: hashName}); !errors.Is(err, ErrNameChanged) {
		t.Fatalf("name changed = %v, want ErrNameChanged", err)
	}
}

func TestPersonVerification_RenameClearsTheTickOnlyWhenAsked(t *testing.T) {
	pool, s := personPool(t)
	ctx := context.Background()
	a := seedPerson(t, pool, "Asha Rao")
	ih := "ih-" + uuid.NewString()
	att := checkedAttempt(t, s, a, ih)
	if _, err := s.ApprovePerson(ctx, ApprovePersonInput{AttemptID: att, UserID: a, IdentityHash: ih, NameHash: "n", HashName: func(string) string { return "n" }}); err != nil {
		t.Fatal(err)
	}
	keep := "RAO Asha"
	p, err := s.UpdateProfile(ctx, a, UpdateProfileParams{DisplayName: &keep, ClearPersonVerificationOnRename: false})
	if err != nil || !p.IsVerified {
		t.Fatalf("kept rename = %+v %v", p, err)
	}
	// Same name sent back with the flag set: not a rename, tick stays.
	p, err = s.UpdateProfile(ctx, a, UpdateProfileParams{DisplayName: &keep, ClearPersonVerificationOnRename: true})
	if err != nil || !p.IsVerified {
		t.Fatalf("unchanged name = %+v %v", p, err)
	}
	other := "Someone Else"
	p, err = s.UpdateProfile(ctx, a, UpdateProfileParams{DisplayName: &other, ClearPersonVerificationOnRename: true})
	if err != nil || p.IsVerified || p.VerificationLevel != "" || p.BadgeFlags&verification.BadgePersonVerified != 0 || p.DisplayName != other {
		t.Fatalf("clearing rename = %+v %v", p, err)
	}
	changed, err := s.SyncUserVerified(ctx, a, false)
	if err != nil || !changed {
		t.Fatalf("sync = %v %v", changed, err)
	}
	if b, _ := s.PersonBindingByUser(ctx, a); b == nil {
		t.Fatal("the binding must survive a cleared tick")
	}
}
