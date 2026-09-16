package http

// Private profile fields on the user-object reads.
//
// GET /v1/users/:userId and GET /v1/users/by-username/:username used to
// serialize the whole app.users row to ANY caller. The api-gateway proxies
// /v1/users to this service and lets a request without a token through with no
// identity, so an anonymous browser could read another user's first name,
// last name, date of birth and gender by user id or handle.
//
// The product already draws the line: identity-profile's public projection
// (GET /v1/profiles/:userId) omits first name, last name, dob and gender, and
// only the owner projection (/v1/profiles/me) carries them. These reads now
// follow the same rule:
//
//   - The owner — the gateway-set X-User-Id equals the profile's id — gets
//     every field.
//   - A sibling service presenting a verified service token
//     (X-Service-Authorization, shared/servicetoken, audience "user-service",
//     operation users:profile.read_private) and NO end-user identity gets
//     every field.
//   - Everyone else gets the public card: same JSON shape, with the private
//     keys omitted (they are omitempty pointers, so a nil drops the key).
//
// The internal service key is NOT a credential for private fields. The
// gateway injects X-Internal-Service-Key on EVERY proxied request, including
// anonymous ones, so "has the key and no X-User-Id" describes an anonymous
// browser exactly as well as it describes a sibling service. Same reasoning as
// dating-service's authorizeServiceCaller.
//
// Anonymous callers are not refused: the route doubles as a public profile
// card, and in-cluster callers that only need public fields (message-service's
// display name/avatar fallback, post-service's @mention resolution,
// group-service's handle-taken check) call it with the key and no identity —
// indistinguishable from an anonymous gateway request.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/shared/servicetoken"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ServiceAuthHeader carries a sibling service's token — the same header
// payments-service and dating-service read.
const ServiceAuthHeader = "X-Service-Authorization"

// AudienceUserService is the service-token audience this service accepts.
const AudienceUserService = "user-service"

// OpReadPrivateProfile is the operation a service token must be scoped to
// before a user read returns private profile fields.
const OpReadPrivateProfile = "users:profile.read_private"

// gatewayIdentityHeaders are the end-user identity headers the api-gateway
// strips from clients and sets only from a verified token
// (trustedIdentityHeaders in api-gateway/cmd/server/main.go).
var gatewayIdentityHeaders = []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"}

// userReader is the slice of the service the user-object reads need, so the
// real router can be exercised without Postgres or Redis.
type userReader interface {
	GetUser(ctx context.Context, id uuid.UUID) (*store.User, error)
	GetUserByUsername(ctx context.Context, username string) (*store.User, error)
}

// serviceTokenVerifier is the slice of *servicetoken.Verifier used here.
type serviceTokenVerifier interface {
	Verify(token, operation, refType string) (*servicetoken.Verified, error)
}

// WithServiceAuth installs the service-token verifier. Nil means no service
// caller can read private profile fields.
func (h *Handler) WithServiceAuth(v *servicetoken.Verifier) *Handler {
	if v == nil {
		h.verifier = nil
		return h
	}
	h.verifier = v
	return h
}

// hasGatewayIdentity reports whether the request carries any end-user
// identity header, i.e. it is a user request the gateway proxied.
func hasGatewayIdentity(c *gin.Context) bool {
	for _, name := range gatewayIdentityHeaders {
		if strings.TrimSpace(c.GetHeader(name)) != "" {
			return true
		}
	}
	return false
}

// mayReadPrivateProfile decides whether this caller sees ownerID's private
// profile fields. Identity is checked first: a request carrying a gateway-set
// user identity is judged as that user, whatever key or token it also carries.
func (h *Handler) mayReadPrivateProfile(c *gin.Context, ownerID uuid.UUID) bool {
	if raw := strings.TrimSpace(c.GetHeader("X-User-Id")); raw != "" {
		viewer, err := uuid.Parse(raw)
		return err == nil && viewer == ownerID
	}
	if hasGatewayIdentity(c) {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
	if token == "" || h.verifier == nil {
		return false
	}
	if _, err := h.verifier.Verify(token, OpReadPrivateProfile, ""); err != nil {
		slog.Warn("user-service: service token refused for private profile read",
			"reason", err.Error(), "path", c.Request.URL.Path)
		return false
	}
	return true
}

// publicProfile returns a copy of u without the private fields. It copies
// rather than mutates: the service hands back the same pointer it is caching.
func publicProfile(u *store.User) *store.User {
	cp := *u
	cp.FirstName = nil
	cp.LastName = nil
	cp.DoB = nil
	cp.Gender = nil
	return &cp
}

// profileFor is the one place a user object is shaped for its caller.
func (h *Handler) profileFor(c *gin.Context, u *store.User) *store.User {
	if h.mayReadPrivateProfile(c, u.ID) {
		return u
	}
	return publicProfile(u)
}

// ServiceCallersFromEnv builds the service-token verifier. Same shape as
// dating-service and payments-service:
//
//	SERVICE_CALLERS=chat-service
//	SERVICE_CALLER_CHAT_SERVICE_KID=c1
//	SERVICE_CALLER_CHAT_SERVICE_PUBKEY=<base64 ed25519 public key>
//	SERVICE_CALLER_CHAT_SERVICE_OPS=users:profile.read_private
//
// A blank SERVICE_CALLERS returns (nil, nil): no service caller reads private
// fields. A named caller with a missing key or an empty operation list is a
// configuration error, never "allow everything".
func ServiceCallersFromEnv(getenv func(string) string) (*servicetoken.Verifier, error) {
	return serviceCallersForAudience(getenv, AudienceUserService)
}

// serviceCallersForAudience builds a verifier for one audience from the
// SERVICE_CALLERS registry. A servicetoken.Verifier checks a single audience,
// so each audience this service accepts gets its own verifier over the same
// registered keys and operation lists (see AdminServiceCallersFromEnv).
func serviceCallersForAudience(getenv func(string) string, audience string) (*servicetoken.Verifier, error) {
	raw := strings.TrimSpace(getenv("SERVICE_CALLERS"))
	if raw == "" {
		return nil, nil
	}
	v := servicetoken.NewVerifier(audience)
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		prefix := "SERVICE_CALLER_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		kid := strings.TrimSpace(getenv(prefix + "_KID"))
		pub := strings.TrimSpace(getenv(prefix + "_PUBKEY"))
		ops := splitCallerList(getenv(prefix + "_OPS"))
		if kid == "" || pub == "" {
			return nil, fmt.Errorf("caller %q is missing %s_KID or %s_PUBKEY", name, prefix, prefix)
		}
		if len(ops) == 0 {
			return nil, fmt.Errorf("caller %q must declare %s_OPS", name, prefix)
		}
		if err := v.RegisterBase64(name, kid, pub, ops, nil); err != nil {
			return nil, fmt.Errorf("caller %q: %w", name, err)
		}
	}
	if v.Callers() == 0 {
		return nil, fmt.Errorf("SERVICE_CALLERS produced no usable entries")
	}
	return v, nil
}

func splitCallerList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
