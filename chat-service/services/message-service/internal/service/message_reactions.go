// Emoji reactions on messages: toggling one and aggregating the
// per-emoji summary.

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/chat-message-service/internal/store/scylla"
	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
)

// --- Reactions ---

func (s *Service) ToggleReaction(ctx context.Context, userID, conversationID, messageID uuid.UUID, bucket string, ts time.Time, emoji string) (*ToggleReactionResponse, error) {
	if !ValidReactionEmoji(emoji) {
		return nil, ErrInvalidReaction
	}
	ok, err := s.convStore.CheckMembership(ctx, conversationID, userID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}

	// The same visibility check as the parity routes (C3): no reacting to
	// an expired disappearing message, or one outside the member's history.
	if _, err := s.visibleMessageAt(ctx, userID, conversationID, messageID, bucket, ts); err != nil {
		return nil, err
	}

	// Check if reaction already exists to determine toggle direction
	exists, err := s.msgStore.HasReaction(ctx, conversationID, bucket, ts, messageID, emoji, userID)
	if err != nil {
		return nil, err
	}

	added := !exists
	if exists {
		if err := s.msgStore.RemoveReaction(ctx, conversationID, bucket, ts, messageID, emoji, userID); err != nil {
			return nil, err
		}
	} else {
		if err := s.msgStore.AddReaction(ctx, conversationID, bucket, ts, messageID, emoji, userID); err != nil {
			return nil, err
		}
	}

	// Real-time delivery via Redis pub/sub to all members
	members, _ := s.convStore.GetMembers(ctx, conversationID)
	go func() {
		pubCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		payload, _ := json.Marshal(map[string]interface{}{
			"type": "reaction",
			"payload": map[string]interface{}{
				"conversation_id": conversationID,
				"message_id":      messageID,
				"user_id":         userID,
				"emoji":           emoji,
				"added":           added,
			},
		})
		// M2: pipeline the per-member PUBLISH commands (see message-
		// send path above for the rationale).
		pipe := s.rdb.Pipeline()
		recipients := 0
		for _, m := range members {
			// Sender included, same reason as the send path above: a
			// reaction added on one device has to appear on that person's
			// other devices. The client applies reaction updates
			// idempotently (add-if-absent, remove-if-present), so the
			// originating tab replaying its own frame changes nothing.
			publishPersonal(pubCtx, pipe, m, payload)
			recipients++
		}
		// Chat parity §2.5: the conversation room hears reactions too, the
		// same way it hears message / message_edited; always flush.
		pipe.Publish(pubCtx, fmt.Sprintf("convroom:%s", conversationID), payload)
		if _, err := pipe.Exec(pubCtx); err != nil {
			s.log.Warn("failed to pipeline-publish reaction to redis", "err", err, "recipients", recipients)
		}
	}()

	// Outbox event
	_ = s.convStore.InsertOutboxEvent(ctx, sharedEvents.ReactionToggled, sharedEvents.ReactionToggledPayload{
		MessageID:      messageID.String(),
		ConversationID: conversationID.String(),
		UserID:         userID.String(),
		Emoji:          emoji,
		Added:          added,
		OccurredAt:     time.Now(),
	})

	return &ToggleReactionResponse{
		Added:     added,
		Emoji:     emoji,
		MessageID: messageID,
	}, nil
}

// aggregateReactions groups flat reaction rows into emoji → user_ids summaries.
func aggregateReactions(rxns []scylla.Reaction) []ReactionSummary {
	emojiUsers := make(map[string][]string)
	for _, r := range rxns {
		emojiUsers[r.Emoji] = append(emojiUsers[r.Emoji], r.UserID.String())
	}
	out := make([]ReactionSummary, 0, len(emojiUsers))
	for emoji, userIDs := range emojiUsers {
		out = append(out, ReactionSummary{Emoji: emoji, UserIDs: userIDs})
	}
	return out
}
