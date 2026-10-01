package postgres

// The in-place correction of the ₹0 invoices (cmd/reissue-invoices,
// migration 039).
//
// Every invoice issued between the P0 checkout and the fix of 1 Oct 2026
// (31dc9eac) carried a ₹0 grand total: IssueInvoice read the NUMERIC rupee
// columns the P0 checkout leaves at 0.00. invoices.order_id is UNIQUE and
// IssueInvoice returns an existing row, so nothing corrects them on its own.
//
// The correction keeps the invoice's identity — number, financial year,
// sequence, issued_at, media keys — and replaces only its money: the four
// totals and is_interstate here, and the documents at the same keys (the
// service layer). Nothing here allocates an invoice number.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// InvoiceReissueActorID is the actor on every invoice_reissue audit row. No
// user holds it; migration 039 binds it to that one action and reason both
// ways, so it can sign nothing else.
var InvoiceReissueActorID = uuid.MustParse("0888221b-0db5-4fcc-81e0-67a27eb880b5")

const (
	// AuditActionInvoiceReissue is migration 039's audit action.
	AuditActionInvoiceReissue = "invoice_reissue"
	// InvoiceReissueReason names the rule that acted, as AutoApproveReason
	// does for the product auto-approval.
	InvoiceReissueReason = "system:invoice_reissue"
	// InvoiceReissueMigration is the migration the audit row needs.
	InvoiceReissueMigration = "039_invoice_reissue_audit.sql"
)

// ErrInvoiceReissueStale refuses a correction when the invoice or its order
// no longer looks as it did when it was listed and its document was built.
var ErrInvoiceReissueStale = errors.New("invoice reissue: the invoice changed since it was listed")

// InvoiceTotalsMinor is an invoice row's money, in paise.
type InvoiceTotalsMinor struct {
	GrandTotal   int64 `json:"grand_total_minor"`
	CGST         int64 `json:"cgst_total_minor"`
	SGST         int64 `json:"sgst_total_minor"`
	IGST         int64 `json:"igst_total_minor"`
	IsInterstate bool  `json:"is_interstate"`
}

// InvoiceReissueRow is one issued invoice beside the money its order stored.
type InvoiceReissueRow struct {
	InvoiceID     uuid.UUID
	OrderID       uuid.UUID
	InvoiceNumber string
	FinancialYear string
	Sequence      int64
	IssuedAt      time.Time
	Totals        InvoiceTotalsMinor
	HTMLMediaKey  *string
	PDFMediaKey   *string

	// OrderFound is false for an invoice whose order row is gone.
	OrderFound  bool
	OrderNumber string
	// HasStoredSplit: the order was written by the P0 checkout
	// (place_of_supply_state set), so it stored a per-line GST split.
	HasStoredSplit  bool
	OrderFinalMinor int64
}

// ListInvoicesForReissue returns every invoice beside its order's stored
// final amount, oldest number first. The NUMERIC totals are read as paise
// in SQL so no float sits between the two sides of the comparison.
func (s *Store) ListInvoicesForReissue(ctx context.Context) ([]InvoiceReissueRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT i.id, i.order_id, i.invoice_number, i.financial_year, i.sequence, i.issued_at,
		       ROUND(i.grand_total * 100)::bigint, ROUND(i.cgst_total * 100)::bigint,
		       ROUND(i.sgst_total * 100)::bigint, ROUND(i.igst_total * 100)::bigint,
		       i.is_interstate, i.html_media_key, i.pdf_media_key,
		       o.id IS NOT NULL, COALESCE(o.order_number, ''),
		       COALESCE(o.place_of_supply_state IS NOT NULL, FALSE),
		       COALESCE(o.final_amount_minor, 0)
		  FROM invoices i
		  LEFT JOIN orders o ON o.id = i.order_id
		 ORDER BY i.financial_year, i.sequence, i.id`)
	if err != nil {
		return nil, fmt.Errorf("list invoices for reissue: %w", err)
	}
	defer rows.Close()
	var out []InvoiceReissueRow
	for rows.Next() {
		var r InvoiceReissueRow
		if err := rows.Scan(&r.InvoiceID, &r.OrderID, &r.InvoiceNumber, &r.FinancialYear, &r.Sequence,
			&r.IssuedAt, &r.Totals.GrandTotal, &r.Totals.CGST, &r.Totals.SGST, &r.Totals.IGST,
			&r.Totals.IsInterstate, &r.HTMLMediaKey, &r.PDFMediaKey,
			&r.OrderFound, &r.OrderNumber, &r.HasStoredSplit, &r.OrderFinalMinor); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// InvoiceReissueMigrationApplied reports whether migration 039 — which lets
// the audit log hold an invoice_reissue row — has run on this database.
func (s *Store) InvoiceReissueMigrationApplied(ctx context.Context) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM schema_migrations
		                WHERE service = 'commerce-service' AND filename = $1)`,
		InvoiceReissueMigration).Scan(&ok)
	return ok, err
}

// InvoiceCorrection is one invoice's correction: the row as it was listed
// (the expectation) and the totals its rebuilt document carries.
type InvoiceCorrection struct {
	Listed InvoiceReissueRow
	After  InvoiceTotalsMinor
}

type invoiceReissueAuditState struct {
	InvoiceNumber string    `json:"invoice_number"`
	FinancialYear string    `json:"financial_year"`
	Sequence      int64     `json:"sequence"`
	IssuedAt      time.Time `json:"issued_at"`
	OrderID       uuid.UUID `json:"order_id"`
	OrderNumber   string    `json:"order_number"`
	HTMLMediaKey  *string   `json:"html_media_key"`
	PDFMediaKey   *string   `json:"pdf_media_key"`
	InvoiceTotalsMinor
	OrderFinalMinor int64 `json:"order_final_amount_minor"`
}

// paiseNumeric renders paise as an exact NUMERIC(12,2) literal.
func paiseNumeric(p int64) string {
	sign := ""
	if p < 0 {
		sign, p = "-", -p
	}
	return fmt.Sprintf("%s%d.%02d", sign, p/100, p%100)
}

// CorrectInvoiceTotals rewrites one invoice's money in place, in one
// transaction:
//
//  1. lock the invoice row and re-check that it, and its order, are exactly
//     as listed — same number and media keys, same totals, the order still
//     has a stored split and the same final amount, the rebuilt grand total
//     equals that final amount, and the stored grand total still differs
//     from it. Anything else is ErrInvoiceReissueStale and nothing is
//     written; a second run finds no difference and so never reaches here;
//  2. update grand_total, cgst/sgst/igst_total and is_interstate — never the
//     number, year, sequence, issued_at or keys;
//  3. append the invoice_reissue audit row (migration 039), before and after;
//  4. call overwrite, which replaces the documents at the SAME keys. It runs
//     last, after every database check has passed, so a refusal never leaves
//     a rewritten document beside an unchanged row; if it fails the
//     transaction rolls back. (If the commit itself fails after a successful
//     overwrite, the document is already right and the row still differs
//     from its order, so the next run corrects the row.)
//
// It allocates no invoice number and emits no event.
func (s *Store) CorrectInvoiceTotals(ctx context.Context, c InvoiceCorrection, overwrite func(context.Context) error) error {
	l := c.Listed
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var (
		number           string
		html, pdf        *string
		now              InvoiceTotalsMinor
		final            int64
		split, orderSeen bool
	)
	err = tx.QueryRow(ctx, `
		SELECT i.invoice_number, i.html_media_key, i.pdf_media_key,
		       ROUND(i.grand_total * 100)::bigint, ROUND(i.cgst_total * 100)::bigint,
		       ROUND(i.sgst_total * 100)::bigint, ROUND(i.igst_total * 100)::bigint, i.is_interstate,
		       o.id IS NOT NULL, COALESCE(o.place_of_supply_state IS NOT NULL, FALSE),
		       COALESCE(o.final_amount_minor, 0)
		  FROM invoices i
		  LEFT JOIN orders o ON o.id = i.order_id
		 WHERE i.id = $1
		   FOR UPDATE OF i`, l.InvoiceID).Scan(
		&number, &html, &pdf, &now.GrandTotal, &now.CGST, &now.SGST, &now.IGST, &now.IsInterstate,
		&orderSeen, &split, &final)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: invoice %s is gone", ErrInvoiceReissueStale, l.InvoiceID)
	}
	if err != nil {
		return fmt.Errorf("lock invoice %s: %w", l.InvoiceID, err)
	}
	switch {
	case number != l.InvoiceNumber:
		return fmt.Errorf("%w: invoice %s is now numbered %s", ErrInvoiceReissueStale, l.InvoiceNumber, number)
	case !sameKey(html, l.HTMLMediaKey) || !sameKey(pdf, l.PDFMediaKey):
		return fmt.Errorf("%w: invoice %s's document keys moved", ErrInvoiceReissueStale, l.InvoiceNumber)
	case now != l.Totals:
		return fmt.Errorf("%w: invoice %s's totals moved from %+v to %+v", ErrInvoiceReissueStale, l.InvoiceNumber, l.Totals, now)
	case !orderSeen || !split:
		return fmt.Errorf("%w: invoice %s's order has no stored split", ErrInvoiceReissueStale, l.InvoiceNumber)
	case final != l.OrderFinalMinor:
		return fmt.Errorf("%w: invoice %s's order final amount moved %d → %d", ErrInvoiceReissueStale, l.InvoiceNumber, l.OrderFinalMinor, final)
	case c.After.GrandTotal != final:
		return fmt.Errorf("%w: invoice %s rebuilt at %d, the order charged %d", ErrInvoiceReissueStale, l.InvoiceNumber, c.After.GrandTotal, final)
	case now.GrandTotal == final:
		return fmt.Errorf("%w: invoice %s already carries the order's final amount", ErrInvoiceReissueStale, l.InvoiceNumber)
	}

	tag, err := tx.Exec(ctx, `
		UPDATE invoices
		   SET grand_total = $2::numeric, cgst_total = $3::numeric,
		       sgst_total = $4::numeric, igst_total = $5::numeric, is_interstate = $6
		 WHERE id = $1 AND invoice_number = $7`,
		l.InvoiceID, paiseNumeric(c.After.GrandTotal), paiseNumeric(c.After.CGST),
		paiseNumeric(c.After.SGST), paiseNumeric(c.After.IGST), c.After.IsInterstate, l.InvoiceNumber)
	if err != nil {
		return fmt.Errorf("update invoice %s: %w", l.InvoiceNumber, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: invoice %s updated %d rows", ErrInvoiceReissueStale, l.InvoiceNumber, tag.RowsAffected())
	}

	state := func(t InvoiceTotalsMinor) invoiceReissueAuditState {
		return invoiceReissueAuditState{
			InvoiceNumber: l.InvoiceNumber, FinancialYear: l.FinancialYear, Sequence: l.Sequence,
			IssuedAt: l.IssuedAt, OrderID: l.OrderID, OrderNumber: l.OrderNumber,
			HTMLMediaKey: l.HTMLMediaKey, PDFMediaKey: l.PDFMediaKey,
			InvoiceTotalsMinor: t, OrderFinalMinor: final,
		}
	}
	reason := InvoiceReissueReason
	if err := insertAdminAudit(ctx, tx, adminAuditEntry{
		Actor: InvoiceReissueActorID, Action: AuditActionInvoiceReissue,
		TargetType: "invoice", TargetID: l.InvoiceID,
		Before: state(now), After: state(c.After), Reason: &reason,
	}); err != nil {
		return err
	}

	if err := overwrite(ctx); err != nil {
		return fmt.Errorf("overwrite the documents of %s: %w", l.InvoiceNumber, err)
	}
	return tx.Commit(ctx)
}

func sameKey(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
