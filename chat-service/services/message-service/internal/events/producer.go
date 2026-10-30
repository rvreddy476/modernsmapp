package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Writer tuning for the outbox relay (audit H3, 30 Oct 2026).
//
// kafka-go's default BatchTimeout is 1 s and a synchronous WriteMessages
// waits for it, so a relay that published one event per call managed about
// one event per second. The relay now hands a whole claimed batch to one
// WriteMessages call, and the short timeout bounds how long a partially
// filled partition batch waits.
const (
	relayBatchTimeout = 10 * time.Millisecond
	relayBatchSize    = 200
	// MaxMessageBytes is the largest envelope (key + value + record
	// overhead) the writer sends: kafka-go's default BatchBytes and the
	// broker's default message.max.bytes. An outbox row whose envelope is
	// larger can never be published.
	MaxMessageBytes = 1 << 20
	// recordOverhead approximates kafka-go's per-record framing (headers,
	// varints, timestamps) so the pre-check refuses a message before
	// WriteMessages does.
	recordOverhead = 128
)

// ErrMessageTooLarge marks an event that can never be published because its
// envelope exceeds MaxMessageBytes. The relay dead-letters such a row at once
// instead of retrying it forever (audit H4).
var ErrMessageTooLarge = errors.New("event exceeds the Kafka message size limit")

// outboxEventNamespace derives stable event ids from outbox rows.
var outboxEventNamespace = uuid.MustParse("8f5e2c1a-6b9d-4c47-9a0e-3d1f7b2c5e60")

// OutboxEventID is the envelope event_id for an outbox row: a UUIDv5 of the
// row's dedupe key (or "outbox:<id>" for a legacy row without one). The same
// row always publishes with the same id, so a consumer can de-duplicate a
// re-publish (a relay crash between WriteMessages and the published mark).
func OutboxEventID(rowID int64, dedupeKey string) string {
	name := dedupeKey
	if name == "" {
		name = "outbox:" + strconv.FormatInt(rowID, 10)
	}
	return uuid.NewSHA1(outboxEventNamespace, []byte(name)).String()
}

// OutboxMessage is one outbox row ready for Kafka.
type OutboxMessage struct {
	RowID      int64
	DedupeKey  string
	EventType  string
	Key        string // partition key: the conversation id (or "" for round robin)
	OccurredAt time.Time
	Payload    json.RawMessage
}

type Producer struct {
	writer   *kafka.Writer
	maxBytes int
}

func NewProducer(brokers []string, topic string) *Producer {
	return NewProducerWithDialer(brokers, topic, nil)
}

func NewProducerWithDialer(brokers []string, topic string, dialer *kafka.Dialer) *Producer {
	return &Producer{
		writer: kafka.NewWriter(kafka.WriterConfig{
			Brokers: brokers,
			Topic:   topic,
			// Hash, not LeastBytes: LeastBytes ignores the key, so the
			// events of one conversation (created, edited, deleted) landed
			// on different partitions and could be consumed out of order.
			// A nil/empty key still round-robins.
			Balancer:     &kafka.Hash{},
			BatchTimeout: relayBatchTimeout,
			BatchSize:    relayBatchSize,
			BatchBytes:   MaxMessageBytes,
			WriteTimeout: 10 * time.Second,
			Dialer:       dialer,
		}),
		maxBytes: MaxMessageBytes,
	}
}

func (p *Producer) PublishConversationCreated(ctx context.Context, payload sharedEvents.ConversationCreatedPayload) error {
	return p.publish(ctx, sharedEvents.ConversationCreated, &payload.CreatedBy, payload.ConversationID, payload)
}

func (p *Producer) PublishMessageCreated(ctx context.Context, payload sharedEvents.MessageCreatedPayload) error {
	return p.publish(ctx, sharedEvents.MessageCreated, &payload.SenderID, payload.ConversationID, payload)
}

func (p *Producer) PublishMessageDeleted(ctx context.Context, payload sharedEvents.MessageDeletedPayload) error {
	return p.publish(ctx, sharedEvents.MessageDeleted, &payload.DeletedBy, payload.ConversationID, payload)
}

func (p *Producer) PublishRaw(ctx context.Context, eventType string, partitionKey string, payloadBytes json.RawMessage) error {
	actorStr := ""
	envelope := sharedEvents.EventEnvelope{
		EventID:     uuid.New().String(),
		EventType:   eventType,
		OccurredAt:  time.Now(),
		ActorUserID: &actorStr,
		Payload:     payloadBytes,
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}
	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   keyBytes(partitionKey),
		Value: envelopeBytes,
	})
}

// PublishOutbox publishes a batch of outbox rows in one WriteMessages call
// and returns one error per message (nil when that message was written).
// Each envelope's event_id is OutboxEventID of its row and occurred_at is the
// row's time, so a re-publish is byte-for-byte the same event. A message too
// large to ever publish gets ErrMessageTooLarge and is not sent; the rest of
// the batch still is.
func (p *Producer) PublishOutbox(ctx context.Context, msgs []OutboxMessage) []error {
	errs := make([]error, len(msgs))
	if len(msgs) == 0 {
		return errs
	}
	kmsgs, index := p.buildOutboxBatch(msgs, errs)
	if len(kmsgs) == 0 {
		return errs
	}
	mapWriteError(p.writer.WriteMessages(ctx, kmsgs...), kmsgs, index, errs)
	return errs
}

// buildOutboxBatch marshals the envelopes, records a permanent error for the
// ones that cannot be sent, and returns the sendable messages with the index
// of each one in msgs.
func (p *Producer) buildOutboxBatch(msgs []OutboxMessage, errs []error) ([]kafka.Message, []int) {
	maxBytes := p.maxBytes
	if maxBytes <= 0 {
		maxBytes = MaxMessageBytes
	}
	kmsgs := make([]kafka.Message, 0, len(msgs))
	index := make([]int, 0, len(msgs))
	actor := ""
	for i, m := range msgs {
		envelope := sharedEvents.EventEnvelope{
			EventID:     OutboxEventID(m.RowID, m.DedupeKey),
			EventType:   m.EventType,
			OccurredAt:  m.OccurredAt,
			ActorUserID: &actor,
			Payload:     m.Payload,
		}
		value, err := json.Marshal(envelope)
		if err != nil {
			errs[i] = fmt.Errorf("marshal envelope: %w", err)
			continue
		}
		if len(value)+len(m.Key)+recordOverhead > maxBytes {
			errs[i] = fmt.Errorf("%w: %d bytes", ErrMessageTooLarge, len(value)+len(m.Key))
			continue
		}
		kmsgs = append(kmsgs, kafka.Message{Key: keyBytes(m.Key), Value: value})
		index = append(index, i)
	}
	return kmsgs, index
}

// mapWriteError spreads a WriteMessages error over the per-message errors.
func mapWriteError(err error, sent []kafka.Message, index []int, errs []error) {
	if err == nil {
		return
	}
	var werrs kafka.WriteErrors
	if errors.As(err, &werrs) && len(werrs) == len(sent) {
		for j, e := range werrs {
			errs[index[j]] = e
		}
		return
	}
	var tooLarge kafka.MessageTooLargeError
	if errors.As(err, &tooLarge) {
		// kafka-go refused the whole call because of one message; nothing
		// was written. That one is permanent, the rest retry.
		for j := range sent {
			if sameBytes(sent[j].Value, tooLarge.Message.Value) {
				errs[index[j]] = fmt.Errorf("%w: %v", ErrMessageTooLarge, err)
			} else {
				errs[index[j]] = err
			}
		}
		return
	}
	for _, i := range index {
		errs[i] = err
	}
}

func sameBytes(a, b []byte) bool {
	return len(a) > 0 && len(a) == len(b) && &a[0] == &b[0]
}

func keyBytes(key string) []byte {
	if key == "" {
		return nil
	}
	return []byte(key)
}

func (p *Producer) publish(ctx context.Context, eventType string, actorID *string, partitionKey string, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	envelope := sharedEvents.EventEnvelope{
		EventID:     uuid.New().String(),
		EventType:   eventType,
		OccurredAt:  time.Now(),
		ActorUserID: actorID,
		Payload:     payloadBytes,
	}

	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   keyBytes(partitionKey),
		Value: envelopeBytes,
	})
}

func (p *Producer) Close() error {
	return p.writer.Close()
}

// IsRecordError reports whether a publish error is about the record itself
// (the broker refused this data) rather than about reaching Kafka. The relay
// counts such failures toward dead-lettering the row; connection and
// availability errors are only retried, so a Kafka outage never
// dead-letters anything.
func IsRecordError(err error) bool {
	for _, code := range []kafka.Error{
		kafka.MessageSizeTooLarge,
		kafka.InvalidMessage,
		kafka.InvalidMessageSize,
		kafka.InvalidRecord,
		kafka.InvalidTimestamp,
		kafka.RecordListTooLarge,
	} {
		if errors.Is(err, code) {
			return true
		}
	}
	return false
}
