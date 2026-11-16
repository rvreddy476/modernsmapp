package events

import (
	"context"
	"testing"

	"github.com/atpost/feed-service/internal/service"
	"github.com/google/uuid"
)

// feed-28: a retried visibility change must not remove the rows its earlier
// attempt already wrote back. The removal is skipped exactly when that
// attempt's fan-out progress exists.
func TestRefanoutUnderWay(t *testing.T) {
	c, mr := staleConsumer(t)
	post := uuid.New()
	ctx := service.WithFanoutKey(context.Background(), "evt-1")

	if c.refanoutUnderWay(ctx, post) {
		t.Fatal("first attempt (no saved progress) must remove the old rows")
	}

	mr.HSet(service.FanoutProgressKey(post, "evt-1"), "followers", "k1:100:cursor")
	if !c.refanoutUnderWay(ctx, post) {
		t.Fatal("a retry with saved progress removed the rows again: the rows written before the saved position are lost")
	}

	// Progress of another event, or of another post, is not this attempt's.
	if c.refanoutUnderWay(service.WithFanoutKey(context.Background(), "evt-2"), post) {
		t.Fatal("another event's progress must not skip this event's removal")
	}
	if c.refanoutUnderWay(ctx, uuid.New()) {
		t.Fatal("another post's progress must not skip this post's removal")
	}

	// No event id: nothing is saved, so every attempt starts from scratch.
	if c.refanoutUnderWay(context.Background(), post) {
		t.Fatal("without an event id the removal must run")
	}
}

// When Redis cannot answer, the removal runs: resuming over rows that may
// just have been deleted is the failure this guards against.
func TestRefanoutUnderWay_RedisDownMeansRemove(t *testing.T) {
	c, mr := staleConsumer(t)
	post := uuid.New()
	mr.HSet(service.FanoutProgressKey(post, "evt-1"), "followers", "k1:100:cursor")
	mr.Close()

	if c.refanoutUnderWay(service.WithFanoutKey(context.Background(), "evt-1"), post) {
		t.Fatal("unknown progress must not skip the removal")
	}
	if (&Consumer{}).refanoutUnderWay(service.WithFanoutKey(context.Background(), "evt-1"), post) {
		t.Fatal("no Redis client: the removal must run")
	}
}
