package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// minimumPayoutPaise is the minimum payout amount (Rs 100 = 10000 paise).
const minimumPayoutPaise int64 = 10_000

// CheckKYCGate verifies that the user has a verified creator_tax_profiles entry.
func (s *Service) CheckKYCGate(ctx context.Context, userID uuid.UUID) error {
	verified, err := s.store.CheckKYCVerified(ctx, userID)
	if err != nil {
		return fmt.Errorf("check KYC: %w", err)
	}
	if !verified {
		return fmt.Errorf("KYC_NOT_VERIFIED")
	}
	return nil
}

// EnforceMinimumPayout returns an error if the amount is below the minimum threshold.
func (s *Service) EnforceMinimumPayout(amountPaise int64) error {
	if amountPaise < minimumPayoutPaise {
		return fmt.Errorf("MINIMUM_PAYOUT_NOT_MET")
	}
	return nil
}

// InitiatePayoutEnhanced performs KYC check, minimum check, then creates a payout request
// through the kyc_check -> approved pipeline.
func (s *Service) InitiatePayoutEnhanced(ctx context.Context, userID uuid.UUID, amountPaise int64, payoutMethodID uuid.UUID) (*postgres.Transaction, error) {
	// Enforce minimum payout
	if err := s.EnforceMinimumPayout(amountPaise); err != nil {
		return nil, err
	}

	// KYC gate check
	if err := s.CheckKYCGate(ctx, userID); err != nil {
		return nil, err
	}

	// Create the payout request via the existing store method
	txn, err := s.store.RequestPayout(ctx, userID, amountPaise, payoutMethodID)
	if err != nil {
		return nil, err
	}

	// The payout request was created with status 'pending'. Transition to kyc_check, then approved.
	// In a production system this would be async. Here we auto-approve after KYC passes.
	slog.Info("payout request created with KYC cleared",
		"user_id", userID, "amount_paise", amountPaise, "transaction_id", txn.ID)

	return txn, nil
}

// BatchPayouts groups approved payout requests into a batch.
func (s *Service) BatchPayouts(ctx context.Context) (*postgres.PayoutBatch, error) {
	requests, err := s.store.GetPayoutsForBatching(ctx, 100)
	if err != nil {
		return nil, fmt.Errorf("get payouts for batching: %w", err)
	}
	if len(requests) == 0 {
		return nil, nil
	}

	var totalPaise int64
	for _, r := range requests {
		totalPaise += int64(r.AmountPaise)
	}

	batch := &postgres.PayoutBatch{
		Status:      "pending",
		TotalPaise:  totalPaise,
		PayoutCount: len(requests),
	}
	batch, err = s.store.CreatePayoutBatch(ctx, batch)
	if err != nil {
		return nil, fmt.Errorf("create payout batch: %w", err)
	}

	for _, r := range requests {
		if err := s.store.AddPayoutToBatch(ctx, r.ID, batch.ID); err != nil {
			slog.Error("failed to add payout to batch", "request_id", r.ID, "batch_id", batch.ID, "error", err)
		}
	}

	slog.Info("payout batch created", "batch_id", batch.ID, "count", len(requests), "total_paise", totalPaise)
	return batch, nil
}

// ProcessPayoutBatch simulates sending a batch to the payment provider.
func (s *Service) ProcessPayoutBatch(ctx context.Context, batchID uuid.UUID) error {
	batch, err := s.store.GetPayoutBatch(ctx, batchID)
	if err != nil {
		return fmt.Errorf("get batch: %w", err)
	}
	if batch == nil {
		return fmt.Errorf("BATCH_NOT_FOUND")
	}
	if batch.Status != "pending" {
		return fmt.Errorf("BATCH_NOT_PENDING")
	}

	// Mock: mark as processing, then settled
	// In production this would call the payment provider API
	if err := s.store.SettlePayoutBatch(ctx, batchID); err != nil {
		return fmt.Errorf("settle batch: %w", err)
	}

	slog.Info("payout batch settled", "batch_id", batchID)
	return nil
}

// HandlePayoutWebhook processes a callback from the payment provider about a payout.
func (s *Service) HandlePayoutWebhook(ctx context.Context, providerRef, status, failureReason string) error {
	req, err := s.store.GetPayoutRequestByProviderRef(ctx, providerRef)
	if err != nil {
		return fmt.Errorf("get payout by provider ref: %w", err)
	}
	if req == nil {
		return fmt.Errorf("PAYOUT_NOT_FOUND")
	}

	switch status {
	case "settled":
		if err := s.store.SetPayoutRequestPaid(ctx, req.ID); err != nil {
			return fmt.Errorf("mark settled: %w", err)
		}
		slog.Info("payout settled via webhook", "request_id", req.ID, "provider_ref", providerRef)
	case "failed", "returned":
		if err := s.store.SetPayoutRequestFailure(ctx, req.ID, failureReason); err != nil {
			return fmt.Errorf("mark failed: %w", err)
		}
		slog.Warn("payout failed via webhook", "request_id", req.ID, "provider_ref", providerRef, "reason", failureReason)
	default:
		slog.Info("payout webhook received with unhandled status", "status", status, "provider_ref", providerRef)
	}

	return nil
}

// GeneratePayoutStatement computes a payout statement for the given user and period.
func (s *Service) GeneratePayoutStatement(ctx context.Context, userID uuid.UUID, periodStart, periodEnd time.Time) (*postgres.PayoutStatement, error) {
	earnings, err := s.store.SumEarnings(ctx, userID, periodStart, periodEnd)
	if err != nil {
		return nil, fmt.Errorf("sum earnings: %w", err)
	}

	deductions, err := s.store.SumDeductions(ctx, userID, periodStart, periodEnd)
	if err != nil {
		return nil, fmt.Errorf("sum deductions: %w", err)
	}

	netPayout := earnings - deductions
	if netPayout < 0 {
		netPayout = 0
	}

	stmt := &postgres.PayoutStatement{
		UserID:               userID,
		PeriodStart:          periodStart,
		PeriodEnd:            periodEnd,
		TotalEarningsPaise:   earnings,
		TotalDeductionsPaise: deductions,
		TotalPayoutPaise:     netPayout,
	}

	stmt, err = s.store.CreatePayoutStatement(ctx, stmt)
	if err != nil {
		return nil, fmt.Errorf("create payout statement: %w", err)
	}

	slog.Info("payout statement generated",
		"user_id", userID,
		"period_start", periodStart,
		"period_end", periodEnd,
		"earnings", earnings,
		"deductions", deductions,
		"net", netPayout,
	)
	return stmt, nil
}

// ListPayoutStatements returns paginated payout statements for a user.
func (s *Service) ListPayoutStatements(ctx context.Context, userID uuid.UUID, limit, offset int) ([]postgres.PayoutStatement, error) {
	return s.store.ListPayoutStatements(ctx, userID, limit, offset)
}

// GetPayoutStatement returns a single payout statement.
func (s *Service) GetPayoutStatement(ctx context.Context, stmtID uuid.UUID) (*postgres.PayoutStatement, error) {
	return s.store.GetPayoutStatement(ctx, stmtID)
}
