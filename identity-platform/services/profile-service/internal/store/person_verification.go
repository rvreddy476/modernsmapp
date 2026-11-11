package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/identity-shared/verification"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Person verification storage (internal/personverify). No value read or
// written here is a name, a date of birth, a photo in the clear or an
// Aadhaar number: see database/setup.sql for what each column holds.

// Attempt statuses.
const (
	PersonAttemptStarted         = "started"
	PersonAttemptIdentityChecked = "identity_checked"
	PersonAttemptApproved        = "approved"
	PersonAttemptRefused         = "refused"
)

var (
	// ErrDuplicatePerson: another account is already verified as this person.
	ErrDuplicatePerson = errors.New("person already verified on another account")
	// ErrIdentityMismatch: this account is bound to a different person.
	ErrIdentityMismatch = errors.New("account is verified as a different person")
	// ErrNameChanged: the display name no longer matches the DigiLocker name.
	ErrNameChanged = errors.New("display name no longer matches")
	// ErrAttemptGone: the attempt is no longer waiting for its selfie.
	ErrAttemptGone = errors.New("verification attempt is no longer pending")
)

// PersonAttempt is one verification attempt.
type PersonAttempt struct {
	ID                   uuid.UUID
	UserID               uuid.UUID
	StateHash            string
	CodeVerifierSealed   []byte
	Status               string
	RefusalCode          string
	IdentityHash         string
	NameHash             string
	ReferencePhotoSealed []byte
	ExpiresAt            time.Time
	CreatedAt            time.Time
}

// String keeps hashes and sealed values out of logs.
func (a PersonAttempt) String() string {
	return "PersonAttempt{" + a.ID.String() + ", " + a.Status + ", redacted}"
}

// PersonBinding ties one person (identity hash) to one account.
type PersonBinding struct {
	IdentityHash string
	UserID       uuid.UUID
	NameHash     string
}

func (b PersonBinding) String() string { return "PersonBinding{" + b.UserID.String() + ", redacted}" }

// ApprovePersonInput is the selfie step's approval.
type ApprovePersonInput struct {
	AttemptID    uuid.UUID
	UserID       uuid.UUID
	IdentityHash string
	NameHash     string
	// HashName is the keyed name hash, applied to the display name read
	// under the row lock.
	HashName   func(displayName string) string
	Similarity float64
}

// CountPersonAttemptsSince counts the user's attempts created since t.
func (s *Store) CountPersonAttemptsSince(ctx context.Context, userID uuid.UUID, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM profile.person_verification_attempts WHERE user_id = $1 AND created_at >= $2`,
		userID, since).Scan(&n)
	return n, err
}

// CreatePersonAttempt records a started attempt.
func (s *Store) CreatePersonAttempt(ctx context.Context, a PersonAttempt) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO profile.person_verification_attempts
			(id, user_id, state_hash, code_verifier_sealed, status, expires_at, created_at)
		VALUES ($1, $2, $3, $4, 'started', $5, $6)`,
		a.ID, a.UserID, a.StateHash, a.CodeVerifierSealed, a.ExpiresAt, a.CreatedAt)
	return err
}

// ConsumePersonAttemptState marks the state used and returns its attempt,
// or nil when there is no unused, unexpired, started attempt with that state
// for this user. One statement, so a replayed state can never be consumed
// twice and a state started by another user is never found.
func (s *Store) ConsumePersonAttemptState(ctx context.Context, userID uuid.UUID, stateHash string, now time.Time) (*PersonAttempt, error) {
	var a PersonAttempt
	err := s.db.QueryRow(ctx, `
		UPDATE profile.person_verification_attempts
		   SET state_used_at = $3
		 WHERE state_hash = $1 AND user_id = $2 AND status = 'started'
		   AND state_used_at IS NULL AND expires_at > $3
		RETURNING id, user_id, code_verifier_sealed, status, expires_at, created_at`,
		stateHash, userID, now).Scan(&a.ID, &a.UserID, &a.CodeVerifierSealed, &a.Status, &a.ExpiresAt, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// MarkPersonIdentityChecked moves a consumed attempt to the selfie step.
func (s *Store) MarkPersonIdentityChecked(ctx context.Context, id uuid.UUID, identityHash, nameHash string, photoSealed []byte, expiresAt time.Time) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE profile.person_verification_attempts
		   SET status = 'identity_checked', identity_hash = $2, name_hash = $3,
		       reference_photo_sealed = $4, code_verifier_sealed = NULL, expires_at = $5
		 WHERE id = $1 AND status = 'started'`,
		id, identityHash, nameHash, photoSealed, expiresAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAttemptGone
	}
	return nil
}

// RefusePersonAttempt ends an attempt and wipes everything sealed on it.
func (s *Store) RefusePersonAttempt(ctx context.Context, id uuid.UUID, code string, now time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE profile.person_verification_attempts
		   SET status = 'refused', refusal_code = $2, completed_at = $3,
		       code_verifier_sealed = NULL, reference_photo_sealed = NULL
		 WHERE id = $1 AND status IN ('started', 'identity_checked')`,
		id, code, now)
	return err
}

// PendingSelfieAttempt is the user's newest attempt waiting for a selfie.
func (s *Store) PendingSelfieAttempt(ctx context.Context, userID uuid.UUID, now time.Time) (*PersonAttempt, error) {
	var a PersonAttempt
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, status, COALESCE(identity_hash, ''), COALESCE(name_hash, ''),
		       reference_photo_sealed, expires_at, created_at
		  FROM profile.person_verification_attempts
		 WHERE user_id = $1 AND status = 'identity_checked' AND expires_at > $2
		 ORDER BY created_at DESC LIMIT 1`,
		userID, now).Scan(&a.ID, &a.UserID, &a.Status, &a.IdentityHash, &a.NameHash, &a.ReferencePhotoSealed, &a.ExpiresAt, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// LatestPersonAttempt is the user's newest attempt (status read only).
func (s *Store) LatestPersonAttempt(ctx context.Context, userID uuid.UUID) (*PersonAttempt, error) {
	var a PersonAttempt
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, status, refusal_code, expires_at, created_at
		  FROM profile.person_verification_attempts
		 WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1`,
		userID).Scan(&a.ID, &a.UserID, &a.Status, &a.RefusalCode, &a.ExpiresAt, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// PersonBindingByIdentity returns the account bound to identityHash, if any.
func (s *Store) PersonBindingByIdentity(ctx context.Context, identityHash string) (*PersonBinding, error) {
	return s.personBinding(ctx, `WHERE identity_hash = $1`, identityHash)
}

// PersonBindingByUser returns the person bound to userID, if any.
func (s *Store) PersonBindingByUser(ctx context.Context, userID uuid.UUID) (*PersonBinding, error) {
	return s.personBinding(ctx, `WHERE user_id = $1`, userID)
}

func (s *Store) personBinding(ctx context.Context, where string, arg any) (*PersonBinding, error) {
	var b PersonBinding
	err := s.db.QueryRow(ctx, `SELECT identity_hash, user_id, name_hash FROM profile.verified_persons `+where, arg).
		Scan(&b.IdentityHash, &b.UserID, &b.NameHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ApprovePerson sets the tick in one transaction: the display name is
// re-checked under the profile row lock, the person is bound to the account
// (refusing a second account or a different person), the attempt is closed
// with its photo wiped, and profile.profiles and usr.users are both set.
func (s *Store) ApprovePerson(ctx context.Context, in ApprovePersonInput) (*Profile, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("approve person: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var displayName string
	if err := tx.QueryRow(ctx, `SELECT display_name FROM profile.profiles WHERE user_id = $1 FOR UPDATE`, in.UserID).Scan(&displayName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAttemptGone
		}
		return nil, fmt.Errorf("approve person: lock profile: %w", err)
	}
	if in.HashName == nil || in.HashName(displayName) != in.NameHash {
		return nil, ErrNameChanged
	}

	rows, err := tx.Query(ctx, `SELECT identity_hash, user_id FROM profile.verified_persons
		WHERE identity_hash = $1 OR user_id = $2 FOR UPDATE`, in.IdentityHash, in.UserID)
	if err != nil {
		return nil, fmt.Errorf("approve person: read bindings: %w", err)
	}
	var conflict error
	for rows.Next() {
		var ih string
		var uid uuid.UUID
		if err := rows.Scan(&ih, &uid); err != nil {
			rows.Close()
			return nil, fmt.Errorf("approve person: scan binding: %w", err)
		}
		switch {
		case ih == in.IdentityHash && uid != in.UserID:
			conflict = ErrDuplicatePerson
		case uid == in.UserID && ih != in.IdentityHash && conflict == nil:
			conflict = ErrIdentityMismatch
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("approve person: read bindings: %w", err)
	}
	if conflict != nil {
		return nil, conflict
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO profile.verified_persons (identity_hash, user_id, name_hash, verified_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (user_id) DO UPDATE SET name_hash = EXCLUDED.name_hash, verified_at = NOW()
		 WHERE profile.verified_persons.identity_hash = EXCLUDED.identity_hash`,
		in.IdentityHash, in.UserID, in.NameHash); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// A concurrent approval bound this person to another account.
			return nil, ErrDuplicatePerson
		}
		return nil, fmt.Errorf("approve person: bind: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		UPDATE profile.person_verification_attempts
		   SET status = 'approved', similarity = $2, completed_at = NOW(),
		       reference_photo_sealed = NULL, code_verifier_sealed = NULL
		 WHERE id = $1 AND user_id = $3 AND status = 'identity_checked'`,
		in.AttemptID, in.Similarity, in.UserID)
	if err != nil {
		return nil, fmt.Errorf("approve person: close attempt: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrAttemptGone
	}

	p, err := scanProfile(tx.QueryRow(ctx, `
		UPDATE profile.profiles
		   SET is_verified = TRUE, verification_level = $2, badge_flags = badge_flags | $3, updated_at = NOW()
		 WHERE user_id = $1
		RETURNING `+allProfileCols,
		in.UserID, verification.LevelPerson, verification.BadgePersonVerified))
	if err != nil {
		return nil, fmt.Errorf("approve person: set profile: %w", err)
	}
	if p == nil {
		return nil, ErrAttemptGone
	}
	if _, err := tx.Exec(ctx, `UPDATE usr.users SET is_verified = TRUE, updated_at = NOW() WHERE id = $1`, in.UserID); err != nil {
		return nil, fmt.Errorf("approve person: set usr.users: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("approve person: commit: %w", err)
	}
	return p, nil
}

// SyncUserVerified mirrors profile.profiles.is_verified onto usr.users after
// a tick was cleared. Reports whether the row changed.
func (s *Store) SyncUserVerified(ctx context.Context, userID uuid.UUID, verified bool) (bool, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE usr.users SET is_verified = $2, updated_at = NOW() WHERE id = $1 AND is_verified IS DISTINCT FROM $2`,
		userID, verified)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}
