package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Chat-app pass (2026-09-05): the community surface — discover with a text
// filter, admins, one-emoji-per-user reactions, reports.

// ReactionCount is one emoji's tally on an update.
type ReactionCount struct {
	Emoji string `json:"emoji"`
	Count int64  `json:"count"`
}

// ChannelReport is a trust & safety intake row.
type ChannelReport struct {
	ID         uuid.UUID  `json:"id"`
	ChannelID  uuid.UUID  `json:"channel_id"`
	UpdateID   *uuid.UUID `json:"update_id,omitempty"`
	ReporterID uuid.UUID  `json:"reporter_id"`
	Reason     string     `json:"reason"`
	Details    string     `json:"details,omitempty"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
}

// DiscoverChannelsFiltered lists public-class active channels ordered by
// subscriber count then recency, optionally filtered by a case-insensitive
// substring on name or handle. Returns limit+1 rows so the caller can tell
// whether a next page exists.
func (s *Store) DiscoverChannelsFiltered(ctx context.Context, q string, limit, offset int) ([]BroadcastChannel, error) {
	query := `SELECT id, owner_id, handle, name, description, avatar_media_id, banner_media_id,
		channel_type, category, language, comment_mode, reaction_mode,
		forward_allowed, paid_access, subscription_price_cents,
		post_schedule_enabled, subscriber_count_visible, allow_preview_posts,
		is_verified, subscriber_count, update_count, status, created_at, updated_at, deleted_at
		FROM broadcast_channels
		WHERE status = 'active' AND channel_type IN ('public','creator','brand','education','official','topic')`
	args := []any{}
	if q = strings.TrimSpace(q); q != "" {
		args = append(args, "%"+strings.ToLower(q)+"%")
		query += fmt.Sprintf(` AND (lower(name) LIKE $%d OR lower(handle) LIKE $%d)`, len(args), len(args))
	}
	args = append(args, limit+1, offset)
	query += fmt.Sprintf(` ORDER BY subscriber_count DESC, created_at DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args))
	return s.scanChannels(ctx, query, args...)
}

// ListAdmins returns the owner and every admin of a channel.
func (s *Store) ListAdmins(ctx context.Context, channelID uuid.UUID) ([]ChannelMember, error) {
	query := `SELECT channel_id, user_id, role, notify_on, muted_until, snoozed_until, paid, subscribed_at
		FROM channel_members WHERE channel_id = $1 AND role IN ('owner','admin')
		ORDER BY CASE role WHEN 'owner' THEN 0 ELSE 1 END, subscribed_at ASC`
	rows, err := s.db.Query(ctx, query, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []ChannelMember{}
	for rows.Next() {
		var m ChannelMember
		if err := rows.Scan(&m.ChannelID, &m.UserID, &m.Role, &m.NotifyOn, &m.MutedUntil, &m.SnoozedUntil, &m.Paid, &m.SubscribedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// SetReaction records the viewer's single emoji on an update (replacing a
// previous one) and re-materialises reaction_count. Returns the previous
// emoji ("" when none).
func (s *Store) SetReaction(ctx context.Context, updateID, userID uuid.UUID, emoji string) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var previous string
	err = tx.QueryRow(ctx, `SELECT emoji FROM update_reactions WHERE update_id = $1 AND user_id = $2 FOR UPDATE`,
		updateID, userID).Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO update_reactions (update_id, user_id, emoji) VALUES ($1, $2, $3)
		ON CONFLICT (update_id, user_id) DO UPDATE SET emoji = EXCLUDED.emoji, created_at = NOW()
	`, updateID, userID, emoji); err != nil {
		return "", err
	}
	if err := syncReactionCount(ctx, tx, updateID); err != nil {
		return "", err
	}
	return previous, tx.Commit(ctx)
}

// RemoveReaction deletes the viewer's reaction. Idempotent; returns whether
// a row existed.
func (s *Store) RemoveReaction(ctx context.Context, updateID, userID uuid.UUID) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `DELETE FROM update_reactions WHERE update_id = $1 AND user_id = $2`, updateID, userID)
	if err != nil {
		return false, err
	}
	if err := syncReactionCount(ctx, tx, updateID); err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, tx.Commit(ctx)
}

func syncReactionCount(ctx context.Context, tx pgx.Tx, updateID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE channel_updates SET reaction_count = (SELECT COUNT(*) FROM update_reactions WHERE update_id = $1),
		    updated_at = NOW()
		WHERE id = $1
	`, updateID)
	return err
}

// GetReactionCounts returns per-emoji tallies for a batch of updates, ordered
// by count desc within each update.
func (s *Store) GetReactionCounts(ctx context.Context, updateIDs []uuid.UUID) (map[uuid.UUID][]ReactionCount, error) {
	out := make(map[uuid.UUID][]ReactionCount, len(updateIDs))
	if len(updateIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT update_id, emoji, COUNT(*) FROM update_reactions
		WHERE update_id = ANY($1)
		GROUP BY update_id, emoji
		ORDER BY update_id, COUNT(*) DESC, emoji
	`, updateIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var rc ReactionCount
		if err := rows.Scan(&id, &rc.Emoji, &rc.Count); err != nil {
			return nil, err
		}
		out[id] = append(out[id], rc)
	}
	return out, rows.Err()
}

// GetViewerReactions returns the viewer's emoji per update for a batch.
func (s *Store) GetViewerReactions(ctx context.Context, userID uuid.UUID, updateIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	out := make(map[uuid.UUID]string, len(updateIDs))
	if len(updateIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT update_id, emoji FROM update_reactions WHERE user_id = $1 AND update_id = ANY($2)
	`, userID, updateIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var emoji string
		if err := rows.Scan(&id, &emoji); err != nil {
			return nil, err
		}
		out[id] = emoji
	}
	return out, rows.Err()
}

// CreateReport files a report against a channel or one of its updates.
func (s *Store) CreateReport(ctx context.Context, channelID uuid.UUID, updateID *uuid.UUID, reporterID uuid.UUID, reason, details string) (*ChannelReport, error) {
	r := &ChannelReport{ChannelID: channelID, UpdateID: updateID, ReporterID: reporterID, Reason: reason, Details: details}
	err := s.db.QueryRow(ctx, `
		INSERT INTO channel_reports (channel_id, update_id, reporter_id, reason, details)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, status, created_at
	`, channelID, updateID, reporterID, reason, details).Scan(&r.ID, &r.Status, &r.CreatedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// CountRecentReportsBy bounds report spam (per reporter, per window).
func (s *Store) CountRecentReportsBy(ctx context.Context, reporterID uuid.UUID, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM channel_reports WHERE reporter_id = $1 AND created_at >= $2`, reporterID, since).Scan(&n)
	return n, err
}
