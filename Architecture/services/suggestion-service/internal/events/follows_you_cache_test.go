package events

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

/*
	"Follows you" (25 Oct 2026): the read path merges the viewer's live
	followers into their friend list on a cache miss, so the follow events
	must clear the right caches:

	  - someone follows the viewer → the viewer's (followee's) friend cache,
	    so the new follower appears now;
	  - the viewer follows back → the viewer's (follower's) friend cache, so
	    the card leaves;
	  - someone unfollows the viewer → the viewer's friend cache, so a stale
	    "Follows you" card leaves.

	No store is set: none of these handlers may need a database read.
*/

func followsYouConsumer(t *testing.T) (*Consumer, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return &Consumer{rdb: rdb}, mr
}

func followEnvelope(t *testing.T, eventType string, follower, followee uuid.UUID) sharedevents.EventEnvelope {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"follower_id": follower.String(), "followee_id": followee.String()})
	if err != nil {
		t.Fatal(err)
	}
	return sharedevents.EventEnvelope{EventType: eventType, Payload: payload}
}

func seedCaches(t *testing.T, mr *miniredis.Miniredis, ids ...uuid.UUID) {
	t.Helper()
	for _, id := range ids {
		for _, typ := range []string{"friend", "follow"} {
			if err := mr.Set("suggestions:"+id.String()+":"+typ, "[]"); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestFollowClearsBothSidesFriendCaches(t *testing.T) {
	c, mr := followsYouConsumer(t)
	follower, followee := uuid.New(), uuid.New()
	seedCaches(t, mr, follower, followee)

	if err := c.dispatch(context.Background(), followEnvelope(t, sharedevents.UserFollowed, follower, followee)); err != nil {
		t.Fatal(err)
	}
	for key, why := range map[string]string{
		"suggestions:" + followee.String() + ":friend": "the person followed has a new follower: their friend list must show \"Follows you\" now",
		"suggestions:" + follower.String() + ":friend": "a follow back must take the \"Follows you\" card out of the follower's list",
		"suggestions:" + follower.String() + ":follow": "the followee must leave the follower's follow list",
	} {
		if mr.Exists(key) {
			t.Errorf("%s not cleared: %s", key, why)
		}
	}
}

func TestUnfollowClearsTheFolloweesFriendCache(t *testing.T) {
	c, mr := followsYouConsumer(t)
	follower, followee := uuid.New(), uuid.New()
	seedCaches(t, mr, follower, followee)

	if err := c.dispatch(context.Background(), followEnvelope(t, sharedevents.UserUnfollowed, follower, followee)); err != nil {
		t.Fatal(err)
	}
	if mr.Exists("suggestions:" + followee.String() + ":friend") {
		t.Error("the unfollowed person's friend cache was not cleared: a stale \"Follows you\" card stays up to an hour")
	}
	if mr.Exists("suggestions:" + follower.String() + ":follow") {
		t.Error("the unfollower's follow cache was not cleared")
	}
}
