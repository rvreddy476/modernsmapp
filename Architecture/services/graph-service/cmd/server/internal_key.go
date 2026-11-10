package main

import (
	"errors"
	"strings"
)

// errInternalKeyRequired is why production refuses to boot without
// INTERNAL_SERVICE_KEY (10 Nov 2026 security review, M12).
var errInternalKeyRequired = errors.New("INTERNAL_SERVICE_KEY is required in production: " +
	"without it every /v1/graph and /v1/internal/graph route is unauthenticated")

// requireInternalKey is the production boot gate. Outside production an
// unset key only warns, as before (see main), so local stacks keep working.
func requireInternalKey(key string, production bool) error {
	if strings.TrimSpace(key) == "" && production {
		return errInternalKeyRequired
	}
	return nil
}

// isProductionEnv reads APP_ENV, ENVIRONMENT, then ENV; the first one set
// decides. Same rule as search-service's IsProductionEnv.
func isProductionEnv(getenv func(string) string) bool {
	for _, key := range []string{"APP_ENV", "ENVIRONMENT", "ENV"} {
		switch strings.ToLower(strings.TrimSpace(getenv(key))) {
		case "production", "prod":
			return true
		case "":
			continue
		default:
			return false
		}
	}
	return false
}
