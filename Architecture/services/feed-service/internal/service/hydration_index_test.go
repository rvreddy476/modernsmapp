package service

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Audit L4: dropping a viewer's whole hydration cache uses the per-viewer
// index the store path keeps, not a SCAN of the shared keyspace, and
// touches no other viewer.
func TestInvalidateViewerHydrationCache_UsesTheIndex(t *testing.T) {
	mr := miniredis.RunT(t)
	s := &Service{rdb: redis.NewClient(&redis.Options{Addr: mr.Addr()})}
	a, b := uuid.New(), uuid.New()
	p1, p2 := uuid.New(), uuid.New()
	s.storeHydratedCache(a, map[string]HydratedPost{p1.String(): {ID: p1}, p2.String(): {ID: p2}})
	s.storeHydratedCache(b, map[string]HydratedPost{p1.String(): {ID: p1}})
	waitForCachedRow(t, mr, hydrationCacheKey(a, p2))
	waitForCachedRow(t, mr, hydrationCacheKey(b, p1))

	if ttl := mr.TTL(hydrationIndexKey(a)); ttl <= 0 || ttl > hydrationCacheTTL {
		t.Fatalf("index TTL = %v, want (0, %v]", ttl, hydrationCacheTTL)
	}
	// A row the index does not list is invisible to the invalidation: the
	// proof it no longer scans the keyspace.
	unindexed := uuid.New()
	_ = mr.Set(hydrationCacheKey(a, unindexed), "{}")

	s.invalidateViewerHydrationCache(context.Background(), a)
	if mr.Exists(hydrationCacheKey(a, p1)) || mr.Exists(hydrationCacheKey(a, p2)) {
		t.Fatal("the viewer's cached rows survived the invalidation")
	}
	if mr.Exists(hydrationIndexKey(a)) {
		t.Fatal("the viewer's index survived the invalidation")
	}
	if !mr.Exists(hydrationCacheKey(b, p1)) {
		t.Fatal("another viewer's cached row was dropped")
	}
	if !mr.Exists(hydrationCacheKey(a, unindexed)) {
		t.Fatal("the invalidation found a row outside the index: it is still scanning")
	}
}
