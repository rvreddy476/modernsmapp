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
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-shared/events"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	store                Store
	producer             Producer
	cfg                  *config.Config
	log                  *slog.Logger
	rdb                  *redis.Client
	miniAppSessionSigner *MiniAppSessionSigner
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
	RotateSessionWithFingerprint(ctx context.Context, sessionID uuid.UUID, refreshTokenHash, ip string, expiresAt time.Time, anomalyFlagged bool) error
	RevokeSession(ctx context.Context, sessionID uuid.UUID) error
	RevokeAllSessions(ctx context.Context, userID uuid.UUID) (int64, error)
	// A13 — login anomaly audit trail
	RecordLoginAnomaly(ctx context.Context, userID uuid.UUID, anomalyType, ip, userAgent, deviceID, countryCode string, riskScore int, challenged bool, metadata map[string]any) error
	ListLoginAnomalies(ctx context.Context, userID uuid.UUID, limit int) ([]store.LoginAnomaly, error)
	AcknowledgeAnomaly(ctx context.Context, userID, anomalyID uuid.UUID) (int64, error)
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

func New(store Store, producer Producer, cfg *config.Config, logger *slog.Logger, rdb *redis.Client, miniAppSessionSigner *MiniAppSessionSigner) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:                store,
		producer:             producer,
		cfg:                  cfg,
		log:                  logger,
		rdb:                  rdb,
		miniAppSessionSigner: miniAppSessionSigner,
	}
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
	s.detectLoginAnomaly(ctx, user.ID.String(), ip, deviceID, platform, userAgent)

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

// minimumAgeYears is the hard floor for self-service registration.
// India's DPDP Act additionally requires verifiable parental consent for
// users under 18 — that consent flow is a separate effort; this gate is
// the absolute minimum age, enforced whenever a date of birth is supplied.
const minimumAgeYears = 13

// validateMinimumAge rejects registrations below minimumAgeYears. An
// absent DOB is not enforced here (the registration form may not collect
// one); a malformed DOB is rejected outright.
func validateMinimumAge(dob string) error {
	if strings.TrimSpace(dob) == "" {
		return nil
	}
	born, err := time.Parse("2006-01-02", dob)
	if err != nil {
		return fmt.Errorf("invalid date of birth")
	}
	now := time.Now()
	age := now.Year() - born.Year()
	if now.YearDay() < born.YearDay() {
		age--
	}
	if age < minimumAgeYears {
		return fmt.Errorf("you must be at least %d years old to register", minimumAgeYears)
	}
	return nil
}

func (s *Service) RegisterWithPassword(ctx context.Context, phone, email, password, firstName, lastName, dob, gender string) (*AuthResponse, error) {
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	if err := validateMinimumAge(dob); err != nil {
		return nil, err
	}

	// Audit A9: bcrypt cost env-tunable via BCRYPT_COST.
	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.cfg.BcryptCost)
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
	s.detectLoginAnomaly(ctx, user.ID.String(), ip, deviceID, platform, userAgent)

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

// A15 + A11: refresh-token IP/UA bind. Refresh-token theft (via XSS / stolen
// laptop / shared device) is the leading silent-takeover vector once
// the access token expires. We persist the IP/UA at session creation
// and on every refresh we evaluate whether the caller's fingerprint
// matches. The policy is graduated:
//
//   - Same /24 (or /48 v6) + same UA family: rotate, no flag (the
//     common case — DHCP rotation within the same NAT pool is normal).
//   - Different specific IP but same /24 subnet, same UA family:
//     rotate but record a low-risk anomaly (legitimate — minor LAN
//     reassignment).
//   - Different /24 OR different UA family: HIGH risk. Refresh is
//     denied; user must re-authenticate. A11 tightened the policy
//     here: previously we required BOTH subnet AND UA to differ
//     (which left obvious carrier-IP-rotation + cookie-theft replay
//     attacks undetected). Now either signal alone is enough — a
//     stolen refresh token replayed from a different network OR a
//     different browser family burns the session.
//   - Session previously marked anomaly_flagged: deny regardless.
//
// `ip` and `userAgent` come from the HTTP handler (gin.ClientIP +
// User-Agent header). Empty inputs are treated as "no signal" — they
// don't trigger a denial but also don't update the stored value.
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

	// A15 + A11: graduated fingerprint check before issuing a new pair.
	// `ipChanged` = different specific address (host-level diff).
	// `subnetChanged` = different /24 (v4) or /48 (v6) — strong signal
	// of a genuinely different network, not just DHCP rotation.
	ipChanged := ip != "" && sess.IP != "" && ip != sess.IP
	subnetChanged := ip != "" && sess.IP != "" && !sameSubnet(sess.IP, ip)
	uaChanged := userAgent != "" && sess.UserAgent != "" && !sameUserAgentFamily(sess.UserAgent, userAgent)
	highRisk := subnetChanged || uaChanged

	if highRisk {
		// Don't issue a new pair. Log + record an anomaly so the user
		// sees it in the security inbox and can change password if it
		// wasn't them. Revoke the session so the stolen refresh token
		// can't be replayed even if the attacker tries again from the
		// original IP/UA — once we suspect compromise we burn the
		// session.
		_ = s.store.RevokeSession(ctx, sess.ID)
		_ = s.store.RecordLoginAnomaly(ctx, sess.UserID, "session_revoked",
			ip, userAgent, sess.DeviceID, "", 90, true, map[string]any{
				"reason":        "refresh_fingerprint_mismatch",
				"original_ip":   sess.IP,
				"original_ua":   sess.UserAgent,
				"presented_ip":  ip,
				"presented_ua":  userAgent,
				"session_id":    sess.ID.String(),
			})
		slog.Warn("auth: refresh denied — fingerprint mismatch",
			"user_id", sess.UserID, "session_id", sess.ID,
			"original_ip", sess.IP, "presented_ip", ip)
		return nil, errors.New("refresh denied — please sign in again")
	}

	if sess.IP != "" && sess.AnomalyFlagged() {
		_ = s.store.RevokeSession(ctx, sess.ID)
		return nil, errors.New("session revoked — please sign in again")
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
	if err := s.store.RotateSessionWithFingerprint(ctx, sess.ID, hashToken(newRefreshToken), ip, newExpiresAt, false); err != nil {
		return nil, err
	}

	// Low-risk anomaly: specific IP changed but stayed within the same
	// /24, UA family unchanged. Common with DHCP rotation. Record so
	// the security inbox can surface "signed in from new location" but
	// don't block the refresh.
	if ipChanged && !subnetChanged && !uaChanged {
		_ = s.store.RecordLoginAnomaly(ctx, sess.UserID, "new_ip",
			ip, userAgent, sess.DeviceID, "", 40, false, map[string]any{
				"original_ip":  sess.IP,
				"session_id":   sess.ID.String(),
			})
	}

	accessToken, err := s.generateAccessToken(user.ID, sess.ID)
	if err != nil {
		return nil, err
	}

	// A10: invalidate the session-by-id cache entry for this session
	// since we've just rotated its refresh-token expiry. The handler
	// layer reads through Redis for /me lookups; let's not serve stale.
	if s.rdb != nil {
		_ = s.rdb.Del(ctx, "sess:"+sess.ID.String()).Err()
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

// sameUserAgentFamily compares the lead "product/version" token of two
// User-Agent strings — switching from Mozilla 119 → 120 is the same
// family; switching from Mozilla → Dalvik (mobile WebView) is not.
// Empty inputs match anything (no signal).
func sameUserAgentFamily(a, b string) bool {
	if a == "" || b == "" {
		return true
	}
	pa := uaProduct(a)
	pb := uaProduct(b)
	return pa == pb
}

func uaProduct(ua string) string {
	for i, r := range ua {
		if r == '/' || r == ' ' {
			return ua[:i]
		}
	}
	return ua
}

// sameSubnet returns true when a and b are within the same broad network
// block: /24 for IPv4 and /48 for IPv6. The goal is to distinguish "DHCP
// reassigned within the same NAT pool / ISP block" (benign) from "actually
// hopped to a different network" (suspect). Returns true when either side
// is empty or unparseable so we don't false-positive on missing telemetry.
func sameSubnet(a, b string) bool {
	if a == "" || b == "" || a == b {
		return true
	}
	ipA := net.ParseIP(a)
	ipB := net.ParseIP(b)
	if ipA == nil || ipB == nil {
		return true
	}
	if v4a, v4b := ipA.To4(), ipB.To4(); v4a != nil && v4b != nil {
		// /24 — match first 3 octets.
		return v4a[0] == v4b[0] && v4a[1] == v4b[1] && v4a[2] == v4b[2]
	}
	v6a, v6b := ipA.To16(), ipB.To16()
	if v6a == nil || v6b == nil {
		return true
	}
	// /48 — match first 6 bytes.
	for i := 0; i < 6; i++ {
		if v6a[i] != v6b[i] {
			return false
		}
	}
	return true
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
	if err := s.store.RevokeSession(ctx, sessionID); err != nil {
		return err
	}
	s.cacheRevoke(ctx, sessionID)
	return nil
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
	if err := s.store.RevokeSession(ctx, sess.ID); err != nil {
		return err
	}
	s.cacheRevoke(ctx, sess.ID)
	return nil
}

// LogoutAll revokes all sessions for a user.
func (s *Service) LogoutAll(ctx context.Context, userID uuid.UUID) (int64, error) {
	sessions, _ := s.store.ListActiveSessions(ctx, userID)
	n, err := s.store.RevokeAllSessions(ctx, userID)
	if err != nil {
		return n, err
	}
	for _, sess := range sessions {
		s.cacheRevoke(ctx, sess.ID)
	}
	return n, nil
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
	if err := s.store.RevokeSession(ctx, sessionID); err != nil {
		return err
	}
	s.cacheRevoke(ctx, sessionID)
	return nil
}

// cacheRevoke marks a session id as revoked in Redis so the JWT
// middleware can short-circuit access tokens that haven't expired yet.
// TTL = access-token TTL + a small grace; once the access token would
// have expired naturally there's no point keeping the entry.
//
// Best-effort: a Redis failure logs WARN but doesn't fail the revoke —
// the DB write is the source of truth; cache is a hot-path
// optimization.
func (s *Service) cacheRevoke(ctx context.Context, sessionID uuid.UUID) {
	if s.rdb == nil {
		return
	}
	ttl := s.cfg.AccessTokenTTL + 60*time.Second
	if err := s.rdb.Set(ctx, "sess_revoked:"+sessionID.String(), "1", ttl).Err(); err != nil {
		s.log.Warn("session revoke: cache set failed", "session_id", sessionID, "err", err)
	}
}

// DeleteAccount soft-deletes a user account with 30-day grace period.
//
// Audit A14: previously revoked sessions AFTER the soft-delete flip and
// silently dropped the revoke error. A late attacker holding a stolen
// access token (15-min TTL window) could keep using it post-deletion
// because the access-token check doesn't re-fetch user state on every
// call — only refresh does. Now we revoke first so the refresh window
// closes immediately, and surface the revoke error so we don't leave
// the user in a half-deleted state.
func (s *Service) DeleteAccount(ctx context.Context, userID uuid.UUID) error {
	if revoked, err := s.store.RevokeAllSessions(ctx, userID); err != nil {
		return fmt.Errorf("account deletion: failed to revoke sessions: %w", err)
	} else {
		s.log.Info("account deletion: revoked sessions", "user_id", userID, "revoked", revoked)
	}
	if err := s.store.SoftDeleteUser(ctx, userID); err != nil {
		return fmt.Errorf("failed to soft delete user: %w", err)
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

// GetUserContact returns the user record for internal service-to-service lookups.
// Used by commerce, notification, etc. to resolve email/phone.
func (s *Service) GetUserContact(ctx context.Context, userID uuid.UUID) (*store.User, error) {
	return s.store.GetUserByID(ctx, userID)
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
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.cfg.BcryptCost)
	if err != nil {
		return err
	}

	if err := s.store.UpdatePassword(ctx, user.ID, string(hash)); err != nil {
		return err
	}

	// A16: wipe any in-flight pending-2FA sessions for this user. An
	// attacker that already cleared step 1 (password) and is sitting on
	// a pending_token in the 5-min Redis TTL must not be allowed to
	// complete step 2 with credentials we just rotated. Best-effort —
	// runs before session revoke so that even a Redis error here still
	// gets the session-revoke attempt below.
	s.InvalidatePending2FASessions(ctx, user.ID)

	// Revoke all existing sessions for security. Audit A4: previously
	// the error was silently dropped (`s.store.RevokeAllSessions(...)`
	// with no error capture), so a Postgres blip during the revoke
	// would leave the attacker's session alive even after the user
	// successfully reset their password — defeating the whole purpose
	// of the reset. Now we surface the error: the password is already
	// new (the attacker's stale token still won't pass refresh, since
	// refresh checks revoked_at), but ops sees the failure and the
	// caller knows to retry the revocation.
	if revoked, err := s.store.RevokeAllSessions(ctx, user.ID); err != nil {
		s.log.Error("password reset: failed to revoke sessions — user should be advised to log out everywhere",
			"err", err, "user_id", user.ID)
		return fmt.Errorf("password updated but session revocation failed: %w", err)
	} else {
		s.log.Info("password reset: revoked active sessions", "user_id", user.ID, "revoked", revoked)
	}

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
	// Audit A6: enforce 2FA at every full-session entry point, not just
	// the password-login path. Previously OAuth (Google/Apple) called
	// this directly and skipped the TwoFactorEnabled check that
	// auth.go:213 enforces for password logins — a 2FA-enabled user
	// could still sign in with only a stolen OAuth refresh token.
	// Returning a pending 2FA session here funnels every login flow
	// through the same second-factor gate.
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

// detectLoginAnomaly is the post-login enforcement point. Checks a
// Redis-cached last-IP entry; on a new IP or new device persists a
// row in auth.login_anomalies (durable audit log + powers the "where
// you've signed in from" security inbox) AND publishes a Kafka event
// so notification-service can fan out a "new sign-in" push.
//
// Industry-standard split:
//   - Persist for audit + user-visible history.
//   - Emit Kafka event for downstream services (notifications, fraud
//     scoring, ops alerting).
//   - Cache the latest IP in Redis so this hot-path check stays sub-ms
//     even at billions-of-users scale.
func (s *Service) detectLoginAnomaly(ctx context.Context, userID, ip, deviceID, platform, userAgent string) {
	lastIPKey := fmt.Sprintf("last_ip:%s", userID)
	lastIP, _ := s.rdb.Get(ctx, lastIPKey).Result()
	isNewIP := lastIP != "" && lastIP != ip
	// New device = the device_id wasn't in the trusted devices set.
	// Cheap because trusted_devices is keyed (user_id, fingerprint).
	isNewDevice := false
	if deviceID != "" {
		// Best-effort: don't block on a transient DB blip.
		if uid, err := uuid.Parse(userID); err == nil {
			devices, derr := s.store.ListTrustedDevices(ctx, uid)
			if derr == nil {
				seen := false
				for _, d := range devices {
					if d.DeviceFingerprint == deviceID {
						seen = true
						break
					}
				}
				isNewDevice = !seen
			}
		}
	}

	// Store new last IP (30-day TTL)
	s.rdb.Set(ctx, lastIPKey, ip, 30*24*time.Hour)

	if !(isNewIP || isNewDevice) {
		return
	}

	// Persist for audit + the in-app security inbox.
	if uid, err := uuid.Parse(userID); err == nil {
		anomalyType := "new_ip"
		risk := 30
		if isNewDevice {
			anomalyType = "new_device"
			risk = 50
		}
		_ = s.store.RecordLoginAnomaly(ctx, uid, anomalyType, ip, userAgent, deviceID, "", risk, false, map[string]any{
			"platform":  platform,
			"prior_ip":  lastIP,
		})
	}

	// Emit Kafka so notification-service can push "new sign-in" alert.
	payload := map[string]interface{}{
		"user_id":       userID,
		"ip":            ip,
		"device_id":     deviceID,
		"platform":      platform,
		"is_new_ip":     isNewIP,
		"is_new_device": isNewDevice,
		"occurred_at":   time.Now(),
	}
	if payloadBytes, err := json.Marshal(payload); err == nil {
		_ = s.producer.PublishRaw(ctx, "user.login_anomaly", userID, json.RawMessage(payloadBytes))
	}
}

// ListMyAnomalies powers the user-facing security inbox. Caller is
// resolved from the JWT (handler layer).
func (s *Service) ListMyAnomalies(ctx context.Context, userID uuid.UUID, limit int) ([]store.LoginAnomaly, error) {
	return s.store.ListLoginAnomalies(ctx, userID, limit)
}

// AcknowledgeMyAnomaly lets the user clear an entry from the inbox.
// Idempotent: a second call on an already-acknowledged row no-ops.
func (s *Service) AcknowledgeMyAnomaly(ctx context.Context, userID, anomalyID uuid.UUID) error {
	_, err := s.store.AcknowledgeAnomaly(ctx, userID, anomalyID)
	return err
}
