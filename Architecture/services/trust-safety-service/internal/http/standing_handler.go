// Standing (Copyright Match plan section 6.4, P-5): the one question
// post-service may ask trust-safety before it publishes on a user's behalf.
//
//	GET /v1/internal/standing/:userId
//
// Two guards, both required. The route is registered behind
// RequireInternalKey, and the request must ALSO carry a service token
// (X-Service-Authorization: Bearer <token>) minted by a caller on
// StandingCallers with the operation OpStandingRead in its scope. The key
// alone proves nothing (the gateway stamps it on edge traffic) and the
// admin-service token family proves the wrong thing: an admin token, even
// one carrying standing.read, is refused here, and a post-service token is
// refused on every admin route (admin_token.go checks the issuer).
//
// The answer is the canonical standing contract, pinned byte-for-byte by
// testdata/contracts/standing.v1.json:
//
//	{"data":{"standing":"ok"|"restricted"|"suspended",
//	         "policy_version":"standing-v1",
//	         "suspended_until":RFC3339|null,
//	         "active_strikes":[{"id","severity","reason","case_id"|null,
//	                            "issued_at","expires_at"}]}}
//
// It is served with a strong ETag over the body and
// Cache-Control: private, max-age=60, so post-service caches an answer
// (allow or deny) for 60 s and revalidates with If-None-Match (304).
package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// StandingPath is the standing route.
const StandingPath = "/v1/internal/standing/:userId"

// OpStandingRead is the service-token operation the standing route
// requires. Deployment registers post-service with exactly this op:
//
//	SERVICE_CALLERS=admin-service,post-service
//	SERVICE_CALLER_POST_SERVICE_KID=p1
//	SERVICE_CALLER_POST_SERVICE_PUBKEY=<base64 ed25519 public key>
//	SERVICE_CALLER_POST_SERVICE_OPS=trust_safety:standing.read
const OpStandingRead = "trust_safety:standing.read"

// StandingCallers is the allowlist of token issuers admitted to the
// standing route. Nobody else, whatever their scope.
var StandingCallers = []string{"post-service"}

// StandingMaxAge is how long a caller may cache an answer.
const StandingMaxAge = 60 * time.Second

// Error codes for the service-caller path.
const (
	CodeServiceCallerRefused = "SERVICE_CALLER_REFUSED"
	CodeServiceScopeDenied   = "SERVICE_SCOPE_DENIED"
)

// standingReader is what the handler needs from the service; tests fake it.
type standingReader interface {
	Standing(ctx context.Context, userID uuid.UUID) (*service.Standing, error)
}

// WithStandingReader replaces the standing source (tests).
func (h *Handler) WithStandingReader(r standingReader) *Handler {
	h.standing = r
	return h
}

// requireServiceCaller admits ONLY a service token whose issuer is on
// callers and whose scope holds op. Refused: no verifier (401), no token
// (401), a token that fails verification (403 SERVICE_CALLER_REFUSED), an
// issuer not on the list (403 SERVICE_CALLER_REFUSED), a token without the
// op (403 SERVICE_SCOPE_DENIED). Identity headers, X-Scopes and the
// internal key riding on the request add nothing.
func (h *Handler) requireServiceCaller(op string, callers ...string) gin.HandlerFunc {
	if op == "" || len(callers) == 0 {
		panic("trust-safety: requireServiceCaller needs an operation and at least one caller")
	}
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		if h.verifier == nil || h.verifier.Callers() == 0 {
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceCredentialRequired,
				"service tokens are not accepted by this deployment", nil)
			c.Abort()
			return
		}
		raw := rawServiceToken(c)
		if raw == "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceCredentialRequired,
				"a service token is required", nil)
			c.Abort()
			return
		}
		v, err := h.verifier.Verify(raw, op, "")
		if err != nil {
			if errors.Is(err, servicetoken.ErrScopeDenied) {
				slog.WarnContext(ctx, "trust-safety: service token lacks the route operation", "required", op, "path", c.Request.URL.Path)
				api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceScopeDenied,
					"the token does not carry the operation this route requires", gin.H{"required": op})
				c.Abort()
				return
			}
			slog.WarnContext(ctx, "trust-safety: service token refused", "reason", err.Error(), "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceCallerRefused, "service token rejected", nil)
			c.Abort()
			return
		}
		allowed := false
		for _, name := range callers {
			allowed = allowed || name == v.Issuer
		}
		if !allowed {
			slog.WarnContext(ctx, "trust-safety: service route called by a caller not on its allowlist", "issuer", v.Issuer, "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceCallerRefused, "service token rejected", nil)
			c.Abort()
			return
		}
		c.Next()
	}
}

// standingResponse is the wire shape. Times are RFC3339 strings, not
// time.Time, so the body is byte-stable (no nanoseconds, always UTC).
type standingResponse struct {
	Standing       string           `json:"standing"`
	PolicyVersion  string           `json:"policy_version"`
	SuspendedUntil *string          `json:"suspended_until"`
	ActiveStrikes  []standingStrike `json:"active_strikes"`
}

type standingStrike struct {
	ID        string  `json:"id"`
	Severity  string  `json:"severity"`
	Reason    string  `json:"reason"`
	CaseID    *string `json:"case_id"`
	IssuedAt  string  `json:"issued_at"`
	ExpiresAt string  `json:"expires_at"`
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func standingBody(st *service.Standing) standingResponse {
	out := standingResponse{
		Standing:      st.Standing,
		PolicyVersion: st.PolicyVersion,
		ActiveStrikes: []standingStrike{},
	}
	if st.SuspendedUntil != nil {
		s := rfc3339(*st.SuspendedUntil)
		out.SuspendedUntil = &s
	}
	for _, s := range st.ActiveStrikes {
		item := standingStrike{
			ID:        s.ID.String(),
			Severity:  s.Severity,
			Reason:    s.Reason,
			IssuedAt:  rfc3339(s.CreatedAt),
			ExpiresAt: rfc3339(s.ExpiresAt),
		}
		if s.CaseID != nil {
			id := s.CaseID.String()
			item.CaseID = &id
		}
		out.ActiveStrikes = append(out.ActiveStrikes, item)
	}
	return out
}

// GetStanding — GET /v1/internal/standing/:userId (internal key + service
// token, see the package comment).
func (h *Handler) GetStanding(c *gin.Context) {
	ctx := c.Request.Context()
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil || userID == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid user ID", nil)
		return
	}
	if h.standing == nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "STANDING_UNAVAILABLE", "standing is unavailable", nil)
		return
	}
	st, err := h.standing.Standing(ctx, userID)
	if err != nil {
		// Any failure is unknown standing: the caller fails closed.
		slog.ErrorContext(ctx, "trust-safety: standing failed", "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "STANDING_UNAVAILABLE", "standing could not be evaluated", nil)
		return
	}
	body, err := json.Marshal(api.Response{Data: standingBody(st)})
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "STANDING_UNAVAILABLE", "standing could not be encoded", nil)
		return
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	hdr := c.Writer.Header()
	hdr.Set("ETag", etag)
	hdr.Set("Cache-Control", "private, max-age=60")
	hdr.Set("Vary", ServiceAuthHeader)
	if etagMatches(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		return
	}
	hdr.Set("Content-Type", "application/json")
	c.Status(http.StatusOK)
	_, _ = c.Writer.Write(body)
}

// etagMatches reports whether an If-None-Match header names etag (a weak
// prefix is ignored; "*" matches).
func etagMatches(ifNoneMatch, etag string) bool {
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			return true
		}
	}
	return false
}
