package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// GroupBan represents a banned user in a group.
type GroupBan struct {
	ID        uuid.UUID  `json:"id"`
	GroupID   uuid.UUID  `json:"group_id"`
	UserID    string     `json:"user_id"`
	BannedBy  string     `json:"banned_by"`
	Reason    *string    `json:"reason,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// Engagement methods moved to group.go (V2 section) with full idempotency + unspark/unstash

func (s *Store) ApproveGroupPost(ctx context.Context, id uuid.UUID, approvedBy string) error {
	now := time.Now()
	_, err := s.db.Exec(ctx, `UPDATE group_posts SET status='published', approved_by=$2, approved_at=$3, updated_at=$3 WHERE id=$1`, id, approvedBy, now)
	return err
}

func (s *Store) RejectGroupPost(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE group_posts SET status='rejected', updated_at=NOW() WHERE id=$1`, id)
	return err
}

func (s *Store) PinGroupPost(ctx context.Context, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE group_posts SET is_pinned=TRUE, updated_at=NOW() WHERE id=$1`, postID)
	return err
}

// --- Bans ---

func (s *Store) BanGroupUser(ctx context.Context, groupID uuid.UUID, userID, bannedBy, reason string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO group_bans (group_id, user_id, banned_by, reason) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, groupID, userID, bannedBy, reason)
	if err != nil {
		return err
	}
	_, _ = s.db.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`, groupID, userID)
	_, _ = s.db.Exec(ctx, `UPDATE groups SET member_count = member_count - 1 WHERE id = $1`, groupID)
	return nil
}

func (s *Store) UnbanGroupUser(ctx context.Context, groupID uuid.UUID, userID string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM group_bans WHERE group_id = $1 AND user_id = $2`, groupID, userID)
	return err
}

func (s *Store) ListGroupBans(ctx context.Context, groupID uuid.UUID) ([]GroupBan, error) {
	rows, err := s.db.Query(ctx, `SELECT id, group_id, user_id, banned_by, reason, expires_at, created_at FROM group_bans WHERE group_id = $1 ORDER BY created_at DESC`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var bans []GroupBan
	for rows.Next() {
		var b GroupBan
		if err := rows.Scan(&b.ID, &b.GroupID, &b.UserID, &b.BannedBy, &b.Reason, &b.ExpiresAt, &b.CreatedAt); err != nil {
			return nil, err
		}
		bans = append(bans, b)
	}
	return bans, nil
}

// --- Join Requests (new methods that don't conflict with existing) ---

func (s *Store) ApproveJoinRequestByID(ctx context.Context, reqID uuid.UUID, reviewedBy string) error {
	now := time.Now()
	_, err := s.db.Exec(ctx, `UPDATE group_join_requests SET status='approved', reviewed_by=$2, reviewed_at=$3 WHERE id=$1`, reqID, reviewedBy, now)
	return err
}

func (s *Store) DeclineJoinRequestByID(ctx context.Context, reqID uuid.UUID, reviewedBy string) error {
	now := time.Now()
	_, err := s.db.Exec(ctx, `UPDATE group_join_requests SET status='declined', reviewed_by=$2, reviewed_at=$3 WHERE id=$1`, reqID, reviewedBy, now)
	return err
}

func (s *Store) ListPendingJoinRequests(ctx context.Context, groupID uuid.UUID) ([]GroupJoinRequest, error) {
	rows, err := s.db.Query(ctx, `SELECT id, group_id, user_id, status, reviewed_by_user_id, reviewed_at, created_at FROM group_join_requests WHERE group_id = $1 AND status = 'pending' ORDER BY created_at`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var reqs []GroupJoinRequest
	for rows.Next() {
		var r GroupJoinRequest
		if err := rows.Scan(&r.ID, &r.GroupID, &r.UserID, &r.Status, &r.ReviewedByUserID, &r.ReviewedAt, &r.CreatedAt); err != nil {
			return nil, err
		}
		reqs = append(reqs, r)
	}
	return reqs, nil
}

// --- Create invite ---

func (s *Store) CreateGroupInvite(ctx context.Context, groupID uuid.UUID, inviterID, inviteeID, message string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO group_invites (group_id, inviter_id, invitee_id, message) VALUES ($1,$2,$3,$4) ON CONFLICT (group_id, invitee_id) DO NOTHING`,
		groupID, inviterID, inviteeID, message)
	return err
}

// --- Group reports ---

func (s *Store) CreateGroupReport(ctx context.Context, groupID uuid.UUID, reporterID, targetType string, targetID uuid.UUID, reason, description string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO group_reports (group_id, reporter_id, target_type, target_id, reason, description) VALUES ($1,$2,$3,$4,$5,$6)`,
		groupID, reporterID, targetType, targetID, reason, description)
	return err
}

func (s *Store) ListGroupReports(ctx context.Context, groupID uuid.UUID) ([]map[string]any, error) {
	rows, err := s.db.Query(ctx, `SELECT id, reporter_id, target_type, target_id, reason, description, status, created_at FROM group_reports WHERE group_id = $1 AND status = 'pending' ORDER BY created_at`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var reports []map[string]any
	for rows.Next() {
		var id, targetID uuid.UUID
		var reporterID, targetType, reason, status string
		var description *string
		var createdAt time.Time
		if err := rows.Scan(&id, &reporterID, &targetType, &targetID, &reason, &description, &status, &createdAt); err != nil {
			return nil, err
		}
		reports = append(reports, map[string]any{
			"id": id, "reporter_id": reporterID, "target_type": targetType, "target_id": targetID,
			"reason": reason, "description": description, "status": status, "created_at": createdAt,
		})
	}
	return reports, nil
}

// suppress unused import
var _ = json.RawMessage{}

// GroupInviteDetail is a pending invite enriched with the target group's
// display fields so clients can render an invite card without extra lookups.
type GroupInviteDetail struct {
	GroupInvite
	GroupName          string     `json:"group_name"`
	GroupAvatarMediaID *uuid.UUID `json:"group_avatar_media_id,omitempty"`
	GroupMemberCount   int64      `json:"group_member_count"`
}

// ListInvitesForUserDetailed returns the user's pending invites joined with
// the group name/avatar/member count.
func (s *Store) ListInvitesForUserDetailed(ctx context.Context, userID uuid.UUID) ([]GroupInviteDetail, error) {
	rows, err := s.db.Query(ctx, `
		SELECT i.id, i.group_id, i.inviter_id, i.invitee_id, i.status, i.created_at, i.updated_at, i.expires_at,
		       g.name, g.avatar_media_id, g.member_count
		FROM group_invites i
		JOIN groups g ON g.id = i.group_id
		WHERE i.invitee_id = $1 AND i.status = 'pending' AND g.is_archived = FALSE
		ORDER BY i.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invites []GroupInviteDetail
	for rows.Next() {
		var inv GroupInviteDetail
		if err := rows.Scan(&inv.ID, &inv.GroupID, &inv.InviterID, &inv.InviteeID, &inv.Status,
			&inv.CreatedAt, &inv.UpdatedAt, &inv.ExpiresAt,
			&inv.GroupName, &inv.GroupAvatarMediaID, &inv.GroupMemberCount); err != nil {
			return nil, err
		}
		invites = append(invites, inv)
	}
	return invites, rows.Err()
}

// ListMyGroupsFeed returns published posts across every group the user is a
// member of, newest first — powers the aggregated MySpace "Your feed".
func (s *Store) ListMyGroupsFeed(ctx context.Context, userID uuid.UUID, limit, offset int) ([]GroupPostV2, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.group_id, p.channel_id, p.author_id, p.content_type, p.title, p.body, p.body_html,
		       p.type_payload, p.attachments, p.needs_approval, p.is_pinned, p.is_announcement, p.status,
		       p.spark_count, p.comment_count, p.echo_count, p.view_count, p.created_at, p.updated_at
		FROM group_posts p
		JOIN group_members m ON m.group_id = p.group_id AND m.user_id = $1
		WHERE p.status = 'published'
		ORDER BY p.created_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []GroupPostV2
	for rows.Next() {
		var p GroupPostV2
		if err := rows.Scan(&p.ID, &p.GroupID, &p.ChannelID, &p.AuthorID, &p.ContentType,
			&p.Title, &p.Body, &p.BodyHTML, &p.TypePayload, &p.Attachments,
			&p.NeedsApproval, &p.IsPinned, &p.IsAnnouncement, &p.Status,
			&p.SparkCount, &p.CommentCount, &p.EchoCount, &p.ViewCount,
			&p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if p.TypePayload == nil {
			p.TypePayload = json.RawMessage(`{}`)
		}
		if p.Attachments == nil {
			p.Attachments = json.RawMessage(`[]`)
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}
