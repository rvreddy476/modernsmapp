package service

// Email two-step verification for app users (founder decision, 10 Nov 2026).
//
// THE RULES
//
//  1. App users turn 2FA on and off with their PASSWORD plus a 6-digit code
//     sent to their VERIFIED email. An access token alone can change nothing:
//     before this, POST /2fa/setup needed only a token, so a stolen token
//     could enrol the attacker's authenticator and survive a password reset.
//  2. Authenticator apps (TOTP) are offered to admin-console accounts only
//     (requireTOTPOffered). Consumers already enrolled keep TOTP; to switch
//     they disable it (password + TOTP) and then enable email.
//  3. Sign-in of an email-2FA account answers requires_2fa with
//     two_factor_method "email" and mails the code at once; /2fa/verify takes
//     that code, /2fa/resend sends another (60 s cooldown + the resend caps).
//  4. Step-up for an email-2FA account: /step-up/email/start mails a code,
//     /step-up takes {challenge_token, code}. It never yields admin MFA: only
//     a TOTP check does (admin_session.go rule 3).
//  5. Every code goes through checkCappedOTP (OTP_MAX_ATTEMPTS per code) under
//     a per-account budget, and is bound to (account, purpose): a code mailed
//     for sign-in cannot enable, disable or step up, and the reverse.
//  6. A password reset leaves email 2FA on: the email IS the factor, and the
//     reset already proved control of it.
//
// The method is a column (auth.users.two_factor_method) so an "sms" method
// can be added beside "email" once an SMS provider exists.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-auth-service/pkg/accesstoken"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Purposes a client names on POST /v1/auth/2fa/email/start.
const (
	TwoFAPurposeEnable  = "enable"
	TwoFAPurposeDisable = "disable"
)

// Internal challenge purposes (also the auth.otp_codes purpose suffix).
const (
	emailChallengeEnable  = "enable"
	emailChallengeDisable = "disable"
	emailChallengeStepUp  = "step_up"
	emailCodeLogin        = "login"
)

const (
	emailChallengePrefix = "2fa:email_challenge:"
	emailCooldownPrefix  = "2fa:email_cooldown:"
	// EmailCodeResendCooldown is the minimum gap between two codes of the
	// same purpose to one account.
	EmailCodeResendCooldown = 60 * time.Second
	// Codes mailed by an explicit start (enable, disable, step-up) per
	// account per hour, on top of the cooldown. Sign-in codes are bounded by
	// the login limiter, resends by checkResendQuota.
	emailCodeSendLimit  int64 = 10
	emailCodeSendWindow       = time.Hour
	// Email code length is part of the client contract.
	emailCodeDigits = 6
)

var (
	// ErrTOTPNotOffered: authenticator-app enrolment is for admin accounts.
	ErrTOTPNotOffered = errors.New("authenticator-app two-step verification is offered to admin accounts only")
	// ErrTwoFactorAlreadyEnabled: enable asked while 2FA is on.
	ErrTwoFactorAlreadyEnabled = errors.New("two-step verification is already on")
	// ErrTwoFactorNotEnabled: disable or step-up asked while 2FA is off.
	ErrTwoFactorNotEnabled = errors.New("two-step verification is not on")
	// ErrTwoFactorMethodMismatch: the request is for another 2FA method than
	// the account uses (an email challenge for a TOTP account, or the reverse).
	ErrTwoFactorMethodMismatch = errors.New("this account uses a different two-step verification method")
	// ErrTwoFactorEmailUnverified: email 2FA needs a verified email address.
	ErrTwoFactorEmailUnverified = errors.New("a verified email address is required for two-step verification")
	// ErrEmailChallengeInvalid: unknown, expired, spent or someone else's
	// challenge_token, or one issued for another purpose or session.
	ErrEmailChallengeInvalid = errors.New("invalid or expired challenge; start again")
	// ErrInvalidTwoFAPurpose: purpose is not "enable" or "disable".
	ErrInvalidTwoFAPurpose = errors.New(`purpose must be "enable" or "disable"`)
	// ErrEmailDeliveryUnavailable: the code could not be sent.
	ErrEmailDeliveryUnavailable = errors.New("the code could not be sent; try again shortly")
	// ErrPending2FAInvalid: unknown or expired pending sign-in token.
	ErrPending2FAInvalid = errors.New("invalid or expired pending session")
)

// EmailChallenge is the answer to a start call: the code went to EmailMasked,
// and ChallengeToken plus that code completes the action before ExpiresAt.
type EmailChallenge struct {
	ChallengeToken string    `json:"challenge_token"`
	ExpiresAt      time.Time `json:"expires_at"`
	EmailMasked    string    `json:"email_masked"`
}

// EmailCodeSent is the answer to POST /v1/auth/2fa/resend.
type EmailCodeSent struct {
	Sent               bool      `json:"sent"`
	ExpiresAt          time.Time `json:"expires_at"`
	EmailMasked        string    `json:"email_masked"`
	ResendAfterSeconds int       `json:"resend_after_seconds"`
}

// emailChallengeRecord is the Redis value behind a challenge_token.
type emailChallengeRecord struct {
	UserID    string `json:"user_id"`
	Purpose   string `json:"purpose"`
	SessionID string `json:"session_id,omitempty"`
}

// emailCodeTTL is how long a mailed code and its challenge live.
func (s *Service) emailCodeTTL() time.Duration {
	if s.cfg == nil || s.cfg.OTPExpiry <= 0 {
		return 5 * time.Minute
	}
	return s.cfg.OTPExpiry
}

// emailOTPKey is the auth.otp_codes key for one account's email 2FA codes.
// Keyed on the user id, not the address, so a code never outlives an email
// change into another account's keyspace.
func emailOTPKey(userID uuid.UUID) string { return "2fa:" + userID.String() }

func emailOTPPurpose(purpose string) string { return "2fa_email_" + purpose }

// verifiedEmail returns the account's address when it is present and
// verified, the only address a 2FA code may go to.
func verifiedEmail(u *store.User) (string, bool) {
	if u == nil || u.Email == nil || *u.Email == "" || !u.EmailVerified {
		return "", false
	}
	return *u.Email, true
}

// generateEmailCode returns a uniformly random 6-digit code. Fixed length
// (not OTP_DIGITS): the client contract says six.
func generateEmailCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", emailCodeDigits, n.Int64()), nil
}

// emailCodeMessage is the subject and body for one purpose. The code is in
// the body only; it is never logged.
func emailCodeMessage(purpose, code string, minutes int) (string, string) {
	switch purpose {
	case emailChallengeEnable:
		return "Confirm two-step verification", fmt.Sprintf(
			"Your code to turn on two-step verification is %s.\n\n"+
				"It expires in %d minutes. Once it is on, every sign-in to your "+
				"Momentum account will ask for a code sent to this address.\n\n"+
				"If you did not ask for this, someone may know your password. "+
				"Change it now.\n", code, minutes)
	case emailChallengeDisable:
		return "Confirm two-step verification", fmt.Sprintf(
			"Your code to turn OFF two-step verification is %s.\n\n"+
				"It expires in %d minutes.\n\n"+
				"If you did not ask for this, do not share the code. Someone may "+
				"know your password: change it now.\n", code, minutes)
	case emailChallengeStepUp:
		return "Confirm it's you", fmt.Sprintf(
			"Your Momentum security code is %s.\n\n"+
				"It expires in %d minutes. Enter it to confirm a sensitive "+
				"change to your account.\n\n"+
				"If you did not ask for this, do not share the code.\n", code, minutes)
	default:
		return "Your sign-in code", fmt.Sprintf(
			"Your Momentum sign-in code is %s.\n\n"+
				"It expires in %d minutes.\n\n"+
				"If you did not try to sign in, someone may know your password. "+
				"Do not share the code, and change your password.\n", code, minutes)
	}
}

// cooldownRemaining reports how long until another code of purpose may go to
// userID (0 when none is running).
func (s *Service) cooldownRemaining(ctx context.Context, userID uuid.UUID, purpose string) (time.Duration, error) {
	if s.rdb == nil {
		return 0, errors.New("email code cooldown unavailable")
	}
	ttl, err := s.rdb.PTTL(ctx, emailCooldownPrefix+purpose+":"+userID.String()).Result()
	if err != nil {
		return 0, err
	}
	if ttl <= 0 {
		return 0, nil
	}
	return ttl, nil
}

// sendEmailCode issues a fresh code for (userID, purpose) and mails it to the
// account's verified address. The cooldown is claimed first (SETNX): with
// enforceCooldown a running cooldown is an *ErrThrottled; without it (the
// sign-in send) the earlier, still-live code stands and nothing is sent. A
// failed send releases the cooldown so the user can retry at once.
func (s *Service) sendEmailCode(ctx context.Context, user *store.User, purpose string, enforceCooldown bool) error {
	to, ok := verifiedEmail(user)
	if !ok {
		return ErrTwoFactorEmailUnverified
	}
	if s.email == nil {
		return ErrEmailDeliveryUnavailable
	}
	if s.rdb == nil {
		return fmt.Errorf("%w: no redis", ErrEmailDeliveryUnavailable)
	}
	cooldownKey := emailCooldownPrefix + purpose + ":" + user.ID.String()
	claimed, err := s.rdb.SetNX(ctx, cooldownKey, "1", EmailCodeResendCooldown).Result()
	if err != nil {
		// Fail closed, like every limiter here.
		return &ErrThrottled{RetryAfter: EmailCodeResendCooldown, Reason: "throttle_unavailable"}
	}
	if !claimed {
		if !enforceCooldown {
			return nil
		}
		ttl, _ := s.rdb.PTTL(ctx, cooldownKey).Result()
		if ttl <= 0 {
			ttl = EmailCodeResendCooldown
		}
		return &ErrThrottled{RetryAfter: ttl, Reason: "email_code_cooldown"}
	}

	code, err := generateEmailCode()
	if err != nil {
		s.rdb.Del(ctx, cooldownKey)
		return fmt.Errorf("generate code: %w", err)
	}
	ttl := s.emailCodeTTL()
	if err := s.store.SaveOTP(ctx, emailOTPKey(user.ID), code, emailOTPPurpose(purpose), ttl); err != nil {
		s.rdb.Del(ctx, cooldownKey)
		return fmt.Errorf("save code: %w", err)
	}
	subject, body := emailCodeMessage(purpose, code, int(ttl.Minutes()))
	if err := s.email.Send(ctx, EmailMessage{To: to, Subject: subject, TextBody: body}); err != nil {
		s.rdb.Del(ctx, cooldownKey)
		s.log.Error("2fa email code not sent", "err", err, "user_id", user.ID, "purpose", purpose)
		return fmt.Errorf("%w: %v", ErrEmailDeliveryUnavailable, err)
	}
	s.log.Info("2fa email code sent", "user_id", user.ID, "purpose", purpose)
	return nil
}

// checkEmailCode verifies a mailed code for (userID, purpose) under the
// per-account budget and the per-code OTP_MAX_ATTEMPTS cap. A wrong code is
// ErrVerifyCodeInvalid.
func (s *Service) checkEmailCode(ctx context.Context, userID uuid.UUID, purpose, code string) error {
	if err := s.allowAttempt(ctx, "2fa_email_rl:"+userID.String(),
		twoFAVerifyLimit, twoFAVerifyWindow, "2fa_email_verify"); err != nil {
		return err
	}
	return s.checkCappedOTP(ctx, emailOTPKey(userID), emailOTPPurpose(purpose), code)
}

// newEmailChallenge stores a challenge record and returns its token.
func (s *Service) newEmailChallenge(ctx context.Context, rec emailChallengeRecord) (string, time.Time, error) {
	if s.rdb == nil {
		return "", time.Time{}, errors.New("challenge store unavailable")
	}
	token, err := generateOpaqueToken(32)
	if err != nil {
		return "", time.Time{}, err
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return "", time.Time{}, err
	}
	ttl := s.emailCodeTTL()
	if err := s.rdb.Set(ctx, emailChallengePrefix+hashToken(token), raw, ttl).Err(); err != nil {
		return "", time.Time{}, err
	}
	return token, time.Now().Add(ttl).UTC(), nil
}

// loadEmailChallenge resolves a challenge_token issued to userID for purpose
// (and, for step-up, for sessionID). It does not consume it: a mistyped code
// must not cost the user their challenge; the code itself is capped.
func (s *Service) loadEmailChallenge(ctx context.Context, token string, userID uuid.UUID, purpose string, sessionID uuid.UUID) error {
	if token == "" || s.rdb == nil {
		return ErrEmailChallengeInvalid
	}
	raw, err := s.rdb.Get(ctx, emailChallengePrefix+hashToken(token)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return ErrEmailChallengeInvalid
		}
		return fmt.Errorf("load challenge: %w", err)
	}
	var rec emailChallengeRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return ErrEmailChallengeInvalid
	}
	if rec.UserID != userID.String() || rec.Purpose != purpose {
		return ErrEmailChallengeInvalid
	}
	if purpose == emailChallengeStepUp && rec.SessionID != sessionID.String() {
		return ErrEmailChallengeInvalid
	}
	return nil
}

func (s *Service) dropEmailChallenge(ctx context.Context, token string) {
	if s.rdb != nil {
		s.rdb.Del(ctx, emailChallengePrefix+hashToken(token))
	}
}

// recordTwoFactorChange writes the security-inbox row ("Two-step
// verification turned on/off") for any method. Best-effort: the change has
// already committed.
func (s *Service) recordTwoFactorChange(ctx context.Context, userID uuid.UUID, enabled bool, method string) {
	kind := "two_factor_disabled"
	if enabled {
		kind = "two_factor_enabled"
	}
	if err := s.store.RecordLoginAnomaly(ctx, userID, kind, "", "", "", "", 0, false,
		map[string]any{"method": method}); err != nil {
		s.log.Warn("2fa change not recorded in the security inbox", "err", err, "user_id", userID, "event", kind)
	}
	s.log.Info("2FA changed", "user_id", userID, "event", kind, "method", method)
}

// requireTOTPOffered refuses authenticator-app enrolment to an account with
// no admin permission (live env allowlist ∪ auth.user_roles, the same map
// that decides admin MFA). A role-lookup failure leaves only the env map, so
// it fails closed for DB-granted roles.
func (s *Service) requireTOTPOffered(ctx context.Context, userID uuid.UUID) error {
	admin, err := s.PermissionsForUser(ctx, userID)
	if err != nil {
		s.log.Warn("totp offer: role lookup failed; env roles only", "err", err, "user_id", userID)
	}
	if adminEmpty(admin) {
		return ErrTOTPNotOffered
	}
	return nil
}

// StartEmail2FA checks the password and mails a code that, with the returned
// challenge, turns email 2FA on (purpose "enable") or off ("disable").
func (s *Service) StartEmail2FA(ctx context.Context, userID uuid.UUID, password, purpose string) (*EmailChallenge, error) {
	if purpose != TwoFAPurposeEnable && purpose != TwoFAPurposeDisable {
		return nil, ErrInvalidTwoFAPurpose
	}
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil || user.AccountStatus != store.AccountStatusActive {
		return nil, ErrSessionNotLive
	}
	// The password re-check shares the per-account budget of every other
	// re-check (Disable2FA, account control), so a stolen token cannot grind
	// the password here.
	if err := s.allowAttempt(ctx, "pwd_reverify_rl:"+userID.String(),
		passwordReverifyLimit, passwordReverifyWindow, "password_reverify"); err != nil {
		return nil, err
	}
	if user.PasswordHash == "" || s.comparePassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}

	method := user.SecondFactorMethod()
	switch purpose {
	case TwoFAPurposeEnable:
		if method != "" {
			return nil, ErrTwoFactorAlreadyEnabled
		}
	case TwoFAPurposeDisable:
		if method == "" {
			return nil, ErrTwoFactorNotEnabled
		}
		if method != store.TwoFactorMethodEmail {
			// A TOTP account turns TOTP off with password + TOTP; an emailed
			// code must never downgrade it.
			return nil, ErrTwoFactorMethodMismatch
		}
	}
	to, ok := verifiedEmail(user)
	if !ok {
		return nil, ErrTwoFactorEmailUnverified
	}
	if err := s.allowAttempt(ctx, "2fa_email_send_rl:"+userID.String(),
		emailCodeSendLimit, emailCodeSendWindow, "2fa_email_send"); err != nil {
		return nil, err
	}

	token, expiresAt, err := s.newEmailChallenge(ctx, emailChallengeRecord{UserID: userID.String(), Purpose: purpose})
	if err != nil {
		return nil, err
	}
	if err := s.sendEmailCode(ctx, user, purpose, true); err != nil {
		s.dropEmailChallenge(ctx, token)
		return nil, err
	}
	return &EmailChallenge{ChallengeToken: token, ExpiresAt: expiresAt, EmailMasked: MaskEmail(to)}, nil
}

// EnableEmail2FA turns email 2FA on with the code from an "enable" challenge.
func (s *Service) EnableEmail2FA(ctx context.Context, userID uuid.UUID, challengeToken, code string) error {
	if err := s.loadEmailChallenge(ctx, challengeToken, userID, emailChallengeEnable, uuid.Nil); err != nil {
		return err
	}
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil || user.AccountStatus != store.AccountStatusActive {
		return ErrSessionNotLive
	}
	if user.TwoFactorEnabled {
		return ErrTwoFactorAlreadyEnabled
	}
	if _, ok := verifiedEmail(user); !ok {
		return ErrTwoFactorEmailUnverified
	}
	if err := s.checkEmailCode(ctx, userID, emailChallengeEnable, code); err != nil {
		return err
	}
	s.dropEmailChallenge(ctx, challengeToken)

	if err := s.store.EnableEmail2FA(ctx, userID); err != nil {
		if errors.Is(err, store.ErrTwoFactorStateChanged) {
			return ErrTwoFactorAlreadyEnabled
		}
		return fmt.Errorf("enable email 2fa: %w", err)
	}
	s.recordTwoFactorChange(ctx, userID, true, store.TwoFactorMethodEmail)
	return nil
}

// DisableEmail2FA turns email 2FA off with the code from a "disable"
// challenge. A TOTP account is refused (ErrTwoFactorMethodMismatch).
func (s *Service) DisableEmail2FA(ctx context.Context, userID uuid.UUID, challengeToken, code string) error {
	if err := s.loadEmailChallenge(ctx, challengeToken, userID, emailChallengeDisable, uuid.Nil); err != nil {
		return err
	}
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil || user.AccountStatus != store.AccountStatusActive {
		return ErrSessionNotLive
	}
	switch user.SecondFactorMethod() {
	case "":
		return ErrTwoFactorNotEnabled
	case store.TwoFactorMethodEmail:
	default:
		return ErrTwoFactorMethodMismatch
	}
	if err := s.checkEmailCode(ctx, userID, emailChallengeDisable, code); err != nil {
		return err
	}
	s.dropEmailChallenge(ctx, challengeToken)

	if err := s.store.Disable2FA(ctx, userID); err != nil {
		return fmt.Errorf("disable email 2fa: %w", err)
	}
	s.recordTwoFactorChange(ctx, userID, false, store.TwoFactorMethodEmail)
	return nil
}

// sendLoginCode mails the sign-in code for a pending email-2FA login. The
// pending envelope goes out even if this fails (logged): the client offers
// POST /2fa/resend, which reports the failure.
func (s *Service) sendLoginCode(ctx context.Context, user *store.User) {
	if err := s.sendEmailCode(ctx, user, emailCodeLogin, false); err != nil {
		s.log.Error("2fa sign-in code not sent; client can resend", "err", err, "user_id", user.ID)
	}
}

// loadPending2FA reads a pending sign-in without consuming it.
func (s *Service) loadPending2FA(ctx context.Context, pendingToken string) (*PendingTwoFASession, uuid.UUID, error) {
	if pendingToken == "" || s.rdb == nil {
		return nil, uuid.Nil, ErrPending2FAInvalid
	}
	data, err := s.rdb.Get(ctx, pendingSessionPrefix+pendingToken).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, uuid.Nil, ErrPending2FAInvalid
		}
		return nil, uuid.Nil, fmt.Errorf("failed to retrieve pending session: %w", err)
	}
	var pending PendingTwoFASession
	if err := json.Unmarshal([]byte(data), &pending); err != nil {
		return nil, uuid.Nil, ErrPending2FAInvalid
	}
	uid, err := uuid.Parse(pending.UserID)
	if err != nil {
		return nil, uuid.Nil, ErrPending2FAInvalid
	}
	return &pending, uid, nil
}

// Resend2FACode mails a fresh sign-in code for a pending email-2FA login:
// 60 s after the last one at the earliest, and within the per-account resend
// caps shared with verification resends.
func (s *Service) Resend2FACode(ctx context.Context, pendingToken string) (*EmailCodeSent, error) {
	_, uid, err := s.loadPending2FA(ctx, pendingToken)
	if err != nil {
		return nil, err
	}
	user, err := s.store.GetUserByID(ctx, uid)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrPending2FAInvalid
	}
	if user.SecondFactorMethod() != store.TwoFactorMethodEmail {
		return nil, ErrTwoFactorMethodMismatch
	}
	to, ok := verifiedEmail(user)
	if !ok {
		return nil, ErrTwoFactorEmailUnverified
	}
	// Cooldown before the quota, so a too-early resend costs no quota.
	if wait, err := s.cooldownRemaining(ctx, uid, emailCodeLogin); err != nil {
		return nil, &ErrThrottled{RetryAfter: EmailCodeResendCooldown, Reason: "throttle_unavailable"}
	} else if wait > 0 {
		return nil, &ErrThrottled{RetryAfter: wait, Reason: "email_code_cooldown"}
	}
	if err := s.checkResendQuota(ctx, uid); err != nil {
		return nil, err
	}
	if err := s.sendEmailCode(ctx, user, emailCodeLogin, true); err != nil {
		return nil, err
	}
	return &EmailCodeSent{
		Sent:               true,
		ExpiresAt:          time.Now().Add(s.emailCodeTTL()).UTC(),
		EmailMasked:        MaskEmail(to),
		ResendAfterSeconds: int(EmailCodeResendCooldown.Seconds()),
	}, nil
}

// liveConsumerSession is the session half of step-up: the caller's own,
// live, consumer session and an active account.
func (s *Service) liveConsumerSession(ctx context.Context, userID, sessionID uuid.UUID) (*store.Session, *store.User, error) {
	sess, err := s.store.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if sess == nil || sess.UserID != userID || sess.RevokedAt != nil || !sess.IsActive || time.Now().After(sess.ExpiresAt) {
		return nil, nil, ErrSessionNotLive
	}
	if sessionKindOf(sess) != store.SessionKindConsumer {
		return nil, nil, ErrWrongSessionKind
	}
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	if user == nil || user.AccountStatus != store.AccountStatusActive {
		return nil, nil, ErrSessionNotLive
	}
	return sess, user, nil
}

// StartStepUpEmail mails a step-up code to an email-2FA account, bound to the
// caller's session.
func (s *Service) StartStepUpEmail(ctx context.Context, userID, sessionID uuid.UUID) (*EmailChallenge, error) {
	_, user, err := s.liveConsumerSession(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	switch user.SecondFactorMethod() {
	case "":
		return nil, ErrTwoFactorNotEnabled
	case store.TwoFactorMethodEmail:
	default:
		return nil, ErrTwoFactorMethodMismatch
	}
	to, ok := verifiedEmail(user)
	if !ok {
		return nil, ErrTwoFactorEmailUnverified
	}
	if err := s.allowAttempt(ctx, "2fa_email_send_rl:"+userID.String(),
		emailCodeSendLimit, emailCodeSendWindow, "2fa_email_send"); err != nil {
		return nil, err
	}
	token, expiresAt, err := s.newEmailChallenge(ctx, emailChallengeRecord{
		UserID: userID.String(), Purpose: emailChallengeStepUp, SessionID: sessionID.String(),
	})
	if err != nil {
		return nil, err
	}
	if err := s.sendEmailCode(ctx, user, emailChallengeStepUp, true); err != nil {
		s.dropEmailChallenge(ctx, token)
		return nil, err
	}
	return &EmailChallenge{ChallengeToken: token, ExpiresAt: expiresAt, EmailMasked: MaskEmail(to)}, nil
}

// StepUpEmail is StepUp for an email-2FA account: the mailed code from a
// step-up challenge on THIS session mints an access token with step_up_at.
// It records "email" in the session's amr, never "otp", so it never makes an
// admin MFA session.
func (s *Service) StepUpEmail(ctx context.Context, userID, sessionID uuid.UUID, challengeToken, code string) (*StepUpResponse, error) {
	// Same per-account step-up budget as the TOTP path.
	if s.throttle != nil {
		allowed, retryAfter, err := s.throttle.Allow(ctx, "stepup_rl:"+userID.String(), stepUpAttemptLimit, stepUpAttemptWindow)
		if err != nil {
			return nil, &ErrThrottled{RetryAfter: stepUpAttemptWindow, Reason: "throttle_unavailable"}
		}
		if !allowed {
			return nil, &ErrThrottled{RetryAfter: retryAfter, Reason: "step_up"}
		}
	}
	sess, user, err := s.liveConsumerSession(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}
	switch user.SecondFactorMethod() {
	case "":
		return nil, ErrTwoFactorNotEnabled
	case store.TwoFactorMethodEmail:
	default:
		return nil, ErrTwoFactorMethodMismatch
	}
	if err := s.loadEmailChallenge(ctx, challengeToken, userID, emailChallengeStepUp, sessionID); err != nil {
		return nil, err
	}
	if err := s.checkEmailCode(ctx, userID, emailChallengeStepUp, code); err != nil {
		return nil, err
	}
	s.dropEmailChallenge(ctx, challengeToken)

	if !hasAMR(sess.AMR, AMREmail) {
		if err := s.store.AddSessionAMR(ctx, sessionID, AMREmail); err != nil {
			if errors.Is(err, store.ErrSessionNotLive) {
				return nil, ErrSessionNotLive
			}
			return nil, err
		}
		sess.AMR = withAMR(sess.AMR, AMREmail)
	}

	now := time.Now()
	token, expiresAt, err := s.mintAccessToken(ctx, user, sess, now)
	if err != nil {
		return nil, err
	}
	_, claims := s.sessionClaims(ctx, user, sess, now)
	s.log.Info("email step-up completed", "user_id", userID, "session_id", sessionID)
	return &StepUpResponse{
		AccessToken:      token,
		ExpiresAt:        expiresAt,
		StepUpAt:         time.Unix(now.Unix(), 0).UTC(),
		StepUpValidUntil: time.Unix(now.Unix(), 0).UTC().Add(accesstoken.StepUpValidity),
		AdminMFA:         claims.AdminMFA,
	}, nil
}
