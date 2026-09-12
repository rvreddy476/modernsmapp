package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/feed-service/internal/ranking"
	"github.com/atpost/feed-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
)

// tube.channel.subscribed / unsubscribed drop the subscriber's cached
// subscribed-owner set (and its looked marker), so the Subscriptions tab
// and the ranker's boost rebuild it on the next request instead of
// serving up to six hours of the old answer.

func subscriptionMessage(t *testing.T, eventType string, p events.ChannelSubscriptionPayload) kafka.Message {
	t.Helper()
	payload, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	value, err := json.Marshal(events.EventEnvelope{
		EventID:    uuid.New().String(),
		EventType:  eventType,
		OccurredAt: time.Now(),
		Payload:    payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Value: value}
}

func TestChannelSubscriptionEvents_DeleteTheCachedSet(t *testing.T) {
	for _, eventType := range []string{events.TubeChannelSubscribed, events.TubeChannelUnsubscribed} {
		t.Run(eventType, func(t *testing.T) {
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			subscriber, owner, bystander := uuid.New(), uuid.New(), uuid.New()
			setKey := ranking.SubscribedOwnersKey(subscriber)
			lookedKey := "feed:subscriptions:looked:" + subscriber.String()
			for _, k := range []string{setKey, ranking.SubscribedOwnersKey(bystander)} {
				if _, err := mr.SAdd(k, owner.String()); err != nil {
					t.Fatal(err)
				}
			}
			if err := mr.Set(lookedKey, "1"); err != nil {
				t.Fatal(err)
			}

			c := &Consumer{service: service.New(nil, nil, rdb), rdb: rdb}
			msg := subscriptionMessage(t, eventType, events.ChannelSubscriptionPayload{
				ChannelID:    uuid.New().String(),
				OwnerID:      owner.String(),
				SubscriberID: subscriber.String(),
				NotifyOn:     "all",
				OccurredAt:   time.Now(),
			})
			if err := c.processMessage(context.Background(), msg); err != nil {
				t.Fatal(err)
			}
			if mr.Exists(setKey) {
				t.Fatalf("%s survived %s", setKey, eventType)
			}
			if mr.Exists(lookedKey) {
				t.Fatalf("the looked marker survived %s; an emptied set would read as \"none\" until the TTL", eventType)
			}
			if !mr.Exists(ranking.SubscribedOwnersKey(bystander)) {
				t.Fatal("another viewer's set was deleted")
			}
		})
	}
}

// A payload without a usable subscriber id is a bad message, not a silent
// no-op: the consumer logs the failure rather than leaving a stale set
// with nothing to say why.
func TestChannelSubscriptionEvents_BadSubscriberIsAnError(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	c := &Consumer{service: service.New(nil, nil, rdb), rdb: rdb}
	msg := subscriptionMessage(t, events.TubeChannelSubscribed, events.ChannelSubscriptionPayload{SubscriberID: "nope"})
	if err := c.processMessage(context.Background(), msg); err == nil {
		t.Fatal("a malformed subscriber_id must be an error")
	}
}
