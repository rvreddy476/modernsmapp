package postgres

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/atpost/chat-message-service/internal/contentcrypt"
	"github.com/atpost/chat-shared/fieldcrypt"
	"github.com/google/uuid"
)

// Field encryption at rest (22 Oct 2026). Every content-bearing column this
// store writes is sealed here and opened here, so the rest of the service only
// ever sees plaintext and the database only ever holds ciphertext. Values
// written before encryption carry no envelope prefix and read through
// unchanged (dual read), so turning encryption on needs no downtime and the
// backfill (cmd/fieldcrypt-backfill) re-seals them at its own pace.
//
// Read failures split two ways:
//   - authoritative content (an intent's text, a scheduled message, a note, a
//     cached response) fails the read: handing ciphertext on, or "", would be
//     silent corruption;
//   - derived previews (the inbox line, a request or star preview) degrade to
//     "" with a warning that names the row only. They are display copies and
//     one bad row must not take a whole inbox down.

// SetFieldCipher installs the at-rest cipher. Call it before serving.
func (s *ConversationStore) SetFieldCipher(c *fieldcrypt.Cipher) { s.crypt = c }

// FieldCipher returns the installed cipher (nil: plaintext).
func (s *ConversationStore) FieldCipher() *fieldcrypt.Cipher { return s.crypt }

func (s *ConversationStore) sealText(ctx context.Context, scope fieldcrypt.Scope, row []byte, v string) (string, error) {
	out, err := s.crypt.SealString(ctx, scope, row, v)
	if err != nil {
		return "", fmt.Errorf("seal %s: %w", scope, err)
	}
	return out, nil
}

func (s *ConversationStore) sealTextPtr(ctx context.Context, scope fieldcrypt.Scope, row []byte, v *string) (*string, error) {
	if v == nil {
		return nil, nil
	}
	out, err := s.sealText(ctx, scope, row, *v)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *ConversationStore) openText(ctx context.Context, scope fieldcrypt.Scope, row []byte, v string) (string, error) {
	out, err := s.crypt.OpenString(ctx, scope, row, v)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", scope, err)
	}
	return out, nil
}

func (s *ConversationStore) openTextPtr(ctx context.Context, scope fieldcrypt.Scope, row []byte, v *string) (*string, error) {
	if v == nil {
		return nil, nil
	}
	out, err := s.openText(ctx, scope, row, *v)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// openPreview opens a derived display copy, degrading to "" on failure.
func (s *ConversationStore) openPreview(ctx context.Context, scope fieldcrypt.Scope, row []byte, v string) string {
	out, err := s.crypt.OpenString(ctx, scope, row, v)
	if err != nil {
		s.logger().Warn("field encryption: a preview did not open; showing none", "scope", string(scope), "err", err)
		return ""
	}
	return out
}

func (s *ConversationStore) openPreviewPtr(ctx context.Context, scope fieldcrypt.Scope, row []byte, v *string) *string {
	if v == nil {
		return nil
	}
	out := s.openPreview(ctx, scope, row, *v)
	return &out
}

func (s *ConversationStore) sealJSON(ctx context.Context, scope fieldcrypt.Scope, row []byte, doc []byte) ([]byte, error) {
	out, err := s.crypt.SealJSON(ctx, scope, row, doc)
	if err != nil {
		return nil, fmt.Errorf("seal %s: %w", scope, err)
	}
	return out, nil
}

func (s *ConversationStore) openJSON(ctx context.Context, scope fieldcrypt.Scope, row []byte, doc []byte) ([]byte, error) {
	out, err := s.crypt.OpenJSON(ctx, scope, row, doc)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", scope, err)
	}
	return out, nil
}

// openConversation opens the inbox preview on a scanned conversation row.
func (s *ConversationStore) openConversation(ctx context.Context, c *Conversation) {
	c.LastMessagePreview = s.openPreview(ctx, contentcrypt.ScopeConversationPreview, contentcrypt.ConversationRow(c.ID), c.LastMessagePreview)
}

// sealConversationPreview cuts the inbox preview on a character boundary
// (it used to be a byte cut, which could split a character) and seals it.
func (s *ConversationStore) sealConversationPreview(ctx context.Context, conversationID uuid.UUID, preview string) (string, error) {
	preview = contentcrypt.TruncateRunes(preview, ConversationPreviewMaxBytes)
	return s.sealText(ctx, contentcrypt.ScopeConversationPreview, contentcrypt.ConversationRow(conversationID), preview)
}

// ConversationPreviewMaxBytes caps chat.conversations.last_message_preview
// before sealing.
const ConversationPreviewMaxBytes = 140

func (s *ConversationStore) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}
