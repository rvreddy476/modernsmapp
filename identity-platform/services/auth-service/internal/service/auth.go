package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-shared/events"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	store    Store
	producer Producer
	cfg      *config.Config
	log      *slog.Logger
	rdb      *redis.Client
}

type Store interface {
	DB() *pgxpool.Pool
	SaveOTP(ctx context.Context, phone, code, purpose string, ttl time.Duration) error
	GetOTP(ctx context.Context, phone, purpose string) (*store.OTP, error)
	IncrementOTPAttempts(ctx context.Context, id uuid.UUID) (int, error)
	DeleteOTP(ctx context.Context, id uuid.UUID) error
	GetUserByPhone(ctx context.Context, phone string) (*store.User, error)
	CreateUser(ctx context.Context, phone string) (*store.User, error)
	CreateUserTx(ctx context.Context, tx pgx.Tx, phone string) (*store.User, error)
	CreateUserWithPassword(ctx context.Context, phone, email, passwordHash string) (*store.User, error)
	CreateUserWithPasswordTx(ctx context.Context, tx pgx.Tx, phone, email, passwordHash string) (*store.User, error)
	GetUserByEmail(ctx context.Context, email string) (*store.User, error)
	GetUserByID(ctx context.Context, userID uuid.UUID) (*store.User, error)
	UpdateLastLogin(ctx context.Context, userID uuid.UUID) error
	SoftDeleteUser(ctx context.Context, userID uuid.UUID) error
	UpdatePassword(ctx context.Context, userID uuid.UUID, passwordHash string) error
	MarkEmailVerified(ctx context.Context, userID uuid.UUID) error
	MarkPhoneVerified(ctx context.Context, userID uuid.UUID) error
	// Sessions
	CreateSession(ctx context.Context, sess *store.Session) error
	GetSessionByRefreshTokenHash(ctx context.Context, refreshTokenHash string) (*store.Session, error)
	GetSessionByID(ctx context.Context, sessionID uuid.UUID) (*store.Session, error)
	ListActiveSessions(ctx context.Context, userID uuid.UUID) ([]store.Session, error)
	RotateSessionRefreshToken(ctx context.Context, sessionID uuid.UUID, refreshTokenHash string, expiresAt time.Time) error
	RevokeSession(ctx context.Context, sessionID uuid.UUID) error
	RevokeAllSessions(ctx context.Context, userID uuid.UUID) (int64, error)
	// Trusted devices
	UpsertTrustedDevice(ctx context.Context, d *store.TrustedDevice) error
	ListTrustedDevices(ctx context.Context, userID uuid.UUID) ([]store.TrustedDevice, error)
	DeleteTrustedDevice(ctx context.Context, userID, deviceID uuid.UUID) error
	// 2FA
	Enable2FA(ctx context.Context, userID uuid.UUID, secret string) error
	Disable2FA(ctx context.Context, userID uuid.UUID) error
	Get2FASecret(ctx context.Context, userID uuid.UUID) (string, error)
	// OAuth
	GetUserByLoginProvider(ctx context.Context, provider, email string) (*store.User, error)
	CreateUserWithOAuth(ctx context.Context, provider, email, name string) (*store.User, error)
	CreateUserWithOAuthTx(ctx context.Context, tx pgx.Tx, provider, email, name string) (*store.User, error)
	LinkOAuthProvider(ctx context.Context, userID uuid.UUID, provider string) error
	// Cross-schema transactional inserts
	CreateUserRecordTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error
	CreateProfileTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, displayName, firstName, lastName, dob, gender string) error
	// Outbox
	InsertOutboxEventTx(ctx context.Context, tx pgx.Tx, eventType, partitionKey string, payload interface{}) error
	FetchUnpublishedOutboxEvents(ctx context.Context, limit int) ([]store.OutboxEvent, error)
	MarkOutboxEventPublished(ctx context.Context, id int64) error
	// Recovery codes
	StoreRecoveryCodes(ctx context.Context, userID uuid.UUID, codeHashes []string) error
	GetUnusedRecoveryCodes(ctx context.Context, userID uuid.UUID) ([]store.RecoveryCode, error)
	MarkRecoveryCodeUsed(ctx context.Context, id uuid.UUID) error
}

type Producer interface {
	PublishUserRegistered(ctx context.Context, userID uuid.UUID, phone string, email *string, firstName, lastName, dob, gender string) error
	PublishUserLoggedIn(ctx context.Context, userID, sessionID uuid.UUID, deviceID, platform, ip string) error
	PublishRaw(ctx context.Context, eventType string, partitionKey string, payloadBytes json.RawMessage) error
}

func New(store Store, producer Producer, cfg *config.Config, logger *slog.Logger, rdb *redis.Client) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: store, producer: producer, cfg: cfg, log: logger, rdb: rdb}
}

type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type AuthResponse struct {
	Tokens       TokenPair   `json:"tokens"`
	User         *store.User `json:"user"`
	SessionID    uuid.UUID   `json:"session_id"`
	Requires2FA  bool        `json:"requires_2fa,omitempty"`
	PendingToken string      `json:"pending_token,omitempty"`
}

type AccessClaims struct {
	jwt.RegisteredClaims
	SessionID string `json:"sid"`
}

// RequestOTP generates and saves an OTP.
func (s *Service) RequestOTP(ctx context.Context, phone, purpose string) error {
	otp, err := s.generateOTP()
	if err != nil {
		return fmt.Errorf("failed to generate otp: %w", err)
	}

	s.log.Debug("otp generated", "phone", maskPhone(phone), "purpose", purpose)
	return s.store.SaveOTP(ctx, phone, otp, purpose, s.cfg.OTPExpiry)
}

// VerifyOTP validates OTP and logs the user in.
func (s *Service) VerifyOTP(ctx context.Context, phone, code, purpose, deviceID, platform, ip, userAgent string) (*AuthResponse, error) {
	if s.cfg.OTPBypassCode != "" && code == s.cfg.OTPBypassCode {
		// Bypass for dev/test environments only
	} else {
		otp, err := s.store.GetOTP(ctx, phone, purpose)
		if err != nil {
			return nil, err
		}
		if otp == nil {
			return nil, errors.New("invalid or expired otp")
		}
		if otp.Attempts >= s.cfg.OTPMaxAttempts {
			return nil, errors.New("invalid or expired otp")
		}
		if err := bcrypt.CompareHashAndPassword([]byte(otp.Hash), []byte(code)); err != nil {
			attempts, incErr := s.store.IncrementOTPAttempts(ctx, otp.ID)
			if incErr != nil {
				return nil, incErr
			}
			if attempts >= s.cfg.OTPMaxAttempts {
				_ = s.store.DeleteOTP(ctx, otp.ID)
			}
			return nil, errors.New("invalid or expired otp")
		}
		if err := s.store.DeleteOTP(ctx, otp.ID); err != nil {
			return nil, err
		}
	}

	user, err := s.store.GetUserByPhone(ctx, phone)
	if err != nil {
		return nil, err
	}
	created := false
	if user == nil {
		// Transactional: create auth user + usr record + profile + outbox event
		tx, err := s.store.DB().Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer tx.Rollback(ctx)

		user, err = s.store.CreateUserTx(ctx, tx, phone)
		if err != nil {
			return nil, err
		}

		if err := s.store.CreateUserRecordTx(ctx, tx, user.ID); err != nil {
			return nil, fmt.Errorf("failed to create user record: %w", err)
		}

		displayName := "User " + user.ID.String()[:8]
		if err := s.store.CreateProfileTx(ctx, tx, user.ID, displayName, "", "", "", ""); err != nil {
			return nil, fmt.Errorf("failed to create profile: %w", err)
		}

		outboxPayload := events.UserRegisteredPayload{
			UserID:    user.ID.String(),
			Phone:     phone,
			CreatedAt: time.Now(),
		}
		if err := s.store.InsertOutboxEventTx(ctx, tx, events.UserRegistered, user.ID.String(), outboxPayload); err != nil {
			return nil, fmt.Errorf("failed to insert outbox event: %w", err)
		}

		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("failed to commit transaction: %w", err)
		}
		created = true
	}

	// Guard: if 2FA is enabled, do not issue a full session — require second factor
	if user.TwoFactorEnabled {
		pendingToken, err := s.StorePending2FASession(ctx, user.ID, deviceID, platform, ip, userAgent)
		if err != nil {
			return nil, fmt.Errorf("failed to create pending 2FA session: %w", err)
		}
		return &AuthResponse{
			Requires2FA:  true,
			PendingToken: pendingToken,
			User:         user,
		}, nil
	}

	sessionID := uuid.New()
	refreshToken, err := generateOpaqueToken(32)
	if err != nil {
		return nil, err
	}

	sess := &store.Session{
		ID:           sessionID,
		UserID:       user.ID,
		RefreshToken: hashToken(refreshToken),
		DeviceID:     deviceID,
		Platform:     platform,
		IP:           ip,
		UserAgent:    userAgent,
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(s.cfg.RefreshTokenTTL),
	}

	if err := s.store.CreateSession(ctx, sess); err != nil {
		return nil, err
	}

	accessToken, err := s.generateAccessToken(user.ID, sessionID)
	if err != nil {
		return nil, err
	}

	if created {
		s.log.Info("user registered via OTP", "user_id", user.ID)
	}
	if err := s.producer.PublishUserLoggedIn(ctx, user.ID, sessionID, deviceID, platform, ip); err != nil {
		s.log.Warn("failed to publish user logged in event", "err", err, "user_id", user.ID, "session_id", sessionID)
	}

	// Anomaly detection: check if IP or device changed
	s.detectLoginAnomaly(ctx, user.ID.String(), ip, deviceID, platform)

	return &AuthResponse{
		Tokens: TokenPair{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			ExpiresAt:    time.Now().Add(s.cfg.AccessTokenTTL),
		},
		User:      user,
		SessionID: sessionID,
	}, nil
}

var (
	ErrPasswordTooShort = errors.New("password must be at least 8 characters")
	ErrPasswordTooWeak  = errors.New("password must contain at least one uppercase letter, one digit, and one special character")
)

var (
	hasUppercase = regexp.MustCompile(`[A-Z]`)
	hasDigit     = regexp.MustCompile(`[0-9]`)
	hasSpecial   = regexp.MustCompile(`[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]`)
)

func validatePassword(pw string) error {
	if len(pw) < 8 {
		return ErrPasswordTooShort
	}
	if !hasUppercase.MatchString(pw) || !hasDigit.MatchString(pw) || !hasSpecial.MatchString(pw) {
		return ErrPasswordTooWeak
	}
	return nil
}

func (s *Service) RegisterWithPassword(ctx context.Context, phone, email, password, firstName, lastName, dob, gender string) (*AuthResponse, error) {
	if err := validatePassword(password); err != nil {
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	// Transactional: create auth user + usr record + profile + outbox event
	tx, err := s.store.DB().Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	user, err := s.store.CreateUserWithPasswordTx(ctx, tx, phone, email, string(hash))
	if err != nil {
		return nil, err
	}

	if err := s.store.CreateUserRecordTx(ctx, tx, user.ID); err != nil {
		return nil, fmt.Errorf("failed to create user record: %w", err)
	}

	displayName := firstName + " " + lastName
	if strings.TrimSpace(displayName) == "" {
		displayName = "User " + user.ID.String()[:8]
	}
	if err := s.store.CreateProfileTx(ctx, tx, user.ID, displayName, firstName, lastName, dob, gender); err != nil {
		return nil, fmt.Errorf("failed to create profile: %w", err)
	}

	var emailPtr *string
	if email != "" {
		emailPtr = &email
	}
	outboxPayload := events.UserRegisteredPayload{
		UserID:    user.ID.String(),
		Phone:     phone,
		Email:     emailPtr,
		FirstName: firstName,
		LastName:  lastName,
		DOB:       dob,
		Gender:    gender,
		CreatedAt: time.Now(),
	}
	if err := s.store.InsertOutboxEventTx(ctx, tx, events.UserRegistered, user.ID.String(), outboxPayload); err != nil {
		return nil, fmt.Errorf("failed to insert outbox event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	sessionID := uuid.New()
	refreshToken, err := generateOpaqueToken(32)
	if err != nil {
		return nil, err
	}

	sess := &store.Session{
		ID:           sessionID,
		UserID:       user.ID,
		RefreshToken: hashToken(refreshToken),
		DeviceID:     "web",
		Platform:     "web",
		IP:           "",
		UserAgent:    "",
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(s.cfg.RefreshTokenTTL),
	}

	if err := s.store.CreateSession(ctx, sess); err != nil {
		return nil, err
	}

	accessToken, err := s.generateAccessToken(user.ID, sessionID)
	if err != nil {
		return nil, err
	}

	if err := s.producer.PublishUserLoggedIn(ctx, user.ID, sessionID, "web", "web", ""); err != nil {
		s.log.Warn("failed to publish user logged in event", "err", err, "user_id", user.ID, "session_id", sessionID)
	}

	return &AuthResponse{
		Tokens: TokenPair{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			ExpiresAt:    time.Now().Add(s.cfg.AccessTokenTTL),
		},
		User:      user,
		SessionID: sessionID,
	}, nil
}

func (s *Service) LoginWithPassword(ctx context.Context, identifier, password, deviceID, platform, ip, userAgent string) (*AuthResponse, error) {
	user, err := s.store.GetUserByPhone(ctx, identifier)
	if err != nil {
		return nil, err
	}

	if user == nil {
		user, err = s.store.GetUserByEmail(ctx, identifier)
		if err != nil {
			return nil, err
		}
	}

	if user == nil {
		return nil, errors.New("invalid credentials")
	}

	if user.PasswordHash == "" {
		return nil, errors.New("user has no password set (try OTP login)")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, errors.New("invalid credentials")
	}

	// If 2FA is enabled, return a pending response instead of creating a full session
	if user.TwoFactorEnabled {
		pendingToken, err := s.StorePending2FASession(ctx, user.ID, deviceID, platform, ip, userAgent)
		if err != nil {
			return nil, fmt.Errorf("failed to create pending 2FA session: %w", err)
		}
		return &AuthResponse{
			Requires2FA:  true,
			PendingToken: pendingToken,
			User:         user,
		}, nil
	}

	sessionID := uuid.New()
	refreshToken, err := generateOpaqueToken(32)
	if err != nil {
		return nil, err
	}

	sess := &store.Session{
		ID:           sessionID,
		UserID:       user.ID,
		RefreshToken: hashToken(refreshToken),
		DeviceID:     deviceID,
		Platform:     platform,
		IP:           ip,
		UserAgent:    userAgent,
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(s.cfg.RefreshTokenTTL),
	}

	if err := s.store.CreateSession(ctx, sess); err != nil {
		return nil, err
	}

	accessToken, err := s.generateAccessToken(user.ID, sessionID)
	if err != nil {
		return nil, err
	}

	// Update last_login_at
	if err := s.store.UpdateLastLogin(ctx, user.ID); err != nil {
		s.log.Warn("failed to update last_login_at", "err", err, "user_id", user.ID)
	}

	if err := s.producer.PublishUserLoggedIn(ctx, user.ID, sessionID, deviceID, platform, ip); err != nil {
		s.log.Warn("failed to publish user logged in event", "err", err, "user_id", user.ID, "session_id", sessionID)
	}

	// Anomaly detection: check if IP or device changed
	s.detectLoginAnomaly(ctx, user.ID.String(), ip, deviceID, platform)

	return &AuthResponse{
		Tokens: TokenPair{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			ExpiresAt:    time.Now().Add(s.cfg.AccessTokenTTL),
		},
		User:      user,
		SessionID: sessionID,
	}, nil
}

func (s *Service) RefreshSession(ctx context.Context, refreshToken, ip, userAgent string) (*AuthResponse, error) {
	if refreshToken == "" {
		return nil, errors.New("missing refresh token")
	}

	refreshTokenHash := hashToken(refreshToken)
	sess, err := s.store.GetSessionByRefreshTokenHash(ctx, refreshTokenHash)
	if err != nil {
		return nil, err
	}
	if sess == nil || sess.RevokedAt != nil {
		return nil, errors.New("invalid refresh token")
	}

	if time.Now().After(sess.ExpiresAt) {
		return nil, errors.New("refresh token expired")
	}

	user, err := s.store.GetUserByID(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, errors.New("user not found")
	}

	newRefreshToken, err := generateOpaqueToken(32)
	if err != nil {
		return nil, err
	}
	newExpiresAt := time.Now().Add(s.cfg.RefreshTokenTTL)
	if err := s.store.RotateSessionRefreshToken(ctx, sess.ID, hashToken(newRefreshToken), newExpiresAt); err != nil {
		return nil, err
	}

	accessToken, err := s.generateAccessToken(user.ID, sess.ID)
	if err != nil {
		return nil, err
	}

	return &AuthResponse{
		Tokens: TokenPair{
			AccessToken:  accessToken,
			RefreshToken: newRefreshToken,
			ExpiresAt:    time.Now().Add(s.cfg.AccessTokenTTL),
		},
		User:      user,
		SessionID: sess.ID,
	}, nil
}

func (s *Service) generateOTP() (string, error) {
	max := int64(1)
	for i := 0; i < s.cfg.OTPDigits; i++ {
		max *= 10
	}
	n, err := rand.Int(rand.Reader, big.NewInt(max))
	if err != nil {
		return "", err
	}
	format := fmt.Sprintf("%%0%dd", s.cfg.OTPDigits)
	return fmt.Sprintf(format, n.Int64()), nil
}

func (s *Service) generateAccessToken(userID, sessionID uuid.UUID) (string, error) {
	now := time.Now()
	claims := AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Issuer:    "auth-service",
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.cfg.AccessTokenTTL)),
		},
		SessionID: sessionID.String(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.cfg.JWTSecret))
}

func generateOpaqueToken(length int) (string, error) {
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Service) GetSessionForLogout(ctx context.Context, refreshToken string) (*store.Session, error) {
	if refreshToken == "" {
		return nil, nil
	}
	return s.store.GetSessionByRefreshTokenHash(ctx, hashToken(refreshToken))
}

func (s *Service) RevokeSession(ctx context.Context, sessionID uuid.UUID) error {
	return s.store.RevokeSession(ctx, sessionID)
}

func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	sess, err := s.store.GetSessionByRefreshTokenHash(ctx, hashToken(refreshToken))
	if err != nil {
		return err
	}
	if sess == nil {
		return nil
	}
	return s.store.RevokeSession(ctx, sess.ID)
}

// LogoutAll revokes all sessions for a user.
func (s *Service) LogoutAll(ctx context.Context, userID uuid.UUID) (int64, error) {
	return s.store.RevokeAllSessions(ctx, userID)
}

// ListSessions returns all active sessions for a user.
func (s *Service) ListSessions(ctx context.Context, userID uuid.UUID) ([]store.Session, error) {
	return s.store.ListActiveSessions(ctx, userID)
}

// RevokeSessionByID revokes a specific session, ensuring it belongs to the user.
func (s *Service) RevokeSessionByID(ctx context.Context, userID, sessionID uuid.UUID) error {
	sess, err := s.store.GetSessionByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if sess == nil {
		return errors.New("session not found")
	}
	if sess.UserID != userID {
		return errors.New("session not found")
	}
	return s.store.RevokeSession(ctx, sessionID)
}

// DeleteAccount soft-deletes a user account with 30-day grace period.
func (s *Service) DeleteAccount(ctx context.Context, userID uuid.UUID) error {
	if err := s.store.SoftDeleteUser(ctx, userID); err != nil {
		return fmt.Errorf("failed to soft delete user: %w", err)
	}
	// Revoke all sessions
	if _, err := s.store.RevokeAllSessions(ctx, userID); err != nil {
		s.log.Warn("failed to revoke sessions during account deletion", "err", err, "user_id", userID)
	}
	return nil
}

// DataExport holds all personal data for a user, for GDPR data portability.
type DataExport struct {
	UserID     string      `json:"user_id"`
	User       interface{} `json:"user"`
	Sessions   interface{} `json:"sessions"`
	Devices    interface{} `json:"devices"`
	ExportedAt time.Time   `json:"exported_at"`
}

// ExportUserData collects a user's personal data from the auth store for GDPR portability.
func (s *Service) ExportUserData(ctx context.Context, userID string) (*DataExport, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, err
	}

	// Fetch user record
	user, err := s.store.GetUserByID(ctx, uid)
	if err != nil {
		return nil, err
	}

	// Fetch active sessions
	sessions, _ := s.store.ListActiveSessions(ctx, uid)

	// Fetch trusted devices
	devices, _ := s.store.ListTrustedDevices(ctx, uid)

	return &DataExport{
		UserID:     userID,
		User:       user,
		Sessions:   sessions,
		Devices:    devices,
		ExportedAt: time.Now(),
	}, nil
}

func maskPhone(phone string) string {
	trimmed := strings.TrimSpace(phone)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= 4 {
		return strings.Repeat("*", len(trimmed))
	}
	return strings.Repeat("*", len(trimmed)-2) + trimmed[len(trimmed)-2:]
}

// --- Password Reset ---

// ForgotPassword sends a password reset OTP to the user's phone or email.
func (s *Service) ForgotPassword(ctx context.Context, identifier string) error {
	// Find user by phone or email
	user, err := s.store.GetUserByPhone(ctx, identifier)
	if err != nil {
		return err
	}
	if user == nil {
		user, err = s.store.GetUserByEmail(ctx, identifier)
		if err != nil {
			return err
		}
	}
	if user == nil {
		// Don't reveal whether the user exists
		return nil
	}

	otp, err := s.generateOTP()
	if err != nil {
		return fmt.Errorf("failed to generate otp: %w", err)
	}

	// Use phone as OTP key; fall back to email
	otpKey := user.Phone
	if otpKey == "" && user.Email != nil {
		otpKey = *user.Email
	}
	if otpKey == "" {
		return errors.New("user has no phone or email")
	}

	s.log.Debug("password reset otp generated", "identifier", maskPhone(identifier))
	return s.store.SaveOTP(ctx, otpKey, otp, "password_reset", s.cfg.OTPExpiry)
}

// ResetPassword verifies the OTP and sets a new password.
func (s *Service) ResetPassword(ctx context.Context, identifier, code, newPassword string) error {
	// Find user
	user, err := s.store.GetUserByPhone(ctx, identifier)
	if err != nil {
		return err
	}
	if user == nil {
		user, err = s.store.GetUserByEmail(ctx, identifier)
		if err != nil {
			return err
		}
	}
	if user == nil {
		return errors.New("invalid credentials")
	}

	// Verify OTP
	otpKey := user.Phone
	if otpKey == "" && user.Email != nil {
		otpKey = *user.Email
	}

	otp, err := s.store.GetOTP(ctx, otpKey, "password_reset")
	if err != nil {
		return err
	}
	if otp == nil {
		return errors.New("invalid or expired code")
	}
	if otp.Attempts >= s.cfg.OTPMaxAttempts {
		return errors.New("too many attempts")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(otp.Hash), []byte(code)); err != nil {
		s.store.IncrementOTPAttempts(ctx, otp.ID)
		return errors.New("invalid or expired code")
	}

	_ = s.store.DeleteOTP(ctx, otp.ID)

	// Validate new password policy
	if err := validatePassword(newPassword); err != nil {
		return err
	}

	// Hash new password and update
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	if err := s.store.UpdatePassword(ctx, user.ID, string(hash)); err != nil {
		return err
	}

	// Revoke all existing sessions for security
	s.store.RevokeAllSessions(ctx, user.ID)

	s.log.Info("password reset successful", "user_id", user.ID)
	return nil
}

// --- Email/Phone Verification ---

// RequestEmailVerification sends a verification OTP to the user's email.
func (s *Service) RequestEmailVerification(ctx context.Context, userID uuid.UUID) error {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil {
		return errors.New("user not found")
	}
	if user.Email == nil || *user.Email == "" {
		return errors.New("no email on account")
	}
	if user.EmailVerified {
		return errors.New("email already verified")
	}

	otp, err := s.generateOTP()
	if err != nil {
		return fmt.Errorf("failed to generate otp: %w", err)
	}

	return s.store.SaveOTP(ctx, *user.Email, otp, "email_verify", s.cfg.OTPExpiry)
}

// VerifyEmail checks the OTP and marks the user's email as verified.
func (s *Service) VerifyEmail(ctx context.Context, userID uuid.UUID, code string) error {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil {
		return errors.New("user not found")
	}
	if user.Email == nil {
		return errors.New("no email on account")
	}

	otp, err := s.store.GetOTP(ctx, *user.Email, "email_verify")
	if err != nil {
		return err
	}
	if otp == nil {
		return errors.New("invalid or expired code")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(otp.Hash), []byte(code)); err != nil {
		s.store.IncrementOTPAttempts(ctx, otp.ID)
		return errors.New("invalid or expired code")
	}

	_ = s.store.DeleteOTP(ctx, otp.ID)
	return s.store.MarkEmailVerified(ctx, userID)
}

// RequestPhoneVerification sends a verification OTP to the user's phone.
func (s *Service) RequestPhoneVerification(ctx context.Context, userID uuid.UUID) error {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil {
		return errors.New("user not found")
	}
	if user.Phone == "" {
		return errors.New("no phone on account")
	}
	if user.PhoneVerified {
		return errors.New("phone already verified")
	}

	otp, err := s.generateOTP()
	if err != nil {
		return fmt.Errorf("failed to generate otp: %w", err)
	}

	return s.store.SaveOTP(ctx, user.Phone, otp, "phone_verify", s.cfg.OTPExpiry)
}

// VerifyPhone checks the OTP and marks the user's phone as verified.
func (s *Service) VerifyPhone(ctx context.Context, userID uuid.UUID, code string) error {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil {
		return errors.New("user not found")
	}
	if user.Phone == "" {
		return errors.New("no phone on account")
	}

	otp, err := s.store.GetOTP(ctx, user.Phone, "phone_verify")
	if err != nil {
		return err
	}
	if otp == nil {
		return errors.New("invalid or expired code")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(otp.Hash), []byte(code)); err != nil {
		s.store.IncrementOTPAttempts(ctx, otp.ID)
		return errors.New("invalid or expired code")
	}

	_ = s.store.DeleteOTP(ctx, otp.ID)
	return s.store.MarkPhoneVerified(ctx, userID)
}

// --- Trusted Devices ---

// ListTrustedDevices returns all trusted devices for a user.
func (s *Service) ListTrustedDevices(ctx context.Context, userID uuid.UUID) ([]store.TrustedDevice, error) {
	return s.store.ListTrustedDevices(ctx, userID)
}

// TrustDevice registers a device as trusted for a user.
func (s *Service) TrustDevice(ctx context.Context, userID uuid.UUID, fingerprint string, deviceName *string) error {
	d := &store.TrustedDevice{
		ID:                uuid.New(),
		UserID:            userID,
		DeviceFingerprint: fingerprint,
		DeviceName:        deviceName,
	}
	return s.store.UpsertTrustedDevice(ctx, d)
}

// RemoveTrustedDevice deletes a trusted device.
func (s *Service) RemoveTrustedDevice(ctx context.Context, userID, deviceID uuid.UUID) error {
	return s.store.DeleteTrustedDevice(ctx, userID, deviceID)
}

// createSessionForUser is a helper to create a full session (shared logic).
func (s *Service) createSessionForUser(ctx context.Context, user *store.User, deviceID, platform, ip, userAgent string) (*AuthResponse, error) {
	sessionID := uuid.New()
	refreshToken, err := generateOpaqueToken(32)
	if err != nil {
		return nil, err
	}

	sess := &store.Session{
		ID:           sessionID,
		UserID:       user.ID,
		RefreshToken: hashToken(refreshToken),
		DeviceID:     deviceID,
		Platform:     platform,
		IP:           ip,
		UserAgent:    userAgent,
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(s.cfg.RefreshTokenTTL),
	}

	if err := s.store.CreateSession(ctx, sess); err != nil {
		return nil, err
	}

	accessToken, err := s.generateAccessToken(user.ID, sessionID)
	if err != nil {
		return nil, err
	}

	return &AuthResponse{
		Tokens: TokenPair{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			ExpiresAt:    time.Now().Add(s.cfg.AccessTokenTTL),
		},
		User:      user,
		SessionID: sessionID,
	}, nil
}

// detectLoginAnomaly checks for a changed IP and emits a best-effort anomaly event via the producer.
func (s *Service) detectLoginAnomaly(ctx context.Context, userID, ip, deviceID, platform string) {
	lastIPKey := fmt.Sprintf("last_ip:%s", userID)
	lastIP, _ := s.rdb.Get(ctx, lastIPKey).Result()
	isNewIP := lastIP != "" && lastIP != ip
	isNewDevice := false // device history check not yet implemented

	// Store new last IP (30-day TTL)
	s.rdb.Set(ctx, lastIPKey, ip, 30*24*time.Hour)

	if isNewIP || isNewDevice {
		payload := map[string]interface{}{
			"user_id":       userID,
			"ip":            ip,
			"device_id":     deviceID,
			"platform":      platform,
			"is_new_ip":     isNewIP,
			"is_new_device": isNewDevice,
			"occurred_at":   time.Now(),
		}
		payloadBytes, err := json.Marshal(payload)
		if err == nil {
			// Best-effort: do not fail auth if anomaly event emission fails
			_ = s.producer.PublishRaw(ctx, "user.login_anomaly", userID, json.RawMessage(payloadBytes))
		}
	}
}
