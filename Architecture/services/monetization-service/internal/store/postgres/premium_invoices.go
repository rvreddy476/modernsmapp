package postgres

// Momentum Premium tax invoices and credit notes (migration 029). Issued only
// inside ApplyPremiumPaymentEvent's transaction (premium.go), numbered from
// premium_invoice_counters in that same transaction; read by the buyer's
// routes, the accountant's GST register and the outbox sweep.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/premium"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const premiumInvoiceColumns = `id, kind, series, fy, seq, number, user_id, pass_id, issued_at, to_char(invoice_date, 'YYYY-MM-DD'),
	supplier_legal_name, supplier_address, supplier_gstin, supplier_state_code, supplier_state_name,
	buyer_name, pos_state_code, pos_state_name, description, sac, plan_code, duration_days, gst_rate_bp,
	taxable_paise, cgst_paise, sgst_paise, igst_paise, total_paise, currency, tax_provisional,
	original_invoice_id, COALESCE(original_number, ''), COALESCE(to_char(original_date, 'YYYY-MM-DD'), '')`

func scanPremiumInvoice(row pgx.Row) (*premium.Invoice, error) {
	var i premium.Invoice
	s := &i.Supplier
	if err := row.Scan(&i.ID, &i.Kind, &i.Series, &i.FY, &i.Seq, &i.Number, &i.UserID, &i.PassID, &i.IssuedAt, &i.InvoiceDate,
		&s.LegalName, &s.Address, &s.GSTIN, &s.StateCode, &s.StateName,
		&i.BuyerName, &i.POSCode, &i.POSName, &i.Description, &i.SAC, &i.PlanCode, &i.DurationDays, &i.RateBP,
		&i.TaxablePaise, &i.CGSTPaise, &i.SGSTPaise, &i.IGSTPaise, &i.TotalPaise, &i.Currency, &i.Provisional,
		&i.OriginalID, &i.OriginalNumber, &i.OriginalDate); err != nil {
		return nil, err
	}
	return &i, nil
}

func (s *Store) queryPremiumInvoices(ctx context.Context, sql string, args ...any) ([]premium.Invoice, error) {
	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []premium.Invoice{}
	for rows.Next() {
		i, err := scanPremiumInvoice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}

// nextPremiumDocumentSeqTx takes the next number of a series in a financial
// year. The counter row stays locked until the transaction ends, so numbers
// are issued one after the other, and a rollback returns the number.
func nextPremiumDocumentSeqTx(ctx context.Context, tx pgx.Tx, series, fy string) (int64, error) {
	var seq int64
	err := tx.QueryRow(ctx, `
		INSERT INTO premium_invoice_counters (series, fy, last_seq) VALUES ($1, $2, 1)
		ON CONFLICT (series, fy) DO UPDATE
		SET last_seq = premium_invoice_counters.last_seq + 1, updated_at = NOW()
		RETURNING last_seq`, series, fy).Scan(&seq)
	return seq, err
}

func insertPremiumInvoiceTx(ctx context.Context, tx pgx.Tx, i premium.Invoice) error {
	var origDate any
	if i.OriginalDate != "" {
		origDate = i.OriginalDate
	}
	var origNumber any
	if i.OriginalNumber != "" {
		origNumber = i.OriginalNumber
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO premium_invoices (id, kind, series, fy, seq, number, user_id, pass_id, issued_at, invoice_date,
			supplier_legal_name, supplier_address, supplier_gstin, supplier_state_code, supplier_state_name,
			buyer_name, pos_state_code, pos_state_name, description, sac, plan_code, duration_days, gst_rate_bp,
			taxable_paise, cgst_paise, sgst_paise, igst_paise, total_paise, currency, tax_provisional,
			original_invoice_id, original_number, original_date)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::date, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23,
			$24, $25, $26, $27, $28, $29, $30, $31, $32, $33::date)`,
		i.ID, i.Kind, i.Series, i.FY, i.Seq, i.Number, i.UserID, i.PassID, i.IssuedAt, i.InvoiceDate,
		i.Supplier.LegalName, i.Supplier.Address, i.Supplier.GSTIN, i.Supplier.StateCode, i.Supplier.StateName,
		i.BuyerName, i.POSCode, i.POSName, i.Description, i.SAC, i.PlanCode, i.DurationDays, i.RateBP,
		i.TaxablePaise, i.CGSTPaise, i.SGSTPaise, i.IGSTPaise, i.TotalPaise, i.Currency, i.Provisional,
		i.OriginalID, origNumber, origDate)
	return err
}

// issuePremiumDocumentTx issues what premium.DocumentFor says for this
// decision, once per pass and kind. The pass row is locked FOR UPDATE by the
// caller, so two events for one pass cannot both issue.
func issuePremiumDocumentTx(ctx context.Context, tx pgx.Tx, d premium.Decision, pass premium.Period, sup premium.Supplier,
	now time.Time, applied *premium.Applied) error {
	if d.Outcome != premium.OutcomeActivated && d.Outcome != premium.OutcomeRefunded {
		return nil
	}
	existing := map[string]*premium.Invoice{}
	rows, err := tx.Query(ctx, `SELECT `+premiumInvoiceColumns+` FROM premium_invoices WHERE pass_id = $1`, pass.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		i, err := scanPremiumInvoice(rows)
		if err != nil {
			rows.Close()
			return err
		}
		existing[i.Kind] = i
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	switch premium.DocumentFor(d, existing[premium.KindInvoice] != nil, existing[premium.KindCreditNote] != nil) {
	case premium.KindInvoice:
		if pass.Tax == nil {
			// A pass opened before prices excluded GST (none in production)
			// has no stored split to invoice from.
			slog.ErrorContext(ctx, "premium: paid pass has no GST split; no invoice issued", "pass_id", pass.ID)
			return nil
		}
		var buyer *string
		if err := tx.QueryRow(ctx, `SELECT buyer_name FROM premium_periods WHERE id = $1`, pass.ID).Scan(&buyer); err != nil {
			return err
		}
		seq, err := nextPremiumDocumentSeqTx(ctx, tx, premium.SeriesInvoice, premium.FinancialYear(now))
		if err != nil {
			return err
		}
		name := ""
		if buyer != nil {
			name = *buyer
		}
		inv, err := premium.NewInvoice(pass, sup, name, now, seq)
		if err != nil {
			return fmt.Errorf("premium invoice for pass %s: %w", pass.ID, err)
		}
		if err := insertPremiumInvoiceTx(ctx, tx, inv); err != nil {
			return err
		}
		applied.Document = &inv
	case premium.KindCreditNote:
		seq, err := nextPremiumDocumentSeqTx(ctx, tx, premium.SeriesCreditNote, premium.FinancialYear(now))
		if err != nil {
			return err
		}
		cn, err := premium.NewCreditNote(*existing[premium.KindInvoice], now, seq)
		if err != nil {
			return fmt.Errorf("premium credit note for pass %s: %w", pass.ID, err)
		}
		if err := insertPremiumInvoiceTx(ctx, tx, cn); err != nil {
			return err
		}
		applied.Document = &cn
	}
	return nil
}

// SetPremiumPeriodBuyer records the buyer's display name on an open pass
// (best effort, at checkout). A paid pass keeps the name it was invoiced with.
func (s *Store) SetPremiumPeriodBuyer(ctx context.Context, periodID uuid.UUID, name string) error {
	_, err := s.db.Exec(ctx, `UPDATE premium_periods SET buyer_name = NULLIF($2, ''), updated_at = NOW()
		WHERE id = $1 AND status IN ('created', 'abandoned')`, periodID, name)
	return err
}

// ListPremiumInvoices answers the buyer's documents, newest first. On an
// equal issued_at a credit note sorts first: it always follows the invoice
// it reverses, and the two series number independently, so seq alone would
// put MP/000007 above MPC/000002.
func (s *Store) ListPremiumInvoices(ctx context.Context, userID uuid.UUID) ([]premium.Invoice, error) {
	return s.queryPremiumInvoices(ctx, `SELECT `+premiumInvoiceColumns+` FROM premium_invoices
		WHERE user_id = $1 ORDER BY issued_at DESC, (kind = 'credit_note') DESC, seq DESC LIMIT 500`, userID)
}

// GetPremiumInvoice answers one document, or nil. The service checks the owner.
func (s *Store) GetPremiumInvoice(ctx context.Context, id uuid.UUID) (*premium.Invoice, error) {
	i, err := scanPremiumInvoice(s.db.QueryRow(ctx, `SELECT `+premiumInvoiceColumns+` FROM premium_invoices WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return i, err
}

// PremiumInvoiceIDForPass answers the tax invoice of a pass, or nil.
func (s *Store) PremiumInvoiceIDForPass(ctx context.Context, passID uuid.UUID) (*uuid.UUID, error) {
	var id uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT id FROM premium_invoices WHERE pass_id = $1 AND kind = 'invoice'`, passID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// PremiumGSTRegister answers every document dated from..to (IST dates,
// inclusive), invoices then credit notes, each in number order.
func (s *Store) PremiumGSTRegister(ctx context.Context, from, to string) ([]premium.Invoice, error) {
	return s.queryPremiumInvoices(ctx, `SELECT `+premiumInvoiceColumns+` FROM premium_invoices
		WHERE invoice_date BETWEEN $1::date AND $2::date
		ORDER BY kind DESC, fy, seq`, from, to)
}

// UnpublishedPremiumInvoices answers documents issued before `before` whose
// event is not yet published (the outbox sweep).
func (s *Store) UnpublishedPremiumInvoices(ctx context.Context, before time.Time, limit int) ([]premium.Invoice, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.queryPremiumInvoices(ctx, `SELECT `+premiumInvoiceColumns+` FROM premium_invoices
		WHERE published_at IS NULL AND issued_at < $1 ORDER BY issued_at LIMIT $2`, before, limit)
}

// MarkPremiumInvoicePublished records that a document's event went out.
func (s *Store) MarkPremiumInvoicePublished(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := s.db.Exec(ctx, `UPDATE premium_invoices SET published_at = $2 WHERE id = $1 AND published_at IS NULL`, id, at)
	return err
}
