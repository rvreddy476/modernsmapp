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

// PostUnreacted brings the velocity counter back down — never below zero —
// and takes the reactor out of the likers set.
func TestPostUnreacted_DecrementsVelocityWithAFloor(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	c := &Consumer{rdb: rdb}

	post, reactor := uuid.New().String(), uuid.New().String()
	counter, likers := "post:counters:"+post+":likes", "post:likers:"+post
	if err := mr.Set(counter, "2"); err != nil {
		t.Fatal(err)
	}
	if _, err := mr.SAdd(likers, reactor); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(events.PostReactedPayload{PostID: post, ReactorID: reactor})
	raw, _ := json.Marshal(events.EventEnvelope{EventType: events.PostUnreacted, Payload: payload})
	msg := kafka.Message{Value: raw}

	for i := 0; i < 4; i++ { // more un-reacts than reactions were counted
		if err := c.processMessage(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := mr.Get(counter); got != "0" {
		t.Fatalf("counter = %q after over-decrement, want 0", got)
	}
	if ok, _ := mr.SIsMember(likers, reactor); ok {
		t.Fatal("reactor still in the likers set")
	}
}
