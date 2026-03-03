package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type User struct {
	ID                  uuid.UUID  `json:"id"`
	Phone               string     `json:"phone,omitempty"`
	Email               *string    `json:"email,omitempty"`
	PasswordHash        string     `json:"-"`
	EmailVerified       bool       `json:"email_verified"`
	PhoneVerified       bool       `json:"phone_verified"`
	TwoFactorEnabled    bool       `json:"two_factor_enabled"`
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
	LastLoginAt         *time.Time `json:"last_login_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type Session struct {
	ID           uuid.UUID  `json:"id"`
	UserID       uuid.UUID  `json:"user_id"`
	RefreshToken string     `json:"refresh_token"`
	DeviceID     string     `json:"device_id"`
	Platform     string     `json:"platform"`
	IP           string     `json:"ip"`
	UserAgent    string     `json:"user_agent"`
	IsActive     bool       `json:"is_active"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    time.Time  `json:"expires_at"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
}

type TrustedDevice struct {
	ID                uuid.UUID `json:"id"`
	UserID            uuid.UUID `json:"user_id"`
	DeviceFingerprint string    `json:"device_fingerprint"`
	DeviceName        *string   `json:"device_name,omitempty"`
	LastUsedAt        time.Time `json:"last_used_at"`
	TrustedAt         time.Time `json:"trusted_at"`
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
	db *pgxpool.Pool
}

var ErrUserExists = errors.New("user already exists")

// allUserCols is the list of columns returned when scanning a User row.
const allUserCols = `user_id, COALESCE(phone, ''), email, password_hash,
	email_verified, phone_verified, two_factor_enabled, two_factor_secret,
	account_type, account_status, login_provider, recovery_email, recovery_phone,
	age_verification, consent_terms, consent_privacy, consent_age,
	deletion_requested_at, scheduled_purge_date, last_login_at,
	created_at, updated_at`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(
		&u.ID, &u.Phone, &u.Email, &u.PasswordHash,
		&u.EmailVerified, &u.PhoneVerified, &u.TwoFactorEnabled, &u.TwoFactorSecret,
		&u.AccountType, &u.AccountStatus, &u.LoginProvider, &u.RecoveryEmail, &u.RecoveryPhone,
		&u.AgeVerification, &u.ConsentTerms, &u.ConsentPrivacy, &u.ConsentAge,
		&u.DeletionRequestedAt, &u.ScheduledPurgeDate, &u.LastLoginAt,
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
		row = tx.QueryRow(ctx, `SELECT `+allUserCols+` FROM auth.users WHERE email = $1`, email)
	}
	return scanUser(row)
}

func (s *Store) GetUserByPhone(ctx context.Context, phone string) (*User, error) {
	row := s.db.QueryRow(ctx, `SELECT `+allUserCols+` FROM auth.users WHERE phone = $1`, phone)
	return scanUser(row)
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	row := s.db.QueryRow(ctx, `SELECT `+allUserCols+` FROM auth.users WHERE email = $1`, email)
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

func (s *Store) SoftDeleteUser(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		UPDATE auth.users
		SET account_status = 'pending_deletion',
		    deletion_requested_at = NOW(),
		    scheduled_purge_date = NOW() + INTERVAL '30 days',
		    updated_at = NOW()
		WHERE user_id = $1
	`, userID)
	if err != nil {
		return err
	}

	payload := fmt.Sprintf(`{"user_id":"%s","requested_at":"%s"}`, userID.String(), time.Now().UTC().Format(time.RFC3339))
	_, err = tx.Exec(ctx,
		`INSERT INTO auth.outbox_events (event_type, partition_key, payload) VALUES ($1, $2, $3::jsonb)`,
		"user.deletion_requested", userID.String(), payload,
	)
	if err != nil {
		return fmt.Errorf("outbox insert failed: %w", err)
	}

	return tx.Commit(ctx)
}

// --- auth.otp_codes ---

func (s *Store) SaveOTP(ctx context.Context, phone, code, purpose string, ttl time.Duration) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(ctx, `
		DELETE FROM auth.otp_codes WHERE phone = $1 AND purpose = $2
	`, phone, purpose)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(ctx, `
		INSERT INTO auth.otp_codes (id, phone, otp_hash, purpose, expires_at, attempts)
		VALUES ($1, $2, $3, $4, $5, 0)
	`, uuid.New(), phone, string(hash), purpose, time.Now().Add(ttl))
	return err
}

func (s *Store) GetOTP(ctx context.Context, phone, purpose string) (*OTP, error) {
	var o OTP
	err := s.db.QueryRow(ctx, `
		SELECT id, phone, otp_hash, purpose, expires_at, attempts
		FROM auth.otp_codes
		WHERE phone = $1 AND purpose = $2 AND expires_at > NOW()
	`, phone, purpose).Scan(&o.ID, &o.Phone, &o.Hash, &o.Purpose, &o.ExpiresAt, &o.Attempts)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &o, nil
}

func (s *Store) IncrementOTPAttempts(ctx context.Context, id uuid.UUID) (int, error) {
	var attempts int
	err := s.db.QueryRow(ctx, `
		UPDATE auth.otp_codes
		SET attempts = attempts + 1
		WHERE id = $1
		RETURNING attempts
	`, id).Scan(&attempts)
	if err != nil {
		return 0, err
	}
	return attempts, nil
}

func (s *Store) DeleteOTP(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM auth.otp_codes WHERE id = $1
	`, id)
	return err
}

// --- auth.sessions ---

func (s *Store) CreateSession(ctx context.Context, sess *Session) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.sessions (session_id, user_id, refresh_token_hash, device_id, platform, ip, user_agent, is_active, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE, $8, $9)
	`, sess.ID, sess.UserID, sess.RefreshToken, sess.DeviceID, sess.Platform, sess.IP, sess.UserAgent, sess.CreatedAt, sess.ExpiresAt)
	return err
}

func scanSession(row pgx.Row) (*Session, error) {
	var sess Session
	err := row.Scan(
		&sess.ID, &sess.UserID, &sess.RefreshToken,
		&sess.DeviceID, &sess.Platform, &sess.IP, &sess.UserAgent,
		&sess.IsActive, &sess.CreatedAt, &sess.ExpiresAt, &sess.RevokedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &sess, nil
}

const allSessionCols = `session_id, user_id, refresh_token_hash, device_id, platform, ip, user_agent, is_active, created_at, expires_at, revoked_at`

func (s *Store) GetSessionByRefreshTokenHash(ctx context.Context, refreshTokenHash string) (*Session, error) {
	row := s.db.QueryRow(ctx, `SELECT `+allSessionCols+` FROM auth.sessions WHERE refresh_token_hash = $1`, refreshTokenHash)
	return scanSession(row)
}

func (s *Store) GetSessionByID(ctx context.Context, sessionID uuid.UUID) (*Session, error) {
	row := s.db.QueryRow(ctx, `SELECT `+allSessionCols+` FROM auth.sessions WHERE session_id = $1`, sessionID)
	return scanSession(row)
}

func (s *Store) ListActiveSessions(ctx context.Context, userID uuid.UUID) ([]Session, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+allSessionCols+`
		FROM auth.sessions
		WHERE user_id = $1 AND is_active = TRUE AND revoked_at IS NULL
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(
			&sess.ID, &sess.UserID, &sess.RefreshToken,
			&sess.DeviceID, &sess.Platform, &sess.IP, &sess.UserAgent,
			&sess.IsActive, &sess.CreatedAt, &sess.ExpiresAt, &sess.RevokedAt,
		); err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

func (s *Store) RotateSessionRefreshToken(ctx context.Context, sessionID uuid.UUID, refreshTokenHash string, expiresAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.sessions
		SET refresh_token_hash = $1, expires_at = $2
		WHERE session_id = $3 AND is_active = TRUE AND revoked_at IS NULL
	`, refreshTokenHash, expiresAt, sessionID)
	return err
}

func (s *Store) RevokeSession(ctx context.Context, sessionID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.sessions
		SET revoked_at = NOW(), is_active = FALSE
		WHERE session_id = $1 AND revoked_at IS NULL
	`, sessionID)
	return err
}

func (s *Store) RevokeAllSessions(ctx context.Context, userID uuid.UUID) (int64, error) {
	res, err := s.db.Exec(ctx, `
		UPDATE auth.sessions
		SET revoked_at = NOW(), is_active = FALSE
		WHERE user_id = $1 AND is_active = TRUE AND revoked_at IS NULL
	`, userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected(), nil
}

// --- auth.trusted_devices ---

func (s *Store) UpsertTrustedDevice(ctx context.Context, d *TrustedDevice) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.trusted_devices (id, user_id, device_fingerprint, device_name, last_used_at, trusted_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (user_id, device_fingerprint) DO UPDATE
		SET last_used_at = NOW(), device_name = COALESCE(EXCLUDED.device_name, auth.trusted_devices.device_name)
	`, d.ID, d.UserID, d.DeviceFingerprint, d.DeviceName)
	return err
}

func (s *Store) ListTrustedDevices(ctx context.Context, userID uuid.UUID) ([]TrustedDevice, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, device_fingerprint, device_name, last_used_at, trusted_at
		FROM auth.trusted_devices
		WHERE user_id = $1
		ORDER BY last_used_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []TrustedDevice
	for rows.Next() {
		var d TrustedDevice
		if err := rows.Scan(&d.ID, &d.UserID, &d.DeviceFingerprint, &d.DeviceName, &d.LastUsedAt, &d.TrustedAt); err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

func (s *Store) DeleteTrustedDevice(ctx context.Context, userID, deviceID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM auth.trusted_devices WHERE id = $1 AND user_id = $2
	`, deviceID, userID)
	return err
}

// --- 2FA ---

// Enable2FA sets two_factor_enabled=true and stores the encrypted TOTP secret.
func (s *Store) Enable2FA(ctx context.Context, userID uuid.UUID, secret string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.users
		SET two_factor_enabled = TRUE, two_factor_secret = $1, updated_at = NOW()
		WHERE user_id = $2
	`, secret, userID)
	return err
}

// Disable2FA clears the TOTP secret and disables 2FA.
func (s *Store) Disable2FA(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.users
		SET two_factor_enabled = FALSE, two_factor_secret = NULL, updated_at = NOW()
		WHERE user_id = $1
	`, userID)
	return err
}

// Get2FASecret returns the TOTP secret for a user.
func (s *Store) Get2FASecret(ctx context.Context, userID uuid.UUID) (string, error) {
	var secret *string
	err := s.db.QueryRow(ctx, `
		SELECT two_factor_secret FROM auth.users WHERE user_id = $1
	`, userID).Scan(&secret)
	if err != nil {
		return "", err
	}
	if secret == nil {
		return "", nil
	}
	return *secret, nil
}

// --- OAuth ---

// GetUserByLoginProvider finds a user by their login_provider and email combination.
func (s *Store) GetUserByLoginProvider(ctx context.Context, provider, email string) (*User, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+allUserCols+` FROM auth.users WHERE login_provider = $1 AND email = $2`,
		provider, email,
	)
	return scanUser(row)
}

// CreateUserWithOAuth creates a new user with a social login provider.
func (s *Store) CreateUserWithOAuth(ctx context.Context, provider, email, name string) (*User, error) {
	id := uuid.New()
	now := time.Now()

	res, err := s.db.Exec(ctx, `
		INSERT INTO auth.users (user_id, email, login_provider, email_verified, created_at, updated_at)
		VALUES ($1, $2, $3, TRUE, $4, $4)
		ON CONFLICT DO NOTHING
	`, id, email, provider, now)
	if err != nil {
		return nil, err
	}
	if res.RowsAffected() == 0 {
		return nil, ErrUserExists
	}
	return s.GetUserByEmail(ctx, email)
}

// CreateUserWithOAuthTx creates a new user with a social login provider inside a transaction.
func (s *Store) CreateUserWithOAuthTx(ctx context.Context, tx pgx.Tx, provider, email, name string) (*User, error) {
	id := uuid.New()
	now := time.Now()

	res, err := tx.Exec(ctx, `
		INSERT INTO auth.users (user_id, email, login_provider, email_verified, created_at, updated_at)
		VALUES ($1, $2, $3, TRUE, $4, $4)
		ON CONFLICT DO NOTHING
	`, id, email, provider, now)
	if err != nil {
		return nil, err
	}
	if res.RowsAffected() == 0 {
		return nil, ErrUserExists
	}
	row := tx.QueryRow(ctx, `SELECT `+allUserCols+` FROM auth.users WHERE email = $1`, email)
	return scanUser(row)
}

// LinkOAuthProvider sets the login_provider for an existing user.
func (s *Store) LinkOAuthProvider(ctx context.Context, userID uuid.UUID, provider string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.users
		SET login_provider = $1, updated_at = NOW()
		WHERE user_id = $2
	`, provider, userID)
	return err
}

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

// --- auth.outbox_events ---

func (s *Store) InsertOutboxEventTx(ctx context.Context, tx pgx.Tx, eventType, partitionKey string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO auth.outbox_events (event_type, partition_key, payload)
		VALUES ($1, $2, $3)
	`, eventType, partitionKey, data)
	return err
}

func (s *Store) FetchUnpublishedOutboxEvents(ctx context.Context, limit int) ([]OutboxEvent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, event_type, payload, created_at
		FROM auth.outbox_events
		WHERE published_at IS NULL
		ORDER BY id ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []OutboxEvent
	for rows.Next() {
		var e OutboxEvent
		if err := rows.Scan(&e.ID, &e.EventType, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) MarkOutboxEventPublished(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.outbox_events SET published_at = NOW() WHERE id = $1
	`, id)
	return err
}

// --- auth.recovery_codes ---

// StoreRecoveryCodes inserts hashed recovery codes into Postgres for the given user,
// replacing any existing unused codes.
func (s *Store) StoreRecoveryCodes(ctx context.Context, userID uuid.UUID, codeHashes []string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Remove existing unused codes
	if _, err := tx.Exec(ctx,
		`DELETE FROM auth.recovery_codes WHERE user_id = $1 AND used_at IS NULL`,
		userID,
	); err != nil {
		return fmt.Errorf("delete old recovery codes: %w", err)
	}

	for _, h := range codeHashes {
		if _, err := tx.Exec(ctx,
			`INSERT INTO auth.recovery_codes (user_id, code_hash) VALUES ($1, $2)`,
			userID, h,
		); err != nil {
			return fmt.Errorf("insert recovery code: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// GetUnusedRecoveryCodes returns all unused recovery code hashes for a user.
func (s *Store) GetUnusedRecoveryCodes(ctx context.Context, userID uuid.UUID) ([]RecoveryCode, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, code_hash FROM auth.recovery_codes WHERE user_id = $1 AND used_at IS NULL`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []RecoveryCode
	for rows.Next() {
		var r RecoveryCode
		if err := rows.Scan(&r.ID, &r.CodeHash); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// MarkRecoveryCodeUsed sets used_at = NOW() for a specific recovery code row.
func (s *Store) MarkRecoveryCodeUsed(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE auth.recovery_codes SET used_at = NOW() WHERE id = $1`,
		id,
	)
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
