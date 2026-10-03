// Package runtimeenv decides whether doorstep-service is running in
// production, for the guards that must fail closed there.
//
// Doorstep is fail-closed (the deploy contract in deploy/services/
// doorstep-service and the compose block): ANYTHING but local, dev or
// development is production. Staging, QA, an unset ENV and a typo all get
// production semantics; only an explicit development value relaxes them.
package runtimeenv

import "strings"

// EnvVars are the variables consulted. Helm values set ENV, compose-production
// sets APP_ENV, and other services in this repo read DEPLOY_ENV and
// ENVIRONMENT; all four count.
var EnvVars = []string{"DEPLOY_ENV", "APP_ENV", "ENVIRONMENT", "ENV"}

// developmentValues are the only values that relax the production guards.
var developmentValues = map[string]bool{"local": true, "dev": true, "development": true}

// IsProduction reports whether the process must apply production guards:
// true unless at least one of EnvVars names a development environment and
// none names anything else. Any non-development signal wins: these answers
// only ever harden the process.
func IsProduction(getenv func(string) string) bool {
	sawDevelopment := false
	for _, key := range EnvVars {
		v := strings.ToLower(strings.TrimSpace(getenv(key)))
		if v == "" {
			continue
		}
		if !developmentValues[v] {
			return true
		}
		sawDevelopment = true
	}
	return !sawDevelopment
}

// IsDevelopment is the negation of IsProduction: an explicit local/dev/
// development environment with no other signal. Dev-only behaviour (the
// Hyderabad seed, mocks) requires it.
func IsDevelopment(getenv func(string) string) bool { return !IsProduction(getenv) }
