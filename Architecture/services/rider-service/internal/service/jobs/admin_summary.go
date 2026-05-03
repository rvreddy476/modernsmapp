// Daily admin queue summary. Sums the pending counters (KYC / vehicle /
// payment / complaints / safety) and emits one
// EventRiderAdminQueueSummary so notification-service can email/push the
// ops cohort. Idempotent: a re-run computes the same counters and emits
// a fresh event — downstream notification de-dupes by (event-type,
// occurred_date) if it doesn't want a second copy.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §15.
package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/rider-service/internal/events"
	"github.com/atpost/rider-service/internal/store"
)

// AdminSummaryPublisher is the publisher contract for the queue-summary event.
type AdminSummaryPublisher interface {
	PublishAdminQueueSummary(ctx context.Context, payload events.AdminQueueSummaryPayload) error
}

// RunAdminQueueSummary computes the pending-queue counts and emits one
// queue-summary event. Returns 1 when the event was published, 0 when
// the publisher is nil (testing path).
func RunAdminQueueSummary(ctx context.Context, st *store.Store, pub AdminSummaryPublisher) (int, error) {
	if st == nil {
		return 0, fmt.Errorf("admin queue summary: store required")
	}
	counts, err := st.FetchAdminQueueCounts(ctx)
	if err != nil {
		return 0, err
	}
	if pub == nil {
		return 0, nil
	}
	payload := events.AdminQueueSummaryPayload{
		PendingKYCCount:          counts.PendingKYCCount,
		PendingVehicleCount:      counts.PendingVehicleCount,
		PendingPaymentCount:      counts.PendingPaymentCount,
		OpenComplaintsCount:      counts.OpenComplaintsCount,
		OpenSafetyIncidentsCount: counts.OpenSafetyIncidentsCount,
		OccurredAt:               time.Now().UTC(),
	}
	if err := pub.PublishAdminQueueSummary(ctx, payload); err != nil {
		return 0, err
	}
	return 1, nil
}
