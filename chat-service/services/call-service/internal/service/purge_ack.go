package service

import (
	"context"

	"github.com/atpost/chat-call-service/internal/purge"
	"github.com/atpost/chat-call-service/internal/store/postgres"
)

// OutboxAckPublisher writes the purge ack into calls.outbox_events; the
// OutboxRelay delivers it to the purge-acks topic. Satisfies
// purge.AckPublisher and is called only after the erase committed.
type OutboxAckPublisher struct{ store *postgres.CallStore }

// NewOutboxAckPublisher builds the adapter.
func NewOutboxAckPublisher(s *postgres.CallStore) *OutboxAckPublisher {
	return &OutboxAckPublisher{store: s}
}

// PublishPurgeAck enqueues the bare ack JSON.
func (p *OutboxAckPublisher) PublishPurgeAck(ctx context.Context, ack purge.Ack) error {
	return p.store.InsertOutboxEvent(ctx, purge.EventUserPurgeAcked, ack)
}
