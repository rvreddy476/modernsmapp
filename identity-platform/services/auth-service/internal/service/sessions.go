// Session lifecycle after sign-in: refresh-token rotation (including the
// user-agent family and subnet fingerprint checks), logout, logout-all,
// listing and revoking sessions with the revocation cache, and trusted
// devices. Pure move out of auth.go; behaviour is unchanged.

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

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
	return s.refreshSessionOfKind(ctx, refreshToken, ip, userAgent, store.SessionKindConsumer)
}

// refreshSessionOfKind is the refresh for one session kind. A refresh token
// of the other kind is refused with ErrWrongSessionKind and left untouched:
// the consumer route never rotates an admin session and the admin route never
// rotates a consumer one. An admin session additionally keeps its absolute
// expiry and ends when its admin MFA would no longer hold (see
// admin_login.go).
func (s *Service) refreshSessionOfKind(ctx context.Context, refreshToken, ip, userAgent, kind string) (*AuthResponse, error) {
	if refreshToken == "" {
		return nil, errors.New("missing refresh token")
	}

	refreshTokenHash := hashToken(refreshToken)
	sess, err := s.store.GetSessionByRefreshTokenHash(ctx, refreshTokenHash)
	if err != nil {
		return nil, err
	}
	retry := false
	if sess == nil {
		// Not the current token of any session. If it WAS one, it has been
		// used once already. Moments after its rotation, from a session whose
		// successor token is still unused, that is a client retrying a lost
		// response: rotate the session again, from its current token, which
		// cuts off the successor the client never received. Otherwise the
		// holder of the rotated token cannot be told apart from a thief, and
		// the whole family is burnt.
		sess, err = s.resolveRotatedRefreshToken(ctx, refreshTokenHash, ip, userAgent)
		if err != nil {
			return nil, err
		}
		if sess == nil {
			return nil, errors.New("invalid refresh token")
		}
		retry = true
	}
	if sess.RevokedAt != nil {
		return nil, errors.New("invalid refresh token")
	}
	if sessionKindOf(sess) != kind {
		return nil, ErrWrongSessionKind
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

	newExpiresAt := time.Now().Add(s.cfg.RefreshTokenTTL)
	if kind == store.SessionKindAdmin {
		// Checked BEFORE rotation, so a session that has lost its admin
		// footing is ended rather than handed a fresh refresh token.
		if err := s.requireAdminFooting(ctx, user, sess); err != nil {
			return nil, err
		}
		newExpiresAt = sess.ExpiresAt // absolute lifetime: refresh never extends it
	}

	newRefreshToken, err := generateOpaqueToken(32)
	if err != nil {
		return nil, err
	}
	var rotated bool
	if retry {
		// Replace the session's current token, the successor the client never
		// received, and only while the row still holds it.
		rotated, err = s.store.RotateSessionOnRetry(ctx, sess.ID, sess.RefreshToken, hashToken(newRefreshToken), ip, newExpiresAt)
	} else {
		rotated, err = s.store.RotateSessionWithFingerprint(ctx, sess.ID, refreshTokenHash, hashToken(newRefreshToken), ip, newExpiresAt, false)
	}
	if err != nil {
		return nil, err
	}
	if !rotated {
		// The row no longer holds the presented hash, or is no longer live:
		// a concurrent refresh with the same token won, or logout landed
		// between the read and this write. Either way nothing is issued.
		return nil, errors.New("invalid refresh token")
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

	// A2: auth_time and amr come from the session row, unchanged; admin
	// permissions and TOTP enrolment are re-resolved on this mint, so a
	// revoked role or a disabled authenticator takes effect here.
	accessToken, accessExpiresAt, err := s.mintAccessToken(ctx, user, sess, time.Time{})
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
			ExpiresAt:    accessExpiresAt,
		},
		User:                    user,
		SessionID:               sess.ID,
		LocalAdminPasswordLogin: s.localAdminSession(sess) && !(user.HasTOTP() && hasAMR(sess.AMR, AMROTP)),
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
