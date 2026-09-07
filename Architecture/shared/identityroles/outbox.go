package identityroles

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SchemaSQL returns the idempotent DDL for the intent queue in the given
// schema ("" for public). Each service applies it the way it already applies
// DDL — commerce through a numbered migration, food and rider by appending to
// their bootstrap setup.sql — so this string is the single definition and the
// three copies cannot drift apart in shape.
//
// WHY A PER-SERVICE TABLE RATHER THAN THE EXISTING outbox_events
//
// Every one of the three services already has an outbox_events table and a
// running publisher. It was tempting to reuse it, and it is the wrong tool:
// that publisher's only sink is Kafka, and the delivery we need is an HTTP
// call to identity. Making it work would mean adding a Kafka consumer to
// auth-service, and auth-service's half of this module is already done,
// committed, and explicitly out of scope. A dedicated table with an HTTP
// worker keeps the change on this side of the boundary.
func SchemaSQL(schema string) string {
	table := TableName(schema)
	idx := "identity_role_intents"
	if s := strings.TrimSpace(schema); s != "" {
		idx = s + "_identity_role_intents"
	}
	return fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %[1]s (
    id               BIGSERIAL PRIMARY KEY,
    op               TEXT        NOT NULL CHECK (op IN ('grant','revoke')),
    user_id          UUID        NOT NULL,
    role_name        TEXT        NOT NULL,
    service          TEXT        NOT NULL,
    reason           TEXT        NOT NULL DEFAULT '',
    attempts         INT         NOT NULL DEFAULT 0,
    next_attempt_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error       TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at     TIMESTAMPTZ,
    dead_lettered_at TIMESTAMPTZ
);

-- The worker's only query. Partial so the index stays the size of the backlog
-- rather than the size of history.
CREATE INDEX IF NOT EXISTS idx_%[2]s_pending
    ON %[1]s (next_attempt_at)
    WHERE delivered_at IS NULL AND dead_lettered_at IS NULL;

-- Operator query: "what is stuck and why".
CREATE INDEX IF NOT EXISTS idx_%[2]s_dead
    ON %[1]s (dead_lettered_at)
    WHERE dead_lettered_at IS NOT NULL;
`, table, idx)
}

// TableName returns the qualified table name for a schema prefix.
func TableName(schema string) string {
	if strings.TrimSpace(schema) == "" {
		return "identity_role_intents"
	}
	return schema + ".identity_role_intents"
}

// Outbox is the durable queue of role intents living in the SERVICE's own
// database, next to the domain rows it describes.
//
// The point of putting it there is atomicity: Enqueue takes the caller's
// pgx.Tx, so "the seller row says approved" and "identity will be told" commit
// or roll back together. There is no window in which one is true and the other
// is not. EnqueuePool exists for the several lifecycle writes in these three
// services that are a single bare Exec with no transaction at all (rider's
// entire admin path, food's UpsertDeliveryPartner); its weaker guarantee is
// documented on the method.
type Outbox struct {
	table   string
	service string
}

// NewOutbox builds an Outbox over the table in schema ("" for public).
// service is stamped on every row so an operator reading the queue can see who
// asked, and matches the `service` field identity requires in the audit row.
func NewOutbox(schema, service string) *Outbox {
	if service == "" {
		service = "unknown-service"
	}
	return &Outbox{table: TableName(schema), service: service}
}

// Enqueue records an intent inside the caller's transaction. This is the
// preferred call: it cannot lose an intent, because losing it would mean
// losing the domain write too.
func (o *Outbox) Enqueue(ctx context.Context, tx pgx.Tx, in Intent) error {
	if err := in.validate(); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, o.insertSQL(), string(in.Op), in.UserID, in.Role, o.service, in.Reason)
	return err
}

// EnqueuePool records an intent outside any transaction.
//
// HONEST LIMITATION: the caller has already committed its domain write, so a
// crash between that commit and this insert loses the intent. Nothing in
// process can close that window. What closes it out of process is the
// reconciliation pass — tools/identityrolebackfill compares each service's
// rows against identity and re-enqueues what is missing, and is safe to run on
// a schedule precisely because every operation here is idempotent. Prefer
// Enqueue whenever the caller has a transaction to hand.
func (o *Outbox) EnqueuePool(ctx context.Context, db *pgxpool.Pool, in Intent) error {
	if err := in.validate(); err != nil {
		return err
	}
	_, err := db.Exec(ctx, o.insertSQL(), string(in.Op), in.UserID, in.Role, o.service, in.Reason)
	return err
}

func (o *Outbox) insertSQL() string {
	return `INSERT INTO ` + o.table + ` (op, user_id, role_name, service, reason) VALUES ($1,$2,$3,$4,$5)`
}

// Claim locks up to limit due intents FOR UPDATE SKIP LOCKED and returns them
// with the transaction still open. The caller MUST finish with Settle, which
// commits, or roll the tx back.
//
// SKIP LOCKED is what makes it safe to run a worker in every replica of a
// service without them fighting over the same rows or double-granting.
// Double-granting would in fact be harmless — identity's grant is idempotent —
// but a queue that quietly does N times the work at N replicas is a bug that
// only shows up under load, so it is designed out rather than tolerated.
func (o *Outbox) Claim(ctx context.Context, db *pgxpool.Pool, limit int) (pgx.Tx, []Intent, error) {
	if limit <= 0 {
		limit = 50
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT id, op, user_id::text, role_name, reason, attempts
		  FROM `+o.table+`
		 WHERE delivered_at IS NULL
		   AND dead_lettered_at IS NULL
		   AND next_attempt_at <= NOW()
		 ORDER BY id
		 LIMIT $1
		   FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, err
	}
	var out []Intent
	for rows.Next() {
		var in Intent
		var op string
		if err := rows.Scan(&in.ID, &op, &in.UserID, &in.Role, &in.Reason, &in.Attempts); err != nil {
			rows.Close()
			_ = tx.Rollback(ctx)
			return nil, nil, err
		}
		in.Op = Op(op)
		out = append(out, in)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, err
	}
	return tx, out, nil
}

// MarkDelivered records success inside the claiming transaction.
func (o *Outbox) MarkDelivered(ctx context.Context, tx pgx.Tx, id int64) error {
	_, err := tx.Exec(ctx,
		`UPDATE `+o.table+` SET delivered_at = NOW(), last_error = NULL WHERE id = $1`, id)
	return err
}

// MarkRetry schedules another attempt after delay and records why.
func (o *Outbox) MarkRetry(ctx context.Context, tx pgx.Tx, id int64, delay time.Duration, cause string) error {
	_, err := tx.Exec(ctx,
		`UPDATE `+o.table+`
		    SET attempts = attempts + 1,
		        next_attempt_at = NOW() + $2::interval,
		        last_error = $3
		  WHERE id = $1`,
		id, fmt.Sprintf("%d milliseconds", delay.Milliseconds()), truncate(cause, 2000))
	return err
}

// MarkDead parks the intent for a human. Nothing retries it afterwards; the
// backfill/reconcile tool is what picks it back up, deliberately, once someone
// has fixed the cause.
func (o *Outbox) MarkDead(ctx context.Context, tx pgx.Tx, id int64, cause string) error {
	_, err := tx.Exec(ctx,
		`UPDATE `+o.table+`
		    SET attempts = attempts + 1, dead_lettered_at = NOW(), last_error = $2
		  WHERE id = $1`,
		id, truncate(cause, 2000))
	return err
}

// Stats is the operator's view of the queue.
type Stats struct {
	Pending    int64
	Delivered  int64
	DeadLetter int64
	OldestAge  time.Duration
}

// Stats counts the queue. Cheap enough to expose on an admin endpoint or scrape.
func (o *Outbox) Stats(ctx context.Context, db *pgxpool.Pool) (Stats, error) {
	var s Stats
	var oldestSeconds *float64
	err := db.QueryRow(ctx, `
		SELECT
		  COUNT(*) FILTER (WHERE delivered_at IS NULL AND dead_lettered_at IS NULL),
		  COUNT(*) FILTER (WHERE delivered_at IS NOT NULL),
		  COUNT(*) FILTER (WHERE dead_lettered_at IS NOT NULL),
		  -- FILTER binds to the aggregate (MIN), not to EXTRACT.
		  EXTRACT(EPOCH FROM (NOW() - MIN(created_at)
		    FILTER (WHERE delivered_at IS NULL AND dead_lettered_at IS NULL)))
		  FROM `+o.table).Scan(&s.Pending, &s.Delivered, &s.DeadLetter, &oldestSeconds)
	if err != nil {
		return Stats{}, err
	}
	if oldestSeconds != nil {
		s.OldestAge = time.Duration(*oldestSeconds * float64(time.Second))
	}
	return s, nil
}

func (in Intent) validate() error {
	if in.Op != OpGrant && in.Op != OpRevoke {
		return fmt.Errorf("identityroles: bad op %q", in.Op)
	}
	if strings.TrimSpace(in.UserID) == "" {
		return fmt.Errorf("identityroles: empty user_id")
	}
	if !IsEcosystemRole(in.Role) {
		return fmt.Errorf("%w: %q", ErrNotGrantable, in.Role)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
