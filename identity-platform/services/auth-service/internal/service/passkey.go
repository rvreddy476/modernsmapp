package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// IssueSessionForUser mints an authenticated session (access + refresh tokens)
// for an already-verified user id. Used by passwordless flows (e.g. a passkey
// login that has cryptographically proven the user) where there's no password/
// OTP step to call. Mirrors the session issuance in VerifyOTP/Login.
func (s *Service) IssueSessionForUser(ctx context.Context, userID uuid.UUID, deviceID, platform, ip, userAgent string) (*AuthResponse, error) {
	if deviceID == "" {
		deviceID = "web"
	}
	if platform == "" {
		platform = "web"
	}
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, errors.New("user not found")
	}

	// amr ["hwk"]: a passkey session is a consumer session. It never carries
	// admin_mfa — an admin reaches that with POST /v1/auth/step-up (TOTP).
	resp, err := s.startSession(ctx, user, deviceID, platform, ip, userAgent, []string{AMRHardwareKey})
	if err != nil {
		return nil, err
	}
	if s.producer != nil {
		if err := s.producer.PublishUserLoggedIn(ctx, user.ID, resp.SessionID, deviceID, platform, ip); err != nil {
			s.log.Warn("publish user logged in failed", "err", err, "user_id", user.ID)
		}
	}
	return resp, nil
}
