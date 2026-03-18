package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Group struct {
	ID                 uuid.UUID  `json:"id"`
	Name               string     `json:"name"`
	Description        string     `json:"description"`
	AvatarMediaID      *uuid.UUID `json:"avatar_media_id,omitempty"`
	CoverMediaID       *uuid.UUID `json:"cover_media_id,omitempty"`
	CreatorID          uuid.UUID  `json:"creator_id"`
	Visibility         string     `json:"visibility"`
	IsArchived         bool       `json:"is_archived"`
	ChatConversationID *uuid.UUID `json:"chat_conversation_id,omitempty"`
	MemberCount        int64      `json:"member_count"`
	PostCount          int64      `json:"post_count"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	// V2 fields
	Handle       string     `json:"handle,omitempty"`
	Category     string     `json:"category,omitempty"`
	PrivacyLevel string     `json:"privacy_level"`
	JoinMode     string     `json:"join_mode"`
	WhoCanPost   string     `json:"who_can_post"`
	WhoCanInvite string     `json:"who_can_invite"`
	Location     string     `json:"location,omitempty"`
	Language     string     `json:"language,omitempty"`
	Status       string     `json:"status"`
	DeletedAt           *time.Time `json:"deleted_at,omitempty"`
	PendingRequestCount int        `json:"pending_request_count"`
	// GCC Phase 1 fields
	GroupType         string          `json:"group_type"`
	MaxMembers        int             `json:"max_members"`
	JoinQuestions     json.RawMessage `json:"join_questions,omitempty"`
	TopicTags         []string        `json:"topic_tags"`
	CommentPermission string          `json:"comment_permission"`
	MemberListVisible bool            `json:"member_list_visible"`
	LinkSharing       bool            `json:"link_sharing"`
}

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

type GroupInvite struct {
	ID        uuid.UUID  `json:"id"`
	GroupID   uuid.UUID  `json:"group_id"`
	InviterID uuid.UUID  `json:"inviter_id"`
	InviteeID uuid.UUID  `json:"invitee_id"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type GroupPost struct {
	GroupID   uuid.UUID `json:"group_id"`
	PostID    uuid.UUID `json:"post_id"`
	AuthorID  uuid.UUID `json:"author_id"`
	CreatedAt time.Time `json:"created_at"`
}

type GroupJoinRequest struct {
	ID               uuid.UUID  `json:"id"`
	GroupID          uuid.UUID  `json:"group_id"`
	UserID           uuid.UUID  `json:"user_id"`
	Status           string     `json:"status"`
	ReviewedByUserID *uuid.UUID `json:"reviewed_by_user_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	ReviewedAt       *time.Time `json:"reviewed_at,omitempty"`
}

type GroupRule struct {
	ID          uuid.UUID `json:"id"`
	GroupID     uuid.UUID `json:"group_id"`
	RuleOrder   int       `json:"rule_order"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// v2 group columns for SELECT queries — use g. prefix for JOIN safety
const groupColumns = `g.id, g.name, g.description, g.avatar_media_id, g.cover_media_id, g.creator_id,
       g.visibility, g.is_archived, g.chat_conversation_id, g.member_count, g.post_count,
       g.created_at, g.updated_at, g.handle, g.category, g.privacy_level, g.join_mode,
       g.who_can_post, g.who_can_invite, g.location, g.language, g.status, g.deleted_at, g.pending_request_count,
       g.group_type, g.max_members, g.join_questions, g.topic_tags, g.comment_permission, g.member_list_visible, g.link_sharing`

func scanGroup(row pgx.Row) (*Group, error) {
	var g Group
	err := row.Scan(
		&g.ID, &g.Name, &g.Description, &g.AvatarMediaID, &g.CoverMediaID, &g.CreatorID,
		&g.Visibility, &g.IsArchived, &g.ChatConversationID, &g.MemberCount, &g.PostCount,
		&g.CreatedAt, &g.UpdatedAt, &g.Handle, &g.Category, &g.PrivacyLevel, &g.JoinMode,
		&g.WhoCanPost, &g.WhoCanInvite, &g.Location, &g.Language, &g.Status, &g.DeletedAt, &g.PendingRequestCount,
		&g.GroupType, &g.MaxMembers, &g.JoinQuestions, &g.TopicTags, &g.CommentPermission, &g.MemberListVisible, &g.LinkSharing,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &g, nil
}

func scanGroups(rows pgx.Rows) ([]Group, error) {
	var groups []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(
			&g.ID, &g.Name, &g.Description, &g.AvatarMediaID, &g.CoverMediaID, &g.CreatorID,
			&g.Visibility, &g.IsArchived, &g.ChatConversationID, &g.MemberCount, &g.PostCount,
			&g.CreatedAt, &g.UpdatedAt, &g.Handle, &g.Category, &g.PrivacyLevel, &g.JoinMode,
			&g.WhoCanPost, &g.WhoCanInvite, &g.Location, &g.Language, &g.Status, &g.DeletedAt, &g.PendingRequestCount,
			&g.GroupType, &g.MaxMembers, &g.JoinQuestions, &g.TopicTags, &g.CommentPermission, &g.MemberListVisible, &g.LinkSharing,
		); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// --- Groups ---

// CreateGroup inserts a new group and adds the creator as an admin member.
func (s *Store) CreateGroup(ctx context.Context, g *Group) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx, `
		INSERT INTO groups (name, description, creator_id, visibility, handle, category,
		                    privacy_level, join_mode, who_can_post, who_can_invite,
		                    location, language, status,
		                    group_type, max_members, join_questions, topic_tags,
		                    comment_permission, member_list_visible, link_sharing)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
		        $14, $15, $16, $17, $18, $19, $20)
		RETURNING id, member_count, post_count, is_archived, created_at, updated_at
	`, g.Name, g.Description, g.CreatorID, g.Visibility, g.Handle, g.Category,
		g.PrivacyLevel, g.JoinMode, g.WhoCanPost, g.WhoCanInvite,
		g.Location, g.Language, g.Status,
		g.GroupType, g.MaxMembers, g.JoinQuestions, g.TopicTags,
		g.CommentPermission, g.MemberListVisible, g.LinkSharing).Scan(
		&g.ID, &g.MemberCount, &g.PostCount, &g.IsArchived, &g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO group_members (group_id, user_id, role, status)
		VALUES ($1, $2, 'admin', 'active')
	`, g.ID, g.CreatorID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE groups SET member_count = 1 WHERE id = $1
	`, g.ID)
	if err != nil {
		return err
	}

	g.MemberCount = 1
	return tx.Commit(ctx)
}

// GetGroupByID returns a group by its primary key, or nil if not found.
func (s *Store) GetGroupByID(ctx context.Context, id uuid.UUID) (*Group, error) {
	row := s.db.QueryRow(ctx, `SELECT `+groupColumns+` FROM groups g WHERE g.id = $1 AND g.status != 'deleted'`, id)
	return scanGroup(row)
}

// GetGroupByHandle returns a group by its handle, or nil if not found.
func (s *Store) GetGroupByHandle(ctx context.Context, handle string) (*Group, error) {
	row := s.db.QueryRow(ctx, `SELECT `+groupColumns+` FROM groups g WHERE g.handle = $1 AND g.status != 'deleted'`, handle)
	return scanGroup(row)
}

// CheckHandleAvailability checks if a handle is available for use.
func (s *Store) CheckHandleAvailability(ctx context.Context, handle string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM groups WHERE handle = $1 AND status != 'deleted')
	`, handle).Scan(&exists)
	return !exists, err
}

// UpdateGroup updates mutable fields of a group.
func (s *Store) UpdateGroup(ctx context.Context, id uuid.UUID, name, desc string, avatar, cover *uuid.UUID, visibility string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE groups
		SET name = $2, description = $3, avatar_media_id = $4, cover_media_id = $5,
		    visibility = $6, updated_at = NOW()
		WHERE id = $1
	`, id, name, desc, avatar, cover, visibility)
	return err
}

// UpdateGroupV2 updates v2 fields of a group.
func (s *Store) UpdateGroupV2(ctx context.Context, id uuid.UUID, name, desc string, avatar, cover *uuid.UUID,
	visibility, category, privacyLevel, joinMode, whoCanPost, whoCanInvite, location, language string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE groups
		SET name = $2, description = $3, avatar_media_id = $4, cover_media_id = $5,
		    visibility = $6, category = $7, privacy_level = $8, join_mode = $9,
		    who_can_post = $10, who_can_invite = $11, location = $12, language = $13,
		    updated_at = NOW()
		WHERE id = $1 AND status != 'deleted'
	`, id, name, desc, avatar, cover, visibility, category, privacyLevel, joinMode,
		whoCanPost, whoCanInvite, location, language)
	return err
}

// UpdateGroupSettings updates GCC Phase 1 settings fields.
func (s *Store) UpdateGroupSettings(ctx context.Context, id uuid.UUID, groupType string, maxMembers int,
	joinQuestions json.RawMessage, topicTags []string, commentPermission string, memberListVisible, linkSharing bool) error {
	_, err := s.db.Exec(ctx, `
		UPDATE groups
		SET group_type = $2, max_members = $3, join_questions = $4, topic_tags = $5,
		    comment_permission = $6, member_list_visible = $7, link_sharing = $8,
		    updated_at = NOW()
		WHERE id = $1 AND status != 'deleted'
	`, id, groupType, maxMembers, joinQuestions, topicTags, commentPermission, memberListVisible, linkSharing)
	return err
}

// DiscoverPublicGroupsByType returns discoverable groups filtered by group_type.
func (s *Store) DiscoverPublicGroupsByType(ctx context.Context, groupType string, limit, offset int) ([]Group, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+groupColumns+`
		FROM groups g
		WHERE g.privacy_level = 'public' AND g.status = 'active' AND g.group_type = $3
		ORDER BY g.member_count DESC
		LIMIT $1 OFFSET $2
	`, limit, offset, groupType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanGroups(rows)
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

// DeleteGroup soft-deletes a group.
func (s *Store) DeleteGroup(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE groups SET status = 'deleted', deleted_at = NOW(), updated_at = NOW() WHERE id = $1
	`, id)
	return err
}

// ArchiveGroup sets a group status to archived.
func (s *Store) ArchiveGroup(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE groups SET status = 'archived', is_archived = TRUE, updated_at = NOW() WHERE id = $1
	`, id)
	return err
}

// SetChatConversationID links a chat conversation to the group.
func (s *Store) SetChatConversationID(ctx context.Context, groupID, convID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE groups SET chat_conversation_id = $2, updated_at = NOW() WHERE id = $1
	`, groupID, convID)
	return err
}

// --- Members ---

// AddMember adds a user to a group with the given role.
func (s *Store) AddMember(ctx context.Context, groupID, userID uuid.UUID, role string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

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

// --- Invites ---

// CreateInvite creates or re-opens an invite for a user to a group.
func (s *Store) CreateInvite(ctx context.Context, inv *GroupInvite) error {
	err := s.db.QueryRow(ctx, `
		INSERT INTO group_invites (group_id, inviter_id, invitee_id, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (group_id, invitee_id) DO UPDATE
		SET status = 'pending', inviter_id = $2, updated_at = NOW(), expires_at = $4
		RETURNING id, status, created_at, updated_at
	`, inv.GroupID, inv.InviterID, inv.InviteeID, inv.ExpiresAt).Scan(
		&inv.ID, &inv.Status, &inv.CreatedAt, &inv.UpdatedAt,
	)
	return err
}

// CreateInviteBatch creates invites for multiple users at once.
func (s *Store) CreateInviteBatch(ctx context.Context, groupID, inviterID uuid.UUID, inviteeIDs []uuid.UUID, expiresAt *time.Time) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, inviteeID := range inviteeIDs {
		_, err := tx.Exec(ctx, `
			INSERT INTO group_invites (group_id, inviter_id, invitee_id, expires_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (group_id, invitee_id) DO UPDATE
			SET status = 'pending', inviter_id = $2, updated_at = NOW(), expires_at = $4
		`, groupID, inviterID, inviteeID, expiresAt)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// GetInviteByID returns an invite by its primary key.
func (s *Store) GetInviteByID(ctx context.Context, id uuid.UUID) (*GroupInvite, error) {
	var inv GroupInvite
	err := s.db.QueryRow(ctx, `
		SELECT id, group_id, inviter_id, invitee_id, status, created_at, updated_at, expires_at
		FROM group_invites WHERE id = $1
	`, id).Scan(
		&inv.ID, &inv.GroupID, &inv.InviterID, &inv.InviteeID, &inv.Status,
		&inv.CreatedAt, &inv.UpdatedAt, &inv.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &inv, nil
}

// UpdateInviteStatus changes the status of an invite.
func (s *Store) UpdateInviteStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE group_invites SET status = $2, updated_at = NOW() WHERE id = $1
	`, id, status)
	return err
}

// ListInvitesByUser returns pending invites for a given user.
func (s *Store) ListInvitesByUser(ctx context.Context, userID uuid.UUID) ([]GroupInvite, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, group_id, inviter_id, invitee_id, status, created_at, updated_at, expires_at
		FROM group_invites
		WHERE invitee_id = $1 AND status = 'pending'
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invites []GroupInvite
	for rows.Next() {
		var inv GroupInvite
		if err := rows.Scan(&inv.ID, &inv.GroupID, &inv.InviterID, &inv.InviteeID, &inv.Status,
			&inv.CreatedAt, &inv.UpdatedAt, &inv.ExpiresAt); err != nil {
			return nil, err
		}
		invites = append(invites, inv)
	}
	return invites, rows.Err()
}

// ListGroupInvites returns pending invites for a given group.
func (s *Store) ListGroupInvites(ctx context.Context, groupID uuid.UUID) ([]GroupInvite, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, group_id, inviter_id, invitee_id, status, created_at, updated_at, expires_at
		FROM group_invites
		WHERE group_id = $1 AND status = 'pending'
		ORDER BY created_at DESC
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invites []GroupInvite
	for rows.Next() {
		var inv GroupInvite
		if err := rows.Scan(&inv.ID, &inv.GroupID, &inv.InviterID, &inv.InviteeID, &inv.Status,
			&inv.CreatedAt, &inv.UpdatedAt, &inv.ExpiresAt); err != nil {
			return nil, err
		}
		invites = append(invites, inv)
	}
	return invites, rows.Err()
}

// --- Join Requests ---

// CreateJoinRequest creates a new join request.
func (s *Store) CreateJoinRequest(ctx context.Context, jr *GroupJoinRequest) error {
	return s.db.QueryRow(ctx, `
		INSERT INTO group_join_requests (group_id, user_id)
		VALUES ($1, $2)
		RETURNING id, status, created_at
	`, jr.GroupID, jr.UserID).Scan(&jr.ID, &jr.Status, &jr.CreatedAt)
}

// GetJoinRequestByID returns a join request by its primary key.
func (s *Store) GetJoinRequestByID(ctx context.Context, id uuid.UUID) (*GroupJoinRequest, error) {
	var jr GroupJoinRequest
	err := s.db.QueryRow(ctx, `
		SELECT id, group_id, user_id, status, reviewed_by_user_id, created_at, reviewed_at
		FROM group_join_requests WHERE id = $1
	`, id).Scan(&jr.ID, &jr.GroupID, &jr.UserID, &jr.Status,
		&jr.ReviewedByUserID, &jr.CreatedAt, &jr.ReviewedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &jr, nil
}

// ListJoinRequests returns pending join requests for a group.
func (s *Store) ListJoinRequests(ctx context.Context, groupID uuid.UUID, limit, offset int) ([]GroupJoinRequest, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, group_id, user_id, status, reviewed_by_user_id, created_at, reviewed_at
		FROM group_join_requests
		WHERE group_id = $1 AND status = 'pending'
		ORDER BY created_at ASC
		LIMIT $2 OFFSET $3
	`, groupID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []GroupJoinRequest
	for rows.Next() {
		var jr GroupJoinRequest
		if err := rows.Scan(&jr.ID, &jr.GroupID, &jr.UserID, &jr.Status,
			&jr.ReviewedByUserID, &jr.CreatedAt, &jr.ReviewedAt); err != nil {
			return nil, err
		}
		requests = append(requests, jr)
	}
	return requests, rows.Err()
}

// UpdateJoinRequestStatus updates the status of a join request.
func (s *Store) UpdateJoinRequestStatus(ctx context.Context, id uuid.UUID, status string, reviewedBy uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE group_join_requests
		SET status = $2, reviewed_by_user_id = $3, reviewed_at = NOW()
		WHERE id = $1
	`, id, status, reviewedBy)
	return err
}

// --- Group Rules ---

// ListGroupRules returns rules for a group ordered by rule_order.
func (s *Store) ListGroupRules(ctx context.Context, groupID uuid.UUID) ([]GroupRule, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, group_id, rule_order, title, description, created_at
		FROM group_rules
		WHERE group_id = $1
		ORDER BY rule_order ASC
	`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []GroupRule
	for rows.Next() {
		var r GroupRule
		if err := rows.Scan(&r.ID, &r.GroupID, &r.RuleOrder, &r.Title, &r.Description, &r.CreatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// ReplaceGroupRules deletes existing rules and inserts new ones atomically.
func (s *Store) ReplaceGroupRules(ctx context.Context, groupID uuid.UUID, rules []GroupRule) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `DELETE FROM group_rules WHERE group_id = $1`, groupID)
	if err != nil {
		return err
	}

	for i, r := range rules {
		_, err := tx.Exec(ctx, `
			INSERT INTO group_rules (group_id, rule_order, title, description)
			VALUES ($1, $2, $3, $4)
		`, groupID, i+1, r.Title, r.Description)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// --- Group Posts ---

// AddGroupPost records a post in a group and increments the post count.
func (s *Store) AddGroupPost(ctx context.Context, groupID, postID, authorID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO group_posts (group_id, post_id, author_id)
		VALUES ($1, $2, $3)
	`, groupID, postID, authorID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		UPDATE groups SET post_count = post_count + 1, updated_at = NOW() WHERE id = $1
	`, groupID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ListGroupPosts returns paginated posts for a group, newest first.
func (s *Store) ListGroupPosts(ctx context.Context, groupID uuid.UUID, limit, offset int) ([]GroupPost, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT group_id, post_id, author_id, created_at
		FROM group_posts
		WHERE group_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, groupID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []GroupPost
	for rows.Next() {
		var p GroupPost
		if err := rows.Scan(&p.GroupID, &p.PostID, &p.AuthorID, &p.CreatedAt); err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// --- Group Queries ---

// ListGroupsByUser returns groups that a user is a member of.
func (s *Store) ListGroupsByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Group, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+groupColumns+`
		FROM groups g
		JOIN group_members gm ON g.id = gm.group_id
		WHERE gm.user_id = $1 AND gm.status = 'active' AND g.status != 'deleted'
		ORDER BY gm.joined_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanGroups(rows)
}

// DiscoverPublicGroups returns public, non-archived groups sorted by member count.
func (s *Store) DiscoverPublicGroups(ctx context.Context, limit, offset int) ([]Group, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+groupColumns+`
		FROM groups g
		WHERE g.privacy_level = 'public' AND g.status = 'active'
		ORDER BY g.member_count DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanGroups(rows)
}

// SearchGroups performs full-text search on group names.
func (s *Store) SearchGroups(ctx context.Context, query string, limit, offset int) ([]Group, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+groupColumns+`
		FROM groups g
		WHERE to_tsvector('english', g.name) @@ to_tsquery('english', $1)
		  AND g.status = 'active'
		ORDER BY g.member_count DESC
		LIMIT $2 OFFSET $3
	`, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanGroups(rows)
}

// PinPost sets pinned_at on a post.
func (s *Store) PinPost(ctx context.Context, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE posts SET pinned_at = NOW() WHERE id = $1`, postID)
	return err
}

// UnpinPost clears pinned_at on a post.
func (s *Store) UnpinPost(ctx context.Context, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE posts SET pinned_at = NULL WHERE id = $1`, postID)
	return err
}

// CountPinnedPosts counts currently pinned posts in a group.
func (s *Store) CountPinnedPosts(ctx context.Context, groupID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM group_posts gp
		JOIN posts p ON p.id = gp.post_id
		WHERE gp.group_id = $1 AND p.pinned_at IS NOT NULL
	`, groupID).Scan(&count)
	return count, err
}

// DeleteGroupPost removes a post from the group and decrements the post count.
func (s *Store) DeleteGroupPost(ctx context.Context, groupID, postID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	cmdTag, err := tx.Exec(ctx, `DELETE FROM group_posts WHERE group_id = $1 AND post_id = $2`, groupID, postID)
	if err != nil {
		return err
	}

	if cmdTag.RowsAffected() > 0 {
		_, err = tx.Exec(ctx, `UPDATE groups SET post_count = GREATEST(post_count - 1, 0), updated_at = NOW() WHERE id = $1`, groupID)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// GetGroupPost returns a group post by group ID and post ID.
func (s *Store) GetGroupPost(ctx context.Context, groupID, postID uuid.UUID) (*GroupPost, error) {
	var p GroupPost
	err := s.db.QueryRow(ctx, `
		SELECT group_id, post_id, author_id, created_at
		FROM group_posts WHERE group_id = $1 AND post_id = $2
	`, groupID, postID).Scan(&p.GroupID, &p.PostID, &p.AuthorID, &p.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
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

// IncrementPendingRequestCount increments the pending request count on a group.
func (s *Store) IncrementPendingRequestCount(ctx context.Context, groupID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE groups SET pending_request_count = pending_request_count + 1 WHERE id = $1`, groupID)
	return err
}

// DecrementPendingRequestCount decrements the pending request count on a group.
func (s *Store) DecrementPendingRequestCount(ctx context.Context, groupID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE groups SET pending_request_count = GREATEST(pending_request_count - 1, 0) WHERE id = $1`, groupID)
	return err
}

// ListGroupMedia returns media posts (image, video, flick) for a group.
func (s *Store) ListGroupMedia(ctx context.Context, groupID uuid.UUID, limit, offset int) ([]GroupPost, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := s.db.Query(ctx, `
		SELECT gp.group_id, gp.post_id, gp.author_id, gp.created_at
		FROM group_posts gp
		JOIN posts p ON p.id = gp.post_id
		WHERE gp.group_id = $1 AND p.content_type IN ('image', 'video', 'flick')
		  AND p.status = 'published'
		ORDER BY gp.created_at DESC
		LIMIT $2 OFFSET $3
	`, groupID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []GroupPost
	for rows.Next() {
		var p GroupPost
		if err := rows.Scan(&p.GroupID, &p.PostID, &p.AuthorID, &p.CreatedAt); err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	return posts, rows.Err()
}

// ═══════════════════════════════════════════════════════════
// Word Blocklist
// ═══════════════════════════════════════════════════════════

func (s *Store) AddWordToBlocklist(ctx context.Context, groupID uuid.UUID, word string, addedBy uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO group_word_blocklist (group_id, word, added_by) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		groupID, word, addedBy)
	return err
}

func (s *Store) RemoveWordFromBlocklist(ctx context.Context, groupID uuid.UUID, word string) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM group_word_blocklist WHERE group_id = $1 AND word = $2`,
		groupID, word)
	return err
}

func (s *Store) GetWordBlocklist(ctx context.Context, groupID uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx,
		`SELECT word FROM group_word_blocklist WHERE group_id = $1 ORDER BY added_at DESC`,
		groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var words []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return nil, err
		}
		words = append(words, w)
	}
	return words, rows.Err()
}

// ═══════════════════════════════════════════════════════════
// Post Approval Queue
// ═══════════════════════════════════════════════════════════

type ApprovalQueueItem struct {
	ID         uuid.UUID  `json:"id"`
	GroupID    uuid.UUID  `json:"group_id"`
	PostID     uuid.UUID  `json:"post_id"`
	AuthorID   uuid.UUID  `json:"author_id"`
	Status     string     `json:"status"`
	ReviewedBy *uuid.UUID `json:"reviewed_by,omitempty"`
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (s *Store) AddToApprovalQueue(ctx context.Context, groupID, postID, authorID uuid.UUID) (*ApprovalQueueItem, error) {
	item := &ApprovalQueueItem{}
	err := s.db.QueryRow(ctx,
		`INSERT INTO post_approval_queue (group_id, post_id, author_id)
		 VALUES ($1, $2, $3)
		 RETURNING id, group_id, post_id, author_id, status, reviewed_by, reviewed_at, created_at`,
		groupID, postID, authorID,
	).Scan(&item.ID, &item.GroupID, &item.PostID, &item.AuthorID, &item.Status, &item.ReviewedBy, &item.ReviewedAt, &item.CreatedAt)
	return item, err
}

func (s *Store) ReviewApprovalItem(ctx context.Context, itemID, reviewerID uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE post_approval_queue SET status=$1, reviewed_by=$2, reviewed_at=NOW() WHERE id=$3 AND status='pending'`,
		status, reviewerID, itemID)
	return err
}

func (s *Store) GetApprovalQueue(ctx context.Context, groupID uuid.UUID, status string, limit, offset int) ([]ApprovalQueueItem, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, group_id, post_id, author_id, status, reviewed_by, reviewed_at, created_at
		 FROM post_approval_queue WHERE group_id = $1 AND status = $2
		 ORDER BY created_at DESC LIMIT $3 OFFSET $4`,
		groupID, status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []ApprovalQueueItem
	for rows.Next() {
		var item ApprovalQueueItem
		if err := rows.Scan(&item.ID, &item.GroupID, &item.PostID, &item.AuthorID, &item.Status, &item.ReviewedBy, &item.ReviewedAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ═══════════════════════════════════════════════════════════
// Group Channels
// ═══════════════════════════════════════════════════════════

type GroupChannel struct {
	ID          uuid.UUID `json:"id"`
	GroupID     uuid.UUID `json:"group_id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Description string    `json:"description"`
	IsArchived  bool      `json:"is_archived"`
	SortOrder   int       `json:"sort_order"`
	PostCount   int64     `json:"post_count"`
	CreatedBy   uuid.UUID `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Store) CreateGroupChannel(ctx context.Context, ch *GroupChannel) (*GroupChannel, error) {
	err := s.db.QueryRow(ctx,
		`INSERT INTO group_channels (group_id, name, type, description, sort_order, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 RETURNING id, group_id, name, type, description, is_archived, sort_order, post_count, created_by, created_at`,
		ch.GroupID, ch.Name, ch.Type, ch.Description, ch.SortOrder, ch.CreatedBy,
	).Scan(&ch.ID, &ch.GroupID, &ch.Name, &ch.Type, &ch.Description, &ch.IsArchived, &ch.SortOrder, &ch.PostCount, &ch.CreatedBy, &ch.CreatedAt)
	return ch, err
}

func (s *Store) ListGroupChannels(ctx context.Context, groupID uuid.UUID) ([]GroupChannel, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, group_id, name, type, description, is_archived, sort_order, post_count, created_by, created_at
		 FROM group_channels WHERE group_id = $1 AND is_archived = FALSE ORDER BY sort_order ASC, created_at ASC`,
		groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var channels []GroupChannel
	for rows.Next() {
		var ch GroupChannel
		if err := rows.Scan(&ch.ID, &ch.GroupID, &ch.Name, &ch.Type, &ch.Description, &ch.IsArchived, &ch.SortOrder, &ch.PostCount, &ch.CreatedBy, &ch.CreatedAt); err != nil {
			return nil, err
		}
		channels = append(channels, ch)
	}
	return channels, rows.Err()
}

func (s *Store) DeleteGroupChannel(ctx context.Context, channelID, groupID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE group_channels SET is_archived = TRUE WHERE id = $1 AND group_id = $2`,
		channelID, groupID)
	return err
}

// ═══════════════════════════════════════════════════════════
// Group Wiki
// ═══════════════════════════════════════════════════════════

type WikiPage struct {
	ID        uuid.UUID  `json:"id"`
	GroupID   uuid.UUID  `json:"group_id"`
	Title     string     `json:"title"`
	Content   string     `json:"content"`
	CreatedBy uuid.UUID  `json:"created_by"`
	UpdatedBy *uuid.UUID `json:"updated_by,omitempty"`
	Version   int        `json:"version"`
	IsPinned  bool       `json:"is_pinned"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func (s *Store) CreateWikiPage(ctx context.Context, groupID, createdBy uuid.UUID, title, content string) (*WikiPage, error) {
	p := &WikiPage{}
	err := s.db.QueryRow(ctx,
		`INSERT INTO group_wiki_pages (group_id, title, content, created_by)
		 VALUES ($1,$2,$3,$4)
		 RETURNING id, group_id, title, content, created_by, updated_by, version, is_pinned, created_at, updated_at`,
		groupID, title, content, createdBy,
	).Scan(&p.ID, &p.GroupID, &p.Title, &p.Content, &p.CreatedBy, &p.UpdatedBy, &p.Version, &p.IsPinned, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

func (s *Store) UpdateWikiPage(ctx context.Context, pageID, updatedBy uuid.UUID, title, content string) (*WikiPage, error) {
	p := &WikiPage{}
	err := s.db.QueryRow(ctx,
		`UPDATE group_wiki_pages SET title=$3, content=$4, updated_by=$2, version=version+1, updated_at=NOW()
		 WHERE id=$1
		 RETURNING id, group_id, title, content, created_by, updated_by, version, is_pinned, created_at, updated_at`,
		pageID, updatedBy, title, content,
	).Scan(&p.ID, &p.GroupID, &p.Title, &p.Content, &p.CreatedBy, &p.UpdatedBy, &p.Version, &p.IsPinned, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

func (s *Store) ListWikiPages(ctx context.Context, groupID uuid.UUID) ([]WikiPage, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, group_id, title, content, created_by, updated_by, version, is_pinned, created_at, updated_at
		 FROM group_wiki_pages WHERE group_id = $1 ORDER BY is_pinned DESC, updated_at DESC`,
		groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pages []WikiPage
	for rows.Next() {
		var p WikiPage
		if err := rows.Scan(&p.ID, &p.GroupID, &p.Title, &p.Content, &p.CreatedBy, &p.UpdatedBy, &p.Version, &p.IsPinned, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	return pages, rows.Err()
}

func (s *Store) DeleteWikiPage(ctx context.Context, pageID, groupID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM group_wiki_pages WHERE id = $1 AND group_id = $2`,
		pageID, groupID)
	return err
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
