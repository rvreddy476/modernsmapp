package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	// Retention for a COMPLETED result. The client may retry for a long time
	// after a lost response, and replaying is only useful while the record
	// survives.
	idempotencyTTL = 24 * time.Hour

	// Retention for an unfinished CLAIM. Bounds how long a key stays locked if
	// the process dies mid-handler: after this the key expires and a retry may
	// legitimately execute. Long enough to cover a slow handler, short enough
	// that a crash does not strand the user for a day.
	idempotencyClaimTTL = 2 * time.Minute

	idempotencyPrefix = "idempotency:"

	// A 2,000-character comment is the client-side cap; as JSON with escaping
	// that is comfortably under 8 KiB. Generous for the payload, small enough
	// that buffering one before validation costs nothing.
	maxIdempotentBodyBytes = 8 << 10

	stateInProgress = "in_progress"
	stateDone       = "done"
)

// claimScript takes the claim, binds the fingerprint and sets the TTL as one
// atomic step. Returns 1 when this caller won the claim, 0 when someone else
// already holds it.
var claimScript = redis.NewScript(`
if redis.call("HSETNX", KEYS[1], "state", ARGV[1]) == 1 then
  redis.call("HSET", KEYS[1], "fingerprint", ARGV[2])
  redis.call("PEXPIRE", KEYS[1], ARGV[3])
  return 1
end
return 0
`)

// responseCapture wraps gin.ResponseWriter to capture the response body and status.
type responseCapture struct {
	gin.ResponseWriter
	body   *bytes.Buffer
	status int
}

func (r *responseCapture) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

func (r *responseCapture) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// idempotencyRedisKey scopes a client-supplied key to the request it belongs to.
//
// The key used to be the bare client string, which made it a GLOBAL namespace:
// any two callers picking the same value shared a cache entry, and reusing a
// key on a different route could return a completely unrelated response body.
//
// BINDING THE CONCRETE TARGET, NOT JUST THE ROUTE PATTERN.
//
// An earlier version hashed only `c.FullPath()`, the registered pattern. For
// `/v1/posts/post-a/comments` and `/v1/posts/post-b/comments` that string is
// identical — `/v1/posts/:postId/comments` — so the same actor, key and bytes
// aimed at a DIFFERENT post hit the same cache entry and were served post A's
// stored response. The handler never ran, and the PostgreSQL record keyed by
// (actor, post, client key) was never consulted, so the durable authority
// could not save it. Including the resolved path parameters makes the cache
// agree with that record about what "the same request" means.
//
// [params] is gin's resolved route parameters in route order, so the encoding
// is deterministic for a given route.
func idempotencyRedisKey(actor, method, route string, params gin.Params, clientKey string) string {
	var b strings.Builder
	b.WriteString(actor)
	b.WriteString("\x00")
	b.WriteString(method)
	b.WriteString("\x00")
	b.WriteString(route)
	for _, p := range params {
		b.WriteString("\x00")
		b.WriteString(p.Key)
		b.WriteString("\x01")
		b.WriteString(p.Value)
	}
	b.WriteString("\x00")
	b.WriteString(clientKey)
	h := sha256.Sum256([]byte(b.String()))
	return idempotencyPrefix + hex.EncodeToString(h[:])
}

// readCappedBody buffers the request body under a hard size limit and restores
// it for the handler. Reports false when it has already written a response.
//
// Separated from [Idempotency] so the cap can run before the optional-key
// early return: the limit protects the ENDPOINT, and a caller must not be able
// to lift it by omitting a header.
func readCappedBody(c *gin.Context) ([]byte, bool) {
	if c.Request.Body == nil {
		return nil, true
	}
	// MaxBytesReader rather than a Content-Length check: a hostile client
	// simply omits the header and sends a chunked body of any size.
	limited := http.MaxBytesReader(c.Writer, c.Request.Body, maxIdempotentBodyBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
				"error": gin.H{
					"code":    "REQUEST_TOO_LARGE",
					"message": "Request body exceeds the maximum size for this endpoint.",
				},
			})
			return nil, false
		}
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"error": gin.H{"code": "INVALID_REQUEST", "message": "could not read request body"},
		})
		return nil, false
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	return raw, true
}

func bodyFingerprint(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// CommentFingerprint binds a comment intent to its post and its normalized text.
//
// The post id is included because the request BODY does not carry it — it is
// only `{"text":"…"}` — and this fingerprint is what the durable PostgreSQL
// record is keyed against. The Redis cache in front of it now binds the
// concrete target too (see idempotencyRedisKey), so both layers agree on what
// "the same request" means rather than one being narrower than the other.
//
// The text is trimmed so that two attempts at one intent are not treated as
// different payloads over whitespace the server itself ignores.
func CommentFingerprint(postID, text string) string {
	sum := sha256.Sum256([]byte(postID + "\x00" + strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])
}

// Idempotency returns a Gin middleware that prevents duplicate mutations.
//
// GUARANTEES
//
//  1. Exactly one request per (actor, method, route, key) executes the handler.
//     The claim is taken with HSETNX, which is atomic in Redis, so two
//     concurrent requests cannot both observe "not present" and both insert.
//     The previous implementation read with HGETALL, ran the handler, then
//     wrote — a check-then-act window in which two same-key requests raced and
//     both created a comment.
//
//  2. A replay only ever returns the response for the SAME request body. The
//     body fingerprint is stored with the claim; a later request presenting the
//     same key with different content gets a deterministic 409 rather than
//     someone else's — or an earlier draft's — response.
//
//  3. A follower arriving while the first request is still running is told to
//     retry rather than being allowed to execute. It must not duplicate the
//     write, and it cannot be served a result that does not exist yet.
//
// # FAILURE HANDLING
//
// Redis errors fail CLOSED with 503. Idempotency-Key is a safety promise;
// silently ignoring it during an outage turns one tap into a duplicate write.
// A non-2xx handler result RELEASES the claim, so a genuine retry can run —
// otherwise a transient 500 would lock the key for its full TTL.
func Idempotency(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only apply to mutating methods
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodDelete {
			c.Next()
			return
		}

		// The body is capped FIRST, before the optional-key check.
		//
		// The cap used to sit after the early return for a missing
		// Idempotency-Key, so omitting that header — which this route allows —
		// skipped it entirely. `CommentRequest.Text` has no server-side
		// maximum, so an authenticated caller could drop one header and make
		// ShouldBindJSON allocate a string of any size. The limit belongs to
		// the ENDPOINT, not to the idempotency feature, so nothing optional may
		// come before it.
		raw, ok := readCappedBody(c)
		if !ok {
			return
		}

		clientKey := c.GetHeader("Idempotency-Key")
		if clientKey == "" {
			// No key provided — pass through without idempotency checking.
			// The body has already been capped and restored above.
			c.Next()
			return
		}

		actor := c.GetHeader("X-User-Id")
		fingerprint := bodyFingerprint(raw)
		redisKey := idempotencyRedisKey(actor, c.Request.Method, c.FullPath(), c.Params, clientKey)
		ctx := context.Background()

		// Claim, bind and expire in ONE operation.
		//
		// These were three commands. A process death between HSETNX and
		// EXPIRE left a key with no TTL: claimed forever, and every later
		// retry of that intent answered "in progress" until someone deleted
		// the key by hand. Lua runs atomically in Redis, so the key never
		// exists in a half-installed state.
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
			// Release the claim so a legitimate retry is not locked out by a
			// transient failure.
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
		if _, err := pipe.Exec(ctx); err != nil {
			// The write already happened, so the response stands. The record
			// is dropped instead of left half-written: a claim with no result
			// would answer every later retry with "in progress" forever.
			rdb.Del(ctx, redisKey)
			fmt.Printf("[idempotency] failed to store result for key %s: %v\n", redisKey, err)
		}
	}
}

// serveExisting answers a request whose key is already claimed by another.
func serveExisting(
	c *gin.Context,
	rdb *redis.Client,
	ctx context.Context,
	redisKey string,
	fingerprint string,
) {
	record, err := rdb.HGetAll(ctx, redisKey).Result()
	if err != nil && err != redis.Nil {
		abortIdempotencyUnavailable(c)
		return
	}
	if len(record) == 0 {
		// The claim expired between HSETNX and this read. Treat it as a
		// conflict rather than executing: the earlier request may still be
		// running, and running now risks the duplicate this middleware exists
		// to prevent.
		abortIdempotencyInProgress(c)
		return
	}

	// A key is bound to one payload. Same key, different body is a client bug
	// or a replay attempt, and answering it with the stored body would show
	// the caller a result for content they did not send.
	if stored, ok := record["fingerprint"]; ok && stored != fingerprint {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": gin.H{
				"code":    "IDEMPOTENCY_KEY_REUSED",
				"message": "This Idempotency-Key was already used for a different request.",
			},
		})
		return
	}

	if record["state"] != stateDone {
		abortIdempotencyInProgress(c)
		return
	}

	status := http.StatusOK
	if s, ok := record["status"]; ok {
		if parsed, err := strconv.Atoi(s); err == nil {
			status = parsed
		}
	}
	c.Data(status, "application/json", []byte(record["body"]))
	c.Abort()
}

func abortIdempotencyUnavailable(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
		"error": gin.H{
			"code":    "IDEMPOTENCY_UNAVAILABLE",
			"message": "Idempotency cache temporarily unavailable, retry with the same Idempotency-Key.",
		},
	})
}

func abortIdempotencyInProgress(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusConflict, gin.H{
		"error": gin.H{
			"code":    "IDEMPOTENCY_IN_PROGRESS",
			"message": "A request with this Idempotency-Key is already being processed. Retry shortly.",
		},
	})
}
