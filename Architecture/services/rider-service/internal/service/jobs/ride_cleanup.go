// Stale ride sweeper. Three cohorts per spec §15:
//   - 'requested' or 'searching_partner' for >5 min  → expire.
//   - 'partner_assigned' for >15 min without arriving → safety incident
//     (kind='partner_no_show') + customer notification.
//   - 'in_progress' for >2 hours → safety incident
//     (kind='long_idle_in_progress'); admin reviews.
//
// Idempotent: the safety-incident creation is the only side-effect that
// could double-fire. We guard it with a per-ride per-kind check
// ("does an open incident already exist for this ride+kind?") so a
// re-run within minutes is a no-op. Offer-expiry (every 30s) is a single
// SQL UPDATE that's already idempotent by virtue of the WHERE status='sent'
// predicate.
//
// Spec ref: mopedu/MOPEDU_SPEC.md §15 + §12 (safety).
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// RidePublisher is the subset of the event publisher this job needs.
type RidePublisher interface {
	PublishRideExpired(ctx context.Context, rideID uuid.UUID) error
}

// RideTransitioner is the contract for moving a ride between statuses.
// Implemented by *service.Service via TransitionRideForJob (added below).
type RideTransitioner interface {
	TransitionRideForJob(ctx context.Context, rideID uuid.UUID, target string, reason string) error
}

// IncidentChecker is the per-ride existing-incident gate.
type IncidentChecker interface {
	HasOpenIncidentForRideKind(ctx context.Context, rideID uuid.UUID, kind string) (bool, error)
}

// RunStaleRideCleanup walks the three stuck-ride cohorts and applies the
// per-cohort remediation. Returns the total count of rides actioned.
func RunStaleRideCleanup(ctx context.Context, st *store.Store, transitioner RideTransitioner, pub RidePublisher) (int, error) {
	if st == nil {
		return 0, fmt.Errorf("ride cleanup: store required")
	}
	processed := 0

	// Cohort 1: requested / searching_partner > 5 min → expired.
	for _, status := range []string{"requested", "searching_partner"} {
		stuck, err := st.ListStuckRides(ctx, store.RidesStuckFilter{
			Status: status, OlderThan: 5 * time.Minute,
		})
		if err != nil {
			return processed, fmt.Errorf("list stuck %s: %w", status, err)
		}
		for i := range stuck {
			r := &stuck[i]
			if transitioner != nil {
				if err := transitioner.TransitionRideForJob(ctx, r.ID, "expired", "stale_no_match"); err != nil {
					slog.Warn("rider job: expire stale ride transition failed",
						"ride_id", r.ID, "error", err)
					continue
				}
			}
			if pub != nil {
				if perr := pub.PublishRideExpired(ctx, r.ID); perr != nil {
					slog.Warn("rider job: ride.expired publish failed",
						"ride_id", r.ID, "error", perr)
				}
			}
			processed++
		}
	}

	// Cohort 2: partner_assigned > 15 min without partner_arriving → safety
	// incident kind='partner_no_show'. We don't auto-cancel; admin reviews.
	stuckAssigned, err := st.ListStuckRides(ctx, store.RidesStuckFilter{
		Status: "partner_assigned", OlderThan: 15 * time.Minute,
	})
	if err != nil {
		return processed, fmt.Errorf("list stuck partner_assigned: %w", err)
	}
	for i := range stuckAssigned {
		r := &stuckAssigned[i]
		if err := createIncidentIfMissing(ctx, st, r, "partner_no_show", "high",
			map[string]any{"detected_at": time.Now().UTC()}); err != nil {
			slog.Warn("rider job: partner_no_show incident create failed",
				"ride_id", r.ID, "error", err)
			continue
		}
		processed++
	}

	// Cohort 3: in_progress > 2 hours → safety incident
	// kind='long_idle_in_progress'. Don't auto-cancel.
	stuckInProgress, err := st.ListStuckRides(ctx, store.RidesStuckFilter{
		Status: "in_progress", OlderThan: 2 * time.Hour,
	})
	if err != nil {
		return processed, fmt.Errorf("list stuck in_progress: %w", err)
	}
	for i := range stuckInProgress {
		r := &stuckInProgress[i]
		if err := createIncidentIfMissing(ctx, st, r, "long_idle_in_progress", "medium",
			map[string]any{"started_at": r.RequestedAt}); err != nil {
			slog.Warn("rider job: long_idle_in_progress incident create failed",
				"ride_id", r.ID, "error", err)
			continue
		}
		processed++
	}
	return processed, nil
}

// createIncidentIfMissing creates a safety incident for the ride+kind pair
// only if one doesn't already exist in open/acknowledged status. This is
// the per-ride-per-kind dedupe that makes the job idempotent across reruns.
func createIncidentIfMissing(ctx context.Context, st *store.Store, r *store.Ride, kind, severity string, metadata map[string]any) error {
	exists, err := st.HasOpenIncidentForRideKind(ctx, r.ID, kind)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	metaBytes, err := json.Marshal(metadata)
	if err != nil {
		metaBytes = []byte(`{}`)
	}
	in := store.CreateSafetyIncidentInput{
		RideID:   &r.ID,
		Kind:     kind,
		Severity: severity,
		Metadata: metaBytes,
	}
	if r.PartnerID != nil {
		in.PartnerID = r.PartnerID
	}
	customerID := r.CustomerUserID
	in.CustomerID = &customerID
	if _, err := st.CreateSafetyIncident(ctx, in); err != nil {
		return fmt.Errorf("create incident: %w", err)
	}
	return nil
}

// RunOfferExpiry sweeps every offer past its expiry. Lives as a thin
// pass-through to the existing store method so the new cron framework
// can register it alongside the new jobs.
func RunOfferExpiry(ctx context.Context, st *store.Store) (int, error) {
	if st == nil {
		return 0, fmt.Errorf("offer expiry: store required")
	}
	n, err := st.ExpireStaleOffers(ctx)
	if err != nil {
		return 0, err
	}
	return int(n), nil
}
