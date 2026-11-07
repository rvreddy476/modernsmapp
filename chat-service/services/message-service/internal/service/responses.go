// Wire shapes the Service returns to the HTTP layer: conversations with
// member profiles, messages with reply quotes and reaction summaries, the
// reaction toggle result and the conversation-list cursor.

package service

import (
	"time"

	"github.com/google/uuid"
)

// --- Response Types ---

type MemberWithProfile struct {
	UserID        uuid.UUID  `json:"user_id"`
	Role          string     `json:"role"`
	JoinedAt      time.Time  `json:"joined_at"`
	DisplayName   string     `json:"display_name,omitempty"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id,omitempty"`
	// LastReadAt is this member's durable read watermark. A sender uses the
	// OTHER member's value to mark its own messages seen, which is what
	// makes a read receipt survive a reload — the live frame only reaches a
	// client already watching the conversation.
	//
	// Subject to the reader's own privacy setting: suppressed when they have
	// read receipts off, so this never discloses more than the live frame.
	LastReadAt *time.Time `json:"last_read_at,omitempty"`
}

type ConversationResponse struct {
	ID        uuid.UUID           `json:"id"`
	Type      string              `json:"type"`
	Title     *string             `json:"title,omitempty"`
	CreatedBy *uuid.UUID          `json:"created_by,omitempty"`
	IsRequest bool                `json:"is_request"`
	Members   []MemberWithProfile `json:"members"`
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`

	// Production chat pass: inbox metadata. HasUnread compares the viewer's
	// durable read cursor with the denormalized last-message timestamp;
	// preview text rides along so the inbox renders without a per-row
	// message fetch.
	AvatarMediaID *uuid.UUID `json:"avatar_media_id,omitempty"`
	// AvatarURL is the group avatar resolved for the VIEWER through
	// media-service delivery (chat-app pass); empty when unset or denied.
	AvatarURL string `json:"avatar_url,omitempty"`
	// Description is the group "about" text (<= 300 runes).
	Description        string     `json:"description,omitempty"`
	LastMessageAt      *time.Time `json:"last_message_at,omitempty"`
	LastMessagePreview string     `json:"last_message_preview,omitempty"`
	LastMessageSender  *uuid.UUID `json:"last_message_sender,omitempty"`
	HasUnread          bool       `json:"has_unread"`

	// Android chat completion pass: the VIEWER's per-conversation settings,
	// joined in one page query — additive, omitted when false, so every
	// earlier client and capture stays byte-compatible.
	IsPinned bool `json:"is_pinned,omitempty"`
	IsMuted  bool `json:"is_muted,omitempty"`

	// A Pulse match's chat (dating mechanic M13): source_app "dating" and
	// its match id, so a client can tell from any entry point. Omitted for
	// every other conversation, so earlier captures stay byte-compatible.
	SourceApp string     `json:"source_app,omitempty"`
	MatchID   *uuid.UUID `json:"match_id,omitempty"`

	// Chat parity §1 / §1.5 (additive). unread_count is server-maintained;
	// streak and peer are null outside a DM; last_message is null when the
	// conversation has no message the viewer can still see.
	UnreadCount int              `json:"unread_count"`
	MuteCalls   bool             `json:"mute_calls"`
	AlertTone   string           `json:"alert_tone,omitempty"`
	Streak      *StreakInfo      `json:"streak"`
	Peer        *PeerInfo        `json:"peer"`
	LastMessage *LastMessageInfo `json:"last_message"`

	// Fix pass 2 (additive). marked_unread is the viewer's own "mark as
	// unread" (PUT .../unread); while set, has_unread is true and
	// unread_count is at least 1. sender is who sent a message request —
	// present only on request conversations.
	MarkedUnread bool           `json:"marked_unread"`
	Sender       *RequestSender `json:"sender,omitempty"`

	// Disappearing messages: the conversation-wide timer in milliseconds,
	// always present; 0 = off, else 86400000 (24 h), 604800000 (7 d) or
	// 7776000000 (90 d).
	DisappearAfterMs int64 `json:"disappear_after_ms"`

	// MyJoin is how the VIEWER came into this group, present only when their
	// history starts at that join (founder, 22 Oct 2026): the client shows
	// "X added you · 22 Oct" above the oldest message. Omitted for the
	// creator, for members with full history, and outside a single-
	// conversation read (see my_join.go).
	MyJoin *MyJoin `json:"my_join,omitempty"`

	// Receipts is the viewer's tick watermarks (founder, 22 Oct 2026; see
	// receipt_ticks.go). Single-conversation reads only.
	Receipts *ReceiptWatermarks `json:"receipts,omitempty"`

	// Locked: the VIEWER has hidden this conversation (hidden chats, 24 Oct
	// 2026). Set on hidden-list rows and on a locked conversation returned
	// by another entry point (open-a-DM); omitted otherwise, so every other
	// row stays byte-compatible.
	Locked bool `json:"locked,omitempty"`
}

type ReactionSummary struct {
	Emoji   string   `json:"emoji"`
	UserIDs []string `json:"user_ids"`
}

type MessageResponse struct {
	ConversationID    uuid.UUID         `json:"conversation_id"`
	Bucket            string            `json:"bucket"`
	Ts                time.Time         `json:"ts"`
	MsgID             uuid.UUID         `json:"msg_id"`
	SenderID          uuid.UUID         `json:"sender_id"`
	SenderDisplayName string            `json:"sender_display_name,omitempty"`
	Type              string            `json:"type"`
	Text              string            `json:"text,omitempty"`
	MediaID           *uuid.UUID        `json:"media_id,omitempty"`
	PostID            *uuid.UUID        `json:"post_id,omitempty"` // "post" card only; see MessageTypePost
	ReplyToID         *uuid.UUID        `json:"reply_to_id,omitempty"`
	ReplyToPreview    string            `json:"reply_to_preview,omitempty"`
	ReplyToSenderID   *uuid.UUID        `json:"reply_to_sender_id,omitempty"`
	Reactions         []ReactionSummary `json:"reactions,omitempty"`
	// Without these two on the wire the "(edited)" marker survived only in
	// the live event: a reload showed the new text with no sign it had been
	// changed.
	IsEdited  bool       `json:"is_edited,omitempty"`
	EditedAt  *time.Time `json:"edited_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`

	// Chat parity §2.2 (additive). forwarded and reply_kind are always
	// present; the card fields only on their type; media on attachments.
	Forwarded     bool         `json:"forwarded"`
	Voice         *VoiceInfo   `json:"voice,omitempty"`
	Sticker       *StickerInfo `json:"sticker,omitempty"`
	GIF           *GIFInfo     `json:"gif,omitempty"`
	ProfileUserID *uuid.UUID   `json:"profile_user_id,omitempty"`
	SoundID       *uuid.UUID   `json:"sound_id,omitempty"`
	StreamID      *uuid.UUID   `json:"stream_id,omitempty"`
	ReplyKind     *string      `json:"reply_kind"`
	Media         *MediaInfo   `json:"media,omitempty"`

	// Disappearing messages (additive): when this message disappears for
	// everyone (RFC 3339). Omitted when the message does not disappear.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	// Location is a shared place (location_messages.go), on its type only.
	Location *LocationInfo `json:"location,omitempty"`

	// Status is the sender's tick — sent | delivered | read — on the
	// viewer's OWN messages in history (receipt_ticks.go); omitted on
	// everyone else's.
	Status *string `json:"status,omitempty"`
}

type ToggleReactionResponse struct {
	Added     bool      `json:"added"`
	Emoji     string    `json:"emoji"`
	MessageID uuid.UUID `json:"message_id"`
}

type ConversationCursor struct {
	UpdatedAt time.Time `json:"updated_at"`
	ID        uuid.UUID `json:"id"`
}
