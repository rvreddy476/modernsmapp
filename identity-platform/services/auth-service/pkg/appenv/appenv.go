// Package appenv decides whether this process is running in production.
//
// Module 3 LB-1. It is a `pkg/` package, not `internal/`, so the CI-only
// edge-auth contract module can drive THIS implementation and the gateway's
// side from the same environment.
//
// The failure being guarded is specific and was live: the Helm values set
// `env: ENV: prod`, while both services originally consulted only APP_ENV and
// ENVIRONMENT. Production therefore ran DEVELOPMENT token rules — HS256
// accepted, issuer unpinned — in the one environment the hardening exists for.
//
// If the gateway and the mint side ever disagree about what "production"
// means, one hardens and the other does not, and every token is rejected at
// the edge. Neither service can detect that alone.
package appenv

import (
	"os"
	"strings"
)

// ProductionEnvVars is consulted in order. It MUST match the gateway's
// tokenpolicy.ProductionEnvVars, including the order — an explicit
// higher-priority value has to win so a stale ENV cannot override a
// deliberate APP_ENV.
var ProductionEnvVars = []string{"APP_ENV", "ENVIRONMENT", "ENV"}

// IsProduction reports whether production rules apply.
func IsProduction() bool {
	for _, key := range ProductionEnvVars {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case "production", "prod":
			return true
		case "":
			continue
		default:
			// An explicit non-production value on a higher-priority variable
			// wins, so APP_ENV=development is not overridden by a stale
			// ENV=prod left in a manifest.
			return false
		}
	}
	return false
}
