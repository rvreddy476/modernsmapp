package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Creator Tax Profile
// ---------------------------------------------------------------------------

// SaveCreatorTaxProfile saves or updates a creator's tax compliance profile.
func (s *Service) SaveCreatorTaxProfile(ctx context.Context, userID uuid.UUID, pan *string, gstin *string, residency string) error {
	if residency == "" {
		residency = "IN"
	}
	p := &postgres.CreatorTaxProfile{
		UserID:       userID,
		PANEncrypted: pan,
		GSTIN:        gstin,
		TaxResidency: residency,
	}
	return s.store.SaveCreatorTaxProfile(ctx, p)
}

// GetCreatorTaxProfile returns a creator's tax profile.
func (s *Service) GetCreatorTaxProfile(ctx context.Context, userID uuid.UUID) (*postgres.CreatorTaxProfile, error) {
	return s.store.GetCreatorTaxProfile(ctx, userID)
}

// ---------------------------------------------------------------------------
// TDS (Tax Deducted at Source)
// ---------------------------------------------------------------------------

// tdsThresholdPaise is the yearly earnings threshold above which TDS is deducted.
// Rs 30,000 = 3,000,000 paise.
const tdsThresholdPaise int64 = 3_000_000

// tdsRateBPS is the TDS rate in basis points (10% = 1000 bps).
const tdsRateBPS int64 = 1000

// DeductTDS calculates and records TDS for a creator on a gross amount.
// TDS is deducted at 10% only if the creator's yearly earnings exceed Rs 30,000 (3,000,000 paise).
// Returns (netPaise, tdsPaise).
func (s *Service) DeductTDS(ctx context.Context, creatorID uuid.UUID, grossAmountPaise int64) (int64, int64, error) {
	if grossAmountPaise <= 0 {
		return 0, 0, fmt.Errorf("gross amount must be positive: %w", ErrInvalidAmount)
	}

	fy := GetFinancialYear()

	// Check if creator is TDS exempt
	profile, err := s.store.GetCreatorTaxProfile(ctx, creatorID)
	if err != nil {
		return 0, 0, fmt.Errorf("get tax profile: %w", err)
	}
	if profile != nil && profile.TDSExempt {
		return grossAmountPaise, 0, nil
	}

	// Get yearly TDS total to check threshold
	yearlyTotal, err := s.store.GetYearlyTDSTotal(ctx, creatorID, fy)
	if err != nil {
		return 0, 0, fmt.Errorf("get yearly TDS total: %w", err)
	}

	// Only deduct TDS if yearly earnings exceed threshold
	if yearlyTotal+grossAmountPaise <= tdsThresholdPaise {
		return grossAmountPaise, 0, nil
	}

	// Calculate TDS: 10% of gross amount
	tdsPaise := grossAmountPaise * tdsRateBPS / 10000
	netPaise := grossAmountPaise - tdsPaise

	// Record TDS entry
	entry := &postgres.TDSEntry{
		CreatorID:        creatorID,
		FinancialYear:    fy,
		GrossAmountPaise: grossAmountPaise,
		TDSAmountPaise:   tdsPaise,
		Section:          "194-O",
	}
	if err := s.store.InsertTDSEntry(ctx, entry); err != nil {
		return 0, 0, fmt.Errorf("insert TDS entry: %w", err)
	}

	return netPaise, tdsPaise, nil
}

// GetTDSSummary returns all TDS entries for a creator in the given financial year.
func (s *Service) GetTDSSummary(ctx context.Context, creatorID uuid.UUID, financialYear string) ([]postgres.TDSEntry, int64, error) {
	entries, err := s.store.GetTDSByCreatorAndYear(ctx, creatorID, financialYear)
	if err != nil {
		return nil, 0, err
	}

	total, err := s.store.GetYearlyTDSTotal(ctx, creatorID, financialYear)
	if err != nil {
		return nil, 0, err
	}

	return entries, total, nil
}

// ---------------------------------------------------------------------------
// GST
// ---------------------------------------------------------------------------

// gstRateBPS is the default GST rate: 18% = 1800 basis points.
const gstRateBPS = 1800

// CalculateGST computes GST components for an amount.
// Returns (cgstPaise, sgstPaise, igstPaise). Currently assumes intra-state (CGST + SGST split).
func (s *Service) CalculateGST(ctx context.Context, amountPaise int64) (int64, int64, int64) {
	// Total GST = 18% of amount
	totalGSTPaise := amountPaise * int64(gstRateBPS) / 10000

	// Intra-state: split equally between CGST and SGST
	cgstPaise := totalGSTPaise / 2
	sgstPaise := totalGSTPaise - cgstPaise // handle rounding
	igstPaise := int64(0)

	return cgstPaise, sgstPaise, igstPaise
}

// ---------------------------------------------------------------------------
// Financial Year
// ---------------------------------------------------------------------------

// GetFinancialYear returns the current Indian financial year string (e.g., "2025-26").
// The Indian FY runs from April 1 to March 31.
func GetFinancialYear() string {
	now := time.Now()
	year := now.Year()
	month := now.Month()

	if month < time.April {
		// Jan-Mar belongs to previous FY
		return fmt.Sprintf("%d-%02d", year-1, year%100)
	}
	return fmt.Sprintf("%d-%02d", year, (year+1)%100)
}

// ---------------------------------------------------------------------------
// Invoices
// ---------------------------------------------------------------------------

// ListInvoices returns paginated invoices for a user.
func (s *Service) ListInvoices(ctx context.Context, userID uuid.UUID, limit, offset int) ([]postgres.Invoice, error) {
	return s.store.ListInvoices(ctx, userID, limit, offset)
}
