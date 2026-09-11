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

// TDSThresholdPaise is the yearly GROSS above which TDS is deducted.
// Rs 30,000 = 3,000,000 paise.
const TDSThresholdPaise int64 = 3_000_000

// tdsRateBPS is the TDS rate in basis points (10% = 1000 bps).
const tdsRateBPS int64 = 1000

// DefaultTDSSection is the section a TDS entry is recorded under when
// MONETIZATION_TDS_SECTION is unset. Flagged for tax counsel in the plan:
// 194-O applicability to a creator fund versus 194J for tips and
// subscriptions is not a question this code answers, which is why the
// value is configuration.
const DefaultTDSSection = "194-O"

// ComputeTDS is the pure rule: the paise to withhold from grossPaise
// given what the creator has already been paid this financial year.
// Nothing is withheld until cumulative gross — INCLUDING this payout —
// exceeds the threshold; the payout that crosses it is taxed in full.
func ComputeTDS(grossPaise, yearlyGrossSoFarPaise int64) int64 {
	if grossPaise <= 0 {
		return 0
	}
	if yearlyGrossSoFarPaise+grossPaise <= TDSThresholdPaise {
		return 0
	}
	return grossPaise * tdsRateBPS / 10000
}

// DeductTDS prices and records TDS for a creator on a gross amount, in
// its own transaction. Returns (netPaise, tdsPaise). The withdrawal path
// uses deductTDSTx under its own transaction instead.
func (s *Service) DeductTDS(ctx context.Context, creatorID uuid.UUID, grossAmountPaise int64) (int64, int64, error) {
	var net, tds int64
	err := s.store.WithTx(ctx, func(tx pgxTx) error {
		n, t, err := s.deductTDSTx(ctx, tx, creatorID, grossAmountPaise, nil)
		net, tds = n, t
		return err
	})
	if err != nil {
		return 0, 0, err
	}
	return net, tds, nil
}

// deductTDSTx computes TDS on the caller's transaction and writes the
// tds_ledger row that records it — one row per payout, withheld or not,
// because the threshold is on cumulative gross and the ledger is where
// that gross is summed from. referenceID is the payout request the row
// belongs to, nil when called outside the withdrawal path.
func (s *Service) deductTDSTx(ctx context.Context, db postgres.DBTX, creatorID uuid.UUID, grossAmountPaise int64, referenceID *uuid.UUID) (int64, int64, error) {
	if grossAmountPaise <= 0 {
		return 0, 0, fmt.Errorf("gross amount must be positive: %w", ErrInvalidAmount)
	}

	fy := GetFinancialYear()

	// A creator holding a valid exemption certificate has nothing withheld
	// and nothing tracked against the threshold.
	profile, err := s.store.GetCreatorTaxProfileTx(ctx, db, creatorID)
	if err != nil {
		return 0, 0, fmt.Errorf("get tax profile: %w", err)
	}
	if profile != nil && profile.TDSExempt {
		return grossAmountPaise, 0, nil
	}

	yearlyGross, err := s.store.GetYearlyTDSGrossTotalTx(ctx, db, creatorID, fy)
	if err != nil {
		return 0, 0, fmt.Errorf("get yearly gross total: %w", err)
	}

	tdsPaise := ComputeTDS(grossAmountPaise, yearlyGross)
	netPaise := grossAmountPaise - tdsPaise

	if err := s.store.InsertTDSEntryTx(ctx, db, &postgres.TDSEntry{
		CreatorID:        creatorID,
		FinancialYear:    fy,
		GrossAmountPaise: grossAmountPaise,
		TDSAmountPaise:   tdsPaise,
		Section:          s.TDSSection(),
		ReferenceID:      referenceID,
	}); err != nil {
		return 0, 0, fmt.Errorf("insert TDS entry: %w", err)
	}

	return netPaise, tdsPaise, nil
}

// GetTDSSummary returns a creator's TDS entries for the financial year,
// the total withheld, and the total gross paid out — the figure the
// threshold is measured against.
func (s *Service) GetTDSSummary(ctx context.Context, creatorID uuid.UUID, financialYear string) (entries []postgres.TDSEntry, totalTDSPaise, totalGrossPaise int64, err error) {
	entries, err = s.store.GetTDSByCreatorAndYear(ctx, creatorID, financialYear)
	if err != nil {
		return nil, 0, 0, err
	}
	totalTDSPaise, err = s.store.GetYearlyTDSTotal(ctx, creatorID, financialYear)
	if err != nil {
		return nil, 0, 0, err
	}
	totalGrossPaise, err = s.store.GetYearlyTDSGrossTotal(ctx, creatorID, financialYear)
	if err != nil {
		return nil, 0, 0, err
	}
	return entries, totalTDSPaise, totalGrossPaise, nil
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
