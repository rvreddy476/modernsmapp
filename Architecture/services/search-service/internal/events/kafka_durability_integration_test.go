//go:build integration

package events

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Module 2 review P0-3 — Kafka offset durability, against a LIVE broker.
//
// LOCAL EVIDENCE: executed against Apache Kafka 3.7.1 (KRaft) and
// OpenSearch 2.13.0 on 2026-08-10; results are recorded in
// prompt/module-02-feed-discovery-search-claude-fixes-v3.md. CI runs the
// same suite against Redpanda, which has not been exercised locally.
// This suite remains a required CI gate
// (.github/workflows/integration-opensearch.yml, job
// `search-kafka-durability`).
//
// THE DEFECT THESE EXIST FOR
//
// The consumer used Reader.ReadMessage. With a consumer group, kafka-go
// commits the offset inside ReadMessage — before the caller has seen the
// payload, let alone applied it. So:
//
//   - a crash between ReadMessage and the projection write lost the
//     message permanently: it was committed, so it was never redelivered;
//   - retries exhausting followed by a failed DLQ write lost it too.
//
// For a takedown or a visibility downgrade, "lost" means the content stays
// publicly searchable forever with nothing left to repair it.
//
// The fix is FetchMessage plus an explicit CommitMessages that runs only
// after the projection succeeded or the message was durably handed off.
// These tests assert the observable consequence: a message that was not
// successfully handled comes back.

// failingPostWriteStore returns a store whose posts_v1 writes always fail,
// plus a counter of how many write attempts it saw.
//
// The counter is the point. An earlier version of the leapfrog tests
// pointed the store at a dead address, which made the failure realistic
// but INVISIBLE: when the consumer group took longer than the test window
// to join, instance 1 consumed nothing at all, the old leapfrogging loop
// never got a chance to leapfrog, and the negative control passed. The
// test could not tell "the fix worked" from "nothing happened".
//
// With this, the test asserts that instance 1 actually attempted the
// removal before drawing any conclusion from what came afterwards.
func failingPostWriteStore(t *testing.T) (*search.Store, *int32) {
	t.Helper()
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if strings.Contains(r.URL.Path, "posts_v1") && r.Method != http.MethodGet {
			atomic.AddInt32(&attempts, 1)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"simulated outage"}`))
			return
		}
		if strings.Contains(r.URL.Path, "posts_v1") && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"simulated outage"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	store, err := search.New(srv.URL)
	if err != nil {
		t.Fatalf("build failing store: %v", err)
	}
	atomic.StoreInt32(&attempts, 0)
	return store, &attempts
}

func kafkaBrokers(t *testing.T) []string {
	t.Helper()
	raw := os.Getenv("KAFKA_BROKERS")
	if raw == "" {
		t.Skip("KAFKA_BROKERS not set")
	}
	return strings.Split(raw, ",")
}

// newTopic creates a uniquely named single-partition topic so tests never
// interfere with each other.
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
		t.Fatalf("kafka controller: %v", err)
	}
	cc, err := kafka.Dial("tcp", fmt.Sprintf("%s:%d", controller.Host, controller.Port))
	if err != nil {
		t.Fatalf("dial controller: %v", err)
	}
	defer cc.Close()

	if err := cc.CreateTopics(kafka.TopicConfig{
		Topic: topic, NumPartitions: 1, ReplicationFactor: 1,
	}); err != nil {
		t.Fatalf("create topic %s: %v", topic, err)
	}
	// NOTE: topics are deliberately NOT deleted afterwards.
	//
	// On Windows, Kafka cannot rename a log directory that still has open
	// file handles; DeleteTopics raises AccessDeniedException, the log dir
	// is marked failed, and the broker shuts itself down mid-suite. Every
	// topic name here is unique, so leaving them costs a little disk on a
	// throwaway broker and nothing else. CI runs Linux where this would be
	// fine either way, but the behaviour should not differ by platform.

	// Topic creation is asynchronous across the cluster metadata.
	time.Sleep(2 * time.Second)
	return topic
}

func publish(t *testing.T, brokers []string, topic string, eventType string, payload any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	env := events.EventEnvelope{EventType: eventType, Payload: raw}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	w := &kafka.Writer{Addr: kafka.TCP(brokers...), Topic: topic, Balancer: &kafka.Hash{}}
	defer w.Close()

	// Topic metadata propagates asynchronously after creation, so a write
	// immediately afterwards can get UNKNOWN_TOPIC_OR_PARTITION. Retry
	// rather than failing the test on a setup race.
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
	t.Fatalf("publish to %s: %v", topic, lastErr)
}

// A takedown that could not be applied and could not be dead-lettered
// must be REDELIVERED, not silently committed.
//
// The first consumer instance is pointed at an unreachable OpenSearch and
// has its DLQ disabled, so it has nowhere durable to put the message. It
// is then stopped, standing in for a crash. A second instance with a
// working OpenSearch must still receive the same message.
func TestKafka_UnappliedRemovalIsRedeliveredAfterRestart(t *testing.T) {
	brokers := kafkaBrokers(t)
	store := liveStore(t)

	topic := newTopic(t, brokers, "m2-durability")
	group := "m2-durability-" + uuid.New().String()[:8]

	postID := uuid.New().String()
	author := uuid.New().String()
	marker := "zz" + uuid.New().String()[:8]
	t.Cleanup(func() { _ = store.DeletePost(context.Background(), postID) })

	// The post is publicly searchable to begin with.
	if err := store.ApplyPostProjection(context.Background(), postProjectionForTest(postID, author, marker, 1)); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshPosts(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The removal that must not be lost.
	publish(t, brokers, topic, events.PostSearchEligibilityChanged,
		events.PostSearchEligibilityChangedPayload{
			PostID: postID, AuthorID: author, Visibility: "public",
			ReviewStatus: "rejected", SearchRev: 2, ChangedAt: time.Now().UTC(),
		})

	// ── Instance 1: broken OpenSearch, no DLQ. It must not commit. ──
	t.Setenv("SEARCH_DLQ_TOPIC", "-")
	brokenStore := mustStore(t, "http://127.0.0.1:1")
	c1 := NewConsumer(brokers, group, topic, brokenStore)
	c1.retry = retryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}

	ctx1, cancel1 := context.WithTimeout(context.Background(), 25*time.Second)
	done := make(chan struct{})
	go func() { c1.Start(ctx1); close(done) }()
	time.Sleep(8 * time.Second) // let it fetch, fail, and refuse to commit
	cancel1()
	<-done
	_ = c1.Close()

	// The removal has NOT been applied yet.
	if err := store.RefreshPosts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if found, err := store.SearchPostsFiltered(context.Background(), marker, nil, 10); err != nil {
		t.Fatal(err)
	} else if len(found) == 0 {
		t.Fatal("precondition broken: the removal was applied by the failing instance")
	}

	// ── Instance 2: same group, working OpenSearch. ──
	c2 := NewConsumer(brokers, group, topic, store)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	done2 := make(chan struct{})
	go func() { c2.Start(ctx2); close(done2) }()

	deadline := time.Now().Add(25 * time.Second)
	applied := false
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		if err := store.RefreshPosts(context.Background()); err != nil {
			continue
		}
		found, err := store.SearchPostsFiltered(context.Background(), marker, nil, 10)
		if err == nil && len(found) == 0 {
			applied = true
			break
		}
	}
	cancel2()
	<-done2
	_ = c2.Close()

	if !applied {
		t.Fatal("M2-P0-3 REGRESSION: a removal that could neither be applied nor " +
			"dead-lettered was committed and never redelivered — the content stays " +
			"publicly searchable with nothing left to repair it")
	}
}

// Successful processing must advance the offset, or every restart would
// reprocess the whole topic.
func TestKafka_SuccessfulProcessingCommitsTheOffset(t *testing.T) {
	brokers := kafkaBrokers(t)
	store := liveStore(t)

	topic := newTopic(t, brokers, "m2-commit")
	group := "m2-commit-" + uuid.New().String()[:8]

	postID := uuid.New().String()
	author := uuid.New().String()
	marker := "zz" + uuid.New().String()[:8]
	t.Cleanup(func() { _ = store.DeletePost(context.Background(), postID) })

	publish(t, brokers, topic, events.PostCreated, events.PostCreatedPayload{
		PostID: postID, AuthorID: author, Text: "commit " + marker,
		Visibility: "public", ReviewStatus: "approved", SearchRev: 1,
		CreatedAt: time.Now().UTC(),
	})

	c := NewConsumer(brokers, group, topic, store)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	done := make(chan struct{})
	go func() { c.Start(ctx); close(done) }()

	deadline := time.Now().Add(25 * time.Second)
	indexed := false
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		if err := store.RefreshPosts(context.Background()); err != nil {
			continue
		}
		found, err := store.SearchPostsFiltered(context.Background(), marker, nil, 10)
		if err == nil && len(found) == 1 {
			indexed = true
			break
		}
	}
	cancel()
	<-done
	_ = c.Close()

	if !indexed {
		t.Fatal("the post was never indexed")
	}

	// A fresh reader in the same group must find nothing left to read.
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokers, GroupID: group, Topic: topic, MinBytes: 1, MaxBytes: 10e6,
	})
	defer r.Close()
	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer fetchCancel()
	if _, err := r.FetchMessage(fetchCtx); err == nil {
		t.Fatal("a successfully processed message was redelivered — the offset was " +
			"never committed, so every restart would reprocess the entire topic")
	}
}

// ─── Re-review P0-1: offset leapfrogging ────────────────────────────────────

// The failure the single-message test could not detect.
//
// Kafka offsets are cumulative: committing offset N+1 implicitly commits
// N. The old loop, on an unresolvable message, did `continue` and fetched
// the NEXT record. One later success then committed the failed removal
// too, and it was never redelivered.
//
// Sequence: publish a removal (N) that cannot be applied or dead-lettered,
// then a harmless event (N+1). If the consumer processes N+1 and commits,
// N is lost. The consumer must instead hold the partition at N.
func TestKafka_FailedMessageIsNotLeapfroggedByALaterSuccess(t *testing.T) {
	brokers := kafkaBrokers(t)
	store := liveStore(t)

	topic := newTopic(t, brokers, "m2-leapfrog")
	group := "m2-leapfrog-" + uuid.New().String()[:8]

	stuckPost := uuid.New().String()
	laterPost := uuid.New().String()
	author := uuid.New().String()
	stuckMarker := "zz" + uuid.New().String()[:8]
	laterMarker := "zz" + uuid.New().String()[:8]
	t.Cleanup(func() {
		_ = store.DeletePost(context.Background(), stuckPost)
		_ = store.DeletePost(context.Background(), laterPost)
	})

	// The post that must end up removed.
	if err := store.ApplyPostProjection(context.Background(),
		postProjectionForTest(stuckPost, author, stuckMarker, 1)); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshPosts(context.Background()); err != nil {
		t.Fatal(err)
	}

	// N: the removal, which cannot be applied while OpenSearch is down.
	publish(t, brokers, topic, events.PostSearchEligibilityChanged,
		events.PostSearchEligibilityChangedPayload{
			PostID: stuckPost, AuthorID: author, Visibility: "public",
			ReviewStatus: "rejected", SearchRev: 2, ChangedAt: time.Now().UTC(),
		})

	// N+1 must succeed INDEPENDENTLY of OpenSearch, or this test proves
	// nothing (re-review v2 P0-3).
	//
	// The earlier version published a PostCreated here, which also needed
	// OpenSearch. With the broken store BOTH records failed, so the old
	// fetch-continue loop committed neither and the test passed even with
	// the bug present.
	//
	// An event type the consumer does not handle falls through to
	// `default: return nil` — it succeeds without touching OpenSearch at
	// all. Under the old loop it would commit, and because Kafka offsets
	// are cumulative that commit would swallow the removal at N.
	publish(t, brokers, topic, "M2TestIgnoredEvent", map[string]string{
		"note": "handled by the default branch; never touches OpenSearch",
	})

	// Instance 1: OpenSearch unreachable, DLQ disabled. Neither message can
	// reach a durable outcome, and the consumer must not advance.
	t.Setenv("SEARCH_DLQ_TOPIC", "-")
	broken, attempts := failingPostWriteStore(t)
	c1 := NewConsumer(brokers, group, topic, broken)
	c1.retry = retryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond}

	ctx1, cancel1 := context.WithTimeout(context.Background(), 90*time.Second)
	done1 := make(chan struct{})
	go func() { c1.Start(ctx1); close(done1) }()
	// Wait until instance 1 has actually ATTEMPTED the removal. Without
	// this the test can silently degrade into "the consumer never joined
	// the group in time", which passes regardless of the loop behaviour.
	attemptDeadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(attemptDeadline) && atomic.LoadInt32(attempts) == 0 {
		time.Sleep(time.Second)
	}
	if atomic.LoadInt32(attempts) == 0 {
		cancel1()
		<-done1
		t.Fatal("instance 1 never attempted the removal; the test would be vacuous")
	}
	// Give it time to move on to N+1 if the loop wrongly allows that.
	time.Sleep(15 * time.Second)
	cancel1()
	<-done1
	_ = c1.Close()

	// PRECONDITION — without this the whole test is vacuous.
	//
	// The final assertion is "the post is no longer searchable". If the
	// post were never searchable to begin with, that would be true from
	// the start and the test would pass no matter what the consumer did.
	// (It did exactly that: the first negative-control run passed with the
	// leapfrogging loop restored.)
	if err := store.RefreshPosts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if found, err := store.SearchPostsFiltered(context.Background(), stuckMarker, nil, 10); err != nil {
		t.Fatal(err)
	} else if len(found) != 1 {
		t.Fatalf("precondition: the post should still be searchable after the failing "+
			"instance (found %d, want 1) — the removal must NOT have been applied yet",
			len(found))
	}

	// Instance 2: healthy. Both messages must still arrive, the removal
	// included.
	c2 := NewConsumer(brokers, group, topic, store)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel2()
	done2 := make(chan struct{})
	go func() { c2.Start(ctx2); close(done2) }()

	deadline := time.Now().Add(35 * time.Second)
	removalApplied := false
	for time.Now().Before(deadline) && !removalApplied {
		time.Sleep(time.Second)
		if err := store.RefreshPosts(context.Background()); err != nil {
			continue
		}
		if found, err := store.SearchPostsFiltered(context.Background(), stuckMarker, nil, 10); err == nil && len(found) == 0 {
			removalApplied = true
		}
	}
	cancel2()
	<-done2
	_ = c2.Close()

	if !removalApplied {
		t.Fatal("RE-REVIEW P0-1 REGRESSION: the removal at offset N was never applied. " +
			"The consumer processed the independently-successful record at N+1 and " +
			"committed it; because Kafka offsets are cumulative that committed N too, " +
			"so rejected content stays publicly searchable with nothing left to repair it")
	}
	_ = laterPost
	_ = laterMarker
}

// The DLQ replayer must not leapfrog either. Its record is the last copy
// of a message that already failed once.
func TestKafka_DLQReplayerDoesNotLeapfrogAFailedRecord(t *testing.T) {
	brokers := kafkaBrokers(t)
	store := liveStore(t)

	dlqTopic := newTopic(t, brokers, "m2-dlq-leapfrog")
	t.Setenv("SEARCH_DLQ_TOPIC", dlqTopic)

	sourceTopic := newTopic(t, brokers, "m2-dlq-src")
	group := "m2-dlq-leapfrog-" + uuid.New().String()[:8]

	stuckPost := uuid.New().String()
	author := uuid.New().String()
	marker := "zz" + uuid.New().String()[:8]
	t.Cleanup(func() { _ = store.DeletePost(context.Background(), stuckPost) })

	if err := store.ApplyPostProjection(context.Background(),
		postProjectionForTest(stuckPost, author, marker, 1)); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshPosts(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Seed the DLQ directly: the removal at N, then a record at N+1 that
	// succeeds WITHOUT OpenSearch (unhandled type → `default: return nil`).
	publish(t, brokers, dlqTopic, events.PostSearchEligibilityChanged,
		events.PostSearchEligibilityChangedPayload{
			PostID: stuckPost, AuthorID: author, Visibility: "public",
			ReviewStatus: "rejected", SearchRev: 2, ChangedAt: time.Now().UTC(),
		})
	publish(t, brokers, dlqTopic, "M2TestIgnoredEvent", map[string]string{
		"note": "succeeds without OpenSearch",
	})

	// Replayer 1: broken OpenSearch AND broken requeue/parking writers.
	//
	// Re-review v2 P0-3: the earlier version left requeue and parking
	// healthy. On a processing failure replayOne then durably REQUEUED the
	// record and returned true, so the record was resolved and the outer
	// hold loop was never what protected it — the test passed with or
	// without replayUntilDurable.
	//
	// Pointing both writers at a dead address makes replayOne return false,
	// which is the only state the outer loop exists for. The reader stays
	// healthy so it can still fetch N+1 if the loop wrongly advances.
	broken, attempts := failingPostWriteStore(t)
	c1 := NewConsumer(brokers, group, sourceTopic, broken)
	r1 := NewDLQReplayer(brokers, group, c1, nil)
	if r1 == nil {
		t.Fatal("expected a replayer; is SEARCH_DLQ_TOPIC set?")
	}
	r1.delay = 200 * time.Millisecond
	deadAddr := kafka.TCP("127.0.0.1:1")
	r1.dlqWrite = &kafka.Writer{Addr: deadAddr, Topic: dlqTopic,
		Balancer: &kafka.Hash{}, WriteTimeout: 2 * time.Second, MaxAttempts: 1}
	r1.parked = &kafka.Writer{Addr: deadAddr, Topic: dlqTopic + ".parked",
		Balancer: &kafka.Hash{}, WriteTimeout: 2 * time.Second, MaxAttempts: 1}

	ctx1, cancel1 := context.WithTimeout(context.Background(), 120*time.Second)
	done1 := make(chan struct{})
	go func() { r1.Start(ctx1); close(done1) }()
	// Wait until the replayer has actually ATTEMPTED the removal, so the
	// test cannot silently degrade into "it never joined the group".
	attemptDeadline := time.Now().Add(75 * time.Second)
	for time.Now().Before(attemptDeadline) && atomic.LoadInt32(attempts) == 0 {
		time.Sleep(time.Second)
	}
	if atomic.LoadInt32(attempts) == 0 {
		cancel1()
		<-done1
		t.Fatal("the replayer never attempted the removal; the test would be vacuous")
	}
	// Give it time to move on to N+1 if the loop wrongly allows that.
	time.Sleep(15 * time.Second)
	cancel1()
	<-done1
	_ = r1.Close()
	_ = c1.Close()

	// PRECONDITION: the removal must NOT have been applied yet, or the
	// final assertion would be trivially true.
	if err := store.RefreshPosts(context.Background()); err != nil {
		t.Fatal(err)
	}
	if found, err := store.SearchPostsFiltered(context.Background(), marker, nil, 10); err != nil {
		t.Fatal(err)
	} else if len(found) != 1 {
		t.Fatalf("precondition: the post should still be searchable after the failing "+
			"replayer (found %d, want 1)", len(found))
	}

	// Replayer 2: healthy. The removal must still be delivered.
	c2 := NewConsumer(brokers, group, sourceTopic, store)
	r2 := NewDLQReplayer(brokers, group, c2, nil)
	r2.delay = 200 * time.Millisecond
	ctx2, cancel2 := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel2()
	done2 := make(chan struct{})
	go func() { r2.Start(ctx2); close(done2) }()

	deadline := time.Now().Add(35 * time.Second)
	applied := false
	for time.Now().Before(deadline) && !applied {
		time.Sleep(time.Second)
		if err := store.RefreshPosts(context.Background()); err != nil {
			continue
		}
		if found, err := store.SearchPostsFiltered(context.Background(), marker, nil, 10); err == nil && len(found) == 0 {
			applied = true
		}
	}
	cancel2()
	<-done2
	_ = r2.Close()
	_ = c2.Close()

	if !applied {
		t.Fatal("RE-REVIEW P0-1 REGRESSION: the DLQ replayer advanced past a record " +
			"it could not resolve. That record was the last copy of the removal")
	}
}
