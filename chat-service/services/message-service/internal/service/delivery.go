package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
)

type deliveryStore interface {
	ReserveMessageDeliveryIntent(ctx context.Context, intent postgres.MessageDeliveryIntent) (*postgres.MessageDeliveryIntent, error)
	FetchPendingMessageDeliveryIntents(ctx context.Context, limit int) ([]postgres.MessageDeliveryIntent, error)
	CompleteMessageDeliveryIntent(ctx context.Context, key string) error
	InsertOutboxEventOnce(ctx context.Context, dedupeKey, eventType string, payload interface{}) error
	InsertMessageMediaReference(ctx context.Context, messageID, mediaID, conversationID uuid.UUID) error
	ViewerMayAccessChatMedia(ctx context.Context, viewerID, mediaID uuid.UUID) (bool, error)
}

func (s *Service) ViewerMayAccessChatMedia(ctx context.Context, viewerID, mediaID uuid.UUID) (bool, error) {
	return s.deliveryStore().ViewerMayAccessChatMedia(ctx, viewerID, mediaID)
}

func (s *Service) deliveryStore() deliveryStore {
	return s.convStore.(deliveryStore)
}

func (s *Service) completeMessageDelivery(ctx context.Context, intent *postgres.MessageDeliveryIntent) error {
	message := &scylla.Message{
		ConversationID: intent.ConversationID,
		Bucket:         intent.Bucket,
		Ts:             intent.MessageTS,
		MsgID:          intent.MessageID,
		SenderID:       intent.SenderID,
		Type:           intent.MessageType,
		Text:           intent.MessageText,
		MediaID:        intent.MediaID,
		CreatedAt:      intent.MessageTS,
	}
	if err := s.msgStore.CreateMessage(ctx, message); err != nil {
		return fmt.Errorf("persist message: %w", err)
	}
	for _, memberID := range intent.MemberIDs {
		if err := s.msgStore.UpsertInbox(ctx, memberID, intent.ConversationID, intent.SenderID, intent.MessageText, intent.MessageTS); err != nil {
			return fmt.Errorf("project inbox for %s: %w", memberID, err)
		}
	}
	if err := s.convStore.TouchConversation(ctx, intent.ConversationID, intent.MessageTS); err != nil {
		return fmt.Errorf("touch conversation: %w", err)
	}
	if intent.MediaID != nil {
		if err := s.deliveryStore().InsertMessageMediaReference(ctx, intent.MessageID, *intent.MediaID, intent.ConversationID); err != nil {
			return fmt.Errorf("persist canonical media reference: %w", err)
		}
	}

	recipients := make([]string, 0, len(intent.MemberIDs))
	for _, memberID := range intent.MemberIDs {
		if memberID != intent.SenderID {
			recipients = append(recipients, memberID.String())
		}
	}
	created := sharedEvents.MessageCreatedPayload{
		MessageID:      intent.MessageID.String(),
		ConversationID: intent.ConversationID.String(),
		SenderID:       intent.SenderID.String(),
		Type:           intent.MessageType,
		RecipientIDs:   recipients,
		CreatedAt:      intent.MessageTS,
	}
	if err := s.deliveryStore().InsertOutboxEventOnce(ctx, "message-created:"+intent.MessageID.String(), sharedEvents.MessageCreated, created); err != nil {
		return fmt.Errorf("queue message-created event: %w", err)
	}

	if intent.SourceApp == "dating" && intent.MatchID != nil {
		preview := intent.MessageText
		if len(preview) > 140 {
			preview = preview[:140]
		}
		for _, recipientID := range recipients {
			payload := sharedEvents.ChatDatingMessageNewPayload{
				ConversationID: intent.ConversationID.String(),
				MatchID:        intent.MatchID.String(),
				SenderID:       intent.SenderID.String(),
				RecipientID:    recipientID,
				MessagePreview: preview,
				SentAt:         intent.MessageTS,
			}
			dedupe := "dating-message:" + intent.MessageID.String() + ":" + recipientID
			if err := s.deliveryStore().InsertOutboxEventOnce(ctx, dedupe, sharedEvents.ChatDatingMessageNew, payload); err != nil {
				return fmt.Errorf("queue dating message event: %w", err)
			}
		}
	}

	if intent.FirstRequest {
		if intent.RequestReceiverID == nil {
			return fmt.Errorf("request delivery has no receiver")
		}
		if err := s.convStore.SetMessageRequestPreview(ctx, intent.ConversationID, intent.MessageText); err != nil {
			return fmt.Errorf("set message request preview: %w", err)
		}
		payload := sharedEvents.MessageRequestPayload{
			ConversationID: intent.ConversationID.String(),
			SenderID:       intent.SenderID.String(),
			ReceiverID:     intent.RequestReceiverID.String(),
			Preview:        intent.MessageText,
			OccurredAt:     intent.MessageTS,
		}
		if err := s.deliveryStore().InsertOutboxEventOnce(ctx, "message-request:"+intent.MessageID.String(), sharedEvents.MessageRequestCreated, payload); err != nil {
			return fmt.Errorf("queue message request event: %w", err)
		}
	}

	return s.deliveryStore().CompleteMessageDeliveryIntent(ctx, intent.IdempotencyKey)
}

func (s *Service) StartMessageDeliveryRepairWorker(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			intents, err := s.deliveryStore().FetchPendingMessageDeliveryIntents(ctx, 100)
			if err != nil {
				s.log.Error("failed to fetch pending message deliveries", "err", err)
				continue
			}
			for index := range intents {
				if err := s.completeMessageDelivery(ctx, &intents[index]); err != nil {
					s.log.Error("failed to repair message delivery", "err", err, "message_id", intents[index].MessageID)
				}
			}
		}
	}
}

func activeMemberIDs(members []postgres.Member) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.UserID)
	}
	return ids
}
