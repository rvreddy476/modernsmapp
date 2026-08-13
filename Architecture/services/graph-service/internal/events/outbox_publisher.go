package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/atpost/graph-service/internal/store"
	sharedevents "github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

// Module 3 SR-2 / LB-2 — the bridge from the durable outbox to Kafka.
//
// The relay in internal/store does not know about Kafka; it knows how to lease
// a row, hand it to a publisher, and mark it published only on success. This
// adapter is that publisher.
//
// LB-2: it now emits the CANONICAL events.EventEnvelope. The first version
// invented its own `outboxEnvelope` shape with different field names, so even
// after the event type was corrected, every consumer would have failed to find
// a payload where it expected one. A durable delivery path that speaks a
// private dialect delivers nothing.
//
// Two details matter for consumers:
//
//   - The partition key is the CANONICAL pair, so every event about one pair
//     lands on one partition and therefore arrives in order. Keying by actor
//     would split a block from its matching unblock and let a consumer apply
//     them out of order.
//   - EventID is the OUTBOX ROW ID, not a fresh uuid per publish. That is what
//     makes at-least-once delivery safe: a redelivery after a crash carries
//     the same EventID, so a consumer can recognise it. The pair sequence
//     travels in the payload envelope extension for the same reason.

// OutboxKafkaPublisher publishes graph outbox events to Kafka.
type OutboxKafkaPublisher struct {
	writer *kafka.Writer
}

// NewOutboxPublisher reuses the producer's writer so there is one connection
// pool and one set of broker settings.
func NewOutboxPublisher(p *Producer) *OutboxKafkaPublisher {
	return &OutboxKafkaPublisher{writer: p.writer}
}

// GraphEventEnvelope is the canonical envelope plus the per-pair sequence.
//
// It embeds events.EventEnvelope so the wire format is byte-compatible with
// what every existing consumer already unmarshals; PairSeq is an additive
// field that consumers may use for ordering and dedupe and that older
// consumers simply ignore.
type GraphEventEnvelope struct {
	sharedevents.EventEnvelope
	// PairSeq is monotonic per canonical (lo,hi) pair. Together with EventID
	// it gives a consumer two independent ways to recognise a replay: the same
	// row id, or a sequence it has already applied for that pair.
	PairSeq int64 `json:"pair_seq"`
}

func (p *OutboxKafkaPublisher) PublishGraphEvent(ctx context.Context, ev store.OutboxEvent) error {
	actor := ev.ActorID.String()

	// NewEnvelope generates a fresh EventID; overwrite it with the outbox row
	// id so a redelivery is identifiable as the SAME event. A per-publish uuid
	// would make every retry look like a new event, which is precisely what
	// makes at-least-once unsafe.
	env := sharedevents.NewEnvelope(ctx, ev.EventType, &actor, ev.Payload)
	env.EventID = ev.ID.String()

	body, err := json.Marshal(GraphEventEnvelope{EventEnvelope: env, PairSeq: ev.PairSeq})
	if err != nil {
		return fmt.Errorf("marshal graph event envelope: %w", err)
	}

	// Canonical pair key: both directions of a pair share one partition, so
	// block and unblock for the same two users cannot be reordered.
	lo, hi := ev.ActorID, ev.TargetID
	if lo.String() > hi.String() {
		lo, hi = hi, lo
	}
	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(lo.String() + ":" + hi.String()),
		Value: body,
	})
}

var _ store.OutboxPublisher = (*OutboxKafkaPublisher)(nil)
