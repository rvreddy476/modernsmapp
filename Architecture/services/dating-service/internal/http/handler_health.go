package http

// Health endpoints are registered directly by health.Checker.RegisterRoutes
// in cmd/server/main.go (mirroring the qa-service pattern). This file exists
// as a placeholder for future readiness checks specific to the dating
// surface (e.g. Kafka topic existence, DigiLocker reachability) — none in
// Sprint 1.
