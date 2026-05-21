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
	for {
		select {
		case <-ctx.Done():
			slog.Info("food-service: SLA auto-reject worker stopped")
			return
		case <-ticker.C:
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
