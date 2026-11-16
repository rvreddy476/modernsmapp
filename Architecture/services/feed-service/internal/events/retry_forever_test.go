package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/feed-service/internal/service"
	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/segmentio/kafka-go"
)

// Finding feed-06. The feed consumer gave up on an event after three
// retries (1 s, 2 s, 4 s) and wrote it to the DLQ, which nothing replays,
// so a short outage of Scylla, Postgres, graph-service or post-service
// dropped every fan-out, delete, visibility change and unfollow purge of
// that window for good.
//
// These tests run the real retry loop (shared/kafka, through its test
// seams: no broker) with the feed's own configuration and the feed's own
// handler, so they fail if either RetryForever or the classification in
// classify.go goes.

// rig is a feed consumer whose commits and dead-letter writes are recorded.
type rig struct {
	c     *Consumer
	inner *sharedkafka.Consumer

	mu      sync.Mutex
	log     []string // "effect", "commit", "dlq" in the order they happened
	dlq     []kafka.Message
	commits int
}

// newRig builds the consumer with the production configuration and only
// the backoff changed, so a test does not sleep for real seconds.
func newRig(t *testing.T, backoff time.Duration) *rig {
	t.Helper()
	cfg := consumerConfig(nil, "feed-service-group", "social.events.v1", DefaultDLQTopic)
	if !cfg.RetryForever {
		t.Fatal("the feed consumer is not configured to retry forever: a transient failure is " +
			"dead-lettered after three retries and the event is lost (finding feed-06)")
	}
	cfg.RetryBackoff = backoff
	r := &rig{c: &Consumer{deadline: 5 * time.Second}}
	r.inner = sharedkafka.NewTestConsumer(cfg, r.c.handle, sharedkafka.TestSeams{
		Dedupe: true,
		Commit: func(context.Context, kafka.Message) error {
			r.record("commit")
			r.mu.Lock()
			r.commits++
			r.mu.Unlock()
			return nil
		},
		DLQ: func(_ context.Context, m kafka.Message) error {
			r.record("dlq")
			r.mu.Lock()
			r.dlq = append(r.dlq, m)
			r.mu.Unlock()
			return nil
		},
	})
	return r
}

func (r *rig) record(what string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, what)
}

func (r *rig) snapshot() (log []string, dlq, commits int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.log...), len(r.dlq), r.commits
}

// audienceChange is a well-formed visibility edit: it reaches the rederive
// seam, which stands in for the Scylla deletes and the re-fan-out.
func audienceChange(t *testing.T) kafka.Message {
	t.Helper()
	return visibilityMessage(t, events.PostVisibilityChangedPayload{
		PostID: uuid.New().String(), AuthorID: uuid.New().String(),
		OldVisibility: "public", NewVisibility: "followers",
		Reason: events.VisibilityChangeAudience,
	})
}

func rawEvent(t *testing.T, eventType string, payload any) kafka.Message {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(events.EventEnvelope{
		EventID: uuid.New().String(), EventType: eventType, Payload: body,
	})
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Value: raw}
}

// The acceptance test of feed-06: a dependency that is down for longer than
// the old retry budget (four attempts) and then comes back. The event's
// effect is applied, nothing is dead-lettered, and the offset is committed
// only after the effect.
func TestTransientFailureIsRetriedUntilItSucceeds(t *testing.T) {
	r := newRig(t, time.Millisecond)
	const outageAttempts = 9 // the old consumer gave up after 4
	attempts := 0
	r.c.rederive = func(context.Context, events.PostVisibilityChangedPayload) error {
		attempts++
		if attempts <= outageAttempts {
			return errors.New("gocql: no hosts available in the pool")
		}
		r.record("effect")
		return nil
	}

	msg := audienceChange(t)
	r.inner.ProcessForTest(context.Background(), msg)

	log, dlq, commits := r.snapshot()
	if dlq != 0 {
		t.Fatalf("a transient failure was dead-lettered after %d attempts; the event is lost (log %v)", attempts, log)
	}
	if attempts != outageAttempts+1 {
		t.Fatalf("handler ran %d times, want %d (every failed attempt and the one that succeeded)", attempts, outageAttempts+1)
	}
	if want := []string{"effect", "commit"}; strings.Join(log, ",") != strings.Join(want, ",") {
		t.Fatalf("log = %v, want %v: the effect once, then the offset commit", log, want)
	}
	if commits != 1 {
		t.Fatalf("commits = %d, want 1", commits)
	}

	// Dedup is intact: a redelivery of the applied event is skipped (and
	// committed), not applied twice.
	r.inner.ProcessForTest(context.Background(), msg)
	log, _, commits = r.snapshot()
	if attempts != outageAttempts+1 {
		t.Fatalf("a redelivered, already-applied event ran the handler again (%d runs)", attempts)
	}
	if commits != 2 || log[len(log)-1] != "commit" {
		t.Fatalf("the redelivery was not committed: commits=%d log=%v", commits, log)
	}
}

// A message that is not an envelope at all, and an envelope whose payload
// can never be applied, each go to the DLQ exactly once, without a single
// retry, and the consumer then handles the next event.
func TestUndecodableEventIsDeadLetteredOnceAndTheConsumerMovesOn(t *testing.T) {
	// An hour of backoff: if either poison event were retried even once,
	// this test would hang instead of passing.
	r := newRig(t, time.Hour)
	applied := 0
	r.c.rederive = func(context.Context, events.PostVisibilityChangedPayload) error {
		applied++
		r.record("effect")
		return nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx := context.Background()
		r.inner.ProcessForTest(ctx, kafka.Message{Value: []byte(`{"event_id": not json`)})
		r.inner.ProcessForTest(ctx, rawEvent(t, events.PostCreated, "a string, not a PostCreated payload"))
		r.inner.ProcessForTest(ctx, rawEvent(t, events.PostDeleted, events.PostDeletedPayload{PostID: "not-a-uuid"}))
		r.inner.ProcessForTest(ctx, audienceChange(t))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the consumer is stuck behind an event that can never be applied: it was retried instead of dead-lettered")
	}

	log, dlq, commits := r.snapshot()
	want := []string{"dlq", "commit", "dlq", "commit", "dlq", "commit", "effect", "commit"}
	if strings.Join(log, ",") != strings.Join(want, ",") {
		t.Fatalf("log = %v, want %v", log, want)
	}
	if dlq != 3 || commits != 4 || applied != 1 {
		t.Fatalf("dlq=%d commits=%d applied=%d, want 3 / 4 / 1", dlq, commits, applied)
	}
	for i, m := range r.dlq {
		headers := map[string]string{}
		for _, h := range m.Headers {
			headers[h.Key] = string(h.Value)
		}
		if headers["x-dlq-error"] == "" || headers["x-dlq-consumer-group"] != "feed-service-group" ||
			headers["x-dlq-original-topic"] != "social.events.v1" {
			t.Fatalf("dead letter %d lacks the headers a replay needs: %v", i, headers)
		}
	}
}

// Retrying forever must not make a pod unstoppable. Cancelling the context
// during the wait between attempts returns at once, with the offset
// uncommitted and no receipt, so the next pod is handed the event again.
func TestShutdownDuringRetryReturnsPromptly(t *testing.T) {
	r := newRig(t, time.Hour) // the wait would outlast any test timeout
	firstAttempt := make(chan struct{})
	var once sync.Once
	down := true
	r.c.rederive = func(context.Context, events.PostVisibilityChangedPayload) error {
		once.Do(func() { close(firstAttempt) })
		if down {
			return errors.New("postgres: connection refused")
		}
		r.record("effect")
		return nil
	}

	msg := audienceChange(t)
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		r.inner.ProcessForTest(ctx, msg)
	}()
	<-firstAttempt
	cancel()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("the consumer did not stop within 2 s of its context being cancelled mid-retry; the pod cannot shut down")
	}

	log, dlq, commits := r.snapshot()
	if dlq != 0 || commits != 0 {
		t.Fatalf("shutdown mid-retry wrote dlq=%d commits=%d (log %v); the unapplied event must stay uncommitted", dlq, commits, log)
	}

	// The redelivery (next pod, dependency back) applies it.
	down = false
	r.inner.ProcessForTest(context.Background(), msg)
	log, dlq, commits = r.snapshot()
	if strings.Join(log, ",") != "effect,commit" || dlq != 0 || commits != 1 {
		t.Fatalf("after redelivery: log=%v dlq=%d commits=%d, want the effect then one commit", log, dlq, commits)
	}
}

// A handler that is itself blocked in a dependency call when shutdown comes
// is released by the same cancellation (the per-event context derives from
// the consumer's).
func TestShutdownReleasesAHandlerBlockedInADependency(t *testing.T) {
	r := newRig(t, time.Hour)
	entered := make(chan struct{})
	r.c.rederive = func(ctx context.Context, _ events.PostVisibilityChangedPayload) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		r.inner.ProcessForTest(ctx, audienceChange(t))
	}()
	<-entered
	cancel()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("a handler blocked in a dependency kept the consumer from stopping")
	}
	if _, dlq, commits := r.snapshot(); dlq != 0 || commits != 0 {
		t.Fatalf("a cancelled handler was dead-lettered or committed: dlq=%d commits=%d", dlq, commits)
	}
}

// A dependency that refuses the request itself is permanent: retried, it
// would hold the partition for good. One that is merely failing is not.
func TestRefusedRequestIsDeadLetteredAndAnOutageIsNot(t *testing.T) {
	refusal := &service.UpstreamStatusError{Service: "graph-service", Status: 400, Body: "OFFSET_TOO_LARGE"}

	r := newRig(t, time.Hour)
	r.c.rederive = func(context.Context, events.PostVisibilityChangedPayload) error {
		return fmt.Errorf("PostVisibilityChanged: re-fan-out: fanout incomplete, will be retried: %w",
			errors.Join(fmt.Errorf("fetch followers at offset 10100: %w", refusal)))
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.inner.ProcessForTest(context.Background(), audienceChange(t))
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a request graph-service refuses on every attempt is being retried forever; the partition is stuck")
	}
	if log, dlq, _ := r.snapshot(); dlq != 1 || strings.Join(log, ",") != "dlq,commit" {
		t.Fatalf("log=%v dlq=%d, want one dead letter then the commit", log, dlq)
	}
}

func TestClassify(t *testing.T) {
	status := func(code int) error {
		return &service.UpstreamStatusError{Service: "graph-service", Status: code}
	}
	pg := func(code string) error { return fmt.Errorf("record note: %w", &pgconn.PgError{Code: code}) }
	cases := []struct {
		name      string
		err       error
		permanent bool
	}{
		{"scylla outage", errors.New("gocql: no hosts available in the pool"), false},
		{"redis outage", errors.New("dial tcp 10.0.0.5:6379: connect: connection refused"), false},
		{"event deadline", fmt.Errorf("fanout incomplete: %w", context.DeadlineExceeded), false},
		{"shutdown", context.Canceled, false},
		{"partial fan-out", errors.New("fanout of post x incomplete, will be retried: 3 timeline write(s) failed"), false},
		{"relationship not answered", errors.New("graph-service returned no relationship for x"), false},
		{"graph 503", fmt.Errorf("fetch followers at offset 0: %w", status(503)), false},
		{"graph 401", status(401), false},
		{"graph 404", status(404), false},
		{"graph 429", status(429), false},
		{"graph 400", fmt.Errorf("fetch followers at offset 10100: %w", status(400)), true},
		{"graph 422", status(422), true},
		{"refusal and outage together", errors.Join(status(400), status(503)), false},
		{"postgres serialization failure", pg("40001"), false},
		{"postgres connection failure", pg("08006"), false},
		{"postgres too many connections", pg("53300"), false},
		{"postgres foreign key (can resolve)", pg("23503"), false},
		{"postgres invalid text", pg("22P02"), true},
		{"postgres bad byte sequence", pg("22021"), true},
		{"already permanent", sharedkafka.Permanent(errors.New("malformed")), true},
	}
	for _, tc := range cases {
		got := classify(tc.err)
		if got == nil {
			t.Errorf("%s: classify dropped the error", tc.name)
			continue
		}
		if sharedkafka.IsPermanent(got) != tc.permanent {
			t.Errorf("%s: permanent = %v, want %v", tc.name, !tc.permanent, tc.permanent)
		}
		if !errors.Is(got, tc.err) && got != tc.err {
			t.Errorf("%s: classify lost the cause: %v", tc.name, got)
		}
	}
	if classify(nil) != nil {
		t.Error("classify(nil) is not nil")
	}
}

// Every event type: a payload that cannot be applied is permanent, straight
// from the handler. A non-nil but empty timeline store gets the handlers
// that return early without one past that check; each of these returns
// before anything touches a store.
func TestMalformedEventsArePermanent(t *testing.T) {
	c := &Consumer{timelineStore: &scylla.TimelineStore{}}
	good := uuid.New().String()
	zero := uuid.Nil.String()
	cases := []struct {
		name      string
		eventType string
		payload   any
	}{
		{"PostCreated payload", events.PostCreated, "not an object"},
		{"PostCreated post_id", events.PostCreated, map[string]any{"post_id": "x", "author_id": good}},
		{"PostCreated author_id", events.PostCreated, map[string]any{"post_id": good, "author_id": "x"}},
		{"PostDeleted payload", events.PostDeleted, []int{1}},
		{"PostDeleted post_id", events.PostDeleted, map[string]any{"post_id": ""}},
		{"PostRestored post_id", events.PostRestored, map[string]any{"post_id": "x", "author_id": good}},
		{"PostRestored author_id", events.PostRestored, map[string]any{"post_id": good, "author_id": "x"}},
		{"PostVisibilityChanged payload", events.EventPostVisibilityChanged, 7},
		{"PostVisibilityChanged post_id", events.EventPostVisibilityChanged, map[string]any{"post_id": "x"}},
		{"PostVisibilityChanged author_id", events.EventPostVisibilityChanged, map[string]any{
			"post_id": good, "author_id": "x", "old_visibility": "public", "new_visibility": "private"}},
		{"PostCopyrightReview post_id", events.EventPostCopyrightReview, map[string]any{"post_id": "x"}},
		{"PostDistributionUpdated post_id", events.PostDistributionUpdated, map[string]any{"post_id": "x"}},
		{"PostContentTypeChanged post_id", events.PostContentTypeChanged, map[string]any{"post_id": "x"}},
		{"UploadDeleted post_id", events.UploadDeleted, map[string]any{"post_id": "x"}},
		{"PostReposted repost_id", events.EventPostReposted, map[string]any{"repost_id": "x", "original_post_id": good, "reposter_user_id": good}},
		{"PostReposted original_post_id", events.EventPostReposted, map[string]any{"repost_id": good, "original_post_id": "x", "reposter_user_id": good}},
		{"PostReposted reposter_user_id", events.EventPostReposted, map[string]any{"repost_id": good, "original_post_id": good, "reposter_user_id": "x"}},
		{"PostRepostUndone repost_id", events.EventPostRepostUndone, map[string]any{"repost_id": "x", "original_post_id": good, "reposter_user_id": good}},
		{"PostRepostUndone reposter_user_id", events.EventPostRepostUndone, map[string]any{"repost_id": good, "original_post_id": good, "reposter_user_id": "x"}},
		{"UserFollowed follower_id", events.UserFollowed, map[string]any{"follower_id": "x", "followee_id": good}},
		{"UserFollowed followee_id", events.UserFollowed, map[string]any{"follower_id": good, "followee_id": "x"}},
		{"UserUnfollowed payload", events.UserUnfollowed, "x"},
		{"UserUnfollowed follower_id", events.UserUnfollowed, map[string]any{"follower_id": "x", "followee_id": good}},
		{"UserUnfollowed all-zero follower", events.UserUnfollowed, map[string]any{"follower_id": zero, "followee_id": good}},
		{"UserUnfollowed all-zero followee", events.UserUnfollowed, map[string]any{"follower_id": good, "followee_id": zero}},
		{"UserDeletionRequested user_id", events.EventUserDeletionRequested, map[string]any{"user_id": "x"}},
		{"UserDeletionRequested all-zero user", events.EventUserDeletionRequested, map[string]any{"user_id": zero}},
		{"UserDeletionRequested payload", events.EventUserDeletionRequested, "x"},
		{"PostReacted payload", events.PostReacted, "x"},
		{"CommentCreated payload", events.CommentCreated, "x"},
		{"HandleChanged payload", events.HandleChanged, "x"},
		{"CrosspostRemoved payload", events.CrosspostRemoved, "x"},
		{"QAQuestionCreated payload", events.EventQAQuestionCreated, "x"},
		{"QAQuestionDeleted payload", events.EventQAQuestionDeleted, "x"},
	}
	for _, tc := range cases {
		msg := rawEvent(t, tc.eventType, tc.payload)
		var envelope events.EventEnvelope
		if err := json.Unmarshal(msg.Value, &envelope); err != nil {
			t.Fatal(err)
		}
		err := c.handle(context.Background(), &envelope)
		if err == nil {
			t.Errorf("%s: accepted (committed as applied)", tc.name)
			continue
		}
		if !sharedkafka.IsPermanent(err) {
			t.Errorf("%s: not permanent, so it would be retried forever and block its partition: %v", tc.name, err)
		}
	}
}

// What is skipped rather than dead-lettered stays skipped.
func TestSkippedEventsAreNotErrors(t *testing.T) {
	c := &Consumer{}
	for name, msg := range map[string]kafka.Message{
		"unknown event type":       rawEvent(t, "some.future.event", map[string]any{"x": 1}),
		"QA question with bad ids": rawEvent(t, events.EventQAQuestionCreated, map[string]any{"question_id": "x"}),
		"QA removal with a bad id": rawEvent(t, events.EventQAQuestionDeleted, map[string]any{"question_id": "x"}),
	} {
		var envelope events.EventEnvelope
		if err := json.Unmarshal(msg.Value, &envelope); err != nil {
			t.Fatal(err)
		}
		if err := c.handle(context.Background(), &envelope); err != nil {
			t.Errorf("%s: err = %v, want it skipped", name, err)
		}
	}
}
