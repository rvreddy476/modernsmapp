package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/*
	Offline copies inside the app (2026-10-02; migration 060).

	post_offline_copies is the record of who was granted a copy of a video,
	on which device and until when. Storage only: who may be granted one,
	and whether a stored copy is still valid, are the service's decisions
	(service/offline_copies.go), made from the post as it stands.

	Nothing a user does deletes a row: removing a copy, deleting the post,
	turning downloads off and making the post private set revoked_at with a
	reason. The rows go with a hard-deleted post (ON DELETE CASCADE) and
	with a purged user (PurgeUser).
*/

// Revoke reasons written to post_offline_copies.revoke_reason.
const (
	OfflineRevokeRemoved    = "removed"     // the viewer removed the copy
	OfflineRevokeDeleted    = "deleted"     // the post was deleted
	OfflineRevokeNotAllowed = "not_allowed" // the creator turned downloads off
	OfflineRevokePrivate    = "private"     // the post is no longer theirs to watch
	OfflineRevokeBlocked    = "blocked"     // a block between viewer and author
)

// OfflineCopy is one row of post_offline_copies.
type OfflineCopy struct {
	UserID        uuid.UUID
	PostID        uuid.UUID
	DeviceID      string
	GrantedAt     time.Time
	ExpiresAt     time.Time
	LastCheckedAt *time.Time
	RevokedAt     *time.Time
	RevokeReason  string
	// Card is what the grant handed the device (the media rendition, the
	// caption tracks, the sound, the poster), kept so the Offline list can
	// be rebuilt without asking media-service about every row again.
	Card json.RawMessage
}

// Active reports a copy that is neither revoked nor expired at now.
func (c OfflineCopy) Active(now time.Time) bool {
	return c.RevokedAt == nil && c.ExpiresAt.After(now)
}

// ErrOfflineCopyLimit: the user already holds the maximum number of active
// copies and this grant would add one.
var ErrOfflineCopyLimit = errors.New("offline copy limit reached")

const offlineCopyCols = `user_id, post_id, device_id, granted_at, expires_at,
	last_checked_at, revoked_at, COALESCE(revoke_reason, ''), COALESCE(grant_card, '{}'::jsonb)`

func scanOfflineCopy(row pgx.Row) (*OfflineCopy, error) {
	var c OfflineCopy
	var card []byte
	if err := row.Scan(&c.UserID, &c.PostID, &c.DeviceID, &c.GrantedAt, &c.ExpiresAt,
		&c.LastCheckedAt, &c.RevokedAt, &c.RevokeReason, &card); err != nil {
		return nil, err
	}
	c.Card = card
	return &c, nil
}

// GrantOfflineCopy writes (or refreshes) the copy of postID on deviceID for
// userID, expiring at expiresAt. It returns the row and whether the grant
// made a copy active that was not (false = an active copy was refreshed).
//
// limit is the most active copies one user may hold across all devices; a
// grant that would add an active copy beyond it is ErrOfflineCopyLimit and
// writes nothing. Refreshing an already-active copy never counts against
// the limit. The count and the write are one transaction under a per-user
// advisory lock, so two concurrent grants cannot both take the last slot.
func (s *Store) GrantOfflineCopy(ctx context.Context, userID, postID uuid.UUID, deviceID string, now, expiresAt time.Time, limit int, card json.RawMessage) (*OfflineCopy, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('post_offline_copies:' || $1::text, 0))`, userID); err != nil {
		return nil, false, fmt.Errorf("offline copy lock: %w", err)
	}

	wasActive := false
	existing, err := scanOfflineCopy(tx.QueryRow(ctx, `
		SELECT `+offlineCopyCols+` FROM post_offline_copies
		WHERE user_id = $1 AND post_id = $2 AND device_id = $3 FOR UPDATE`, userID, postID, deviceID))
	switch {
	case err == nil:
		wasActive = existing.Active(now)
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return nil, false, fmt.Errorf("offline copy read: %w", err)
	}

	if !wasActive {
		var active int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM post_offline_copies
			WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > $2`, userID, now).Scan(&active); err != nil {
			return nil, false, fmt.Errorf("offline copy count: %w", err)
		}
		if active >= limit {
			return nil, false, ErrOfflineCopyLimit
		}
	}

	if len(card) == 0 {
		card = json.RawMessage(`{}`)
	}
	// An active copy keeps its granted_at (it is the same copy, refreshed);
	// a revoked or expired one is granted anew.
	row, err := scanOfflineCopy(tx.QueryRow(ctx, `
		INSERT INTO post_offline_copies (user_id, post_id, device_id, granted_at, expires_at, last_checked_at, grant_card)
		VALUES ($1, $2, $3, $4, $5, $4, $7::jsonb)
		ON CONFLICT (user_id, post_id, device_id) DO UPDATE SET
			granted_at      = CASE WHEN $6::boolean THEN post_offline_copies.granted_at ELSE EXCLUDED.granted_at END,
			expires_at      = EXCLUDED.expires_at,
			last_checked_at = EXCLUDED.last_checked_at,
			revoked_at      = NULL,
			revoke_reason   = NULL,
			grant_card      = EXCLUDED.grant_card
		RETURNING `+offlineCopyCols, userID, postID, deviceID, now, expiresAt, wasActive, []byte(card)))
	if err != nil {
		return nil, false, fmt.Errorf("offline copy write: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return row, !wasActive, nil
}

// OfflineCopiesForPosts returns the caller's rows on one device for the
// given posts, revoked and expired ones included (the service tells them
// apart). A post with no row is absent from the map.
func (s *Store) OfflineCopiesForPosts(ctx context.Context, userID uuid.UUID, deviceID string, postIDs []uuid.UUID) (map[uuid.UUID]OfflineCopy, error) {
	out := make(map[uuid.UUID]OfflineCopy, len(postIDs))
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+offlineCopyCols+` FROM post_offline_copies
		WHERE user_id = $1 AND device_id = $2 AND post_id = ANY($3)`, userID, deviceID, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanOfflineCopy(rows)
		if err != nil {
			return nil, err
		}
		out[c.PostID] = *c
	}
	return out, rows.Err()
}

// ListActiveOfflineCopies returns the caller's copies on one device that are
// neither revoked nor expired at now, newest grant first.
func (s *Store) ListActiveOfflineCopies(ctx context.Context, userID uuid.UUID, deviceID string, now time.Time) ([]OfflineCopy, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+offlineCopyCols+` FROM post_offline_copies
		WHERE user_id = $1 AND device_id = $2 AND revoked_at IS NULL AND expires_at > $3
		ORDER BY granted_at DESC, post_id ASC`, userID, deviceID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OfflineCopy{}
	for rows.Next() {
		c, err := scanOfflineCopy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// TouchOfflineCopies stamps last_checked_at on the caller's copies of
// postIDs on one device. It never moves expires_at: a check is not a grant.
func (s *Store) TouchOfflineCopies(ctx context.Context, userID uuid.UUID, deviceID string, postIDs []uuid.UUID, now time.Time) error {
	if len(postIDs) == 0 {
		return nil
	}
	_, err := s.db.Exec(ctx, `
		UPDATE post_offline_copies SET last_checked_at = $4
		WHERE user_id = $1 AND device_id = $2 AND post_id = ANY($3)`, userID, deviceID, postIDs, now)
	return err
}

// RevokeOfflineCopies marks the caller's copies of postIDs on one device
// revoked with reason. Idempotent: a row already revoked keeps its first
// revoked_at and reason, and a post with no row is not an error. Returns
// how many rows it revoked.
func (s *Store) RevokeOfflineCopies(ctx context.Context, userID uuid.UUID, deviceID string, postIDs []uuid.UUID, reason string, now time.Time) (int64, error) {
	if len(postIDs) == 0 {
		return 0, nil
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE post_offline_copies SET revoked_at = $5, revoke_reason = $4
		WHERE user_id = $1 AND device_id = $2 AND post_id = ANY($3) AND revoked_at IS NULL`,
		userID, deviceID, postIDs, reason, now)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RevokePostOfflineCopiesTx marks every unrevoked copy of the given posts
// revoked, inside the transaction that changed the post. keepUserID's own
// copies are left alone (the owner may always keep a copy of their own
// post; uuid.Nil keeps nobody's). keepShared also leaves the copies of the
// users on each post's private share list, who can still watch a post that
// was just made private.
func RevokePostOfflineCopiesTx(ctx context.Context, tx pgx.Tx, postIDs []uuid.UUID, reason string, keepUserID uuid.UUID, keepShared bool) error {
	if len(postIDs) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE post_offline_copies c SET revoked_at = NOW(), revoke_reason = $2
		WHERE c.post_id = ANY($1) AND c.revoked_at IS NULL
		  AND c.user_id <> $3
		  AND NOT ($4::boolean AND EXISTS (
		        SELECT 1 FROM post_private_shares ps
		        WHERE ps.post_id = c.post_id AND ps.user_id = c.user_id))`,
		postIDs, reason, keepUserID, keepShared)
	if err != nil {
		return fmt.Errorf("revoke offline copies (%s): %w", reason, err)
	}
	return nil
}
