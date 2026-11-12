package premium

import (
	"errors"
	"strings"
	"testing"
	"time"

	sharedinvoice "github.com/atpost/shared/invoice"
	"github.com/google/uuid"
)

func paidPass(t *testing.T) Period {
	t.Helper()
	b, err := EstimatedTax().Compute(14900, "", time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	s, e := time.Date(2026, 11, 13, 4, 30, 0, 0, time.UTC), time.Date(2026, 12, 13, 4, 30, 0, 0, time.UTC)
	return Period{ID: uuid.New(), UserID: uuid.New(), PlanCode: Plan30d, DurationDays: 30, AmountPaise: b.TotalPaise, Tax: &b,
		Currency: Currency, Status: StatusPaid, PeriodStart: &s, PeriodEnd: &e}
}

func testSupplier() Supplier {
	return Supplier{LegalName: "Example Supplier Private Limited", Address: "1 Example Road, Bengaluru 560001",
		GSTIN: "29AAAAA0000A1Z0", StateCode: "29", StateName: "Karnataka"}
}

func TestInvoiceNumberIsGSTShaped(t *testing.T) {
	n, err := InvoiceNumber(SeriesInvoice, "26-27", 1)
	if err != nil || n != "MP/26-27/000001" {
		t.Fatalf("%q %v", n, err)
	}
	n, err = InvoiceNumber(SeriesCreditNote, "26-27", 999999)
	if err != nil || n != "MPC/26-27/999999" || len(n) != 16 {
		t.Fatalf("%q %v", n, err)
	}
	// Past 16 characters (or a sequence below 1) is refused, never truncated.
	for _, c := range []struct {
		series string
		seq    int64
	}{{SeriesCreditNote, 1_000_000}, {SeriesInvoice, 0}, {SeriesInvoice, -3}, {"", 1}} {
		if _, err := InvoiceNumber(c.series, "26-27", c.seq); !errors.Is(err, ErrInvoiceNotIssuable) {
			t.Errorf("%s %d: %v", c.series, c.seq, err)
		}
	}
	if n, _ := InvoiceNumber(SeriesInvoice, "26-27", 1_000_000); n != "MP/26-27/1000000" {
		t.Fatalf("the invoice series widens within 16 characters: %q", n)
	}
}

// The financial year and the invoice date are India's: 31 March 23:59 IST is
// still the old year; 00:00 IST on 1 April (18:30 UTC on 31 March) is the new.
func TestFinancialYearTurnsAtMidnightIST(t *testing.T) {
	before := time.Date(2027, 3, 31, 18, 29, 59, 0, time.UTC)
	after := time.Date(2027, 3, 31, 18, 30, 0, 0, time.UTC)
	if FinancialYear(before) != "26-27" || InvoiceDate(before) != "2027-03-31" {
		t.Fatalf("before: %s %s", FinancialYear(before), InvoiceDate(before))
	}
	if FinancialYear(after) != "27-28" || InvoiceDate(after) != "2027-04-01" {
		t.Fatalf("after: %s %s", FinancialYear(after), InvoiceDate(after))
	}
	// A UTC date is not an Indian date: 20:00 UTC on 13 Nov is 14 Nov in India.
	if InvoiceDate(time.Date(2026, 11, 13, 20, 0, 0, 0, time.UTC)) != "2026-11-14" {
		t.Fatal("invoice date must be IST")
	}
}

func TestNewInvoiceCopiesThePassSplit(t *testing.T) {
	p := paidPass(t)
	at := time.Date(2026, 11, 13, 4, 30, 0, 0, time.UTC)
	inv, err := NewInvoice(p, testSupplier(), "  Asha   Rao ", at, 7)
	if err != nil {
		t.Fatal(err)
	}
	tx := *p.Tax
	if inv.TaxablePaise != tx.TaxablePaise || inv.CGSTPaise != tx.CGSTPaise || inv.SGSTPaise != tx.SGSTPaise ||
		inv.IGSTPaise != tx.IGSTPaise || inv.TotalPaise != p.AmountPaise || inv.RateBP != tx.RateBP || inv.SAC != "998439" ||
		inv.Provisional != tx.Provisional {
		t.Fatalf("invoice %+v does not carry the pass split %+v", inv, tx)
	}
	if inv.Number != "MP/26-27/000007" || inv.Kind != KindInvoice || inv.InvoiceDate != "2026-11-13" || inv.BuyerName != "Asha Rao" ||
		inv.Description != "Momentum Premium — 30 days" || inv.POSCode != "29" || inv.POSName != "Karnataka" ||
		inv.PassID != p.ID || inv.UserID != p.UserID || inv.OriginalID != nil {
		t.Fatalf("invoice %+v", inv)
	}
	if err := inv.Document().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestNewInvoiceRefusesWhatCannotBeInvoiced(t *testing.T) {
	at := time.Now()
	for name, mut := range map[string]func(*Period){
		"not paid":       func(p *Period) { p.Status = StatusCreated },
		"no split":       func(p *Period) { p.Tax = nil },
		"amount differs": func(p *Period) { p.AmountPaise++ },
		"split broken":   func(p *Period) { c := *p.Tax; c.CGSTPaise++; p.Tax = &c },
	} {
		p := paidPass(t)
		mut(&p)
		if _, err := NewInvoice(p, testSupplier(), "", at, 1); !errors.Is(err, ErrInvoiceNotIssuable) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCreditNoteNegatesItsInvoice(t *testing.T) {
	p := paidPass(t)
	inv, err := NewInvoice(p, testSupplier(), "Asha", time.Date(2026, 11, 13, 4, 30, 0, 0, time.UTC), 1)
	if err != nil {
		t.Fatal(err)
	}
	cn, err := NewCreditNote(inv, time.Date(2027, 4, 2, 4, 30, 0, 0, time.UTC), 1)
	if err != nil {
		t.Fatal(err)
	}
	if cn.TaxablePaise != -inv.TaxablePaise || cn.CGSTPaise != -inv.CGSTPaise || cn.SGSTPaise != -inv.SGSTPaise ||
		cn.IGSTPaise != -inv.IGSTPaise || cn.TotalPaise != -inv.TotalPaise {
		t.Fatalf("credit note %+v does not negate %+v", cn, inv)
	}
	if cn.Kind != KindCreditNote || cn.Number != "MPC/27-28/000001" || cn.OriginalID == nil || *cn.OriginalID != inv.ID ||
		cn.OriginalNumber != inv.Number || cn.OriginalDate != inv.InvoiceDate || cn.ID == inv.ID ||
		cn.BuyerName != inv.BuyerName || cn.Supplier != inv.Supplier || cn.POSCode != inv.POSCode {
		t.Fatalf("credit note %+v", cn)
	}
	d := cn.Document()
	if d.Kind != sharedinvoice.KindCreditNote || d.Original == nil || d.TotalPaise != inv.TotalPaise || d.Validate() != nil {
		t.Fatalf("document %+v", d)
	}
	if _, err := NewCreditNote(cn, time.Now(), 2); !errors.Is(err, ErrInvoiceNotIssuable) {
		t.Fatal("a credit note cannot be credited")
	}
}

// tax_provisional is internal: nothing on the customer's document says it.
func TestDocumentNeverShowsProvisional(t *testing.T) {
	p := paidPass(t)
	inv, _ := NewInvoice(p, testSupplier(), "Asha", time.Now(), 1)
	if !inv.Provisional {
		t.Fatal("the estimate is provisional")
	}
	html, err := sharedinvoice.RenderServiceHTML(inv.Document())
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.ToLower(string(html)); strings.Contains(s, "provisional") || strings.Contains(s, "adviser") {
		t.Fatal("the customer document mentions the provisional rate")
	}
}

func TestDocumentFor(t *testing.T) {
	for _, c := range []struct {
		out     Outcome
		inv, cn bool
		want    string
	}{
		{OutcomeActivated, false, false, KindInvoice},
		{OutcomeActivated, true, false, ""},
		{OutcomeRefunded, true, false, KindCreditNote},
		{OutcomeRefunded, true, true, ""},
		{OutcomeRefunded, false, false, ""},
		{OutcomePartialRefund, true, false, ""},
		{OutcomeIgnored, false, false, ""},
		{OutcomeMismatch, false, false, ""},
		{OutcomeDuplicate, false, false, ""},
	} {
		if got := DocumentFor(Decision{Outcome: c.out}, c.inv, c.cn); got != c.want {
			t.Errorf("%s inv=%v cn=%v: %q, want %q", c.out, c.inv, c.cn, got, c.want)
		}
	}
}

func TestSupplierFromEnvRefusesProductionWithoutTheSupplier(t *testing.T) {
	full := map[string]string{EnvSupplierLegalName: "Example Supplier Private Limited",
		EnvSupplierAddress: "1 Example Road,\n Bengaluru 560001", EnvPlatformGSTIN: testGSTIN(t, "29")}
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	s, err := SupplierFromEnv(get(full), "production")
	if err != nil || !s.Complete() || s.StateCode != "29" || s.StateName != "Karnataka" || s.Address != "1 Example Road, Bengaluru 560001" {
		t.Fatalf("%+v %v", s, err)
	}
	for _, missing := range []string{EnvSupplierLegalName, EnvSupplierAddress, EnvPlatformGSTIN} {
		m := map[string]string{}
		for k, v := range full {
			if k != missing {
				m[k] = v
			}
		}
		m[missing] = "   "
		if _, err := SupplierFromEnv(get(m), "production"); !errors.Is(err, ErrSupplierRequired) {
			t.Errorf("production without %s: %v", missing, err)
		}
		if _, err := SupplierFromEnv(get(m), "dev"); err != nil {
			t.Errorf("dev without %s must start: %v", missing, err)
		}
	}
	bad := map[string]string{EnvPlatformGSTIN: "29AAAAA0000A1Z0X"}
	if _, err := SupplierFromEnv(get(bad), "dev"); !errors.Is(err, ErrPlatformGSTINInvalid) {
		t.Fatalf("a malformed GSTIN refuses anywhere: %v", err)
	}
}
