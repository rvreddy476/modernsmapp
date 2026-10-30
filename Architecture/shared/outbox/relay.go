package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sort"
	"strconv"
	"time"

	"github.com/atpost/shared/o11y/trace"
	"github.com/google/uuid"
	kafka "github.com/segmentio/kafka-go"
)

// The relay pass (commerce backend audit M4, 30 Oct 2026; the design of
// chat-service message-service's outbox relay, audit H3/H4).
//
// Each poll, one replica (the holder of the table's relay advisory lock)
// claims batches of rows with a lease, publishes each batch in one Kafka
// write keyed by partition_key, and marks the batch published in one
// statement. It keeps draining while batches come back full, so a backlog
// clears at Kafka speed.
//
// A row that can never be published (larger than the broker accepts, or
// refused MaxAttempts times) is dead-lettered and skipped, so it cannot block
// the head of the queue. A transient failure (Kafka unreachable) releases the
// claims and ends the pass, so the next poll retries the same rows first and
// per-key order holds.
//
// One case gives up strict order: a row the broker refuses (a counted
// failure) backs off, and later rows of its key may publish before its retry.
// Such a row is broken data that dead-letters after MaxAttempts anyway.
const (
	defaultClaimLease  = 30 * time.Second
	defaultMaxAttempts = 10
	// maxRoundsPerPass bounds one pass (the lock holder drains at most
	// this many full batches before letting the next poll, possibly on
	// another replica, continue).
	maxRoundsPerPass = 50
	// writerBatchTimeout bounds how long a partly filled partition batch
	// waits inside WriteMessages.
	writerBatchTimeout = 10 * time.Millisecond
	// MaxMessageBytes is the largest message (key + value + headers +
	// record overhead) the writer sends: kafka-go's default BatchBytes and
	// the broker's default message.max.bytes. A row whose message is
	// larger can never be published and is dead-lettered.
	MaxMessageBytes = 1 << 20
	// recordOverhead approximates kafka-go's per-record framing so the
	// pre-check refuses a message before WriteMessages does.
	recordOverhead = 128
	// maxErrorText caps last_error. It is error text, never the payload.
	maxErrorText = 1000
	// detachedWriteTimeout bounds the bookkeeping writes after a publish,
	// which run even when the pass's context was cancelled (shutdown) so a
	// published batch is still marked.
	detachedWriteTimeout = 5 * time.Second
)

// Kafka headers the relay sets on every message.
const (
	// HeaderEventType carries outbox_events.event_type.
	HeaderEventType = "event_type"
	// HeaderEventID carries EventID(table, row id): the same row always
	// publishes with the same id, so a consumer can de-duplicate a
	// re-publish (a relay that died between WriteMessages and the mark).
	HeaderEventID = "event_id"
	// HeaderOutboxID carries the outbox row id.
	HeaderOutboxID = "outbox_id"
)

// ErrMessageTooLarge marks a row whose message exceeds MaxMessageBytes. It is
// dead-lettered at once instead of retried forever.
var ErrMessageTooLarge = errors.New("outbox event exceeds the Kafka message size limit")

// errNotWritten is the transient error of a message kafka-go did not write
// because another message in the call was too large.
var errNotWritten = errors.New("not written: another message in the batch exceeded the size limit")

// eventIDNamespace derives stable event ids from outbox rows.
var eventIDNamespace = uuid.MustParse("4b3e8f0a-2d6c-4e1b-9f57-6a0c3d8e21b4")

// EventID is the stable id of outbox row id in table (as Publisher names it:
// "outbox_events" or "<schema>.outbox_events"): a UUIDv5, sent in the
// "event_id" header.
func EventID(table string, id int64) string {
	return uuid.NewSHA1(eventIDNamespace, []byte(table+":"+strconv.FormatInt(id, 10))).String()
}

// relayLockKey is the advisory lock key of one outbox table's relay.
func relayLockKey(table string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("shared/outbox:relay:" + table))
	return int64(h.Sum64() & 0x7FFFFFFFFFFFFFFF)
}

type outboxRow struct {
	ID           int64
	EventType    string
	PartitionKey string
	Payload      json.RawMessage
}

// failure describes why one claimed row was not published.
type failure struct {
	// Reason is stored in last_error (truncated): error text only.
	Reason string
	// Permanent dead-letters the row now.
	Permanent bool
	// Counted counts toward MaxAttempts and backs the row off. A failure
	// that is neither is transient: the claim is released and the pass ends.
	Counted bool
}

// relayStore is the database half of the relay (pgStore in production).
type relayStore interface {
	// tryLock makes this replica the relay for one pass; ok is false when
	// another replica holds the lock.
	tryLock(ctx context.Context) (release func(), ok bool, err error)
	// claim leases up to limit due rows, in id order.
	claim(ctx context.Context, limit int, lease time.Duration) ([]outboxRow, error)
	// markPublished marks rows published and clears their claims.
	markPublished(ctx context.Context, ids []int64) error
	// release gives claims back after a transient failure, recording reason.
	release(ctx context.Context, ids []int64, reason string) error
	// recordFailure records a counted or permanent failure of one row and
	// reports whether it is now dead-lettered.
	recordFailure(ctx context.Context, id int64, f failure, maxAttempts int) (dead bool, err error)
}

// messageWriter is *kafka.Writer.
type messageWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
}

// pass runs one relay pass: take the lock, drain full batches, release.
func (p *Publisher) pass(ctx context.Context, w messageWriter) {
	release, held, err := p.store.tryLock(ctx)
	if err != nil {
		slog.Error("outbox relay: lock failed", "table", p.table, "error", err)
		return
	}
	if !held {
		return // another replica is relaying this pass
	}
	defer release()
	for round := 0; round < maxRoundsPerPass && ctx.Err() == nil; round++ {
		n, more := p.relayBatch(ctx, w)
		if !more || n < p.cfg.BatchSize {
			return
		}
	}
}

// relayBatch claims and publishes one batch. It returns how many rows were
// claimed and whether the pass may continue (false after a transient failure
// or a claim error).
func (p *Publisher) relayBatch(ctx context.Context, w messageWriter) (int, bool) {
	rows, err := p.store.claim(ctx, p.cfg.BatchSize, p.cfg.ClaimLease)
	if err != nil {
		slog.Error("outbox sweep failed", "table", p.table, "error", err)
		return 0, false
	}
	if len(rows) == 0 {
		return 0, false
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })

	// Bound the write well inside the lease, so a slow broker cannot hold
	// rows past their claim (kafka-go retries a write up to 10 times).
	wctx, wcancel := context.WithTimeout(ctx, p.cfg.ClaimLease*2/3)
	errs := p.publish(wctx, w, rows)
	wcancel()

	// The publish is done; record it even if ctx was cancelled meanwhile
	// (shutdown), or the batch is re-published after the lease.
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), detachedWriteTimeout)
	defer cancel()

	published := make([]int64, 0, len(rows))
	var transientIDs []int64
	transientReason := ""
	more := true
	for i, r := range rows {
		if errs[i] == nil {
			published = append(published, r.ID)
			continue
		}
		f := classifyPublishError(errs[i])
		if !f.Permanent && !f.Counted {
			transientIDs = append(transientIDs, r.ID)
			transientReason = f.Reason
			continue
		}
		if !p.recordFailure(bctx, r, f) {
			// Not recorded (legacy table, or the database failed): the
			// row is still due, so end the pass rather than spin on it.
			more = false
		}
	}
	if len(published) > 0 {
		if err := p.store.markPublished(bctx, published); err != nil {
			slog.Warn("outbox: failed to mark published", "table", p.table, "count", len(published), "error", err)
		}
	}
	if len(transientIDs) > 0 {
		slog.Error("outbox publish failed; will retry", "table", p.table,
			"count", len(transientIDs), "error", transientReason)
		if err := p.store.release(bctx, transientIDs, transientReason); err != nil {
			slog.Warn("outbox: failed to release claims", "table", p.table, "count", len(transientIDs), "error", err)
		}
		return len(rows), false
	}
	slog.Debug("outbox sweep done", "table", p.table, "count", len(published))
	return len(rows), more
}

// recordFailure records a counted or permanent failure and reports whether
// it was recorded.
func (p *Publisher) recordFailure(ctx context.Context, r outboxRow, f failure) bool {
	dead, err := p.store.recordFailure(ctx, r.ID, f, p.cfg.MaxAttempts)
	if err != nil {
		slog.Error("outbox: failed to record publish failure", "table", p.table, "outbox_id", r.ID,
			"event_type", r.EventType, "error", err, "publish_error", f.Reason)
		return false
	}
	if dead {
		// Operator alarm. The row stays in the table with failed_at set;
		// requeue it with UPDATE <table> SET failed_at = NULL, attempts = 0
		// WHERE id = ...
		slog.Error("outbox event dead-lettered", "table", p.table, "outbox_id", r.ID,
			"event_type", r.EventType, "permanent", f.Permanent, "last_error", f.Reason)
		return true
	}
	slog.Warn("outbox event refused; will retry", "table", p.table, "outbox_id", r.ID,
		"event_type", r.EventType, "error", f.Reason)
	return true
}

// publish writes rows in one WriteMessages call and returns one error per
// row (nil when that row was written).
func (p *Publisher) publish(ctx context.Context, w messageWriter, rows []outboxRow) []error {
	errs := make([]error, len(rows))
	msgs := make([]kafka.Message, 0, len(rows))
	index := make([]int, 0, len(rows))
	for i, r := range rows {
		m := p.message(ctx, r)
		if n := messageSize(m); n > MaxMessageBytes {
			errs[i] = fmt.Errorf("%w: %d bytes", ErrMessageTooLarge, n)
			continue
		}
		msgs = append(msgs, m)
		index = append(index, i)
	}
	if len(msgs) == 0 {
		return errs
	}
	mapWriteError(w.WriteMessages(ctx, msgs...), msgs, index, errs)
	return errs
}

func (p *Publisher) message(ctx context.Context, r outboxRow) kafka.Message {
	headers := []kafka.Header{
		{Key: HeaderEventType, Value: []byte(r.EventType)},
		{Key: HeaderEventID, Value: []byte(EventID(p.table, r.ID))},
		{Key: HeaderOutboxID, Value: []byte(strconv.FormatInt(r.ID, 10))},
	}
	// Phase F3.3 — W3C trace context in the headers so consumers can link
	// their spans back to the publisher (a fresh span per pass; the
	// originating request context is not stored with the row).
	trace.InjectKafkaHeaders(ctx, &headers)
	var key []byte
	if r.PartitionKey != "" {
		key = []byte(r.PartitionKey) // empty: nil, so Hash round-robins
	}
	return kafka.Message{Key: key, Value: r.Payload, Headers: headers}
}

func messageSize(m kafka.Message) int {
	n := len(m.Key) + len(m.Value) + recordOverhead
	for _, h := range m.Headers {
		n += len(h.Key) + len(h.Value)
	}
	return n
}

// mapWriteError spreads a WriteMessages error over the per-row errors.
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
		// was written. That one is permanent, the rest retry (their error
		// must not wrap MessageSizeTooLarge, or they would be counted).
		for j := range sent {
			if sameMessage(sent[j], tooLarge.Message) {
				errs[index[j]] = fmt.Errorf("%w: %v", ErrMessageTooLarge, err)
			} else {
				errs[index[j]] = errNotWritten
			}
		}
		return
	}
	for _, i := range index {
		errs[i] = err
	}
}

func sameMessage(a, b kafka.Message) bool {
	return len(a.Value) > 0 && len(a.Value) == len(b.Value) && &a.Value[0] == &b.Value[0]
}

// classifyPublishError maps a publish error to how the row is treated.
func classifyPublishError(err error) failure {
	reason := err.Error()
	if len(reason) > maxErrorText {
		reason = reason[:maxErrorText]
	}
	f := failure{Reason: reason}
	switch {
	case errors.Is(err, ErrMessageTooLarge):
		f.Permanent = true
	case isRecordError(err):
		f.Counted = true
	}
	return f
}

// isRecordError reports whether the broker refused the record itself rather
// than being unreachable. Only such failures count toward dead-lettering; a
// Kafka outage delays events, it never dead-letters them.
func isRecordError(err error) bool {
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
