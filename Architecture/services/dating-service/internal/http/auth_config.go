package http

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/atpost/shared/servicetoken"
)

// ErrInternalKeyRequired is returned by ResolveInternalKey when the service
// would otherwise boot with no credential on its routes outside local/dev.
var ErrInternalKeyRequired = errors.New("INTERNAL_SERVICE_KEY is required unless ENV is local or dev: " +
	"without it every /v1/dating route accepts a spoofed X-User-Id")

// IsLocalEnv is true only for ENV local, dev and development. A blank ENV is
// NOT local — same rule food-service uses (foodpii.IsProduction,
// payments.legacyAllowed), so a deployment that forgets ENV fails closed.
func IsLocalEnv(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "local", "dev", "development":
		return true
	}
	return false
}

// ResolveInternalKey applies the fail-closed boot rule to
// INTERNAL_SERVICE_KEY. It returns the key; a non-empty warning when the
// service may boot but is running without one (local/dev only); or
// ErrInternalKeyRequired, on which main refuses to start.
func ResolveInternalKey(getenv func(string) string) (key, warning string, err error) {
	key = strings.TrimSpace(getenv("INTERNAL_SERVICE_KEY"))
	if key != "" {
		return key, "", nil
	}
	env := getenv("ENV")
	if !IsLocalEnv(env) {
		return "", "", fmt.Errorf("%w (ENV=%q)", ErrInternalKeyRequired, env)
	}
	return "", "dating-service: INTERNAL_SERVICE_KEY not set (ENV=" + strings.TrimSpace(env) + ") — " +
		"/v1/dating routes are not key-gated and the internal family admits service tokens only. Local/dev only.", nil
}

// ErrIdentityProfileURLRequired is returned by ResolveIdentityProfileURL when
// the service would otherwise boot trusting the client's birth date outside
// local/dev.
var ErrIdentityProfileURLRequired = errors.New("IDENTITY_PROFILE_SERVICE_URL is required unless ENV is local or dev: " +
	"without it dating takes birth date and first name from the dating client")

// ResolveIdentityProfileURL applies the D1-style boot rule to
// IDENTITY_PROFILE_SERVICE_URL (identity-profile's internal identity read).
// It returns the base URL (trailing slash trimmed); a non-empty warning when
// the service may boot without one (local/dev only, the interim client rule
// applies); or an error, on which main refuses to start. A set but malformed
// URL is refused in every environment.
func ResolveIdentityProfileURL(getenv func(string) string) (baseURL, warning string, err error) {
	raw := strings.TrimSpace(getenv("IDENTITY_PROFILE_SERVICE_URL"))
	if raw != "" {
		u, perr := url.Parse(raw)
		if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", "", fmt.Errorf("IDENTITY_PROFILE_SERVICE_URL must be an absolute http(s) URL")
		}
		return strings.TrimRight(raw, "/"), "", nil
	}
	env := getenv("ENV")
	if !IsLocalEnv(env) {
		return "", "", fmt.Errorf("%w (ENV=%q)", ErrIdentityProfileURLRequired, env)
	}
	return "", "dating-service: IDENTITY_PROFILE_SERVICE_URL not set (ENV=" + strings.TrimSpace(env) + ") — " +
		"birth date and first name come from the dating client under the interim lock-once rule. Local/dev only.", nil
}

// ServiceCallersFromEnv builds the service-token verifier for the
// /v1/dating/internal family. Same shape as payments-service, minus
// reference types (dating operations are not per-reference):
//
//	SERVICE_CALLERS=notification-service,chat-service
//	SERVICE_CALLER_NOTIFICATION_SERVICE_KID=n1
//	SERVICE_CALLER_NOTIFICATION_SERVICE_PUBKEY=<base64 ed25519 public key>
//	SERVICE_CALLER_NOTIFICATION_SERVICE_OPS=dating:profile.preview
//
// A blank SERVICE_CALLERS returns (nil, nil): the legacy internal key stays
// the only service credential. A named caller with a missing key or an empty
// operation list is a configuration error, never "allow everything".
func ServiceCallersFromEnv(getenv func(string) string) (*servicetoken.Verifier, error) {
	raw := strings.TrimSpace(getenv("SERVICE_CALLERS"))
	if raw == "" {
		return nil, nil
	}
	v := servicetoken.NewVerifier(AudienceDating)
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		prefix := "SERVICE_CALLER_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		kid := strings.TrimSpace(getenv(prefix + "_KID"))
		pub := strings.TrimSpace(getenv(prefix + "_PUBKEY"))
		ops := splitList(getenv(prefix + "_OPS"))
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

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
