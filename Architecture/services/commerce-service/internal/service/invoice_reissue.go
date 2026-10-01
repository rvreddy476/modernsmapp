package service

// The in-place correction of the ₹0 invoices (cmd/reissue-invoices).
//
// Every invoice issued between the P0 checkout and 31dc9eac went out at ₹0
// (see invoice_money.go). invoices.order_id is UNIQUE and IssueInvoice
// returns an existing row, so they are never corrected by issuing again.
// The founder approved correcting them IN PLACE (Option 1): the invoice
// keeps its number, financial year, sequence and date; its money and its
// documents are rebuilt from the order's stored paise exactly as
// IssueInvoice builds them today (draftInvoice), and overwritten at the
// SAME keys.
//
// Deliberately absent: no invoice number is allocated, no event is emitted
// and no buyer is notified. Whether a production buyer is told their invoice
// changed is the founder's decision, not this tool's.

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/invoice"
)

// InvoiceReissueOutcome is what happened, or would happen, to one invoice.
type InvoiceReissueOutcome string

const (
	// ReissueWouldCorrect: a dry run found the invoice wrong and its
	// corrected document builds; --apply would write it.
	ReissueWouldCorrect InvoiceReissueOutcome = "would-correct"
	// ReissueCorrected: the row, its documents and its audit row were written.
	ReissueCorrected InvoiceReissueOutcome = "corrected"
	// ReissueSkipped: out of scope or refused, with the reason; nothing written.
	ReissueSkipped InvoiceReissueOutcome = "skipped"
	// ReissueFailed: an unexpected error; nothing committed for this invoice.
	ReissueFailed InvoiceReissueOutcome = "failed"
)

// InvoiceReissueResult is one invoice's line in the report.
type InvoiceReissueResult struct {
	Row     postgres.InvoiceReissueRow
	After   *postgres.InvoiceTotalsMinor // the totals the rebuilt invoice carries
	Outcome InvoiceReissueOutcome
	Reason  string
}

// InvoiceReissueReport is a whole run. Results holds every candidate and
// every skipped invoice; invoices that already carry their order's final
// amount are only counted.
type InvoiceReissueReport struct {
	Results        []InvoiceReissueResult
	AlreadyCorrect int
}

// Count returns how many results had the outcome.
func (r *InvoiceReissueReport) Count(o InvoiceReissueOutcome) int {
	n := 0
	for _, x := range r.Results {
		if x.Outcome == o {
			n++
		}
	}
	return n
}

// Reasons an invoice is not a candidate.
const (
	reissueSkipNoOrder = "the invoice's order row is missing"
	reissueSkipNoSplit = "pre-P0 legacy order: no stored GST split (place_of_supply_state unset); out of scope"
)

// selectForReissue decides whether an invoice is a correction candidate.
//
// A candidate is an invoice whose order STORED a GST split (the P0 checkout
// wrote it) and whose stored grand total differs from the order's final
// paise. An invoice that already matches is not a candidate, which is what
// makes a second run a no-op. An invoice whose order has no stored split is
// not touched and is reported with the reason.
func selectForReissue(r postgres.InvoiceReissueRow) (candidate bool, skipReason string) {
	switch {
	case !r.OrderFound:
		return false, reissueSkipNoOrder
	case !r.HasStoredSplit:
		return false, reissueSkipNoSplit
	case r.Totals.GrandTotal == r.OrderFinalMinor:
		return false, ""
	default:
		return true, ""
	}
}

// renderReissuePDF renders the PDF of a corrected invoice. A variable so a
// test can stand in for wkhtmltopdf.
var renderReissuePDF = func(inv invoice.Invoice) ([]byte, string, error) {
	return invoice.PDFRenderer{}.Render(inv)
}

// ReissueInvoices lists every invoice, selects the candidates, rebuilds each
// one's document, and — only when apply is set — corrects it in place, one
// transaction per invoice (postgres.CorrectInvoiceTotals). A dry run writes
// nothing anywhere and needs no blob store.
func (s *Service) ReissueInvoices(ctx context.Context, apply bool) (*InvoiceReissueReport, error) {
	if apply {
		if s.blob == nil {
			return nil, fmt.Errorf("invoice reissue: --apply needs the blob store")
		}
		ok, err := s.store.InvoiceReissueMigrationApplied(ctx)
		if err != nil {
			return nil, fmt.Errorf("invoice reissue: read schema_migrations: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("invoice reissue: migration %s has not run on this database; deploy the "+
				"commerce-service image that carries it (it runs at boot) before --apply", postgres.InvoiceReissueMigration)
		}
	}
	rows, err := s.store.ListInvoicesForReissue(ctx)
	if err != nil {
		return nil, err
	}
	rep := &InvoiceReissueReport{}
	for _, row := range rows {
		candidate, skip := selectForReissue(row)
		switch {
		case skip != "":
			rep.Results = append(rep.Results, InvoiceReissueResult{Row: row, Outcome: ReissueSkipped, Reason: skip})
		case !candidate:
			rep.AlreadyCorrect++
		default:
			rep.Results = append(rep.Results, s.reissueOne(ctx, row, apply))
		}
	}
	return rep, nil
}

func (s *Service) reissueOne(ctx context.Context, row postgres.InvoiceReissueRow, apply bool) InvoiceReissueResult {
	res := InvoiceReissueResult{Row: row}
	skip := func(format string, a ...any) InvoiceReissueResult {
		res.Outcome, res.Reason = ReissueSkipped, fmt.Sprintf(format, a...)
		return res
	}
	fail := func(err error) InvoiceReissueResult {
		res.Outcome, res.Reason = ReissueFailed, err.Error()
		return res
	}

	if row.HTMLMediaKey == nil || *row.HTMLMediaKey == "" {
		return skip("the invoice has no HTML document key to overwrite")
	}
	order, err := s.store.GetOrderByID(ctx, row.OrderID)
	if err != nil {
		return fail(fmt.Errorf("get order: %w", err))
	}
	// The original date, in UTC: the service image runs in UTC (alpine, no
	// tzdata), so this is the date and "Generated on" the ₹0 document printed.
	draft, err := s.draftInvoice(ctx, row.OrderID, order, row.IssuedAt.UTC())
	if errors.Is(err, errInvoiceMoneyUnreconciled) {
		return skip("the order's stored money does not reconcile (%v)", err)
	}
	if err != nil {
		return fail(err)
	}
	if !draft.storedSplit {
		return skip(reissueSkipNoSplit)
	}
	if draft.shipToErr != nil {
		return skip("the delivery address could not be read, and correcting would blank the ship-to "+
			"(are the COMMERCE_PII_* keys set?): %v", draft.shipToErr)
	}
	m := draft.stored
	after := postgres.InvoiceTotalsMinor{
		GrandTotal: m.FinalMinor, CGST: m.CGSTMinor, SGST: m.SGSTMinor, IGST: m.IGSTMinor,
		IsInterstate: m.IsInterstate,
	}
	res.After = &after
	if after.GrandTotal != row.OrderFinalMinor {
		return skip("the order's final amount moved while listing (%d → %d)", row.OrderFinalMinor, after.GrandTotal)
	}

	inv := draft.inv
	inv.Number = row.InvoiceNumber // the SAME number; nothing is allocated
	inv.OrderDate = inv.OrderDate.UTC()

	htmlBody, htmlCT, err := invoice.HTMLRenderer{}.Render(inv)
	if err != nil {
		return fail(fmt.Errorf("render html: %w", err))
	}
	var pdfBody []byte
	var pdfCT string
	if row.PDFMediaKey != nil {
		// The download link prefers the PDF, so a ₹0 PDF left in place would
		// still be what the buyer gets. If it cannot be re-rendered here, the
		// invoice is not touched at all.
		if pdfBody, pdfCT, err = renderReissuePDF(inv); err != nil {
			return skip("the invoice has a PDF at %s that cannot be re-rendered here, and leaving it would "+
				"keep the ₹0 PDF as the download (%v)", *row.PDFMediaKey, err)
		}
	}

	if !apply {
		res.Outcome = ReissueWouldCorrect
		return res
	}
	err = s.store.CorrectInvoiceTotals(ctx, postgres.InvoiceCorrection{Listed: row, After: after},
		func(ctx context.Context) error {
			if err := s.blob.Upload(ctx, *row.HTMLMediaKey, htmlBody, htmlCT); err != nil {
				return fmt.Errorf("html: %w", err)
			}
			if row.PDFMediaKey != nil {
				if err := s.blob.Upload(ctx, *row.PDFMediaKey, pdfBody, pdfCT); err != nil {
					return fmt.Errorf("pdf: %w", err)
				}
			}
			return nil
		})
	if errors.Is(err, postgres.ErrInvoiceReissueStale) {
		return skip("%v", err)
	}
	if err != nil {
		return fail(err)
	}
	res.Outcome = ReissueCorrected
	return res
}

// NewOffline builds a Service for an operator tool: a store and nothing
// else — no Kafka writer, no Redis, no outbound clients. Attach only what
// the tool needs (WithBlob, WithPII, WithPIICutover, WithKYCCutover).
func NewOffline(store *postgres.Store) *Service {
	return &Service{store: store, orders: store}
}
