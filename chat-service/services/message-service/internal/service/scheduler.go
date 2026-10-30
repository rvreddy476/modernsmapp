package service

import (
	"context"
	"time"
)

const (
	scheduledMessageMaxAttempts = 5
	// scheduledClaimBatch is how many due rows one replica claims per tick.
	scheduledClaimBatch = 100
)

// StartScheduledMessageWorker polls every 30s for due scheduled messages
// and sends them through the normal SendMessage pipeline.
// Stops when ctx is cancelled.
//
// Safe on every replica (audit M2, 30 Oct 2026): GetPendingScheduledMessages
// claims rows with FOR UPDATE SKIP LOCKED and a lease, so no two replicas
// send the same row (the loser used to record a failure attempt on a message
// that was in fact sent), and it picks at most ScheduledPerSenderPerClaim
// rows per sender per claim, so one sender's backlog cannot starve others.
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
	msgs, err := s.extrasStore().GetPendingScheduledMessages(ctx, time.Now(), scheduledClaimBatch)
	if err != nil {
		s.log.Error("failed to claim scheduled messages", "err", err)
		return
	}
	for _, msg := range msgs {
		_, sendErr := s.SendMessage(ctx, msg.SenderID, msg.ConversationID, msg.Type,
			derefStr(msg.Content), msg.MediaID, nil, nil, "scheduled-"+msg.ID.String())
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
