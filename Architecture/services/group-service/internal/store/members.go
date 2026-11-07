package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

type GroupMember struct {
	GroupID         uuid.UUID  `json:"group_id"`
	UserID          uuid.UUID  `json:"user_id"`
	Role            string     `json:"role"`
	JoinedAt        time.Time  `json:"joined_at"`
	ID              uuid.UUID  `json:"id,omitempty"`
	InvitedByUserID *uuid.UUID `json:"invited_by_user_id,omitempty"`
	Status          string     `json:"status"`
	RemovedAt       *time.Time `json:"removed_at,omitempty"`
	RemovedByUserID *uuid.UUID `json:"removed_by_user_id,omitempty"`
	RemovalReason   *string    `json:"removal_reason,omitempty"`
}

// GetActiveMemberCount returns the count of active members in a group.
func (s *Store) GetActiveMemberCount(ctx context.Context, groupID uuid.UUID) (int64, error) {
	var count int64
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM group_members WHERE group_id = $1 AND status = 'active'`,
		groupID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count active members: %w", err)
	}
	return count, nil
}

// --- Members ---

// AddMember adds a user to a group with the given role.
// ErrGroupFull is returned by AddMember when maxMembers > 0 and the
// group is already at capacity. Audit HG6: previously the service
// checked GetActiveMemberCount and then called AddMember in a separate
// statement — two concurrent joins could both pass the check and push
// the group over its max.
var ErrGroupFull = fmt.Errorf("group has reached its maximum member limit")

func (s *Store) AddMember(ctx context.Context, groupID, userID uuid.UUID, role string) error {
	return s.AddMemberWithMax(ctx, groupID, userID, role, 0)
}

// AddMemberWithMax performs the membership insert atomically against
// the active-member count. maxMembers == 0 disables the cap (matches
// the existing schema's "0 means unlimited"). When the cap would be
// exceeded the function returns ErrGroupFull and leaves the row alone.
func (s *Store) AddMemberWithMax(ctx context.Context, groupID, userID uuid.UUID, role string, maxMembers int) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if maxMembers > 0 {
		// Lock the count for the duration of the transaction. The
		// CTE is keyed by group_id so concurrent inserts to other
		// groups don't block this one.
		var active int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(*) FROM group_members
			WHERE group_id = $1 AND status = 'active'
			FOR UPDATE
		`, groupID).Scan(&active); err != nil {
			return err
		}
		// If the actor is already an inactive member we'll reactivate,
		// which doesn't change the active count — allow regardless.
		var existingActive bool
		_ = tx.QueryRow(ctx, `
			SELECT status = 'active' FROM group_members
			WHERE group_id = $1 AND user_id = $2
		`, groupID, userID).Scan(&existingActive)
		if !existingActive && active >= maxMembers {
			return ErrGroupFull
		}
	}

	cmdTag, err := tx.Exec(ctx, `
		INSERT INTO group_members (group_id, user_id, role, status)
		VALUES ($1, $2, $3, 'active')
		ON CONFLICT (group_id, user_id) DO UPDATE SET status = 'active', role = $3, removed_at = NULL, removed_by_user_id = NULL
		WHERE group_members.status != 'active'
	`, groupID, userID, role)
	if err != nil {
		return err
	}

	if cmdTag.RowsAffected() > 0 {
		_, err = tx.Exec(ctx, `
			UPDATE groups SET member_count = member_count + 1, updated_at = NOW() WHERE id = $1
		`, groupID)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// AddMemberWithInviter adds a user to a group with the given role and tracks who invited them.
func (s *Store) AddMemberWithInviter(ctx context.Context, groupID, userID uuid.UUID, role string, invitedBy uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	cmdTag, err := tx.Exec(ctx, `
		INSERT INTO group_members (group_id, user_id, role, status, invited_by_user_id)
		VALUES ($1, $2, $3, 'active', $4)
		ON CONFLICT (group_id, user_id) DO UPDATE SET status = 'active', role = $3, invited_by_user_id = $4, removed_at = NULL, removed_by_user_id = NULL
		WHERE group_members.status != 'active'
	`, groupID, userID, role, invitedBy)
	if err != nil {
		return err
	}

	if cmdTag.RowsAffected() > 0 {
		_, err = tx.Exec(ctx, `
			UPDATE groups SET member_count = member_count + 1, updated_at = NOW() WHERE id = $1
		`, groupID)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// RemoveMember soft-removes a user from a group (status='left').
func (s *Store) RemoveMember(ctx context.Context, groupID, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	cmdTag, err := tx.Exec(ctx, `
		UPDATE group_members SET status = 'left', removed_at = NOW()
		WHERE group_id = $1 AND user_id = $2 AND status = 'active'
	`, groupID, userID)
	if err != nil {
		return err
	}

	if cmdTag.RowsAffected() > 0 {
		_, err = tx.Exec(ctx, `
			UPDATE groups SET member_count = GREATEST(member_count - 1, 0), updated_at = NOW() WHERE id = $1
		`, groupID)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// BanMember sets a member's status to 'banned'.
func (s *Store) BanMember(ctx context.Context, groupID, userID, removedBy uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	cmdTag, err := tx.Exec(ctx, `
		UPDATE group_members SET status = 'banned', removed_at = NOW(), removed_by_user_id = $3
		WHERE group_id = $1 AND user_id = $2 AND status = 'active'
	`, groupID, userID, removedBy)
	if err != nil {
		return err
	}

	if cmdTag.RowsAffected() > 0 {
		_, err = tx.Exec(ctx, `
			UPDATE groups SET member_count = GREATEST(member_count - 1, 0), updated_at = NOW() WHERE id = $1
		`, groupID)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// KickMember sets a member's status to 'removed' with the actor who removed them.
func (s *Store) KickMember(ctx context.Context, groupID, userID, removedBy uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	cmdTag, err := tx.Exec(ctx, `
		UPDATE group_members SET status = 'removed', removed_at = NOW(), removed_by_user_id = $3
		WHERE group_id = $1 AND user_id = $2 AND status = 'active'
	`, groupID, userID, removedBy)
	if err != nil {
		return err
	}

	if cmdTag.RowsAffected() > 0 {
		_, err = tx.Exec(ctx, `
			UPDATE groups SET member_count = GREATEST(member_count - 1, 0), updated_at = NOW() WHERE id = $1
		`, groupID)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// UpdateMemberRole changes a member's role within a group.
func (s *Store) UpdateMemberRole(ctx context.Context, groupID, userID uuid.UUID, role string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE group_members SET role = $3 WHERE group_id = $1 AND user_id = $2 AND status = 'active'
	`, groupID, userID, role)
	return err
}

// GetMember returns a group member by primary key.
func (s *Store) GetMember(ctx context.Context, groupID, userID uuid.UUID) (*GroupMember, error) {
	var m GroupMember
	err := s.db.QueryRow(ctx, `
		SELECT group_id, user_id, role, joined_at, id, invited_by_user_id, status, removed_at, removed_by_user_id, removal_reason
		FROM group_members WHERE group_id = $1 AND user_id = $2
	`, groupID, userID).Scan(&m.GroupID, &m.UserID, &m.Role, &m.JoinedAt,
		&m.ID, &m.InvitedByUserID, &m.Status, &m.RemovedAt, &m.RemovedByUserID, &m.RemovalReason)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// GetActiveMember returns an active group member.
func (s *Store) GetActiveMember(ctx context.Context, groupID, userID uuid.UUID) (*GroupMember, error) {
	var m GroupMember
	err := s.db.QueryRow(ctx, `
		SELECT group_id, user_id, role, joined_at, id, invited_by_user_id, status, removed_at, removed_by_user_id, removal_reason
		FROM group_members WHERE group_id = $1 AND user_id = $2 AND status = 'active'
	`, groupID, userID).Scan(&m.GroupID, &m.UserID, &m.Role, &m.JoinedAt,
		&m.ID, &m.InvitedByUserID, &m.Status, &m.RemovedAt, &m.RemovedByUserID, &m.RemovalReason)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// CheckMembership returns true if the user is an active member of the group.
func (s *Store) CheckMembership(ctx context.Context, groupID, userID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2 AND status = 'active')
	`, groupID, userID).Scan(&exists)
	return exists, err
}

// CheckBanned returns true if the user is banned from the group.
func (s *Store) CheckBanned(ctx context.Context, groupID, userID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2 AND status = 'banned')
	`, groupID, userID).Scan(&exists)
	return exists, err
}

// ListMembers returns paginated active group members.
func (s *Store) ListMembers(ctx context.Context, groupID uuid.UUID, limit, offset int) ([]GroupMember, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT group_id, user_id, role, joined_at, id, invited_by_user_id, status, removed_at, removed_by_user_id, removal_reason
		FROM group_members
		WHERE group_id = $1 AND status = 'active'
		ORDER BY joined_at ASC
		LIMIT $2 OFFSET $3
	`, groupID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []GroupMember
	for rows.Next() {
		var m GroupMember
		if err := rows.Scan(&m.GroupID, &m.UserID, &m.Role, &m.JoinedAt,
			&m.ID, &m.InvitedByUserID, &m.Status, &m.RemovedAt, &m.RemovedByUserID, &m.RemovalReason); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// --- GDPR ---

// RemoveUserFromAllGroups soft-removes a user from all groups they belong to.
func (s *Store) RemoveUserFromAllGroups(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE group_members SET status = 'removed', removed_at = NOW()
		WHERE user_id = $1 AND status = 'active'
	`, userID)
	return err
}

// CancelUserInvites cancels all pending invites for a user.
func (s *Store) CancelUserInvites(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE group_invites SET status = 'rejected', updated_at = NOW()
		WHERE invitee_id = $1 AND status = 'pending'
	`, userID)
	return err
}

// CancelUserJoinRequests cancels all pending join requests by a user.
func (s *Store) CancelUserJoinRequests(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE group_join_requests SET status = 'rejected', reviewed_at = NOW()
		WHERE user_id = $1 AND status = 'pending'
	`, userID)
	return err
}

// ListGroupsWhereUserIsOnlyAdmin returns group IDs where the user is the sole admin.
func (s *Store) ListGroupsWhereUserIsOnlyAdmin(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT gm.group_id FROM group_members gm
		WHERE gm.user_id = $1 AND gm.role = 'admin' AND gm.status = 'active'
		AND NOT EXISTS (
			SELECT 1 FROM group_members gm2
			WHERE gm2.group_id = gm.group_id AND gm2.user_id != $1
			AND gm2.role = 'admin' AND gm2.status = 'active'
		)
	`, userID)
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

// UnbanMember resets a banned member back to removed status (they can rejoin).
func (s *Store) UnbanMember(ctx context.Context, groupID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE group_members SET status = 'removed', removal_reason = NULL
		WHERE group_id = $1 AND user_id = $2 AND status = 'banned'
	`, groupID, userID)
	return err
}

// BanMemberWithReason sets a member's status to 'banned' with a reason.
func (s *Store) BanMemberWithReason(ctx context.Context, groupID, userID, removedBy uuid.UUID, reason string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	cmdTag, err := tx.Exec(ctx, `
		UPDATE group_members SET status = 'banned', removed_at = NOW(), removed_by_user_id = $3, removal_reason = $4
		WHERE group_id = $1 AND user_id = $2 AND status = 'active'
	`, groupID, userID, removedBy, reason)
	if err != nil {
		return err
	}

	if cmdTag.RowsAffected() > 0 {
		_, err = tx.Exec(ctx, `
			UPDATE groups SET member_count = GREATEST(member_count - 1, 0), updated_at = NOW() WHERE id = $1
		`, groupID)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// ═══════════════════════════════════════════════════════════
// Group Member Stats
// ═══════════════════════════════════════════════════════════

// MemberStats represents a member's activity stats within a group.
type MemberStats struct {
	GroupID        uuid.UUID `json:"group_id"`
	UserID         uuid.UUID `json:"user_id"`
	PostCount      int       `json:"post_count"`
	SparksReceived int       `json:"sparks_received"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// GetMemberStats returns stats for a specific member in a group.
func (s *Store) GetMemberStats(ctx context.Context, groupID, userID uuid.UUID) (*MemberStats, error) {
	var ms MemberStats
	err := s.db.QueryRow(ctx, `
		SELECT group_id, user_id, post_count, sparks_received, updated_at
		FROM group_member_stats
		WHERE group_id = $1 AND user_id = $2
	`, groupID, userID).Scan(&ms.GroupID, &ms.UserID, &ms.PostCount, &ms.SparksReceived, &ms.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &ms, nil
}

// IncrementMemberPostCount increments the post count for a member in a group.
func (s *Store) IncrementMemberPostCount(ctx context.Context, groupID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO group_member_stats (group_id, user_id, post_count, updated_at)
		VALUES ($1, $2, 1, NOW())
		ON CONFLICT (group_id, user_id) DO UPDATE
		SET post_count = group_member_stats.post_count + 1, updated_at = NOW()
	`, groupID, userID)
	return err
}

// IncrementMemberSparks increments the sparks received for a member in a group.
func (s *Store) IncrementMemberSparks(ctx context.Context, groupID, userID uuid.UUID, delta int) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO group_member_stats (group_id, user_id, sparks_received, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (group_id, user_id) DO UPDATE
		SET sparks_received = group_member_stats.sparks_received + $3, updated_at = NOW()
	`, groupID, userID, delta)
	return err
}

// GetTopContributors returns the top contributors for a group ordered by post count.
func (s *Store) GetTopContributors(ctx context.Context, groupID uuid.UUID, limit int) ([]MemberStats, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows, err := s.db.Query(ctx, `
		SELECT group_id, user_id, post_count, sparks_received, updated_at
		FROM group_member_stats
		WHERE group_id = $1
		ORDER BY post_count DESC
		LIMIT $2
	`, groupID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []MemberStats
	for rows.Next() {
		var ms MemberStats
		if err := rows.Scan(&ms.GroupID, &ms.UserID, &ms.PostCount, &ms.SparksReceived, &ms.UpdatedAt); err != nil {
			return nil, err
		}
		stats = append(stats, ms)
	}
	return stats, rows.Err()
}

// ListBannedMembers returns banned members for a group.
func (s *Store) ListBannedMembers(ctx context.Context, groupID uuid.UUID, limit, offset int) ([]GroupMember, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT group_id, user_id, role, joined_at, id, invited_by_user_id, status, removed_at, removed_by_user_id, removal_reason
		FROM group_members
		WHERE group_id = $1 AND status = 'banned'
		ORDER BY removed_at DESC
		LIMIT $2 OFFSET $3
	`, groupID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []GroupMember
	for rows.Next() {
		var m GroupMember
		if err := rows.Scan(&m.GroupID, &m.UserID, &m.Role, &m.JoinedAt,
			&m.ID, &m.InvitedByUserID, &m.Status, &m.RemovedAt, &m.RemovedByUserID, &m.RemovalReason); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// CheckMembershipCached checks membership using Redis cache first, falling back to DB.
// Cache TTL of 5 minutes avoids repeated DB hits for active commenters.
func (s *Store) CheckMembershipCached(ctx context.Context, rdb *redis.Client, groupID, userID uuid.UUID) (bool, error) {
	// No cache configured (tests, a degraded boot): the database is the truth.
	if rdb == nil {
		return s.CheckMembership(ctx, groupID, userID)
	}
	cacheKey := fmt.Sprintf("gm:%s:%s", groupID, userID)
	val, err := rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		return val == "1", nil
	}
	// Cache miss → query DB
	isMember, err := s.CheckMembership(ctx, groupID, userID)
	if err != nil {
		return false, err
	}
	// Cache result for 5 minutes
	if isMember {
		rdb.Set(ctx, cacheKey, "1", 5*time.Minute)
	} else {
		rdb.Set(ctx, cacheKey, "0", 1*time.Minute)
	}
	return isMember, nil
}

// HasOtherActiveAdmin reports whether the group has an active admin other
// than userID. One EXISTS over every member: LeaveGroup used to look in the
// first 100 members only, and refused a departing admin whose co-admin
// joined later (G-L2).
func (s *Store) HasOtherActiveAdmin(ctx context.Context, groupID, userID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM group_members
			WHERE group_id = $1 AND user_id <> $2 AND role = 'admin' AND status = 'active'
		)
	`, groupID, userID).Scan(&exists)
	return exists, err
}
