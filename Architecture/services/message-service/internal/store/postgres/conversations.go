package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ConversationStore struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *ConversationStore {
	return &ConversationStore{db: db}
}

type Conversation struct {
	ID                 uuid.UUID  `json:"id"`
	Type               string     `json:"type"`
	Name               *string    `json:"name,omitempty"`
	IconURL            *string    `json:"icon_url,omitempty"`
	CreatedBy          *uuid.UUID `json:"created_by,omitempty"`
	LastMessageAt      *time.Time `json:"last_message_at,omitempty"`
	LastMessagePreview *string    `json:"last_message_preview,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type ConversationMember struct {
	ConversationID    uuid.UUID  `json:"conversation_id"`
	UserID            uuid.UUID  `json:"user_id"`
	Role              string     `json:"role"`
	LastReadMessageID *string    `json:"last_read_message_id,omitempty"`
	LastReadAt        *time.Time `json:"last_read_at,omitempty"`
	JoinedAt          time.Time  `json:"joined_at"`
	LeftAt            *time.Time `json:"left_at,omitempty"`
}

// CreateDirectConversation idempotently creates a conversation
func (s *ConversationStore) CreateDirectConversation(ctx context.Context, userA, userB uuid.UUID) (uuid.UUID, error) {
	// Ensure UserA < UserB for canonical key
	if userA.String() > userB.String() {
		userA, userB = userB, userA
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	// 1. Check if exists
	var conversationID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT conversation_id FROM chat.direct_conversation_keys WHERE user_a = $1 AND user_b = $2`, userA, userB).Scan(&conversationID)
	if err == nil {
		return conversationID, nil // Already exists
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}

	// 2. Create Conversation
	conversationID = uuid.New()
	now := time.Now()
	_, err = tx.Exec(ctx, `INSERT INTO chat.conversations (id, type, created_at, updated_at) VALUES ($1, 'direct', $2, $2)`, conversationID, now)
	if err != nil {
		return uuid.Nil, err
	}

	// 3. Add Members
	_, err = tx.Exec(ctx, `INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at) VALUES ($1, $2, 'member', $3), ($1, $4, 'member', $3)`, conversationID, userA, now, userB)
	if err != nil {
		return uuid.Nil, err
	}

	// 4. Set Key
	_, err = tx.Exec(ctx, `INSERT INTO chat.direct_conversation_keys (user_a, user_b, conversation_id) VALUES ($1, $2, $3)`, userA, userB, conversationID)
	if err != nil {
		return uuid.Nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}

	return conversationID, nil
}

func (s *ConversationStore) GetConversation(ctx context.Context, conversationID uuid.UUID) (*Conversation, error) {
	var c Conversation
	err := s.db.QueryRow(ctx, `
		SELECT id, type, name, icon_url, created_by, last_message_at, last_message_preview, created_at, updated_at
		FROM chat.conversations
		WHERE id = $1
	`, conversationID).Scan(&c.ID, &c.Type, &c.Name, &c.IconURL, &c.CreatedBy, &c.LastMessageAt, &c.LastMessagePreview, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (s *ConversationStore) TouchConversation(ctx context.Context, conversationID uuid.UUID, ts time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversations
		SET updated_at = $2
		WHERE id = $1
	`, conversationID, ts)
	return err
}

// TouchConversationWithPreview updates the conversation timestamp and last message preview.
func (s *ConversationStore) TouchConversationWithPreview(ctx context.Context, conversationID uuid.UUID, ts time.Time, preview string) error {
	if len(preview) > 100 {
		preview = preview[:100]
	}
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversations
		SET updated_at = $2, last_message_at = $2, last_message_preview = $3
		WHERE id = $1
	`, conversationID, ts, preview)
	return err
}

// UpdateConversation updates group name and/or icon.
func (s *ConversationStore) UpdateConversation(ctx context.Context, conversationID uuid.UUID, name, iconURL *string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversations
		SET name = COALESCE($2, name),
		    icon_url = COALESCE($3, icon_url),
		    updated_at = NOW()
		WHERE id = $1
	`, conversationID, name, iconURL)
	return err
}

func (s *ConversationStore) ListConversationsByUser(ctx context.Context, userID uuid.UUID, limit int, cursorUpdatedAt *time.Time, cursorID *uuid.UUID) ([]Conversation, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	var rows pgx.Rows
	var err error
	if cursorUpdatedAt != nil && cursorID != nil {
		rows, err = s.db.Query(ctx, `
			SELECT c.id, c.type, c.name, c.icon_url, c.created_by, c.last_message_at, c.last_message_preview, c.created_at, c.updated_at
			FROM chat.conversations c
			JOIN chat.conversation_members m ON m.conversation_id = c.id
			WHERE m.user_id = $1
			  AND m.left_at IS NULL
			  AND (c.updated_at, c.id) < ($2, $3)
			ORDER BY c.updated_at DESC, c.id DESC
			LIMIT $4
		`, userID, *cursorUpdatedAt, *cursorID, limit)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT c.id, c.type, c.name, c.icon_url, c.created_by, c.last_message_at, c.last_message_preview, c.created_at, c.updated_at
			FROM chat.conversations c
			JOIN chat.conversation_members m ON m.conversation_id = c.id
			WHERE m.user_id = $1
			  AND m.left_at IS NULL
			ORDER BY c.updated_at DESC, c.id DESC
			LIMIT $2
		`, userID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.Type, &c.Name, &c.IconURL, &c.CreatedBy, &c.LastMessageAt, &c.LastMessagePreview, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CheckMembership checks if a user is an active member of a conversation.
func (s *ConversationStore) CheckMembership(ctx context.Context, conversationID, userID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM chat.conversation_members WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL)
	`, conversationID, userID).Scan(&exists)
	return exists, err
}

// GetMembers returns active members of a conversation.
func (s *ConversationStore) GetMembers(ctx context.Context, conversationID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `SELECT user_id FROM chat.conversation_members WHERE conversation_id = $1 AND left_at IS NULL`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []uuid.UUID
	for rows.Next() {
		var uid uuid.UUID
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		members = append(members, uid)
	}
	return members, nil
}

// GetMembersWithReadState returns members with their read state.
func (s *ConversationStore) GetMembersWithReadState(ctx context.Context, conversationID uuid.UUID) ([]ConversationMember, error) {
	rows, err := s.db.Query(ctx, `
		SELECT conversation_id, user_id, role, last_read_message_id, last_read_at, joined_at, left_at
		FROM chat.conversation_members
		WHERE conversation_id = $1 AND left_at IS NULL
	`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []ConversationMember
	for rows.Next() {
		var m ConversationMember
		if err := rows.Scan(&m.ConversationID, &m.UserID, &m.Role, &m.LastReadMessageID, &m.LastReadAt, &m.JoinedAt, &m.LeftAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// MarkRead updates the read cursor for a user in a conversation and inserts a read receipt.
func (s *ConversationStore) MarkRead(ctx context.Context, conversationID, userID uuid.UUID, messageID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	now := time.Now()

	// Update conversation member read cursor
	_, err = tx.Exec(ctx, `
		UPDATE chat.conversation_members
		SET last_read_message_id = $3, last_read_at = $4
		WHERE conversation_id = $1 AND user_id = $2
	`, conversationID, userID, messageID, now)
	if err != nil {
		return err
	}

	// Insert read receipt
	_, err = tx.Exec(ctx, `
		INSERT INTO chat.message_reads (conversation_id, user_id, message_id, read_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (conversation_id, user_id, message_id) DO NOTHING
	`, conversationID, userID, messageID, now)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// GetReadReceipts returns which users have read which messages.
func (s *ConversationStore) GetReadReceipts(ctx context.Context, conversationID uuid.UUID, messageIDs []string) (map[string][]uuid.UUID, error) {
	if len(messageIDs) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT message_id, user_id
		FROM chat.message_reads
		WHERE conversation_id = $1 AND message_id = ANY($2)
	`, conversationID, messageIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string][]uuid.UUID)
	for rows.Next() {
		var msgID string
		var uid uuid.UUID
		if err := rows.Scan(&msgID, &uid); err != nil {
			return nil, err
		}
		result[msgID] = append(result[msgID], uid)
	}
	return result, rows.Err()
}

// LeaveConversation marks a user as having left a conversation.
func (s *ConversationStore) LeaveConversation(ctx context.Context, conversationID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversation_members
		SET left_at = NOW()
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
	`, conversationID, userID)
	return err
}

// CreateGroupConversation creates a group-type conversation with initial members.
func (s *ConversationStore) CreateGroupConversation(ctx context.Context, creatorID uuid.UUID, memberIDs []uuid.UUID, name string) (uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	conversationID := uuid.New()
	now := time.Now()

	_, err = tx.Exec(ctx, `
		INSERT INTO chat.conversations (id, type, name, created_by, created_at, updated_at)
		VALUES ($1, 'group', $2, $3, $4, $4)
	`, conversationID, name, creatorID, now)
	if err != nil {
		return uuid.Nil, err
	}

	// Add creator as admin
	_, err = tx.Exec(ctx, `
		INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at)
		VALUES ($1, $2, 'admin', $3)
	`, conversationID, creatorID, now)
	if err != nil {
		return uuid.Nil, err
	}

	// Add other members (deduplicated)
	seen := map[uuid.UUID]bool{creatorID: true}
	for _, uid := range memberIDs {
		if seen[uid] {
			continue
		}
		seen[uid] = true
		_, err = tx.Exec(ctx, `
			INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at)
			VALUES ($1, $2, 'member', $3)
		`, conversationID, uid, now)
		if err != nil {
			return uuid.Nil, err
		}
	}

	return conversationID, tx.Commit(ctx)
}

// AddGroupConversationMember adds a member to a group conversation.
func (s *ConversationStore) AddGroupConversationMember(ctx context.Context, conversationID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at)
		VALUES ($1, $2, 'member', NOW())
		ON CONFLICT (conversation_id, user_id) DO UPDATE SET left_at = NULL, joined_at = NOW()
	`, conversationID, userID)
	return err
}

// RemoveGroupConversationMember removes a member from a group conversation.
func (s *ConversationStore) RemoveGroupConversationMember(ctx context.Context, conversationID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversation_members
		SET left_at = NOW()
		WHERE conversation_id = $1 AND user_id = $2 AND left_at IS NULL
	`, conversationID, userID)
	return err
}
