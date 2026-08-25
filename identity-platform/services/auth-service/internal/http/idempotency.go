package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-shared/api"
)

// IdempotencyHeader is the request header carrying the client's key.
const IdempotencyHeader = "Idempotency-Key"

// idempotencyStore is the consumer-owned boundary for the middleware.
type idempotencyStore interface {
	LookupIdempotentResponse(ctx context.Context, endpoint, key, requestHash string) (*store.IdempotentResponse, error)
	SaveIdempotentResponse(ctx context.Context, endpoint, key, requestHash string, statusCode int, body []byte) error
}

// maxIdempotencyKeyLen bounds what is accepted. A key is an opaque client
// token; anything longer is either a bug or an attempt to bloat the table.
const maxIdempotencyKeyLen = 200

// Idempotency replays the original response for a repeated request.
//
// A client that times out mid-write cannot tell "not created" from "created,
// response lost". Without this the only thing standing between a retry and a
// duplicate is a unique constraint — and a constraint violation is not
// idempotency: it returns USER_EXISTS to someone who never saw a success.
//
// The header is OPTIONAL. A caller that omits it gets exactly the old
// behaviour, so this cannot break an existing client.
//
// Only 2xx responses are stored. A failed attempt must remain retryable —
// replaying a 500 for 24 hours would turn a transient fault into a permanent
// one for that key.
func Idempotency(s idempotencyStore, endpoint string, log logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// No store configured (unit tests, or a deployment that has not wired
		// it) — behave exactly as before this middleware existed.
		if s == nil {
			c.Next()
			return
		}
		key := c.GetHeader(IdempotencyHeader)
		if key == "" {
			c.Next()
			return
		}
		if len(key) > maxIdempotencyKeyLen {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST",
				"Idempotency-Key is too long.", nil, nil)
			c.Abort()
			return
		}

		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST",
				"Could not read request body.", nil, nil)
			c.Abort()
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		sum := sha256.Sum256(bodyBytes)
		requestHash := hex.EncodeToString(sum[:])

		stored, err := s.LookupIdempotentResponse(c.Request.Context(), endpoint, key, requestHash)
		switch {
		case errors.Is(err, store.ErrIdempotencyKeyReused):
			// Same key, different body. Replaying would report success for a
			// request that was never processed.
			api.Error(c.Writer, http.StatusUnprocessableEntity, "IDEMPOTENCY_KEY_REUSED",
				"This Idempotency-Key was already used with a different request.", nil, nil)
			c.Abort()
			return
		case err != nil:
			// Fail OPEN. This is a duplicate-suppression convenience, not a
			// safety control: a storage blip must not stop people registering.
			// Worst case a retry behaves as it did before this existed.
			log.Warn("idempotency lookup failed — proceeding without replay",
				"event", "idempotency_lookup_failed", "endpoint", endpoint, "err", err)
		case stored != nil:
			c.Header("Idempotent-Replay", "true")
			c.Data(stored.StatusCode, "application/json", stored.Body)
			c.Abort()
			return
		}

		recorder := &responseRecorder{ResponseWriter: c.Writer, body: &bytes.Buffer{}}
		c.Writer = recorder
		c.Next()

		if recorder.status >= http.StatusOK && recorder.status < http.StatusMultipleChoices {
			if err := s.SaveIdempotentResponse(
				c.Request.Context(), endpoint, key, requestHash,
				recorder.status, recorder.body.Bytes(),
			); err != nil {
				log.Warn("failed to store idempotent response — a retry will re-execute",
					"event", "idempotency_store_failed", "endpoint", endpoint, "err", err)
			}
		}
	}
}

// logger is the narrow slice of *slog.Logger this file needs.
type logger interface {
	Warn(msg string, args ...any)
}

// responseRecorder captures the status and body so they can be replayed.
type responseRecorder struct {
	gin.ResponseWriter
	body   *bytes.Buffer
	status int
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		// Gin defaults to 200 when a handler writes without WriteHeader.
		r.status = http.StatusOK
	}
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

func (r *responseRecorder) WriteString(s string) (int, error) {
	return r.Write([]byte(s))
}
