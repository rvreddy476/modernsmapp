package http

import (
	"fmt"
	"strings"

	"github.com/atpost/shared/servicetoken"
)

// AudienceChat is the audience group-service accepts on service tokens.
// It is the Chat dashboard's audience, shared with channel-service and
// community-service; the permission in scope names the action.
const AudienceChat = "chat"

// ServiceAuthHeader carries a caller's service token. The gateway never sets
// it, and Authorization stays free for the gateway's own use.
const ServiceAuthHeader = "X-Service-Authorization"

// ServiceCallersFromEnv builds the service-token verifier for the token-only
// admin family (admin_token.go). Same shape as food-service:
//
//	SERVICE_CALLERS=admin-service
//	SERVICE_CALLER_ADMIN_SERVICE_KID=a1
//	SERVICE_CALLER_ADMIN_SERVICE_PUBKEY=<base64 ed25519 public key>
//	SERVICE_CALLER_ADMIN_SERVICE_OPS=chat:stats.read,chat:reports.read,...
//
// A blank SERVICE_CALLERS returns (nil, nil): no token is accepted and the
// admin family answers 401. A named caller with a missing key or an empty
// operation list is a configuration error, never "allow everything".
func ServiceCallersFromEnv(getenv func(string) string) (*servicetoken.Verifier, error) {
	raw := strings.TrimSpace(getenv("SERVICE_CALLERS"))
	if raw == "" {
		return nil, nil
	}
	v := servicetoken.NewVerifier(AudienceChat)
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		prefix := "SERVICE_CALLER_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		kid := strings.TrimSpace(getenv(prefix + "_KID"))
		pub := strings.TrimSpace(getenv(prefix + "_PUBKEY"))
		ops := serviceCallerList(getenv(prefix + "_OPS"))
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

func serviceCallerList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
