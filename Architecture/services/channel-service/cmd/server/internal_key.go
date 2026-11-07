package main

import (
	"errors"
	"strings"
)

// errInternalKeyRequired refuses a production boot without
// INTERNAL_SERVICE_KEY (audit 2026-11-07, X-L1). Every handler reads the
// actor from X-User-Id, which only the gateway may set; without the key
// gate, any pod that can reach this service could act as anyone.
var errInternalKeyRequired = errors.New("INTERNAL_SERVICE_KEY is required in production; channel handlers trust gateway identity headers")

// internalKeyForBoot returns the key to install, or errInternalKeyRequired
// when it is unset in production. Outside production an empty key is
// allowed (dev loops) and main logs the warning.
func internalKeyForBoot(getenv func(string) string) (string, error) {
	key := getenv("INTERNAL_SERVICE_KEY")
	if key == "" && isProduction(getenv) {
		return "", errInternalKeyRequired
	}
	return key, nil
}

// isProduction is group-service's detection, which this matches on
// purpose: the first of APP_ENV, ENVIRONMENT, ENV that is set decides, and
// only "prod" or "production" (any case, trimmed) mean production.
func isProduction(getenv func(string) string) bool {
	for _, key := range []string{"APP_ENV", "ENVIRONMENT", "ENV"} {
		switch strings.ToLower(strings.TrimSpace(getenv(key))) {
		case "prod", "production":
			return true
		case "":
			continue
		default:
			return false
		}
	}
	return false
}
