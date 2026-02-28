package store

import (
	"context"
	"errors"
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
}

type GroupMember struct {
	GroupID  uuid.UUID `json:"group_id"`
	UserID   uuid.UUID `json:"user_id"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

type GroupInvite struct {
	ID        uuid.UUID `json:"id"`
	GroupID   uuid.UUID `json:"group_id"`
	InviterID uuid.UUID `json:"inviter_id"`
	InviteeID uuid.UUID `json:"invitee_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type GroupPost struct {
	GroupID   uuid.UUID `json:"group_id"`
	PostID    uuid.UUID `json:"post_id"`
	AuthorID  uuid.UUID `json:"author_id"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
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
		INSERT INTO groups (name, description, creator_id, visibility)
		VALUES ($1, $2, $3, $4)
		RETURNING id, member_count, post_count, is_archived, created_at, updated_at
	`, g.Name, g.Description, g.CreatorID, g.Visibility).Scan(
		&g.ID, &g.MemberCount, &g.PostCount, &g.IsArchived, &g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO group_members (group_id, user_id, role)
		VALUES ($1, $2, 'admin')
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
	var g Group
	err := s.db.QueryRow(ctx, `
		SELECT id, name, description, avatar_media_id, cover_media_id, creator_id,
		       visibility, is_archived, chat_conversation_id, member_count, post_count,
		       created_at, updated_at
		FROM groups WHERE id = $1
	`, id).Scan(
		&g.ID, &g.Name, &g.Description, &g.AvatarMediaID, &g.CoverMediaID, &g.CreatorID,
		&g.Visibility, &g.IsArchived, &g.ChatConversationID, &g.MemberCount, &g.PostCount,
		&g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &g, nil
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

// DeleteGroup removes a group (cascades to members, invites, posts).
func (s *Store) DeleteGroup(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM groups WHERE id = $1`, id)
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
		INSERT INTO group_members (group_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (group_id, user_id) DO NOTHING
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

// RemoveMember removes a user from a group.
func (s *Store) RemoveMember(ctx context.Context, groupID, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	cmdTag, err := tx.Exec(ctx, `
		DELETE FROM group_members WHERE group_id = $1 AND user_id = $2
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

// UpdateMemberRole changes a member's role within a group.
func (s *Store) UpdateMemberRole(ctx context.Context, groupID, userID uuid.UUID, role string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE group_members SET role = $3 WHERE group_id = $1 AND user_id = $2
	`, groupID, userID, role)
	return err
}

// GetMember returns a group member by primary key.
func (s *Store) GetMember(ctx context.Context, groupID, userID uuid.UUID) (*GroupMember, error) {
	var m GroupMember
	err := s.db.QueryRow(ctx, `
		SELECT group_id, user_id, role, joined_at
		FROM group_members WHERE group_id = $1 AND user_id = $2
	`, groupID, userID).Scan(&m.GroupID, &m.UserID, &m.Role, &m.JoinedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// CheckMembership returns true if the user is a member of the group.
func (s *Store) CheckMembership(ctx context.Context, groupID, userID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM group_members WHERE group_id = $1 AND user_id = $2)
	`, groupID, userID).Scan(&exists)
	return exists, err
}

// ListMembers returns paginated group members.
func (s *Store) ListMembers(ctx context.Context, groupID uuid.UUID, limit, offset int) ([]GroupMember, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT group_id, user_id, role, joined_at
		FROM group_members
		WHERE group_id = $1
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
		if err := rows.Scan(&m.GroupID, &m.UserID, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// --- Invites ---

// CreateInvite creates or re-opens an invite for a user to a group.
func (s *Store) CreateInvite(ctx context.Context, inv *GroupInvite) error {
	err := s.db.QueryRow(ctx, `
		INSERT INTO group_invites (group_id, inviter_id, invitee_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (group_id, invitee_id) DO UPDATE
		SET status = 'pending', inviter_id = $2, updated_at = NOW()
		RETURNING id, status, created_at, updated_at
	`, inv.GroupID, inv.InviterID, inv.InviteeID).Scan(
		&inv.ID, &inv.Status, &inv.CreatedAt, &inv.UpdatedAt,
	)
	return err
}

// GetInviteByID returns an invite by its primary key.
func (s *Store) GetInviteByID(ctx context.Context, id uuid.UUID) (*GroupInvite, error) {
	var inv GroupInvite
	err := s.db.QueryRow(ctx, `
		SELECT id, group_id, inviter_id, invitee_id, status, created_at, updated_at
		FROM group_invites WHERE id = $1
	`, id).Scan(
		&inv.ID, &inv.GroupID, &inv.InviterID, &inv.InviteeID, &inv.Status,
		&inv.CreatedAt, &inv.UpdatedAt,
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
		SELECT id, group_id, inviter_id, invitee_id, status, created_at, updated_at
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
		if err := rows.Scan(&inv.ID, &inv.GroupID, &inv.InviterID, &inv.InviteeID, &inv.Status, &inv.CreatedAt, &inv.UpdatedAt); err != nil {
			return nil, err
		}
		invites = append(invites, inv)
	}
	return invites, rows.Err()
}

// ListGroupInvites returns pending invites for a given group.
func (s *Store) ListGroupInvites(ctx context.Context, groupID uuid.UUID) ([]GroupInvite, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, group_id, inviter_id, invitee_id, status, created_at, updated_at
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
		if err := rows.Scan(&inv.ID, &inv.GroupID, &inv.InviterID, &inv.InviteeID, &inv.Status, &inv.CreatedAt, &inv.UpdatedAt); err != nil {
			return nil, err
		}
		invites = append(invites, inv)
	}
	return invites, rows.Err()
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
		SELECT g.id, g.name, g.description, g.avatar_media_id, g.cover_media_id, g.creator_id,
		       g.visibility, g.is_archived, g.chat_conversation_id, g.member_count, g.post_count,
		       g.created_at, g.updated_at
		FROM groups g
		JOIN group_members gm ON g.id = gm.group_id
		WHERE gm.user_id = $1
		ORDER BY gm.joined_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(
			&g.ID, &g.Name, &g.Description, &g.AvatarMediaID, &g.CoverMediaID, &g.CreatorID,
			&g.Visibility, &g.IsArchived, &g.ChatConversationID, &g.MemberCount, &g.PostCount,
			&g.CreatedAt, &g.UpdatedAt,
		); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// DiscoverPublicGroups returns public, non-archived groups sorted by member count.
func (s *Store) DiscoverPublicGroups(ctx context.Context, limit, offset int) ([]Group, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, name, description, avatar_media_id, cover_media_id, creator_id,
		       visibility, is_archived, chat_conversation_id, member_count, post_count,
		       created_at, updated_at
		FROM groups
		WHERE visibility = 'public' AND is_archived = FALSE
		ORDER BY member_count DESC
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(
			&g.ID, &g.Name, &g.Description, &g.AvatarMediaID, &g.CoverMediaID, &g.CreatorID,
			&g.Visibility, &g.IsArchived, &g.ChatConversationID, &g.MemberCount, &g.PostCount,
			&g.CreatedAt, &g.UpdatedAt,
		); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// SearchGroups performs full-text search on group names.
func (s *Store) SearchGroups(ctx context.Context, query string, limit, offset int) ([]Group, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, name, description, avatar_media_id, cover_media_id, creator_id,
		       visibility, is_archived, chat_conversation_id, member_count, post_count,
		       created_at, updated_at
		FROM groups
		WHERE to_tsvector('english', name) @@ to_tsquery('english', $1)
		  AND is_archived = FALSE
		ORDER BY member_count DESC
		LIMIT $2 OFFSET $3
	`, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(
			&g.ID, &g.Name, &g.Description, &g.AvatarMediaID, &g.CoverMediaID, &g.CreatorID,
			&g.Visibility, &g.IsArchived, &g.ChatConversationID, &g.MemberCount, &g.PostCount,
			&g.CreatedAt, &g.UpdatedAt,
		); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}
