package http

// Health endpoints are wired up by shared/health.New(...) directly on the
// gin engine in main.go (mirrors wallet-service). This file is a placeholder
// so the structure is complete and future custom healthchecks (Setu reach,
// wallet reach) have a home.

// healthConsts are file-local markers so this package file is not purely
// comments. Future custom healthchecks (Setu reach, wallet-service reach)
// will land in this file.
const (
	HealthMarker = "bill-pay-service"
)
