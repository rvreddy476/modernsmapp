package scylla

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// TimelineTS must be a pure function of (post, created_at): that is what
// makes a re-write of a post an upsert instead of a second timeline row.

func TestTimelineTS_SamePostSameKeyAcrossWriters(t *testing.T) {
	post := uuid.New()
	// Fan-out has the event's nanoseconds; the follow backfill re-reads
	// created_at from Scylla, which stores milliseconds. Same key.
	fromEvent := time.Date(2026, 10, 13, 9, 30, 15, 123456789, time.UTC)
	fromScylla := fromEvent.Truncate(time.Millisecond)

	a, b := TimelineTS(post, fromEvent), TimelineTS(post, fromEvent)
	if a != b {
		t.Fatalf("two writes of one post derived different keys: %s vs %s", a, b)
	}
	if c := TimelineTS(post, fromScylla); c != a {
		t.Fatalf("backfill key %s differs from fan-out key %s for the same post", c, a)
	}
	// Same instant in another zone is the same instant.
	if d := TimelineTS(post, fromEvent.In(time.FixedZone("IST", 5*3600+1800))); d != a {
		t.Fatalf("time zone changed the key: %s vs %s", d, a)
	}
}

func TestTimelineTS_TimestampOrdersLikeCreatedAt(t *testing.T) {
	post := uuid.New()
	at := time.Date(2026, 10, 13, 9, 30, 15, 123000000, time.UTC)
	ts := TimelineTS(post, at)
	if ts.Version() != 1 {
		t.Fatalf("key is a v%d UUID, want a v1 timeuuid", ts.Version())
	}
	if got := ts.Time(); !got.Equal(at) {
		t.Fatalf("key carries time %s, want %s", got, at)
	}
	if later := TimelineTS(uuid.New(), at.Add(time.Millisecond)); later.Timestamp() <= ts.Timestamp() {
		t.Fatal("a later post did not get a later key timestamp")
	}
}

func TestTimelineTS_DifferentPostsSameMillisecondDiffer(t *testing.T) {
	at := time.Date(2026, 10, 13, 9, 30, 15, 0, time.UTC)
	seen := map[string]bool{}
	for i := 0; i < 10000; i++ {
		k := TimelineTS(uuid.New(), at).String()
		if seen[k] {
			t.Fatalf("two posts created in the same millisecond collided on key %s", k)
		}
		seen[k] = true
	}
}
