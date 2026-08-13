package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Module 3 LB-2 — consumer-side idempotency for graph safety events.
//
// WHY THIS IS NEEDED
//
// graph-service delivers safety events from a durable outbox. Delivery is
// AT LEAST ONCE by design: the relay publishes, then marks the row published,
// and a crash between those two steps redelivers. That ordering is deliberate
// — the alternative loses events, and a lost block is a user still reachable
// by someone they blocked.
//
// At-least-once is only safe if a consumer can recognise a replay. Until now
// nothing did: the claim that duplicates were harmless rested on every handler
// happening to be idempotent, which was never verified and is not true in
// general (a counter increment, an audit row, a notification send).
//
// WHAT MAKES A REPLAY RECOGNISABLE
//
// The publisher sets EventID to the OUTBOX ROW ID rather than a fresh uuid per
// publish, so a redelivery carries the same identifier. PairSeq is monotonic
// per canonical pair, so a consumer can also drop an event older than one it
// has already applied for that pair.
//
// This uses Redis rather than a table because the guarantee needed is "do not
// apply twice within the redelivery window", not "remember forever". A key
// expiring after the retention window is correct: an event replayed after that
// is not a broker retry, it is a genuine reprocess (a topic replay), and
// applying it again is the intended behaviour.
//
// CLB-1 — THE MARK HAPPENS AFTER THE EFFECT, NEVER BEFORE
//
// The first version of this claimed the key with SETNX BEFORE the handler ran.
// That inverted the guarantee: a handler whose datastore writes then failed
// left the event marked applied, and every later redelivery — the only chance
// to repair it — was suppressed. A pre-effect claim converts an at-least-once
// delivery into an at-most-once one, which is the opposite of what a safety
// event needs.
//
// So the deduper now has two operations, and the ORDER between them is the
// property that matters:
//
//	Seen()        — read-only, before the handler: skip work already done.
//	MarkApplied() — after the durable effect has COMMITTED.
//
// This is a best-effort fast path, not the correctness boundary. Two replicas
// racing the same redelivery can both pass Seen() and both apply; that is safe
// because the authoritative de-duplication is the PostgreSQL consumer inbox
// row written inside the effect's own transaction (see
// store.ApplyUserBlockedEffects). Redis exists here to avoid repeating work,
// not to decide whether the work happened.
//
// FAIL-OPEN IS CORRECT HERE, AND THAT IS DELIBERATE
//
// If Redis is unavailable the event is PROCESSED rather than skipped. The two
// failure modes are not symmetric: processing a duplicate block re-applies a
// cooldown that is already in place, while skipping one leaves a blocked
// account in the viewer's suggestions. Duplicate safety work is wasted effort;
// skipped safety work is the harm.

// claimStore is the small key surface the deduper needs. It is an interface so
// the two operations — and the fact that the marking one is separate from, and
// later than, the reading one — are explicit in the type.
type claimStore interface {
	// Has reports whether the key exists. Read-only: calling it must not
	// make a later delivery look already-applied.
	Has(ctx context.Context, key string) (bool, error)
	// ClaimIfAbsent returns true when THIS caller created the key.
	ClaimIfAbsent(ctx context.Context, key string, value int64, ttl time.Duration) (bool, error)
}

type redisClaimStore struct{ rdb *redis.Client }

func (r redisClaimStore) Has(ctx context.Context, key string) (bool, error) {
	n, err := r.rdb.Exists(ctx, key).Result()
	return n > 0, err
}

func (r redisClaimStore) ClaimIfAbsent(ctx context.Context, key string, value int64, ttl time.Duration) (bool, error) {
	// SETNX: check and set in one round trip, so concurrent replicas cannot
	// both win. Splitting this into Get + Set would reintroduce the race the
	// deduper exists to close.
	return r.rdb.SetNX(ctx, key, value, ttl).Result()
}

// GraphEventDeduper decides whether a graph event has already been applied.
type GraphEventDeduper struct {
	claims claimStore
	ttl    time.Duration
}

// NewGraphEventDeduper builds a deduper. A nil Redis client yields a deduper
// that always reports "not seen", so a deployment without Redis processes
// every delivery — see the fail-open note above.
func NewGraphEventDeduper(rdb *redis.Client, ttl time.Duration) *GraphEventDeduper {
	if ttl <= 0 {
		// Comfortably longer than the relay's retry window, so a redelivery
		// during a broker incident is still recognised.
		ttl = 24 * time.Hour
	}
	if rdb == nil {
		return &GraphEventDeduper{ttl: ttl}
	}
	return &GraphEventDeduper{claims: redisClaimStore{rdb: rdb}, ttl: ttl}
}

// newDeduperWithStore is used by tests to substitute the claim store without
// standing up Redis. The Redis path is one SETNX call; what a test needs to
// pin is that the CALLER makes a single atomic claim and interprets the result
// correctly.
func newDeduperWithStore(claims claimStore, ttl time.Duration) *GraphEventDeduper {
	return &GraphEventDeduper{claims: claims, ttl: ttl}
}

// graphEnvelope is the canonical envelope plus the per-pair sequence
// graph-service adds. Older producers omit pair_seq; it decodes as 0 and the
// event id alone carries the dedupe.
type graphEnvelope struct {
	EventID   string          `json:"event_id"`
	EventType string          `json:"event_type"`
	PairSeq   int64           `json:"pair_seq"`
	Payload   json.RawMessage `json:"payload"`
}

// dedupeKey returns the applied-marker key for an event, and "" when the event
// carries no identifier a replay could be recognised by.
func (d *GraphEventDeduper) dedupeKey(raw []byte) (string, int64, error) {
	var env graphEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", 0, fmt.Errorf("dedupe: decode envelope: %w", err)
	}
	if env.EventID == "" {
		// No identifier means no way to recognise a replay. Process it —
		// see the fail-open reasoning above.
		return "", 0, nil
	}
	return "suggestion:applied:" + env.EventID, env.PairSeq, nil
}

// Seen reports whether this exact event has already been APPLIED — that is,
// whether a previous delivery reached MarkApplied after its durable effect
// committed.
//
// This is read-only on purpose. Observing an event must not mark it: a handler
// that then fails has to stay redeliverable, and a marker written here would
// suppress the very retry that repairs it.
func (d *GraphEventDeduper) Seen(ctx context.Context, raw []byte) (bool, error) {
	if d == nil || d.claims == nil {
		return false, nil
	}
	key, _, err := d.dedupeKey(raw)
	if err != nil || key == "" {
		return false, err
	}
	seen, err := d.claims.Has(ctx, key)
	if err != nil {
		// Store unavailable: process rather than skip.
		return false, fmt.Errorf("dedupe: read applied marker: %w", err)
	}
	return seen, nil
}

// MarkApplied records that the event's durable effect has committed. It must
// be called only AFTER that commit; calling it earlier reintroduces the loss
// path described at the top of this file.
func (d *GraphEventDeduper) MarkApplied(ctx context.Context, raw []byte) error {
	if d == nil || d.claims == nil {
		return nil
	}
	key, seq, err := d.dedupeKey(raw)
	if err != nil || key == "" {
		return err
	}
	if _, err := d.claims.ClaimIfAbsent(ctx, key, seq, d.ttl); err != nil {
		// The marker is an optimisation. Failing to write it costs a
		// redundant replay, which the durable inbox absorbs.
		return fmt.Errorf("dedupe: write applied marker: %w", err)
	}
	return nil
}
