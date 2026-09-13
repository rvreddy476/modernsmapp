package routing

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type countingRouter struct {
	calls  atomic.Int64
	source string
	err    error
}

func (c *countingRouter) Route(context.Context, LatLng, LatLng) (Route, error) {
	n := c.calls.Add(1)
	if c.err != nil {
		return Route{}, c.err
	}
	return Route{DistanceMeters: 1000 * int(n), Duration: time.Duration(n) * time.Minute, Source: c.source}, nil
}

func cacheFor(t *testing.T, next Router, clock *time.Time) (*Cache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	c := NewCache(rdb, next, nil).(*Cache).WithClock(func() time.Time { return *clock })
	return c, mr
}

// bucketStart is a whole five-minute bucket boundary.
var bucketStart = time.Unix(1_789_000_200, 0).UTC()

func TestCache_ServesAGoogleAnswerWithinItsBucket(t *testing.T) {
	next := &countingRouter{source: SourceGoogle}
	now := bucketStart.Add(10 * time.Second)
	c, mr := cacheFor(t, next, &now)

	first, err := c.Route(context.Background(), blr, indira)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(4 * time.Minute)
	second, err := c.Route(context.Background(), blr, indira)
	if err != nil {
		t.Fatal(err)
	}
	if next.calls.Load() != 1 || second != first {
		t.Fatalf("calls = %d, first %+v second %+v", next.calls.Load(), first, second)
	}
	key := CacheKey(blr, indira, now)
	if ttl := mr.TTL(key); ttl != CacheTTL {
		t.Fatalf("ttl = %v, want %v", ttl, CacheTTL)
	}
}

// The next bucket is a miss even while the old entry is still inside its TTL:
// traffic re-prices at least every five minutes.
func TestCache_KeyCarriesTheTimeBucket(t *testing.T) {
	next := &countingRouter{source: SourceGoogle}
	now := bucketStart.Add(CacheTTL - time.Second)
	c, _ := cacheFor(t, next, &now)

	if _, err := c.Route(context.Background(), blr, indira); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second) // one second into the next bucket; the entry has 298 s left
	r, err := c.Route(context.Background(), blr, indira)
	if err != nil {
		t.Fatal(err)
	}
	if next.calls.Load() != 2 || r.Duration != 2*time.Minute {
		t.Fatalf("next bucket served from cache: calls = %d route %+v", next.calls.Load(), r)
	}

	if CacheKey(blr, indira, bucketStart) != CacheKey(blr, indira, bucketStart.Add(CacheTTL-time.Nanosecond)) {
		t.Fatal("one bucket produced two keys")
	}
	if CacheKey(blr, indira, bucketStart) == CacheKey(blr, indira, bucketStart.Add(CacheTTL)) {
		t.Fatal("adjacent buckets share a key")
	}
}

func TestCacheKey_RoundsEndpointsToAbout50m(t *testing.T) {
	const degPerMeterLat = 1 / 111_195.0
	origin := LatLng{Lat: 12.97162, Lng: 77.59462}
	near := LatLng{Lat: origin.Lat + 8*degPerMeterLat, Lng: origin.Lng}
	far := LatLng{Lat: origin.Lat + 120*degPerMeterLat, Lng: origin.Lng}
	if CacheKey(origin, indira, bucketStart) != CacheKey(near, indira, bucketStart) {
		t.Fatal("points 8 m apart got different keys")
	}
	if CacheKey(origin, indira, bucketStart) == CacheKey(far, indira, bucketStart) {
		t.Fatal("points 120 m apart share a key")
	}
	if CacheKey(origin, indira, bucketStart) == CacheKey(indira, origin, bucketStart) {
		t.Fatal("a leg and its reverse share a key")
	}
}

func TestCache_NeverStoresAHaversineAnswer(t *testing.T) {
	next := &countingRouter{source: SourceHaversine}
	now := bucketStart
	c, mr := cacheFor(t, next, &now)
	for i := 0; i < 2; i++ {
		if _, err := c.Route(context.Background(), blr, indira); err != nil {
			t.Fatal(err)
		}
	}
	if next.calls.Load() != 2 || len(mr.Keys()) != 0 {
		t.Fatalf("calls = %d keys = %v", next.calls.Load(), mr.Keys())
	}
}

func TestNewCache_WithoutRedisIsNoCache(t *testing.T) {
	next := &countingRouter{source: SourceGoogle}
	if got := NewCache(nil, next, nil); got != Router(next) {
		t.Fatalf("NewCache(nil) = %T, want the next router unchanged", got)
	}
}

func TestCache_RedisDownPassesThrough(t *testing.T) {
	next := &countingRouter{source: SourceGoogle}
	now := bucketStart
	c, mr := cacheFor(t, next, &now)
	mr.Close()
	r, err := c.Route(context.Background(), blr, indira)
	if err != nil || r.Source != SourceGoogle || next.calls.Load() != 1 {
		t.Fatalf("route = %+v err = %v calls = %d", r, err, next.calls.Load())
	}
}

func TestCache_PassesNextErrorsThrough(t *testing.T) {
	boom := errors.New("boom")
	now := bucketStart
	c, _ := cacheFor(t, &countingRouter{err: boom}, &now)
	if _, err := c.Route(context.Background(), blr, indira); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}
