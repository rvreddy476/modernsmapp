// Package outbox relays trust.enforcement_outbox to Kafka (Copyright Match
// plan section 6.4, P-7).
//
// Rows are published in id order and marked published afterwards, so a
// crash between the two duplicates an event (same event_id) and never loses
// one. A failing row blocks the rows behind it (ordering per user matters:
// StrikeIssued before StrikeVoided) until it has failed MaxOrderedAttempts
// times; from then on it is retried with exponential backoff and no longer
// holds the queue, so one poisoned row cannot stall every other event
// forever. It is never dropped.
package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/shared/transport"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/segmentio/kafka-go"
)

// Store is the dispatcher's view of the outbox (postgres.OutboxStore).
type Store interface {
	PendingOutbox(ctx context.Context, limit int) ([]postgres.OutboxEvent, error)
	MarkOutboxPublished(ctx context.Context, id int64) error
	RecordOutboxFailure(ctx context.Context, id int64, cause error, retryAfter time.Duration) error
}

// Message is one Kafka record: the row's payload as the value, keyed by the
// row's partition key, on the row's topic.
type Message struct {
	Topic     string
	Key       string
	Value     []byte
	EventType string
	EventID   string
}

// Publisher writes one message durably (acked by every in-sync replica).
type Publisher interface {
	Publish(ctx context.Context, msg Message) error
}

// Dispatcher polls the outbox and publishes.
type Dispatcher struct {
	store Store
	pub   Publisher
	log   *slog.Logger
	// Interval between sweeps.
	Interval time.Duration
	// BatchSize is the most rows one sweep reads.
	BatchSize int
	// MaxOrderedAttempts is how many times a row may fail before the sweep
	// continues past it (it is still retried, with backoff).
	MaxOrderedAttempts int
	// MaxBackoff caps the retry delay of a row that has stopped blocking.
	MaxBackoff time.Duration
}

// New builds a dispatcher with the defaults: 1 s sweeps, 100 rows, 5
// ordered attempts, 5 min maximum backoff.
func New(store Store, pub Publisher, log *slog.Logger) *Dispatcher {
	if log == nil {
		log = slog.Default()
	}
	return &Dispatcher{store: store, pub: pub, log: log, Interval: time.Second, BatchSize: 100, MaxOrderedAttempts: 5, MaxBackoff: 5 * time.Minute}
}

// Run sweeps until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	d.log.Info("enforcement outbox dispatcher started", "interval", d.Interval)
	ticker := time.NewTicker(d.Interval)
	defer ticker.Stop()
	for {
		if _, err := d.Sweep(ctx); err != nil && ctx.Err() == nil {
			d.log.Error("enforcement outbox sweep failed", "error", err)
		}
		select {
		case <-ctx.Done():
			d.log.Info("enforcement outbox dispatcher stopped")
			return
		case <-ticker.C:
		}
	}
}

// Sweep publishes one batch. It returns how many rows it published; the
// error is a store failure (a publish failure is recorded on the row).
func (d *Dispatcher) Sweep(ctx context.Context) (int, error) {
	rows, err := d.store.PendingOutbox(ctx, d.BatchSize)
	if err != nil {
		return 0, err
	}
	published := 0
	for _, row := range rows {
		msg := Message{Topic: row.Topic, Key: row.PartitionKey, Value: row.Payload, EventType: row.EventType, EventID: row.EventID.String()}
		if err := d.pub.Publish(ctx, msg); err != nil {
			attempts := row.Attempts + 1
			blocking := attempts < d.MaxOrderedAttempts
			retryAfter := time.Duration(0)
			if !blocking {
				retryAfter = d.backoff(attempts)
			}
			if recErr := d.store.RecordOutboxFailure(ctx, row.ID, err, retryAfter); recErr != nil {
				return published, recErr
			}
			if blocking {
				// Preserve ordering: nothing behind this row moves until it
				// publishes or has failed enough times to stop blocking.
				d.log.Error("enforcement outbox: publish failed; holding the queue",
					"outbox_id", row.ID, "event_type", row.EventType, "attempts", attempts, "error", err)
				return published, nil
			}
			d.log.Error("enforcement outbox: publish failed repeatedly; continuing past the row",
				"outbox_id", row.ID, "event_type", row.EventType, "attempts", attempts, "retry_after", retryAfter, "error", err)
			continue
		}
		if err := d.store.MarkOutboxPublished(ctx, row.ID); err != nil {
			// The event is on the broker. Stop here: marking later
			// re-publishes this same event_id, which consumers dedupe.
			d.log.Error("enforcement outbox: mark published failed; the event will be re-sent",
				"outbox_id", row.ID, "event_id", row.EventID, "error", err)
			return published, err
		}
		published++
	}
	return published, nil
}

// backoff is 2^(attempts-MaxOrderedAttempts) seconds, capped at MaxBackoff.
func (d *Dispatcher) backoff(attempts int) time.Duration {
	exp := attempts - d.MaxOrderedAttempts
	if exp < 0 {
		exp = 0
	}
	if exp > 20 {
		exp = 20
	}
	wait := time.Second << uint(exp)
	if wait > d.MaxBackoff {
		wait = d.MaxBackoff
	}
	return wait
}

// ── Kafka publisher ─────────────────────────────────────────────────────────

// KafkaPublisher writes to whichever topic each message names, with
// RequireAll acks: "published" is only ever said of a replicated write.
type KafkaPublisher struct {
	writer *kafka.Writer
}

// NewKafkaPublisher builds the writer. dialer may be nil.
func NewKafkaPublisher(brokers []string, dialer *kafka.Dialer) (*KafkaPublisher, error) {
	if len(brokers) == 0 {
		return nil, fmt.Errorf("outbox: kafka brokers are required")
	}
	if dialer == nil {
		var err error
		if dialer, err = transport.KafkaDialerFromEnv(); err != nil {
			return nil, err
		}
	}
	return &KafkaPublisher{writer: kafka.NewWriter(kafka.WriterConfig{
		Brokers:      brokers,
		Balancer:     &kafka.Hash{},
		RequiredAcks: int(kafka.RequireAll),
		WriteTimeout: 10 * time.Second,
		Dialer:       dialer,
	})}, nil
}

// Publish writes one record to msg.Topic.
func (p *KafkaPublisher) Publish(ctx context.Context, msg Message) error {
	return p.writer.WriteMessages(ctx, kafka.Message{
		Topic: msg.Topic,
		Key:   []byte(msg.Key),
		Value: msg.Value,
		Headers: []kafka.Header{
			{Key: "event_type", Value: []byte(msg.EventType)},
			{Key: "event_id", Value: []byte(msg.EventID)},
		},
	})
}

// Close releases the writer.
func (p *KafkaPublisher) Close() error { return p.writer.Close() }
