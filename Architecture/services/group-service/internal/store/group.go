package store

import (
	"context"
	"encoding/json"
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
	// V2 fields
	Handle              string     `json:"handle,omitempty"`
	Category            string     `json:"category,omitempty"`
	PrivacyLevel        string     `json:"privacy_level"`
	JoinMode            string     `json:"join_mode"`
	WhoCanPost          string     `json:"who_can_post"`
	WhoCanInvite        string     `json:"who_can_invite"`
	Location            string     `json:"location,omitempty"`
	Language            string     `json:"language,omitempty"`
	Status              string     `json:"status"`
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
	IsMature          bool            `json:"is_mature"`
	// Whether this group lets members post without their name shown.
	// Opt-in, default false: a group that never asked for anonymity must not
	// acquire it because the feature shipped.
	AllowAnonymousPosts bool `json:"allow_anonymous_posts"`
}

type Store struct {
	db *pgxpool.Pool
	// holders gates LockMediaHolder so lock holders can never exhaust db
	// (media_holder_gate.go).
	holders *holderGate
}

func New(db *pgxpool.Pool) *Store {
	maxConns := int32(0)
	if db != nil {
		maxConns = db.Config().MaxConns
	}
	return &Store{db: db, holders: newHolderGate(holderSlotsFor(maxConns))}
}

// v2 group columns for SELECT queries — use g. prefix for JOIN safety
/*
	groupColumns is the one projection every group read scans through
	(scanGroup / scanGroups), so it has to be safe for every row the table
	can hold — not only the rows the service itself writes.

	handle is COALESCEd. The column is nullable and carries a UNIQUE index,
	so it cannot default to '' at the schema level the way category,
	location and language do (migration 001). Group.Handle is a plain
	string, and pgx refuses to scan NULL into *string. One NULL-handle row
	— written directly to the table, not through CreateGroup — made
	ListGroupsByUser fail for EVERY member of that group: GET /v1/groups/my
	answered 500 for an account whose other groups were fine, while
	GET /v1/groups/:id on those other groups kept working. A list that dies
	on one bad row is worse than a row with an empty handle; the JSON tag is
	omitempty, so '' and NULL are the same thing on the wire.
*/
const groupColumns = `g.id, g.name, g.description, g.avatar_media_id, g.cover_media_id, g.creator_id,
       g.visibility, g.is_archived, g.chat_conversation_id, g.member_count, g.post_count,
       g.created_at, g.updated_at, COALESCE(g.handle, '') AS handle, g.category, g.privacy_level, g.join_mode,
       g.who_can_post, g.who_can_invite, g.location, g.language, g.status, g.deleted_at, g.pending_request_count,
       g.group_type, g.max_members, g.join_questions, g.topic_tags, g.comment_permission, g.member_list_visible, g.link_sharing, g.is_mature, g.allow_anonymous_posts`

func scanGroup(row pgx.Row) (*Group, error) {
	var g Group
	err := row.Scan(
		&g.ID, &g.Name, &g.Description, &g.AvatarMediaID, &g.CoverMediaID, &g.CreatorID,
		&g.Visibility, &g.IsArchived, &g.ChatConversationID, &g.MemberCount, &g.PostCount,
		&g.CreatedAt, &g.UpdatedAt, &g.Handle, &g.Category, &g.PrivacyLevel, &g.JoinMode,
		&g.WhoCanPost, &g.WhoCanInvite, &g.Location, &g.Language, &g.Status, &g.DeletedAt, &g.PendingRequestCount,
		&g.GroupType, &g.MaxMembers, &g.JoinQuestions, &g.TopicTags, &g.CommentPermission, &g.MemberListVisible, &g.LinkSharing, &g.IsMature, &g.AllowAnonymousPosts,
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
			&g.GroupType, &g.MaxMembers, &g.JoinQuestions, &g.TopicTags, &g.CommentPermission, &g.MemberListVisible, &g.LinkSharing, &g.IsMature, &g.AllowAnonymousPosts,
		); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

// --- Groups ---

/*
	FindGroupByIdempotencyKey returns the group a previous request with this key
	created, or nil if there is none.

	CreateGroup already refuses to create twice for one key, but it is the LAST
	thing the service does — name, handle and privacy are all validated first.
	A retry of a request that actually succeeded therefore died on "handle is
	already taken" by its own group, which is the one answer a retry must never
	get. This lets the service answer a replay before it validates anything.

	No advisory lock here: this is a plain read, and two concurrent first-time
	requests still serialise inside CreateGroup, which takes the lock. The worst
	case is that both miss here and one waits there, which is the behaviour that
	already existed.
*/
func (s *Store) FindGroupByIdempotencyKey(ctx context.Context, creatorID uuid.UUID, key string) (*Group, error) {
	if key == "" {
		return nil, nil
	}
	var groupID uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT group_id FROM group_creation_requests
		WHERE creator_id=$1 AND idempotency_key=$2
	`, creatorID, key).Scan(&groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetGroupByID(ctx, groupID)
}

// CreateGroup inserts a new group and adds the creator as an admin member.
func (s *Store) CreateGroup(ctx context.Context, g *Group, idempotencyKey string) (*uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, g.CreatorID.String()+":"+idempotencyKey); err != nil {
		return nil, err
	}
	var existingID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT group_id FROM group_creation_requests
		WHERE creator_id=$1 AND idempotency_key=$2
	`, g.CreatorID, idempotencyKey).Scan(&existingID)
	if err == nil {
		return &existingID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	err = tx.QueryRow(ctx, `
		INSERT INTO groups (name, description, creator_id, visibility, handle, category,
		                    privacy_level, join_mode, who_can_post, who_can_invite,
		                    location, language, status,
		                    group_type, max_members, join_questions, topic_tags,
		                    comment_permission, member_list_visible, link_sharing, is_mature)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
		        $14, $15, $16, $17, $18, $19, $20, $21)
		RETURNING id, member_count, post_count, is_archived, created_at, updated_at
	`, g.Name, g.Description, g.CreatorID, g.Visibility, g.Handle, g.Category,
		g.PrivacyLevel, g.JoinMode, g.WhoCanPost, g.WhoCanInvite,
		g.Location, g.Language, g.Status,
		g.GroupType, g.MaxMembers, g.JoinQuestions, g.TopicTags,
		g.CommentPermission, g.MemberListVisible, g.LinkSharing, g.IsMature).Scan(
		&g.ID, &g.MemberCount, &g.PostCount, &g.IsArchived, &g.CreatedAt, &g.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO group_members (group_id, user_id, role, status)
		VALUES ($1, $2, 'admin', 'active')
	`, g.ID, g.CreatorID)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx, `
		UPDATE groups SET member_count = 1 WHERE id = $1
	`, g.ID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO group_creation_requests (creator_id, idempotency_key, group_id)
		VALUES ($1,$2,$3)
	`, g.CreatorID, idempotencyKey, g.ID); err != nil {
		return nil, err
	}

	g.MemberCount = 1
	return nil, tx.Commit(ctx)
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
		WHERE g.privacy_level = 'public' AND `+listableGroupSQL+` AND g.group_type = $3
		ORDER BY g.member_count DESC
		LIMIT $1 OFFSET $2
	`, limit, offset, groupType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanGroups(rows)
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
		WHERE g.privacy_level = 'public' AND `+listableGroupSQL+`
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
/*
websearch_to_tsquery, not to_tsquery.

This used to pass the raw query string to to_tsquery, which parses an OPERATOR
expression rather than free text. `book & club` is valid there; `book club` —
two words, which is what a search box produces — raises

	syntax error in tsquery: "book club"

from Postgres. The error reaches the handler as a plain error and becomes a
500, so searching for any multi-word group name has always failed. A user could
also crash it with a lone `&` or `!`.

websearch_to_tsquery (Postgres 11+; this service targets 16) accepts exactly
what a search box produces — bare words, "quoted phrases", or, -negation — and
is documented never to raise a syntax error on any input.

The INDEX is unaffected: idx_groups_name_search is on to_tsvector('english',
name), the left-hand side, which is unchanged.
*/
func (s *Store) SearchGroups(ctx context.Context, query string, limit, offset int) ([]Group, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+groupColumns+`
		FROM groups g
		WHERE to_tsvector('english', g.name) @@ websearch_to_tsquery('english', $1)
		  AND `+listableGroupSQL+`
		ORDER BY g.member_count DESC
		LIMIT $2 OFFSET $3
	`, query, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanGroups(rows)
}

/*
	listableGroupSQL is the one rule for "may this group appear in a list a
	stranger sees": search, discover and the ranked discover all use it.

	Search filtered only status = 'active', so it listed every matching private
	group with its name, description, creator and member count (G-M1). A
	"private" or "hidden" group_type never changed privacy_level before G-L1,
	so rows created before that fix are excluded by their type as well.
	Restricted groups are listed: their card is public and they take requests.
*/
const listableGroupSQL = `g.status = 'active'
		  AND g.privacy_level IN ('public', 'restricted')
		  AND g.visibility <> 'private'
		  AND g.group_type NOT IN ('private', 'hidden')`
