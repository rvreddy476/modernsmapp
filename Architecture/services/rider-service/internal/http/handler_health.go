// Health endpoints are registered directly via shared/health.Checker in
// cmd/server/main.go. This file is a placeholder for future readiness checks
// specific to rider-service (e.g. wallet-service reachability, DigiLocker
// partner ping). None in Sprint 1.
package http

// _ keeps the package non-empty even if the build never imports a symbol
// from this file. Referenced via the declaration below; no runtime cost.
var _ = struct{}{}

