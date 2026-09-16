package service

// Admin sessions, mandatory 2FA and step-up (admin console Wave 0, A2).
//
// THE RULES
//
//  1. Every access token carries auth_time and amr, read from the session row,
//     so refresh carries them unchanged (pkg/accesstoken.Claims).
//  2. A user is an ADMIN when their live admin permission map is non-empty
//     (env allowlists, platform-wide rows and app-scoped rows alike).
//  3. An admin's token has admin_mfa=true only when TOTP is enrolled AND the
//     session's amr contains "otp" (a TOTP challenge completed on THIS
//     session: the login 2FA step, or POST /v1/auth/step-up). Otherwise
//     admin_mfa=false, the platform ladder roles (superadmin/admin/moderator)
//     are removed from `scopes`, and /me/capabilities reports
//     admin.mfa_required=true with an empty permission map.
//  4. The consumer session is untouched by rule 3: same login, same refresh
//     token, same ecosystem scopes (seller, …). An admin signing in to a
//     consumer app just gets a consumer token.
//  5. An admin_mfa token lives at most accesstoken.AdminSessionMaxTTL.
//     Every mint (login, refresh, step-up) re-resolves permissions and MFA.
//  6. Role grants/revokes and force logout need admin_mfa AND a step_up_at
//     no older than accesstoken.StepUpValidity (300 s).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/identity-auth-service/internal/permissions"
	"github.com/atpost/identity-auth-service/internal/roles"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-auth-service/pkg/accesstoken"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
)

// amr values (RFC 8176 where one exists).
const (
	AMRPassword    = "pwd"
	AMROTP         = "otp" // TOTP verified on this session — the only admin MFA
	AMRFederated   = "fed" // OAuth provider (Google / Apple)
	AMRHardwareKey = "hwk" // passkey
	AMRSMS         = "sms" // phone OTP
	AMREmail       = "email"
	// AMRRecovery: a 2FA recovery code. Deliberately NOT "otp": a recovery
	// code gets a consumer session, not an admin one; re-enrol TOTP instead.
	AMRRecovery = "recovery"
)

// withAMR returns a copy of amr with m appended once.
func withAMR(amr []string, m string) []string {
	out := make([]string, 0, len(amr)+1)
	for _, v := range amr {
		if v == m {
			return append(out, amr...)
		}
	}
	out = append(out, amr...)
	return append(out, m)
}

func hasAMR(amr []string, m string) bool {
	for _, v := range amr {
		if v == m {
			return true
		}
	}
	return false
}

// SessionAuth is the verified access token's session claims. The auth
// middleware attaches it to the request context; service code that gates on
// MFA or step-up reads it from there and fails closed when it is absent.
type SessionAuth struct {
	SessionID uuid.UUID
	AuthTime  int64
	AMR       []string
	AdminMFA  bool
	StepUpAt  int64
}

type sessionAuthKey struct{}

// WithSessionAuth attaches verified session claims to ctx.
func WithSessionAuth(ctx context.Context, sa SessionAuth) context.Context {
	return context.WithValue(ctx, sessionAuthKey{}, sa)
}

// SessionAuthFrom returns the verified session claims on ctx, if any.
func SessionAuthFrom(ctx context.Context) (SessionAuth, bool) {
	sa, ok := ctx.Value(sessionAuthKey{}).(SessionAuth)
	return sa, ok
}

var (
	// ErrStepUpRequired: the action needs a step_up_at younger than
	// accesstoken.StepUpValidity. HTTP code STEP_UP_REQUIRED.
	ErrStepUpRequired = errors.New("a fresh two-factor check is required: POST /v1/auth/step-up, then retry within 5 minutes")
	// ErrTOTPNotEnrolled: step-up needs an authenticator app enrolled.
	ErrTOTPNotEnrolled = errors.New("two-factor authentication is not enrolled")
	// ErrInvalidOTP: the TOTP code is wrong or outside its window.
	ErrInvalidOTP = errors.New("invalid two-factor code")
	// ErrSessionNotLive: the session behind the token is revoked or gone.
	ErrSessionNotLive = errors.New("session is no longer valid; sign in again")
	// ErrInvalidPermission: not "<app>:<action>" with a known app.
	ErrInvalidPermission = errors.New("invalid permission name")
)

// adminEmpty reports whether a permission map grants nothing.
func adminEmpty(a permissions.Admin) bool {
	if len(a.Platform) > 0 {
		return false
	}
	for _, perms := range a.Apps {
		if len(perms) > 0 {
			return false
		}
	}
	return true
}

// stripLadderScopes removes superadmin/admin/moderator from a scopes claim,
// keeping ecosystem roles. Used for an admin whose session lacks MFA.
func stripLadderScopes(scopes string) string {
	var kept []string
	for _, sc := range strings.Fields(scopes) {
		if roles.IsPlatform(sc) {
			continue
		}
		kept = append(kept, sc)
	}
	return strings.Join(kept, " ")
}

// sessionClaims resolves the scopes and session claims for one mint.
func (s *Service) sessionClaims(ctx context.Context, user *store.User, sess *store.Session, stepUpAt time.Time) (string, accesstoken.Session) {
	admin, err := s.PermissionsForUser(ctx, user.ID)
	if err != nil {
		// Degrades to the env allowlist map, like resolveScopes does, so the
		// admin decision and the scopes agree during a DB outage.
		s.log.Warn("admin permissions lookup failed at mint; env roles only", "user_id", user.ID, "err", err)
	}
	isAdmin := !adminEmpty(admin)
	adminMFA := isAdmin && user.TwoFactorEnabled && hasAMR(sess.AMR, AMROTP)

	scopes := s.resolveScopes(ctx, user.ID)
	if !adminMFA {
		scopes = stripLadderScopes(scopes)
	}

	authTime := sess.CreatedAt
	if sess.AuthTime != nil {
		authTime = *sess.AuthTime
	}
	kind := ""
	if sess.IsAdmin() {
		kind = accesstoken.SessionKindAdmin
	}
	return scopes, accesstoken.Session{
		AuthTime: authTime,
		AMR:      sess.AMR,
		AdminMFA: adminMFA,
		StepUpAt: stepUpAt,
		Kind:     kind,
	}
}

// mintAccessToken mints the access token for a session row. stepUpAt is zero
// except on POST /v1/auth/step-up.
func (s *Service) mintAccessToken(ctx context.Context, user *store.User, sess *store.Session, stepUpAt time.Time) (string, time.Time, error) {
	scopes, claims := s.sessionClaims(ctx, user, sess, stepUpAt)
	return accesstoken.MintSession(s.accessTokenConfig(), s.accessSigningKey, user.ID, sess.ID, scopes, claims, time.Now())
}

// startSession creates a session row authenticated by amr and mints its
// first token pair. Every full-session entry point ends here.
func (s *Service) startSession(ctx context.Context, user *store.User, deviceID, platform, ip, userAgent string, amr []string) (*AuthResponse, error) {
	resp, _, err := s.startSessionOfKind(ctx, user, deviceID, platform, ip, userAgent, amr, store.SessionKindConsumer, s.cfg.RefreshTokenTTL)
	return resp, err
}

// startSessionOfKind creates the session row with an explicit kind and
// lifetime, and returns the row alongside the first token pair. Only the
// admin console sign-in passes SessionKindAdmin.
func (s *Service) startSessionOfKind(ctx context.Context, user *store.User, deviceID, platform, ip, userAgent string, amr []string, kind string, lifetime time.Duration) (*AuthResponse, *store.Session, error) {
	now := time.Now()
	refreshToken, err := generateOpaqueToken(32)
	if err != nil {
		return nil, nil, err
	}
	sess := &store.Session{
		ID:           uuid.New(),
		UserID:       user.ID,
		RefreshToken: hashToken(refreshToken),
		DeviceID:     deviceID,
		Platform:     platform,
		IP:           ip,
		UserAgent:    userAgent,
		IsActive:     true,
		CreatedAt:    now,
		ExpiresAt:    now.Add(lifetime),
		AuthTime:     &now,
		AMR:          append([]string(nil), amr...),
		Kind:         kind,
	}
	if err := s.store.CreateSession(ctx, sess); err != nil {
		return nil, nil, err
	}
	accessToken, expiresAt, err := s.mintAccessToken(ctx, user, sess, time.Time{})
	if err != nil {
		return nil, nil, err
	}
	return &AuthResponse{
		Tokens: TokenPair{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			ExpiresAt:    expiresAt,
		},
		User:      user,
		SessionID: sess.ID,
	}, sess, nil
}

// totpReplayWindow covers the ±1 step skew totp.Validate accepts.
const totpReplayWindow = 90 * time.Second

// verifyTOTPOnce validates a TOTP code for a user and burns it: the same code
// is refused (ErrTOTPReplay) for totpReplayWindow on EVERY TOTP path — login
// 2FA, anomaly step-up and admin step-up share the totp_used key. The burn is
// an atomic SETNX, and a Redis failure refuses the code (fail closed).
func (s *Service) verifyTOTPOnce(ctx context.Context, userID uuid.UUID, code string) error {
	secret, err := s.store.Get2FASecret(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to get 2FA secret: %w", err)
	}
	code = strings.TrimSpace(code)
	if secret == "" || code == "" || !totp.Validate(code, secret) {
		return ErrInvalidOTP
	}
	if s.rdb == nil {
		return errors.New("totp replay protection unavailable")
	}
	fresh, err := s.rdb.SetNX(ctx, fmt.Sprintf("totp_used:%s:%s", userID, code), "1", totpReplayWindow).Result()
	if err != nil {
		return fmt.Errorf("totp replay protection unavailable: %w", err)
	}
	if !fresh {
		return ErrTOTPReplay
	}
	return nil
}

// Step-up attempt limit per ACCOUNT (the route also has the login IP limiter).
const (
	stepUpAttemptLimit  int64 = 5
	stepUpAttemptWindow       = 15 * time.Minute
)

// StepUpResponse is the body of POST /v1/auth/step-up.
type StepUpResponse struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	StepUpAt    time.Time `json:"step_up_at"`
	// StepUpValidUntil = StepUpAt + accesstoken.StepUpValidity.
	StepUpValidUntil time.Time `json:"step_up_valid_until"`
	AdminMFA         bool      `json:"admin_mfa"`
}

// StepUp re-verifies TOTP for the caller's live session and mints a fresh
// access token carrying step_up_at=now. It also records "otp" in the
// session's amr, so an admin session that had not completed an OTP challenge
// becomes an admin_mfa session from here on (including across refresh).
// The refresh token is not rotated.
func (s *Service) StepUp(ctx context.Context, userID, sessionID uuid.UUID, code string) (*StepUpResponse, error) {
	return s.stepUpKind(ctx, userID, sessionID, code, store.SessionKindConsumer)
}

// AdminStepUp is StepUp for an admin console session
// (POST /v1/auth/admin-session/step-up). A consumer session is refused with
// ErrWrongSessionKind, as an admin session is on the consumer route.
func (s *Service) AdminStepUp(ctx context.Context, userID, sessionID uuid.UUID, code string) (*StepUpResponse, error) {
	return s.stepUpKind(ctx, userID, sessionID, code, store.SessionKindAdmin)
}

func (s *Service) stepUpKind(ctx context.Context, userID, sessionID uuid.UUID, code, kind string) (*StepUpResponse, error) {
	if s.throttle != nil {
		allowed, retryAfter, err := s.throttle.Allow(ctx, "stepup_rl:"+userID.String(), stepUpAttemptLimit, stepUpAttemptWindow)
		if err != nil {
			return nil, &ErrThrottled{RetryAfter: stepUpAttemptWindow, Reason: "throttle_unavailable"}
		}
		if !allowed {
			return nil, &ErrThrottled{RetryAfter: retryAfter, Reason: "step_up"}
		}
	}

	sess, err := s.store.GetSessionByID(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sess == nil || sess.UserID != userID || sess.RevokedAt != nil || !sess.IsActive || time.Now().After(sess.ExpiresAt) {
		return nil, ErrSessionNotLive
	}
	if sessionKindOf(sess) != kind {
		return nil, ErrWrongSessionKind
	}
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil || user.AccountStatus != store.AccountStatusActive {
		return nil, ErrSessionNotLive
	}
	if !user.TwoFactorEnabled {
		return nil, ErrTOTPNotEnrolled
	}
	if err := s.verifyTOTPOnce(ctx, userID, code); err != nil {
		return nil, err
	}

	if !hasAMR(sess.AMR, AMROTP) {
		if err := s.store.AddSessionAMR(ctx, sessionID, AMROTP); err != nil {
			if errors.Is(err, store.ErrSessionNotLive) {
				return nil, ErrSessionNotLive
			}
			return nil, err
		}
		sess.AMR = withAMR(sess.AMR, AMROTP)
	}

	now := time.Now()
	token, expiresAt, err := s.mintAccessToken(ctx, user, sess, now)
	if err != nil {
		return nil, err
	}
	_, claims := s.sessionClaims(ctx, user, sess, now)
	s.log.Info("step-up completed", "user_id", userID, "session_id", sessionID, "admin_mfa", claims.AdminMFA)
	return &StepUpResponse{
		AccessToken:      token,
		ExpiresAt:        expiresAt,
		StepUpAt:         time.Unix(now.Unix(), 0).UTC(),
		StepUpValidUntil: time.Unix(now.Unix(), 0).UTC().Add(accesstoken.StepUpValidity),
		AdminMFA:         claims.AdminMFA,
	}, nil
}

// requireFreshAdminSession is the MFA + step-up half of authorizePrivileged.
func (s *Service) requireFreshAdminSession(ctx context.Context, actorID uuid.UUID) error {
	sa, ok := SessionAuthFrom(ctx)
	if !ok || !sa.AdminMFA {
		return ErrMFARequired
	}
	// Live re-check: a token minted before TOTP was disabled still says
	// admin_mfa=true for up to 15 minutes.
	actor, err := s.store.GetUserByID(ctx, actorID)
	if err != nil || actor == nil || !actor.TwoFactorEnabled {
		return ErrMFARequired
	}
	if !accesstoken.StepUpFresh(sa.StepUpAt, time.Now()) {
		return ErrStepUpRequired
	}
	return nil
}

// ForceLogout revokes every session of target (superadmin, admin MFA and a
// fresh step-up required). The revocation and its audit row commit together;
// each session is then marked sess_revoked:<sid> so the gateway stops its
// access tokens at once. Returns how many sessions were revoked.
func (s *Service) ForceLogout(ctx context.Context, actorID, targetID uuid.UUID, reason string) (int, error) {
	const action = "session.force_logout"
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return 0, ErrReasonRequired
	}
	if len(reason) > maxReasonLen {
		reason = reason[:maxReasonLen]
	}
	if err := s.authorizePrivileged(ctx, actorID, targetID, action); err != nil {
		return 0, err
	}
	ids, err := s.store.RevokeAllSessionsAudited(ctx, targetID, store.RoleAudit{
		ActorID: actorID, TargetID: targetID, Action: action, Detail: "reason=" + reason,
	})
	if err != nil {
		return 0, err
	}
	s.revokeCached(ctx, ids)
	s.InvalidatePending2FASessions(ctx, targetID)
	return len(ids), nil
}

// revokeCached writes sess_revoked:<sid> for each revoked session.
func (s *Service) revokeCached(ctx context.Context, ids []uuid.UUID) {
	for _, id := range ids {
		s.cacheRevoke(ctx, id)
	}
}

// envAllowlistIDs returns every parseable user id in the env role allowlists.
func (s *Service) envAllowlistIDs() []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for _, set := range []map[string]struct{}{s.cfg.ScopeSuperadminUserIDs, s.cfg.ScopeAdminUserIDs, s.cfg.ScopeModeratorUserIDs} {
		for id := range set {
			if u, err := uuid.Parse(id); err == nil && !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
	}
	return out
}

// validPermissionName accepts "<app>:<action>" with a catalogue app (or "*")
// and an action of [a-z0-9._-].
func validPermissionName(p string) bool {
	app, action, ok := strings.Cut(p, ":")
	if !ok || action == "" || len(p) > 100 {
		return false
	}
	if app != "*" && !permissions.ValidApp(app) {
		return false
	}
	for _, r := range action {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// CountOtherHolders counts ACTIVE accounts, other than excludeUserID, that
// hold permission right now AND have TOTP enrolled. admin-service uses it for
// two-person approval: 0 means the requester is the only possible approver.
// Holders without TOTP are not counted — they cannot open an admin session,
// so they cannot approve.
func (s *Service) CountOtherHolders(ctx context.Context, permission string, excludeUserID uuid.UUID) (int, error) {
	if !validPermissionName(permission) {
		return 0, ErrInvalidPermission
	}
	candidates, err := s.store.AdminHolderCandidates(ctx, s.envAllowlistIDs())
	if err != nil {
		return 0, err
	}
	now := time.Now()
	count := 0
	for _, c := range candidates {
		if c.UserID == excludeUserID || !c.TwoFactorEnabled {
			continue
		}
		var grants []permissions.Grant
		for _, r := range s.cfg.EnvRolesForUser(c.UserID.String()) {
			grants = append(grants, permissions.Grant{Role: r})
		}
		for _, g := range c.Grants {
			grants = append(grants, permissions.Grant{Role: g.Role, App: g.App, ExpiresAt: g.ExpiresAt})
		}
		if permissions.Resolve(grants, now).Has(permission) {
			count++
		}
	}
	return count, nil
}
