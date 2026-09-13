package service

import (
	"context"
	"log/slog"
	"time"
)

// StartSLAAutoRejectWorker scans for CONFIRMED orders whose
// accept_deadline_at passed and transitions them to
// RESTAURANT_REJECTED. Designed to run as a long-lived goroutine from
// main.go. Cancel the context to stop. Tick interval is 15s; on a
// busy lunch tick we process up to 50 expired orders per pass.
//
// Idempotent: the SKIP LOCKED query in the store guarantees two
// workers can run concurrently without double-rejecting the same
// order.
func (s *Service) StartSLAAutoRejectWorker(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	slog.Info("food-service: SLA auto-reject worker started")
	tick := 0
	for {
		select {
		case <-ctx.Done():
			slog.Info("food-service: SLA auto-reject worker stopped")
			return
		case <-ticker.C:
			// Wave 1 B1: once a minute, pause restaurants whose approved
			// FSSAI licence has expired and announce each one.
			if tick%fssaiExpiryEveryTicks == 0 {
				s.runFSSAIExpiryPass(ctx)
			}
			tick++
			// Wave 1 B3: resubmit system refunds whose first submission failed.
			if resubmitted, err := s.ResubmitPendingSystemRefunds(ctx); err != nil {
				slog.Warn("food-service: system refund resubmission pass failed", "error", err)
			} else if resubmitted > 0 {
				slog.Info("food-service: resubmitted system refunds", "count", resubmitted)
			}
			n, err := s.AutoRejectSLAExpiredOrders(ctx)
			if err != nil {
				slog.Warn("food-service: auto-reject pass failed", "error", err)
				continue
			}
			if n > 0 {
				slog.Info("food-service: auto-rejected SLA-breached orders", "count", n)
			}
		}
	}
}

// fssaiExpiryEveryTicks runs the FSSAI expiry pass on every 4th 15-second
// tick: once a minute, starting with the first tick.
const fssaiExpiryEveryTicks = 4

func (s *Service) runFSSAIExpiryPass(ctx context.Context) {
	n, err := s.PauseExpiredFSSAIRestaurants(ctx)
	if err != nil {
		slog.Warn("food-service: FSSAI expiry pass failed", "error", err)
		return
	}
	if n > 0 {
		slog.Info("food-service: paused restaurants with expired FSSAI licences", "count", n)
	}
}
