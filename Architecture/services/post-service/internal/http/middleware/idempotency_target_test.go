package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// postTo sends to an explicit post id, with an optional Idempotency-Key.
// An empty key omits the header entirely — that is the keyless path.
// A contentLength of -1 models a chunked body that declares no length.
func postTo(
	r *gin.Engine,
	postID, actor, key, body string,
	contentLength int64,
) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/posts/"+postID+"/comments", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", actor)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if contentLength != 0 {
		req.ContentLength = contentLength
		if contentLength < 0 {
			req.TransferEncoding = []string{"chunked"}
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ── CLB-1: the Redis cache must not replay across posts ────────────────

// TestSameKeyOnDifferentPostsDoesNotReplay is the END-TO-END proof, through the
// router, that the fast path cannot serve post A's response for post B.
//
// The previous test for this was a false positive: it called CommentFingerprint
// directly, so it passed while the middleware short-circuited the request using
// a completely different value. c.FullPath() is "/v1/posts/:postId/comments"
// for BOTH posts, so hashing only the template made the two requests share one
// cache entry. The handler never ran for post B, and the PostgreSQL record
// keyed by (actor, post, client_key) was never reached to catch it.
//
// Both are independent intents under that PostgreSQL key, so both execute.
func TestSameKeyOnDifferentPostsDoesNotReplay(t *testing.T) {
	rdb := newTestRedis(t)
	var executions int64
	r := router(rdb, &executions, 0)
	key := uniqueKey(t)
	body := `{"text":"same bytes"}`

	a := postTo(r, "post-a", "actor-1", key, body, 0)
	b := postTo(r, "post-b", "actor-1", key, body, 0)

	if a.Code != http.StatusCreated || b.Code != http.StatusCreated {
		t.Fatalf("codes = %d, %d; want 201, 201", a.Code, b.Code)
	}
	if executions != 2 {
		t.Fatalf("handler executed %d times, want 2 (one per post)", executions)
	}
	if a.Body.String() == b.Body.String() {
		t.Fatalf("post-b was served post-a's response: %s", a.Body.String())
	}

	// The per-post promise still holds: a genuine retry on post-a replays.
	again := postTo(r, "post-a", "actor-1", key, body, 0)
	if again.Body.String() != a.Body.String() {
		t.Fatalf("retry on post-a did not replay:\n want=%s\n got =%s",
			a.Body.String(), again.Body.String())
	}
	if executions != 2 {
		t.Fatalf("retry on post-a re-executed the handler; executions=%d", executions)
	}
}

// legacyRouteTemplateKey reproduces the pre-fix derivation: actor, method,
// route TEMPLATE and client key, with no concrete target.
func legacyRouteTemplateKey(actor, method, route, clientKey string) string {
	h := sha256.Sum256([]byte(actor + "\x00" + method + "\x00" + route + "\x00" + clientKey))
	return idempotencyPrefix + "legacy:" + hex.EncodeToString(h[:])
}

// legacyIdempotency is the CURRENT middleware with only the key derivation
// reverted, so the control isolates exactly the fix under test and nothing else.
func legacyIdempotency(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodDelete {
			c.Next()
			return
		}
		raw, ok := readCappedBody(c)
		if !ok {
			return
		}
		clientKey := c.GetHeader("Idempotency-Key")
		if clientKey == "" {
			c.Next()
			return
		}
		actor := c.GetHeader("X-User-Id")
		fingerprint := bodyFingerprint(raw)
		redisKey := legacyRouteTemplateKey(actor, c.Request.Method, c.FullPath(), clientKey)
		ctx := context.Background()

		claimed, err := claimScript.Run(
			ctx, rdb, []string{redisKey},
			stateInProgress, fingerprint, idempotencyClaimTTL.Milliseconds(),
		).Int64()
		if err != nil {
			abortIdempotencyUnavailable(c)
			return
		}
		if claimed == 0 {
			serveExisting(c, rdb, ctx, redisKey, fingerprint)
			return
		}
		capture := &responseCapture{
			ResponseWriter: c.Writer,
			body:           &bytes.Buffer{},
			status:         http.StatusOK,
		}
		c.Writer = capture
		c.Next()
		if capture.status < 200 || capture.status >= 300 {
			rdb.Del(ctx, redisKey)
			return
		}
		pipe := rdb.Pipeline()
		pipe.HSet(ctx, redisKey, map[string]interface{}{
			"state":  stateDone,
			"status": strconv.Itoa(capture.status),
			"body":   capture.body.String(),
		})
		pipe.Expire(ctx, redisKey, idempotencyTTL)
		_, _ = pipe.Exec(ctx)
	}
}

// TestNegativeControlRouteTemplateKeyReplaysAcrossPosts is the load-bearing
// control for CLB-1. With the concrete target removed from the key, post B is
// served post A's response and the handler runs once. If this ever stops
// replaying, the test above has stopped proving that the target binding is what
// prevents it.
func TestNegativeControlRouteTemplateKeyReplaysAcrossPosts(t *testing.T) {
	rdb := newTestRedis(t)
	var executions int64

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/v1/posts/:postId/comments", legacyIdempotency(rdb), func(c *gin.Context) {
		atomic.AddInt64(&executions, 1)
		c.JSON(http.StatusCreated, gin.H{
			"data": gin.H{"id": fmt.Sprintf("comment-%d", atomic.LoadInt64(&executions))},
		})
	})

	key := uniqueKey(t)
	body := `{"text":"same bytes"}`
	a := postTo(r, "post-a", "actor-1", key, body, 0)
	b := postTo(r, "post-b", "actor-1", key, body, 0)

	if executions != 1 {
		t.Fatalf("negative control executed %d times; it must replay across posts "+
			"to prove the concrete-target binding is what prevents it", executions)
	}
	if a.Body.String() != b.Body.String() {
		t.Fatalf("negative control did not replay post-a's body for post-b:\n a=%s\n b=%s",
			a.Body.String(), b.Body.String())
	}
}

// ── CLB-2: the body cap must not depend on an optional header ──────────

// TestOversizedKeylessBodyRejected covers the bypass. The cap sat AFTER the
// early return for a missing Idempotency-Key, and this route allows the header
// to be absent, so omitting one optional header lifted the limit entirely and
// let ShouldBindJSON allocate a string of any size.
func TestOversizedKeylessBodyRejected(t *testing.T) {
	rdb := newTestRedis(t)
	var executions int64
	r := router(rdb, &executions, 0)

	huge := fmt.Sprintf(`{"text":"%s"}`, strings.Repeat("x", 64<<10))
	w := postTo(r, "p1", "actor-1", "", huge, int64(len(huge)))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413 for an oversized keyless body", w.Code)
	}
	if executions != 0 {
		t.Fatalf("handler ran %d times for an oversized keyless body, want 0", executions)
	}
	if !strings.Contains(w.Body.String(), "REQUEST_TOO_LARGE") {
		t.Fatalf("body = %s, want REQUEST_TOO_LARGE", w.Body.String())
	}
}

// TestOversizedKeylessChunkedBodyRejected removes the last escape hatch: no
// key AND no declared length.
func TestOversizedKeylessChunkedBodyRejected(t *testing.T) {
	rdb := newTestRedis(t)
	var executions int64
	r := router(rdb, &executions, 0)

	huge := fmt.Sprintf(`{"text":"%s"}`, strings.Repeat("x", 64<<10))
	w := postTo(r, "p1", "actor-1", "", huge, -1)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413 for an oversized keyless chunked body", w.Code)
	}
	if executions != 0 {
		t.Fatalf("handler ran %d times, want 0", executions)
	}
}

// TestKeylessBodyUnderTheCapIsAccepted is the control for the two above.
// Moving the cap earlier must not break an ordinary keyless request, and if the
// limit ever crept below a legitimate comment those tests would still pass
// while the endpoint had quietly become unusable.
func TestKeylessBodyUnderTheCapIsAccepted(t *testing.T) {
	rdb := newTestRedis(t)
	var executions int64
	r := router(rdb, &executions, 0)

	body := fmt.Sprintf(`{"text":"%s"}`, strings.Repeat("y", 2_000))
	w := postTo(r, "p1", "actor-1", "", body, int64(len(body)))

	if w.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201 for a keyless maximum-length comment", w.Code)
	}
	if executions != 1 {
		t.Fatalf("handler ran %d times, want 1", executions)
	}
}

// TestKeylessRequestStillReachesTheHandlerWithItsBody proves the body survives
// the new buffering. readCappedBody now runs for keyless requests too, so a
// failure to restore the reader would leave the handler seeing an empty body —
// a regression that every status-code assertion above would miss.
func TestKeylessRequestStillReachesTheHandlerWithItsBody(t *testing.T) {
	rdb := newTestRedis(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var seen string
	r.POST("/v1/posts/:postId/comments", Idempotency(rdb), func(c *gin.Context) {
		b, _ := io.ReadAll(c.Request.Body)
		seen = string(b)
		c.JSON(http.StatusCreated, gin.H{"data": gin.H{"id": "x"}})
	})

	body := `{"text":"keyless body must survive"}`
	if w := postTo(r, "p1", "actor-1", "", body, int64(len(body))); w.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201", w.Code)
	}
	if seen != body {
		t.Fatalf("handler saw %q, want %q", seen, body)
	}
}

// legacyKeyFirstIdempotency reproduces the pre-fix ORDERING: the optional-key
// early return happens before the body cap. Everything else is unchanged, so
// the control isolates exactly the ordering under test.
func legacyKeyFirstIdempotency() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodDelete {
			c.Next()
			return
		}
		// The bypass: no key, so the cap below is never reached.
		if c.GetHeader("Idempotency-Key") == "" {
			c.Next()
			return
		}
		if _, ok := readCappedBody(c); !ok {
			return
		}
		c.Next()
	}
}

// TestNegativeControlKeyCheckBeforeCapLetsOversizedBodiesThrough is the
// load-bearing control for CLB-2. With the old ordering an oversized KEYLESS
// body reaches the handler untouched. If this ever stops happening, the
// keyless 413 tests have stopped proving that the ordering is what closes it.
func TestNegativeControlKeyCheckBeforeCapLetsOversizedBodiesThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var executions int64
	var seenBytes int
	r := gin.New()
	r.POST("/v1/posts/:postId/comments", legacyKeyFirstIdempotency(), func(c *gin.Context) {
		atomic.AddInt64(&executions, 1)
		b, _ := io.ReadAll(c.Request.Body)
		seenBytes = len(b)
		c.JSON(http.StatusCreated, gin.H{"data": gin.H{"id": "x"}})
	})

	huge := fmt.Sprintf(`{"text":"%s"}`, strings.Repeat("x", 64<<10))
	w := postTo(r, "p1", "actor-1", "", huge, int64(len(huge)))

	if w.Code == http.StatusRequestEntityTooLarge {
		t.Fatal("negative control rejected the oversized keyless body; it must let it " +
			"through to prove the cap ordering is what closes the bypass")
	}
	if executions != 1 {
		t.Fatalf("negative control executed %d times, want 1", executions)
	}
	if seenBytes <= maxIdempotentBodyBytes {
		t.Fatalf("negative control handler saw %d bytes, want more than the %d cap",
			seenBytes, maxIdempotentBodyBytes)
	}
}
