package events

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// PostUnreacted takes the reactor out of the likers set and, only if they
// were in it, brings the velocity counter down by one (never below zero).
// Applying the same event again changes nothing.

func unreactFixture(t *testing.T) (*miniredis.Miniredis, *Consumer) {
	t.Helper()
	mr := miniredis.RunT(t)
	return mr, &Consumer{rdb: redis.NewClient(&redis.Options{Addr: mr.Addr()})}
}

func unreactMessage(post, reactor string) kafka.Message {
	payload, _ := json.Marshal(events.PostReactedPayload{PostID: post, ReactorID: reactor})
	raw, _ := json.Marshal(events.EventEnvelope{EventID: uuid.NewString(), EventType: events.PostUnreacted, Payload: payload})
	return kafka.Message{Value: raw}
}

func counterOf(t *testing.T, mr *miniredis.Miniredis, post string) string {
	t.Helper()
	got, err := mr.Get("post:counters:" + post + ":likes")
	if err != nil {
		return "(absent)"
	}
	return got
}

// The retry case. Three people liked the post; one takes theirs back and
// the event is applied four times (retries are unbounded, and post-service
// may publish it twice). It used to take four off the counter, erasing the
// other two people's likes; it must take one.
func TestPostUnreacted_AppliedRepeatedlyDecrementsOnce(t *testing.T) {
	mr, c := unreactFixture(t)
	post, reactor := uuid.NewString(), uuid.NewString()
	others := []string{uuid.NewString(), uuid.NewString()}
	_ = mr.Set("post:counters:"+post+":likes", "3")
	_, _ = mr.SAdd("post:likers:"+post, reactor, others[0], others[1])

	msg := unreactMessage(post, reactor)
	for i := 0; i < 4; i++ {
		if err := c.processMessage(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
	}
	if got := counterOf(t, mr, post); got != "2" {
		t.Fatalf("counter = %s after one un-like applied four times, want 2 (one decrement)", got)
	}
	if ok, _ := mr.SIsMember("post:likers:"+post, reactor); ok {
		t.Fatal("reactor still in the likers set")
	}
	for _, o := range others {
		if ok, _ := mr.SIsMember("post:likers:"+post, o); !ok {
			t.Fatal("another person's like was removed from the likers set")
		}
	}
}

// A Redis failure is an error (the event is retried), and the retries
// after Redis is back apply the un-like exactly once between them.
func TestPostUnreacted_FailsThenRetriesApplyOnce(t *testing.T) {
	mr, c := unreactFixture(t)
	post, reactor := uuid.NewString(), uuid.NewString()
	_ = mr.Set("post:counters:"+post+":likes", "5")
	_, _ = mr.SAdd("post:likers:"+post, reactor, uuid.NewString())
	msg := unreactMessage(post, reactor)

	mr.SetError("LOADING Redis is loading the dataset in memory")
	for i := 0; i < 3; i++ {
		if err := c.processMessage(context.Background(), msg); err == nil {
			t.Fatal("a Redis failure was swallowed: the event is committed and the like is counted for a week")
		}
	}
	mr.SetError("")
	if got := counterOf(t, mr, post); got != "5" {
		t.Fatalf("counter = %s after three failed attempts, want 5 (untouched)", got)
	}
	for i := 0; i < 3; i++ { // the retry that lands, and two redeliveries
		if err := c.processMessage(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
	}
	if got := counterOf(t, mr, post); got != "4" {
		t.Fatalf("counter = %s, want 4: exactly one decrement across every attempt", got)
	}
}

// An un-react this service never counted (the reactor is not in the likers
// set: the like predates the counter, or the keys expired) leaves the
// counter alone, and the counter never goes below zero.
func TestPostUnreacted_UncountedReactionAndFloor(t *testing.T) {
	mr, c := unreactFixture(t)
	post, stranger, liker := uuid.NewString(), uuid.NewString(), uuid.NewString()
	_ = mr.Set("post:counters:"+post+":likes", "2")
	_, _ = mr.SAdd("post:likers:"+post, liker)

	if err := c.processMessage(context.Background(), unreactMessage(post, stranger)); err != nil {
		t.Fatal(err)
	}
	if got := counterOf(t, mr, post); got != "2" {
		t.Fatalf("counter = %s after an un-react that was never counted, want 2", got)
	}

	// Floor: the counter is already 0 (expired and recreated, say) while the
	// reactor is still in the set.
	_ = mr.Set("post:counters:"+post+":likes", "0")
	if err := c.processMessage(context.Background(), unreactMessage(post, liker)); err != nil {
		t.Fatal(err)
	}
	if got := counterOf(t, mr, post); got != "0" {
		t.Fatalf("counter = %s, want 0 (never below zero)", got)
	}

	// No counter at all: nothing is created.
	other, reactor := uuid.NewString(), uuid.NewString()
	_, _ = mr.SAdd("post:likers:"+other, reactor)
	if err := c.processMessage(context.Background(), unreactMessage(other, reactor)); err != nil {
		t.Fatal(err)
	}
	if got := counterOf(t, mr, other); got != "(absent)" {
		t.Fatalf("an un-react created counter %s for a post that had none", got)
	}
}

// A like, an un-like and a like again each count: the set is per reactor,
// not a one-way latch.
func TestPostUnreacted_ReactUnreactReactAgain(t *testing.T) {
	mr, c := unreactFixture(t)
	post, reactor := uuid.NewString(), uuid.NewString()
	react := func() {
		t.Helper()
		payload, _ := json.Marshal(events.PostReactedPayload{PostID: post, ReactorID: reactor, ReactType: "like"})
		raw, _ := json.Marshal(events.EventEnvelope{EventID: uuid.NewString(), EventType: events.PostReacted, Payload: payload})
		if err := c.processMessage(context.Background(), kafka.Message{Value: raw}); err != nil {
			t.Fatal(err)
		}
	}
	unreact := func() {
		t.Helper()
		if err := c.processMessage(context.Background(), unreactMessage(post, reactor)); err != nil {
			t.Fatal(err)
		}
	}
	react()
	unreact()
	react()
	if got := counterOf(t, mr, post); got != "1" {
		t.Fatalf("counter = %s after like, un-like, like; want 1", got)
	}
	unreact()
	unreact()
	if got := counterOf(t, mr, post); got != "0" {
		t.Fatalf("counter = %s after the second un-like (applied twice); want 0", got)
	}
}
