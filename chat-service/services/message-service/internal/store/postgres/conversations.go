package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Conversation struct {
	ID        uuid.UUID  `json:"id"`
	Type      string     `json:"type"`
	Title     *string    `json:"title,omitempty"`
	CreatedBy *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type Member struct {
	UserID   uuid.UUID `json:"user_id"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

type OutboxEvent struct {
	ID        int64           `json:"id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type IdempotencyResult struct {
	RequestHash string          `json:"request_hash"`
	Response    json.RawMessage `json:"response"`
}

type ConversationStore struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *ConversationStore {
	return &ConversationStore{db: db}
}

// CreateDirectConversation idempotently creates a 1:1 conversation.
func (s *ConversationStore) CreateDirectConversation(ctx context.Context, userA, userB, createdBy uuid.UUID) (uuid.UUID, error) {
	if userA.String() > userB.String() {
		userA, userB = userB, userA
	}
	pairKey := userA.String() + ":" + userB.String()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	// Serialize direct-conversation creation per pair to avoid duplicate rows under race.
	lockRows, err := tx.Query(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, pairKey)
	if err != nil {
		return uuid.Nil, err
	}
	lockRows.Close()

	var conversationID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT conversation_id FROM chat.direct_conversation_keys WHERE user_a = $1 AND user_b = $2`, userA, userB).Scan(&conversationID)
	if err == nil {
		return conversationID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}

	conversationID = uuid.New()
	now := time.Now()

	_, err = tx.Exec(ctx, `INSERT INTO chat.conversations (id, type, created_by, created_at, updated_at) VALUES ($1, 'direct', $2, $3, $3)`, conversationID, createdBy, now)
	if err != nil {
		return uuid.Nil, err
	}

	_, err = tx.Exec(ctx, `INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at) VALUES ($1, $2, 'member', $3), ($1, $4, 'member', $3)`, conversationID, userA, now, userB)
	if err != nil {
		return uuid.Nil, err
	}

	_, err = tx.Exec(ctx, `INSERT INTO chat.direct_conversation_keys (user_a, user_b, conversation_id) VALUES ($1, $2, $3)`, userA, userB, conversationID)
	if err != nil {
		return uuid.Nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return conversationID, nil
}

// CreateGroupConversation creates a group conversation with the creator as admin.
func (s *ConversationStore) CreateGroupConversation(ctx context.Context, creatorID uuid.UUID, title string, memberIDs []uuid.UUID) (uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)

	conversationID := uuid.New()
	now := time.Now()

	_, err = tx.Exec(ctx, `INSERT INTO chat.conversations (id, type, title, created_by, created_at, updated_at) VALUES ($1, 'group', $2, $3, $4, $4)`, conversationID, title, creatorID, now)
	if err != nil {
		return uuid.Nil, err
	}

	// Creator is admin
	_, err = tx.Exec(ctx, `INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at) VALUES ($1, $2, 'admin', $3)`, conversationID, creatorID, now)
	if err != nil {
		return uuid.Nil, err
	}

	// Other members
	for _, memberID := range memberIDs {
		if memberID == creatorID {
			continue
		}
		_, err = tx.Exec(ctx, `INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at) VALUES ($1, $2, 'member', $3) ON CONFLICT DO NOTHING`, conversationID, memberID, now)
		if err != nil {
			return uuid.Nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return conversationID, nil
}

func (s *ConversationStore) GetConversation(ctx context.Context, conversationID uuid.UUID) (*Conversation, error) {
	var c Conversation
	err := s.db.QueryRow(ctx, `
		SELECT id, type, title, created_by, created_at, updated_at
		FROM chat.conversations WHERE id = $1
	`, conversationID).Scan(&c.ID, &c.Type, &c.Title, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (s *ConversationStore) TouchConversation(ctx context.Context, conversationID uuid.UUID, ts time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE chat.conversations SET updated_at = $2 WHERE id = $1`, conversationID, ts)
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
			SELECT c.id, c.type, c.title, c.created_by, c.created_at, c.updated_at
			FROM chat.conversations c
			JOIN chat.conversation_members m ON m.conversation_id = c.id
			WHERE m.user_id = $1 AND (c.updated_at, c.id) < ($2, $3)
			ORDER BY c.updated_at DESC, c.id DESC
			LIMIT $4
		`, userID, *cursorUpdatedAt, *cursorID, limit)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT c.id, c.type, c.title, c.created_by, c.created_at, c.updated_at
			FROM chat.conversations c
			JOIN chat.conversation_members m ON m.conversation_id = c.id
			WHERE m.user_id = $1
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
		if err := rows.Scan(&c.ID, &c.Type, &c.Title, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *ConversationStore) CheckMembership(ctx context.Context, conversationID, userID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM chat.conversation_members WHERE conversation_id = $1 AND user_id = $2)`, conversationID, userID).Scan(&exists)
	return exists, err
}

func (s *ConversationStore) GetMembers(ctx context.Context, conversationID uuid.UUID) ([]Member, error) {
	rows, err := s.db.Query(ctx, `SELECT user_id, role, joined_at FROM chat.conversation_members WHERE conversation_id = $1`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func (s *ConversationStore) GetMemberRole(ctx context.Context, conversationID, userID uuid.UUID) (string, error) {
	var role string
	err := s.db.QueryRow(ctx, `SELECT role FROM chat.conversation_members WHERE conversation_id = $1 AND user_id = $2`, conversationID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return role, nil
}

func (s *ConversationStore) AddMember(ctx context.Context, conversationID, userID uuid.UUID, role string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO chat.conversation_members (conversation_id, user_id, role, joined_at) VALUES ($1, $2, $3, NOW()) ON CONFLICT DO NOTHING`, conversationID, userID, role)
	return err
}

func (s *ConversationStore) RemoveMember(ctx context.Context, conversationID, userID uuid.UUID) (bool, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM chat.conversation_members WHERE conversation_id = $1 AND user_id = $2`, conversationID, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (s *ConversationStore) UpdateTitle(ctx context.Context, conversationID uuid.UUID, title string) error {
	_, err := s.db.Exec(ctx, `UPDATE chat.conversations SET title = $2, updated_at = NOW() WHERE id = $1`, conversationID, title)
	return err
}

// --- Outbox ---

func (s *ConversationStore) InsertOutboxEvent(ctx context.Context, eventType string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO chat.outbox_events (event_type, payload) VALUES ($1, $2)`, eventType, data)
	return err
}

func (s *ConversationStore) FetchUnpublishedOutboxEvents(ctx context.Context, limit int) ([]OutboxEvent, error) {
	rows, err := s.db.Query(ctx, `SELECT id, event_type, payload, created_at FROM chat.outbox_events WHERE published_at IS NULL ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OutboxEvent
	for rows.Next() {
		var e OutboxEvent
		if err := rows.Scan(&e.ID, &e.EventType, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *ConversationStore) MarkOutboxEventPublished(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `UPDATE chat.outbox_events SET published_at = NOW() WHERE id = $1`, id)
	return err
}

// --- Idempotency ---

func (s *ConversationStore) CheckIdempotencyKey(ctx context.Context, key string) (*IdempotencyResult, error) {
	var requestHash string
	var response json.RawMessage
	err := s.db.QueryRow(ctx, `SELECT request_hash, response FROM chat.idempotency_keys WHERE key = $1 AND expires_at > NOW()`, key).Scan(&requestHash, &response)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &IdempotencyResult{RequestHash: requestHash, Response: response}, nil
}

func (s *ConversationStore) CreateIdempotencyKey(ctx context.Context, key, requestHash string) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO chat.idempotency_keys (key, request_hash, response)
		VALUES ($1, $2, NULL)
		ON CONFLICT (key) DO NOTHING
	`, key, requestHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (s *ConversationStore) SaveIdempotencyResponse(ctx context.Context, key, requestHash string, response interface{}) error {
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE chat.idempotency_keys
		SET response = $3
		WHERE key = $1 AND request_hash = $2
	`, key, requestHash, data)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("idempotency key not found or request hash mismatch")
	}
	return nil
}

func (s *ConversationStore) ReleaseIdempotencyKey(ctx context.Context, key, requestHash string) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM chat.idempotency_keys
		WHERE key = $1 AND request_hash = $2 AND response IS NULL
	`, key, requestHash)
	return err
}
