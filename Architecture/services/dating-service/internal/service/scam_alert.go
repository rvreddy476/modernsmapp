// Scam alerts (mechanic M17, DATING_SCAM_ALERT_ENABLED).
//
// An admin suspending someone on a report filed as "scam" warns everyone
// who matched with them in the last 90 days — by the person's first name,
// with a reminder never to send money — through notification-service
// (dating.safety.scam_alert, a safety notice that is always pushed). The
// reporter is left out: their report's status update tells them.
//
// The warnings go through the dating_scam_alerts outbox: queued with the
// suspension, sent at once where possible and retried by the sweeper.
package service

import (
	"context"
	"log/slog"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// scamAlertBatch is how many warnings one send pass handles.
const scamAlertBatch = 200

// queueScamAlerts queues the warnings for a suspension on a scam report and
// tries to send them straight away. Failures are logged: the suspension has
// already happened and the sweeper retries the sending.
func (s *Service) queueScamAlerts(ctx context.Context, subjectID, reporterID uuid.UUID) {
	n, err := s.store.QueueScamAlerts(ctx, subjectID, reporterID)
	if err != nil {
		slog.Error("scam alerts: queueing failed", "subject_id", subjectID, "error", err)
		return
	}
	if n == 0 {
		return
	}
	slog.Info("scam alerts: queued", "subject_id", subjectID, "count", n)
	if _, err := s.SendPendingScamAlerts(ctx); err != nil {
		slog.Warn("scam alerts: first send failed; the sweeper retries", "subject_id", subjectID, "error", err)
	}
}

// SendPendingScamAlerts publishes queued warnings. Without a producer it
// sends nothing and leaves them queued.
func (s *Service) SendPendingScamAlerts(ctx context.Context) (int, error) {
	if !s.mechanics.ScamAlert || s.producer == nil {
		return 0, nil
	}
	return s.store.SendPendingScamAlerts(ctx, scamAlertBatch, func(a store.ScamAlert) error {
		name := ""
		if a.FirstName != nil {
			name = *a.FirstName
		}
		return s.producer.PublishScamAlert(ctx, a.RecipientID, a.MatchID, name)
	})
}
