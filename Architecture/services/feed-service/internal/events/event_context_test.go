package events

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/feed-service/internal/service"
	"github.com/atpost/shared/events"
)

// Audit H2/H3: every handler runs with a deadline, on the fan-out clients,
// and with the event id that keys chunk progress.
func TestEventContext_DeadlineBackgroundAndKey(t *testing.T) {
	c := &Consumer{deadline: 3 * time.Second}
	ctx, cancel := c.eventContext(context.Background(), events.EventEnvelope{EventID: "evt-42"})
	defer cancel()
	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("an event handler runs without a deadline: one slow fan-out blocks the partition")
	}
	if left := time.Until(dl); left > 3*time.Second || left < 2*time.Second {
		t.Fatalf("deadline in %v, want about 3s", left)
	}
	if !service.IsBackgroundTraffic(ctx) {
		t.Fatal("event work is not marked background: it would share the request path's breakers")
	}
	if got := service.FanoutKeyFrom(ctx); got != "evt-42" {
		t.Fatalf("fan-out key = %q, want the event id", got)
	}

	// No configured deadline still gets the default.
	ctx2, cancel2 := (&Consumer{}).eventContext(context.Background(), events.EventEnvelope{})
	defer cancel2()
	if dl, ok := ctx2.Deadline(); !ok || time.Until(dl) > DefaultEventDeadline {
		t.Fatal("a consumer without a configured deadline runs handlers unbounded")
	}
}

func TestEventDeadline_Env(t *testing.T) {
	t.Setenv("FEED_EVENT_DEADLINE", "")
	if eventDeadline() != DefaultEventDeadline {
		t.Fatal("unset FEED_EVENT_DEADLINE must use the default")
	}
	t.Setenv("FEED_EVENT_DEADLINE", "45s")
	if eventDeadline() != 45*time.Second {
		t.Fatal("FEED_EVENT_DEADLINE=45s was ignored")
	}
	t.Setenv("FEED_EVENT_DEADLINE", "-1s")
	if eventDeadline() != DefaultEventDeadline {
		t.Fatal("a negative deadline must fall back to the default")
	}
}
