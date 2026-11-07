// Reading a conversation: paged message history across time buckets and
// marking messages read (the read cursor and receipt message id).

package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/google/uuid"
)

// historyOldestBucket is the oldest month bucket a conversation's messages can
// be in: the month before it was created (one month of slack for an app
// clock behind the database's at a month boundary). "" when unknown.
func historyOldestBucket(conv *postgres.Conversation) string {
	if conv == nil || conv.CreatedAt.IsZero() {
		return ""
	}
	c := conv.CreatedAt.UTC()
	return time.Date(c.Year(), c.Month()-1, 1, 0, 0, 0, 0, time.UTC).Format("200601")
}

func (s *Service) GetMessages(ctx context.Context, userID, conversationID uuid.UUID, cursor *scylla.MessageCursor, limit int) ([]MessageResponse, *scylla.MessageCursor, error) {
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, ErrNotMember
	}
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, nil, err
	}
	oldest := historyOldestBucket(conv)

	messages, nextCursor, err := s.msgStore.GetMessages(ctx, conversationID, cursor, limit, oldest)
	if err != nil {
		return nil, nil, err
	}
	// Chat parity §1.2 / §1.3 / §2.3: the member's cleared history and the
	// messages they deleted for themselves are not returned.
	messages, nextCursor, err = s.applyHistoryCutoff(ctx, userID, conversationID, messages, nextCursor)
	if err != nil {
		return nil, nil, err
	}
	// Disappearing messages: one past its expiry is never served, even in
	// the seconds before the expiry worker deletes it.
	now := s.clock()
	messages = dropExpired(messages, now)
	// Messages hidden for this member are filtered after the read, so a page
	// can come back short or empty while older history remains. Keep reading
	// (bounded) so a run of hidden messages never looks like the start of
	// the chat to a client that stops on an empty page.
	// An empty read with a cursor is a bucket-only resume point (a run of
	// empty months longer than one read's bucket budget): keep going from it.
	for round := 0; round < 5 && len(messages) < limit && nextCursor != nil; round++ {
		more, moreCursor, err := s.msgStore.GetMessages(ctx, conversationID, nextCursor, limit-len(messages), oldest)
		if err != nil {
			return nil, nil, err
		}
		more, moreCursor, err = s.applyHistoryCutoff(ctx, userID, conversationID, more, moreCursor)
		if err != nil {
			return nil, nil, err
		}
		messages = append(messages, dropExpired(more, now)...)
		nextCursor = moreCursor
	}

	// Group messages by bucket to batch-fetch reactions.
	bucketKeys := make(map[string][]scylla.MsgKey)
	for _, m := range messages {
		bucketKeys[m.Bucket] = append(bucketKeys[m.Bucket], scylla.MsgKey{Ts: m.Ts, MsgID: m.MsgID})
	}

	allReactions := make(map[uuid.UUID][]scylla.Reaction)
	for bucket, keys := range bucketKeys {
		rxns, err := s.msgStore.GetReactionsForMessages(ctx, conversationID, bucket, keys)
		if err != nil {
			s.log.Warn("failed to fetch reactions", "err", err, "bucket", bucket)
			continue
		}
		for id, r := range rxns {
			allReactions[id] = append(allReactions[id], r...)
		}
	}

	// Batch-fetch sender profiles
	senderIDSet := make(map[uuid.UUID]struct{})
	for _, m := range messages {
		senderIDSet[m.SenderID] = struct{}{}
	}
	senderIDs := make([]uuid.UUID, 0, len(senderIDSet))
	for id := range senderIDSet {
		senderIDs = append(senderIDs, id)
	}
	senderProfiles, err := s.convStore.GetUserProfiles(ctx, senderIDs)
	if err != nil {
		s.log.Warn("failed to fetch sender profiles", "err", err)
		senderProfiles = map[uuid.UUID]postgres.UserProfile{}
	}

	out := make([]MessageResponse, 0, len(messages))
	for _, m := range messages {
		resp := MessageResponse{
			ConversationID:  m.ConversationID,
			Bucket:          m.Bucket,
			Ts:              m.Ts,
			MsgID:           m.MsgID,
			SenderID:        m.SenderID,
			Type:            m.Type,
			Text:            m.Text,
			MediaID:         m.MediaID,
			PostID:          m.PostID,
			ReplyToID:       m.ReplyToID,
			ReplyToPreview:  m.ReplyToPreview,
			ReplyToSenderID: m.ReplyToSenderID,
			IsEdited:        m.IsEdited,
			EditedAt:        m.EditedAt,
			CreatedAt:       m.CreatedAt,
		}
		if p, ok := senderProfiles[m.SenderID]; ok {
			resp.SenderDisplayName = p.DisplayName
		}
		if rxns, ok := allReactions[m.MsgID]; ok {
			resp.Reactions = aggregateReactions(rxns)
		}
		applyExtra(&resp, decodeExtra(m.Extra))
		out = append(out, resp)
	}
	s.resolveStickerURLs(ctx, userID, out)
	// Fix pass 2: moderation usually lands after the send; complete
	// media.sensitive from media-service (cached, bounded).
	s.fillMediaSensitivity(ctx, out)
	// Ticks: sent | delivered | read on the viewer's own messages.
	s.attachTickStatus(ctx, userID, conversationID, out)

	return out, nextCursor, nil
}

// MarkRead durably advances the reader's unread watermark, then broadcasts a
// read receipt ONLY when the reader's privacy settings permit disclosure.
//
// The cursor write is unconditional — unread state is the reader's OWN data
// and repairs push/inbox counts (CH-LB-4.6). The receipt broadcast is a
// DISCLOSURE and obeys who_can_see_read_receipts: no_one (or unknown policy)
// discloses nothing; everyone and connections_only disclose to the other
// member of this accepted chat, unless THEIR receipts are off (reciprocity;
// readReceiptVisible in receipt_ticks.go). Group conversations get no
// per-user receipt frames at launch — members get only the aggregate "read
// by everyone" watermark, never a reader roster (directive §3.5).
func (s *Service) MarkRead(ctx context.Context, userID, conversationID uuid.UUID, messageID string) error {
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotMember
	}

	now := time.Now().UTC()
	// Ticks: the group read frame goes only to members whose "everyone has
	// read" watermark this read moves, which needs the cursor before it.
	prevRead := s.priorReadAt(ctx, conversationID, userID)
	if msgID, err := uuid.Parse(messageID); err == nil {
		if err := s.groupStore().UpsertReadCursor(ctx, conversationID, userID, msgID, now); err != nil {
			s.log.Warn("read cursor upsert failed", "err", err, "conversation_id", conversationID, "user_id", userID)
		}
	}
	// Chat parity §1: reading resets the server-maintained unread count
	// (the inbox also reconciles it against the cursor written above).
	if ps, ok := s.parity(); ok {
		if err := ps.ResetUnread(ctx, conversationID, userID); err != nil {
			s.log.Warn("unread reset failed", "err", err, "conversation_id", conversationID, "user_id", userID)
		}
	}

	policy := s.GetChatPolicy(ctx, userID)
	if !policy.Known || policy.ChatPaused || policy.ReadReceiptsVisibility == "no_one" {
		return nil
	}

	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil || conv == nil {
		return nil
	}
	// DM consent: no read receipts while the conversation is a request
	// (pending, or deleted without the sender being told) — reading a
	// request must not tell its sender anything.
	if conv.IsRequest {
		return nil
	}
	members, _ := s.convStore.GetMembers(ctx, conversationID)

	if conv.Type != "direct" {
		// Launch groups: aggregate counts only, computed from read cursors —
		// no per-reader receipt frames. The aggregate "read by everyone"
		// watermark (the coloured tick) is the only group frame.
		s.publishGroupReadAggregate(ctx, conversationID, userID, prevRead)
		return nil
	}

	// L6: the frame names a message of THIS conversation, or nothing is
	// forwarded. The client's string used to be echoed verbatim to the
	// other member, whatever it held. The read itself (cursor, unread
	// reset) above still counts.
	receiptMsgID, ok := s.receiptMessageID(ctx, conversationID, messageID)
	if !ok {
		return nil
	}

	// Dating mechanic M9: a gated dating conversation delivers the frame
	// only to members dating allows to see receipts.
	gated := false
	if meta, err := s.convStore.GetConversationMeta(ctx, conversationID); err == nil && meta != nil {
		gated = meta.ReceiptsGated
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "read_receipt",
		"payload": map[string]interface{}{
			"conversation_id": conversationID.String(),
			"user_id":         userID.String(),
			"message_id":      receiptMsgID.String(),
			"read_at":         now.Format(time.RFC3339Nano),
			// Ticks: the recipient's read watermark (= read_at in a DM).
			"read_up_to": now.Format(time.RFC3339Nano),
		},
	})
	for _, m := range members {
		if m.UserID == userID {
			continue
		}
		// One rule, reciprocity included: a member whose own receipts are
		// off sees nobody's (readReceiptVisible, receipt_ticks.go).
		if !readReceiptVisible(s.GetChatPolicy(ctx, m.UserID), policy) {
			continue
		}
		if gated && !s.datingReceiptsAllowed(ctx, conversationID, m.UserID) {
			continue
		}
		publishPersonal(ctx, s.rdb, m, payload)
	}
	return nil
}

// receiptMessageID parses a read receipt's message_id and confirms the
// message belongs to conversationID (its durable delivery record). Anything
// else — not a UUID, another conversation's message, no record — is false.
func (s *Service) receiptMessageID(ctx context.Context, conversationID uuid.UUID, raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, false
	}
	if _, _, err := s.locateMessage(ctx, conversationID, id, nil); err != nil {
		if !errors.Is(err, ErrMessageNotFound) {
			s.log.Warn("read receipt: message lookup failed", "err", err, "conversation_id", conversationID)
		}
		return uuid.Nil, false
	}
	return id, true
}
