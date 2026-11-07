// Changing a sent message: editing (with the pending-request edit rules)
// and deleting or unsending it.

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
)

// EditMessage rewrites the author's own message and tells every member.
//
// There was no edit path on the server at all: the web called
// PATCH /conversations/:id/messages/:messageId, which was not a registered
// route. The client updated its own copy optimistically, so the author saw
// the new text and nobody else ever did — not even after a reload, because
// nothing was stored.
func (s *Service) EditMessage(
	ctx context.Context,
	userID, conversationID, messageID uuid.UUID,
	bucket string,
	ts time.Time,
	newText string,
) (*scylla.Message, error) {
	newText = strings.TrimSpace(newText)
	if newText == "" {
		return nil, invalidInput("message text is required")
	}
	// The same cap as send (H4): the edited text rides the MessageEdited
	// event and every member's frame, so it can never be larger than a
	// message could be when it was sent.
	if utf8.RuneCountInString(newText) > MessageTextMaxRunes {
		return nil, fmt.Errorf("%w: text exceeds %d characters", ErrInvalidMessageContent, MessageTextMaxRunes)
	}

	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}

	// An edit puts new content in front of the other members, so it runs
	// the send gates that judge content (H2): chat pause on either side of
	// a DM, a closed dating match, and the message-request rules below.
	if _, err := s.contentGates(ctx, userID, conversationID); err != nil {
		return nil, err
	}
	if err := s.editRequestRules(ctx, conversationID, newText); err != nil {
		return nil, err
	}

	// The same visibility check as the parity routes (C3): an expired
	// disappearing message, or one outside the editor's history, is gone.
	msg, err := s.visibleMessageAt(ctx, userID, conversationID, messageID, bucket, ts)
	if err != nil {
		return nil, err
	}

	// Author only. A group admin may DELETE someone else's message; rewriting
	// it would be putting words in their mouth, which is a different power.
	if msg.SenderID != userID {
		return nil, forbidden("not allowed to edit this message")
	}
	// A media message carries its meaning in the attachment, not in a text
	// column that happens to be empty.
	if msg.Type != "" && msg.Type != "text" {
		return nil, invalidInput("only text messages can be edited")
	}

	editedAt := time.Now().UTC()
	if err := s.msgStore.UpdateMessageText(ctx, conversationID, bucket, ts, messageID, newText, editedAt); err != nil {
		return nil, err
	}
	msg.Text = newText
	msg.IsEdited = true
	msg.EditedAt = &editedAt

	// The inbox preview is denormalised from the newest message. Its guard is
	// `last_message_at <= ts`, so passing this message's own timestamp updates
	// the preview when this IS the newest message and is a no-op when a newer
	// one has already replaced it — no stale text, no resurrected preview.
	if err := s.lastMessageStore().SetLastMessage(ctx, conversationID, msg.SenderID, newText, msg.Ts); err != nil {
		s.log.Warn("edit: inbox preview refresh failed", "err", err, "message_id", messageID)
	}

	s.rdb.Del(ctx, fmt.Sprintf("chat_messages:%s", conversationID))

	// Chat parity: search re-indexes the new text.
	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MessageEdited, sharedEvents.MessageEditedPayload{
		MessageID:      messageID.String(),
		ConversationID: conversationID.String(),
		SenderID:       msg.SenderID.String(),
		Text:           newText,
		EditedAt:       editedAt,
	})

	members, _ := s.convStore.GetMembers(ctx, conversationID)
	// The fan-out outlives the request: detach from its cancellation but
	// keep its values (request id, trace).
	bgCtx := context.WithoutCancel(ctx)
	go func() {
		pubCtx, cancel := context.WithTimeout(bgCtx, 2*time.Second)
		defer cancel()
		payload, _ := json.Marshal(map[string]interface{}{
			"type": "message_edited",
			"payload": map[string]interface{}{
				"conversation_id": conversationID,
				"msg_id":          messageID,
				"new_text":        newText,
				"edited_at":       editedAt,
			},
		})
		pipe := s.rdb.Pipeline()
		recipients := 0
		for _, m := range members {
			// The author's other sessions included: an edit made on the
			// phone has to land on the same person's open laptop tab.
			publishPersonal(pubCtx, pipe, m, payload)
			recipients++
		}
		pipe.Publish(pubCtx, fmt.Sprintf("convroom:%s", conversationID), payload)
		if _, err := pipe.Exec(pubCtx); err != nil {
			s.log.Warn("failed to publish message edit", "err", err, "recipients", recipients)
		}
	}()

	return msg, nil
}

// editRequestRules applies the message-request rules to an edit (H2). While
// the conversation is a request, the only message in it is the sender's one
// allowed first message, so its edit obeys the same rules: plain text, no
// link, at most 500 characters. A request that is no longer pending (ignored,
// blocked, reported, expired) takes no edits at all, just as it takes no
// further messages.
func (s *Service) editRequestRules(ctx context.Context, conversationID uuid.UUID, newText string) error {
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if conv == nil || !conv.IsRequest {
		return nil
	}
	mr, err := s.convStore.GetMessageRequestByConversation(ctx, conversationID)
	if err != nil {
		return err
	}
	if mr == nil || mr.Status != "pending" || (!mr.ExpiresAt.IsZero() && !mr.ExpiresAt.After(time.Now())) {
		return ErrAwaitingRequestAcceptance
	}
	if containsLink(newText) {
		return fmt.Errorf("%w: links are not allowed", ErrRequestFirstMessageInvalid)
	}
	if utf8.RuneCountInString(newText) > requestFirstMessageMaxRunes {
		return fmt.Errorf("%w: exceeds %d characters", ErrRequestFirstMessageInvalid, requestFirstMessageMaxRunes)
	}
	return nil
}

func (s *Service) DeleteMessage(ctx context.Context, userID, conversationID, messageID uuid.UUID, bucket string, ts time.Time) error {
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotMember
	}
	msg, err := s.msgStore.GetMessage(ctx, conversationID, bucket, ts, messageID)
	if err != nil {
		return err
	}
	if msg == nil || msg.IsDeleted {
		return ErrMessageNotFound
	}
	authorized := msg.SenderID == userID
	if !authorized {
		conv, err := s.convStore.GetConversation(ctx, conversationID)
		if err != nil {
			return err
		}
		if conv == nil {
			return notFound("conversation not found")
		}
		if conv.Type == "group" {
			role, err := s.convStore.GetMemberRole(ctx, conversationID, userID)
			if err != nil {
				return err
			}
			// Chat parity §2.3: the group OWNER may unsend members'
			// messages too (the owner role was refused here before).
			authorized = role == "admin" || role == "owner"
		}
	}
	if !authorized {
		return ErrNotPermittedDelete
	}

	// MP-LB-1 WRITE-AHEAD: the durable preview-repair obligation is committed
	// BEFORE the soft delete. The inbox preview is denormalized from the
	// newest message; if that is THIS message, its text keeps showing on
	// every member's inbox until repaired, and the repair can fail after the
	// delete succeeded (Scylla read, PostgreSQL write, crash, restart). With
	// the obligation on disk first, every one of those failures is resumed by
	// the repair worker. If the obligation itself cannot be recorded, the
	// message is NOT deleted — the client retries a delete that did nothing.
	if err := s.previewRepairStore().CreatePreviewRepairObligation(
		ctx, conversationID, messageID, bucket, msg.Ts,
	); err != nil {
		return fmt.Errorf("record preview repair obligation: %w", err)
	}

	// Unsend removes the content everywhere it was kept beside the message
	// (22 Oct 2026): the completed delivery intent, the cached send
	// response, the published MessageCreated payload, translations and star
	// previews. It runs BEFORE the Scylla delete: if it fails nothing is
	// deleted and the client's retry runs both again (both are idempotent);
	// the other order would leave a deleted message whose copies a retry
	// can never reach ("message not found").
	if err := s.convStore.RedactUnsentMessageRecords(ctx, conversationID, messageID); err != nil {
		return fmt.Errorf("redact unsent message records: %w", err)
	}

	if err := s.msgStore.SoftDeleteMessage(ctx, conversationID, bucket, ts, messageID); err != nil {
		// The obligation stands; the worker later finds the message still
		// live and retires it without touching the preview.
		return err
	}

	// Invalidate cache
	key := fmt.Sprintf("chat_messages:%s", conversationID)
	s.rdb.Del(ctx, key)

	// Inline repair attempt. On success the obligation is retired; on any
	// failure it stays durable and the worker finishes the job — the API
	// still reports success because the message IS deleted.
	obligation := postgres.PreviewRepairObligation{
		MessageID:      messageID,
		ConversationID: conversationID,
		Bucket:         bucket,
		DeletedTs:      msg.Ts,
		CreatedAt:      time.Now(),
	}
	if outcome, rerr := s.resolvePreviewRepair(ctx, obligation); outcome == previewRepairResolved {
		if cerr := s.previewRepairStore().CompletePreviewRepairObligation(ctx, messageID); cerr != nil {
			s.log.Warn("preview repair completion failed; worker will retire it",
				"err", cerr, "message_id", messageID)
		}
	} else if rerr != nil {
		s.log.Warn("inline preview repair deferred to worker",
			"err", rerr, "message_id", messageID, "conversation_id", conversationID)
	}

	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.MessageDeleted, sharedEvents.MessageDeletedPayload{
		MessageID:      messageID.String(),
		ConversationID: conversationID.String(),
		DeletedBy:      userID.String(),
		DeletedAt:      time.Now(),
	})

	// Chat parity §2.3: unsent messages leave the shared tabs, and every
	// member (and the room) hears message_deleted, like message_edited.
	if ps, ok := s.parity(); ok {
		if err := ps.RemoveSharedItem(ctx, conversationID, messageID); err != nil {
			s.log.Warn("unsend: shared-items removal failed", "err", err, "message_id", messageID)
		}
		// An unsent message no longer counts as unread for anyone who had
		// not read it yet.
		if err := ps.ReleaseUnreadForUnsent(ctx, conversationID, messageID, msg.SenderID, msg.Ts); err != nil {
			s.log.Warn("unsend: unread release failed", "err", err, "message_id", messageID)
		}
	}
	if members, err := s.convStore.GetMembers(ctx, conversationID); err == nil {
		s.publishToConversation(conversationID, members, "message_deleted", map[string]interface{}{
			"conversation_id": conversationID,
			"message_id":      messageID,
		})
	}

	return nil
}
