// fraud_worker.go runs every 6 hours and writes per-user fraud
// signals into food.fraud_scores. Three signals in v1:
//
//   1. refund_abuse — N approved refunds in the trailing 30d.
//      score = min(20, refunds*5).
//   2. coupon_burn — same (user, coupon) used ≥ threshold times in
//      30d. Falls out of ReportCouponAbuse; score = use_count * 2.
//   3. cancellation_pattern — high CANCELLED_BY_CUSTOMER ratio in
//      14d. score = cancellations * 3.
//
// The admin queue reads TopFraudUsers(168h) to triage.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// fraudPassInterval is how often the fraud pass runs.
const fraudPassInterval = 6 * time.Hour

func (s *Service) StartFraudScoreWorker(ctx context.Context) {
	ticker := time.NewTicker(fraudPassInterval)
	defer ticker.Stop()
	slog.Info("food-service: fraud score worker started")
	// Audit 2026-10-30 L6: the boot pass no longer runs on every replica.
	// It runs only when no pass wrote a score within one interval (so a dev
	// environment still populates the table at once), and every pass runs on
	// one replica at a time under an advisory lock.
	s.runFraudBootPass(ctx)
	for {
		select {
		case <-ctx.Done():
			slog.Info("food-service: fraud score worker stopped")
			return
		case <-ticker.C:
			s.runFraudPassExclusive(ctx)
		}
	}
}

func (s *Service) runFraudBootPass(ctx context.Context) {
	if _, err := s.runExclusive(ctx, workerFraud, func(ctx context.Context) error {
		if r, ok := s.store.(fraudPassRecorder); ok {
			last, err := r.LatestFraudScoreAt(ctx)
			if err != nil {
				return err
			}
			if !fraudBootPassDue(last, time.Now(), fraudPassInterval) {
				return nil
			}
		}
		s.runFraudPass(ctx)
		return nil
	}); err != nil {
		slog.Warn("food-service: fraud boot pass skipped", "error", err)
	}
}

func (s *Service) runFraudPassExclusive(ctx context.Context) {
	if _, err := s.runExclusive(ctx, workerFraud, func(ctx context.Context) error {
		s.runFraudPass(ctx)
		return nil
	}); err != nil {
		slog.Warn("food-service: fraud pass skipped", "error", err)
	}
}

func (s *Service) runFraudPass(ctx context.Context) {
	// Refund-abuse signal.
	refunds, err := s.store.RecentRefundsByUser(ctx, 24*30)
	if err != nil {
		slog.Warn("food-service: fraud refund scan failed", "error", err)
	} else {
		for _, r := range refunds {
			uid, err := uuid.Parse(r.UserID)
			if err != nil {
				continue
			}
			score := float64(r.RefundsCount) * 5
			if score > 20 {
				score = 20
			}
			_ = s.store.RecordFraudScore(ctx, uid, "refund_abuse", score, map[string]any{
				"refunds_count": r.RefundsCount,
				"total_amount":  r.TotalRefunded,
			})
		}
	}
	// Coupon-burn signal — reuse the existing ReportCouponAbuse
	// window over the last 30 days with default threshold.
	w := postgres.ReportWindow{From: time.Now().Add(-30 * 24 * time.Hour)}
	abusers, err := s.store.ReportCouponAbuse(ctx, w, 5)
	if err != nil {
		slog.Warn("food-service: fraud coupon scan failed", "error", err)
	} else {
		for _, a := range abusers {
			uid, err := uuid.Parse(a.CustomerID)
			if err != nil {
				continue
			}
			score := float64(a.UseCount) * 2
			_ = s.store.RecordFraudScore(ctx, uid, "coupon_burn", score, map[string]any{
				"coupon_code": a.CouponCode,
				"use_count":   a.UseCount,
			})
		}
	}
	// Cancellation-pattern signal — customers cancelling repeatedly in
	// the trailing 14 days. score = cancellations * 3.
	cancellations, err := s.store.RecentCustomerCancellations(ctx, 24*14)
	if err != nil {
		slog.Warn("food-service: fraud cancellation scan failed", "error", err)
	} else {
		for _, c := range cancellations {
			uid, err := uuid.Parse(c.UserID)
			if err != nil {
				continue
			}
			_ = s.store.RecordFraudScore(ctx, uid, "cancellation_pattern",
				float64(c.CancellationCount)*3, map[string]any{
					"cancellations": c.CancellationCount,
				})
		}
	}
}

// TopFraudUsers is the admin-side view.
func (s *Service) TopFraudUsers(ctx context.Context, windowHours, limit int) ([]postgres.TopFraudUsersRow, error) {
	return s.store.TopFraudUsers(ctx, windowHours, limit)
}
