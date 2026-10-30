package outbox

import (
	"context"
	"log/slog"
	"strings"
	"time"

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
	// LeaderAcksOnly downgrades the producer to RequireOne.
	//
	// Commerce P0 amendment A3 / review R-2. The default is now RequireAll,
	// and no money topic may set this. Under RequireOne a leader can
	// acknowledge a write, lose power before any replica has copied it, and
	// this publisher will still mark the outbox row published — destroying
	// the only durable record that a payment was captured. Consumer
	// idempotency absorbs a duplicated event; nothing absorbs an event that
	// no longer exists anywhere.
	//
	// The flag exists so a non-money, high-volume topic can opt into the
	// weaker guarantee deliberately and visibly, not so the platform can
	// drift back into it silently.
	LeaderAcksOnly bool
	// ClaimLease is how long a claimed row is reserved for the relay that
	// claimed it. Default: 30s.
	ClaimLease time.Duration
	// MaxAttempts is how many counted failures (the broker refuses the
	// record) a row gets before it is dead-lettered. Default: 10.
	MaxAttempts int
}

// Publisher polls outbox_events and publishes to Kafka.
//
// Commerce backend audit M4 (30 Oct 2026). Every replica used to read the
// same unpublished rows with a plain SELECT and publish them, so with two
// replicas each event went out twice; and the LeastBytes balancer ignored
// the partition key, so two events of one order could land on different
// partitions and be consumed out of order. The relay (relay.go) now:
//
//   - runs a pass only on the replica holding a per-table advisory lock, so
//     the events of one key reach Kafka in id order across replicas;
//   - claims rows with a lease (FOR UPDATE SKIP LOCKED), so even two relays
//     never publish the same row while the lease runs;
//   - publishes each claimed batch in one WriteMessages call with the Hash
//     balancer keyed by partition_key and a short BatchTimeout;
//   - stamps each message with a stable event id (header "event_id");
//   - dead-letters a row that can never be published (failed_at), and ends
//     the pass on a broker outage so the next pass retries in order.
//
// The claim columns come from a per-service migration. Against a table that
// does not have them yet the publisher runs in legacy mode: same lock, same
// batch and balancer, plain SELECT, no dead letters.
type Publisher struct {
	db      *pgxpool.Pool
	cfg     Config
	table   string
	lockKey int64
	store   relayStore
}

// New creates a Publisher ready to Run.
func New(db *pgxpool.Pool, cfg Config) *Publisher {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 500 * time.Millisecond
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 100
	}
	if cfg.ClaimLease <= 0 {
		cfg.ClaimLease = defaultClaimLease
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	table := "outbox_events"
	if cfg.DBSchema != "" {
		table = cfg.DBSchema + ".outbox_events"
	}
	p := &Publisher{db: db, cfg: cfg, table: table, lockKey: relayLockKey(table)}
	if db != nil {
		p.store = newPGStore(db, cfg.DBSchema, table, p.lockKey)
	}
	return p
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
	if p.store == nil {
		slog.Error("outbox publisher has no database", "table", p.table)
		return
	}
	if p.cfg.LeaderAcksOnly {
		slog.Warn("outbox publisher running with leader-only acks; an unreplicated leader failure will lose events",
			"table", p.table, "topic", p.cfg.DefaultTopic)
	}
	writer := kafka.NewWriter(writerConfig(brokers, p.cfg, dialer))
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
			p.pass(ctx, writer)
		}
	}
}

// writerConfig is the Kafka writer the relay publishes through.
func writerConfig(brokers []string, cfg Config, dialer *kafka.Dialer) kafka.WriterConfig {
	// A3: RequireAll by default. The publisher marks a row published as
	// soon as WriteMessages returns, so the ack it waits for IS the
	// durability guarantee for that event. Waiting only for the leader
	// makes "published" a lie the moment that leader fails.
	acks := kafka.RequireAll
	if cfg.LeaderAcksOnly {
		acks = kafka.RequireOne
	}
	batch := cfg.BatchSize
	if batch <= 0 {
		batch = 100
	}
	return kafka.WriterConfig{
		Brokers: brokers,
		Topic:   cfg.DefaultTopic,
		// M4: Hash, not LeastBytes. LeastBytes ignores Message.Key, so the
		// partition_key the services set (order id, intent id) did nothing
		// and one aggregate's events could be consumed out of order. Hash
		// (FNV-1a, Sarama-compatible) sends one key to one partition; a nil
		// key still round-robins.
		Balancer: &kafka.Hash{},
		// kafka-go's default BatchTimeout is 1 s and a synchronous
		// WriteMessages waits for it. The relay hands a whole claimed batch
		// to one call; the short timeout bounds how long a partly filled
		// partition batch waits.
		BatchTimeout: writerBatchTimeout,
		BatchSize:    batch,
		BatchBytes:   MaxMessageBytes,
		WriteTimeout: 10 * time.Second,
		RequiredAcks: int(acks),
		Dialer:       dialer,
	}
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
