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

// extendIfOwnerScript implements check-and-extend atomically. The
// naive GET → EXPIRE pair has a race window in which a second replica
// can SETNX in between, producing duplicate publishes. Doing both in
// one Lua call keeps the leader unique even when N replicas wake at
// the same tick.
var extendIfOwnerScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0
`)

func (p *Publisher) tickIfLeader(ctx context.Context) {
	// First try to acquire as fresh leader. SETNX is atomic.
	ok, err := p.rdb.SetNX(ctx, LeaderLockKey, p.leaderID, 2*p.interval).Result()
	if err != nil {
		p.log.Warn("leader lock acquire failed", "err", err)
		return
	}
	if !ok {
		// Lock already held — atomically extend the TTL iff we still
		// own it. The Lua script avoids the GET/EXPIRE race that
		// could let two replicas both think they're leader.
		ttlMs := int64((2 * p.interval).Milliseconds())
		res, err := extendIfOwnerScript.Run(
			ctx, p.rdb,
			[]string{LeaderLockKey},
			p.leaderID, ttlMs,
		).Int()
		if err != nil || res == 0 {
			// Not the owner; another replica is publishing this tick.
			return
		}
	}

	if err := p.publishIfChanged(ctx); err != nil {
		p.log.Warn("trending publish failed", "err", err)
	}
}

// publishIfChanged reads the current top-N and publishes a snapshot
// iff the (name, rank, count) tuple has changed since the previous
// tick. Returns nil on a no-op.
//
// UTC rollover handling: at 00:00 UTC the daily bucket key flips to
// the new date and starts empty. Without fallback, the publisher
// would push an empty top-N and every connected client would clear
// its chip strip for hours until traffic accumulates. Reads both
// today's and yesterday's buckets and uses whichever has data,
// preferring today. (The 48 h TTL on each bucket — set by
// post-service CreatePost — guarantees yesterday's data is still
// available across the rollover.)
//
// Empty-snapshot guard: if BOTH buckets are empty (cold-start /
// post-purge), we don't publish at all. Subscribers keep showing
// whatever they had on the initial REST load; the next CreatePost
// will populate Redis and the following tick will publish.
func (p *Publisher) publishIfChanged(ctx context.Context) error {
	now := time.Now().UTC()
	entries, err := p.readTopN(ctx, now)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		// Cold start or post-purge; don't broadcast emptiness.
		return nil
	}
	if !p.snapshotChanged(entries) {
		return nil
	}
	p.lastSnap = entries

	payload, err := json.Marshal(Snapshot{
		Tags:      entries,
		UpdatedAt: now,
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

// readTopN reads today's bucket, falling back to yesterday's when
// today is empty (handles the UTC rollover window).
func (p *Publisher) readTopN(ctx context.Context, now time.Time) ([]Entry, error) {
	for offset := 0; offset <= 1; offset++ {
		day := now.AddDate(0, 0, -offset).Format("2006-01-02")
		key := "trending:hashtags:" + day
		results, err := p.rdb.ZRevRangeWithScores(ctx, key, 0, int64(p.topN-1)).Result()
		if err != nil {
			return nil, fmt.Errorf("zrevrange %s: %w", key, err)
		}
		if len(results) == 0 {
			continue
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
		return entries, nil
	}
	return nil, nil
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
