package purge

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// PGStore is the Postgres slice of the message-service purge.
type PGStore interface {
	UserConversations(ctx context.Context, userID uuid.UUID) ([]ConversationRef, error)
	PurgeUser(ctx context.Context, userID uuid.UUID) error
	InsertOutboxEvent(ctx context.Context, eventType string, payload interface{}) error
}

// ConversationRef mirrors postgres.ConversationRef without importing it.
type ConversationRef struct {
	ID        uuid.UUID
	CreatedAt time.Time
}

// ScyllaStore is the Scylla slice.
type ScyllaStore interface {
	RedactUserMessages(ctx context.Context, convID, userID uuid.UUID, since time.Time) error
	DeleteUserInbox(ctx context.Context, userID uuid.UUID, since time.Time) error
}

// Eraser composes both stores: Scylla first (idempotent redactions), then the
// single Postgres transaction. Satisfies the Eraser interface.
type StoreEraser struct {
	pg PGStore
	sc ScyllaStore
	// inboxLookback bounds the conversations_by_user bucket scan when the
	// user has no memberships left to derive a lower bound from.
	inboxLookback time.Duration
}

// NewEraser builds the composite eraser.
func NewEraser(pg PGStore, sc ScyllaStore) *StoreEraser {
	return &StoreEraser{pg: pg, sc: sc, inboxLookback: 24 * 30 * 24 * time.Hour}
}

// PurgeUser implements the message-service erase.
func (e *StoreEraser) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	convs, err := e.pg.UserConversations(ctx, userID)
	if err != nil {
		return fmt.Errorf("list conversations: %w", err)
	}
	earliest := time.Now().UTC().Add(-e.inboxLookback)
	for _, c := range convs {
		if c.CreatedAt.Before(earliest) {
			earliest = c.CreatedAt
		}
		if err := e.sc.RedactUserMessages(ctx, c.ID, userID, c.CreatedAt); err != nil {
			return err
		}
	}
	if err := e.sc.DeleteUserInbox(ctx, userID, earliest); err != nil {
		return err
	}
	return e.pg.PurgeUser(ctx, userID)
}

// OutboxAckPublisher writes the ack into chat.outbox_events; the service's
// relay routes user.purge_acked rows to the purge-acks producer.
type OutboxAckPublisher struct{ pg PGStore }

// NewOutboxAckPublisher builds the adapter.
func NewOutboxAckPublisher(pg PGStore) *OutboxAckPublisher { return &OutboxAckPublisher{pg: pg} }

// PublishPurgeAck enqueues the bare ack JSON as the outbox payload.
func (p *OutboxAckPublisher) PublishPurgeAck(ctx context.Context, ack Ack) error {
	return p.pg.InsertOutboxEvent(ctx, EventUserPurgeAcked, ack)
}

// AckUserID extracts user_id from an outbox ack payload (relay partition key).
func AckUserID(payload json.RawMessage) string {
	var a Ack
	_ = json.Unmarshal(payload, &a)
	return a.UserID
}
