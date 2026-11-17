package main

import (
	"log/slog"

	"github.com/atpost/chat-message-service/internal/http"
	"github.com/atpost/chat-shared/health"
)

// newReadiness is what GET /readyz checks: the three stores this process
// refuses to boot without. Conversations live in Postgres, messages in
// ScyllaDB, and Redis carries delivery to ws-gateway, idempotency and rate
// limits.
//
// Left out on purpose:
//   - Kafka: events wait in the Postgres outbox through a broker outage, so
//     sends and reads still work; boot does not dial it either.
//   - peer HTTP services and KMS: each backs one feature or is only needed
//     at boot.
func newReadiness(log *slog.Logger, postgres, scylla, redis health.CheckFunc) *health.Checker {
	c := health.New(http.ServiceName, log)
	c.Register("postgres", postgres)
	c.Register("scylla", scylla)
	c.Register("redis", redis)
	return c
}
