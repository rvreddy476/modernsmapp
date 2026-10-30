package http

import (
	"context"
	"sync"
	"time"
)

// chartCache holds the studio's retention and demographics results for a
// few minutes (audit M9). Each result is one bounded scan of a content
// item's watch_sessions partition; a creator reopening or refreshing the
// drawer, or several tabs opening it at once, read it once per TTL rather
// than once per request.
//
// Concurrent misses for one key share one load. Errors are never cached:
// the next request retries. Entries are per process; with several pods a
// key may be computed once per pod per TTL, which is still bounded.
//
// The cache sits behind the ownership gate (requireOwnedContent runs
// first, on every request), and its keys carry the content id, so a
// cached chart is only ever served to a caller allowed to see it.
type chartCache struct {
	ttl        time.Duration
	maxEntries int
	now        func() time.Time

	mu       sync.Mutex
	entries  map[string]chartEntry
	inflight map[string]*chartCall
}

type chartEntry struct {
	value   any
	sampled bool
	expires time.Time
}

type chartCall struct {
	done    chan struct{}
	value   any
	sampled bool
	err     error
}

const (
	chartCacheTTL        = 2 * time.Minute
	chartCacheMaxEntries = 4096
)

func newChartCache(ttl time.Duration, maxEntries int) *chartCache {
	return &chartCache{
		ttl: ttl, maxEntries: maxEntries, now: time.Now,
		entries:  make(map[string]chartEntry),
		inflight: make(map[string]*chartCall),
	}
}

// get returns the cached value for key, or runs load once — however many
// callers are waiting — and caches what it returns. load runs on a
// context detached from the first caller's cancellation (its own timeout
// still applies, see scylla.chartQueryTimeout), so one client hanging up
// does not fail the others waiting on the same load.
func (c *chartCache) get(ctx context.Context, key string, load func(context.Context) (any, bool, error)) (any, bool, error) {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		if c.now().Before(e.expires) {
			c.mu.Unlock()
			return e.value, e.sampled, nil
		}
		delete(c.entries, key)
	}
	if call, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-call.done:
			return call.value, call.sampled, call.err
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	call := &chartCall{done: make(chan struct{})}
	c.inflight[key] = call
	c.mu.Unlock()

	call.value, call.sampled, call.err = load(context.WithoutCancel(ctx))

	c.mu.Lock()
	delete(c.inflight, key)
	if call.err == nil {
		c.storeLocked(key, chartEntry{value: call.value, sampled: call.sampled, expires: c.now().Add(c.ttl)})
	}
	c.mu.Unlock()
	close(call.done)
	return call.value, call.sampled, call.err
}

// storeLocked adds an entry, first dropping expired ones and then, if the
// cache is still full, the entry closest to expiry. c.mu must be held.
func (c *chartCache) storeLocked(key string, e chartEntry) {
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxEntries {
		now := c.now()
		for k, v := range c.entries {
			if !now.Before(v.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= c.maxEntries {
			var oldest string
			var oldestAt time.Time
			for k, v := range c.entries {
				if oldest == "" || v.expires.Before(oldestAt) {
					oldest, oldestAt = k, v.expires
				}
			}
			delete(c.entries, oldest)
		}
	}
	c.entries[key] = e
}
