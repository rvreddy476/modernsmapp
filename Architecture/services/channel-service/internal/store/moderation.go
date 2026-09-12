package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Communities invite-only pilot (2026-09-12): the moderation controls the
// founder's decision requires — member removal, ban/unban (the `banned` role
// was enforced on every read path but unreachable), a reader for
// channel_reports (nothing read that table but a rate-limit COUNT), and the
// per-channel `suspended` status the CHECK already admitted and nothing wrote.

// UnbanMember returns a banned member to plain subscriber standing. Returns
// false when the user was not banned (idempotent).
func (s *Store) UnbanMember(ctx context.Context, channelID, userID uuid.UUID) (bool, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE channel_members SET role = 'subscriber' WHERE channel_id = $1 AND user_id = $2 AND role = 'banned'`,
		channelID, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ListMembersByRole pages the roster filtered to one role — the only way a
// moderator can see who is banned, since ListSubscribers excludes them.
func (s *Store) ListMembersByRole(ctx context.Context, channelID uuid.UUID, role string, limit, offset int) ([]ChannelMember, error) {
	query := `SELECT channel_id, user_id, role, notify_on, muted_until, snoozed_until, paid, subscribed_at
		FROM channel_members WHERE channel_id = $1 AND role = $2
		ORDER BY subscribed_at DESC LIMIT $3 OFFSET $4`
	rows, err := s.db.Query(ctx, query, channelID, role, limit, offset)
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

// ListAllMembers pages the whole roster INCLUDING banned rows, so a
// moderator sees every row and its role in one pass.
func (s *Store) ListAllMembers(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]ChannelMember, error) {
	query := `SELECT channel_id, user_id, role, notify_on, muted_until, snoozed_until, paid, subscribed_at
		FROM channel_members WHERE channel_id = $1
		ORDER BY subscribed_at DESC LIMIT $2 OFFSET $3`
	rows, err := s.db.Query(ctx, query, channelID, limit, offset)
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

// --- Channel status (emergency disable, one channel) ---

// SetChannelStatus writes a lifecycle status. 'suspended' is the per-channel
// emergency disable; 'active' lifts it. Refuses to resurrect a deleted row.
func (s *Store) SetChannelStatus(ctx context.Context, channelID uuid.UUID, status string) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE broadcast_channels SET status = $2, updated_at = NOW() WHERE id = $1 AND status != 'deleted'`,
		channelID, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("channel not found")
	}
	return nil
}

// --- Report review ---

// ReportListItem is one row of the moderation queue. It carries the channel
// handle/name so a reviewer does not have to fan out per report.
type ReportListItem struct {
	ChannelReport
	ChannelHandle string     `json:"channel_handle"`
	ChannelName   string     `json:"channel_name"`
	ChannelStatus string     `json:"channel_status"`
	ReviewerID    *uuid.UUID `json:"reviewer_id,omitempty"`
	ReviewNote    string     `json:"review_note,omitempty"`
	ReviewedAt    *time.Time `json:"reviewed_at,omitempty"`
}

// ValidReportStatuses are the states the channel_reports CHECK admits.
var ValidReportStatuses = map[string]bool{"open": true, "reviewed": true, "dismissed": true}

// EncodeReportCursor packs a keyset cursor over (created_at, id) — the
// newest-first ordering of the queue. Opaque to clients.
func EncodeReportCursor(createdAt time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(createdAt.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

// DecodeReportCursor unpacks EncodeReportCursor. A junk cursor reads as "no
// cursor" rather than an error, matching the offset cursors elsewhere.
func DecodeReportCursor(cur string) (time.Time, uuid.UUID, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(cur)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, false
	}
	ts, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	return ts, id, true
}

// reportQueueOrder and reportQueueKeyset must agree: the keyset comparison
// walks the same (created_at, id) tuple the ordering sorts by, or paging
// skips or repeats rows.
const (
	reportQueueOrder  = `ORDER BY r.created_at DESC, r.id DESC`
	reportQueueKeyset = `(r.created_at, r.id) <`
)

// ListReports pages the moderation queue newest-first, optionally filtered
// by status. Keyset paginated on (created_at, id) so a queue that is being
// worked does not skip or repeat rows the way OFFSET would.
func (s *Store) ListReports(ctx context.Context, status string, limit int, cursor string) ([]ReportListItem, error) {
	query := `SELECT r.id, r.channel_id, r.update_id, r.reporter_id, r.reason, r.details, r.status, r.created_at,
			r.reviewer_id, r.review_note, r.reviewed_at,
			c.handle, c.name, c.status
		FROM channel_reports r
		JOIN broadcast_channels c ON c.id = r.channel_id
		WHERE 1 = 1`
	args := []any{}
	if status = strings.TrimSpace(strings.ToLower(status)); status != "" && status != "all" {
		args = append(args, status)
		query += fmt.Sprintf(` AND r.status = $%d`, len(args))
	}
	if ts, id, ok := DecodeReportCursor(cursor); ok {
		args = append(args, ts, id)
		query += fmt.Sprintf(` AND `+reportQueueKeyset+` ($%d, $%d)`, len(args)-1, len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(` `+reportQueueOrder+` LIMIT $%d`, len(args))

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReportListItem{}
	for rows.Next() {
		var it ReportListItem
		var note *string
		if err := rows.Scan(&it.ID, &it.ChannelID, &it.UpdateID, &it.ReporterID, &it.Reason, &it.Details,
			&it.Status, &it.CreatedAt, &it.ReviewerID, &note, &it.ReviewedAt,
			&it.ChannelHandle, &it.ChannelName, &it.ChannelStatus); err != nil {
			return nil, err
		}
		if note != nil {
			it.ReviewNote = *note
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ReviewReport writes the status nothing wrote before, plus who reviewed it
// and when. Returns the updated row, or nil when the id is unknown.
func (s *Store) ReviewReport(ctx context.Context, reportID, reviewerID uuid.UUID, status, note string) (*ChannelReport, error) {
	var r ChannelReport
	err := s.db.QueryRow(ctx, `
		UPDATE channel_reports
		SET status = $2, reviewer_id = $3, review_note = $4, reviewed_at = NOW()
		WHERE id = $1
		RETURNING id, channel_id, update_id, reporter_id, reason, details, status, created_at
	`, reportID, status, reviewerID, note).Scan(
		&r.ID, &r.ChannelID, &r.UpdateID, &r.ReporterID, &r.Reason, &r.Details, &r.Status, &r.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}
