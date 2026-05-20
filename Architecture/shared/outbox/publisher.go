package outbox

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/shared/o11y/trace"
	"github.com/atpost/shared/transport"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	kafka "github.com/segmentio/kafka-go"
)

// Config configures the outbox publisher.
type Config struct {
	// DBSchema is the Postgres schema prefix (e.g. "visibility", "orders").
	// Leave empty to use the public schema outbox_events table.
	DBSchema string
	// KafkaBrokers comma-separated.
	KafkaBrokers string
	// DefaultTopic is the Kafka topic to publish to.
	DefaultTopic string
	// PollInterval between sweeps. Default: 500ms.
	PollInterval time.Duration
	// BatchSize max events per sweep. Default: 100.
	BatchSize int
}

// Publisher polls outbox_events and publishes to Kafka.
type Publisher struct {
	db    *pgxpool.Pool
	cfg   Config
	table string
}

// New creates a Publisher ready to Run.
func New(db *pgxpool.Pool, cfg Config) *Publisher {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 100
	}
	table := "outbox_events"
	if cfg.DBSchema != "" {
		table = cfg.DBSchema + ".outbox_events"
	}
	return &Publisher{db: db, cfg: cfg, table: table}
}

// Run starts the polling loop; blocks until ctx is cancelled.
func (p *Publisher) Run(ctx context.Context) {
	dialer, err := transport.KafkaDialerFromEnv()
	if err != nil {
		slog.Error("outbox publisher kafka config invalid", "table", p.table, "error", err)
		return
	}
	brokers := splitAndClean(p.cfg.KafkaBrokers)
	if len(brokers) == 0 {
		slog.Error("outbox publisher kafka brokers not configured", "table", p.table)
		return
	}
	writer := kafka.NewWriter(kafka.WriterConfig{
		Brokers:      brokers,
		Topic:        p.cfg.DefaultTopic,
		Balancer:     &kafka.LeastBytes{},
		RequiredAcks: int(kafka.RequireOne),
		Dialer:       dialer,
	})
	defer writer.Close()

	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()

	slog.Info("outbox publisher started", "table", p.table, "topic", p.cfg.DefaultTopic)

	for {
		select {
		case <-ctx.Done():
			slog.Info("outbox publisher stopped", "table", p.table)
			return
		case <-ticker.C:
			if err := p.sweep(ctx, writer); err != nil {
				slog.Error("outbox sweep failed", "table", p.table, "error", err)
			}
		}
	}
}

type outboxRow struct {
	ID           int64
	EventType    string
	PartitionKey string
	Payload      json.RawMessage
}

func (p *Publisher) sweep(ctx context.Context, writer *kafka.Writer) error {
	rows, err := p.db.Query(ctx,
		`SELECT id, event_type, partition_key, payload
		 FROM `+p.table+`
		 WHERE published_at IS NULL
		 ORDER BY id ASC
		 LIMIT $1`,
		p.cfg.BatchSize,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	var events []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.ID, &r.EventType, &r.PartitionKey, &r.Payload); err != nil {
			return err
		}
		events = append(events, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if len(events) == 0 {
		return nil
	}

	msgs := make([]kafka.Message, 0, len(events))
	ids := make([]int64, 0, len(events))
	for _, e := range events {
		// Phase F3.3 — inject W3C trace context into the message
		// headers so consumers can link their child spans back to the
		// transaction that produced the event. We don't have the
		// originating request context here (events are read off the
		// outbox table), so the span will be a fresh top-level one
		// rooted at the publisher; the value is downstream linkage,
		// not a one-trace-end-to-end story.
		headers := []kafka.Header{
			{Key: "event_type", Value: []byte(e.EventType)},
		}
		trace.InjectKafkaHeaders(ctx, &headers)
		msgs = append(msgs, kafka.Message{
			Key:     []byte(e.PartitionKey),
			Value:   e.Payload,
			Headers: headers,
		})
		ids = append(ids, e.ID)
	}

	if err := writer.WriteMessages(ctx, msgs...); err != nil {
		return err
	}

	_, err = p.db.Exec(ctx,
		`UPDATE `+p.table+` SET published_at = NOW() WHERE id = ANY($1)`,
		ids,
	)
	if err != nil {
		slog.Warn("outbox: failed to mark published", "count", len(ids), "error", err)
	}

	slog.Debug("outbox sweep done", "table", p.table, "count", len(ids))
	return nil
}

// Queuer provides transactional outbox insert capability.
type Queuer struct {
	table string
}

// NewQueuer creates a Queuer for transactional use.
func NewQueuer(schemaPrefix string) *Queuer {
	table := "outbox_events"
	if schemaPrefix != "" {
		table = schemaPrefix + ".outbox_events"
	}
	return &Queuer{table: table}
}

// Enqueue inserts an outbox event inside a pgx transaction.
// Call this alongside your domain writes in the same transaction.
func (q *Queuer) Enqueue(ctx context.Context, tx pgx.Tx, eventType, partitionKey string, payload []byte) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO `+q.table+` (event_type, partition_key, payload) VALUES ($1, $2, $3)`,
		eventType, partitionKey, payload,
	)
	return err
}

// EnqueuePool inserts an outbox event using a pool connection (outside a transaction — use Enqueue for transactional inserts).
func (q *Queuer) EnqueuePool(ctx context.Context, db *pgxpool.Pool, eventType, partitionKey string, payload []byte) error {
	_, err := db.Exec(ctx,
		`INSERT INTO `+q.table+` (event_type, partition_key, payload) VALUES ($1, $2, $3)`,
		eventType, partitionKey, payload,
	)
	return err
}

func splitAndClean(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
