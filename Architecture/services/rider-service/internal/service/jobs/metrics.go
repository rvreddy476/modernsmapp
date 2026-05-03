// Partner metrics nightly recompute. For every active partner, we
// compute trailing-30d acceptance / completion / cancellation rates from
// rider_ride_offers + rider_rides and write the percentages back onto
// rider_partners.
//
// Idempotent: writes are direct UPDATEs over fresh aggregates, so a
// re-run produces the same row state. metrics_recalc_at = NOW() lets ops
// see when each partner was last refreshed.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §15.
package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/atpost/rider-service/internal/store"
)

// RunPartnerMetricsRecalc walks every active partner and recomputes the
// trailing-30d performance metrics. Returns the count of partners
// processed.
func RunPartnerMetricsRecalc(ctx context.Context, st *store.Store) (int, error) {
	if st == nil {
		return 0, fmt.Errorf("partner metrics: store required")
	}
	ids, err := st.ListActivePartnerIDs(ctx)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, pid := range ids {
		m, err := st.ComputePartnerMetrics(ctx, pid, 30)
		if err != nil {
			slog.Warn("rider job: compute partner metrics failed",
				"partner_id", pid, "error", err)
			continue
		}
		if err := st.UpdatePartnerMetrics(ctx, pid, m); err != nil {
			slog.Warn("rider job: update partner metrics failed",
				"partner_id", pid, "error", err)
			continue
		}
		processed++
	}
	return processed, nil
}
