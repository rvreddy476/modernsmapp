package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// redisAddr allows CI to point at another instance; defaults to the compose one.
func redisAddr() string {
	if v := os.Getenv("TEST_REDIS_ADDR"); v != "" {
		return v
	}
	return "127.0.0.1:6379"
}

// newTestRedis returns a client, or skips when no Redis is reachable.
//
// Deliberately a REAL Redis rather than an in-memory fake. The guarantee under
// test is that HSETNX is atomic across concurrent clients; a fake that
// serialises commands internally would report success for an implementation
// that is not actually safe.
func newTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr()})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("no Redis at %s: %v", redisAddr(), err)
	}
	return rdb
}

// router builds a gin engine whose single handler counts executions.
func router(rdb *redis.Client, executions *int64, hold time.Duration) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/v1/posts/:postId/comments", Idempotency(rdb), func(c *gin.Context) {
		atomic.AddInt64(executions, 1)
		if hold > 0 {
			time.Sleep(hold)
		}
		c.JSON(http.StatusCreated, gin.H{"data": gin.H{"id": fmt.Sprintf("comment-%d", atomic.LoadInt64(executions))}})
	})
	return r
}

func post(r *gin.Engine, actor, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/posts/p1/comments", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", actor)
	req.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func uniqueKey(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("test-%s-%d", t.Name(), time.Now().UnixNano())
}

// TestSameKeySameBodyReplaysOnce is the baseline: a retry of an identical
// request must execute the handler once and replay the stored response.
func TestSameKeySameBodyReplaysOnce(t *testing.T) {
	rdb := newTestRedis(t)
	var executions int64
	r := router(rdb, &executions, 0)
	key := uniqueKey(t)
	body := `{"text":"hello"}`

	first := post(r, "actor-1", key, body)
	second := post(r, "actor-1", key, body)

	if executions != 1 {
		t.Fatalf("handler executed %d times, want 1", executions)
	}
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("codes = %d, %d; want 201, 201", first.Code, second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replay body differs:\n first=%s\nsecond=%s", first.Body.String(), second.Body.String())
	}
}

// TestSameKeyDifferentBodyIsRejected covers the payload-binding defect.
//
// Before the fix the key was bound to nothing, so a user who edited their
// comment after a lost response would be served the EARLIER comment: their
// correction silently dropped and the text they replaced shown as theirs.
func TestSameKeyDifferentBodyIsRejected(t *testing.T) {
	rdb := newTestRedis(t)
	var executions int64
	r := router(rdb, &executions, 0)
	key := uniqueKey(t)

	first := post(r, "actor-1", key, `{"text":"original"}`)
	second := post(r, "actor-1", key, `{"text":"edited"}`)

	if first.Code != http.StatusCreated {
		t.Fatalf("first code = %d, want 201", first.Code)
	}
	if second.Code != http.StatusConflict {
		t.Fatalf("second code = %d, want 409", second.Code)
	}
	if !strings.Contains(second.Body.String(), "IDEMPOTENCY_KEY_REUSED") {
		t.Fatalf("second body = %s, want IDEMPOTENCY_KEY_REUSED", second.Body.String())
	}
	if strings.Contains(second.Body.String(), "comment-1") {
		t.Fatalf("edited request replayed the earlier response: %s", second.Body.String())
	}
	if executions != 1 {
		t.Fatalf("handler executed %d times, want 1", executions)
	}
}

// TestSameKeyDifferentActorsDoNotCollide covers the unscoped-namespace defect.
//
// The key used to be the bare client string, so two users who happened to
// generate the same value shared one cache entry and one could receive the
// other's response body.
func TestSameKeyDifferentActorsDoNotCollide(t *testing.T) {
	rdb := newTestRedis(t)
	var executions int64
	r := router(rdb, &executions, 0)
	key := uniqueKey(t)
	body := `{"text":"hello"}`

	a := post(r, "actor-1", key, body)
	b := post(r, "actor-2", key, body)

	if a.Code != http.StatusCreated || b.Code != http.StatusCreated {
		t.Fatalf("codes = %d, %d; want 201, 201", a.Code, b.Code)
	}
	if executions != 2 {
		t.Fatalf("handler executed %d times, want 2 (one per actor)", executions)
	}
	if a.Body.String() == b.Body.String() {
		t.Fatalf("actors shared a response body: %s", a.Body.String())
	}
}

// TestConcurrentSameKeyExecutesOnce is the check-then-act race.
//
// The old middleware did HGETALL -> handler -> HSET. Two requests arriving
// together both missed the read and both inserted a comment. The handler here
// holds long enough to guarantee the overlap.
func TestConcurrentSameKeyExecutesOnce(t *testing.T) {
	rdb := newTestRedis(t)
	var executions int64
	r := router(rdb, &executions, 300*time.Millisecond)
	key := uniqueKey(t)
	body := `{"text":"concurrent"}`

	const callers = 8
	var wg sync.WaitGroup
	codes := make([]int, callers)
	start := make(chan struct{})

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			codes[idx] = post(r, "actor-1", key, body).Code
		}(i)
	}
	close(start)
	wg.Wait()

	if executions != 1 {
		t.Fatalf("handler executed %d times under concurrency, want exactly 1", executions)
	}

	created, conflicts := 0, 0
	for _, c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected status %d", c)
		}
	}
	if created != 1 {
		t.Fatalf("got %d 201s, want exactly 1", created)
	}
	if conflicts != callers-1 {
		t.Fatalf("got %d 409s, want %d", conflicts, callers-1)
	}
}

// TestNegativeControlCheckThenActDuplicates is the required negative control.
//
// It reimplements the ORIGINAL check-then-act sequence — HGETALL, run, HSET —
// and shows it lets concurrent same-key requests execute more than once. If
// this ever stops duplicating, the concurrency test above has stopped proving
// anything and both must be re-examined.
func TestNegativeControlCheckThenActDuplicates(t *testing.T) {
	rdb := newTestRedis(t)
	var executions int64

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/v1/posts/:postId/comments", legacyCheckThenAct(rdb), func(c *gin.Context) {
		atomic.AddInt64(&executions, 1)
		time.Sleep(300 * time.Millisecond)
		c.JSON(http.StatusCreated, gin.H{"data": gin.H{"id": "x"}})
	})

	key := uniqueKey(t)
	const callers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			post(r, "actor-1", key, `{"text":"concurrent"}`)
		}()
	}
	close(start)
	wg.Wait()

	if executions <= 1 {
		t.Fatalf("negative control executed %d times; it must duplicate to prove the "+
			"atomic claim in Idempotency is what prevents duplicates", executions)
	}
}

// legacyCheckThenAct reproduces the pre-fix middleware for the negative control.
func legacyCheckThenAct(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("Idempotency-Key")
		if key == "" {
			c.Next()
			return
		}
		redisKey := "legacy-idempotency:" + key
		ctx := context.Background()

		cached, err := rdb.HGetAll(ctx, redisKey).Result()
		if err != nil && err != redis.Nil {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		if len(cached) > 0 {
			c.Data(http.StatusOK, "application/json", []byte(cached["body"]))
			c.Abort()
			return
		}

		c.Next()

		rdb.HSet(ctx, redisKey, map[string]interface{}{"status": "201", "body": "{}"})
		rdb.Expire(ctx, redisKey, time.Minute)
	}
}

// postRaw sends a body with explicit control over the declared length.
// contentLength of -1 models a chunked request that declares no length at all.
func postRaw(r *gin.Engine, actor, key, body string, contentLength int64) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/posts/p1/comments", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", actor)
	req.Header.Set("Idempotency-Key", key)
	req.ContentLength = contentLength
	if contentLength < 0 {
		req.TransferEncoding = []string{"chunked"}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestOversizedBodyRejectedBeforeHandler covers LB-3B.
//
// The middleware must buffer the body to hash it, which means an unbounded
// io.ReadAll here lets any authenticated caller force a large allocation
// before a single validation rule runs. The handler must never see it.
func TestOversizedBodyRejectedBeforeHandler(t *testing.T) {
	rdb := newTestRedis(t)
	var executions int64
	r := router(rdb, &executions, 0)

	huge := fmt.Sprintf(`{"text":"%s"}`, strings.Repeat("x", 64<<10))
	w := postRaw(r, "actor-1", uniqueKey(t), huge, int64(len(huge)))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413", w.Code)
	}
	if executions != 0 {
		t.Fatalf("handler ran %d times for an oversized body, want 0", executions)
	}
	if !strings.Contains(w.Body.String(), "REQUEST_TOO_LARGE") {
		t.Fatalf("body = %s, want REQUEST_TOO_LARGE", w.Body.String())
	}
}

// TestOversizedChunkedBodyRejected proves the cap does not rely on a declared
// Content-Length, which a hostile client simply omits.
func TestOversizedChunkedBodyRejected(t *testing.T) {
	rdb := newTestRedis(t)
	var executions int64
	r := router(rdb, &executions, 0)

	huge := fmt.Sprintf(`{"text":"%s"}`, strings.Repeat("x", 64<<10))
	w := postRaw(r, "actor-1", uniqueKey(t), huge, -1)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413 for an unknown-length body", w.Code)
	}
	if executions != 0 {
		t.Fatalf("handler ran %d times for an oversized chunked body, want 0", executions)
	}
}

// TestBodyUnderTheCapIsAccepted is the negative control for the two above: if
// the limit ever creeps below a legitimate comment, they would still pass
// while the endpoint had become unusable.
func TestBodyUnderTheCapIsAccepted(t *testing.T) {
	rdb := newTestRedis(t)
	var executions int64
	r := router(rdb, &executions, 0)

	// A full-length comment at the client's 2,000-character cap.
	body := fmt.Sprintf(`{"text":"%s"}`, strings.Repeat("y", 2_000))
	w := postRaw(r, "actor-1", uniqueKey(t), body, int64(len(body)))

	if w.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201 for a maximum-length comment", w.Code)
	}
	if executions != 1 {
		t.Fatalf("handler ran %d times, want 1", executions)
	}
}

// TestClaimInstallsATTLAtomically covers the three-command claim window.
//
// HSETNX, HSET and EXPIRE used to be separate round trips; a process death
// between the first and the last left a key claimed with no expiry, which
// answered every later retry of that intent with "in progress" forever.
func TestClaimInstallsATTLAtomically(t *testing.T) {
	rdb := newTestRedis(t)
	var executions int64
	// Hold the handler so the claim is observable mid-flight.
	r := router(rdb, &executions, 200*time.Millisecond)
	key := uniqueKey(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		post(r, "actor-1", key, `{"text":"ttl"}`)
	}()

	time.Sleep(50 * time.Millisecond)
	// Same params the router resolved for /v1/posts/p1/comments.
	redisKey := idempotencyRedisKey(
		"actor-1", http.MethodPost, "/v1/posts/:postId/comments",
		gin.Params{{Key: "postId", Value: "p1"}}, key,
	)
	ttl, err := rdb.TTL(context.Background(), redisKey).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= 0 {
		t.Fatalf("claim has no TTL (%v); a crash here would strand the key", ttl)
	}
	<-done
}

// TestCommentFingerprintSeparatesPosts covers the target-scoping gap.
//
// The Redis key uses the ROUTE TEMPLATE, so it cannot tell two posts apart on
// its own, and the body is only {"text":…}. Without the post id in the
// semantic fingerprint, the same key and text aimed at another post would
// replay the first post's comment.
func TestCommentFingerprintSeparatesPosts(t *testing.T) {
	a := CommentFingerprint("post-a", "same text")
	b := CommentFingerprint("post-b", "same text")
	if a == b {
		t.Fatal("fingerprint ignores the post id; a key could replay another post's comment")
	}
	if a != CommentFingerprint("post-a", "  same text  ") {
		t.Fatal("fingerprint is not whitespace-normalized; one intent would look like two")
	}
	if a == CommentFingerprint("post-a", "different text") {
		t.Fatal("fingerprint ignores the text; an edited comment could replay the original")
	}
}
