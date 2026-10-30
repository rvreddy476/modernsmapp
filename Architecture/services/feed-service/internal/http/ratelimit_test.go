package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Audit M2: the limiter's counter always carries its window's expiry, so a
// user can never be locked out for good.

func rateLimitedEngine(t *testing.T) (*gin.Engine, *miniredis.Miniredis) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	h := &Handler{rdb: redis.NewClient(&redis.Options{Addr: mr.Addr()})}
	r := gin.New()
	r.Use(h.feedRateLimit())
	r.GET("/v1/feed/home", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r, mr
}

func hit(r *gin.Engine, user string) int {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/feed/home", nil)
	req.Header.Set("X-User-Id", user)
	r.ServeHTTP(w, req)
	return w.Code
}

func TestFeedRateLimit_FirstRequestArmsTheWindow(t *testing.T) {
	r, mr := rateLimitedEngine(t)
	user := uuid.New().String()
	if code := hit(r, user); code != http.StatusOK {
		t.Fatalf("first request = %d", code)
	}
	if ttl := mr.TTL("feed_rl:" + user); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("counter TTL after the first request = %v, want (0, 1m]", ttl)
	}
}

func TestFeedRateLimit_CounterWithoutTTLIsReArmed(t *testing.T) {
	r, mr := rateLimitedEngine(t)
	user := uuid.New().String()
	// A counter an older, two-call limiter left behind with no TTL.
	if err := mr.Set("feed_rl:"+user, "500"); err != nil {
		t.Fatal(err)
	}
	if code := hit(r, user); code != http.StatusTooManyRequests {
		t.Fatalf("over-limit request = %d, want 429", code)
	}
	if ttl := mr.TTL("feed_rl:" + user); ttl <= 0 {
		t.Fatal("an over-limit counter with no TTL stayed without one: the user is locked out for good")
	}
	mr.FastForward(61 * time.Second)
	if code := hit(r, user); code != http.StatusOK {
		t.Fatalf("after the window = %d, want 200", code)
	}
}

func TestFeedRateLimit_LimitsAndDoesNotExtendTheWindow(t *testing.T) {
	r, mr := rateLimitedEngine(t)
	user := uuid.New().String()
	for i := 0; i < 120; i++ {
		if code := hit(r, user); code != http.StatusOK {
			t.Fatalf("request %d = %d, want 200", i+1, code)
		}
		if i == 0 {
			mr.FastForward(30 * time.Second)
		}
	}
	if code := hit(r, user); code != http.StatusTooManyRequests {
		t.Fatalf("request 121 = %d, want 429", code)
	}
	mr.FastForward(31 * time.Second) // 61 s after the first request
	if code := hit(r, user); code != http.StatusOK {
		t.Fatalf("a new window = %d, want 200: later requests pushed the window back", code)
	}
}
