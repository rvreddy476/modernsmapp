package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/shared/events"
	"github.com/atpost/shared/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Chat-closure events through the transactional outbox (audit H2) ─────────
//
// chat-service's message-service closes a dating conversation only when it
// consumes dating.match.closed (by match id) or dating.user.blocked (by
// pair). Those events used to be published with kafka-go on the request
// context AFTER the closure committed, so a dropped connection or a broker
// hiccup left the chat open for good. Every closure now writes its events to
// dating.outbox_events in the SAME transaction as the state change, and
// shared/outbox.Publisher (started in cmd/server) relays them: the event
// exists if and only if the closure committed.
//
// The row payload is the whole shared envelope, so the Kafka value is
// byte-for-byte the shape the direct producer wrote and every consumer
// (message-service, notification-service) decodes it unchanged.

// OutboxSchema is the schema of dating-service's outbox table. dating shares
// the app database, whose public.outbox_events belongs to another
// declaration (the food/doorstep reason for a schema-local table).
const OutboxSchema = "dating"

// OutboxTable is the table as shared/outbox names it.
const OutboxTable = OutboxSchema + ".outbox_events"

// Close reasons for closures no participant asked for. Past matches render
// them as "closed" (endedHow), so the other person never learns why.
const (
	// CloseReasonSuspended: a moderator suspended one participant.
	CloseReasonSuspended = "suspended"
	// CloseReasonRestricted: one participant was restricted (a moderator,
	// or an under-18 identity birth date): no adult keeps chatting with
	// someone flagged under 18.
	CloseReasonRestricted = "restricted"
	// CloseReasonDeleted: one participant deleted their profile (self
	// delete or the platform DPDP deletion).
	CloseReasonDeleted = "deleted"
	// CloseReasonRiskHold: risk scoring put one participant on chat_hold,
	// admin_review or suspend.
	CloseReasonRiskHold = "risk_hold"
)

// MatchClosedEvent is the dating.match.closed payload (events.MatchClosedPayload).
type MatchClosedEvent struct {
	MatchID  string    `json:"match_id"`
	ClosedBy string    `json:"closed_by"`
	UserA    string    `json:"user_a"`
	UserB    string    `json:"user_b"`
	ClosedAt time.Time `json:"closed_at"`
	// Reason is close_reason; consumers that predate it ignore it.
	Reason string `json:"reason,omitempty"`
}

// UserBlockedEvent is the dating.user.blocked payload (events.UserBlockedPayload).
type UserBlockedEvent struct {
	BlockerID string    `json:"blocker_id"`
	BlockedID string    `json:"blocked_id"`
	BlockedAt time.Time `json:"blocked_at"`
}

// outboxWriter is the transactional insert (*outbox.Queuer).
type outboxWriter interface {
	Enqueue(ctx context.Context, tx pgx.Tx, eventType, partitionKey string, payload []byte) error
}

var datingOutbox outboxWriter = outbox.NewQueuer(OutboxSchema)

// EncodeOutboxEnvelope wraps payload in the shared envelope the dating
// producer has always written, with actor as the envelope actor.
func EncodeOutboxEnvelope(ctx context.Context, eventType string, actor *uuid.UUID, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal %s payload: %w", eventType, err)
	}
	var actorStr *string
	if actor != nil {
		s := actor.String()
		actorStr = &s
	}
	env, err := json.Marshal(events.NewEnvelope(ctx, eventType, actorStr, body))
	if err != nil {
		return nil, fmt.Errorf("marshal %s envelope: %w", eventType, err)
	}
	return env, nil
}

// MatchClosedPartitionKey keys a match's events to one partition.
func MatchClosedPartitionKey(matchID uuid.UUID) string { return "match:" + matchID.String() }

// PairPartitionKey keys a pair's events to one partition, whatever the order.
func PairPartitionKey(x, y uuid.UUID) string {
	a, b := canonicalPair(x, y)
	return "pair:" + a.String() + ":" + b.String()
}

// enqueueMatchClosed writes dating.match.closed for m inside tx.
func enqueueMatchClosed(ctx context.Context, tx pgx.Tx, m *Match, closedBy uuid.UUID, reason string) error {
	payload, err := EncodeOutboxEnvelope(ctx, events.EventDatingMatchClosed, &closedBy, MatchClosedEvent{
		MatchID: m.ID.String(), ClosedBy: closedBy.String(),
		UserA: m.UserA.String(), UserB: m.UserB.String(),
		ClosedAt: time.Now().UTC(), Reason: reason,
	})
	if err != nil {
		return err
	}
	if err := datingOutbox.Enqueue(ctx, tx, events.EventDatingMatchClosed, MatchClosedPartitionKey(m.ID), payload); err != nil {
		return fmt.Errorf("enqueue dating.match.closed for %s: %w", m.ID, err)
	}
	return nil
}

// enqueueUserBlocked writes dating.user.blocked inside tx.
func enqueueUserBlocked(ctx context.Context, tx pgx.Tx, blockerID, blockedID uuid.UUID) error {
	payload, err := EncodeOutboxEnvelope(ctx, events.EventDatingUserBlocked, &blockerID, UserBlockedEvent{
		BlockerID: blockerID.String(), BlockedID: blockedID.String(), BlockedAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	if err := datingOutbox.Enqueue(ctx, tx, events.EventDatingUserBlocked, PairPartitionKey(blockerID, blockedID), payload); err != nil {
		return fmt.Errorf("enqueue dating.user.blocked: %w", err)
	}
	return nil
}

// closeOpenMatchesForUserTx closes every open match userID takes part in
// (closed_by = userID, close_reason = reason) and enqueues one
// dating.match.closed per closed row, all inside tx. Idempotent: a user with
// no open match closes and enqueues nothing, so a redelivered deletion or a
// repeated suspension is a no-op.
func closeOpenMatchesForUserTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, reason string) ([]*Match, error) {
	rows, err := tx.Query(ctx, `
        UPDATE dating_matches
        SET status = 'closed', closed_by = $1, closed_at = now(), close_reason = $2
        WHERE (user_a = $1 OR user_b = $1) AND status IN `+openMatchStatuses+`
        RETURNING `+matchSelectCols, userID, reason)
	if err != nil {
		return nil, fmt.Errorf("close open matches (%s): %w", reason, err)
	}
	closed, err := collectMatches(rows)
	if err != nil {
		return nil, fmt.Errorf("close open matches (%s): %w", reason, err)
	}
	for _, m := range closed {
		if err := enqueueMatchClosed(ctx, tx, m, userID, reason); err != nil {
			return nil, err
		}
	}
	return closed, nil
}

// chatClosureReasonForStatus is the close reason a profile status imposes on
// the user's open matches, or "" when the status leaves them open.
func chatClosureReasonForStatus(status string) string {
	switch status {
	case ProfileStatusSuspended:
		return CloseReasonSuspended
	case ProfileStatusRestricted:
		return CloseReasonRestricted
	case ProfileStatusDeleted:
		return CloseReasonDeleted
	}
	return ""
}

// RiskLevelBlocksChat reports whether a risk level stops the user chatting
// in dating matches (the levels service.ErrRiskBlocked gates sparks on).
func RiskLevelBlocksChat(level string) bool {
	switch level {
	case RiskLevelChatHold, RiskLevelAdminReview, RiskLevelSuspend:
		return true
	}
	return false
}
