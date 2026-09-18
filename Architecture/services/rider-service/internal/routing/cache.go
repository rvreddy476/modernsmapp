package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// CacheTTL is how long one answer lives, and the width of the time bucket
	// in the key: an entry is never served outside the five minutes it was
	// computed in, so traffic moves the estimate at least that often.
	CacheTTL = 5 * time.Minute
	// cacheGridDegrees rounds both endpoints to a grid of about 50 m
	// (0.0005 deg is 55.6 m of latitude, and 54 m of longitude at Bengaluru's
	// 13 deg N).
	cacheGridDegrees = 0.0005
	cacheKeyPrefix   = "rider:route:v1:2w"
)

// Cache keeps Google answers in Redis. Only Google answers are stored: a
// haversine answer costs nothing to recompute, and caching one would pin the
// fallback for five minutes after a single transient Google failure.
type Cache struct {
	rdb    *redis.Client
	next   Router
	now    func() time.Time
	logger *slog.Logger

	errOnce sync.Once
}

// NewCache wraps next. Without Redis (nil client) there is no cache and next
// is returned as it is.
func NewCache(rdb *redis.Client, next Router, logger *slog.Logger) Router {
	if rdb == nil {
		return next
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Cache{rdb: rdb, next: next, now: time.Now, logger: logger}
}

// WithClock replaces the bucket clock (tests).
func (c *Cache) WithClock(now func() time.Time) *Cache {
	c.now = now
	return c
}

type cachedRoute struct {
	DistanceMeters int    `json:"distance_meters"`
	DurationMS     int64  `json:"duration_ms"`
	Source         string `json:"source"`
}

// CacheKey is the Redis key for a leg asked at `at`: both endpoints on the
// ~50 m grid plus the five-minute bucket.
func CacheKey(from, to LatLng, at time.Time) string {
	bucket := at.Unix() / int64(CacheTTL/time.Second)
	return fmt.Sprintf("%s:%d:%d:%d:%d:%d", cacheKeyPrefix,
		gridCell(from.Lat), gridCell(from.Lng), gridCell(to.Lat), gridCell(to.Lng), bucket)
}

func gridCell(deg float64) int64 {
	return int64(math.Round(deg / cacheGridDegrees))
}

// Route implements Router. A Redis failure is logged once and bypassed.
func (c *Cache) Route(ctx context.Context, from, to LatLng) (Route, error) {
	if !from.Valid() || !to.Valid() {
		return c.next.Route(ctx, from, to)
	}
	key := CacheKey(from, to, c.now())
	raw, err := c.rdb.Get(ctx, key).Bytes()
	switch {
	case err == nil:
		var cr cachedRoute
		if json.Unmarshal(raw, &cr) == nil && cr.Source != "" {
			return Route{DistanceMeters: cr.DistanceMeters, Duration: time.Duration(cr.DurationMS) * time.Millisecond, Source: cr.Source}, nil
		}
	case !errors.Is(err, redis.Nil):
		c.redisFailed(err)
	}

	r, err := c.next.Route(ctx, from, to)
	if err != nil || r.Source != SourceGoogle {
		return r, err
	}
	body, _ := json.Marshal(cachedRoute{DistanceMeters: r.DistanceMeters, DurationMS: r.Duration.Milliseconds(), Source: r.Source})
	if err := c.rdb.Set(ctx, key, body, CacheTTL).Err(); err != nil {
		c.redisFailed(err)
	}
	return r, nil
}

func (c *Cache) redisFailed(err error) {
	c.errOnce.Do(func() {
		c.logger.Warn("rider-service: route cache unavailable; routing uncached (logged once)", "error", err)
	})
}
