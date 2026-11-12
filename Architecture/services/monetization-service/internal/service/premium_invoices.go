package service

// Momentum Premium tax invoices and credit notes (migration 029; rules in
// internal/premium/invoice.go). The store issues them inside the signed
// event's transaction; this file names the buyer at checkout, publishes the
// document events after commit (the invoice row is its own outbox, swept by
// the premium worker), and serves the buyer's documents and the
// accountant's GST register.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/events"
	"github.com/atpost/monetization-service/internal/premium"
	sharedinvoice "github.com/atpost/shared/invoice"
	"github.com/google/uuid"
)

var (
	// ErrPremiumInvoiceNotFound: no such document, or not the caller's — 404.
	ErrPremiumInvoiceNotFound = errors.New("PREMIUM_INVOICE_NOT_FOUND")
	// ErrPremiumRegisterRange: from/to are not dates, reversed, or span
	// more than MaxGSTRegisterDays — 400.
	ErrPremiumRegisterRange = errors.New("PREMIUM_REGISTER_RANGE")
)

// MaxGSTRegisterDays is the widest register one call answers (a financial
// year, leap day included).
const MaxGSTRegisterDays = 366

// PremiumInvoiceStore is the document half of PremiumStore.
type PremiumInvoiceStore interface {
	SetPremiumPeriodBuyer(ctx context.Context, periodID uuid.UUID, name string) error
	ListPremiumInvoices(ctx context.Context, userID uuid.UUID) ([]premium.Invoice, error)
	GetPremiumInvoice(ctx context.Context, id uuid.UUID) (*premium.Invoice, error)
	PremiumInvoiceIDForPass(ctx context.Context, passID uuid.UUID) (*uuid.UUID, error)
	PremiumGSTRegister(ctx context.Context, from, to string) ([]premium.Invoice, error)
	UnpublishedPremiumInvoices(ctx context.Context, before time.Time, limit int) ([]premium.Invoice, error)
	MarkPremiumInvoicePublished(ctx context.Context, id uuid.UUID, at time.Time) error
}

// WithSupplier installs the supplier printed on every document
// (premium.SupplierFromEnv; production refuses to boot without it).
func (p *Premium) WithSupplier(s premium.Supplier) *Premium {
	p.supplier = s
	return p
}

// WithProfiles installs the profile source the buyer is named from. nil:
// invoices carry no buyer name (not required on a B2C invoice below
// ₹50,000, rule 46(e)).
func (p *Premium) WithProfiles(src ProfileSource) *Premium {
	p.profiles = src
	return p
}

// recordBuyerName stores the buyer's display name on the pass, best effort:
// a failure leaves the name blank and never blocks the checkout.
func (p *Premium) recordBuyerName(ctx context.Context, userID, periodID uuid.UUID) {
	if p.profiles == nil {
		return
	}
	lookup, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	profiles, err := p.profiles.Profiles(lookup, []uuid.UUID{userID})
	if err != nil {
		slog.WarnContext(ctx, "premium: buyer name lookup failed; invoice will carry none", "pass_id", periodID, "error", err)
		return
	}
	name := premium.BuyerName(profiles[userID].DisplayName)
	if name == "" {
		return
	}
	if err := p.store.SetPremiumPeriodBuyer(ctx, periodID, name); err != nil {
		slog.WarnContext(ctx, "premium: record buyer name", "pass_id", periodID, "error", err)
	}
}

// publishDocument announces one document and marks it published. A failure
// leaves it for PublishPendingDocuments.
func (p *Premium) publishDocument(ctx context.Context, inv premium.Invoice) {
	if p.pub == nil {
		return
	}
	payload := events.PremiumInvoicePayload{UserID: inv.UserID.String(), InvoiceID: inv.ID.String(), PassID: inv.PassID.String(), Kind: inv.Kind}
	if err := p.pub.PublishPremiumInvoiceEvent(ctx, inv.EventType(), payload); err != nil {
		slog.WarnContext(ctx, "premium: publish document event failed; the sweep retries", "invoice_id", inv.ID, "error", err)
		return
	}
	if err := p.store.MarkPremiumInvoicePublished(ctx, inv.ID, p.clock()); err != nil {
		slog.ErrorContext(ctx, "premium: mark document published", "invoice_id", inv.ID, "error", err)
	}
}

// documentSweepDelay leaves the post-commit publish time to finish first.
const documentSweepDelay = time.Minute

// PublishPendingDocuments is the outbox sweep: every document whose event did
// not go out after commit. The event id is stable per document, so a
// duplicate publish is delivered once.
func (p *Premium) PublishPendingDocuments(ctx context.Context) (int, error) {
	if p.pub == nil {
		return 0, nil
	}
	pending, err := p.store.UnpublishedPremiumInvoices(ctx, p.clock().Add(-documentSweepDelay), 100)
	if err != nil {
		return 0, err
	}
	for _, inv := range pending {
		p.publishDocument(ctx, inv)
	}
	return len(pending), nil
}

// PremiumInvoiceSummary is one row of GET /premium/invoices.
type PremiumInvoiceSummary struct {
	ID         uuid.UUID `json:"id"`
	Number     string    `json:"number"`
	Kind       string    `json:"kind"`
	Date       string    `json:"date"`
	TotalPaise int64     `json:"total_paise"`
	Currency   string    `json:"currency"`
	PassID     uuid.UUID `json:"pass_id"`
}

// PremiumInvoiceParty is the supplier as printed.
type PremiumInvoiceParty struct {
	LegalName string `json:"legal_name"`
	Address   string `json:"address"`
	GSTIN     string `json:"gstin"`
	StateName string `json:"state_name"`
	StateCode string `json:"state_code"`
}

// PremiumInvoiceState is a place of supply.
type PremiumInvoiceState struct {
	StateName string `json:"state_name"`
	StateCode string `json:"state_code"`
}

// PremiumInvoiceLine is the document's one line. Amounts are signed: a
// credit note's are negative.
type PremiumInvoiceLine struct {
	Description  string `json:"description"`
	SAC          string `json:"sac"`
	Quantity     int    `json:"quantity"`
	TaxablePaise int64  `json:"taxable_paise"`
	GSTRateBP    int    `json:"gst_rate_bp"`
	CGSTPaise    int64  `json:"cgst_paise"`
	SGSTPaise    int64  `json:"sgst_paise"`
	IGSTPaise    int64  `json:"igst_paise"`
	TotalPaise   int64  `json:"total_paise"`
}

// PremiumInvoiceRef names the invoice a credit note is against.
type PremiumInvoiceRef struct {
	ID     uuid.UUID `json:"id"`
	Number string    `json:"number"`
	Date   string    `json:"date"`
}

// PremiumInvoiceView is GET /premium/invoices/:id. tax_provisional is
// deliberately absent: it is internal.
type PremiumInvoiceView struct {
	ID              uuid.UUID            `json:"id"`
	Number          string               `json:"number"`
	Kind            string               `json:"kind"`
	Date            string               `json:"date"`
	PassID          uuid.UUID            `json:"pass_id"`
	Supplier        PremiumInvoiceParty  `json:"supplier"`
	BuyerName       *string              `json:"buyer_name"`
	PlaceOfSupply   PremiumInvoiceState  `json:"place_of_supply"`
	ReverseCharge   bool                 `json:"reverse_charge"`
	Lines           []PremiumInvoiceLine `json:"lines"`
	TaxablePaise    int64                `json:"taxable_paise"`
	CGSTPaise       int64                `json:"cgst_paise"`
	SGSTPaise       int64                `json:"sgst_paise"`
	IGSTPaise       int64                `json:"igst_paise"`
	TotalPaise      int64                `json:"total_paise"`
	Currency        string               `json:"currency"`
	AmountInWords   string               `json:"amount_in_words"`
	OriginalInvoice *PremiumInvoiceRef   `json:"original_invoice"`
}

func premiumInvoiceView(inv premium.Invoice) PremiumInvoiceView {
	v := PremiumInvoiceView{
		ID: inv.ID, Number: inv.Number, Kind: inv.Kind, Date: inv.InvoiceDate, PassID: inv.PassID,
		Supplier: PremiumInvoiceParty{LegalName: inv.Supplier.LegalName, Address: inv.Supplier.Address, GSTIN: inv.Supplier.GSTIN,
			StateName: inv.Supplier.StateName, StateCode: inv.Supplier.StateCode},
		PlaceOfSupply: PremiumInvoiceState{StateName: inv.POSName, StateCode: inv.POSCode},
		Lines: []PremiumInvoiceLine{{Description: inv.Description, SAC: inv.SAC, Quantity: 1, TaxablePaise: inv.TaxablePaise,
			GSTRateBP: inv.RateBP, CGSTPaise: inv.CGSTPaise, SGSTPaise: inv.SGSTPaise, IGSTPaise: inv.IGSTPaise, TotalPaise: inv.TotalPaise}},
		TaxablePaise: inv.TaxablePaise, CGSTPaise: inv.CGSTPaise, SGSTPaise: inv.SGSTPaise, IGSTPaise: inv.IGSTPaise,
		TotalPaise: inv.TotalPaise, Currency: inv.Currency, AmountInWords: sharedinvoice.AmountInWords(inv.TotalPaise),
	}
	if inv.BuyerName != "" {
		n := inv.BuyerName
		v.BuyerName = &n
	}
	if inv.OriginalID != nil {
		v.OriginalInvoice = &PremiumInvoiceRef{ID: *inv.OriginalID, Number: inv.OriginalNumber, Date: inv.OriginalDate}
	}
	return v
}

// Invoices answers the buyer's documents, newest first.
func (p *Premium) Invoices(ctx context.Context, userID uuid.UUID) ([]PremiumInvoiceSummary, error) {
	rows, err := p.store.ListPremiumInvoices(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]PremiumInvoiceSummary, 0, len(rows))
	for _, inv := range rows {
		if inv.UserID != userID {
			continue
		}
		out = append(out, PremiumInvoiceSummary{ID: inv.ID, Number: inv.Number, Kind: inv.Kind, Date: inv.InvoiceDate,
			TotalPaise: inv.TotalPaise, Currency: inv.Currency, PassID: inv.PassID})
	}
	return out, nil
}

// ownInvoice answers the caller's document; another buyer's is not found,
// exactly as one that does not exist.
func (p *Premium) ownInvoice(ctx context.Context, userID, id uuid.UUID) (*premium.Invoice, error) {
	inv, err := p.store.GetPremiumInvoice(ctx, id)
	if err != nil {
		return nil, err
	}
	if inv == nil || inv.UserID != userID {
		return nil, ErrPremiumInvoiceNotFound
	}
	return inv, nil
}

// Invoice answers one of the buyer's documents.
func (p *Premium) Invoice(ctx context.Context, userID, id uuid.UUID) (*PremiumInvoiceView, error) {
	inv, err := p.ownInvoice(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	v := premiumInvoiceView(*inv)
	return &v, nil
}

// PremiumDocumentFile is a rendered document.
type PremiumDocumentFile struct {
	Body        []byte
	ContentType string
	Filename    string
}

// InvoiceDocument renders one of the buyer's documents as printable HTML
// (no PDF binary ships in the image; the browser saves it as PDF).
func (p *Premium) InvoiceDocument(ctx context.Context, userID, id uuid.UUID) (*PremiumDocumentFile, error) {
	inv, err := p.ownInvoice(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	body, err := sharedinvoice.RenderServiceHTML(inv.Document())
	if err != nil {
		return nil, fmt.Errorf("render premium document %s: %w", inv.ID, err)
	}
	return &PremiumDocumentFile{Body: body, ContentType: "text/html; charset=utf-8", Filename: sharedinvoice.ServiceFilename(inv.Number, ".html")}, nil
}

// GSTRegisterRow is one B2C document for GSTR-1. Amounts are signed (a credit
// note is negative), so a column sums to the net.
type GSTRegisterRow struct {
	Kind              string `json:"kind"`
	Number            string `json:"number"`
	Date              string `json:"date"`
	OriginalNumber    string `json:"original_number,omitempty"`
	OriginalDate      string `json:"original_date,omitempty"`
	PlaceOfSupplyCode string `json:"place_of_supply_code"`
	PlaceOfSupplyName string `json:"place_of_supply_name"`
	SupplyType        string `json:"supply_type"` // intra_state | inter_state
	SAC               string `json:"sac"`
	GSTRateBP         int    `json:"gst_rate_bp"`
	TaxablePaise      int64  `json:"taxable_paise"`
	CGSTPaise         int64  `json:"cgst_paise"`
	SGSTPaise         int64  `json:"sgst_paise"`
	IGSTPaise         int64  `json:"igst_paise"`
	TotalPaise        int64  `json:"total_paise"`
	TaxProvisional    bool   `json:"tax_provisional"`
}

// GSTRegisterTotals sums the register's columns.
type GSTRegisterTotals struct {
	Invoices     int   `json:"invoices"`
	CreditNotes  int   `json:"credit_notes"`
	TaxablePaise int64 `json:"taxable_paise"`
	CGSTPaise    int64 `json:"cgst_paise"`
	SGSTPaise    int64 `json:"sgst_paise"`
	IGSTPaise    int64 `json:"igst_paise"`
	TotalPaise   int64 `json:"total_paise"`
}

// GSTRegister is GET /internal/premium/gst-register.
type GSTRegister struct {
	From         string            `json:"from"`
	To           string            `json:"to"`
	SupplierGSTN string            `json:"supplier_gstin"`
	Rows         []GSTRegisterRow  `json:"rows"`
	Totals       GSTRegisterTotals `json:"totals"`
}

// GSTRegister answers every document dated from..to (IST, inclusive), for
// the accountant's GSTR-1. All Premium buyers are unregistered (B2C).
func (p *Premium) GSTRegister(ctx context.Context, from, to string) (*GSTRegister, error) {
	f, ferr := time.Parse("2006-01-02", from)
	t, terr := time.Parse("2006-01-02", to)
	if ferr != nil || terr != nil || t.Before(f) || t.Sub(f) > (MaxGSTRegisterDays-1)*24*time.Hour {
		return nil, ErrPremiumRegisterRange
	}
	rows, err := p.store.PremiumGSTRegister(ctx, from, to)
	if err != nil {
		return nil, err
	}
	out := &GSTRegister{From: from, To: to, SupplierGSTN: p.supplier.GSTIN, Rows: make([]GSTRegisterRow, 0, len(rows))}
	for _, inv := range rows {
		supply := "intra_state"
		if inv.IGSTPaise != 0 {
			supply = "inter_state"
		}
		out.Rows = append(out.Rows, GSTRegisterRow{Kind: inv.Kind, Number: inv.Number, Date: inv.InvoiceDate,
			OriginalNumber: inv.OriginalNumber, OriginalDate: inv.OriginalDate,
			PlaceOfSupplyCode: inv.POSCode, PlaceOfSupplyName: inv.POSName, SupplyType: supply, SAC: inv.SAC, GSTRateBP: inv.RateBP,
			TaxablePaise: inv.TaxablePaise, CGSTPaise: inv.CGSTPaise, SGSTPaise: inv.SGSTPaise, IGSTPaise: inv.IGSTPaise,
			TotalPaise: inv.TotalPaise, TaxProvisional: inv.Provisional})
		tot := &out.Totals
		if inv.Kind == premium.KindCreditNote {
			tot.CreditNotes++
		} else {
			tot.Invoices++
		}
		tot.TaxablePaise += inv.TaxablePaise
		tot.CGSTPaise += inv.CGSTPaise
		tot.SGSTPaise += inv.SGSTPaise
		tot.IGSTPaise += inv.IGSTPaise
		tot.TotalPaise += inv.TotalPaise
	}
	return out, nil
}
