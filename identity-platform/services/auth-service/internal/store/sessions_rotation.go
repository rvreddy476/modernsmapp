package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Refresh-token rotation, reuse detection and session cleanup.
//
// A refresh token is single use. Rotating it is conditional on the row still
// holding the hash that was presented, so of two concurrent refreshes with the
// same token exactly one wins, and a refresh that races logout (the row is
// revoked between the read and the write) rotates nothing. Every hash that is
// rotated away is remembered in auth.refresh_token_history; presenting one of
// those again is a replay of a stolen or leaked token, and the caller revokes
// the whole session family (see service.refreshSessionOfKind).

// RotateSessionWithFingerprint replaces the refresh token hash oldHash with
// newHash on a live session, sets the new expiry, last_refresh_ip and
// anomaly_flagged, and records oldHash in auth.refresh_token_history, all in
// one statement. It reports rotated=false when the row no longer holds
// oldHash, or is revoked or inactive: a concurrent refresh won, or logout got
// there first. The caller must then issue nothing.
func (s *Store) RotateSessionWithFingerprint(ctx context.Context, sessionID uuid.UUID, oldHash, newHash, ip string, expiresAt time.Time, anomalyFlagged bool) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		WITH rotated AS (
			UPDATE auth.sessions
			SET refresh_token_hash = $2,
			    expires_at = $3,
			    last_refresh_at = NOW(),
			    last_refresh_ip = NULLIF($4, ''),
			    anomaly_flagged = $5
			WHERE session_id = $1
			  AND refresh_token_hash = $6
			  AND is_active = TRUE
			  AND revoked_at IS NULL
			RETURNING session_id, COALESCE(family_id, session_id) AS family_id
		)
		INSERT INTO auth.refresh_token_history (refresh_token_hash, session_id, family_id)
		SELECT $6, session_id, family_id FROM rotated
	`, sessionID, newHash, expiresAt, ip, anomalyFlagged, oldHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// LookupRotatedRefreshToken returns the session that a rotated-away refresh
// token belonged to, or nil when the hash was never rotated through this
// table (unknown token, or its session has been purged).
func (s *Store) LookupRotatedRefreshToken(ctx context.Context, refreshTokenHash string) (*uuid.UUID, error) {
	var sessionID uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT session_id FROM auth.refresh_token_history WHERE refresh_token_hash = $1`,
		refreshTokenHash).Scan(&sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sessionID, nil
}

// RevokeSessionFamily revokes the session and every session sharing its
// family, and returns the ids it revoked so the caller can write the
// access-token revocation cache for each.
func (s *Store) RevokeSessionFamily(ctx context.Context, sessionID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		UPDATE auth.sessions
		SET revoked_at = NOW(), is_active = FALSE
		WHERE revoked_at IS NULL
		  AND (session_id = $1
		       OR family_id = (SELECT COALESCE(family_id, session_id) FROM auth.sessions WHERE session_id = $1))
		RETURNING session_id
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// PurgeDeadSessions deletes at most limit sessions that expired, or were
// revoked, more than retention ago. Rotation history goes with them
// (ON DELETE CASCADE). SKIP LOCKED lets several replicas run it at once
// without blocking each other or a live refresh. Returns rows deleted.
func (s *Store) PurgeDeadSessions(ctx context.Context, retention time.Duration, limit int) (int64, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("purge dead sessions: limit must be positive")
	}
	if retention < 0 {
		return 0, fmt.Errorf("purge dead sessions: retention must not be negative")
	}
	cutoff := time.Now().Add(-retention)
	tag, err := s.db.Exec(ctx, `
		DELETE FROM auth.sessions
		WHERE session_id IN (
			SELECT session_id FROM auth.sessions
			WHERE expires_at < $1
			   OR (revoked_at IS NOT NULL AND revoked_at < $1)
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
	`, cutoff, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
