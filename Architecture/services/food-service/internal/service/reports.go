package service

import (
	"context"

	"github.com/atpost/food-service/internal/store/postgres"
)

// ReportRestaurantSLA + friends are thin pass-throughs to the store
// layer. We keep them on the service so future enrichment (caching,
// telemetry decoration, denormalised joins) has one obvious home.
func (s *Service) ReportRestaurantSLA(ctx context.Context, w postgres.ReportWindow) ([]postgres.RestaurantSLAReport, error) {
	return s.store.ReportRestaurantSLA(ctx, w)
}

func (s *Service) ReportDeliverySLA(ctx context.Context, w postgres.ReportWindow) ([]postgres.DeliverySLAReport, error) {
	return s.store.ReportDeliverySLA(ctx, w)
}

func (s *Service) ReportPaymentRecon(ctx context.Context, w postgres.ReportWindow) ([]postgres.PaymentReconRow, error) {
	return s.store.ReportPaymentRecon(ctx, w)
}

func (s *Service) ReportRefundsCancellations(ctx context.Context, w postgres.ReportWindow) ([]postgres.RefundCancelRow, error) {
	return s.store.ReportRefundsCancellations(ctx, w)
}

func (s *Service) ReportCouponAbuse(ctx context.Context, w postgres.ReportWindow, threshold int) ([]postgres.CouponAbuseRow, error) {
	return s.store.ReportCouponAbuse(ctx, w, threshold)
}

func (s *Service) ReportCompliance(ctx context.Context) ([]postgres.ComplianceReportRow, error) {
	return s.store.ReportCompliance(ctx)
}
