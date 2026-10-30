package http

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

/*
	Connection and frame limits (audit M5).

	Each socket holds its own Redis PubSub connection, so one account opening
	sockets in a loop could exhaust Redis maxclients for every service that
	shares it. Three bounds now apply:

	  - per user: at most MaxConnsPerUser live sockets across every gateway
	    replica, counted in Redis (wsconns:<user>, a ZSET of connID ->
	    expiry). Admission is one Lua script, so concurrent connects cannot
	    both squeeze in. Entries are refreshed on every pong and removed on
	    close; a crashed replica's entries expire after wsConnEntryTTL.
	  - per process: at most MaxConnsPerProcess sockets on this replica,
	    which bounds this replica's Redis connections however many users
	    connect.
	  - per socket: a token bucket on inbound frames; a frame over the rate
	    is dropped before it costs any Redis round trip.

	Sharing one PubSub connection per process would remove the per-socket
	Redis connection entirely, but every room type subscribes and
	unsubscribes on the per-socket PubSub (conversation, post, live, call
	rooms), so that is a router rewrite, not a limit; the caps above bound
	the cost in the meantime.
*/

const (
	defaultMaxConnsPerUser = 10
	defaultFrameRate       = 20 // frames per second, sustained
	defaultFrameBurst      = 60 // an ICE trickle at call start fits
)

func wsConnsKey(userID string) string { return "wsconns:" + userID }

// admitConnScript: KEYS[1]=wsconns:<user>; ARGV = now, expiry, connID, max,
// keyTTLSeconds. Returns 1 when admitted.
var admitConnScript = redis.NewScript(`
redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", ARGV[1])
if redis.call("ZCARD", KEYS[1]) >= tonumber(ARGV[4]) then
  return 0
end
redis.call("ZADD", KEYS[1], ARGV[2], ARGV[3])
redis.call("EXPIRE", KEYS[1], ARGV[5])
return 1
`)

// wsConnEntryTTL is how long an admission lives without a pong.
func (s *Server) wsConnEntryTTL() time.Duration { return 2 * s.opts.PongWait }

// admitUserConnection reserves one of the user's socket slots.
func (s *Server) admitUserConnection(ctx context.Context, userID, connID string, now time.Time) (bool, error) {
	if s.opts.MaxConnsPerUser < 0 {
		return true, nil
	}
	ttl := s.wsConnEntryTTL()
	n, err := admitConnScript.Run(ctx, s.rdb, []string{wsConnsKey(userID)},
		now.Unix(), now.Add(ttl).Unix(), connID, s.opts.MaxConnsPerUser, int64(ttl.Seconds())+1,
	).Int()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// refreshUserConnection keeps a live socket's slot (only if still held).
func (s *Server) refreshUserConnection(ctx context.Context, userID, connID string, now time.Time) {
	if s.opts.MaxConnsPerUser < 0 {
		return
	}
	ttl := s.wsConnEntryTTL()
	pipe := s.rdb.Pipeline()
	pipe.ZAddXX(ctx, wsConnsKey(userID), redis.Z{Score: float64(now.Add(ttl).Unix()), Member: connID})
	pipe.Expire(ctx, wsConnsKey(userID), ttl+time.Second)
	_, _ = pipe.Exec(ctx)
}

// releaseUserConnection frees the slot on close.
func (s *Server) releaseUserConnection(ctx context.Context, userID, connID string) {
	if s.opts.MaxConnsPerUser < 0 {
		return
	}
	_ = s.rdb.ZRem(ctx, wsConnsKey(userID), connID).Err()
}

// acquireProcessSlot reserves one of this replica's socket slots.
func (s *Server) acquireProcessSlot() bool {
	if s.opts.MaxConnsPerProcess <= 0 {
		s.openConns.Add(1)
		return true
	}
	for {
		cur := s.openConns.Load()
		if cur >= int64(s.opts.MaxConnsPerProcess) {
			return false
		}
		if s.openConns.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

func (s *Server) releaseProcessSlot() { s.openConns.Add(-1) }

// frameLimiter is a token bucket over one socket's inbound frames. It is
// used only by that socket's read loop, so it needs no lock.
type frameLimiter struct {
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newFrameLimiter(rate float64, burst int, now time.Time) *frameLimiter {
	if rate <= 0 {
		rate = defaultFrameRate
	}
	if burst <= 0 {
		burst = defaultFrameBurst
	}
	return &frameLimiter{rate: rate, burst: float64(burst), tokens: float64(burst), last: now}
}

func (l *frameLimiter) allow(now time.Time) bool {
	if elapsed := now.Sub(l.last).Seconds(); elapsed > 0 {
		l.tokens += elapsed * l.rate
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
		l.last = now
	}
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
