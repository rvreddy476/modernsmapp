package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// revokeOnRefreshReuse is called when a refresh token matched no live session.
// When the hash is one a rotation already replaced, the token has been used
// before, so the session family is revoked (database and access-token
// revocation cache) and the event lands in the user's security inbox. An
// unknown hash is left alone. The caller refuses the refresh either way.
func (s *Service) revokeOnRefreshReuse(ctx context.Context, refreshTokenHash, ip, userAgent string) error {
	sessionID, err := s.store.LookupRotatedRefreshToken(ctx, refreshTokenHash)
	if err != nil {
		return fmt.Errorf("refresh reuse lookup: %w", err)
	}
	if sessionID == nil {
		return nil
	}
	sess, err := s.store.GetSessionByID(ctx, *sessionID)
	if err != nil {
		return fmt.Errorf("refresh reuse: load session: %w", err)
	}
	revoked, err := s.store.RevokeSessionFamily(ctx, *sessionID)
	if err != nil {
		return fmt.Errorf("refresh reuse: revoke family: %w", err)
	}
	s.cacheRevokeAll(ctx, revoked)
	if sess != nil {
		_ = s.store.RecordLoginAnomaly(ctx, sess.UserID, "session_revoked",
			ip, userAgent, sess.DeviceID, "", 90, true, map[string]any{
				"reason":     "refresh_token_reuse",
				"session_id": sess.ID.String(),
				"revoked":    len(revoked),
			})
		s.log.Warn("auth: refresh token reuse; session family revoked",
			"user_id", sess.UserID, "session_id", sess.ID, "revoked", len(revoked))
	}
	return nil
}

// cacheRevokeAll writes the access-token revocation cache for each session.
func (s *Service) cacheRevokeAll(ctx context.Context, sessionIDs []uuid.UUID) {
	for _, id := range sessionIDs {
		s.cacheRevoke(ctx, id)
	}
}

// liveSessionIDs lists the user's live sessions, for writing the revocation
// cache once a bulk revoke that cannot report them (the password-reset
// transaction) has committed. Best-effort: a failed read is logged and the
// database revocation still holds; only the access-token lag remains.
func (s *Service) liveSessionIDs(ctx context.Context, userID uuid.UUID) []uuid.UUID {
	sessions, err := s.store.ListActiveSessions(ctx, userID)
	if err != nil {
		s.log.Warn("list live sessions failed", "user_id", userID, "err", err)
		return nil
	}
	ids := make([]uuid.UUID, 0, len(sessions))
	for _, sess := range sessions {
		ids = append(ids, sess.ID)
	}
	return ids
}
