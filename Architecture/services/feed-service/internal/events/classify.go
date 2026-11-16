package events

import (
	"errors"
	"fmt"

	"github.com/atpost/feed-service/internal/service"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Which failures are retried and which are dead-lettered (finding feed-06).
//
// The consumer retries a failed event until it succeeds, and the partition
// waits behind it. That is right for an outage and wrong for an event that
// can never succeed: one of those would hold up every later event on its
// partition for good. So every error a handler returns is one of two kinds.
//
// PERMANENT: sent to the DLQ at once, no retries, the partition moves on.
//
//   - the payload does not decode into the event's type;
//   - an id the handler needs is not a UUID (or is the all-zero id where
//     the handler cannot act on it): post, author, follower, followee,
//     reposter, subscriber, deleted user;
//   - a dependency refused the request itself (service.UpstreamRejected:
//     400, 413, 414, 422). Fan-out no longer meets graph-service's
//     OFFSET_TOO_LARGE (it pages by cursor since 16 Nov 2026, and a refused
//     SAVED cursor restarts its leg), so this is now a backstop for a
//     request that is wrong on every attempt;
//   - Postgres refused the VALUE (SQLSTATE class 22, data exception: a text
//     it cannot store, a number out of range): refused on every attempt.
//     Constraint violations (class 23) are NOT here: one that waits on
//     another row can resolve, so they are retried.
//
// TRANSIENT: everything else, retried until it succeeds. Scylla, Postgres
// and Redis errors; a dependency that is unreachable, slow, or answers 401,
// 403, 404, 408, 429 or 5xx; the per-event deadline running out (a fan-out
// resumes from its saved chunk); a response body that does not decode; a
// relationship graph-service did not answer for. When in doubt an error is
// transient: a wrongly retried event is delayed and visible as lag, a
// wrongly dead-lettered one is lost.
//
// Not errors at all: an event type this service does not handle, a Q&A
// event whose ids do not parse, a malformed optional channel_id, and a
// re-fan-out of a note this service never recorded are skipped and
// committed, as before. No handler looks a post up, so "the post no longer
// exists" is never an error here: deletes and purges of rows that are
// already gone succeed.

// errNilID is the cause recorded for an all-zero id.
var errNilID = errors.New("the all-zero id")

// malformed marks an event that can never be applied because of what it
// says, naming the event and the offending part for the x-dlq-error header.
func malformed(envelope events.EventEnvelope, field string, err error) error {
	return sharedkafka.Permanent(fmt.Errorf("%s %s: malformed %s: %w",
		envelope.EventType, envelope.EventID, field, err))
}

// eventUUID parses an id the handler cannot do without.
func eventUUID(envelope events.EventEnvelope, field, raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, malformed(envelope, field, fmt.Errorf("%q: %w", raw, err))
	}
	return id, nil
}

// classify marks a handler error permanent when retrying cannot change the
// outcome, and returns every other error unchanged for the consumer to
// retry. Handlers mark malformed input themselves; this adds the two
// refusals that surface from below them.
func classify(err error) error {
	if err == nil || sharedkafka.IsPermanent(err) {
		return err
	}
	if service.UpstreamRejected(err) || postgresRefusedValue(err) {
		return sharedkafka.Permanent(err)
	}
	return err
}

// postgresRefusedValue reports a Postgres error about the data itself.
func postgresRefusedValue(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || len(pgErr.Code) < 2 {
		return false
	}
	return pgErr.Code[:2] == "22"
}
