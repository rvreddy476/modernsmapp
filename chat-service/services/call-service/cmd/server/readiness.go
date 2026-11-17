package main

import (
	"log/slog"

	callhttp "github.com/atpost/chat-call-service/internal/http"
	"github.com/atpost/chat-shared/health"
)

// newReadiness is what GET /readyz checks: the two connections this process
// refuses to boot without, and that every call route needs.
//
// Left out on purpose:
//   - Kafka: lifecycle events wait in the Postgres outbox through a broker
//     outage, so calls still work; boot does not dial it either.
//   - LiveKit, TURN and chat_db (call mute): optional at boot.
func newReadiness(log *slog.Logger, postgres, redis health.CheckFunc) *health.Checker {
	c := health.New(callhttp.ServiceName, log)
	c.Register("postgres", postgres)
	c.Register("redis", redis)
	return c
}
