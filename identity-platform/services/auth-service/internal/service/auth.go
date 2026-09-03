package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/atpost/identity-auth-service/pkg/accesstoken"
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
	ListUserRoles(ctx context.Context, userID uuid.UUID) ([]store.UserRole, error)
	InsertAdminAudit(ctx context.Context, actorID, targetID uuid.UUID, action, detail string, allowed bool) error
	ListAdminAudit(ctx context.Context, limit int) ([]store.AdminAuditEntry, error)
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
	ConsumeRecoveryAndSetPassword(ctx context.Context, userID uuid.UUID, otpKey, purpose string, verify func(string) bool, newPasswordHash string) error
	// CLB-3: the credential that lets a PENDING account finish signing up.
	// Verify and resend are public routes, so the account they act on must be
	// named by something the server issued rather than by the caller.
	CreateVerificationTransaction(ctx context.Context, userID uuid.UUID, purpose string, ttl time.Duration) (*store.VerificationTransaction, error)
	LookupVerificationTransaction(ctx context.Context, token, purpose string) (uuid.UUID, error)
	ConsumeVerificationTransaction(ctx context.Context, token, purpose string) (uuid.UUID, error)
	CreateProfileTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, displayName, firstName, lastName, dob, gender string) error

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
	Tokens       TokenPair   `json:"tokens"`
	User         *store.User `json:"user"`
	SessionID    uuid.UUID   `json:"session_id"`
	Requires2FA  bool        `json:"requires_2fa,omitempty"`
	PendingToken string      `json:"pending_token,omitempty"`
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

	if created {
		s.log.Info("user registered via OTP", "user_id", user.ID)
	}

	// Route through createSessionForUser so the 2FA gate AND the A13
	// anomaly gate are applied in one place. The step-up sentinel is
	// re-wrapped as an AuthResponse with RequiresStepUp set so the
	// handler can render it.
	resp, err := s.createSessionForUser(ctx, user, deviceID, platform, ip, userAgent)
	if err != nil {
		if errors.Is(err, ErrAnomalyStepUpRequired) {
			// Surface the envelope without the sentinel — handler will
			// inspect RequiresStepUp to format the 401.
			return resp, err
		}
		return nil, err
	}

	// Publish the user-logged-in event only for the success path
	// (no pending 2FA / step-up). The TokenPair is zero-value when a
	// pending response is returned, so checking AccessToken keeps the
	// publish on the real-session case.
	if resp != nil && resp.Tokens.AccessToken != "" {
		if pErr := s.producer.PublishUserLoggedIn(ctx, user.ID, resp.SessionID, deviceID, platform, ip); pErr != nil {
			s.log.Warn("failed to publish user logged in event", "err", pErr, "user_id", user.ID, "session_id", resp.SessionID)
		}
	}

	return resp, nil
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

// RegisterWithPassword delegates to the consent-aware launch registration
// path below; the old optional 13+ helper has been removed.
func (s *Service) RegisterWithPassword(ctx context.Context, phone, email, password, firstName, lastName, dob, gender string) (*AuthResponse, error) {
	return s.RegisterWithConsent(ctx, phone, email, password, firstName, lastName, dob, gender,
		RegistrationConsent{})
}

// RegisterWithConsent is the registration entry point.
//
// SR-6: the age gate was 13 AND was skipped entirely when no date of birth was
// supplied — and `dob` was an optional request field, so any client could
// bypass it by omission. There was no consent capture at all. Both are now
// mandatory and enforced here, at the service layer, so a second handler
// cannot reintroduce the bypass.
func (s *Service) RegisterWithConsent(ctx context.Context, phone, email, password, firstName, lastName, dob, gender string, consent RegistrationConsent) (*AuthResponse, error) {
	if err := validatePassword(password); err != nil {
		return nil, err
	}
	if err := CheckRegistrationEligibility(dob, consent, time.Now()); err != nil {
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

	// LB-5: the account is created PENDING, and the versioned consent is
	// recorded, both inside this transaction.
	//
	// Previously the account was created ACTIVE and this function returned
	// access and refresh tokens immediately, with no verification challenge
	// sent — so someone else's email address became a working account and the
	// real owner was never contacted. The accepted terms version was checked
	// in memory and discarded, leaving no record of what anyone agreed to.
	if err := s.store.SetAccountPendingTx(ctx, tx, user.ID); err != nil {
		return nil, fmt.Errorf("failed to mark account pending: %w", err)
	}
	var declaredDOB *time.Time
	if parsed, perr := ParseDOB(dob, time.Now()); perr == nil {
		declaredDOB = &parsed
	}
	if err := s.store.RecordConsentTx(ctx, tx, user.ID, store.RegistrationConsent{
		TermsVersion:    consent.Version,
		AcceptedTerms:   consent.Accepted,
		AcceptedPrivacy: consent.Accepted,
		DeclaredDOB:     declaredDOB,
	}); err != nil {
		return nil, fmt.Errorf("failed to record consent: %w", err)
	}

	// Enqueue the verification email INSIDE the transaction.
	//
	// This is the durability half of the fix: the account row and the
	// obligation to contact its owner commit together. Either both exist or
	// neither does, so there is no window in which an account is created that
	// nobody will ever be told about.
	if err := s.store.EnqueueEmailJobTx(ctx, tx, user.ID, store.EmailJobPurposeVerify); err != nil {
		return nil, fmt.Errorf("failed to enqueue verification email: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Fast path: try to send immediately so the common case is instant.
	//
	// A failure here is NOT fatal any more. It used to return an error, which
	// meant a mail-provider blip produced a committed account whose owner saw
	// "registration failed" — with nothing that would ever retry. The job is
	// already durably queued, so the relay owns delivery from here and the
	// registration is reported honestly as the success it is.
	if err := s.RequestEmailVerification(ctx, user.ID); err != nil {
		s.log.Warn("registration: immediate verification send failed — relay will retry",
			"event", "email_immediate_send_failed",
			"user_id", user.ID, "err", err)
	} else if err := s.store.MarkUserEmailJobsSent(ctx, user.ID); err != nil {
		// Worst case the relay sends a second code. A duplicate email is a
		// far smaller failure than a silently undelivered one.
		s.log.Warn("verification sent but job not marked — relay may resend",
			"event", "email_job_mark_failed", "user_id", user.ID, "err", err)
	}

	// CLB-3: issue the verification transaction.
	//
	// The code goes to the mailbox; this goes to the client. Neither alone
	// activates anything — the client must present both, and the credential
	// names which account without the client being able to choose it. Before
	// this, the code had nowhere to be submitted: verify and resend sat behind
	// the auth middleware and derived the user from X-User-Id, which only a
	// verified session produces.
	vt, err := s.store.CreateVerificationTransaction(ctx, user.ID,
		store.VerificationPurposeEmail, store.VerificationTransactionTTL)
	if err != nil {
		s.log.Error("registration: verification transaction not issued", "err", err, "user_id", user.ID)
		return nil, fmt.Errorf("account created but verification could not be started: %w", err)
	}

	// No tokens. The caller must verify first — that is the whole point of a
	// pending account. Returning a session here would mean an unverified
	// address is a usable identity.
	return &AuthResponse{
		User:                  user,
		RequiresVerification:  true,
		VerificationToken:     vt.Token,
		VerificationExpiresAt: &vt.ExpiresAt,
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

	// LB-5: an unverified account must not be able to log in.
	//
	// Registration creates the account PENDING and issues no session. Without
	// this check the pending state would be decorative: a caller could simply
	// register and then log in, and an unverified email address would still be
	// a usable identity.
	//
	// CLB-3 — THE PASSWORD IS CHECKED FIRST, AND THAT IS THE SECURE ORDER.
	//
	// This check used to run BEFORE the password comparison, on the reasoning
	// that it avoided leaking whether the password was right. It leaked more
	// than it saved: anyone could learn "this address has a pending account
	// here" by submitting any password at all, and — worse — a real user who
	// closed the app after registering had NO way back. The verification
	// transaction was gone with the process, and resend needed a session they
	// could never get.
	//
	// With the password checked first, a wrong password is the same generic
	// "invalid credentials" whether the account is pending, active, or absent,
	// and a user who proves they own the account gets a fresh verification
	// transaction instead of a dead end. No session is issued either way.
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, errors.New("invalid credentials")
	}

	if pending, perr := s.store.IsAccountPendingVerification(ctx, user.ID); perr != nil {
		return nil, fmt.Errorf("check account status: %w", perr)
	} else if pending {
		vt, verr := s.store.CreateVerificationTransaction(ctx, user.ID,
			store.VerificationPurposeEmail, store.VerificationTransactionTTL)
		if verr != nil {
			s.log.Error("pending login: verification transaction not issued",
				"err", verr, "user_id", user.ID)
			return nil, ErrEmailNotVerified
		}
		// A resumption path, not a session: no access token, no refresh token,
		// no session row.
		return &AuthResponse{
			User:                  user,
			RequiresVerification:  true,
			VerificationToken:     vt.Token,
			VerificationExpiresAt: &vt.ExpiresAt,
		}, ErrEmailNotVerified
	}

	// Account-control state machine (deactivate / delete / purge). Runs
	// AFTER the verification gate and the password check, so only the
	// account's owner can trigger a transition, and a wrong password stays
	// the same generic "invalid credentials" regardless of lifecycle state.
	//
	//	deactivated                     → reactivate, continue login
	//	pending_deletion, window open   → cancel deletion, continue login
	//	pending_deletion, window closed → ErrAccountPendingPurge (403)
	//	purged                          → ErrAccountPurged (403)
	if err := s.applyLoginLifecycle(ctx, user); err != nil {
		return nil, err
	}

	// Route through createSessionForUser so both the 2FA gate (A6) and
	// the A13 anomaly gate are applied centrally. The step-up sentinel
	// is forwarded to the handler so the 401-with-body can render.
	resp, err := s.createSessionForUser(ctx, user, deviceID, platform, ip, userAgent)
	if err != nil {
		if errors.Is(err, ErrAnomalyStepUpRequired) {
			return resp, err
		}
		return nil, err
	}

	// Side effects only run on the real-session branch (TokenPair
	// populated). 2FA-pending and step-up-pending responses skip these
	// so the user-logged-in event doesn't fire before the session
	// actually exists.
	if resp != nil && resp.Tokens.AccessToken != "" {
		if uErr := s.store.UpdateLastLogin(ctx, user.ID); uErr != nil {
			s.log.Warn("failed to update last_login_at", "err", uErr, "user_id", user.ID)
		}
		if pErr := s.producer.PublishUserLoggedIn(ctx, user.ID, resp.SessionID, deviceID, platform, ip); pErr != nil {
			s.log.Warn("failed to publish user logged in event", "err", pErr, "user_id", user.ID, "session_id", resp.SessionID)
		}
	}

	return resp, nil
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
//   - Different /24 (or /48), same UA family: MEDIUM risk. Rotate, and
//     record an anomaly the user can see in the security inbox. This is
//     NOT a denial: a phone moves between Wi-Fi and cellular, and between
//     IPv4 and IPv6 egress, many times a day. Once the gateway forwards
//     real client addresses (TRUSTED_PROXIES), treating a network change
//     as theft signed every mobile user out within hours — observed on
//     the dev tunnel the same day it was enabled. A11's "either signal
//     alone burns the session" is therefore narrowed to the UA signal.
//   - Different UA family: HIGH risk. A refresh token replayed from a
//     different client is the theft shape worth burning the session for.
//     Refresh is denied and the session revoked.
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
	highRisk := uaChanged

	if subnetChanged && !highRisk {
		// A different network on the same client: normal for a phone.
		// Rotate as usual, but leave a visible, non-flagging record.
		_ = s.store.RecordLoginAnomaly(ctx, sess.UserID, "new_ip",
			ip, userAgent, sess.DeviceID, "", 40, false, map[string]any{
				"reason":       "refresh_network_changed",
				"original_ip":  sess.IP,
				"presented_ip": ip,
				"session_id":   sess.ID.String(),
			})
	}

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
				"reason":       "refresh_fingerprint_mismatch",
				"original_ip":  sess.IP,
				"original_ua":  sess.UserAgent,
				"presented_ip": ip,
				"presented_ua": userAgent,
				"session_id":   sess.ID.String(),
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

	// Account-control guard. Deactivate/delete/purge all revoke every
	// session, so a live refresh token for such an account should be
	// impossible — but the account_status check is asserted here anyway so
	// a missed revocation (or a session created in a race with the flip)
	// cannot quietly resurrect a deactivated, deleting, suspended or purged
	// account. Reactivation happens ONLY through a full password login.
	if user.AccountStatus != store.AccountStatusActive {
		_ = s.store.RevokeSession(ctx, sess.ID)
		s.cacheRevoke(ctx, sess.ID)
		return nil, fmt.Errorf("refresh denied: account status is %q — please sign in again", user.AccountStatus)
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
				"original_ip": sess.IP,
				"session_id":  sess.ID.String(),
			})
	}

	accessToken, err := s.generateAccessToken(ctx, user.ID, sess.ID)
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

func (s *Service) generateAccessToken(ctx context.Context, userID, sessionID uuid.UUID) (string, error) {
	// Module 3 LB-1 — the claim set is a CONTRACT with the API gateway's
	// verifier, and it now lives in pkg/accesstoken so the contract can be
	// tested against BOTH real implementations without either deployable
	// service depending on the other. See that package's doc comment.
	//
	// Scopes are resolved here, server-side (env allowlist ∪ DB roles),
	// because that requires the store. A client can never influence them —
	// they are bound to the user id inside the signature.
	return accesstoken.Mint(
		s.accessTokenConfig(),
		s.accessSigningKey,
		userID,
		sessionID,
		s.resolveScopes(ctx, userID),
		time.Now(),
	)
}

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

// DeleteAccount / DeactivateAccount live in account_lifecycle.go.

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

	// SR-6: recovery is EMAIL ONLY at launch.
	//
	// This used to prefer the phone as the OTP key and then send nothing at
	// all — there was no delivery mechanism in the service. The result was a
	// "Forgot password" that returned 200 while no code ever arrived: account
	// recovery did not exist. SMS is not wired either, so a phone-keyed code
	// would still be undeliverable.
	if user.Email == nil || strings.TrimSpace(*user.Email) == "" {
		// Same silent return as the unknown-user case: revealing that an
		// account exists but has no email is still an account oracle.
		s.log.Warn("password reset requested for an account with no email address",
			"user_id", user.ID)
		return nil
	}
	otpKey := *user.Email

	if err := s.store.SaveOTP(ctx, otpKey, otp, "password_reset", s.cfg.OTPExpiry); err != nil {
		return fmt.Errorf("save reset code: %w", err)
	}

	if s.email == nil {
		// Report the failure rather than returning success. A caller that
		// treats this as sent tells the user their code is on the way.
		return errors.New("password reset requires email delivery, which is not configured")
	}
	if err := s.email.Send(ctx, EmailMessage{
		To:      otpKey,
		Subject: "Your atPost password reset code",
		TextBody: fmt.Sprintf(
			"Your password reset code is %s.\n\n"+
				"It expires in %d minutes. If you did not request a password reset, "+
				"you can ignore this email — your password has not been changed.\n",
			otp, int(s.cfg.OTPExpiry.Minutes())),
	}); err != nil {
		return fmt.Errorf("send reset code: %w", err)
	}
	s.log.Info("password reset code sent", "user_id", user.ID)
	return nil
}

// ResetPassword consumes the one-time code, sets the new password, and revokes
// every session — atomically.
//
// LB-5, two defects:
//
//  1. THE KEY MISMATCH. ForgotPassword stores the code under the EMAIL
//     (recovery is email-only at launch; SMS does not exist). This function
//     looked up `user.Phone` first and only fell back to email when the phone
//     was empty. So any account with BOTH a phone and an email could never
//     find its own emailed code: recovery was broken for exactly the users
//     most likely to have completed a full profile.
//
//  2. THE ORDERING. The code was deleted BEFORE the new password was
//     validated, so a weak-password rejection left the user with a consumed
//     code and no way to retry. And the password was committed BEFORE
//     sessions were revoked, so a failure in between left the account with a
//     new password and the attacker's session still live — the opposite of
//     what a reset is for.
//
// Everything now happens in one transaction, and the password is validated
// and hashed BEFORE the code is consumed.
func (s *Service) ResetPassword(ctx context.Context, identifier, code, newPassword string) error {
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

	// Validate and hash BEFORE consuming anything. A rejected password must
	// leave the code usable.
	if err := validatePassword(newPassword); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), s.cfg.BcryptCost)
	if err != nil {
		return err
	}

	// Recovery is EMAIL-ONLY, matching ForgotPassword. Keying off the phone
	// here is what broke recovery for every user who had both.
	if user.Email == nil || strings.TrimSpace(*user.Email) == "" {
		return errors.New("invalid credentials")
	}
	otpKey := *user.Email

	// One transaction: consume the code, set the password, revoke sessions.
	err = s.store.ConsumeRecoveryAndSetPassword(
		ctx, user.ID, otpKey, "password_reset",
		func(storedHash string) bool {
			return bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(code)) == nil
		},
		string(hash),
	)
	if err != nil {
		if errors.Is(err, store.ErrRecoveryCodeInvalid) {
			return errors.New("invalid or expired code")
		}
		return err
	}

	// A16: wipe any in-flight pending-2FA sessions. An attacker who already
	// cleared step 1 and is sitting on a pending_token within its Redis TTL
	// must not complete step 2 with credentials that were just rotated.
	//
	// This is deliberately AFTER the transaction and best-effort: it lives in
	// Redis, cannot join a Postgres transaction, and a failure here must not
	// roll back a completed password reset. The durable revocation already
	// committed above, so a stale pending token cannot mint a session that
	// survives refresh.
	s.InvalidatePending2FASessions(ctx, user.ID)

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

	if err := s.store.SaveOTP(ctx, *user.Email, otp, "email_verify", s.cfg.OTPExpiry); err != nil {
		return fmt.Errorf("save verification code: %w", err)
	}

	// LB-5: this SAVED the code and returned. It never sent anything, so no
	// verification email has ever been delivered by this service — and
	// registration created the account active anyway, which is why nobody
	// noticed. Now the account stays pending until this arrives, so a silent
	// failure here would lock the user out permanently. It is reported.
	if s.email == nil {
		return errors.New("email verification requires email delivery, which is not configured")
	}
	if err := s.email.Send(ctx, EmailMessage{
		To:      *user.Email,
		Subject: "Verify your atPost email address",
		TextBody: fmt.Sprintf(
			"Your verification code is %s.\n\n"+
				"It expires in %d minutes. Enter it in the app to finish setting up "+
				"your account.\n\nIf you did not create an atPost account, you can "+
				"ignore this email — the address will not be used.\n",
			otp, int(s.cfg.OTPExpiry.Minutes())),
	}); err != nil {
		return fmt.Errorf("send verification code: %w", err)
	}
	s.log.Info("verification code sent", "user_id", user.ID)
	return nil
}

// VerifyEmail checks the OTP and marks the user's email as verified.
// VerifyEmailWithTransaction is the public verification entry point.
//
// CLB-3: the account is named by the SERVER-ISSUED credential, never by the
// caller. A public route that accepted a user id would let anyone grind codes
// against any account they can name; this way the only accounts an attacker
// can attempt are the ones they were handed a credential for.
//
// Ordering matters and is deliberate:
//
//  1. look the credential up WITHOUT consuming it, so a mistyped code does not
//     burn the user's only way back in;
//  2. check the code, which is where the attempt counter and the OTP expiry
//     do their work;
//  3. consume the credential ATOMICALLY, so a replay of a correct submission
//     cannot re-enter the activation path;
//  4. activate, which is itself one-time.
//
// A wrong purpose, an expired credential, a forged one, or one belonging to
// another user all fail at step 1 with the same error — there is nothing for
// an attacker to learn from the difference.
func (s *Service) VerifyEmailWithTransaction(ctx context.Context, transaction, code string) error {
	userID, err := s.store.LookupVerificationTransaction(ctx, transaction, store.VerificationPurposeEmail)
	if err != nil {
		return err
	}

	if err := s.verifyEmailCode(ctx, userID, code); err != nil {
		return err
	}

	// Spend the credential. If a concurrent request already did, this one
	// matches no row and stops here rather than activating a second time.
	if _, err := s.store.ConsumeVerificationTransaction(ctx, transaction, store.VerificationPurposeEmail); err != nil {
		return err
	}

	return s.activateVerifiedAccount(ctx, userID)
}

// ResendVerificationWithTransaction re-sends the code for the account the
// credential names. It does NOT consume the credential: a user waiting on a
// slow mail provider may legitimately ask more than once, and burning their
// only credential on a resend would recreate the dead end this fixes.
func (s *Service) ResendVerificationWithTransaction(ctx context.Context, transaction string) error {
	userID, err := s.store.LookupVerificationTransaction(ctx, transaction, store.VerificationPurposeEmail)
	if err != nil {
		return err
	}

	// Per-ACCOUNT quota, checked after the token is exchanged for a user id.
	//
	// Without this the only cap on resend was the per-IP limiter, because
	// OTPRateLimit derives its strong caps from a `phone` field that this
	// route does not carry. That left two abuse paths open: flooding a
	// third party's inbox (burning this domain's sending reputation for every
	// user), and denying verification indefinitely — each resend replaces the
	// stored OTP, so an attacker resending faster than the victim reads mail
	// keeps the victim's code permanently stale.
	if err := s.checkResendQuota(ctx, userID); err != nil {
		return err
	}

	return s.RequestEmailVerification(ctx, userID)
}

// VerifyEmail verifies by user id. CLB-3 moved the public entry point to
// VerifyEmailWithTransaction; this remains the internal form, callable only
// where the user id is already established by the server.
func (s *Service) VerifyEmail(ctx context.Context, userID uuid.UUID, code string) error {
	if err := s.verifyEmailCode(ctx, userID, code); err != nil {
		return err
	}
	return s.activateVerifiedAccount(ctx, userID)
}

// verifyEmailCode checks the emailed code and consumes it on success.
func (s *Service) verifyEmailCode(ctx context.Context, userID uuid.UUID, code string) error {
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
	return nil
}

// activateVerifiedAccount performs the one-time pending → active transition.
//
// LB-5: registration creates the account pending and issues no session, so
// this is the transition that makes it usable. ActivateVerifiedAccount only
// promotes a row that is still pending, which is what makes it one-time: a
// replay finds an active account and changes nothing.
func (s *Service) activateVerifiedAccount(ctx context.Context, userID uuid.UUID) error {
	if err := s.store.ActivateVerifiedAccount(ctx, userID); err != nil {
		if errors.Is(err, store.ErrAccountNotPending) {
			// Already active. The code was valid and is now consumed; report
			// success rather than an error the user cannot act on.
			return s.store.MarkEmailVerified(ctx, userID)
		}
		return fmt.Errorf("activate account: %w", err)
	}
	return nil
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

	// A13 — anomaly enforcement. Probe BEFORE minting tokens so we can
	// either (a) gate the session behind step-up when the risk band is
	// high AND enforcement is on, or (b) fall through to the normal
	// path and run shadow side effects. The probe is cheap (one Redis
	// GET + one trusted-devices read); it runs unconditionally so the
	// shadow-mode telemetry continues to be accurate.
	//
	// Sub-policy: suspended accounts are rejected outright (the
	// existing suspended-account gate lives at the GetUserByX layer for
	// password login but not here, so re-assert it). paused / restricted
	// users CAN attempt step-up so they have a path to clear the flag.
	if user.AccountStatus == "suspended" {
		return nil, errors.New("account suspended")
	}

	probe := s.probeLoginAnomaly(ctx, user.ID.String(), ip, deviceID)
	risk := classifyAnomalyRisk(probe.LastIP, ip, probe.IsNewIP, probe.IsNewDevice, probe.UAFamilyChanged)

	if risk == anomalyHigh && s.cfg.LoginAnomalyEnforce == "enforce" {
		// High-risk + enforce mode: refuse to mint tokens. Stash the
		// pending state, dispatch the email-OTP if available, and
		// return a step-up envelope. The handler maps the sentinel to
		// a 401 with body.
		//
		// applyAnomalySideEffects is intentionally NOT called here: we
		// don't want to advance last_ip until the user proves they own
		// the account (otherwise the second login attempt would see
		// the new IP as already-known and bypass the gate).
		resp, err := s.startAnomalyStepUp(ctx, user, deviceID, platform, ip, userAgent)
		if err == nil {
			// startAnomalyStepUp returns (nil, ErrAnomalyStepUpRequired)
			// on success; landing here means an unexpected nil-error
			// success path. Fall through to issue session so we don't
			// strand the user.
			return resp, nil
		}
		// ErrAnomalyStepUpRequired is the expected "happy path" for
		// the enforce branch — return both the envelope and the
		// sentinel so the handler can format the 401-with-body.
		if errors.Is(err, ErrAnomalyStepUpRequired) {
			return resp, err
		}
		// Step-up unavailable (no email_verified, no 2FA) → refuse
		// the login. The user must contact support to recover.
		if errors.Is(err, ErrAnomalyStepUpUnavailable) {
			s.log.Warn("anomaly enforce: no step-up channel; refusing login",
				"user_id", user.ID, "ip", ip)
			return nil, err
		}
		// Any other startAnomalyStepUp failure (Redis down,
		// marshaling, etc.): log and fall through to shadow behaviour
		// rather than locking everybody out. Better to issue the
		// session and rely on the security inbox + push notification
		// than to brick logins on infra hiccups.
		s.log.Error("anomaly enforce: step-up setup failed; falling back to shadow",
			"err", err, "user_id", user.ID)
	}

	// Shadow (or low/medium risk under enforce mode): record the
	// anomaly + emit Kafka if there's anything to report, then mint
	// the session.
	s.applyAnomalySideEffects(ctx, user.ID.String(), ip, deviceID, platform, userAgent, probe)

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

	accessToken, err := s.generateAccessToken(ctx, user.ID, sessionID)
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

// anomalyProbeResult captures everything classifyAnomalyRisk needs to
// make the enforcement call. We return signals rather than executing
// side effects so the caller can decide (based on LOGIN_ANOMALY_ENFORCE)
// whether to issue the session or stash a step-up.
type anomalyProbeResult struct {
	IsNewIP         bool
	IsNewDevice     bool
	LastIP          string
	UAFamilyChanged bool
}

// probeLoginAnomaly runs the cheap "is this novel?" checks WITHOUT
// recording the anomaly row or publishing the Kafka event. Returns
// enough signal for classifyAnomalyRisk to grade the attempt. Side
// effects (RecordLoginAnomaly + Kafka publish + last-IP refresh) are
// deferred to applyAnomalySideEffects so we can suppress the last-IP
// refresh on enforce-blocked logins (otherwise a successful step-up
// would still see the IP as "new" the second time round).
func (s *Service) probeLoginAnomaly(ctx context.Context, userID, ip, deviceID string) anomalyProbeResult {
	lastIPKey := fmt.Sprintf("last_ip:%s", userID)
	lastIP, _ := s.rdb.Get(ctx, lastIPKey).Result()
	isNewIP := lastIP != "" && lastIP != ip

	isNewDevice := false
	if deviceID != "" {
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

	return anomalyProbeResult{
		IsNewIP:     isNewIP,
		IsNewDevice: isNewDevice,
		LastIP:      lastIP,
		// UA family change is computed by the refresh path against the
		// stored session; at login time we don't have a prior UA to
		// diff against here, so this stays false. Reserved for future
		// enrichment when we cache last-UA per user.
		UAFamilyChanged: false,
	}
}

// applyAnomalySideEffects is the legacy "shadow" behaviour: persist
// the audit row, refresh the last-IP cache, emit the Kafka event. Run
// when (a) the risk is low/medium and the session is being issued, or
// (b) enforcement mode is "shadow" so we keep doing exactly what we
// did before.
func (s *Service) applyAnomalySideEffects(ctx context.Context, userID, ip, deviceID, platform, userAgent string, probe anomalyProbeResult) {
	lastIPKey := fmt.Sprintf("last_ip:%s", userID)
	// Always refresh the cache — even on no-change logins this resets
	// the TTL so a long-lived account keeps its baseline.
	s.rdb.Set(ctx, lastIPKey, ip, 30*24*time.Hour)

	if !(probe.IsNewIP || probe.IsNewDevice) {
		return
	}

	if uid, err := uuid.Parse(userID); err == nil {
		anomalyType := "new_ip"
		risk := 30
		if probe.IsNewDevice {
			anomalyType = "new_device"
			risk = 50
		}
		_ = s.store.RecordLoginAnomaly(ctx, uid, anomalyType, ip, userAgent, deviceID, "", risk, false, map[string]any{
			"platform": platform,
			"prior_ip": probe.LastIP,
		})
	}

	payload := map[string]interface{}{
		"user_id":       userID,
		"ip":            ip,
		"device_id":     deviceID,
		"platform":      platform,
		"is_new_ip":     probe.IsNewIP,
		"is_new_device": probe.IsNewDevice,
		"occurred_at":   time.Now(),
	}
	if payloadBytes, err := json.Marshal(payload); err == nil {
		_ = s.producer.PublishRaw(ctx, "user.login_anomaly", userID, json.RawMessage(payloadBytes))
	}
}

// detectLoginAnomaly preserves the original audit-only contract for
// callers that don't want to thread the enforcement gate (currently
// only used by VerifyOTP / LoginWithPassword AFTER the createSession
// path; on enforce mode those paths route through createSessionForUser
// which probes + enforces directly).
//
// Industry-standard split:
//   - Persist for audit + user-visible history.
//   - Emit Kafka event for downstream services (notifications, fraud
//     scoring, ops alerting).
//   - Cache the latest IP in Redis so this hot-path check stays sub-ms
//     even at billions-of-users scale.
func (s *Service) detectLoginAnomaly(ctx context.Context, userID, ip, deviceID, platform, userAgent string) {
	probe := s.probeLoginAnomaly(ctx, userID, ip, deviceID)
	s.applyAnomalySideEffects(ctx, userID, ip, deviceID, platform, userAgent, probe)
}

// bcryptCompare is a thin shim used by anomaly_stepup.go so it doesn't
// have to import golang.org/x/crypto/bcrypt directly — keeps the file
// focused on the policy logic. Returns the underlying bcrypt error on
// mismatch (or any other failure mode).
func bcryptCompare(hash, plain string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
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
