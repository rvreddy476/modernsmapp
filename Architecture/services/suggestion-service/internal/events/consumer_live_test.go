//go:build integration

package events

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	sharedevents "github.com/atpost/shared/events"
	"github.com/atpost/suggestion-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// Module 3 CLB-1 — live Kafka + live PostgreSQL proof that a UserBlocked
// offset is committed only after a durable, idempotent safety effect.
//
//	KAFKA_BROKERS=127.0.0.1:9092 \
//	POSTGRES_DSN=postgres://postgres:...@127.0.0.1:55432/m3suggest \
//	go test -tags integration ./internal/events/ -run Live -v
//
// WHY AN IN-PROCESS PROOF WAS REJECTED, AND WHAT THIS PROVES INSTEAD
//
// The previous evidence was a claim-store test: it showed that an atomic
// check-and-set has exactly one winner. That was never the disputed property.
// The disputed property is the ORDER of three things — broker acknowledgement,
// dedupe marking, and the durable effect — and an in-process fake cannot model
// broker acknowledgement at all.
//
// So these tests use a real broker and a real database, and assert on the
// broker's own committed offset (OffsetFetch), not on anything the consumer
// says about itself. The three loss paths named in the closure review each get
// a test, and each has a named negative control listed with it.
//
// HOW A DURABLE FAILURE IS FORCED WITHOUT KILLING THE DATABASE
//
// Each test runs in its own PostgreSQL schema on the search_path. Phase one
// leaves that schema EMPTY, so the effect's very first statement fails with a
// real 42P01 from a real server — the same class of failure as an outage, with
// none of the collateral damage of stopping the shared instance. Phase two
// creates the tables and the retry succeeds.

const (
	// A failing effect retries on this cadence, so a test can observe the
	// offset staying put across several attempts without waiting long.
	liveRetryBackoff = 150 * time.Millisecond
	liveMaxBackoff   = 300 * time.Millisecond
)

func liveBrokers(t *testing.T) []string {
	t.Helper()
	raw := os.Getenv("KAFKA_BROKERS")
	if raw == "" {
		t.Skip("KAFKA_BROKERS not set; live broker proof skipped")
	}
	var out []string
	for _, b := range strings.Split(raw, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}

// liveSchema creates an isolated, initially EMPTY schema and returns a pool
// whose search_path points at it. Nothing in the store qualifies its table
// names, so this decides whether the effect's statements find their tables.
func liveSchema(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set; live database proof skipped")
	}
	schema := "clb1_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")

	admin, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := admin.Exec(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		admin.Close()
	})

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect scoped: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, schema
}

// createEffectTables makes the effect's target tables exist, turning a failing
// handler into a succeeding one mid-test.
func createEffectTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if err := store.New(pool, pool).EnsureSchema(context.Background()); err != nil {
		t.Fatalf("create effect tables: %v", err)
	}
}

func liveRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

// liveTopic creates a fresh single-partition topic. Single partition is
// deliberate: offset assertions are about ordering on ONE partition, and a
// spread across several would make "the offset did not advance" ambiguous.
func liveTopic(t *testing.T, brokers []string) string {
	t.Helper()
	topic := "clb1-" + uuid.NewString()
	conn, err := kafka.Dial("tcp", brokers[0])
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer conn.Close()
	controller, err := conn.Controller()
	if err != nil {
		t.Fatalf("controller: %v", err)
	}
	cc, err := kafka.Dial("tcp", fmt.Sprintf("%s:%d", controller.Host, controller.Port))
	if err != nil {
		t.Fatalf("dial controller: %v", err)
	}
	defer cc.Close()
	if err := cc.CreateTopics(kafka.TopicConfig{
		Topic: topic, NumPartitions: 1, ReplicationFactor: 1,
	}); err != nil {
		t.Fatalf("create topic: %v", err)
	}
	// Topic creation is asynchronous: the controller accepts it and the
	// metadata reaches the broker afterwards. Publishing before then fails
	// with UnknownTopicOrPartition, so wait for a real partition leader.
	deadline := time.Now().Add(30 * time.Second)
	for {
		parts, err := conn.ReadPartitions(topic)
		if err == nil && len(parts) == 1 && parts[0].Leader.ID >= 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("topic %s never became available: %v", topic, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
	// Topics are NOT deleted on cleanup: DeleteTopics has killed this broker
	// before, and a leftover test topic costs nothing.
	return topic
}

// publish writes one message, retrying while the broker still reports the
// freshly created topic as unknown.
//
// kafka-go's default Transport caches cluster metadata for a few seconds and
// shares that cache across writers, so a topic created after the cache was
// last filled is invisible until it expires. Giving each writer its own
// Transport plus a bounded retry keeps that from showing up as a flake in a
// test whose subject is offsets, not topic creation.
func publish(t *testing.T, brokers []string, topic string, key, body []byte) {
	t.Helper()
	w := &kafka.Writer{
		Addr:         kafka.TCP(brokers...),
		Topic:        topic,
		Balancer:     &kafka.LeastBytes{},
		RequiredAcks: kafka.RequireAll,
		Transport:    &kafka.Transport{MetadataTTL: 100 * time.Millisecond},
	}
	defer w.Close()

	deadline := time.Now().Add(45 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := w.WriteMessages(ctx, kafka.Message{Key: key, Value: body})
		cancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("publish to %s: %v", topic, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// publishBlock produces one UserBlocked message in the exact wire format
// graph-service's outbox publisher emits.
func publishBlock(t *testing.T, brokers []string, topic string, eventID uuid.UUID, blocker, blocked uuid.UUID) []byte {
	t.Helper()
	body := graphEvent(t, eventID, 1, blocker, blocked)
	publish(t, brokers, topic, []byte(blocker.String()), body)
	return body
}

// committedOffset asks the BROKER what this group has committed. This is the
// assertion that matters: it is the broker's state, not the consumer's claim
// about itself. -1 means nothing has ever been committed.
func committedOffset(t *testing.T, brokers []string, group, topic string) int64 {
	t.Helper()
	client := &kafka.Client{Addr: kafka.TCP(brokers...), Timeout: 15 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := client.OffsetFetch(ctx, &kafka.OffsetFetchRequest{
		GroupID: group,
		Topics:  map[string][]int{topic: {0}},
	})
	if err != nil {
		t.Fatalf("offset fetch: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("offset fetch: %v", resp.Error)
	}
	for _, p := range resp.Topics[topic] {
		if p.Partition == 0 {
			if p.Error != nil {
				t.Fatalf("offset fetch partition 0: %v", p.Error)
			}
			return p.CommittedOffset
		}
	}
	return -1
}

// liveConsumer builds a consumer wired exactly the way main.go wires one, with
// a short retry backoff so a failing message can be observed several times.
func liveConsumer(t *testing.T, brokers []string, group, topic string, pool *pgxpool.Pool, rdb *redis.Client) *Consumer {
	t.Helper()
	st := store.New(pool, pool)
	c := NewConsumer(brokers, group, topic, rdb, nil, st)
	c.retryBackoff = liveRetryBackoff
	c.maxBackoff = liveMaxBackoff
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count (%s): %v", query, err)
	}
	return n
}

// ─────────────────────────────────────────────────────────────────────────────
// PROOF 1 — a failing durable effect does not advance the offset, and the same
// event is retried until it succeeds.
//
// NEGATIVE CONTROLS THIS FAILS UNDER:
//   - restore ReadMessage in Start (it commits before returning the message):
//     the offset advances during the failing phase and the first assertion
//     fires;
//   - swallow the PostgreSQL error in handleUserBlocked (return nil instead of
//     the wrapped error): the offset advances while nothing is written and
//     both the offset and the state assertions fire;
//   - move MarkApplied before dispatch: the retry is suppressed as a replay,
//     the effect is never applied, and the state assertions fire.
//
// ─────────────────────────────────────────────────────────────────────────────
func TestLiveOffsetIsNotCommittedUntilTheDurableEffectSucceeds(t *testing.T) {
	brokers := liveBrokers(t)
	pool, _ := liveSchema(t)
	rdb := liveRedis(t)
	topic := liveTopic(t, brokers)
	group := "clb1-fail-" + uuid.NewString()

	blocker, blocked := uuid.New(), uuid.New()
	eventID := uuid.New()
	publishBlock(t, brokers, topic, eventID, blocker, blocked)

	// The schema is empty, so the effect cannot possibly succeed yet.
	c := liveConsumer(t, brokers, group, topic, pool, rdb)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); c.Start(ctx) }()

	// Give the consumer time to join the group, fetch, and fail several times
	// over. Comfortably longer than the reader's 10s MaxWait plus a group join,
	// so this is not asserting on a consumer that has not started yet.
	time.Sleep(25 * time.Second)
	if off := committedOffset(t, brokers, group, topic); off > 0 {
		t.Fatalf("committed offset is %d while the durable effect was FAILING. "+
			"The event has been acknowledged without its safety state being written: "+
			"the block is lost and no redelivery will repair it.", off)
	}

	// The database recovers.
	createEffectTables(t, pool)

	deadline := time.Now().Add(60 * time.Second)
	var off int64
	for time.Now().Before(deadline) {
		if off = committedOffset(t, brokers, group, topic); off >= 1 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if off < 1 {
		t.Fatalf("the offset never advanced after the database recovered (still %d); "+
			"the retry loop is not redelivering the message", off)
	}

	cancel()
	wg.Wait()

	// The effect must be COMPLETE and symmetric, not partial.
	if n := countRows(t, pool,
		`SELECT count(*) FROM suggestion_cooldowns WHERE cooldown_type='block' AND cooldown_until IS NULL
		   AND ((viewer_id=$1 AND candidate_id=$2) OR (viewer_id=$2 AND candidate_id=$1))`,
		blocker, blocked); n != 2 {
		t.Fatalf("got %d permanent block cooldowns, want 2 (one per direction). "+
			"A one-directional block still suggests the blocker to the blocked user.", n)
	}
	if n := countRows(t, pool,
		`SELECT count(*) FROM suggestion_consumer_inbox WHERE event_id=$1`, eventID.String()); n != 1 {
		t.Fatalf("consumer inbox has %d rows for the event, want exactly 1", n)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PROOF 2 — the crash boundary. Apply the effect, die before committing the
// offset, redeliver: the replay must leave ONE stable state, not two effects
// and not a lost one.
//
// This drives fetch → process → (no commit) directly rather than through
// Start, because that is precisely what a crash between those two steps looks
// like, and it is the one interleaving Start cannot be asked to produce on
// demand.
//
// NEGATIVE CONTROL: drop the ON CONFLICT DO NOTHING inbox claim from
// ApplyUserBlockedEffects and the replay is applied a second time — the inbox
// count assertion fires with 2.
// ─────────────────────────────────────────────────────────────────────────────
func TestLiveCrashBeforeCommitReplaysIdempotently(t *testing.T) {
	brokers := liveBrokers(t)
	pool, _ := liveSchema(t)
	createEffectTables(t, pool)
	topic := liveTopic(t, brokers)
	group := "clb1-crash-" + uuid.NewString()

	// The two replicas get SEPARATE, empty Redis instances on purpose.
	//
	// Sharing one would let the replay be recognised by the Redis marker and
	// prove nothing about the durable path. A cache is exactly the thing that
	// is not there after an eviction, a flush, a failover, or a restart in
	// another zone — and the whole point of CLB-1 is that recognising a replay
	// must not depend on it. With a cold cache the PostgreSQL consumer inbox
	// is the only thing left to do the job, which is what this asserts.
	rdbA, rdbB := liveRedis(t), liveRedis(t)

	blocker, blocked := uuid.New(), uuid.New()
	eventID := uuid.New()
	publishBlock(t, brokers, topic, eventID, blocker, blocked)

	// Seed candidate rows in both directions so their removal is observable.
	for _, p := range [2][2]uuid.UUID{{blocker, blocked}, {blocked, blocker}} {
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO suggestion_candidates (viewer_id, candidate_id, suggestion_type)
			VALUES ($1,$2,'friend') ON CONFLICT DO NOTHING`, p[0], p[1]); err != nil {
			t.Fatalf("seed candidate: %v", err)
		}
	}

	// ── Replica A: fetch, apply the durable effect, then "crash". ──
	first := liveConsumer(t, brokers, group, topic, pool, rdbA)
	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 60*time.Second)
	m, err := first.reader.FetchMessage(fetchCtx)
	fetchCancel()
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if err := first.processMessage(context.Background(), m); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	// The effect is committed in PostgreSQL...
	applied, err := store.New(pool, pool).UserBlockedEffectApplied(context.Background(), eventID.String())
	if err != nil {
		t.Fatalf("inbox read: %v", err)
	}
	if !applied {
		t.Fatal("the effect reported success but left no consumer inbox row")
	}
	// Record when the effect landed. If the replay re-runs the effect rather
	// than recognising it, the cooldown upsert resets created_at — an
	// observable state change, which is what "one stable state" rules out.
	// Every individual statement in the effect is idempotent on its own, so
	// this timestamp is the only thing that can tell a recognised replay from
	// a repeated one.
	var appliedAtBefore time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT max(created_at) FROM suggestion_cooldowns
		   WHERE (viewer_id=$1 AND candidate_id=$2) OR (viewer_id=$2 AND candidate_id=$1)`,
		blocker, blocked).Scan(&appliedAtBefore); err != nil {
		t.Fatalf("read cooldown timestamp: %v", err)
	}

	// ...and the process dies HERE, before CommitMessages.
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if off := committedOffset(t, brokers, group, topic); off > 0 {
		t.Fatalf("offset %d was committed even though the replica crashed before "+
			"CommitMessages; the crash boundary is not where the code says it is", off)
	}

	// ── Replica B: the broker redelivers the same event. ──
	second := liveConsumer(t, brokers, group, topic, pool, rdbB)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); second.Start(ctx) }()

	deadline := time.Now().Add(60 * time.Second)
	var off int64
	for time.Now().Before(deadline) {
		if off = committedOffset(t, brokers, group, topic); off >= 1 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	cancel()
	wg.Wait()
	if off < 1 {
		t.Fatalf("the redelivered event was never committed (offset %d)", off)
	}

	// ONE stable state: not doubled, not lost.
	if n := countRows(t, pool,
		`SELECT count(*) FROM suggestion_consumer_inbox WHERE event_id=$1`, eventID.String()); n != 1 {
		t.Fatalf("consumer inbox has %d rows for the replayed event, want exactly 1: "+
			"the replay was applied a second time instead of being recognised", n)
	}
	if n := countRows(t, pool,
		`SELECT count(*) FROM suggestion_cooldowns
		   WHERE (viewer_id=$1 AND candidate_id=$2) OR (viewer_id=$2 AND candidate_id=$1)`,
		blocker, blocked); n != 2 {
		t.Fatalf("got %d cooldown rows after the replay, want 2", n)
	}
	if n := countRows(t, pool,
		`SELECT count(*) FROM suggestion_candidates
		   WHERE (viewer_id=$1 AND candidate_id=$2) OR (viewer_id=$2 AND candidate_id=$1)`,
		blocker, blocked); n != 0 {
		t.Fatalf("%d candidate rows survived the block; the blocked pair is still "+
			"suggestible to each other", n)
	}

	// The replay must have been RECOGNISED, not merely survived. Re-running an
	// idempotent effect leaves the same rows but a newer created_at.
	var appliedAtAfter time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT max(created_at) FROM suggestion_cooldowns
		   WHERE (viewer_id=$1 AND candidate_id=$2) OR (viewer_id=$2 AND candidate_id=$1)`,
		blocker, blocked).Scan(&appliedAtAfter); err != nil {
		t.Fatalf("read cooldown timestamp: %v", err)
	}
	if !appliedAtAfter.Equal(appliedAtBefore) {
		t.Fatalf("the replay REAPPLIED the effect (cooldown created_at moved %s -> %s) "+
			"instead of being recognised by the durable consumer inbox. The rows happen "+
			"to be idempotent today, so this is the only signal that a replay storm "+
			"would be rewriting safety state rather than skipping it.",
			appliedAtBefore, appliedAtAfter)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PROOF 3 — a partial effect is never observable and never mistaken for a
// finished one.
//
// The candidate delete is the LAST statement in the transaction. If the
// transaction were not really a transaction, a failure at that point would
// leave the two cooldowns behind and the inbox row claiming the event was
// applied — a permanently half-applied block that no replay would repair.
//
// NEGATIVE CONTROL: replace the transaction in ApplyUserBlockedEffects with
// direct pool.Exec calls and this test fails: the cooldowns and the inbox row
// survive the failure.
// ─────────────────────────────────────────────────────────────────────────────
func TestLivePartialEffectIsRolledBackEntirely(t *testing.T) {
	_ = liveBrokers(t) // same gating as the rest of the live suite
	pool, _ := liveSchema(t)
	createEffectTables(t, pool)
	ctx := context.Background()

	// Break the LAST statement only. suggestion_candidates is dropped, so the
	// two cooldown upserts and the inbox insert have already run when the
	// failure hits.
	if _, err := pool.Exec(ctx, `DROP TABLE suggestion_candidates`); err != nil {
		t.Fatalf("drop candidates: %v", err)
	}

	st := store.New(pool, pool)
	blocker, blocked := uuid.New(), uuid.New()
	eventID := uuid.NewString()

	_, err := st.ApplyUserBlockedEffects(ctx, eventID, blocker, blocked)
	if err == nil {
		t.Fatal("a failing candidate delete was reported as success; the offset would " +
			"be committed for an effect that did not complete")
	}

	// The failure must be reported BY THE STATEMENT THAT CAUSED IT, not merely
	// noticed later by COMMIT.
	//
	// This assertion exists because of a measured gap. Swallowing the delete
	// error on its own did NOT fail this test: PostgreSQL aborts the
	// transaction, so the subsequent COMMIT returns ErrTxCommitRollback and an
	// error surfaced anyway. Safety survived, but by accident of transaction
	// semantics rather than by the code checking its own writes — and the
	// operator would see "commit failed" with no indication of which statement
	// broke. Two swallows in a row (delete AND commit) then produced a real
	// silent-success loss path.
	//
	// Pinning the origin closes that: any single swallowed statement error
	// fails here, before the combination can become a data-loss bug.
	if !strings.Contains(err.Error(), "remove candidates") {
		t.Fatalf("the effect failed at the candidate delete, but the error reported was %q. "+
			"An error a statement does not raise itself is one a later swallow can hide.", err)
	}

	if n := countRows(t, pool, `SELECT count(*) FROM suggestion_cooldowns
		WHERE (viewer_id=$1 AND candidate_id=$2) OR (viewer_id=$2 AND candidate_id=$1)`,
		blocker, blocked); n != 0 {
		t.Fatalf("%d cooldown rows survived a failed effect; the write is not atomic", n)
	}
	if n := countRows(t, pool, `SELECT count(*) FROM suggestion_consumer_inbox WHERE event_id=$1`,
		eventID); n != 0 {
		t.Fatalf("the event was marked applied (%d inbox rows) even though its effect "+
			"failed. Every redelivery would now be skipped and the block lost forever.", n)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PROOF 4 — a message that can never succeed must not stall the partition,
// and must not be mistaken for a durable success either.
//
// A corrupt record sitting at the head of a partition with an infinite retry
// loop behind it would stop every later block event from ever being applied.
// ─────────────────────────────────────────────────────────────────────────────
func TestLivePoisonMessageDoesNotStallLaterSafetyEvents(t *testing.T) {
	brokers := liveBrokers(t)
	pool, _ := liveSchema(t)
	rdb := liveRedis(t)
	createEffectTables(t, pool)
	topic := liveTopic(t, brokers)
	group := "clb1-poison-" + uuid.NewString()

	// A UserBlocked event whose blocker_id will never parse, followed by a
	// good one.
	badPayload, _ := json.Marshal(sharedevents.UserBlockedPayload{
		BlockerID: "not-a-uuid", BlockedID: uuid.NewString(), BlockedAt: time.Now().UTC(),
	})
	actor := "not-a-uuid"
	badEnv := sharedevents.NewEnvelope(context.Background(), sharedevents.UserBlocked, &actor, badPayload)
	badEnv.EventID = uuid.NewString()
	badBody, _ := json.Marshal(badEnv)

	publish(t, brokers, topic, nil, badBody)

	blocker, blocked := uuid.New(), uuid.New()
	goodID := uuid.New()
	publishBlock(t, brokers, topic, goodID, blocker, blocked)

	c := liveConsumer(t, brokers, group, topic, pool, rdb)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); c.Start(ctx) }()

	st := store.New(pool, pool)
	deadline := time.Now().Add(60 * time.Second)
	applied := false
	for time.Now().Before(deadline) {
		ok, err := st.UserBlockedEffectApplied(context.Background(), goodID.String())
		if err == nil && ok {
			applied = true
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	cancel()
	wg.Wait()

	if !applied {
		t.Fatal("a block event queued behind an unparseable message was never applied; " +
			"one corrupt record stalls the entire safety pipeline for that partition")
	}
}
