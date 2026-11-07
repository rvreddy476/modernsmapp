// Core of the auth service: the Service type and its constructor, the Store,
// Producer and EmailSender dependencies it is built from, the token and auth
// response shapes returned to handlers, and the small shared primitives
// (OTP generation, opaque refresh tokens and their hashes, the access-token
// minter configuration, bcryptCompare) used by every flow. The flows
// themselves live in registration.go, login.go, sessions.go,
// password_reset.go, verification.go, data_export.go and login_anomaly.go.

package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/atpost/identity-auth-service/pkg/accesstoken"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/store"
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
	// accessSigningKey, when non-nil, switches access-token signing to RS256.
	// Loaded from cfg.AccessTokenPrivateKeyPEM at construction; nil → HS256.
	accessSigningKey *rsa.PrivateKey
	// SR-6: email delivery. Before this existed, verification and
	// password-reset codes were generated, stored, and sent NOWHERE — account
	// recovery silently did not work. Nil means unconfigured, and the callers
	// return an error rather than reporting a send that did not happen.
	email EmailSender
	// throttle enforces abuse limits keyed on server-resolved identities.
	// See throttle.go — the HTTP middleware cannot do this because it runs
	// before the verification token is exchanged for a user id.
	throttle Throttle
	// Google ID-token signing keys for the mobile OAuth route
	// (oauth_idtoken.go). Created on first use; tests install their own.
	googleKeysOnce sync.Once
	googleKeySet   *googleKeySet
	// refreshReuseGrace is RefreshReuseGrace; a field so a test can pin the
	// no-grace behaviour of the rotation race it exercises.
	refreshReuseGrace time.Duration
	// comparePassword is bcrypt.CompareHashAndPassword; a field so a test can
	// observe that every login path pays for one comparison.
	comparePassword func(hash, password []byte) error
	// dummyPasswordHash is compared against when there is no real hash, so an
	// unknown account takes as long to refuse as a wrong password.
	dummyPasswordHashOnce sync.Once
	dummyPasswordHash     []byte
}

// SetThrottle installs the abuse limiter. Separate from New so tests can
// inject a deterministic fake without widening the constructor.
func (s *Service) SetThrottle(t Throttle) { s.throttle = t }

// EmailSender delivers a security email. Mirrors internal/email.Sender so the
// service package does not depend on the AWS SDK.
type EmailSender interface {
	Send(ctx context.Context, msg EmailMessage) error
}

// EmailMessage is one outbound message.
type EmailMessage struct {
	To       string
	Subject  string
	TextBody string
}

// WithEmailSender wires delivery. Without it, ForgotPassword and email
// verification report failure instead of pretending to have sent something.
func (s *Service) WithEmailSender(sender EmailSender) *Service {
	s.email = sender
	return s
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
	// Account control — deactivate / delete-with-window / purge rescue.
	// Each pairs the row change with its outbox event in one transaction.
	DeactivateUser(ctx context.Context, userID uuid.UUID) error
	ReactivateUser(ctx context.Context, userID uuid.UUID) (bool, error)
	ScheduleDeletion(ctx context.Context, userID uuid.UUID) (time.Time, error)
	CancelDeletion(ctx context.Context, userID uuid.UUID) (bool, error)
	UpdatePassword(ctx context.Context, userID uuid.UUID, passwordHash string) error
	MarkEmailVerified(ctx context.Context, userID uuid.UUID) error
	MarkPhoneVerified(ctx context.Context, userID uuid.UUID) error
	// RBAC roles
	GrantRole(ctx context.Context, userID, grantedBy uuid.UUID, role string) error
	RevokeRole(ctx context.Context, userID uuid.UUID, role string) error
	RolesForUser(ctx context.Context, userID uuid.UUID) ([]string, error)
	// RoleGrantsForUser returns active platform-wide and app-scoped rows.
	RoleGrantsForUser(ctx context.Context, userID uuid.UUID) ([]store.RoleGrant, error)
	// ChangeRole applies an admin grant/revoke and its audit row atomically.
	ChangeRole(ctx context.Context, ch store.RoleChange, audit store.RoleAudit,
		guard func(holders []store.SuperadminHolder) error) (store.RoleChangeResult, error)
	// Admin sessions (A2): step-up amr, audited force logout, holder counts.
	AddSessionAMR(ctx context.Context, sessionID uuid.UUID, method string) error
	RevokeAllSessionsAudited(ctx context.Context, userID uuid.UUID, audit store.RoleAudit) ([]uuid.UUID, error)
	AdminHolderCandidates(ctx context.Context, envUserIDs []uuid.UUID) ([]store.HolderCandidate, error)
	RecordRoleBootstrap(ctx context.Context, userID uuid.UUID, detail string) (bool, error)
	ListUserRoles(ctx context.Context, userID uuid.UUID) ([]store.UserRole, error)
	InsertAdminAudit(ctx context.Context, actorID, targetID uuid.UUID, action, detail string, allowed bool) error
	// InsertServiceAudit records an action whose actor is a SERVICE, not a
	// person (commerce approving a seller, etc.). Separate method rather than
	// a sentinel uuid on InsertAdminAudit — see store.AdminAuditEntry.
	InsertServiceAudit(ctx context.Context, targetID uuid.UUID, service, action, detail string, allowed bool) error
	ListAdminAudit(ctx context.Context, limit int) ([]store.AdminAuditEntry, error)
	// Admin console Access page reads (store/admin_console.go).
	ListRoleHolders(ctx context.Context, f store.RoleHolderFilter) ([]store.RoleHolder, error)
	ListAdminAuditFiltered(ctx context.Context, f store.AuditFilter) ([]store.AdminAuditEntry, error)
	SearchUsers(ctx context.Context, q string, limit int) ([]store.UserSearchHit, error)
	// Sessions
	CreateSession(ctx context.Context, sess *store.Session) error
	GetSessionByRefreshTokenHash(ctx context.Context, refreshTokenHash string) (*store.Session, error)
	GetSessionByID(ctx context.Context, sessionID uuid.UUID) (*store.Session, error)
	ListActiveSessions(ctx context.Context, userID uuid.UUID) ([]store.Session, error)
	// RotateSessionWithFingerprint rotates ONLY while the row still holds
	// oldHash and is live; rotated=false means a concurrent refresh or a
	// logout won, and nothing may be issued.
	RotateSessionWithFingerprint(ctx context.Context, sessionID uuid.UUID, oldHash, newHash, ip string, expiresAt time.Time, anomalyFlagged bool) (bool, error)
	// RotateSessionOnRetry is the same conditional rotation for a grace-window
	// retry; it marks the replaced, never-delivered token in the history.
	RotateSessionOnRetry(ctx context.Context, sessionID uuid.UUID, lostHash, newHash, ip string, expiresAt time.Time) (bool, error)
	// Refresh-token reuse detection (sessions_rotation.go).
	LookupRotatedRefreshToken(ctx context.Context, refreshTokenHash string) (*store.RotatedRefreshToken, error)
	RevokeSessionFamily(ctx context.Context, sessionID uuid.UUID) ([]uuid.UUID, error)
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
	// A5: pre-creation flow variant — accepts explicit verification
	// flags so OAuth callers can avoid stamping email_verified=true
	// when the provider didn't actually assert it, and stamp
	// phone_verified=true once the SMS-OTP step has passed.
	CreateUserWithOAuthExtendedTx(ctx context.Context, tx pgx.Tx, provider, email, name, phone string, emailVerified, phoneVerified bool) (*store.User, error)
	LinkOAuthProvider(ctx context.Context, userID uuid.UUID, provider string) error
	// Cross-schema transactional inserts
	CreateUserRecordTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error
	// LB-5: pending activation, versioned consent, and atomic recovery.
	SetAccountPendingTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID) error
	RecordConsentTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, c store.RegistrationConsent) error
	ActivateVerifiedAccount(ctx context.Context, userID uuid.UUID) error
	IsAccountPendingVerification(ctx context.Context, userID uuid.UUID) (bool, error)
	// maxAttempts: wrong codes are counted under the row lock and the code is
	// deleted at the cap (store.ConsumeRecoveryAndSetPassword).
	ConsumeRecoveryAndSetPassword(ctx context.Context, userID uuid.UUID, otpKey, purpose string, verify func(string) bool, newPasswordHash string, maxAttempts int) error
	// CLB-3: the credential that lets a PENDING account finish signing up.
	// Verify and resend are public routes, so the account they act on must be
	// named by something the server issued rather than by the caller.
	CreateVerificationTransaction(ctx context.Context, userID uuid.UUID, purpose string, ttl time.Duration) (*store.VerificationTransaction, error)
	LookupVerificationTransaction(ctx context.Context, token, purpose string) (uuid.UUID, error)
	ConsumeVerificationTransaction(ctx context.Context, token, purpose string) (uuid.UUID, error)
	CreateProfileTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, displayName, firstName, lastName, dob, gender string) error
	AssignUsernameTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, candidates []string) (string, error)

	// Durable verification-email delivery (migration 016). Enqueued inside the
	// registration transaction so the account and the obligation to contact
	// its owner commit together.
	EnqueueEmailJobTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, purpose string) error
	MarkUserEmailJobsSent(ctx context.Context, userID uuid.UUID) error
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
	svc := &Service{
		store:                store,
		producer:             producer,
		cfg:                  cfg,
		log:                  logger,
		rdb:                  rdb,
		miniAppSessionSigner: miniAppSessionSigner,
		throttle:             NewRedisThrottle(rdb),
		refreshReuseGrace:    RefreshReuseGrace,
		comparePassword:      bcrypt.CompareHashAndPassword,
	}
	// Opt-in RS256 access-token signing. Reuses the same PEM parser as the
	// mini-app signer. A parse failure is fatal: misconfiguring the signing key
	// must not silently fall back to HS256 in a deploy that intended RS256.
	if pem := strings.TrimSpace(cfg.AccessTokenPrivateKeyPEM); pem != "" {
		key, err := loadMiniAppPrivateKey(pem)
		if err != nil {
			logger.Error("failed to load JWT_PRIVATE_KEY_PEM for RS256 access tokens", "err", err)
			panic(fmt.Sprintf("invalid JWT_PRIVATE_KEY_PEM: %v", err))
		}
		svc.accessSigningKey = key
		logger.Info("RS256 access-token signing enabled", "kid", cfg.AccessTokenRS256KID)
	}
	return svc
}

// AccessTokenPublicKey returns the RSA public key for verifying RS256 access
// tokens this service mints, or nil when signing is HS256. main wires this into
// the auth middleware so the service's own protected endpoints accept its tokens.
func (s *Service) AccessTokenPublicKey() *rsa.PublicKey {
	if s.accessSigningKey == nil {
		return nil
	}
	return &s.accessSigningKey.PublicKey
}

type TokenPair struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type AuthResponse struct {
	LocalAdminPasswordLogin bool        `json:"-"`
	Tokens                  TokenPair   `json:"tokens"`
	User                    *store.User `json:"user"`
	SessionID               uuid.UUID   `json:"session_id"`
	Requires2FA             bool        `json:"requires_2fa,omitempty"`
	PendingToken            string      `json:"pending_token,omitempty"`
	// A13 — anomaly step-up envelope. When RequiresStepUp is true the
	// password / OTP check has passed but the risk band demanded a
	// second channel; the UI must POST to /v1/auth/anomaly/verify-* to
	// finish the login. StepUpMethods enumerates the channels the
	// caller can offer (e.g. ["email_otp","totp"]).
	RequiresStepUp bool     `json:"requires_step_up,omitempty"`
	StepUpMethods  []string `json:"step_up_methods,omitempty"`
	// LB-5: registration no longer returns a session. The account is created
	// PENDING and a verification email is sent; the client must complete
	// verification before it has any credential. An unverified address must
	// never be a usable identity.
	RequiresVerification bool `json:"requires_verification,omitempty"`
	// CLB-3: the credential that lets a pending account finish signing up.
	//
	// This is NOT a session. It authorises submitting or re-requesting a
	// verification code for ONE account, and nothing else. Without it the
	// pending state LB-5 introduced had no exit: verify and resend both
	// required a verified bearer session, which a pending account by
	// definition does not have.
	VerificationToken     string     `json:"verification_token,omitempty"`
	VerificationExpiresAt *time.Time `json:"verification_expires_at,omitempty"`
}

type AccessClaims struct {
	jwt.RegisteredClaims
	SessionID string `json:"sid"`
	// Scopes is a space-separated authorization scope list resolved from the
	// server-side allowlist at mint time (e.g. "admin moderator"). Empty for
	// ordinary users. The gateway reads this claim — NOT a client header — to
	// authorize admin/internal surfaces.
	Scopes string `json:"scopes,omitempty"`
	// TokenType distinguishes an access token from a refresh token.
	//
	// Module 3 SR-1: the gateway rejects any token whose type is not an access
	// token, because a refresh credential presented as a bearer token must not
	// authenticate an API call. That check is only meaningful if the mint side
	// actually stamps the claim — an absent `typ` is indistinguishable from a
	// refresh token that omits it.
	TokenType string `json:"typ"`
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

// Access tokens are minted ONLY by mintAccessToken (admin_session.go).
//
// Module 3 LB-1 — the claim set is a CONTRACT with the API gateway's verifier,
// and it lives in pkg/accesstoken so the contract can be tested against BOTH
// real implementations without either deployable service depending on the
// other. Scopes and the session claims are resolved server-side (env
// allowlist ∪ DB roles, the session row); a client can never influence them.

// accessTokenConfig maps service configuration onto the minter.
func (s *Service) accessTokenConfig() accesstoken.Config {
	return accesstoken.Config{
		Issuer:      s.cfg.JWTIssuer,
		Audience:    s.cfg.JWTAudience,
		TTL:         s.cfg.AccessTokenTTL,
		RS256KID:    s.cfg.AccessTokenRS256KID,
		HS256KID:    s.cfg.JWTKID,
		HS256Secret: s.cfg.JWTSecret,
	}
}

// AccessTokenConfigForContract exposes the exact configuration mapping the
// production mint path uses, so the CI-only edge-auth contract module can
// prove the real claim set rather than a restatement of it.
func (s *Service) AccessTokenConfigForContract() accesstoken.Config {
	return s.accessTokenConfig()
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

// bcryptCompare is a thin shim used by anomaly_stepup.go so it doesn't
// have to import golang.org/x/crypto/bcrypt directly — keeps the file
// focused on the policy logic. Returns the underlying bcrypt error on
// mismatch (or any other failure mode).
func bcryptCompare(hash, plain string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
}
