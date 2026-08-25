//go:build integration

package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// Module 3 M3-P0-3 / LB-5 — pending activation, consent, atomic recovery,
// against live PostgreSQL.
//
//	POSTGRES_DSN=postgres://... go test -tags integration ./internal/store/ -v
//
// These are TRANSACTION semantics. A fake store cannot prove any of them: the
// whole point is what survives a failure part-way through, and a fake has no
// notion of a partial commit.

func authPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	// The suite owns its schema so it can run against an empty database.
	if _, err := pool.Exec(context.Background(), authTestSchema); err != nil {
		t.Fatalf("install schema: %v", err)
	}
	return pool
}

// authTestSchema is the subset LB-5 touches. It mirrors database/setup.sql;
// the columns and constraints that matter here are account_status, the consent
// flags, the consent record table, otp_codes and sessions.
const authTestSchema = `
CREATE SCHEMA IF NOT EXISTS auth;
CREATE SCHEMA IF NOT EXISTS usr;

CREATE TABLE IF NOT EXISTS auth.users (
    user_id UUID PRIMARY KEY,
    phone TEXT UNIQUE,
    email TEXT UNIQUE,
    password_hash TEXT,
    email_verified BOOLEAN NOT NULL DEFAULT FALSE,
    phone_verified BOOLEAN NOT NULL DEFAULT FALSE,
    account_status TEXT NOT NULL DEFAULT 'active',
    consent_terms BOOLEAN NOT NULL DEFAULT FALSE,
    consent_privacy BOOLEAN NOT NULL DEFAULT FALSE,
    consent_age BOOLEAN NOT NULL DEFAULT FALSE,
    deletion_requested_at TIMESTAMPTZ,
    scheduled_purge_date TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS auth.registration_consents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES auth.users(user_id) ON DELETE CASCADE,
    terms_version TEXT NOT NULL,
    accepted_terms BOOLEAN NOT NULL DEFAULT FALSE,
    accepted_privacy BOOLEAN NOT NULL DEFAULT FALSE,
    declared_dob DATE,
    accepted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip TEXT,
    user_agent TEXT
);

CREATE TABLE IF NOT EXISTS auth.otp_codes (
    id UUID PRIMARY KEY,
    phone TEXT NOT NULL,
    otp_hash TEXT NOT NULL,
    purpose TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS auth.sessions (
    session_id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    refresh_token_hash TEXT NOT NULL,
    device_id TEXT, platform TEXT, ip TEXT, user_agent TEXT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '30 days',
    revoked_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS usr.users (
    id UUID PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'active',
    is_verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

func seedUser(t *testing.T, pool *pgxpool.Pool, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO auth.users (user_id, email, password_hash) VALUES ($1,$2,'old-hash')`,
		id, email); err != nil {
		t.Fatalf("seed auth user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO usr.users (id) VALUES ($1)`, id); err != nil {
		t.Fatalf("seed usr user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM auth.sessions WHERE user_id=$1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM auth.otp_codes WHERE phone=$1`, email)
		_, _ = pool.Exec(context.Background(), `DELETE FROM auth.users WHERE user_id=$1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM usr.users WHERE id=$1`, id)
	})
	return id
}

func accountStatus(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT account_status FROM auth.users WHERE user_id=$1`, id).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	return status
}

// ── Pending activation ──────────────────────────────────────────────────────

func TestPendingAccountActivatesExactlyOnce(t *testing.T) {
	pool := authPool(t)
	s := New(pool)
	ctx := context.Background()
	id := seedUser(t, pool, "pending-"+uuid.NewString()+"@example.com")

	// Put the account in the state registration now creates.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := s.SetAccountPendingTx(ctx, tx, id); err != nil {
		t.Fatalf("set pending: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got := accountStatus(t, pool, id); got != AccountStatusPendingVerification {
		t.Fatalf("status=%q, want %q", got, AccountStatusPendingVerification)
	}

	pending, err := s.IsAccountPendingVerification(ctx, id)
	if err != nil || !pending {
		t.Fatalf("IsAccountPendingVerification = %v, %v; login must be refused", pending, err)
	}

	// First activation succeeds.
	if err := s.ActivateVerifiedAccount(ctx, id); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if got := accountStatus(t, pool, id); got != AccountStatusActive {
		t.Fatalf("status=%q after activation, want %q", got, AccountStatusActive)
	}

	// The app-side projection must be active too, or the account can log in
	// and is invisible to every other service.
	var projStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM usr.users WHERE id=$1`, id).Scan(&projStatus); err != nil {
		t.Fatalf("read projection: %v", err)
	}
	if projStatus != "active" {
		t.Errorf("usr.users.status=%q, want active", projStatus)
	}

	// A REPLAYED verification must change nothing.
	err = s.ActivateVerifiedAccount(ctx, id)
	if !errors.Is(err, ErrAccountNotPending) {
		t.Fatalf("replayed activation returned %v, want ErrAccountNotPending — "+
			"activation must be one-time", err)
	}
}

// ── Consent ─────────────────────────────────────────────────────────────────

func TestConsentIsPersistedWithItsVersion(t *testing.T) {
	pool := authPool(t)
	s := New(pool)
	ctx := context.Background()
	id := seedUser(t, pool, "consent-"+uuid.NewString()+"@example.com")

	dob := time.Date(1990, 3, 17, 0, 0, 0, 0, time.UTC)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := s.RecordConsentTx(ctx, tx, id, RegistrationConsent{
		TermsVersion:    "2026-08-01",
		AcceptedTerms:   true,
		AcceptedPrivacy: true,
		DeclaredDOB:     &dob,
		IP:              "203.0.113.4",
		UserAgent:       "atPost/1.0",
	}); err != nil {
		t.Fatalf("record consent: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var version, ip string
	var acceptedTerms, acceptedPrivacy bool
	var declared time.Time
	if err := pool.QueryRow(ctx, `
		SELECT terms_version, accepted_terms, accepted_privacy, declared_dob, ip
		FROM auth.registration_consents WHERE user_id=$1`, id).
		Scan(&version, &acceptedTerms, &acceptedPrivacy, &declared, &ip); err != nil {
		t.Fatalf("no consent record was written — an audit cannot answer what this "+
			"user agreed to: %v", err)
	}
	if version != "2026-08-01" {
		t.Errorf("terms_version=%q: a boolean alone cannot say WHICH text was agreed to", version)
	}
	if !acceptedTerms || !acceptedPrivacy {
		t.Errorf("acceptance flags not recorded: terms=%v privacy=%v", acceptedTerms, acceptedPrivacy)
	}
	if declared.Year() != 1990 {
		t.Errorf("declared_dob=%v, want 1990-03-17", declared)
	}

	// The boolean columns existing code reads must be set too.
	var cTerms, cPrivacy, cAge bool
	if err := pool.QueryRow(ctx,
		`SELECT consent_terms, consent_privacy, consent_age FROM auth.users WHERE user_id=$1`,
		id).Scan(&cTerms, &cPrivacy, &cAge); err != nil {
		t.Fatalf("read flags: %v", err)
	}
	if !cTerms || !cPrivacy || !cAge {
		t.Errorf("auth.users consent flags not set: terms=%v privacy=%v age=%v",
			cTerms, cPrivacy, cAge)
	}
}

// The consent record must roll back with the registration. A consent for an
// account that does not exist is worse than none.
func TestConsentRollsBackWithTheRegistrationTransaction(t *testing.T) {
	pool := authPool(t)
	s := New(pool)
	ctx := context.Background()
	id := seedUser(t, pool, "rollback-"+uuid.NewString()+"@example.com")

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := s.RecordConsentTx(ctx, tx, id, RegistrationConsent{
		TermsVersion: "2026-08-01", AcceptedTerms: true, AcceptedPrivacy: true,
	}); err != nil {
		t.Fatalf("record consent: %v", err)
	}
	// Registration fails after the consent write.
	_ = tx.Rollback(ctx)

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM auth.registration_consents WHERE user_id=$1`, id).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("%d consent row(s) survived a rolled-back registration", n)
	}
}

// ── Atomic recovery ─────────────────────────────────────────────────────────

func seedResetCode(t *testing.T, pool *pgxpool.Pool, key, code string, ttl time.Duration) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash code: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO auth.otp_codes (id, phone, otp_hash, purpose, expires_at)
		VALUES ($1,$2,$3,'password_reset',$4)`,
		uuid.New(), key, string(hash), time.Now().Add(ttl)); err != nil {
		t.Fatalf("seed code: %v", err)
	}
}

func verifier(code string) func(string) bool {
	return func(stored string) bool {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(code)) == nil
	}
}

func activeSessions(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM auth.sessions WHERE user_id=$1 AND is_active`, id).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func TestRecoveryConsumesCodeSetsPasswordAndRevokesSessionsAtomically(t *testing.T) {
	pool := authPool(t)
	s := New(pool)
	ctx := context.Background()
	email := "reset-" + uuid.NewString() + "@example.com"
	id := seedUser(t, pool, email)

	// An attacker's live session.
	if _, err := pool.Exec(ctx, `
		INSERT INTO auth.sessions (session_id, user_id, refresh_token_hash) VALUES ($1,$2,'attacker')`,
		uuid.New(), id); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if activeSessions(t, pool, id) != 1 {
		t.Fatal("precondition: one active session")
	}

	seedResetCode(t, pool, email, "123456", 5*time.Minute)

	if err := s.ConsumeRecoveryAndSetPassword(
		ctx, id, email, "password_reset", verifier("123456"), "new-hash"); err != nil {
		t.Fatalf("recovery: %v", err)
	}

	var hash string
	if err := pool.QueryRow(ctx, `SELECT password_hash FROM auth.users WHERE user_id=$1`, id).Scan(&hash); err != nil {
		t.Fatalf("read password: %v", err)
	}
	if hash != "new-hash" {
		t.Errorf("password not updated: %q", hash)
	}
	if n := activeSessions(t, pool, id); n != 0 {
		t.Errorf("%d session(s) still active after a password reset — the attacker's "+
			"session survived the reset that was supposed to end it", n)
	}
	var codes int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM auth.otp_codes WHERE phone=$1 AND purpose='password_reset'`,
		email).Scan(&codes); err != nil {
		t.Fatalf("count codes: %v", err)
	}
	if codes != 0 {
		t.Errorf("the one-time code was not consumed (%d remain): it can be replayed", codes)
	}
}

// A WRONG code must leave everything untouched — including the code, so the
// user can retry with the real one.
func TestWrongCodeChangesNothingAndLeavesTheCodeUsable(t *testing.T) {
	pool := authPool(t)
	s := New(pool)
	ctx := context.Background()
	email := "wrong-" + uuid.NewString() + "@example.com"
	id := seedUser(t, pool, email)

	if _, err := pool.Exec(ctx, `
		INSERT INTO auth.sessions (session_id, user_id, refresh_token_hash) VALUES ($1,$2,'live')`,
		uuid.New(), id); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	seedResetCode(t, pool, email, "123456", 5*time.Minute)

	err := s.ConsumeRecoveryAndSetPassword(
		ctx, id, email, "password_reset", verifier("000000"), "new-hash")
	if !errors.Is(err, ErrRecoveryCodeInvalid) {
		t.Fatalf("got %v, want ErrRecoveryCodeInvalid", err)
	}

	var hash string
	_ = pool.QueryRow(ctx, `SELECT password_hash FROM auth.users WHERE user_id=$1`, id).Scan(&hash)
	if hash != "old-hash" {
		t.Errorf("password changed on a WRONG code: %q", hash)
	}
	if activeSessions(t, pool, id) != 1 {
		t.Error("sessions were revoked on a wrong code")
	}
	var codes int
	_ = pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM auth.otp_codes WHERE phone=$1`, email).Scan(&codes)
	if codes != 1 {
		t.Errorf("the code was consumed by a WRONG attempt (%d remain): the user "+
			"can no longer retry with the real one", codes)
	}
}

func TestExpiredCodeIsRefused(t *testing.T) {
	pool := authPool(t)
	s := New(pool)
	ctx := context.Background()
	email := "expired-" + uuid.NewString() + "@example.com"
	id := seedUser(t, pool, email)

	seedResetCode(t, pool, email, "123456", -1*time.Minute) // already expired

	err := s.ConsumeRecoveryAndSetPassword(
		ctx, id, email, "password_reset", verifier("123456"), "new-hash")
	if !errors.Is(err, ErrRecoveryCodeInvalid) {
		t.Fatalf("an EXPIRED code was accepted: %v", err)
	}
	var hash string
	_ = pool.QueryRow(ctx, `SELECT password_hash FROM auth.users WHERE user_id=$1`, id).Scan(&hash)
	if hash != "old-hash" {
		t.Error("password changed using an expired code")
	}
}

// The code is single-use: a replay after a successful reset must fail.
func TestRecoveryCodeCannotBeReplayed(t *testing.T) {
	pool := authPool(t)
	s := New(pool)
	ctx := context.Background()
	email := "replay-" + uuid.NewString() + "@example.com"
	id := seedUser(t, pool, email)
	seedResetCode(t, pool, email, "123456", 5*time.Minute)

	if err := s.ConsumeRecoveryAndSetPassword(
		ctx, id, email, "password_reset", verifier("123456"), "first-hash"); err != nil {
		t.Fatalf("first reset: %v", err)
	}
	err := s.ConsumeRecoveryAndSetPassword(
		ctx, id, email, "password_reset", verifier("123456"), "second-hash")
	if !errors.Is(err, ErrRecoveryCodeInvalid) {
		t.Fatalf("the code was replayable: %v", err)
	}
	var hash string
	_ = pool.QueryRow(ctx, `SELECT password_hash FROM auth.users WHERE user_id=$1`, id).Scan(&hash)
	if hash != "first-hash" {
		t.Errorf("a replayed code changed the password again: %q", hash)
	}
}

// THE KEY MISMATCH. ForgotPassword stores the code under the EMAIL. Recovery
// must look it up there — the old code checked the phone first, so any account
// with BOTH could never find its own emailed code.
func TestRecoveryFindsTheCodeForAnAccountWithBothPhoneAndEmail(t *testing.T) {
	pool := authPool(t)
	s := New(pool)
	ctx := context.Background()

	email := "both-" + uuid.NewString() + "@example.com"
	phone := "+9199" + uuid.NewString()[:8]
	id := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO auth.users (user_id, phone, email, password_hash) VALUES ($1,$2,$3,'old-hash')`,
		id, phone, email); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM auth.otp_codes WHERE phone IN ($1,$2)`, email, phone)
		_, _ = pool.Exec(context.Background(), `DELETE FROM auth.users WHERE user_id=$1`, id)
	})

	// The code lives under the EMAIL, as ForgotPassword writes it.
	seedResetCode(t, pool, email, "123456", 5*time.Minute)

	if err := s.ConsumeRecoveryAndSetPassword(
		ctx, id, email, "password_reset", verifier("123456"), "new-hash"); err != nil {
		t.Fatalf("recovery failed for an account with BOTH a phone and an email: %v\n"+
			"This is the population most likely to have a complete profile.", err)
	}
}
