package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// RateLimitConfig holds per-category rate limit settings.
type RateLimitConfig struct {
	// IPRate is requests per second per IP address (default 100).
	IPRate float64
	// IPBurst is the burst allowance per IP (default 200).
	IPBurst int
	// UserRate is requests per second per authenticated user (default 60).
	UserRate float64
	// UserBurst is the burst allowance per user (default 120).
	UserBurst int
}

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type rateLimiterStore struct {
	mu       sync.Mutex
	limiters map[string]*limiterEntry
	rate     float64
	burst    int
}

func newStore(r float64, b int) *rateLimiterStore {
	s := &rateLimiterStore{
		limiters: make(map[string]*limiterEntry),
		rate:     r,
		burst:    b,
	}
	go s.cleanup()
	return s
}

func (s *rateLimiterStore) get(key string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.limiters[key]; ok {
		e.lastSeen = time.Now()
		return e.limiter
	}
	l := rate.NewLimiter(rate.Limit(s.rate), s.burst)
	s.limiters[key] = &limiterEntry{limiter: l, lastSeen: time.Now()}
	return l
}

func (s *rateLimiterStore) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		threshold := time.Now().Add(-10 * time.Minute)
		for k, e := range s.limiters {
			if e.lastSeen.Before(threshold) {
				delete(s.limiters, k)
			}
		}
		s.mu.Unlock()
	}
}

// RateLimit returns a Gin middleware that enforces per-IP and per-user rate limits.
// When a limit is exceeded it returns HTTP 429 Too Many Requests.
func RateLimit(cfg RateLimitConfig) gin.HandlerFunc {
	if cfg.IPRate == 0 {
		cfg.IPRate = 100
	}
	if cfg.IPBurst == 0 {
		cfg.IPBurst = 200
	}
	if cfg.UserRate == 0 {
		cfg.UserRate = 60
	}
	if cfg.UserBurst == 0 {
		cfg.UserBurst = 120
	}

	ipStore := newStore(cfg.IPRate, cfg.IPBurst)
	userStore := newStore(cfg.UserRate, cfg.UserBurst)

	return func(c *gin.Context) {
		ip := c.ClientIP()
		if !ipStore.get(ip).Allow() {
			c.Header("Retry-After", "1")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{"code": "RATE_LIMITED", "message": "too many requests"},
			})
			return
		}

		if userID := c.GetHeader("X-User-Id"); userID != "" {
			if !userStore.get(userID).Allow() {
				c.Header("Retry-After", "1")
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
					"error": gin.H{"code": "RATE_LIMITED", "message": "too many requests"},
				})
				return
			}
		}

		c.Next()
	}
}
