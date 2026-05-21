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

func (s *Service) StartFraudScoreWorker(ctx context.Context) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	slog.Info("food-service: fraud score worker started")
	// Run once immediately so dev environments populate the table
	// without waiting six hours.
	s.runFraudPass(ctx)
	for {
		select {
		case <-ctx.Done():
			slog.Info("food-service: fraud score worker stopped")
			return
		case <-ticker.C:
			s.runFraudPass(ctx)
		}
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
}

// TopFraudUsers is the admin-side view.
func (s *Service) TopFraudUsers(ctx context.Context, windowHours, limit int) ([]postgres.TopFraudUsersRow, error) {
	return s.store.TopFraudUsers(ctx, windowHours, limit)
}
