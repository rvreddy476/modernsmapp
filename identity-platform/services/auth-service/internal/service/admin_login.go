package service

// Admin console sign-in on its own host (admin console Wave 1, B3).
//
// THE RULES
//
//  1. An admin session is a separate session KIND (auth.sessions.kind =
//     'admin'). It is created ONLY by AdminVerify2FA and its tokens are only
//     ever written to the host-only admin_* cookies by the
//     /v1/auth/admin-session/* routes. Its access tokens carry sk=admin.
//  2. Sign-in is two steps and the second is always TOTP: AdminLogin checks
//     the password, that the account is active, holds at least one admin
//     permission and has TOTP enrolled, and returns a pending token (never a
//     session). AdminVerify2FA accepts ONLY a TOTP code (a recovery code is
//     not an admin factor) and creates the session with amr [pwd, otp], so its
//     tokens carry admin_mfa=true. There is no password-only admin session.
//  3. Pending admin sign-ins live under their own Redis prefix, so the
//     consumer POST /v1/auth/2fa/verify cannot complete one and the admin
//     route cannot complete a consumer one.
//  4. Refresh rotates only an admin session (the consumer refresh refuses
//     one), never extends its absolute lifetime (ADMIN_SESSION_TTL), and ends
//     the session the moment it would no longer be an admin MFA session (role
//     revoked, TOTP disabled, account not active).
//  5. Logout on the admin route revokes only an admin session.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-auth-service/pkg/accesstoken"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

var (
	// ErrAdminInvalidCredentials: unknown account or wrong password (one
	// answer for both). HTTP AUTH_FAILED.
	ErrAdminInvalidCredentials = errors.New("invalid credentials")
	// ErrAdminAccountInactive: the password matched but the account is not
	// active (pending, deactivated, suspended, deleting). The admin sign-in
	// never reactivates an account. HTTP ACCOUNT_NOT_ACTIVE.
	ErrAdminAccountInactive = errors.New("this account is not active")
	// ErrNotAdmin: the account holds no admin permission. HTTP NOT_ADMIN.
	ErrNotAdmin = errors.New("this account has no admin access")
	// ErrAdminPermissionsUnavailable: permissions could not be read, so the
	// sign-in fails closed. HTTP PERMISSIONS_UNAVAILABLE.
	ErrAdminPermissionsUnavailable = errors.New("admin permissions could not be checked")
	// ErrAdminPendingInvalid: the pending sign-in is unknown, expired or
	// already used. HTTP ADMIN_SIGN_IN_EXPIRED.
	ErrAdminPendingInvalid = errors.New("this sign-in has expired; enter your password again")
	// ErrWrongSessionKind: a consumer credential on an admin-session route, or
	// an admin credential on a consumer route.
	ErrWrongSessionKind = errors.New("this session cannot be used here")
	// ErrAdminAccessLost: an admin session whose holder is no longer an
	// active, TOTP-enrolled admin. The session has been revoked.
	ErrAdminAccessLost = errors.New("admin access has ended for this session; sign in again")
)

const (
	adminPendingPrefix = "admin_2fa:pending:"
	adminPendingTTL    = 5 * time.Minute
	// Per-ACCOUNT TOTP attempt limit on the second step (the route also has
	// the login IP limiter). Same budget as step-up.
	adminLoginOTPLimit  int64 = 5
	adminLoginOTPWindow       = 15 * time.Minute
	// AdminSessionPlatform is the session's platform column; informational
	// only — the kind column is what the rules read.
	AdminSessionPlatform = "admin_console"
)

// sessionKindOf normalises the stored kind (empty = consumer).
func sessionKindOf(sess *store.Session) string {
	if sess != nil && sess.Kind == store.SessionKindAdmin {
		return store.SessionKindAdmin
	}
	return store.SessionKindConsumer
}

// AdminLoginChallenge is the body of POST /v1/auth/admin-session/login. It is
// never a session: only the TOTP step creates one.
type AdminLoginChallenge struct {
	Requires2FA  bool      `json:"requires_2fa"`
	PendingToken string    `json:"pending_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type adminPendingSignIn struct {
	UserID    string   `json:"user_id"`
	IP        string   `json:"ip"`
	UserAgent string   `json:"user_agent"`
	AMR       []string `json:"amr"`
}

// adminEligible re-checks, from live state, that user may hold an admin
// session: active, at least one admin permission, TOTP enrolled.
func (s *Service) adminEligible(ctx context.Context, user *store.User) error {
	if user == nil || user.AccountStatus != store.AccountStatusActive {
		return ErrAdminAccountInactive
	}
	perms, err := s.PermissionsForUser(ctx, user.ID)
	if err != nil {
		s.log.Warn("admin sign-in: permissions unavailable", "user_id", user.ID, "err", err)
		return ErrAdminPermissionsUnavailable
	}
	if adminEmpty(perms) {
		return ErrNotAdmin
	}
	if !user.TwoFactorEnabled {
		return ErrTOTPNotEnrolled
	}
	return nil
}

// AdminLogin is step one: password, then eligibility, then a pending token.
func (s *Service) AdminLogin(ctx context.Context, identifier, password, ip, userAgent string) (*AdminLoginChallenge, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" || password == "" {
		return nil, ErrAdminInvalidCredentials
	}
	user, err := s.store.GetUserByPhone(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if user == nil {
		if user, err = s.store.GetUserByEmail(ctx, identifier); err != nil {
			return nil, err
		}
	}
	if user == nil || user.PasswordHash == "" {
		return nil, ErrAdminInvalidCredentials
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return nil, ErrAdminInvalidCredentials
	}
	// Only the account's owner reaches the checks below, so their distinct
	// answers reveal nothing to a guesser.
	if err := s.adminEligible(ctx, user); err != nil {
		return nil, err
	}
	if s.rdb == nil {
		return nil, errors.New("admin sign-in unavailable: no session store")
	}

	token, err := generateOpaqueToken(32)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(adminPendingSignIn{
		UserID: user.ID.String(), IP: ip, UserAgent: userAgent, AMR: []string{AMRPassword},
	})
	if err != nil {
		return nil, err
	}
	if err := s.rdb.Set(ctx, adminPendingPrefix+token, data, adminPendingTTL).Err(); err != nil {
		return nil, fmt.Errorf("store pending admin sign-in: %w", err)
	}
	// Indexed with the consumer pending tokens so a password reset or force
	// logout (InvalidatePending2FASessions) wipes it too.
	idxKey := pendingByUserSetPrefix + user.ID.String()
	_ = s.rdb.SAdd(ctx, idxKey, token).Err()
	_ = s.rdb.Expire(ctx, idxKey, pendingSessionTTL).Err()

	return &AdminLoginChallenge{
		Requires2FA:  true,
		PendingToken: token,
		ExpiresAt:    time.Now().Add(adminPendingTTL).UTC(),
	}, nil
}

// AdminVerify2FA is step two: a TOTP code for the pending sign-in creates the
// admin session. Recovery codes are refused (ErrInvalidOTP).
func (s *Service) AdminVerify2FA(ctx context.Context, pendingToken, code string) (*AuthResponse, error) {
	pendingToken = strings.TrimSpace(pendingToken)
	if pendingToken == "" {
		return nil, ErrAdminPendingInvalid
	}
	if s.rdb == nil {
		return nil, errors.New("admin sign-in unavailable: no session store")
	}
	key := adminPendingPrefix + pendingToken
	raw, err := s.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return nil, ErrAdminPendingInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("read pending admin sign-in: %w", err)
	}
	var pending adminPendingSignIn
	if err := json.Unmarshal([]byte(raw), &pending); err != nil {
		return nil, ErrAdminPendingInvalid
	}
	userID, err := uuid.Parse(pending.UserID)
	if err != nil {
		return nil, ErrAdminPendingInvalid
	}

	if s.throttle != nil {
		allowed, retryAfter, terr := s.throttle.Allow(ctx, "admin_login_otp_rl:"+userID.String(), adminLoginOTPLimit, adminLoginOTPWindow)
		if terr != nil {
			return nil, &ErrThrottled{RetryAfter: adminLoginOTPWindow, Reason: "throttle_unavailable"}
		}
		if !allowed {
			return nil, &ErrThrottled{RetryAfter: retryAfter, Reason: "admin_login_otp"}
		}
	}

	// TOTP only. verifyTOTPOnce burns the code across every TOTP path.
	if err := s.verifyTOTPOnce(ctx, userID, code); err != nil {
		return nil, err
	}

	// Consume the pending sign-in exactly once: of two concurrent requests
	// with the same token, only the one whose delete removed it proceeds.
	n, err := s.rdb.Del(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("consume pending admin sign-in: %w", err)
	}
	if n != 1 {
		return nil, ErrAdminPendingInvalid
	}
	_ = s.rdb.SRem(ctx, pendingByUserSetPrefix+userID.String(), pendingToken).Err()

	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.adminEligible(ctx, user); err != nil {
		return nil, err
	}

	amr := withAMR(pending.AMR, AMROTP)
	resp, sess, err := s.startSessionOfKind(ctx, user, "", AdminSessionPlatform, pending.IP, pending.UserAgent,
		amr, store.SessionKindAdmin, s.adminSessionTTL())
	if err != nil {
		return nil, err
	}
	// Belt and braces: the session must mint as an admin MFA session. If it
	// does not (a permission read failing between the checks), end it.
	if _, claims := s.sessionClaims(ctx, user, sess, time.Time{}); !claims.AdminMFA {
		_ = s.RevokeSession(ctx, sess.ID)
		return nil, ErrAdminAccessLost
	}

	if uErr := s.store.UpdateLastLogin(ctx, user.ID); uErr != nil {
		s.log.Warn("failed to update last_login_at", "err", uErr, "user_id", user.ID)
	}
	if pErr := s.producer.PublishUserLoggedIn(ctx, user.ID, sess.ID, "", AdminSessionPlatform, pending.IP); pErr != nil {
		s.log.Warn("failed to publish user logged in event", "err", pErr, "user_id", user.ID, "session_id", sess.ID)
	}
	s.log.Info("admin console sign-in", "user_id", user.ID, "session_id", sess.ID)
	return resp, nil
}

// adminSessionTTL is the admin session's absolute lifetime.
func (s *Service) adminSessionTTL() time.Duration {
	if s.cfg.AdminSessionTTL > 0 {
		return s.cfg.AdminSessionTTL
	}
	return 12 * time.Hour
}

// requireAdminFooting ends an admin session whose holder no longer qualifies
// (called by the admin refresh before rotating).
func (s *Service) requireAdminFooting(ctx context.Context, user *store.User, sess *store.Session) error {
	if err := s.adminEligible(ctx, user); err != nil {
		if errors.Is(err, ErrAdminPermissionsUnavailable) {
			return err // transient: do not end the session over a DB blip
		}
		_ = s.RevokeSession(ctx, sess.ID)
		return ErrAdminAccessLost
	}
	if !hasAMR(sess.AMR, AMROTP) {
		_ = s.RevokeSession(ctx, sess.ID)
		return ErrAdminAccessLost
	}
	return nil
}

// AdminRefreshSession rotates an admin session (and only an admin session).
func (s *Service) AdminRefreshSession(ctx context.Context, refreshToken, ip, userAgent string) (*AuthResponse, error) {
	return s.refreshSessionOfKind(ctx, refreshToken, ip, userAgent, store.SessionKindAdmin)
}

// AdminLogout revokes the admin session behind refreshToken. A consumer
// session's token is ignored (not revoked): this route only ends admin
// sessions.
func (s *Service) AdminLogout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	sess, err := s.store.GetSessionByRefreshTokenHash(ctx, hashToken(refreshToken))
	if err != nil {
		return err
	}
	if sess == nil || !sess.IsAdmin() {
		return nil
	}
	return s.RevokeSession(ctx, sess.ID)
}

// AdminSessionStatus is the body of GET /v1/auth/admin-session.
type AdminSessionStatus struct {
	UserID           string     `json:"user_id"`
	Email            string     `json:"email,omitempty"`
	AdminMFA         bool       `json:"admin_mfa"`
	SessionExpiresAt time.Time  `json:"session_expires_at"`
	StepUpValidUntil *time.Time `json:"step_up_valid_until,omitempty"`
}

// AdminSessionStatusFor describes the caller's live admin session.
func (s *Service) AdminSessionStatusFor(ctx context.Context, userID, sessionID uuid.UUID) (*AdminSessionStatus, error) {
	sess, err := s.store.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sess == nil || sess.UserID != userID || sess.RevokedAt != nil || !sess.IsActive || time.Now().After(sess.ExpiresAt) {
		return nil, ErrSessionNotLive
	}
	if !sess.IsAdmin() {
		return nil, ErrWrongSessionKind
	}
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrSessionNotLive
	}
	_, claims := s.sessionClaims(ctx, user, sess, time.Time{})
	out := &AdminSessionStatus{
		UserID:           userID.String(),
		AdminMFA:         claims.AdminMFA,
		SessionExpiresAt: sess.ExpiresAt.UTC(),
	}
	if user.Email != nil {
		out.Email = *user.Email
	}
	if sa, ok := SessionAuthFrom(ctx); ok && sa.StepUpAt > 0 {
		until := time.Unix(sa.StepUpAt, 0).UTC().Add(accesstoken.StepUpValidity)
		out.StepUpValidUntil = &until
	}
	return out, nil
}
