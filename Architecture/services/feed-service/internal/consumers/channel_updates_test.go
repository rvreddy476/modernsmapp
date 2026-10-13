package consumers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

// The offset of a feed-inject message is committed only after the message
// was applied or its retries were spent — never before the handler runs,
// which is what ReadMessage used to do.

type applyRecorder struct {
	failures int // the handler fails this many times, then succeeds
	calls    int
	order    []string
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

func TestApply_GivesUpAfterBoundedAttemptsAndCommits(t *testing.T) {
	r := &applyRecorder{failures: 1000}
	if !newApplyConsumer(r).apply(context.Background(), kafka.Message{}) {
		t.Fatal("apply reported cancellation")
	}
	if r.calls != channelInjectAttempts {
		t.Fatalf("handler ran %d times, want %d", r.calls, channelInjectAttempts)
	}
	if last := r.order[len(r.order)-1]; last != "commit" {
		t.Fatalf("last step = %q, want commit", last)
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
	for _, step := range r.order {
		if step == "commit" {
			t.Fatal("committed a message that was never applied")
		}
	}
}
