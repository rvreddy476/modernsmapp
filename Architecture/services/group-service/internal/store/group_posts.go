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

// --- Engagement on group_posts table ---

func (s *Store) SparkGroupPost(ctx context.Context, postID uuid.UUID, userID string, isSupernova bool) error {
	weight := 1
	if isSupernova {
		weight = 5
	}
	_, err := s.db.Exec(ctx, `INSERT INTO group_post_sparks (post_id, user_id, is_supernova) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, postID, userID, isSupernova)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `UPDATE group_posts SET spark_count = spark_count + $2 WHERE id = $1`, postID, weight)
	return err
}

func (s *Store) StashGroupPost(ctx context.Context, postID uuid.UUID, userID string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO group_post_stashes (post_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, postID, userID)
	return err
}

func (s *Store) RecordGroupPostView(ctx context.Context, postID uuid.UUID, userID string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO group_post_views (post_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, postID, userID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `UPDATE group_posts SET view_count = view_count + 1 WHERE id = $1`, postID)
	return err
}

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
