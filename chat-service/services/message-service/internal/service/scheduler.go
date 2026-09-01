package service

import (
	"context"
	"time"
)

const scheduledMessageMaxAttempts = 5

// StartScheduledMessageWorker polls every 30s for due scheduled messages
// and sends them through the normal SendMessage pipeline.
// Stops when ctx is cancelled.
func (s *Service) StartScheduledMessageWorker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.processScheduledMessages(ctx)
		}
	}
}

func (s *Service) processScheduledMessages(ctx context.Context) {
	msgs, err := s.extrasStore().GetPendingScheduledMessages(ctx, time.Now(), 100)
	if err != nil {
		return
	}
	for _, msg := range msgs {
		_, sendErr := s.SendMessage(ctx, msg.SenderID, msg.ConversationID, msg.Type,
			derefStr(msg.Content), msg.MediaID, nil, "scheduled-"+msg.ID.String())
		if sendErr != nil {
			if err := s.extrasStore().RecordScheduledMessageFailure(ctx, msg.ID, sendErr.Error(), scheduledMessageMaxAttempts); err != nil {
				s.log.Error("failed to record scheduled message retry", "message_id", msg.ID, "err", err)
			}
			continue
		}
		_ = s.extrasStore().MarkScheduledMessageSent(ctx, msg.ID)
	}
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
