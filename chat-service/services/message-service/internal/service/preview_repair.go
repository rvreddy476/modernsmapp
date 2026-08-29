package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// MP-LB-1: a successful message deletion must never leave the deleted text in
// chat.conversations.last_message_preview indefinitely. The repair used to be
// a best-effort call after the Scylla soft delete; a crash, Scylla read
// failure, PostgreSQL write failure or restart in that window stranded the
// plaintext on every member's inbox with nothing left to retry it.
//
// The repair is now a DURABLE OBLIGATION written before the delete:
//
//	1. obligation row committed (fails → the message is NOT deleted);
//	2. Scylla soft delete;
//	3. inline repair attempt; the obligation is completed only after the
//	   repair verifiably resolved;
//	4. anything that dies or fails between 2 and 3 is resumed by
//	   [Service.StartPreviewRepairWorker] after restart.

type previewRepairStore interface {
	CreatePreviewRepairObligation(ctx context.Context, conversationID, messageID uuid.UUID, bucket string, deletedTs time.Time) error
	ClaimDuePreviewRepairObligations(ctx context.Context, limit int, lease time.Duration) ([]postgres.PreviewRepairObligation, error)
	CompletePreviewRepairObligation(ctx context.Context, messageID uuid.UUID) error
	DeferPreviewRepairObligation(ctx context.Context, messageID uuid.UUID, retryIn time.Duration, lastErr string) error
}

func (s *Service) previewRepairStore() previewRepairStore {
	return s.convStore.(previewRepairStore)
}

const (
	// previewRecomputeScan is how many rows the survivor lookup reads.
	// Deliberately wider than one: Scylla applies LIMIT before the
	// soft-delete filter, so a scan of 1 returned only the message just
	// deleted and blanked the preview instead of repairing it.
	previewRecomputeScan = 20

	previewRepairClaimBatch   = 50
	previewRepairLease        = 2 * time.Minute
	previewRepairPollInterval = 5 * time.Second

	// liveMessageRetireAfter is how old an obligation must be before finding
	// its message STILL LIVE proves the deletion never happened (the crash
	// landed between the obligation write and the Scylla delete). Younger
	// than this, the deleting request may simply still be in flight, so the
	// worker defers rather than touching a live message's preview.
	liveMessageRetireAfter = 10 * time.Minute

	previewRepairBackoffBase = 5 * time.Second
	previewRepairBackoffCap  = 10 * time.Minute
)

type previewRepairOutcome int

const (
	previewRepairResolved previewRepairOutcome = iota
	previewRepairRetry
)

// resolvePreviewRepair drives one obligation to a safe conclusion.
//
//   - Scylla read failure or PostgreSQL write failure → retry (the durable
//     obligation stays pending; nothing is lost).
//   - Message still LIVE (write-ahead case: the obligation committed but the
//     Scylla delete never happened) → never touch the preview; retry while
//     the deleting request could still be in flight, retire once the
//     obligation is old enough that the absence of a deletion is proven.
//   - Deletion confirmed → recompute from the newest survivor and rewrite
//     through [postgres.ConversationStore.ReplaceLastMessage], whose window
//     guard makes the write a no-op when a genuinely newer message already
//     owns the preview — so repair can never overwrite concurrent delivery,
//     and replaying a resolved obligation is harmless.
func (s *Service) resolvePreviewRepair(ctx context.Context, o postgres.PreviewRepairObligation) (previewRepairOutcome, error) {
	msg, err := s.msgStore.GetMessage(ctx, o.ConversationID, o.Bucket, o.DeletedTs, o.MessageID)
	if err != nil {
		return previewRepairRetry, fmt.Errorf("verify deletion: %w", err)
	}
	if msg != nil && !msg.IsDeleted {
		if time.Since(o.CreatedAt) < liveMessageRetireAfter {
			// The deleting request may still be between its obligation
			// write and its Scylla delete. Not an error — just not provable
			// yet.
			return previewRepairRetry, nil
		}
		// Old obligation, live message: the deletion demonstrably never
		// happened. Retire without touching the live preview.
		return previewRepairResolved, nil
	}
	// msg == nil (the row vanished entirely) repairs the same way as a
	// confirmed soft delete: the guarded rewrite below only fires if the
	// preview still points at the obligation's timestamp.

	msgs, _, err := s.msgStore.GetMessages(ctx, o.ConversationID, nil, previewRecomputeScan)
	if err != nil {
		return previewRepairRetry, fmt.Errorf("find surviving preview: %w", err)
	}
	preview := ""
	var senderID *uuid.UUID
	var ts *time.Time
	if len(msgs) > 0 {
		newest := msgs[0]
		preview = newest.Text
		sender := newest.SenderID
		newestTs := newest.Ts
		senderID = &sender
		ts = &newestTs
	}
	if err := s.convStore.ReplaceLastMessage(ctx, o.ConversationID, o.DeletedTs, preview, senderID, ts); err != nil {
		return previewRepairRetry, fmt.Errorf("rewrite preview: %w", err)
	}
	return previewRepairResolved, nil
}

// StartPreviewRepairWorker resumes pending preview-repair obligations until
// the service's shutdown context ends. Safe with multiple replicas: claims go
// through FOR UPDATE SKIP LOCKED with a lease, so no two workers process the
// same obligation and a crashed worker's claim expires on its own.
func (s *Service) StartPreviewRepairWorker(ctx context.Context) {
	ticker := time.NewTicker(previewRepairPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.runPreviewRepairPass(ctx); err != nil {
				s.log.Error("preview repair pass failed", "err", err)
			}
		}
	}
}

func (s *Service) runPreviewRepairPass(ctx context.Context) error {
	obligations, err := s.previewRepairStore().ClaimDuePreviewRepairObligations(ctx, previewRepairClaimBatch, previewRepairLease)
	if err != nil {
		return err
	}
	for _, o := range obligations {
		outcome, rerr := s.resolvePreviewRepair(ctx, o)
		switch outcome {
		case previewRepairResolved:
			if cerr := s.previewRepairStore().CompletePreviewRepairObligation(ctx, o.MessageID); cerr != nil {
				// The obligation stays leased and is re-claimed after the
				// lease expires; resolution is idempotent, so the retried
				// completion costs a no-op rewrite at worst.
				s.log.Warn("preview repair completion failed; will retry",
					"err", cerr, "message_id", o.MessageID)
			}
		case previewRepairRetry:
			lastErr := ""
			if rerr != nil {
				lastErr = rerr.Error()
				s.log.Warn("preview repair deferred",
					"err", rerr, "message_id", o.MessageID,
					"conversation_id", o.ConversationID, "attempts", o.AttemptCount)
			}
			if derr := s.previewRepairStore().DeferPreviewRepairObligation(
				ctx, o.MessageID, previewRepairBackoff(o.AttemptCount), lastErr,
			); derr != nil {
				// Still safe: the claim lease acts as the fallback backoff.
				s.log.Warn("preview repair defer failed", "err", derr, "message_id", o.MessageID)
			}
		}
	}
	return nil
}

// previewRepairBackoff grows the retry delay with the attempt count, bounded
// so a persistent failure retries forever at the cap rather than never.
func previewRepairBackoff(attempts int) time.Duration {
	backoff := previewRepairBackoffBase
	for i := 1; i < attempts && backoff < previewRepairBackoffCap; i++ {
		backoff *= 2
	}
	if backoff > previewRepairBackoffCap {
		backoff = previewRepairBackoffCap
	}
	return backoff
}
