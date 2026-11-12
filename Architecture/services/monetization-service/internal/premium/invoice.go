package premium

// GST tax invoices and credit notes for Momentum Premium (13 Nov 2026).
//
//   - A tax invoice is issued in the SAME transaction that marks a pass paid
//     from the signed payment.succeeded event, exactly once per pass.
//   - A credit note is issued in the SAME transaction as the full refund that
//     revokes the pass, exactly once per pass, against its invoice.
//   - Numbers: one series per kind, consecutive per Indian financial year in
//     IST, from a counter row updated in the issuing transaction (a rollback
//     takes the number back, so there are no gaps): MP/26-27/000001 for
//     invoices, MPC/26-27/000001 for credit notes (rule 46(b): at most 16
//     characters, unique within the financial year).
//   - Every amount is integer paise copied from the split stored on the pass
//     (migration 028); a credit note is the exact negative of its invoice.
//     Only shared/invoice's renderer formats rupees.
//   - The supplier is snapshotted from configuration onto every document;
//     production with Premium on refuses to boot without the legal name,
//     address and GSTIN. The buyer is named by their profile display name
//     (best effort, at checkout); no e-mail or phone is ever printed.
//   - tax_provisional is kept on the row for the accountant; it is never on
//     the customer's document.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	sharedinvoice "github.com/atpost/shared/invoice"
	"github.com/atpost/shared/kyc"
	"github.com/google/uuid"
)

// Document kinds as the API and the database name them.
const (
	KindInvoice    = "invoice"
	KindCreditNote = "credit_note"
)

// Number series.
const (
	SeriesInvoice    = "MP"
	SeriesCreditNote = "MPC"
)

// Outbox events (payload ids only).
const (
	EventInvoiceIssued    = "premium.invoice.issued"
	EventCreditNoteIssued = "premium.credit_note.issued"
)

// Supplier configuration.
const (
	EnvSupplierLegalName = "PREMIUM_SUPPLIER_LEGAL_NAME"
	EnvSupplierAddress   = "PREMIUM_SUPPLIER_ADDRESS"
)

// IST is India Standard Time: invoice dates and financial years are Indian.
var IST = time.FixedZone("IST", 5*3600+30*60)

var (
	// ErrSupplierRequired: production with Premium on and the supplier's
	// legal name, address or GSTIN missing.
	ErrSupplierRequired = errors.New("PREMIUM_SUPPLIER_LEGAL_NAME, PREMIUM_SUPPLIER_ADDRESS and PREMIUM_PLATFORM_GSTIN are required in production: every Premium sale is invoiced in the supplier's name")
	// ErrInvoiceNotIssuable: the pass or invoice cannot be documented.
	ErrInvoiceNotIssuable = errors.New("PREMIUM_INVOICE_NOT_ISSUABLE")
)

// Supplier is the platform as printed on every document.
type Supplier struct {
	LegalName string
	Address   string
	GSTIN     string
	StateCode string
	StateName string
}

// Complete reports whether every field a tax invoice needs is set.
func (s Supplier) Complete() bool {
	return s.LegalName != "" && s.Address != "" && s.GSTIN != "" && s.StateCode != "" && s.StateName != ""
}

// SupplierFromEnv reads the supplier. In production every field is required
// (ErrSupplierRequired); elsewhere what is set is used and the rest prints as
// a dash. A GSTIN that does not validate refuses anywhere.
func SupplierFromEnv(getenv func(string) string, environment string) (Supplier, error) {
	s := Supplier{
		LegalName: collapse(getenv(EnvSupplierLegalName), 200),
		Address:   collapse(getenv(EnvSupplierAddress), 500),
	}
	if raw := strings.TrimSpace(getenv(EnvPlatformGSTIN)); raw != "" {
		v, err := kyc.ValidateGSTIN(raw)
		if err != nil {
			return Supplier{}, fmt.Errorf("%w: %v", ErrPlatformGSTINInvalid, err)
		}
		s.GSTIN, s.StateCode = v.Normalized, v.StateCode
		s.StateName, _ = kyc.GSTStateName(v.StateCode)
	}
	if IsProduction(environment) && !s.Complete() {
		return Supplier{}, ErrSupplierRequired
	}
	return s, nil
}

// collapse trims, folds runs of white space (an address may be configured
// over several lines) and caps the length in runes.
func collapse(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}

// BuyerName cleans a profile display name for the invoice ("" if none).
func BuyerName(displayName string) string { return collapse(displayName, 200) }

// Invoice is a premium_invoices row: a tax invoice or a credit note. On a
// credit note every amount is negative.
type Invoice struct {
	ID           uuid.UUID
	Kind         string
	Series       string
	FY           string
	Seq          int64
	Number       string
	UserID       uuid.UUID
	PassID       uuid.UUID
	IssuedAt     time.Time
	InvoiceDate  string // YYYY-MM-DD in IST
	Supplier     Supplier
	BuyerName    string
	POSCode      string
	POSName      string
	Description  string
	SAC          string
	PlanCode     string
	DurationDays int
	RateBP       int
	TaxablePaise int64
	CGSTPaise    int64
	SGSTPaise    int64
	IGSTPaise    int64
	TotalPaise   int64
	Currency     string
	// Provisional: the rate awaits the adviser (internal; never printed).
	Provisional bool
	// The invoice a credit note is against.
	OriginalID     *uuid.UUID
	OriginalNumber string
	OriginalDate   string
}

// InvoiceDate is the date of `at` in India.
func InvoiceDate(at time.Time) string { return at.In(IST).Format("2006-01-02") }

// FinancialYear is the short Indian financial year of `at` in IST ("26-27").
func FinancialYear(at time.Time) string { return sharedinvoice.ShortFinancialYear(at.In(IST)) }

// InvoiceNumber formats a document number: "MP/26-27/000001". It refuses a
// number GST does not allow (no series, a sequence below 1, or longer than
// 16 characters).
func InvoiceNumber(series, fy string, seq int64) (string, error) {
	if series == "" || fy == "" || seq < 1 {
		return "", fmt.Errorf("%w: series %q, year %q, sequence %d", ErrInvoiceNotIssuable, series, fy, seq)
	}
	n := fmt.Sprintf("%s/%s/%06d", series, fy, seq)
	if len(n) > sharedinvoice.MaxDocumentNumberLen {
		return "", fmt.Errorf("%w: %q is longer than %d characters", ErrInvoiceNotIssuable, n, sharedinvoice.MaxDocumentNumberLen)
	}
	return n, nil
}

// Description is the invoice line of a pass.
func Description(durationDays int) string {
	return fmt.Sprintf("Momentum Premium — %d days", durationDays)
}

// NewInvoice builds the tax invoice of a paid pass from the split stored on
// it. seq is the next number of the invoice series in FinancialYear(at).
func NewInvoice(p Period, sup Supplier, buyerName string, at time.Time, seq int64) (Invoice, error) {
	if p.Status != StatusPaid {
		return Invoice{}, fmt.Errorf("%w: pass is %s", ErrInvoiceNotIssuable, p.Status)
	}
	if p.Tax == nil || !p.Tax.Valid() || p.Tax.TotalPaise != p.AmountPaise {
		return Invoice{}, fmt.Errorf("%w: pass has no GST split that adds up to its amount", ErrInvoiceNotIssuable)
	}
	fy := FinancialYear(at)
	number, err := InvoiceNumber(SeriesInvoice, fy, seq)
	if err != nil {
		return Invoice{}, err
	}
	t := *p.Tax
	return Invoice{
		ID: uuid.New(), Kind: KindInvoice, Series: SeriesInvoice, FY: fy, Seq: seq, Number: number,
		UserID: p.UserID, PassID: p.ID, IssuedAt: Truncate(at), InvoiceDate: InvoiceDate(at),
		Supplier: sup, BuyerName: BuyerName(buyerName),
		// Place of supply: the supplier's state (premium/tax.go — the
		// platform keeps no address for the buyer; adviser item).
		POSCode: sup.StateCode, POSName: sup.StateName,
		Description: Description(p.DurationDays), SAC: t.SAC, PlanCode: p.PlanCode, DurationDays: p.DurationDays,
		RateBP: t.RateBP, TaxablePaise: t.TaxablePaise, CGSTPaise: t.CGSTPaise, SGSTPaise: t.SGSTPaise, IGSTPaise: t.IGSTPaise,
		TotalPaise: t.TotalPaise, Currency: p.Currency, Provisional: t.Provisional,
	}, nil
}

// NewCreditNote builds the credit note that reverses an invoice in full:
// every amount negated, the supplier, buyer and place of supply as invoiced.
func NewCreditNote(orig Invoice, at time.Time, seq int64) (Invoice, error) {
	if orig.Kind != KindInvoice || orig.TotalPaise <= 0 {
		return Invoice{}, fmt.Errorf("%w: a credit note reverses a positive tax invoice", ErrInvoiceNotIssuable)
	}
	fy := FinancialYear(at)
	number, err := InvoiceNumber(SeriesCreditNote, fy, seq)
	if err != nil {
		return Invoice{}, err
	}
	c := orig
	origID := orig.ID
	c.ID, c.Kind, c.Series, c.FY, c.Seq, c.Number = uuid.New(), KindCreditNote, SeriesCreditNote, fy, seq, number
	c.IssuedAt, c.InvoiceDate = Truncate(at), InvoiceDate(at)
	c.TaxablePaise, c.CGSTPaise, c.SGSTPaise, c.IGSTPaise, c.TotalPaise =
		-orig.TaxablePaise, -orig.CGSTPaise, -orig.SGSTPaise, -orig.IGSTPaise, -orig.TotalPaise
	c.OriginalID, c.OriginalNumber, c.OriginalDate = &origID, orig.Number, orig.InvoiceDate
	return c, nil
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// Document is the customer's document. A credit note states the amounts it
// credits (magnitudes); tax_provisional is not on it.
func (inv Invoice) Document() sharedinvoice.ServiceDocument {
	line := sharedinvoice.ServiceLine{Description: inv.Description, SAC: inv.SAC, Quantity: 1, TaxablePaise: abs(inv.TaxablePaise),
		RateBP: inv.RateBP, CGSTPaise: abs(inv.CGSTPaise), SGSTPaise: abs(inv.SGSTPaise), IGSTPaise: abs(inv.IGSTPaise),
		TotalPaise: abs(inv.TotalPaise)}
	d := sharedinvoice.ServiceDocument{
		Kind: sharedinvoice.KindTaxInvoice, Number: inv.Number, Date: inv.InvoiceDate,
		Supplier: sharedinvoice.ServiceParty{LegalName: inv.Supplier.LegalName, Address: inv.Supplier.Address,
			GSTIN: inv.Supplier.GSTIN, StateName: inv.Supplier.StateName, StateCode: inv.Supplier.StateCode},
		RecipientName: inv.BuyerName, PlaceOfSupplyName: inv.POSName, PlaceOfSupplyCode: inv.POSCode,
		Lines:        []sharedinvoice.ServiceLine{line},
		TaxablePaise: line.TaxablePaise, CGSTPaise: line.CGSTPaise, SGSTPaise: line.SGSTPaise, IGSTPaise: line.IGSTPaise,
		TotalPaise: line.TotalPaise,
	}
	if inv.Kind == KindCreditNote {
		d.Kind = sharedinvoice.KindCreditNote
		d.Original = &sharedinvoice.ServiceReference{Number: inv.OriginalNumber, Date: inv.OriginalDate}
		d.Notes = []string{"Issued on the full refund of the Momentum Premium pass invoiced above."}
	}
	return d
}

// EventType is the outbox event announcing the document.
func (inv Invoice) EventType() string {
	if inv.Kind == KindCreditNote {
		return EventCreditNoteIssued
	}
	return EventInvoiceIssued
}
