// auth.users: the row types the store returns (User, Session, TrustedDevice,
// LoginAnomaly, OTP, OutboxEvent, RecoveryCode), the Store itself, user
// creation and lookup, the cross-schema registration inserts, password and
// verification flags, and username assignment. The other tables live in
// otp_codes.go, sessions.go, twofa.go, oauth.go and outbox.go.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/atpost/identity-shared/crypto"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type User struct {
	ID                  uuid.UUID  `json:"id"`
	Phone               string     `json:"phone,omitempty"`
	Email               *string    `json:"email,omitempty"`
	PasswordHash        string     `json:"-"`
	EmailVerified       bool       `json:"email_verified"`
	PhoneVerified       bool       `json:"phone_verified"`
	TwoFactorEnabled    bool       `json:"two_factor_enabled"`
	TwoFactorMethod     string     `json:"two_factor_method"` // "totp" | "email" | "" (off); read via SecondFactorMethod
	TwoFactorSecret     *string    `json:"-"`
	AccountType         string     `json:"account_type"`
	AccountStatus       string     `json:"account_status"`
	LoginProvider       *string    `json:"login_provider,omitempty"`
	RecoveryEmail       *string    `json:"recovery_email,omitempty"`
	RecoveryPhone       *string    `json:"recovery_phone,omitempty"`
	AgeVerification     string     `json:"age_verification"`
	ConsentTerms        bool       `json:"consent_terms"`
	ConsentPrivacy      bool       `json:"consent_privacy"`
	ConsentAge          bool       `json:"consent_age"`
	DeletionRequestedAt *time.Time `json:"deletion_requested_at,omitempty"`
	ScheduledPurgeDate  *time.Time `json:"scheduled_purge_date,omitempty"`
	DeactivatedAt       *time.Time `json:"deactivated_at,omitempty"`
	PurgeCompletedAt    *time.Time `json:"-"`
	LastLoginAt         *time.Time `json:"last_login_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type Session struct {
	ID             uuid.UUID  `json:"id"`
	UserID         uuid.UUID  `json:"user_id"`
	RefreshToken   string     `json:"-"`
	DeviceID       string     `json:"device_id"`
	Platform       string     `json:"platform"`
	IP             string     `json:"ip"`
	UserAgent      string     `json:"user_agent"`
	IsActive       bool       `json:"is_active"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	FamilyID       *uuid.UUID `json:"family_id,omitempty"`
	anomalyFlagged bool
	LastRefreshAt  *time.Time `json:"last_refresh_at,omitempty"`
	LastRefreshIP  string     `json:"last_refresh_ip,omitempty"`
	// AuthTime is the last full authentication of this session (auth_time
	// claim); nil on sessions created before it was recorded.
	AuthTime *time.Time `json:"-"`
	// AMR is how this session authenticated (amr claim), e.g. ["pwd","otp"].
	AMR []string `json:"-"`
	// Kind is SessionKindConsumer or SessionKindAdmin (auth.sessions.kind).
	// Empty is stored as consumer. Only the admin-session sign-in sets admin.
	Kind string `json:"kind"`
}

// Session kinds. An admin session lives only in the admin console's
// host-only admin_* cookies; see internal/service/admin_login.go.
const (
	SessionKindConsumer = "consumer"
	SessionKindAdmin    = "admin"
)

// IsAdmin reports whether this is an admin-console session.
func (s *Session) IsAdmin() bool { return s != nil && s.Kind == SessionKindAdmin }

// AnomalyFlagged returns the persisted anomaly flag. Lowercase field
// keeps the JSON shape stable (we don't want to leak the flag to API
// consumers).
func (s *Session) AnomalyFlagged() bool { return s.anomalyFlagged }

type TrustedDevice struct {
	ID                uuid.UUID `json:"id"`
	UserID            uuid.UUID `json:"user_id"`
	DeviceFingerprint string    `json:"device_fingerprint"`
	DeviceName        *string   `json:"device_name,omitempty"`
	LastUsedAt        time.Time `json:"last_used_at"`
	TrustedAt         time.Time `json:"trusted_at"`
}

// LoginAnomaly is one row of auth.login_anomalies. Industry-standard
// audit trail backing the in-app security inbox + ops review queue.
type LoginAnomaly struct {
	ID             uuid.UUID      `json:"id"`
	UserID         uuid.UUID      `json:"user_id"`
	AnomalyType    string         `json:"anomaly_type"`
	IP             string         `json:"ip,omitempty"`
	UserAgent      string         `json:"user_agent,omitempty"`
	DeviceID       string         `json:"device_id,omitempty"`
	CountryCode    string         `json:"country_code,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	RiskScore      int            `json:"risk_score"`
	Challenged     bool           `json:"challenged"`
	AcknowledgedAt *time.Time     `json:"acknowledged_at,omitempty"`
	OccurredAt     time.Time      `json:"occurred_at"`
}

type OTP struct {
	ID        uuid.UUID
	Phone     string
	Hash      string
	Purpose   string
	ExpiresAt time.Time
	Attempts  int
}

type OutboxEvent struct {
	ID        int64
	EventType string
	Payload   json.RawMessage
	CreatedAt time.Time
}

// RecoveryCode represents a row in auth.recovery_codes.
type RecoveryCode struct {
	ID       uuid.UUID
	CodeHash string
}

type Store struct {
	db        *pgxpool.Pool
	totpCrypt *crypto.SecretBox
}

var ErrUserExists = errors.New("user already exists")

// WithTOTPEncryption wires the AES-256-GCM box used to seal/open
// TOTP secrets. Optional — when nil, Enable2FA still writes the
// legacy plaintext column (logged as a warning by main.go) and
// Get2FASecret reads it back.
func (s *Store) WithTOTPEncryption(box *crypto.SecretBox) *Store {
	s.totpCrypt = box
	return s
}

// allUserCols is the list of columns returned when scanning a User row.
const allUserCols = `user_id, COALESCE(phone, ''), email, COALESCE(password_hash, ''),
	email_verified, phone_verified, two_factor_enabled, COALESCE(two_factor_method, ''), two_factor_secret,
	account_type, account_status, login_provider, recovery_email, recovery_phone,
	age_verification, consent_terms, consent_privacy, consent_age,
	deletion_requested_at, scheduled_purge_date, deactivated_at,
	purge_completed_at, last_login_at,
	created_at, updated_at`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(
		&u.ID, &u.Phone, &u.Email, &u.PasswordHash,
		&u.EmailVerified, &u.PhoneVerified, &u.TwoFactorEnabled, &u.TwoFactorMethod, &u.TwoFactorSecret,
		&u.AccountType, &u.AccountStatus, &u.LoginProvider, &u.RecoveryEmail, &u.RecoveryPhone,
		&u.AgeVerification, &u.ConsentTerms, &u.ConsentPrivacy, &u.ConsentAge,
		&u.DeletionRequestedAt, &u.ScheduledPurgeDate, &u.DeactivatedAt,
		&u.PurgeCompletedAt, &u.LastLoginAt,
		&u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

func (s *Store) DB() *pgxpool.Pool {
	return s.db
}

// --- auth.users ---

func (s *Store) CreateUser(ctx context.Context, phone string) (*User, error) {
	id := uuid.New()
	now := time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.users (user_id, phone, created_at, updated_at)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (phone) DO NOTHING
	`, id, phone, now)
	if err != nil {
		return nil, err
	}
	return s.GetUserByPhone(ctx, phone)
}

func (s *Store) CreateUserTx(ctx context.Context, tx pgx.Tx, phone string) (*User, error) {
	id := uuid.New()
	now := time.Now()
	_, err := tx.Exec(ctx, `
		INSERT INTO auth.users (user_id, phone, created_at, updated_at)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (phone) DO NOTHING
	`, id, phone, now)
	if err != nil {
		return nil, err
	}
	row := tx.QueryRow(ctx, `SELECT `+allUserCols+` FROM auth.users WHERE phone = $1`, phone)
	return scanUser(row)
}

func (s *Store) CreateUserWithPassword(ctx context.Context, phone, email, passwordHash string) (*User, error) {
	id := uuid.New()
	now := time.Now()

	if phone == "" && email == "" {
		return nil, errors.New("either phone or email is required")
	}

	var phonePtr *string
	if phone != "" {
		phonePtr = &phone
	}

	var emailPtr *string
	if email != "" {
		emailPtr = &email
	}

	res, err := s.db.Exec(ctx, `
		INSERT INTO auth.users (user_id, phone, email, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT DO NOTHING
	`, id, phonePtr, emailPtr, passwordHash, now)

	if err != nil {
		return nil, err
	}
	if res.RowsAffected() == 0 {
		return nil, ErrUserExists
	}

	if phone != "" {
		return s.GetUserByPhone(ctx, phone)
	}
	return s.GetUserByEmail(ctx, email)
}

func (s *Store) CreateUserWithPasswordTx(ctx context.Context, tx pgx.Tx, phone, email, passwordHash string) (*User, error) {
	id := uuid.New()
	now := time.Now()

	if phone == "" && email == "" {
		return nil, errors.New("either phone or email is required")
	}

	var phonePtr *string
	if phone != "" {
		phonePtr = &phone
	}

	var emailPtr *string
	if email != "" {
		emailPtr = &email
	}

	res, err := tx.Exec(ctx, `
		INSERT INTO auth.users (user_id, phone, email, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT DO NOTHING
	`, id, phonePtr, emailPtr, passwordHash, now)

	if err != nil {
		return nil, err
	}
	if res.RowsAffected() == 0 {
		return nil, ErrUserExists
	}

	var row pgx.Row
	if phone != "" {
		row = tx.QueryRow(ctx, `SELECT `+allUserCols+` FROM auth.users WHERE phone = $1`, phone)
	} else {
		row = tx.QueryRow(ctx, `SELECT `+allUserCols+` FROM auth.users WHERE lower(email) = lower($1)`, email)
	}
	return scanUser(row)
}

func (s *Store) GetUserByPhone(ctx context.Context, phone string) (*User, error) {
	row := s.db.QueryRow(ctx, `SELECT `+allUserCols+` FROM auth.users WHERE phone = $1`, phone)
	return scanUser(row)
}

// GetUserByEmail matches case-insensitively. Uniqueness is on lower(email)
// (uq_users_email_lower), so this can match at most one row and is served by
// that index. Rows written before emails were normalised keep their original
// case and are still found.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	row := s.db.QueryRow(ctx, `SELECT `+allUserCols+` FROM auth.users WHERE lower(email) = lower($1)`, email)
	return scanUser(row)
}

func (s *Store) GetUserByID(ctx context.Context, userID uuid.UUID) (*User, error) {
	row := s.db.QueryRow(ctx, `SELECT `+allUserCols+` FROM auth.users WHERE user_id = $1`, userID)
	return scanUser(row)
}

func (s *Store) UpdateLastLogin(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.users SET last_login_at = NOW(), updated_at = NOW() WHERE user_id = $1
	`, userID)
	return err
}

// SoftDeleteUser was removed with the account-control workstream: it emitted
// `user.deletion_requested` at REQUEST time, whose consumers erase graph/post/
// user/dating slices immediately — incompatible with a cancellable 30-day
// window. The replacement is ScheduleDeletion (internal/store/lifecycle.go),
// which emits `user.deletion_scheduled`; the irreversible
// `user.purge_requested` is only emitted by the purge worker after the window
// has elapsed.

// --- Cross-schema transactional inserts (used during registration) ---

func (s *Store) CreateUserRecordTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error {
	now := time.Now()
	_, err := tx.Exec(ctx, `
		INSERT INTO usr.users (id, status, is_verified, created_at, updated_at)
		VALUES ($1, 'active', false, $2, $2)
		ON CONFLICT (id) DO NOTHING
	`, userID, now)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO usr.user_settings (user_id, account_visibility, allow_messages_from, allow_comments_from, created_at, updated_at)
		VALUES ($1, 'public', 'everyone', 'everyone', $2, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, userID, now)
	return err
}

func (s *Store) CreateProfileTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, displayName, firstName, lastName, dob, gender string) error {
	now := time.Now()

	var firstNamePtr, lastNamePtr, genderPtr *string
	if firstName != "" {
		firstNamePtr = &firstName
	}
	if lastName != "" {
		lastNamePtr = &lastName
	}
	if gender != "" {
		genderPtr = &gender
	}

	var dobPtr *time.Time
	if dob != "" {
		if t, err := time.Parse("2006-01-02", dob); err == nil {
			dobPtr = &t
		}
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO profile.profiles
			(user_id, display_name, first_name, last_name, bio, dob, gender,
			 category, profession, website, location, badge_flags, created_at, updated_at)
		VALUES ($1, $2, $3, $4, '', $5, $6, 'personal', '', '', '', 0, $7, $7)
		ON CONFLICT (user_id) DO NOTHING
	`, userID, displayName, firstNamePtr, lastNamePtr, dobPtr, genderPtr, now)
	return err
}

// --- Password Reset & Verification ---

// UpdatePassword updates a user's password hash.
func (s *Store) UpdatePassword(ctx context.Context, userID uuid.UUID, passwordHash string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.users SET password_hash = $1, updated_at = NOW() WHERE user_id = $2
	`, passwordHash, userID)
	return err
}

// MarkEmailVerified sets email_verified = true for a user.
func (s *Store) MarkEmailVerified(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.users SET email_verified = TRUE, updated_at = NOW() WHERE user_id = $1
	`, userID)
	return err
}

// MarkPhoneVerified sets phone_verified = true for a user.
func (s *Store) MarkPhoneVerified(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.users SET phone_verified = TRUE, updated_at = NOW() WHERE user_id = $1
	`, userID)
	return err
}

// ErrNoUsernameAvailable means every candidate handle was taken. The caller
// is expected to have supplied a random fallback, so reaching this is a
// signal that something other than contention is wrong.
var ErrNoUsernameAvailable = errors.New("no username candidate was available")

// AssignUsernameTx claims the first free handle from `candidates`, inside the
// caller's transaction.
//
// It does NOT check-then-write. Two registrations racing on the same email
// shape would both see a handle free and both try to take it; the partial
// unique index on profile.profiles(username) is the only thing that can
// actually arbitrate. So each attempt runs inside a SAVEPOINT and a unique
// violation just moves on to the next candidate, leaving the outer
// transaction — which is creating the whole account — untouched.
func (s *Store) AssignUsernameTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, candidates []string) (string, error) {
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		sp, err := tx.Begin(ctx) // SAVEPOINT
		if err != nil {
			return "", err
		}
		_, err = sp.Exec(ctx, `
			UPDATE profile.profiles SET username = $2, updated_at = NOW()
			WHERE user_id = $1
		`, userID, candidate)
		if err == nil {
			if err = sp.Commit(ctx); err == nil {
				return candidate, nil
			}
		}
		_ = sp.Rollback(ctx)
		if !isUniqueViolation(err) {
			return "", err
		}
	}
	return "", ErrNoUsernameAvailable
}

// isUniqueViolation reports whether err is Postgres 23505.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
