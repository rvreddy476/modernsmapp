package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// H5 — shared-Redis rate limiter. The in-memory token-bucket store in
// rateLimitMiddleware enforces 100 rps per IP per gateway pod, which means
// N pods aggregate to N × 100 rps in production. EKS horizontal scaling is
// what makes that wrong: the published guarantee is per-IP, not per-pod.
//
// The redis path uses fixed-window INCR + EXPIRE counters: cheap (2 ops per
// request, one pipeline) and good enough for protection against burst /
// spam. Sliding-window correctness inside the window is intentionally
// approximated — at 100 rps the worst-case extra burst from window edges
// is one window's worth, which the per-pod limiter would have allowed too.
//
// Fail mode is OPEN: on Redis error we let the request through. The
// in-memory limiter still runs in front of this when Redis is configured,
// so a Redis blip degrades to "per-pod only" rather than "no limits at
// all". This is the safer choice for an api-gateway — failing closed
// could brick all client traffic during a Redis incident.

type redisRateLimiter struct {
	rdb    *redis.Client
	ipMax  int
	userMax int
	window time.Duration
}

func newRedisRateLimiter(rdb *redis.Client, ipMax, userMax int, window time.Duration) *redisRateLimiter {
	if window <= 0 {
		window = time.Second
	}
	return &redisRateLimiter{
		rdb:     rdb,
		ipMax:   ipMax,
		userMax: userMax,
		window:  window,
	}
}

// allow reports whether (key, max) is within quota for the current window
// bucket. Failures return (true, err) — fail-open on Redis errors.
func (r *redisRateLimiter) allow(ctx context.Context, scope, key string, maxCount int) (bool, error) {
	if r == nil || r.rdb == nil || maxCount <= 0 {
		return true, nil
	}
	windowSecs := int64(r.window / time.Second)
	if windowSecs <= 0 {
		windowSecs = 1
	}
	bucket := time.Now().Unix() / windowSecs
	k := fmt.Sprintf("rl:gw:%s:%s:%d", scope, key, bucket)

	pipe := r.rdb.Pipeline()
	incr := pipe.Incr(ctx, k)
	pipe.Expire(ctx, k, r.window*2)
	if _, err := pipe.Exec(ctx); err != nil {
		return true, err
	}
	return incr.Val() <= int64(maxCount), nil
}

// redisRateLimitMiddleware replaces the in-memory rateLimitMiddleware when
// the gateway has a Redis connection. The configured per-IP / per-user
// rates apply across the whole gateway fleet, not per-pod.
func redisRateLimitMiddleware(rl *redisRateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ip := clientIPForRateLimit(r)

		if ok, err := rl.allow(ctx, "ip", ip, rl.ipMax); err != nil {
			slog.Warn("redis rate limit error (fail-open)", "scope", "ip", "err", err)
		} else if !ok {
			writeRateLimitedResponse(w)
			return
		}
		if userID := r.Header.Get("X-User-Id"); userID != "" {
			if ok, err := rl.allow(ctx, "user", userID, rl.userMax); err != nil {
				slog.Warn("redis rate limit error (fail-open)", "scope", "user", "err", err)
			} else if !ok {
				writeRateLimitedResponse(w)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// clientIPForRateLimit centralises the same XFF / X-Real-IP / RemoteAddr
// fallback the in-memory limiter uses so the two paths can't disagree.
func clientIPForRateLimit(r *http.Request) string {
	ip := r.RemoteAddr
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		ip = strings.TrimSpace(realIP)
	} else if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx != -1 {
			ip = strings.TrimSpace(xff[:idx])
		} else {
			ip = strings.TrimSpace(xff)
		}
	}
	// Strip port if present (RemoteAddr includes port).
	if host, _, err := splitHostPortLenient(ip); err == nil {
		ip = host
	}
	return ip
}

// splitHostPortLenient mirrors net.SplitHostPort but is lenient about
// IPv6 literals already stripped of brackets (which RemoteAddr never
// emits but XFF sometimes does).
func splitHostPortLenient(addr string) (string, string, error) {
	if addr == "" {
		return "", "", fmt.Errorf("empty addr")
	}
	if strings.HasPrefix(addr, "[") {
		// Already in [ipv6]:port form
		end := strings.LastIndex(addr, "]")
		if end < 0 {
			return addr, "", nil
		}
		return addr[1:end], strings.TrimPrefix(addr[end+1:], ":"), nil
	}
	if !strings.Contains(addr, ":") {
		return addr, "", nil
	}
	// IPv4:port or hostname:port
	colon := strings.LastIndex(addr, ":")
	host := addr[:colon]
	port := addr[colon+1:]
	// Reject malformed (e.g. unbracketed IPv6 with multiple colons)
	if strings.Contains(host, ":") {
		return addr, "", fmt.Errorf("ambiguous host:port")
	}
	return host, port, nil
}

func writeRateLimitedResponse(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"error":{"code":"RATE_LIMITED","message":"too many requests"}}`))
}

// connectRedisForRateLimit dials Redis for the rate-limit path. Reads the
// same REDIS_* envs as every other service. Returns nil when REDIS_ADDR is
// unset or the connection fails — the caller then falls back to the
// per-pod in-memory limiter, which is fine in dev.
func connectRedisForRateLimit(redisAddr string) *redis.Client {
	if strings.TrimSpace(redisAddr) == "" {
		return nil
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:         redisAddr,
		Username:     envOrEmpty("REDIS_USERNAME"),
		Password:     envOrEmpty("REDIS_PASSWORD"),
		PoolSize:     20,
		MinIdleConns: 5,
		PoolTimeout:  10 * time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Warn("rate limiter: redis ping failed, falling back to in-memory limiter", "err", err)
		_ = rdb.Close()
		return nil
	}
	return rdb
}

// envOrEmpty is a small wrapper so this file doesn't drag in viper / a
// secondary config layer.
func envOrEmpty(key string) string {
	return env(key, "")
}
