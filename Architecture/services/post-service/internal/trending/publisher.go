// Real-time trending leaderboard publisher.
//
// Reads the top N entries from the Redis ZSET that CreatePost already
// maintains (`trending:hashtags:<UTC-date>`), debounced on a fixed
// cadence — typically 30 s. When the top N changes (rank, post count,
// or membership), publishes the snapshot to the
// `trending:hashtags:updates` Redis pub/sub channel. SSE/WS handlers
// fan that out to live clients (web + mobile TrendingHashtagStrip).
//
// Why a poll + Redis-leader-lock instead of pushing on every ZIncrBy:
// CreatePost can fire many ZIncrBy's per second under load; pushing
// each one would (1) saturate the SSE channel and (2) cause UI churn.
// Debouncing in one place plus running the publisher on exactly one
// post-service replica (via SETNX-with-TTL) keeps cost flat regardless
// of how many writers there are.

package trending

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// PubSubChannel is the Redis channel SSE/WS handlers subscribe to.
// Exported so the HTTP handler and the publisher can't drift.
const PubSubChannel = "trending:hashtags:updates"

// LeaderLockKey is held by whichever post-service replica is currently
// driving the publish loop. TTL beats wall-clock cadence so the lock
// auto-releases if the holder dies between ticks.
const LeaderLockKey = "lock:trending:publisher"

// Default cadence + top-N. Both are configurable via env so we can
// turn the dial in staging without redeploying.
const (
	defaultInterval = 30 * time.Second
	defaultTopN     = 15
)

type Publisher struct {
	rdb       *redis.Client
	log       *slog.Logger
	interval  time.Duration
	topN      int
	leaderID  string // unique per process so we can recover our own lock on contention
	lastSnap  []Entry
}

// Entry is one row of the published top-N list. Field names match the
// JSON the HTTP handler returns for /v1/hashtags/trending so clients
// can render the SSE payload through the same component.
type Entry struct {
	NormalizedName string `json:"normalized_name"`
	DisplayName    string `json:"display_name"`
	PostCount      int64  `json:"post_count"`
	IsTrending     bool   `json:"is_trending"`
}

type Snapshot struct {
	Tags       []Entry   `json:"hashtags"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func New(rdb *redis.Client, log *slog.Logger) *Publisher {
	interval := defaultInterval
	if v := os.Getenv("TRENDING_PUBLISH_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			interval = d
		}
	}
	host, _ := os.Hostname()
	return &Publisher{
		rdb:      rdb,
		log:      log.With("component", "trending-publisher"),
		interval: interval,
		topN:     defaultTopN,
		leaderID: fmt.Sprintf("%s-%d", host, time.Now().UnixNano()),
	}
}

// Start launches the publish loop. Returns immediately; the loop
// exits when ctx is cancelled. Safe to call from N replicas — only
// the one holding LeaderLockKey actually publishes; the rest sleep
// until they win the lock on a subsequent tick.
func (p *Publisher) Start(ctx context.Context) {
	go p.run(ctx)
}

func (p *Publisher) run(ctx context.Context) {
	t := time.NewTicker(p.interval)
	defer t.Stop()

	// First tick fires immediately so freshly-restarted clusters
	// publish the current top-N without waiting interval seconds.
	p.tickIfLeader(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.tickIfLeader(ctx)
		}
	}
}

func (p *Publisher) tickIfLeader(ctx context.Context) {
	// SETNX with TTL = 2× interval. Tick to tick, we re-acquire to
	// keep ownership; if we crash, the lock auto-expires and a
	// neighbour picks up within one tick.
	ok, err := p.rdb.SetNX(ctx, LeaderLockKey, p.leaderID, 2*p.interval).Result()
	if err != nil {
		p.log.Warn("leader lock acquire failed", "err", err)
		return
	}
	if !ok {
		// Refresh TTL only if WE already own the lock. Avoids the
		// thundering-herd risk of every replica resetting the TTL on
		// the same key.
		cur, err := p.rdb.Get(ctx, LeaderLockKey).Result()
		if err != nil || cur != p.leaderID {
			return
		}
		if _, err := p.rdb.Expire(ctx, LeaderLockKey, 2*p.interval).Result(); err != nil {
			p.log.Warn("leader lock refresh failed", "err", err)
		}
	}

	if err := p.publishIfChanged(ctx); err != nil {
		p.log.Warn("trending publish failed", "err", err)
	}
}

// publishIfChanged reads the current top-N from the daily ZSET and
// publishes a snapshot iff the (name → rank, name → count) pairs
// have changed since the previous tick. Returns nil on a no-op
// (empty set, or unchanged).
func (p *Publisher) publishIfChanged(ctx context.Context) error {
	today := time.Now().UTC().Format("2006-01-02")
	key := "trending:hashtags:" + today

	results, err := p.rdb.ZRevRangeWithScores(ctx, key, 0, int64(p.topN-1)).Result()
	if err != nil {
		return fmt.Errorf("zrevrange: %w", err)
	}
	entries := make([]Entry, 0, len(results))
	for _, z := range results {
		name, _ := z.Member.(string)
		name = strings.TrimSpace(strings.ToLower(strings.TrimPrefix(name, "#")))
		if name == "" {
			continue
		}
		entries = append(entries, Entry{
			NormalizedName: name,
			DisplayName:    name,
			PostCount:      int64(z.Score),
			IsTrending:     true,
		})
	}

	if !p.snapshotChanged(entries) {
		return nil
	}
	p.lastSnap = entries

	payload, err := json.Marshal(Snapshot{
		Tags:      entries,
		UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	if err := p.rdb.Publish(ctx, PubSubChannel, payload).Err(); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	p.log.Info("trending snapshot published", "count", len(entries))
	return nil
}

// snapshotChanged returns true when the new list differs from the
// previously published one in name, order, or post count. Cheap
// element-wise comparison — top-N is small (≤ 30).
func (p *Publisher) snapshotChanged(next []Entry) bool {
	if len(next) != len(p.lastSnap) {
		return true
	}
	for i, e := range next {
		prev := p.lastSnap[i]
		if e.NormalizedName != prev.NormalizedName || e.PostCount != prev.PostCount {
			return true
		}
	}
	return false
}
