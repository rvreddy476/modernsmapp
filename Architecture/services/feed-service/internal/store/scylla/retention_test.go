package scylla

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRowTTLIsWhatIsLeftOf360Days(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	full := int(TimelineRetention / time.Second)
	if full != 31104000 {
		t.Fatalf("retention = %d s, want 31104000 (360 days, the schema's default_time_to_live)", full)
	}
	cases := []struct {
		name    string
		created time.Time
		want    int
	}{
		{"just posted", now, full},
		{"posted 10 days ago", now.Add(-10 * 24 * time.Hour), full - 10*86400},
		{"one second left", now.Add(-TimelineRetention + time.Second), 1},
		{"exactly 360 days old", now.Add(-TimelineRetention), 0},
		{"older than 360 days", now.Add(-400 * 24 * time.Hour), 0},
	}
	for _, tc := range cases {
		if got := rowTTL(tc.created, now); got != tc.want {
			t.Errorf("%s: rowTTL = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// The shard is a pure function of the owner, in range, and spreads owners.
// It must never change for an existing owner: rows are found again only
// under the shard they were written to.
// pinnedShard is IndexShard of the fixed owner below, under FNV-1a mod 16.
const pinnedShard = 3

func TestIndexShardIsStableInRangeAndSpreads(t *testing.T) {
	owner := uuid.MustParse("0b6f5c1e-8a7d-4f3e-9b2a-1c4d5e6f7a8b")
	if a, b := IndexShard(owner), IndexShard(owner); a != b {
		t.Fatalf("IndexShard is not deterministic: %d vs %d", a, b)
	}
	// Pinned: a change to the hash or the shard count fails here, before
	// it orphans a production index.
	if got := IndexShard(owner); got != pinnedShard {
		t.Fatalf("IndexShard(%s) = %d, pinned %d — the shard function changed", owner, got, pinnedShard)
	}
	used := map[int]int{}
	for i := 0; i < 4000; i++ {
		s := IndexShard(uuid.New())
		if s < 0 || s >= IndexShards {
			t.Fatalf("shard %d out of [0,%d)", s, IndexShards)
		}
		used[s]++
	}
	if len(used) != IndexShards {
		t.Fatalf("4000 owners used only %d of %d shards", len(used), IndexShards)
	}
	for s, n := range used {
		if n < 4000/IndexShards/2 {
			t.Errorf("shard %d got %d of 4000 owners; expected about %d", s, n, 4000/IndexShards)
		}
	}
	if len(allIndexShards) != IndexShards || allIndexShards[0] != 0 || allIndexShards[IndexShards-1] != IndexShards-1 {
		t.Fatalf("allIndexShards = %v", allIndexShards)
	}
}
