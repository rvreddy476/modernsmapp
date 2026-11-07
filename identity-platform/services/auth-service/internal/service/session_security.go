package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// RefreshReuseGrace is how long after a rotation the refresh token it
// replaced is still honoured, once, as a retry rather than a theft.
//
// A refresh whose response is lost (a dropped connection, an app killed
// mid-request), or two tabs refreshing with the same cookie, presents the old
// token again moments after rotating it. Without a window that replay revoked
// the whole session family and signed the user out everywhere. Inside the
// window the session is rotated again instead: a fresh pair is issued and the
// successor the client never received stops working, so the session still has
// exactly one live refresh token. Past the window, once the successor has
// itself been used, or when the replayed token is such a cut-off successor, a
// replay is treated as reuse: the family is revoked. A rotated token is
// honoured this way at most once, because the retry supersedes it.
const RefreshReuseGrace = 30 * time.Second

// resolveRotatedRefreshToken handles a refresh token that is no session's
// current token. It returns the session to rotate when the token is a retry
// inside the grace window (see RefreshReuseGrace), and nil otherwise; the
// caller then refuses the refresh. A token a rotation replaced outside the
// window, or whose successor has already been used, is reuse: the session
// family is revoked (database and access-token revocation cache) and the
// event lands in the user's security inbox. An unknown hash is left alone.
func (s *Service) resolveRotatedRefreshToken(ctx context.Context, refreshTokenHash, ip, userAgent string) (*store.Session, error) {
	rotated, err := s.store.LookupRotatedRefreshToken(ctx, refreshTokenHash)
	if err != nil {
		return nil, fmt.Errorf("refresh reuse lookup: %w", err)
	}
	if rotated == nil {
		return nil, nil
	}
	sess, err := s.store.GetSessionByID(ctx, rotated.SessionID)
	if err != nil {
		return nil, fmt.Errorf("refresh reuse: load session: %w", err)
	}
	if s.isGraceRetry(rotated, sess) {
		return sess, nil
	}
	revoked, err := s.store.RevokeSessionFamily(ctx, rotated.SessionID)
	if err != nil {
		return nil, fmt.Errorf("refresh reuse: revoke family: %w", err)
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
	return nil, nil
}

// isGraceRetry reports whether a rotated-away token may be honoured as a
// retry: its session is still live, the successor it was replaced by has not
// been used, it was not itself a never-delivered successor cut off by an
// earlier retry, and the rotation is inside the grace window.
func (s *Service) isGraceRetry(rotated *store.RotatedRefreshToken, sess *store.Session) bool {
	return sess != nil && sess.RevokedAt == nil && sess.IsActive &&
		!rotated.Superseded && !rotated.ReplacedOnRetry &&
		rotated.Age < s.refreshReuseGrace
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
