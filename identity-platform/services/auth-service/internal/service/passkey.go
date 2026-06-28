package service

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
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
	if s.producer != nil {
		if err := s.producer.PublishUserLoggedIn(ctx, user.ID, sessionID, deviceID, platform, ip); err != nil {
			s.log.Warn("publish user logged in failed", "err", err, "user_id", user.ID)
		}
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
