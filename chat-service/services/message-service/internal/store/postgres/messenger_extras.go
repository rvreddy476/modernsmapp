package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// --- Types ---

type ConversationSettings struct {
	ConversationID      uuid.UUID  `json:"conversation_id"`
	UserID              uuid.UUID  `json:"user_id"`
	Label               *string    `json:"label,omitempty"`
	IsMuted             bool       `json:"is_muted"`
	MuteUntil           *time.Time `json:"mute_until,omitempty"`
	DisappearAfterMs    *int64     `json:"disappear_after_ms,omitempty"`
	ReadReceiptsEnabled bool       `json:"read_receipts_enabled"`
	Theme               string     `json:"theme"`
	IsPinned            bool       `json:"is_pinned"`
	PinnedAt            *time.Time `json:"pinned_at,omitempty"`
}

type ChatFolder struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Name      string    `json:"name"`
	Icon      string    `json:"icon"`
	SortOrder int       `json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
}

type ConversationPin struct {
	ID             uuid.UUID `json:"id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	MessageID      uuid.UUID `json:"message_id"`
	PinnedBy       uuid.UUID `json:"pinned_by"`
	PinnedAt       time.Time `json:"pinned_at"`
}

type MessageRequestSettings struct {
	UserID               uuid.UUID `json:"user_id"`
	AllowFrom            string    `json:"allow_from"`
	AutoFilterLikelySpam bool      `json:"auto_filter_likely_spam"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type StarredMessage struct {
	ID             uuid.UUID `json:"id"`
	UserID         uuid.UUID `json:"user_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	MessageID      uuid.UUID `json:"message_id"`
	MessagePreview *string   `json:"message_preview,omitempty"`
	StarredAt      time.Time `json:"starred_at"`
}

type ChatBackup struct {
	ID               uuid.UUID  `json:"id"`
	UserID           uuid.UUID  `json:"user_id"`
	Status           string     `json:"status"`
	SizeBytes        *int64     `json:"size_bytes,omitempty"`
	MessageCount     *int64     `json:"message_count,omitempty"`
	EncryptedBlobURL *string    `json:"encrypted_blob_url,omitempty"`
	KeyHint          *string    `json:"key_hint,omitempty"`
	BackupVersion    int        `json:"backup_version"`
	CreatedAt        time.Time  `json:"created_at"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
}

type ScheduledMessage struct {
	ID             uuid.UUID  `json:"id"`
	ConversationID uuid.UUID  `json:"conversation_id"`
	SenderID       uuid.UUID  `json:"sender_id"`
	Type           string     `json:"type"`
	Content        *string    `json:"content,omitempty"`
	MediaID        *uuid.UUID `json:"media_id,omitempty"`
	SendAt         time.Time  `json:"send_at"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
}

type MessageTranslation struct {
	MessageID      uuid.UUID `json:"message_id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	TargetLang     string    `json:"target_lang"`
	TranslatedText string    `json:"translated_text"`
	SourceLang     *string   `json:"source_lang,omitempty"`
	TranslatedAt   time.Time `json:"translated_at"`
}

type MessageThread struct {
	ID               uuid.UUID  `json:"id"`
	ConversationID   uuid.UUID  `json:"conversation_id"`
	ParentMessageID  uuid.UUID  `json:"parent_message_id"`
	ReplyCount       int        `json:"reply_count"`
	LastReplyAt      *time.Time `json:"last_reply_at,omitempty"`
	LastReplyPreview *string    `json:"last_reply_preview,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// --- Conversation Settings ---

func (s *ConversationStore) UpsertConversationSettings(ctx context.Context, settings *ConversationSettings) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.conversation_settings
			(conversation_id, user_id, label, is_muted, mute_until, disappear_after_ms, read_receipts_enabled, theme, is_pinned, pinned_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (conversation_id, user_id) DO UPDATE SET
			label                 = EXCLUDED.label,
			is_muted              = EXCLUDED.is_muted,
			mute_until            = EXCLUDED.mute_until,
			disappear_after_ms    = EXCLUDED.disappear_after_ms,
			read_receipts_enabled = EXCLUDED.read_receipts_enabled,
			theme                 = EXCLUDED.theme,
			is_pinned             = EXCLUDED.is_pinned,
			pinned_at             = EXCLUDED.pinned_at
	`, settings.ConversationID, settings.UserID, settings.Label, settings.IsMuted,
		settings.MuteUntil, settings.DisappearAfterMs, settings.ReadReceiptsEnabled,
		settings.Theme, settings.IsPinned, settings.PinnedAt)
	return err
}

func (s *ConversationStore) GetConversationSettings(ctx context.Context, convID, userID uuid.UUID) (*ConversationSettings, error) {
	var cs ConversationSettings
	err := s.db.QueryRow(ctx, `
		SELECT conversation_id, user_id, label, is_muted, mute_until, disappear_after_ms,
		       read_receipts_enabled, theme, is_pinned, pinned_at
		FROM chat.conversation_settings
		WHERE conversation_id = $1 AND user_id = $2
	`, convID, userID).Scan(
		&cs.ConversationID, &cs.UserID, &cs.Label, &cs.IsMuted, &cs.MuteUntil,
		&cs.DisappearAfterMs, &cs.ReadReceiptsEnabled, &cs.Theme, &cs.IsPinned, &cs.PinnedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Return defaults
			return &ConversationSettings{
				ConversationID:      convID,
				UserID:              userID,
				ReadReceiptsEnabled: true,
				Theme:               "default",
			}, nil
		}
		return nil, err
	}
	return &cs, nil
}

func (s *ConversationStore) ListConversationsByLabel(ctx context.Context, userID uuid.UUID, label string, limit, offset int) ([]ConversationSettings, error) {
	rows, err := s.db.Query(ctx, `
		SELECT conversation_id, user_id, label, is_muted, mute_until, disappear_after_ms,
		       read_receipts_enabled, theme, is_pinned, pinned_at
		FROM chat.conversation_settings
		WHERE user_id = $1 AND label = $2
		ORDER BY pinned_at DESC NULLS LAST, conversation_id
		LIMIT $3 OFFSET $4
	`, userID, label, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ConversationSettings
	for rows.Next() {
		var cs ConversationSettings
		if err := rows.Scan(
			&cs.ConversationID, &cs.UserID, &cs.Label, &cs.IsMuted, &cs.MuteUntil,
			&cs.DisappearAfterMs, &cs.ReadReceiptsEnabled, &cs.Theme, &cs.IsPinned, &cs.PinnedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

// --- Chat Folders ---

func (s *ConversationStore) CreateChatFolder(ctx context.Context, f *ChatFolder) (*ChatFolder, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO chat.chat_folders (user_id, name, icon, sort_order)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, name, icon, sort_order, created_at
	`, f.UserID, f.Name, f.Icon, f.SortOrder).Scan(
		&f.ID, &f.UserID, &f.Name, &f.Icon, &f.SortOrder, &f.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (s *ConversationStore) GetChatFolder(ctx context.Context, id uuid.UUID) (*ChatFolder, error) {
	var f ChatFolder
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, name, icon, sort_order, created_at
		FROM chat.chat_folders WHERE id = $1
	`, id).Scan(&f.ID, &f.UserID, &f.Name, &f.Icon, &f.SortOrder, &f.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}

func (s *ConversationStore) ListChatFolders(ctx context.Context, userID uuid.UUID) ([]ChatFolder, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, name, icon, sort_order, created_at
		FROM chat.chat_folders
		WHERE user_id = $1
		ORDER BY sort_order ASC, created_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChatFolder
	for rows.Next() {
		var f ChatFolder
		if err := rows.Scan(&f.ID, &f.UserID, &f.Name, &f.Icon, &f.SortOrder, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *ConversationStore) DeleteChatFolder(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM chat.chat_folders WHERE id = $1`, id)
	return err
}

func (s *ConversationStore) AddConversationToFolder(ctx context.Context, folderID, conversationID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.chat_folder_conversations (folder_id, conversation_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, folderID, conversationID)
	return err
}

func (s *ConversationStore) RemoveConversationFromFolder(ctx context.Context, folderID, conversationID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM chat.chat_folder_conversations
		WHERE folder_id = $1 AND conversation_id = $2
	`, folderID, conversationID)
	return err
}

func (s *ConversationStore) GetFolderConversations(ctx context.Context, folderID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT conversation_id FROM chat.chat_folder_conversations
		WHERE folder_id = $1
		ORDER BY added_at ASC
	`, folderID)
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

// --- Pins ---

func (s *ConversationStore) PinMessage(ctx context.Context, convID, messageID, pinnedBy uuid.UUID) (*ConversationPin, error) {
	// Enforce max 3 pins per conversation
	var count int
	err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM chat.conversation_pins WHERE conversation_id = $1`, convID).Scan(&count)
	if err != nil {
		return nil, err
	}
	if count >= 3 {
		return nil, errors.New("maximum 3 pinned messages per conversation")
	}

	var pin ConversationPin
	err = s.db.QueryRow(ctx, `
		INSERT INTO chat.conversation_pins (conversation_id, message_id, pinned_by)
		VALUES ($1, $2, $3)
		RETURNING id, conversation_id, message_id, pinned_by, pinned_at
	`, convID, messageID, pinnedBy).Scan(
		&pin.ID, &pin.ConversationID, &pin.MessageID, &pin.PinnedBy, &pin.PinnedAt,
	)
	if err != nil {
		return nil, err
	}
	return &pin, nil
}

func (s *ConversationStore) UnpinMessage(ctx context.Context, pinID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM chat.conversation_pins WHERE id = $1`, pinID)
	return err
}

func (s *ConversationStore) GetConversationPins(ctx context.Context, convID uuid.UUID) ([]ConversationPin, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, conversation_id, message_id, pinned_by, pinned_at
		FROM chat.conversation_pins
		WHERE conversation_id = $1
		ORDER BY pinned_at DESC
	`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ConversationPin
	for rows.Next() {
		var p ConversationPin
		if err := rows.Scan(&p.ID, &p.ConversationID, &p.MessageID, &p.PinnedBy, &p.PinnedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- Message Requests ---

func (s *ConversationStore) GetMessageRequestSettings(ctx context.Context, userID uuid.UUID) (*MessageRequestSettings, error) {
	var ms MessageRequestSettings
	err := s.db.QueryRow(ctx, `
		SELECT user_id, allow_from, auto_filter_likely_spam, updated_at
		FROM chat.message_request_settings
		WHERE user_id = $1
	`, userID).Scan(&ms.UserID, &ms.AllowFrom, &ms.AutoFilterLikelySpam, &ms.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &MessageRequestSettings{
				UserID:               userID,
				AllowFrom:            "everyone",
				AutoFilterLikelySpam: true,
				UpdatedAt:            time.Now(),
			}, nil
		}
		return nil, err
	}
	return &ms, nil
}

func (s *ConversationStore) UpsertMessageRequestSettings(ctx context.Context, ms *MessageRequestSettings) error {
	ms.UpdatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.message_request_settings (user_id, allow_from, auto_filter_likely_spam, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO UPDATE SET
			allow_from            = EXCLUDED.allow_from,
			auto_filter_likely_spam = EXCLUDED.auto_filter_likely_spam,
			updated_at            = EXCLUDED.updated_at
	`, ms.UserID, ms.AllowFrom, ms.AutoFilterLikelySpam, ms.UpdatedAt)
	return err
}

func (s *ConversationStore) AcceptMessageRequest(ctx context.Context, convID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversations
		SET is_request = FALSE, request_accepted_at = NOW()
		WHERE id = $1
	`, convID)
	return err
}

func (s *ConversationStore) DeclineMessageRequest(ctx context.Context, convID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.conversations
		SET request_declined_at = NOW()
		WHERE id = $1
	`, convID)
	return err
}

func (s *ConversationStore) ListMessageRequests(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Conversation, error) {
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.type, c.title, c.created_by, c.created_at, c.updated_at
		FROM chat.conversations c
		JOIN chat.conversation_members m ON m.conversation_id = c.id
		WHERE m.user_id = $1 AND c.is_request = TRUE AND c.request_declined_at IS NULL
		ORDER BY c.created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
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

// --- Message Request Envelopes (spec §8.6) ---

// MessageRequest is the request envelope: who reached out, the first-message
// preview, and the request lifecycle status.
type MessageRequest struct {
	ID             int64      `json:"id"`
	ConversationID uuid.UUID  `json:"conversation_id"`
	SenderID       uuid.UUID  `json:"sender_id"`
	ReceiverID     uuid.UUID  `json:"receiver_id"`
	Preview        string     `json:"preview"`
	Status         string     `json:"status"`
	RiskScore      int        `json:"risk_score"`
	CreatedAt      time.Time  `json:"created_at"`
	RespondedAt    *time.Time `json:"responded_at,omitempty"`
	ExpiresAt      time.Time  `json:"expires_at"`
}

// CreateMessageRequest inserts the envelope for a new request conversation.
// Idempotent on conversation_id so a retried conversation create is safe.
func (s *ConversationStore) CreateMessageRequest(ctx context.Context, convID, senderID, receiverID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.message_requests (conversation_id, sender_id, receiver_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (conversation_id) DO NOTHING
	`, convID, senderID, receiverID)
	return err
}

func (s *ConversationStore) GetMessageRequestByConversation(ctx context.Context, convID uuid.UUID) (*MessageRequest, error) {
	var m MessageRequest
	err := s.db.QueryRow(ctx, `
		SELECT id, conversation_id, sender_id, receiver_id, preview, status, risk_score, created_at, responded_at, expires_at
		FROM chat.message_requests WHERE conversation_id = $1
	`, convID).Scan(&m.ID, &m.ConversationID, &m.SenderID, &m.ReceiverID, &m.Preview,
		&m.Status, &m.RiskScore, &m.CreatedAt, &m.RespondedAt, &m.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// SetMessageRequestPreview stores the sender's first-message text as the
// preview shown in the recipient's Requests folder.
func (s *ConversationStore) SetMessageRequestPreview(ctx context.Context, convID uuid.UUID, preview string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.message_requests SET preview = $2 WHERE conversation_id = $1
	`, convID, preview)
	return err
}

// UpdateMessageRequestStatus moves the request to a terminal status.
func (s *ConversationStore) UpdateMessageRequestStatus(ctx context.Context, convID uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.message_requests SET status = $2, responded_at = NOW()
		WHERE conversation_id = $1 AND status = 'pending'
	`, convID, status)
	return err
}

// --- Starred Messages ---

func (s *ConversationStore) StarMessage(ctx context.Context, userID, convID, messageID uuid.UUID, preview *string) (*StarredMessage, error) {
	var sm StarredMessage
	err := s.db.QueryRow(ctx, `
		INSERT INTO chat.starred_messages (user_id, conversation_id, message_id, message_preview)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, message_id) DO UPDATE SET message_preview = EXCLUDED.message_preview
		RETURNING id, user_id, conversation_id, message_id, message_preview, starred_at
	`, userID, convID, messageID, preview).Scan(
		&sm.ID, &sm.UserID, &sm.ConversationID, &sm.MessageID, &sm.MessagePreview, &sm.StarredAt,
	)
	if err != nil {
		return nil, err
	}
	return &sm, nil
}

func (s *ConversationStore) UnstarMessage(ctx context.Context, userID, messageID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM chat.starred_messages
		WHERE user_id = $1 AND message_id = $2
	`, userID, messageID)
	return err
}

func (s *ConversationStore) GetStarredMessages(ctx context.Context, userID uuid.UUID, limit, offset int) ([]StarredMessage, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, conversation_id, message_id, message_preview, starred_at
		FROM chat.starred_messages
		WHERE user_id = $1
		ORDER BY starred_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []StarredMessage
	for rows.Next() {
		var sm StarredMessage
		if err := rows.Scan(&sm.ID, &sm.UserID, &sm.ConversationID, &sm.MessageID, &sm.MessagePreview, &sm.StarredAt); err != nil {
			return nil, err
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}

// --- Chat Backups ---

func (s *ConversationStore) CreateChatBackup(ctx context.Context, b *ChatBackup) (*ChatBackup, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO chat.chat_backups (user_id, status, key_hint)
		VALUES ($1, 'in_progress', $2)
		RETURNING id, user_id, status, size_bytes, message_count, encrypted_blob_url, key_hint, backup_version, created_at, completed_at
	`, b.UserID, b.KeyHint).Scan(
		&b.ID, &b.UserID, &b.Status, &b.SizeBytes, &b.MessageCount,
		&b.EncryptedBlobURL, &b.KeyHint, &b.BackupVersion, &b.CreatedAt, &b.CompletedAt,
	)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (s *ConversationStore) UpdateChatBackup(ctx context.Context, id uuid.UUID, status string, sizeBytes, messageCount *int64, blobURL *string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.chat_backups
		SET status = $2,
		    size_bytes = COALESCE($3, size_bytes),
		    message_count = COALESCE($4, message_count),
		    encrypted_blob_url = COALESCE($5, encrypted_blob_url),
		    completed_at = CASE WHEN $2 IN ('completed','failed') THEN NOW() ELSE completed_at END
		WHERE id = $1
	`, id, status, sizeBytes, messageCount, blobURL)
	return err
}

func (s *ConversationStore) GetLatestChatBackup(ctx context.Context, userID uuid.UUID) (*ChatBackup, error) {
	var b ChatBackup
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, status, size_bytes, message_count, encrypted_blob_url, key_hint, backup_version, created_at, completed_at
		FROM chat.chat_backups
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, userID).Scan(
		&b.ID, &b.UserID, &b.Status, &b.SizeBytes, &b.MessageCount,
		&b.EncryptedBlobURL, &b.KeyHint, &b.BackupVersion, &b.CreatedAt, &b.CompletedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

// --- Scheduled Messages ---

func (s *ConversationStore) CreateScheduledMessage(ctx context.Context, m *ScheduledMessage) (*ScheduledMessage, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO chat.scheduled_messages (conversation_id, sender_id, type, content, media_id, send_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, conversation_id, sender_id, type, content, media_id, send_at, status, created_at
	`, m.ConversationID, m.SenderID, m.Type, m.Content, m.MediaID, m.SendAt).Scan(
		&m.ID, &m.ConversationID, &m.SenderID, &m.Type, &m.Content, &m.MediaID, &m.SendAt, &m.Status, &m.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (s *ConversationStore) CancelScheduledMessage(ctx context.Context, id, senderID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.scheduled_messages
		SET status = 'cancelled'
		WHERE id = $1 AND sender_id = $2 AND status = 'pending'
	`, id, senderID)
	return err
}

func (s *ConversationStore) GetPendingScheduledMessages(ctx context.Context, before time.Time, limit int) ([]ScheduledMessage, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, conversation_id, sender_id, type, content, media_id, send_at, status, created_at
		FROM chat.scheduled_messages
		WHERE status = 'pending' AND send_at <= $1
		ORDER BY send_at ASC
		LIMIT $2
	`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ScheduledMessage
	for rows.Next() {
		var m ScheduledMessage
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.SenderID, &m.Type, &m.Content, &m.MediaID, &m.SendAt, &m.Status, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *ConversationStore) MarkScheduledMessageSent(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.scheduled_messages SET status = 'sent' WHERE id = $1
	`, id)
	return err
}

func (s *ConversationStore) ListScheduledMessages(ctx context.Context, conversationID, senderID uuid.UUID) ([]ScheduledMessage, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, conversation_id, sender_id, type, content, media_id, send_at, status, created_at
		FROM chat.scheduled_messages
		WHERE conversation_id = $1 AND sender_id = $2 AND status = 'pending'
		ORDER BY send_at ASC
	`, conversationID, senderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ScheduledMessage
	for rows.Next() {
		var m ScheduledMessage
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.SenderID, &m.Type, &m.Content, &m.MediaID, &m.SendAt, &m.Status, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// --- Message Translations ---

func (s *ConversationStore) UpsertMessageTranslation(ctx context.Context, t *MessageTranslation) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO chat.message_translations (message_id, conversation_id, target_lang, translated_text, source_lang)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (message_id, target_lang) DO UPDATE SET
			translated_text = EXCLUDED.translated_text,
			source_lang     = EXCLUDED.source_lang,
			translated_at   = NOW()
	`, t.MessageID, t.ConversationID, t.TargetLang, t.TranslatedText, t.SourceLang)
	return err
}

func (s *ConversationStore) GetMessageTranslation(ctx context.Context, messageID uuid.UUID, targetLang string) (*MessageTranslation, error) {
	var t MessageTranslation
	err := s.db.QueryRow(ctx, `
		SELECT message_id, conversation_id, target_lang, translated_text, source_lang, translated_at
		FROM chat.message_translations
		WHERE message_id = $1 AND target_lang = $2
	`, messageID, targetLang).Scan(
		&t.MessageID, &t.ConversationID, &t.TargetLang, &t.TranslatedText, &t.SourceLang, &t.TranslatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

// --- Message Threads ---

func (s *ConversationStore) GetOrCreateThread(ctx context.Context, convID, parentMessageID uuid.UUID) (*MessageThread, error) {
	var t MessageThread
	err := s.db.QueryRow(ctx, `
		INSERT INTO chat.message_threads (conversation_id, parent_message_id)
		VALUES ($1, $2)
		ON CONFLICT (conversation_id, parent_message_id) DO UPDATE SET last_reply_at = NOW()
		RETURNING id, conversation_id, parent_message_id, reply_count, last_reply_at, last_reply_preview, created_at
	`, convID, parentMessageID).Scan(
		&t.ID, &t.ConversationID, &t.ParentMessageID, &t.ReplyCount, &t.LastReplyAt, &t.LastReplyPreview, &t.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *ConversationStore) IncrementThreadReplyCount(ctx context.Context, threadID uuid.UUID, lastReplyPreview string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE chat.message_threads
		SET reply_count = reply_count + 1,
		    last_reply_at = NOW(),
		    last_reply_preview = $2
		WHERE id = $1
	`, threadID, lastReplyPreview)
	return err
}

func (s *ConversationStore) GetThread(ctx context.Context, convID, parentMessageID uuid.UUID) (*MessageThread, error) {
	var t MessageThread
	err := s.db.QueryRow(ctx, `
		SELECT id, conversation_id, parent_message_id, reply_count, last_reply_at, last_reply_preview, created_at
		FROM chat.message_threads
		WHERE conversation_id = $1 AND parent_message_id = $2
	`, convID, parentMessageID).Scan(
		&t.ID, &t.ConversationID, &t.ParentMessageID, &t.ReplyCount, &t.LastReplyAt, &t.LastReplyPreview, &t.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (s *ConversationStore) ListConversationThreads(ctx context.Context, convID uuid.UUID) ([]MessageThread, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, conversation_id, parent_message_id, reply_count, last_reply_at, last_reply_preview, created_at
		FROM chat.message_threads
		WHERE conversation_id = $1
		ORDER BY last_reply_at DESC NULLS LAST, created_at DESC
	`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MessageThread
	for rows.Next() {
		var t MessageThread
		if err := rows.Scan(&t.ID, &t.ConversationID, &t.ParentMessageID, &t.ReplyCount, &t.LastReplyAt, &t.LastReplyPreview, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
