package consumers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

// The offset of a feed-inject message is committed only after the message
// was applied: never before the handler runs (what ReadMessage used to do),
// and never after giving up (what the 5-attempt loop used to do: the topic
// has no dead letter, so a Scylla outage longer than ~15 s silently dropped
// every channel update that arrived during it).

type applyRecorder struct {
	failures int // the handler fails this many times, then succeeds
	calls    int
	order    []string
	waits    []time.Duration
}

func newApplyConsumer(r *applyRecorder) *ChannelUpdateConsumer {
	return &ChannelUpdateConsumer{
		backoff: time.Millisecond,
		handle: func(context.Context, kafka.Message) error {
			r.calls++
			r.order = append(r.order, "handle")
			if r.calls <= r.failures {
				return errors.New("scylla timeout")
			}
			return nil
		},
		commit: func(context.Context, kafka.Message) error {
			r.order = append(r.order, "commit")
			return nil
		},
	}
}

// instantSleep records each wait and returns at once.
func instantSleep(r *applyRecorder) func(context.Context, time.Duration) bool {
	return func(ctx context.Context, d time.Duration) bool {
		r.waits = append(r.waits, d)
		return ctx.Err() == nil
	}
}

func commits(r *applyRecorder) int {
	n := 0
	for _, step := range r.order {
		if step == "commit" {
			n++
		}
	}
	return n
}

func TestApply_RetriesThenCommitsAfterSuccess(t *testing.T) {
	r := &applyRecorder{failures: 2}
	if !newApplyConsumer(r).apply(context.Background(), kafka.Message{}) {
		t.Fatal("apply reported cancellation")
	}
	want := []string{"handle", "handle", "handle", "commit"}
	if len(r.order) != len(want) {
		t.Fatalf("order = %v, want %v", r.order, want)
	}
	for i := range want {
		if r.order[i] != want[i] {
			t.Fatalf("order = %v, want %v", r.order, want)
		}
	}
}

// Replaces TestApply_GivesUpAfterBoundedAttemptsAndCommits. An outage far
// longer than the old five attempts: the message is never committed while
// it is failing, and is applied exactly once and committed once when the
// store is back.
func TestApply_OutlastsALongOutageAndAppliesOnce(t *testing.T) {
	const outage = 200 // attempts; the old loop gave up and committed at 5
	r := &applyRecorder{failures: outage}
	c := newApplyConsumer(r)
	c.backoff = time.Second
	c.sleepFn = instantSleep(r)
	committedWhileFailing := false
	inner := c.handle
	c.handle = func(ctx context.Context, m kafka.Message) error {
		if commits(r) > 0 {
			committedWhileFailing = true
		}
		return inner(ctx, m)
	}

	if !c.apply(context.Background(), kafka.Message{}) {
		t.Fatal("apply reported cancellation")
	}
	if committedWhileFailing {
		t.Fatal("the offset was committed while the message was still failing: the update is lost")
	}
	if r.calls != outage+1 {
		t.Fatalf("handler ran %d times, want %d (every failure, then the one success)", r.calls, outage+1)
	}
	if n := commits(r); n != 1 || r.order[len(r.order)-1] != "commit" {
		t.Fatalf("commits = %d, last step %q; want exactly one commit, after the success", n, r.order[len(r.order)-1])
	}
	// Exactly one handler call succeeded, so the update was applied once.
	if succeeded := r.calls - r.failures; succeeded != 1 {
		t.Fatalf("the update was applied %d times, want once", succeeded)
	}
}

func TestApply_BackoffDoublesToTheCap(t *testing.T) {
	r := &applyRecorder{failures: 9}
	c := newApplyConsumer(r)
	c.backoff = time.Second
	c.sleepFn = instantSleep(r)
	if !c.apply(context.Background(), kafka.Message{}) {
		t.Fatal("apply reported cancellation")
	}
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30, 30, 30}
	if len(r.waits) != len(want) {
		t.Fatalf("waits = %v, want %d of them", r.waits, len(want))
	}
	for i, w := range want {
		if r.waits[i] != w*time.Second {
			t.Fatalf("wait %d = %v, want %v (doubling from 1 s, capped at %v)", i+1, r.waits[i], w*time.Second, channelInjectMaxBackoff)
		}
	}
}

func TestApply_FailsNTimesThenSucceedsAppliesOnce(t *testing.T) {
	for _, n := range []int{1, 4, 5, 6, 25} { // 5 was the old give-up point
		r := &applyRecorder{failures: n}
		c := newApplyConsumer(r)
		c.sleepFn = instantSleep(r)
		if !c.apply(context.Background(), kafka.Message{}) {
			t.Fatalf("%d failures: apply reported cancellation", n)
		}
		if r.calls != n+1 || commits(r) != 1 {
			t.Fatalf("%d failures: handler ran %d times with %d commit(s); want %d and 1", n, r.calls, commits(r), n+1)
		}
	}
}

func TestApply_CancelledMidRetryDoesNotCommit(t *testing.T) {
	r := &applyRecorder{failures: 1000}
	c := newApplyConsumer(r)
	c.backoff = time.Hour // the first retry waits; cancellation must win
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c.apply(ctx, kafka.Message{}) {
		t.Fatal("apply reported success on a cancelled context")
	}
	if commits(r) != 0 {
		t.Fatal("committed a message that was never applied")
	}
}

// Shutdown while a message is waiting out its backoff (a real timer, an
// hour long): apply returns promptly and commits nothing, so the message is
// redelivered to the next pod.
func TestApply_CancelDuringTheWaitReturnsPromptlyWithoutCommit(t *testing.T) {
	r := &applyRecorder{failures: 1000}
	c := newApplyConsumer(r)
	c.backoff = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() { done <- c.apply(ctx, kafka.Message{}) }()

	time.Sleep(20 * time.Millisecond) // let it fail once and start waiting
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("apply reported success after cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("apply did not return within 2 s of cancellation")
	}
	if commits(r) != 0 {
		t.Fatal("committed a message that was never applied")
	}
}

// A handler that fails BECAUSE the context was cancelled (the Scylla call
// was interrupted by shutdown) must not be slept on or committed.
func TestApply_HandlerInterruptedByShutdownDoesNotCommit(t *testing.T) {
	r := &applyRecorder{}
	c := newApplyConsumer(r)
	ctx, cancel := context.WithCancel(context.Background())
	c.handle = func(ctx context.Context, _ kafka.Message) error {
		r.calls++
		cancel()
		return ctx.Err()
	}
	c.sleepFn = func(context.Context, time.Duration) bool {
		t.Error("slept after shutdown")
		return true
	}
	if c.apply(ctx, kafka.Message{}) {
		t.Fatal("apply reported success after shutdown")
	}
	if r.calls != 1 || commits(r) != 0 {
		t.Fatalf("handler ran %d times with %d commit(s); want 1 and 0", r.calls, commits(r))
	}
}

// Malformed messages are still skipped: processMessage returns nil for
// them, so they are committed on the first pass and cannot stall the
// partition now that failures are retried without limit.
func TestProcessMessage_MalformedMessagesAreSkippedNotRetried(t *testing.T) {
	c := &ChannelUpdateConsumer{} // no store, no redis: reaching either would panic
	for name, value := range map[string]string{
		"not json":          `{"event_type":`,
		"no target":         `{"payload":{"item_id":"11111111-1111-4111-8111-111111111111","source_id":"22222222-2222-4222-8222-222222222222"}}`,
		"no item":           `{"payload":{"target_user_id":"11111111-1111-4111-8111-111111111111","source_id":"22222222-2222-4222-8222-222222222222"}}`,
		"target not a uuid": `{"payload":{"target_user_id":"x","item_id":"11111111-1111-4111-8111-111111111111","source_id":"22222222-2222-4222-8222-222222222222"}}`,
		"item not a uuid":   `{"payload":{"target_user_id":"11111111-1111-4111-8111-111111111111","item_id":"x","source_id":"22222222-2222-4222-8222-222222222222"}}`,
		"source not a uuid": `{"payload":{"target_user_id":"11111111-1111-4111-8111-111111111111","item_id":"33333333-3333-4333-8333-333333333333","source_id":"x"}}`,
	} {
		if err := c.processMessage(context.Background(), kafka.Message{Value: []byte(value)}); err != nil {
			t.Errorf("%s: err = %v; a malformed message must be skipped (nil), or it is retried for ever", name, err)
		}
	}
}
