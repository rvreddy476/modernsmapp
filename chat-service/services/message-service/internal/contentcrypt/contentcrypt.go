// Package contentcrypt names every chat field that is encrypted at rest and
// the row each ciphertext is bound to. The stores (internal/store/postgres,
// internal/store/scylla) seal on write and open on read through these
// definitions, and the backfill (cmd/fieldcrypt-backfill) re-seals legacy
// rows through the SAME definitions, so a value written by either opens in
// the other.
//
// Founder decision, 22 Oct 2026: no end-to-end encryption for launch;
// message text and every other content-bearing field is encrypted in the chat
// databases instead, so a leaked database or backup shows nothing readable.
// The service decrypts in memory, so reporting, search indexing, push
// previews and moderation keep working.
//
// A scope is the column; the binding is the row identity, so a ciphertext
// copied into another row (another conversation, message, user or request)
// fails to open instead of being decrypted on that row's behalf.
package contentcrypt

import (
	"context"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/atpost/chat-shared/fieldcrypt"
	"github.com/google/uuid"
)

// Scopes, one per encrypted column. Never rename one: values sealed under a
// scope only open under the same scope.
const (
	// Scylla chatservice.messages.
	ScopeMessageText         fieldcrypt.Scope = "chat.message.text"
	ScopeMessageExtra        fieldcrypt.Scope = "chat.message.extra"
	ScopeMessageReplyPreview fieldcrypt.Scope = "chat.message.reply_preview"
	// Scylla chatservice.conversations_by_user.last_text.
	ScopeInboxLastText fieldcrypt.Scope = "chat.inbox.last_text"

	// Postgres chat.conversations.last_message_preview.
	ScopeConversationPreview fieldcrypt.Scope = "chat.conversation.last_preview"
	// Postgres chat.message_delivery_intents.
	ScopeIntentText         fieldcrypt.Scope = "chat.intent.text"
	ScopeIntentReplyPreview fieldcrypt.Scope = "chat.intent.reply_preview"
	ScopeIntentExtra        fieldcrypt.Scope = "chat.intent.extra"
	// Postgres chat.idempotency_keys.response (the cached send response).
	ScopeIdempotencyResponse fieldcrypt.Scope = "chat.idempotency.response"
	// Postgres chat.outbox_events.payload.
	ScopeOutboxPayload fieldcrypt.Scope = "chat.outbox.payload"
	// Postgres chat.message_requests.preview.
	ScopeRequestPreview fieldcrypt.Scope = "chat.request.preview"
	// Postgres chat.starred_messages.message_preview.
	ScopeStarredPreview fieldcrypt.Scope = "chat.starred.preview"
	// Postgres chat.scheduled_messages.content.
	ScopeScheduledContent fieldcrypt.Scope = "chat.scheduled.content"
	// Postgres chat.notes.text.
	ScopeNoteText fieldcrypt.Scope = "chat.note.text"
	// Postgres chat.message_translations.translated_text.
	ScopeTranslationText fieldcrypt.Scope = "chat.translation.text"
	// Postgres chat.message_threads.last_reply_preview.
	ScopeThreadPreview fieldcrypt.Scope = "chat.thread.last_reply_preview"
)

// AllScopes lists every scope (readiness probes and tests).
var AllScopes = []fieldcrypt.Scope{
	ScopeMessageText, ScopeMessageExtra, ScopeMessageReplyPreview, ScopeInboxLastText,
	ScopeConversationPreview, ScopeIntentText, ScopeIntentReplyPreview, ScopeIntentExtra,
	ScopeIdempotencyResponse, ScopeOutboxPayload, ScopeRequestPreview, ScopeStarredPreview,
	ScopeScheduledContent, ScopeNoteText, ScopeTranslationText, ScopeThreadPreview,
}

// MessageRow binds a message's text, extra and quote snapshot (Scylla) and
// the delivery intent that carries them (message_id is unique).
func MessageRow(conversationID, messageID uuid.UUID) []byte {
	return fieldcrypt.Bind(conversationID.String(), messageID.String())
}

// InboxRow binds one conversations_by_user row: the member, the conversation
// and the message timestamp at Scylla's millisecond precision (the value read
// back is truncated to milliseconds, so the binding must be too).
func InboxRow(userID, conversationID uuid.UUID, ts time.Time) []byte {
	return fieldcrypt.Bind(userID.String(), conversationID.String(), strconv.FormatInt(ts.UnixMilli(), 10))
}

// ConversationRow binds a value kept once per conversation (inbox preview,
// message request preview).
func ConversationRow(conversationID uuid.UUID) []byte {
	return fieldcrypt.Bind(conversationID.String())
}

// IdempotencyRow binds a cached response to its idempotency key.
func IdempotencyRow(key string) []byte { return fieldcrypt.Bind(key) }

// OutboxRow binds an outbox payload to its event type and dedupe key (every
// row written since encryption carries a unique dedupe key).
func OutboxRow(eventType, dedupeKey string) []byte {
	return fieldcrypt.Bind(eventType, dedupeKey)
}

// StarredRow binds a star (one per user and message).
func StarredRow(userID, messageID uuid.UUID) []byte {
	return fieldcrypt.Bind(userID.String(), messageID.String())
}

// IDRow binds a row keyed by its own uuid (scheduled message, thread).
func IDRow(id uuid.UUID) []byte { return fieldcrypt.Bind(id.String()) }

// NoteRow binds a note to its id and author.
func NoteRow(noteID, userID uuid.UUID) []byte {
	return fieldcrypt.Bind(noteID.String(), userID.String())
}

// TranslationRow binds a translation (one per message and target language).
func TranslationRow(messageID uuid.UUID, targetLang string) []byte {
	return fieldcrypt.Bind(messageID.String(), targetLang)
}

// Field is one stored column value for Reseal. Value nil is SQL NULL.
type Field struct {
	Scope fieldcrypt.Scope
	Row   []byte
	Value *string
	// JSON marks a JSONB column (sealed as a JSON string).
	JSON bool
}

// Reseal returns the value each field should be stored as: a legacy
// (plaintext) value sealed, anything else unchanged. NULL, "" and a JSON
// null carry no content and stay as they are. changed reports whether any
// field needs writing. Running it on its own output changes nothing, which
// is what makes the backfill idempotent.
func Reseal(ctx context.Context, c *fieldcrypt.Cipher, fields []Field) (out []*string, changed bool, err error) {
	if !c.Enabled() {
		return nil, false, fieldcrypt.ErrNoKeyRing
	}
	out = make([]*string, len(fields))
	for i, f := range fields {
		out[i] = f.Value
		if f.Value == nil || *f.Value == "" {
			continue
		}
		v := *f.Value
		if f.JSON {
			if fieldcrypt.IsSealedJSON([]byte(v)) || v == "null" {
				continue
			}
			sealed, err := c.SealJSON(ctx, f.Scope, f.Row, []byte(v))
			if err != nil {
				return nil, false, err
			}
			s := string(sealed)
			out[i], changed = &s, true
			continue
		}
		if fieldcrypt.IsSealed(v) {
			continue
		}
		sealed, err := c.SealString(ctx, f.Scope, f.Row, v)
		if err != nil {
			return nil, false, err
		}
		out[i], changed = &sealed, true
	}
	return out, changed, nil
}

// TruncateRunes cuts s to at most maxBytes bytes without splitting a UTF-8
// sequence. A byte cut (s[:n]) could leave half a character, which Postgres
// TEXT refuses and clients render as U+FFFD.
func TruncateRunes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	if maxBytes <= 0 {
		return ""
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
