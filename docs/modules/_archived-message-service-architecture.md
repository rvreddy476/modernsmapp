# Module: _archived-message-service-architecture

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /conversations/:conversationId
DELETE /conversations/:conversationId/members/:userId
DELETE /conversations/:conversationId/messages/:messageId
DELETE /conversations/:id/pin
DELETE /messages/:messageId/reactions
GET /conversations
GET /conversations/:conversationId/messages
GET /conversations/:id/pin
GET /keys/:userId
GET /messages/:messageId/reactions
GET /messages/:receiverId
PATCH /conversations/:conversationId
PATCH /conversations/:conversationId/messages/:messageId
POST /conversations/:conversationId/members
POST /conversations/:conversationId/messages
POST /conversations/:conversationId/read
POST /conversations/:conversationId/typing
POST /conversations/direct
POST /conversations/group
POST /conversations/:id/pin/:msgId
POST /keys
POST /messages/:messageId/reactions
POST /messages/:receiverId
PUT /conversations/:conversationId/messages/:messageId/reactions
GROUP /v1/chat
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS chat.conversations (
    id UUID PRIMARY KEY,
    type TEXT NOT NULL,                 -- 'direct' or 'group'
    name TEXT,                          -- group name; NULL for DMs
    icon_url TEXT,                      -- group icon URL
    created_by UUID,                    -- creator user_id
    last_message_at TIMESTAMPTZ,       -- for sort ordering
    last_message_preview TEXT,          -- snippet for list view
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS chat.conversation_members (
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    role TEXT NOT NULL DEFAULT 'member', -- 'admin', 'moderator', 'member'
    nickname TEXT,                       -- per-conversation nickname
    is_muted BOOLEAN NOT NULL DEFAULT false,
    last_read_message_id TEXT,          -- for unread count
    last_read_at TIMESTAMPTZ,
    joined_at TIMESTAMPTZ NOT NULL,
    left_at TIMESTAMPTZ,               -- NULL = still in
    PRIMARY KEY (conversation_id, user_id)
);

CREATE TABLE IF NOT EXISTS chat.direct_conversation_keys (
    user_a UUID NOT NULL,
    user_b UUID NOT NULL,
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id),
    PRIMARY KEY (user_a, user_b),
    CHECK (user_a < user_b)
);

CREATE TABLE IF NOT EXISTS chat.message_reads (
    conversation_id UUID NOT NULL,
    user_id UUID NOT NULL,
    message_id TEXT NOT NULL,
    read_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (conversation_id, user_id, message_id)
);

CREATE TABLE IF NOT EXISTS chat.message_reactions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id    TEXT NOT NULL,
    user_id       UUID NOT NULL,
    reaction_type TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(message_id, user_id)
);

CREATE TABLE IF NOT EXISTS chat.key_bundles (
    user_id           UUID PRIMARY KEY,
    identity_key      BYTEA NOT NULL,
    signed_pre_key    BYTEA NOT NULL,
    pre_key_signature BYTEA,
    one_time_pre_keys BYTEA[] DEFAULT '{}',
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

```

## API types (request/response Go structs with JSON tags)
```go
type SendMessageRequest struct {
	Text            string `json:"text"`
	MessageType     string `json:"message_type"`
	MediaID         string `json:"media_id"`
	ReplyToID       string `json:"reply_to_id"`
	ForwardedFromID string `json:"forwarded_from_id"`
}

type EditMessageRequest struct {
	Text      string `json:"text" binding:"required,min=1,max=2000"`
	Timestamp string `json:"timestamp" binding:"required"`
}

type DeleteMessageRequest struct {
	Timestamp string `json:"timestamp" binding:"required"`
}

type ToggleReactionRequest struct {
	Emoji string `json:"emoji" binding:"required,min=1,max=8"`
}

type MarkReadRequest struct {
	MessageID string `json:"message_id" binding:"required"`
}

type UpdateConversationRequest struct {
	Name    *string `json:"name"`
	IconURL *string `json:"icon_url"`
}

type AddMemberRequest struct {
	UserID string `json:"user_id" binding:"required"`
}

type CreateDirectConversationRequest struct {
	OtherUserID string `json:"other_user_id" binding:"required"`
}

type CreateGroupConversationRequest struct {
	Title     string   `json:"title" binding:"required"`
	MemberIDs []string `json:"member_ids" binding:"required"`
}

type cursorPayload struct {
	Ts time.Time `json:"ts"`
	ID string    `json:"id"`
}
```
