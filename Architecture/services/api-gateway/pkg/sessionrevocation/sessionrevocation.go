// Package sessionrevocation refuses tokens whose auth-service session has
// been revoked.
//
// Admin console plan, Wave 0 lane A2 (gateway half). The production token
// policy already requires a `sid` claim "so revocation can be reasoned
// about", but until this package the gateway never looked: a revoked session,
// or a user whose role was just taken away, kept working until the access
// token expired (24 h in the dev container, 8760 h as the compose default).
//
// auth-service marks a revoked session in Redis as `sess_revoked:<sid>` with a
// TTL covering the longest access token (identity-platform auth-service,
// internal/service/auth.go), and its own middleware answers 401
// SESSION_REVOKED for it. The gateway reads the same key and answers the same
// code, so a client sees one behaviour whichever layer refused it.
//
// When Redis cannot answer:
//
//   - privileged requests FAIL CLOSED with 503 SESSION_CHECK_UNAVAILABLE. A
//     request is privileged when its path is under /v1/admin or has an
//     `admin` segment (every product's admin routes), or when its token
//     carries a platform role (X-Admin-Role was stamped from the verified
//     scopes). Revoking a moderator must not be undone by a Redis blip.
//   - consumer requests FAIL OPEN, as today, so a Redis incident does not
//     take the whole platform down. Every such request is counted in
//     gateway_session_revocation_check_failures_total and logged, at most
//     once per logInterval.
//
// Lives under pkg/ because a new file under cmd/server/ is git-ignored.
package sessionrevocation

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/redis/go-redis/v9"
)

// KeyPrefix is auth-service's revocation key prefix. It must match
// auth-service's `"sess_revoked:" + sessionID.String()` exactly.
const KeyPrefix = "sess_revoked:"

// DefaultLookupTimeout bounds the Redis read so a slow Redis degrades a
// request by a quarter of a second rather than hanging it.
const DefaultLookupTimeout = 250 * time.Millisecond

const logInterval = 10 * time.Second

// Checker answers whether a session id has been revoked.
type Checker interface {
	IsRevoked(ctx context.Context, sessionID string) (bool, error)
}

// RedisChecker reads auth-service's revocation marks.
type RedisChecker struct {
	RDB     redis.UniversalClient
	Timeout time.Duration
}

// IsRevoked reports true when `sess_revoked:<sid>` holds a non-empty value,
// matching auth-service's own check. A missing key is not revoked.
func (c RedisChecker) IsRevoked(ctx context.Context, sessionID string) (bool, error) {
	if c.RDB == nil {
		return false, errors.New("sessionrevocation: no redis client")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultLookupTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	val, err := c.RDB.Get(ctx, KeyPrefix+sessionID).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return val != "", nil
}

type sessionIDKey struct{}

// WithSessionID records the verified token's session id. Only the gateway's
// token middleware calls it; nothing a client sends can set it.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, sessionID)
}

// SessionID returns the verified session id, or "" for an anonymous request or
// a legacy token without one.
func SessionID(ctx context.Context) string {
	id, _ := ctx.Value(sessionIDKey{}).(string)
	return id
}

// IsPrivilegedRequest reports whether a Redis failure must refuse the request.
func IsPrivilegedRequest(r *http.Request) bool {
	if r.Header.Get("X-Admin-Role") != "" {
		return true
	}
	return isAdminPath(r.URL.Path)
}

func isAdminPath(p string) bool {
	cleaned := strings.ToLower(path.Clean("/" + strings.ReplaceAll(p, `\`, "/")))
	if cleaned == "/v1/admin" || strings.HasPrefix(cleaned, "/v1/admin/") {
		return true
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == "admin" {
			return true
		}
	}
	return false
}

var checkFailures = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "gateway_session_revocation_check_failures_total",
	Help: "Session revocation lookups that could not be answered, by outcome (fail_open for consumer requests, fail_closed for privileged ones).",
}, []string{"outcome"})

var revokedRefusals = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "gateway_session_revoked_refusals_total",
	Help: "Requests refused at the gateway because their session was revoked.",
})

func init() {
	prometheus.MustRegister(checkFailures, revokedRefusals)
}

// FailOpenCount and FailClosedCount expose the counters to tests.
func FailOpenCount() float64   { return counterValue(checkFailures.WithLabelValues("fail_open")) }
func FailClosedCount() float64 { return counterValue(checkFailures.WithLabelValues("fail_closed")) }

func counterValue(c prometheus.Counter) float64 {
	var m dto.Metric
	if err := c.Write(&m); err != nil || m.Counter == nil {
		return 0
	}
	return m.Counter.GetValue()
}

// Middleware refuses revoked sessions. It must run after the token middleware
// that calls WithSessionID. A nil checker disables the lookup entirely (Redis
// not configured outside production); main.go refuses to start that way in
// production.
func Middleware(checker Checker, next http.Handler) http.Handler {
	if checker == nil {
		return next
	}
	var lastLog atomic.Int64
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sid := SessionID(r.Context())
		if sid == "" {
			next.ServeHTTP(w, r)
			return
		}
		revoked, err := checker.IsRevoked(r.Context(), sid)
		if err != nil {
			privileged := IsPrivilegedRequest(r)
			outcome := "fail_open"
			if privileged {
				outcome = "fail_closed"
			}
			checkFailures.WithLabelValues(outcome).Inc()
			now := time.Now().UnixNano()
			if prev := lastLog.Load(); now-prev >= int64(logInterval) && lastLog.CompareAndSwap(prev, now) {
				slog.Warn("session revocation check unavailable",
					"outcome", outcome, "path", r.URL.Path, "err", err)
			}
			if privileged {
				writeError(w, http.StatusServiceUnavailable, "SESSION_CHECK_UNAVAILABLE",
					"Session could not be verified; try again shortly")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if revoked {
			revokedRefusals.Inc()
			writeError(w, http.StatusUnauthorized, "SESSION_REVOKED", "Session has been revoked")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"` + message + `"}}`))
}
