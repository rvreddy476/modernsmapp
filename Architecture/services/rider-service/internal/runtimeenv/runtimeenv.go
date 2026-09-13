// Package runtimeenv decides whether rider-service is running in production,
// for the guards that must fail closed there.
package runtimeenv

import "strings"

// ProductionEnvVars are the variables consulted. Helm values set ENV
// (`ENV: prod`), compose-production sets APP_ENV, and other services in this
// repo read DEPLOY_ENV and ENVIRONMENT; all four count.
var ProductionEnvVars = []string{"DEPLOY_ENV", "APP_ENV", "ENVIRONMENT", "ENV"}

// IsProduction reports whether any of ProductionEnvVars says prod or
// production. Any signal wins: these answers only ever harden the process,
// so a stale production value must not be overridden by a development one.
func IsProduction(getenv func(string) string) bool {
	for _, key := range ProductionEnvVars {
		switch strings.ToLower(strings.TrimSpace(getenv(key))) {
		case "prod", "production":
			return true
		}
	}
	return false
}
