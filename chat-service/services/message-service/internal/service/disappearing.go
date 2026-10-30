package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
)

// Disappearing messages (20 Oct 2026), WhatsApp semantics:
//
//   - the timer is CONVERSATION-WIDE: either member of a DM, or a group
//     owner/admin, sets it for everyone;
//   - a message sent while it is on expires at created_at + the timer in force
//     AT SEND TIME. The expiry rides the message (MessageExtra.ExpiresAt) and
//     a chat.message_expiries row; changing the timer later, or turning it
//     off, never moves, extends or cancels an existing message's expiry;
//   - when it expires the message is deleted for everyone along the unsend
//     path: preview repair obligation, unread release, shared-tab removal,
//     MessageDeleted outbox event and the `message_deleted` realtime frame
//     clients already handle (with "reason": "expired" added).

// Allowed timer values in milliseconds. 0 is "off" on the wire (stored NULL).
const (
	DisappearOff int64 = 0
	Disappear24h int64 = 24 * 60 * 60 * 1000
	Disappear7d  int64 = 7 * Disappear24h
	Disappear90d int64 = 90 * Disappear24h
)

// ErrInvalidDisappearTimer is a timer value outside the allowed set.
var ErrInvalidDisappearTimer = errors.New("disappear_after_ms must be 0 (off), 86400000 (24 hours), 604800000 (7 days) or 7776000000 (90 days)")

// ValidDisappearAfterMs reports whether ms is an allowed timer value.
func ValidDisappearAfterMs(ms int64) bool {
	switch ms {
	case DisappearOff, Disappear24h, Disappear7d, Disappear90d:
		return true
	}
	return false
}

// disappearExpiry is the expiry a message sent at sentAt gets under the
// timer in force then: nil when the timer is off.
func disappearExpiry(sentAt time.Time, timerMs *int64) *time.Time {
	if timerMs == nil || *timerMs <= 0 {
		return nil
	}
	at := sentAt.Add(time.Duration(*timerMs) * time.Millisecond)
	return &at
}

// dropExpired removes messages whose expiry has passed (the read-side guard
// for the window before the worker deletes them).
func dropExpired(msgs []scylla.Message, now time.Time) []scylla.Message {
	out := msgs[:0:0]
	for _, m := range msgs {
		if e := decodeExtra(m.Extra); e != nil && e.ExpiresAt != nil && !e.ExpiresAt.After(now) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// disappearStore is the Postgres surface the feature needs. Reached by a
// checked type assertion so the in-memory fakes older tests use keep
// compiling (and simply schedule nothing).
type disappearStore interface {
	GetDisappearTimer(ctx context.Context, conversationID uuid.UUID) (*postgres.DisappearTimer, error)
	SetDisappearTimer(ctx context.Context, conversationID uuid.UUID, afterMs *int64, updatedBy uuid.UUID) error
	ScheduleMessageExpiry(ctx context.Context, e postgres.MessageExpiry) error
	ClaimDueMessageExpiries(ctx context.Context, limit int, lease time.Duration) ([]postgres.MessageExpiry, error)
	CompleteMessageExpiry(ctx context.Context, messageID uuid.UUID) error
	DeferMessageExpiry(ctx context.Context, messageID uuid.UUID, retryIn time.Duration, lastErr string) error
	ExpireMessageRecords(ctx context.Context, e postgres.MessageExpiry, alreadyGone bool, eventType string, eventPayload interface{}) ([]uuid.UUID, error)
}

func (s *Service) disappearing() (disappearStore, bool) {
	ds, ok := s.convStore.(disappearStore)
	return ds, ok
}

// messageRedactor is the Scylla content wipe (falls back to the soft delete).
type messageRedactor interface {
	RedactMessage(ctx context.Context, conversationID uuid.UUID, bucket string, ts time.Time, msgID uuid.UUID) error
}

// inboxProjectionCleaner drops the per-member inbox projection rows one
// message wrote (they hold a copy of its text).
type inboxProjectionCleaner interface {
	DeleteInboxProjection(ctx context.Context, memberIDs []uuid.UUID, conversationID uuid.UUID, ts time.Time) error
}

// scheduleExpiryFor records the expiry of a message whose stored payload
// carries one. Part of the durable delivery, so repair replays it.
func (s *Service) scheduleExpiryFor(ctx context.Context, intent *postgres.MessageDeliveryIntent) error {
	e := decodeExtra(string(intent.Extra))
	if e == nil || e.ExpiresAt == nil {
		return nil
	}
	ds, ok := s.disappearing()
	if !ok {
		return nil
	}
	return ds.ScheduleMessageExpiry(ctx, postgres.MessageExpiry{
		MessageID:      intent.MessageID,
		ConversationID: intent.ConversationID,
		Bucket:         intent.Bucket,
		MessageTS:      intent.MessageTS,
		SenderID:       intent.SenderID,
		ExpiresAt:      *e.ExpiresAt,
	})
}

// DisappearingTimer is the timer state the settings routes return.
type DisappearingTimer struct {
	ConversationID   uuid.UUID  `json:"conversation_id"`
	DisappearAfterMs int64      `json:"disappear_after_ms"`
	UpdatedBy        *uuid.UUID `json:"disappear_updated_by,omitempty"`
	UpdatedAt        *time.Time `json:"disappear_updated_at,omitempty"`
}

// GetDisappearingTimer returns the conversation-wide timer (0 = off).
func (s *Service) GetDisappearingTimer(ctx context.Context, conversationID uuid.UUID) (*DisappearingTimer, error) {
	out := &DisappearingTimer{ConversationID: conversationID}
	ds, ok := s.disappearing()
	if !ok {
		return out, nil
	}
	t, err := ds.GetDisappearTimer(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if t.AfterMs != nil {
		out.DisappearAfterMs = *t.AfterMs
	}
	out.UpdatedBy, out.UpdatedAt = t.UpdatedBy, t.UpdatedAt
	return out, nil
}

// SetDisappearingTimer sets the conversation-wide timer for every member. A
// DM's members may both set it; in a group only the owner or an admin (the
// same rule as editing group info). Messages already sent keep their expiry.
// Every member and the room hear `disappearing_timer_updated`.
func (s *Service) SetDisappearingTimer(ctx context.Context, actorID, conversationID uuid.UUID, afterMs int64) (*DisappearingTimer, error) {
	if !ValidDisappearAfterMs(afterMs) {
		return nil, ErrInvalidDisappearTimer
	}
	ok, err := s.convStore.CheckMembership(ctx, conversationID, actorID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotMember
	}
	conv, err := s.convStore.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if conv == nil {
		return nil, notFound("conversation not found")
	}
	if conv.Type != "direct" {
		if _, _, err := s.requireGroupRole(ctx, conversationID, actorID, "owner", "admin"); err != nil {
			return nil, err
		}
	}
	ds, ok := s.disappearing()
	if !ok {
		return nil, errors.New("disappearing messages are not available")
	}
	current, err := s.GetDisappearingTimer(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if current.DisappearAfterMs == afterMs {
		return current, nil // unchanged: no write, no frame
	}
	var stored *int64
	if afterMs != DisappearOff {
		v := afterMs
		stored = &v
	}
	if err := ds.SetDisappearTimer(ctx, conversationID, stored, actorID); err != nil {
		return nil, err
	}
	updated, err := s.GetDisappearingTimer(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	if members, err := s.convStore.GetMembers(ctx, conversationID); err == nil {
		body := map[string]interface{}{
			"conversation_id":    conversationID,
			"disappear_after_ms": updated.DisappearAfterMs,
			"updated_by":         actorID,
		}
		if updated.UpdatedAt != nil {
			body["updated_at"] = updated.UpdatedAt
		}
		s.publishToConversation(conversationID, members, "disappearing_timer_updated", body)
	}
	return updated, nil
}

// --- Expiry worker ---

const (
	// DefaultMessageExpiryPollInterval and DefaultMessageExpiryBatch are the
	// worker defaults (config: DISAPPEARING_MESSAGES_POLL_INTERVAL /
	// DISAPPEARING_MESSAGES_BATCH).
	DefaultMessageExpiryPollInterval = 30 * time.Second
	DefaultMessageExpiryBatch        = 100
	// messageExpiryLease is how long a claim keeps other replicas off a row.
	messageExpiryLease = 2 * time.Minute
)

// messageExpiredPayload is MessageDeleted plus why (additive; consumers that
// only read message_id / conversation_id are unaffected).
type messageExpiredPayload struct {
	sharedEvents.MessageDeletedPayload
	Reason string `json:"reason"`
}

// StartMessageExpiryWorker deletes due disappearing messages until ctx ends.
// Safe with several replicas (claims use FOR UPDATE SKIP LOCKED + a lease).
func (s *Service) StartMessageExpiryWorker(ctx context.Context, interval time.Duration, batch int) {
	if interval <= 0 {
		interval = DefaultMessageExpiryPollInterval
	}
	if batch <= 0 {
		batch = DefaultMessageExpiryBatch
	}
	if _, ok := s.disappearing(); !ok {
		s.log.Warn("disappearing-message expiry worker not started: store lacks the expiry table")
		return
	}
	s.log.Info("disappearing-message expiry worker started", "interval", interval.String(), "batch", batch)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := s.runMessageExpiryPass(ctx, batch); err != nil {
				s.log.Error("message expiry pass failed", "err", err)
			} else if n > 0 {
				s.log.Info("disappearing messages expired", "count", n)
			}
		}
	}
}

// runMessageExpiryPass claims at most batch due rows and expires each one.
// It returns how many were fully expired.
func (s *Service) runMessageExpiryPass(ctx context.Context, batch int) (int, error) {
	ds, ok := s.disappearing()
	if !ok {
		return 0, nil
	}
	due, err := ds.ClaimDueMessageExpiries(ctx, batch, messageExpiryLease)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, e := range due {
		if err := s.expireMessage(ctx, e); err != nil {
			if !errors.Is(err, postgres.ErrExpiryDeliveryPending) {
				s.log.Warn("message expiry deferred", "err", err, "message_id", e.MessageID,
					"conversation_id", e.ConversationID, "attempts", e.AttemptCount)
			}
			if derr := ds.DeferMessageExpiry(ctx, e.MessageID, previewRepairBackoff(e.AttemptCount), err.Error()); derr != nil {
				// The claim lease is the fallback backoff.
				s.log.Warn("message expiry defer failed", "err", derr, "message_id", e.MessageID)
			}
			continue
		}
		if err := ds.CompleteMessageExpiry(ctx, e.MessageID); err != nil {
			// Re-claimed after the lease; every step is idempotent.
			s.log.Warn("message expiry completion failed; will retry", "err", err, "message_id", e.MessageID)
			continue
		}
		done++
	}
	return done, nil
}

// expireMessage deletes one due message for everyone. Every step is safe to
// repeat: the Postgres side runs once (guarded by deleted_at), and the
// Scylla redaction, preview repair and frame are idempotent.
func (s *Service) expireMessage(ctx context.Context, e postgres.MessageExpiry) error {
	ds, ok := s.disappearing()
	if !ok {
		return errors.New("disappearing messages are not available")
	}
	msg, err := s.msgStore.GetMessage(ctx, e.ConversationID, e.Bucket, e.MessageTS, e.MessageID)
	if err != nil {
		return fmt.Errorf("read message: %w", err)
	}
	retry := e.DeletedAt != nil
	// Unsent by a member before it expired (or never landed): unsend already
	// released the unread counts and told everyone, so only the content
	// wipe remains.
	alreadyGone := !retry && (msg == nil || msg.IsDeleted)

	now := s.clock()
	payload := messageExpiredPayload{
		MessageDeletedPayload: sharedEvents.MessageDeletedPayload{
			MessageID:      e.MessageID.String(),
			ConversationID: e.ConversationID.String(),
			// The author's own timer removed it.
			DeletedBy: e.SenderID.String(),
			DeletedAt: now,
		},
		Reason: "expired",
	}
	memberIDs, err := ds.ExpireMessageRecords(ctx, e, alreadyGone, sharedEvents.MessageDeleted, payload)
	if err != nil {
		return err
	}
	if msg == nil {
		return nil
	}

	if !alreadyGone {
		// MP-LB-1 write-ahead: the preview obligation precedes the Scylla
		// mutation, exactly as on unsend.
		if err := s.previewRepairStore().CreatePreviewRepairObligation(ctx, e.ConversationID, e.MessageID, e.Bucket, msg.Ts); err != nil {
			return fmt.Errorf("record preview repair obligation: %w", err)
		}
	}
	if r, ok := s.msgStore.(messageRedactor); ok {
		err = r.RedactMessage(ctx, e.ConversationID, e.Bucket, e.MessageTS, e.MessageID)
	} else {
		err = s.msgStore.SoftDeleteMessage(ctx, e.ConversationID, e.Bucket, e.MessageTS, e.MessageID)
	}
	if err != nil {
		return fmt.Errorf("redact message: %w", err)
	}
	if c, ok := s.msgStore.(inboxProjectionCleaner); ok && len(memberIDs) > 0 {
		if err := c.DeleteInboxProjection(ctx, memberIDs, e.ConversationID, e.MessageTS); err != nil {
			return fmt.Errorf("drop inbox projection: %w", err)
		}
	}
	if s.rdb != nil {
		s.rdb.Del(ctx, fmt.Sprintf("chat_messages:%s", e.ConversationID))
	}
	if alreadyGone {
		return nil
	}

	obligation := postgres.PreviewRepairObligation{
		MessageID: e.MessageID, ConversationID: e.ConversationID,
		Bucket: e.Bucket, DeletedTs: msg.Ts, CreatedAt: now,
	}
	if outcome, rerr := s.resolvePreviewRepair(ctx, obligation); outcome == previewRepairResolved {
		if cerr := s.previewRepairStore().CompletePreviewRepairObligation(ctx, e.MessageID); cerr != nil {
			s.log.Warn("preview repair completion failed; worker will retire it", "err", cerr, "message_id", e.MessageID)
		}
	} else if rerr != nil {
		s.log.Warn("expiry preview repair deferred to worker", "err", rerr, "message_id", e.MessageID)
	}

	if members, err := s.convStore.GetMembers(ctx, e.ConversationID); err == nil {
		s.publishToConversation(e.ConversationID, members, "message_deleted", map[string]interface{}{
			"conversation_id": e.ConversationID,
			"message_id":      e.MessageID,
			"reason":          "expired",
		})
	}
	return nil
}
