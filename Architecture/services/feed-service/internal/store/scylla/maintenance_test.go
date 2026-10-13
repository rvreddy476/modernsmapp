package scylla

import (
	"math"
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

var maintNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func homeRow(owner uuid.UUID, post uuid.UUID, created time.Time, ts gocql.UUID, hasTTL bool) TimelineRow {
	pid := gocql.UUID(post)
	return TimelineRow{
		Kind: "home", Owner: gocql.UUID(owner), Bucket: bucket(created), TS: ts,
		PostID: &pid, AuthorID: gocql.UUID(uuid.New()), CreatedAt: created.Truncate(time.Millisecond),
		ContentType: "post", HasTTL: hasTTL,
	}
}

func countOps(plan []MaintenanceAction) map[string]int {
	out := map[string]int{}
	for _, a := range plan {
		out[a.Op+"/"+a.Why]++
	}
	return out
}

// The 2026-09-09 shape: one post, several rows at clock-sequence timeuuids,
// none at the canonical key. One canonical row is written and indexed
// BEFORE the copies are deleted.
func TestPlanCollapsesDuplicatesOntoTheCanonicalKey(t *testing.T) {
	owner, post := uuid.New(), uuid.New()
	created := maintNow.Add(-48 * time.Hour)
	rows := []TimelineRow{
		homeRow(owner, post, created, gocql.UUIDFromTime(created), false),
		homeRow(owner, post, created, gocql.UUIDFromTime(created), false),
		homeRow(owner, post, created, gocql.UUIDFromTime(created), false),
	}
	plan := PlanPartition(rows, maintNow)

	if got := countOps(plan); got["write/duplicate"] != 1 || got["index/index"] != 1 || got["delete/duplicate"] != 3 || len(plan) != 5 {
		t.Fatalf("plan = %v", got)
	}
	if plan[0].Op != OpWrite || plan[1].Op != OpIndex {
		t.Fatalf("the kept row must be written and indexed before any delete; plan starts %s, %s", plan[0].Op, plan[1].Op)
	}
	want := TimelineTS(post, created)
	if plan[0].Row.TS != want || plan[1].Row.TS != want {
		t.Fatalf("kept row at %s, want TimelineTS %s", plan[0].Row.TS, want)
	}
	if left := int(TimelineRetention/time.Second) - 2*86400; plan[0].TTL != left {
		t.Fatalf("TTL = %d, want %d (360 days from created_at)", plan[0].TTL, left)
	}
}

// A duplicate next to a row already at the canonical key: keep that one,
// delete the other, give the kept row a TTL when it has none.
func TestPlanKeepsTheCanonicalRow(t *testing.T) {
	owner, post := uuid.New(), uuid.New()
	created := maintNow.Add(-time.Hour)
	canonical := homeRow(owner, post, created, TimelineTS(post, created), false)
	dup := homeRow(owner, post, created, gocql.UUIDFromTime(created), false)

	got := countOps(PlanPartition([]TimelineRow{dup, canonical}, maintNow))
	if got["write/ttl"] != 1 || got["index/index"] != 1 || got["delete/duplicate"] != 1 || got["write/duplicate"] != 0 {
		t.Fatalf("plan = %v", got)
	}
}

// A clean row — canonical key, TTL already set — is only (re)indexed.
func TestPlanLeavesACleanRowAlone(t *testing.T) {
	owner, post := uuid.New(), uuid.New()
	created := maintNow.Add(-time.Hour)
	plan := PlanPartition([]TimelineRow{homeRow(owner, post, created, TimelineTS(post, created), true)}, maintNow)
	if len(plan) != 1 || plan[0].Op != OpIndex {
		t.Fatalf("plan = %v", countOps(plan))
	}
}

// A single row at an old-style key is moved to the canonical key, so the
// next live re-write upserts it instead of adding a copy.
func TestPlanRekeysALoneLegacyRow(t *testing.T) {
	owner, post := uuid.New(), uuid.New()
	created := maintNow.Add(-24 * time.Hour)
	got := countOps(PlanPartition([]TimelineRow{homeRow(owner, post, created, gocql.UUIDFromTime(created), false)}, maintNow))
	if got["write/rekey"] != 1 || got["delete/rekey"] != 1 || got["index/index"] != 1 {
		t.Fatalf("plan = %v", got)
	}
}

func TestPlanDeletesExpiredPostsAndGhosts(t *testing.T) {
	owner := uuid.New()
	old := maintNow.Add(-400 * 24 * time.Hour)
	expired := uuid.New()
	ghost := TimelineRow{Kind: "home", Owner: gocql.UUID(owner), Bucket: bucket(maintNow), TS: gocql.UUIDFromTime(maintNow), ContentType: "reel"}
	rows := []TimelineRow{
		homeRow(owner, expired, old, TimelineTS(expired, old), false),
		homeRow(owner, expired, old, gocql.UUIDFromTime(old), false),
		ghost,
	}
	got := countOps(PlanPartition(rows, maintNow))
	if got["delete/expired"] != 2 || got["delete/ghost"] != 1 || len(got) != 2 {
		t.Fatalf("plan = %v", got)
	}
}

// A row whose created_at is null falls back to the time in its key.
func TestPlanUsesTheKeyTimeWhenCreatedAtIsNull(t *testing.T) {
	owner, post := uuid.New(), uuid.New()
	created := maintNow.Add(-time.Hour).Truncate(time.Millisecond)
	r := homeRow(owner, post, created, TimelineTS(post, created), true)
	r.CreatedAt = time.Time{}
	plan := PlanPartition([]TimelineRow{r}, maintNow)
	if len(plan) != 1 || plan[0].Op != OpIndex {
		t.Fatalf("plan = %v", countOps(plan))
	}
}

func TestSplitTokenRingCoversTheRingWithoutGaps(t *testing.T) {
	for _, n := range []int{1, 3, 16, 4096} {
		ranges := SplitTokenRing(n)
		if len(ranges) != n {
			t.Fatalf("n=%d: %d ranges", n, len(ranges))
		}
		if ranges[0].Start != math.MinInt64 || ranges[n-1].End != math.MaxInt64 {
			t.Fatalf("n=%d: ring is [%d, %d]", n, ranges[0].Start, ranges[n-1].End)
		}
		for i := 1; i < n; i++ {
			if ranges[i].Start != ranges[i-1].End+1 {
				t.Fatalf("n=%d: gap or overlap between range %d (%d) and %d (%d)", n, i-1, ranges[i-1].End, i, ranges[i].Start)
			}
			if ranges[i].End < ranges[i].Start {
				t.Fatalf("n=%d: range %d is empty", n, i)
			}
		}
	}
}
