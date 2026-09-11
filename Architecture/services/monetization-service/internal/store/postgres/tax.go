package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Models
// ---------------------------------------------------------------------------

// CreatorTaxProfile represents a creator's tax compliance profile.
type CreatorTaxProfile struct {
	UserID       uuid.UUID  `json:"user_id"`
	PANEncrypted *string    `json:"pan_encrypted,omitempty"`
	GSTIN        *string    `json:"gstin,omitempty"`
	TaxResidency string     `json:"tax_residency"`
	TDSExempt    bool       `json:"tds_exempt"`
	VerifiedAt   *time.Time `json:"verified_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// TDSEntry represents a TDS deduction record.
type TDSEntry struct {
	ID               uuid.UUID  `json:"id"`
	CreatorID        uuid.UUID  `json:"creator_id"`
	FinancialYear    string     `json:"financial_year"`
	GrossAmountPaise int64      `json:"gross_amount_paise"`
	TDSAmountPaise   int64      `json:"tds_amount_paise"`
	Section          string     `json:"section"`
	ReferenceID      *uuid.UUID `json:"reference_id,omitempty"`
	DeductedAt       time.Time  `json:"deducted_at"`
}

// GSTEntry represents a GST ledger record.
type GSTEntry struct {
	ID                 uuid.UUID  `json:"id"`
	TransactionID      *uuid.UUID `json:"transaction_id,omitempty"`
	TaxableAmountPaise int64      `json:"taxable_amount_paise"`
	GSTRateBPS         int        `json:"gst_rate_bps"`
	CGSTPaise          int64      `json:"cgst_paise"`
	SGSTPaise          int64      `json:"sgst_paise"`
	IGSTPaise          int64      `json:"igst_paise"`
	CreatedAt          time.Time  `json:"created_at"`
}

// Invoice represents a generated invoice.
type Invoice struct {
	ID            uuid.UUID  `json:"id"`
	UserID        uuid.UUID  `json:"user_id"`
	InvoiceNumber int        `json:"invoice_number"`
	InvoiceType   string     `json:"invoice_type"`
	AmountPaise   int64      `json:"amount_paise"`
	TaxPaise      int64      `json:"tax_paise"`
	TotalPaise    int64      `json:"total_paise"`
	PDFMediaID    *uuid.UUID `json:"pdf_media_id,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// ---------------------------------------------------------------------------
// Creator Tax Profile
// ---------------------------------------------------------------------------

// SaveCreatorTaxProfile upserts a creator's tax profile.
func (s *Store) SaveCreatorTaxProfile(ctx context.Context, p *CreatorTaxProfile) error {
	now := time.Now()
	p.UpdatedAt = now
	_, err := s.db.Exec(ctx, `
		INSERT INTO creator_tax_profiles (user_id, pan_encrypted, gstin, tax_residency, tds_exempt, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $6)
		ON CONFLICT (user_id) DO UPDATE SET
			pan_encrypted = EXCLUDED.pan_encrypted,
			gstin = EXCLUDED.gstin,
			tax_residency = EXCLUDED.tax_residency,
			tds_exempt = EXCLUDED.tds_exempt,
			updated_at = EXCLUDED.updated_at
	`, p.UserID, p.PANEncrypted, p.GSTIN, p.TaxResidency, p.TDSExempt, now)
	return err
}

// GetCreatorTaxProfile returns a creator's tax profile, or nil if not found.
func (s *Store) GetCreatorTaxProfile(ctx context.Context, userID uuid.UUID) (*CreatorTaxProfile, error) {
	return s.GetCreatorTaxProfileTx(ctx, s.db, userID)
}

// GetCreatorTaxProfileTx is GetCreatorTaxProfile on the caller's
// transaction, so the TDS exemption a withdrawal reads is the one that
// commits with it.
func (s *Store) GetCreatorTaxProfileTx(ctx context.Context, db DBTX, userID uuid.UUID) (*CreatorTaxProfile, error) {
	var p CreatorTaxProfile
	err := db.QueryRow(ctx, `
		SELECT user_id, pan_encrypted, gstin, tax_residency, tds_exempt, verified_at, created_at, updated_at
		FROM creator_tax_profiles
		WHERE user_id = $1
	`, userID).Scan(
		&p.UserID, &p.PANEncrypted, &p.GSTIN, &p.TaxResidency, &p.TDSExempt,
		&p.VerifiedAt, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// ---------------------------------------------------------------------------
// TDS Ledger
// ---------------------------------------------------------------------------

// InsertTDSEntry inserts a TDS ledger record.
func (s *Store) InsertTDSEntry(ctx context.Context, e *TDSEntry) error {
	return s.InsertTDSEntryTx(ctx, s.db, e)
}

// InsertTDSEntryTx inserts a TDS ledger record on the caller's
// transaction. One row is written per payout whether or not anything was
// withheld: the yearly threshold is on cumulative GROSS, so the gross of
// a zero-TDS payout has to be on the ledger for the threshold to ever be
// crossed.
func (s *Store) InsertTDSEntryTx(ctx context.Context, db DBTX, e *TDSEntry) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	e.DeductedAt = time.Now()
	_, err := db.Exec(ctx, `
		INSERT INTO tds_ledger (id, creator_id, financial_year, gross_amount_paise, tds_amount_paise, section, reference_id, deducted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, e.ID, e.CreatorID, e.FinancialYear, e.GrossAmountPaise, e.TDSAmountPaise,
		e.Section, e.ReferenceID, e.DeductedAt)
	return err
}

// GetTDSByCreatorAndYear returns all TDS entries for a creator in a given financial year.
func (s *Store) GetTDSByCreatorAndYear(ctx context.Context, creatorID uuid.UUID, financialYear string) ([]TDSEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, creator_id, financial_year, gross_amount_paise, tds_amount_paise, section, reference_id, deducted_at
		FROM tds_ledger
		WHERE creator_id = $1 AND financial_year = $2
		ORDER BY deducted_at DESC
	`, creatorID, financialYear)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []TDSEntry
	for rows.Next() {
		var e TDSEntry
		if err := rows.Scan(
			&e.ID, &e.CreatorID, &e.FinancialYear, &e.GrossAmountPaise, &e.TDSAmountPaise,
			&e.Section, &e.ReferenceID, &e.DeductedAt,
		); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// GetYearlyTDSTotal returns the sum of tds_amount_paise for a creator in
// a given financial year — what has been WITHHELD. It is what the TDS
// summary reports; it is not the threshold input.
func (s *Store) GetYearlyTDSTotal(ctx context.Context, creatorID uuid.UUID, financialYear string) (int64, error) {
	var total int64
	err := s.db.QueryRow(ctx, `
		SELECT COALESCE(SUM(tds_amount_paise), 0)
		FROM tds_ledger
		WHERE creator_id = $1 AND financial_year = $2
	`, creatorID, financialYear).Scan(&total)
	return total, err
}

// GetYearlyTDSGrossTotal returns the sum of gross_amount_paise for a
// creator in a given financial year — what has been PAID OUT. This is the
// threshold input: section 194-O's Rs 30,000 is a limit on gross, and
// summing the TDS column instead (as DeductTDS did before Phase 3A) meant
// the threshold could never be reached, because nothing was withheld
// until it was.
func (s *Store) GetYearlyTDSGrossTotal(ctx context.Context, creatorID uuid.UUID, financialYear string) (int64, error) {
	return s.GetYearlyTDSGrossTotalTx(ctx, s.db, creatorID, financialYear)
}

// GetYearlyTDSGrossTotalTx is GetYearlyTDSGrossTotal on the caller's
// transaction.
func (s *Store) GetYearlyTDSGrossTotalTx(ctx context.Context, db DBTX, creatorID uuid.UUID, financialYear string) (int64, error) {
	var total int64
	err := db.QueryRow(ctx, `
		SELECT COALESCE(SUM(gross_amount_paise), 0)
		FROM tds_ledger
		WHERE creator_id = $1 AND financial_year = $2
	`, creatorID, financialYear).Scan(&total)
	return total, err
}

// ---------------------------------------------------------------------------
// GST Ledger
// ---------------------------------------------------------------------------

// InsertGSTEntry inserts a GST ledger record.
func (s *Store) InsertGSTEntry(ctx context.Context, e *GSTEntry) error {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	e.CreatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO gst_ledger (id, transaction_id, taxable_amount_paise, gst_rate_bps, cgst_paise, sgst_paise, igst_paise, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, e.ID, e.TransactionID, e.TaxableAmountPaise, e.GSTRateBPS,
		e.CGSTPaise, e.SGSTPaise, e.IGSTPaise, e.CreatedAt)
	return err
}

// ---------------------------------------------------------------------------
// Invoices
// ---------------------------------------------------------------------------

// CreateInvoice inserts a new invoice record.
func (s *Store) CreateInvoice(ctx context.Context, inv *Invoice) error {
	if inv.ID == uuid.Nil {
		inv.ID = uuid.New()
	}
	inv.CreatedAt = time.Now()
	err := s.db.QueryRow(ctx, `
		INSERT INTO invoices (id, user_id, invoice_type, amount_paise, tax_paise, total_paise, pdf_media_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING invoice_number
	`, inv.ID, inv.UserID, inv.InvoiceType, inv.AmountPaise,
		inv.TaxPaise, inv.TotalPaise, inv.PDFMediaID, inv.CreatedAt).Scan(&inv.InvoiceNumber)
	return err
}

// ListInvoices returns paginated invoices for a user, ordered by created_at DESC.
func (s *Store) ListInvoices(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Invoice, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, invoice_number, invoice_type, amount_paise, tax_paise, total_paise, pdf_media_id, created_at
		FROM invoices
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var invoices []Invoice
	for rows.Next() {
		var inv Invoice
		if err := rows.Scan(
			&inv.ID, &inv.UserID, &inv.InvoiceNumber, &inv.InvoiceType,
			&inv.AmountPaise, &inv.TaxPaise, &inv.TotalPaise, &inv.PDFMediaID, &inv.CreatedAt,
		); err != nil {
			return nil, err
		}
		invoices = append(invoices, inv)
	}
	return invoices, rows.Err()
}

// GetTDSEntryByReferenceTx returns the tds_ledger row a payout request
// wrote when it was priced (positive gross), or nil. The failed/reversed
// convergence mirrors it so the record for that request nets to zero.
func (s *Store) GetTDSEntryByReferenceTx(ctx context.Context, db DBTX, referenceID uuid.UUID) (*TDSEntry, error) {
	var e TDSEntry
	err := db.QueryRow(ctx, `
		SELECT id, creator_id, financial_year, gross_amount_paise, tds_amount_paise, section, reference_id, deducted_at
		FROM tds_ledger
		WHERE reference_id = $1 AND gross_amount_paise > 0
		ORDER BY deducted_at ASC
		LIMIT 1
	`, referenceID).Scan(
		&e.ID, &e.CreatorID, &e.FinancialYear, &e.GrossAmountPaise, &e.TDSAmountPaise,
		&e.Section, &e.ReferenceID, &e.DeductedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}
