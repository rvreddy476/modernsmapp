package service

// Admin console reads and the registry presentation write (admin-service).
// Thin on purpose: the confinement by application and the audit row live in
// the store, in the same statement or transaction as the read or write.

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/google/uuid"
)

// AdminGetIntent reads an intent and its refund commands.
func (s *Service) AdminGetIntent(ctx context.Context, id uuid.UUID, applicationID string) (*postgres.AdminIntentDetail, error) {
	return s.store.AdminGetIntent(ctx, id, applicationID)
}

// AdminListIntents pages intents newest first.
func (s *Service) AdminListIntents(ctx context.Context, f postgres.AdminIntentFilter) ([]postgres.AdminIntent, *postgres.RefundCursor, error) {
	return s.store.AdminListIntents(ctx, f)
}

// AdminGetRefund reads one refund command.
func (s *Service) AdminGetRefund(ctx context.Context, id uuid.UUID, applicationID string) (*postgres.AdminRefund, error) {
	return s.store.AdminGetRefund(ctx, id, applicationID)
}

// AdminReconciliation reads what the reconciler and refund worker have flagged.
func (s *Service) AdminReconciliation(ctx context.Context, applicationID string, pendingAge time.Duration, limit int) (*postgres.Reconciliation, error) {
	return s.store.AdminReconciliation(ctx, applicationID, pendingAge, limit)
}

// AdminStats counts the dashboard numbers.
func (s *Service) AdminStats(ctx context.Context, applicationID string, pendingAge time.Duration) (*postgres.AdminStats, error) {
	return s.store.AdminStats(ctx, applicationID, pendingAge)
}

// AdminPaymentAudit pages payment_audit_log.
func (s *Service) AdminPaymentAudit(ctx context.Context, f postgres.PaymentAuditFilter) ([]postgres.PaymentAuditEntry, int64, error) {
	return s.store.AdminPaymentAudit(ctx, f)
}

// AdminApplicationAudit pages application_audit_log.
func (s *Service) AdminApplicationAudit(ctx context.Context, applicationID string, beforeID int64, limit int) ([]postgres.ApplicationAuditEntry, int64, error) {
	return s.store.AdminApplicationAudit(ctx, applicationID, beforeID, limit)
}

// UpdateApplicationPresentation changes an application's names or methods,
// audited in the same transaction.
func (s *Service) UpdateApplicationPresentation(ctx context.Context, in postgres.ApplicationPresentationInput) (*postgres.ApplicationWrite, error) {
	res, err := s.store.UpdateApplicationPresentation(ctx, in)
	if err != nil {
		return nil, err
	}
	if res.Changed {
		slog.Warn("payments: application registry changed from the admin console",
			"application_id", in.Key, "operator_id", in.OperatorID, "credential", in.Credential)
	}
	return res, nil
}
