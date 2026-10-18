package events

// Chat message indexing (chat parity programme, CONTRACT §6, lane B5).
//
// Consumes chat-service's outbox topic (chat.events.v1, envelope
// {event_id, event_type, occurred_at, trace_id, actor_user_id, payload} —
// the same shape as shared/events.EventEnvelope) and keeps
// chat_messages_v1 in step:
//
//	MessageCreated          type "text" with non-empty `text` → upsert
//	                        (anything else — media, post, voice, a create
//	                        from a producer that does not carry text yet,
//	                        a dating conversation — is skipped)
//	MessageEdited           non-empty `text` → replace text if newer
//	MessageDeleted          tombstone (unsent text never comes back)
//	MemberRemoved, MemberLeft
//	                        drop the user from member_ids on that
//	                        conversation's messages
//	MessageRequestAccepted  is_request=false on that conversation
//
// MemberAdded is deliberately NOT applied: a newly added member gets the
// messages sent after they joined (their id is on those events), and
// whether they may see older history is message-service's call. Search
// therefore under-returns for them rather than over-returning.
//
// Search is never the authority: message-service re-checks current
// membership and the clear / hide cut-offs on every hit, so the races
// left here (a create replayed after a removal, for example — guarded in
// the store anyway) cannot leak a message to a non-member.
//
// chat-service is a separate Go module; the payload fields read here are
// declared locally (same pattern as the community/channel payloads in
// consumer.go). Everything is read defensively: a missing field means
// "skip", never "index something empty".

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/search-service/internal/store/search"
)

// Chat-service event types consumed here (chat-service/shared/events).
const (
	ChatMessageCreated         = "MessageCreated"
	ChatMessageEdited          = "MessageEdited"
	ChatMessageDeleted         = "MessageDeleted"
	ChatMemberRemoved          = "MemberRemoved"
	ChatMemberLeft             = "MemberLeft"
	ChatMessageRequestAccepted = "MessageRequestAccepted"
)

// ChatIndex is the subset of *search.Store the chat handlers need.
type ChatIndex interface {
	UpsertChatMessage(ctx context.Context, d search.ChatMessageDoc) error
	EditChatMessage(ctx context.Context, messageID, conversationID, text string, editedAt time.Time) error
	TombstoneChatMessage(ctx context.Context, messageID, conversationID string) error
	RemoveChatMember(ctx context.Context, conversationID, userID string) error
	ClearChatRequest(ctx context.Context, conversationID string) error
}

// WithChatIndex wires chat message indexing onto a consumer. Wire it on the
// chat-topic consumer AND on the consumer the DLQ replayer re-runs through,
// or a dead-lettered unsend would be dropped on replay.
func (c *Consumer) WithChatIndex(ix ChatIndex) *Consumer {
	c.chat = ix
	return c
}

func isChatEvent(t string) bool {
	switch t {
	case ChatMessageCreated, ChatMessageEdited, ChatMessageDeleted,
		ChatMemberRemoved, ChatMemberLeft, ChatMessageRequestAccepted:
		return true
	}
	return false
}

type chatMessageCreatedPayload struct {
	MessageID      string   `json:"message_id"`
	ConversationID string   `json:"conversation_id"`
	SenderID       string   `json:"sender_id"`
	Type           string   `json:"type"`
	Text           *string  `json:"text"`
	IsRequest      bool     `json:"is_request"`
	RecipientIDs   []string `json:"recipient_ids"`
	// SourceApp is optional; a dating conversation is never in chat search.
	SourceApp string    `json:"source_app"`
	CreatedAt time.Time `json:"created_at"`
}

type chatMessageEditedPayload struct {
	MessageID      string    `json:"message_id"`
	ConversationID string    `json:"conversation_id"`
	Text           *string   `json:"text"`
	EditedAt       time.Time `json:"edited_at"`
}

type chatMessageDeletedPayload struct {
	MessageID      string `json:"message_id"`
	ConversationID string `json:"conversation_id"`
}

type chatMemberPayload struct {
	ConversationID string `json:"conversation_id"`
	UserID         string `json:"user_id"`
}

type chatRequestPayload struct {
	ConversationID string `json:"conversation_id"`
}

// handleChatEvent applies one chat event. Errors retry and dead-letter like
// every other write in this consumer; malformed payloads are skipped.
func (c *Consumer) handleChatEvent(ctx context.Context, eventType string, raw json.RawMessage) error {
	switch eventType {
	case ChatMessageCreated:
		var p chatMessageCreatedPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if !strings.EqualFold(p.Type, "text") || p.Text == nil || strings.TrimSpace(*p.Text) == "" {
			return nil // text messages only; no text on the event → nothing to index
		}
		if strings.EqualFold(p.SourceApp, "dating") {
			return nil
		}
		if p.MessageID == "" || p.ConversationID == "" || p.SenderID == "" {
			slog.WarnContext(ctx, "search: chat MessageCreated missing ids; skipped")
			return nil
		}
		members := append([]string{p.SenderID}, p.RecipientIDs...)
		return c.chat.UpsertChatMessage(ctx, search.ChatMessageDoc{
			MessageID:      p.MessageID,
			ConversationID: p.ConversationID,
			SenderID:       p.SenderID,
			MemberIDs:      members,
			Text:           *p.Text,
			CreatedAt:      p.CreatedAt,
			IsRequest:      p.IsRequest,
		})

	case ChatMessageEdited:
		var p chatMessageEditedPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if p.MessageID == "" || p.Text == nil || strings.TrimSpace(*p.Text) == "" {
			return nil
		}
		return c.chat.EditChatMessage(ctx, p.MessageID, p.ConversationID, *p.Text, p.EditedAt)

	case ChatMessageDeleted:
		var p chatMessageDeletedPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if p.MessageID == "" {
			return nil
		}
		return c.chat.TombstoneChatMessage(ctx, p.MessageID, p.ConversationID)

	case ChatMemberRemoved, ChatMemberLeft:
		var p chatMemberPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if p.ConversationID == "" || p.UserID == "" {
			return nil
		}
		return c.chat.RemoveChatMember(ctx, p.ConversationID, p.UserID)

	case ChatMessageRequestAccepted:
		var p chatRequestPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if p.ConversationID == "" {
			return nil
		}
		return c.chat.ClearChatRequest(ctx, p.ConversationID)
	}
	return nil
}
