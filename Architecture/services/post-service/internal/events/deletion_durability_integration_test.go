//go:build integration

package events

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
)

// Re-review P0-5 — account deletion must be durably consumed.
//
// LOCAL EVIDENCE: executed against Apache Kafka 3.7.1 (KRaft) and
// PostgreSQL 16.4 on 2026-08-10; results are recorded in
// prompt/module-02-feed-discovery-search-claude-fixes-v3.md. CI runs the
// same suite against Redpanda, which has not been exercised locally.
// This suite remains a required CI gate
// (.github/workflows/integration-opensearch.yml, job
// `post-service-deletion-durability`).
//
// THE DEFECT
//
// This consumer used ReadMessage, which commits the offset before the
// handler runs, and then only LOGGED a failed deletion. A transient
// PostgreSQL or outbox failure left a deleted account's posts undeleted in
// the canonical database, with the request already committed and no
// redelivery — permanently.
//
// search-service's author fence does not cover this. It protects the
// search index; the posts table and every other post-service read surface
// would still serve the deleted account's content.

func testBrokers(t *testing.T) []string {
	t.Helper()
	raw := os.Getenv("KAFKA_BROKERS")
	if raw == "" {
		t.Skip("KAFKA_BROKERS not set")
	}
	return strings.Split(raw, ",")
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newTopic(t *testing.T, brokers []string, prefix string) string {
	t.Helper()
	topic := fmt.Sprintf("%s-%s", prefix, uuid.New().String()[:8])

	conn, err := kafka.Dial("tcp", brokers[0])
	if err != nil {
		t.Fatalf("dial kafka: %v", err)
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
	// Topics are deliberately NOT deleted afterwards: on Windows, Kafka
	// cannot rename a log directory with open handles, DeleteTopics raises
	// AccessDeniedException, the log dir is marked failed and the broker
	// shuts down mid-suite. Names are unique, so leaving them is harmless.
	time.Sleep(2 * time.Second)
	return topic
}

func publishDeletion(t *testing.T, brokers []string, topic, userID string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"user_id": userID})
	env := events.EventEnvelope{EventType: events.EventUserDeletionRequested, Payload: payload}
	body, _ := json.Marshal(env)

	w := &kafka.Writer{Addr: kafka.TCP(brokers...), Topic: topic, Balancer: &kafka.Hash{}}
	defer w.Close()

	// Topic metadata propagates asynchronously; retry rather than failing
	// the test on a setup race.
	deadline := time.Now().Add(45 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		lastErr = w.WriteMessages(ctx, kafka.Message{Value: body})
		cancel()
		if lastErr == nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("publish: %v", lastErr)
}

// seedPost inserts a live post for the author.
func seedPost(t *testing.T, pool *pgxpool.Pool, authorID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO posts (id, author_id, text, visibility, review_status, content_type, created_at, updated_at, search_rev)
		VALUES ($1, $2, 'durability seed', 'public', 'approved', 'post', NOW(), NOW(), 1)`,
		id, authorID)
	if err != nil {
		t.Fatalf("seed post: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM posts WHERE id = $1`, id)
	})
	return id
}

func countLivePosts(t *testing.T, pool *pgxpool.Pool, authorID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM posts WHERE author_id = $1 AND deleted_at IS NULL`,
		authorID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func countOutbox(t *testing.T, pool *pgxpool.Pool, postIDs []uuid.UUID) int {
	t.Helper()
	ids := make([]string, 0, len(postIDs))
	for _, id := range postIDs {
		ids = append(ids, id.String())
	}
	var n int
	// The table is post_outbox_events (see post-service migration 007);
	// the aggregate id carries the post id.
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM post_outbox_events
		 WHERE event_type = $1 AND aggregate_id::text = ANY($2)`,
		events.PostSearchEligibilityChanged, ids).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	return n
}

// A deletion that cannot be applied must be redelivered, and when it does
// apply, the posts and their per-post outbox events must land together.
func TestPostService_AccountDeletionIsRedeliveredUntilItSucceeds(t *testing.T) {
	brokers := testBrokers(t)
	pool := testPool(t)

	// NOTE: the consumer hardcodes the group id "post-service-group", so
	// both instances below share it and the second resumes where the first
	// refused to commit.
	topic := newTopic(t, brokers, "ps-deletion")

	author := uuid.New()
	postIDs := []uuid.UUID{
		seedPost(t, pool, author),
		seedPost(t, pool, author),
	}
	if got := countLivePosts(t, pool, author); got != 2 {
		t.Fatalf("precondition: %d live posts, want 2", got)
	}

	publishDeletion(t, brokers, topic, author.String())

	// ── Instance 1: PostgreSQL unreachable. Must not commit. ──
	brokenPool, err := pgxpool.New(context.Background(),
		"postgres://postgres:postgres@127.0.0.1:1/nope?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("build broken pool: %v", err)
	}
	c1 := NewConsumer(brokers, topic, brokenPool)
	ctx1, cancel1 := context.WithTimeout(context.Background(), 25*time.Second)
	done1 := make(chan struct{})
	go func() { c1.Start(ctx1); close(done1) }()
	time.Sleep(10 * time.Second)
	cancel1()
	<-done1
	_ = c1.Close()
	brokenPool.Close()

	if got := countLivePosts(t, pool, author); got != 2 {
		t.Fatalf("precondition broken: the failing instance deleted %d posts", 2-got)
	}

	// ── Instance 2: healthy. The deletion must still arrive. ──
	c2 := NewConsumer(brokers, topic, pool)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel2()
	done2 := make(chan struct{})
	go func() { c2.Start(ctx2); close(done2) }()

	deadline := time.Now().Add(35 * time.Second)
	deleted := false
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		if countLivePosts(t, pool, author) == 0 {
			deleted = true
			break
		}
	}
	cancel2()
	<-done2
	_ = c2.Close()

	if !deleted {
		t.Fatal("RE-REVIEW P0-5 REGRESSION: the account-deletion request was committed " +
			"before the transaction ran and never redelivered. The deleted account's " +
			"posts remain live in the canonical database")
	}

	// Every post must have produced an eligibility event in the same
	// transaction — otherwise search would never learn about the deletion.
	if got := countOutbox(t, pool, postIDs); got != len(postIDs) {
		t.Fatalf("outbox events = %d, want %d: the soft-delete and its eligibility "+
			"events must commit atomically", got, len(postIDs))
	}
}

// A successful deletion must advance the offset.
func TestPostService_SuccessfulDeletionCommitsTheOffset(t *testing.T) {
	brokers := testBrokers(t)
	pool := testPool(t)

	topic := newTopic(t, brokers, "ps-commit")
	author := uuid.New()
	seedPost(t, pool, author)

	publishDeletion(t, brokers, topic, author.String())

	c := NewConsumer(brokers, topic, pool)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	done := make(chan struct{})
	go func() { c.Start(ctx); close(done) }()

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) && countLivePosts(t, pool, author) != 0 {
		time.Sleep(time.Second)
	}
	cancel()
	<-done
	_ = c.Close()

	if countLivePosts(t, pool, author) != 0 {
		t.Fatal("deletion was never applied")
	}

	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers, GroupID: "post-service-group", Topic: topic,
		MinBytes: 1, MaxBytes: 10e6,
	})
	defer r.Close()
	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer fetchCancel()
	if _, err := r.FetchMessage(fetchCtx); err == nil {
		t.Fatal("a successfully handled deletion was redelivered; the offset was " +
			"never committed and every restart would redo the whole topic")
	}
}
