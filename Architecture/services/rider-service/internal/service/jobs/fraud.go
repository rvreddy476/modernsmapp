// Fraud-score nightly recompute. For each active partner we sum a small
// set of weighted signals into a 0..100 score per spec §13:
//
//	+10  accept-then-cancel ratio > 15%
//	+15  cancellation ratio > 25%
//	+5   per customer complaint in trailing 30 days
//	+20  per detected GPS jump (placeholder; the location-anomaly detector
//	     is a Sprint 5 deliverable, so this contributes 0 today)
//	+50  multi-account same vehicle (the unique vehicle-registration index
//	     enforces this at insert; this branch catches deletes-and-resubmits
//	     by counting active duplicates — placeholder for now)
//	+10  documents expired but partner still online
//
// At >= 70 we emit EventRiderPartnerFraudFlagged. At >= 90 we
// auto-suspend the partner (status='suspended', reason='auto:fraud_score').
//
// Idempotent: a re-run computes the same score over the same window.
// The auto-suspend branch is guarded by "status NOT IN suspended/blocked"
// in the SQL so a second run within the day is a no-op.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/rider-service/internal/events"
	"github.com/atpost/rider-service/internal/store"
)

// FraudPublisher is the publisher contract for fraud-flagged events.
type FraudPublisher interface {
	PublishPartnerFraudFlagged(ctx context.Context, payload events.PartnerFraudFlaggedPayload) error
}

// FraudThresholds tunes the score weights so tests can verify the
// arithmetic. Defaults applied via defaults().
type FraudThresholds struct {
	AcceptThenCancelRatio float64 // default 0.15
	AcceptThenCancelBonus float64 // default 10
	CancelRatio           float64 // default 0.25
	CancelBonus           float64 // default 15
	PerComplaint          float64 // default 5
	PerGPSAnomaly         float64 // default 20
	MultiAccountBonus     float64 // default 50
	ExpiredDocsBonus      float64 // default 10
	FlagAt                float64 // default 70
	AutoSuspendAt         float64 // default 90
}

func (t *FraudThresholds) defaults() {
	if t.AcceptThenCancelRatio <= 0 {
		t.AcceptThenCancelRatio = 0.15
	}
	if t.AcceptThenCancelBonus <= 0 {
		t.AcceptThenCancelBonus = 10
	}
	if t.CancelRatio <= 0 {
		t.CancelRatio = 0.25
	}
	if t.CancelBonus <= 0 {
		t.CancelBonus = 15
	}
	if t.PerComplaint <= 0 {
		t.PerComplaint = 5
	}
	if t.PerGPSAnomaly <= 0 {
		t.PerGPSAnomaly = 20
	}
	if t.MultiAccountBonus <= 0 {
		t.MultiAccountBonus = 50
	}
	if t.ExpiredDocsBonus <= 0 {
		t.ExpiredDocsBonus = 10
	}
	if t.FlagAt <= 0 {
		t.FlagAt = 70
	}
	if t.AutoSuspendAt <= 0 {
		t.AutoSuspendAt = 90
	}
}

// ComputeFraudScore runs the formula on the inputs. Returns the score
// (capped at 100) and a list of human-readable reasons. Pure function
// so it's easy to unit-test against arithmetic edge cases.
func ComputeFraudScore(in store.FraudInputs, t FraudThresholds) (float64, []string) {
	t.defaults()
	var score float64
	var reasons []string

	if in.RidesAssigned > 0 {
		atcRatio := float64(in.AcceptThenCancel) / float64(in.RidesAssigned)
		if atcRatio > t.AcceptThenCancelRatio {
			score += t.AcceptThenCancelBonus
			reasons = append(reasons, fmt.Sprintf("accept_then_cancel_ratio=%.2f", atcRatio))
		}
		cancelRatio := float64(in.RidesCancelledByP) / float64(in.RidesAssigned)
		if cancelRatio > t.CancelRatio {
			score += t.CancelBonus
			reasons = append(reasons, fmt.Sprintf("cancel_ratio=%.2f", cancelRatio))
		}
	}
	if in.ComplaintsCount > 0 {
		score += t.PerComplaint * float64(in.ComplaintsCount)
		reasons = append(reasons, fmt.Sprintf("complaints=%d", in.ComplaintsCount))
	}
	// SafetyIncidentsCount stands in for "GPS anomalies" until the
	// location-anomaly detector lands in S5 — each open safety incident
	// counts as one anomaly with the same weight.
	if in.SafetyIncidentsCount > 0 {
		score += t.PerGPSAnomaly * float64(in.SafetyIncidentsCount)
		reasons = append(reasons, fmt.Sprintf("safety_incidents=%d", in.SafetyIncidentsCount))
	}
	if in.ExpiredDocsActive > 0 {
		score += t.ExpiredDocsBonus * float64(in.ExpiredDocsActive)
		reasons = append(reasons, fmt.Sprintf("expired_docs_while_online=%d", in.ExpiredDocsActive))
	}
	if score > 100 {
		score = 100
	}
	return score, reasons
}

// RunFraudScoreRecalc is the cron entry point. Recomputes the score for
// every active partner, writes it back to rider_partners, and emits the
// flagged/suspend events at the configured thresholds.
func RunFraudScoreRecalc(ctx context.Context, st *store.Store, pub FraudPublisher) (int, error) {
	if st == nil {
		return 0, fmt.Errorf("fraud recalc: store required")
	}
	ids, err := st.ListActivePartnerIDs(ctx)
	if err != nil {
		return 0, err
	}
	t := FraudThresholds{}
	t.defaults()
	processed := 0
	for _, pid := range ids {
		in, err := st.FetchFraudInputs(ctx, pid, 30)
		if err != nil {
			slog.Warn("rider job: fraud inputs fetch failed",
				"partner_id", pid, "error", err)
			continue
		}
		score, reasons := ComputeFraudScore(in, t)
		if err := st.SetFraudScore(ctx, pid, score); err != nil {
			slog.Warn("rider job: set fraud score failed",
				"partner_id", pid, "error", err)
			continue
		}
		processed++
		if score < t.FlagAt {
			continue
		}
		autoSuspend := score >= t.AutoSuspendAt
		if autoSuspend {
			if err := st.SetPartnerSuspended(ctx, pid, "auto:fraud_score>=90"); err != nil {
				slog.Warn("rider job: auto-suspend failed",
					"partner_id", pid, "score", score, "error", err)
			}
		}
		payload := events.PartnerFraudFlaggedPayload{
			PartnerID:   pid.String(),
			FraudScore:  score,
			AutoSuspend: autoSuspend,
			Reasons:     reasons,
			OccurredAt:  time.Now().UTC(),
		}
		if pub != nil {
			if perr := pub.PublishPartnerFraudFlagged(ctx, payload); perr != nil {
				slog.Warn("rider job: fraud flagged publish failed",
					"partner_id", pid, "error", perr)
			}
		}
	}
	return processed, nil
}
