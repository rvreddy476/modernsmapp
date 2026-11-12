package invoice

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func premiumLikeDoc() ServiceDocument {
	return ServiceDocument{
		Kind: KindTaxInvoice, Number: "MP/26-27/000001", Date: "2026-11-13",
		Supplier:      ServiceParty{LegalName: "Example Pvt Ltd", Address: "1 Road, Hyderabad", GSTIN: "36AAAAA0000A1Z5", StateName: "Telangana", StateCode: "36"},
		RecipientName: "Asha <b>", PlaceOfSupplyName: "Telangana", PlaceOfSupplyCode: "36",
		Lines: []ServiceLine{{Description: "Momentum Premium — 30 days", SAC: "998439", Quantity: 1, TaxablePaise: 14900, RateBP: 1800,
			CGSTPaise: 1341, SGSTPaise: 1341, TotalPaise: 17582}},
		TaxablePaise: 14900, CGSTPaise: 1341, SGSTPaise: 1341, TotalPaise: 17582,
	}
}

func TestServiceDocumentRendersTheRule46Fields(t *testing.T) {
	out, err := RenderServiceHTML(premiumLikeDoc())
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{"Tax invoice", "MP/26-27/000001", "13 Nov 2026", "Example Pvt Ltd", "36AAAAA0000A1Z5",
		"Telangana (36)", "998439", "₹149.00", "₹13.41", "₹175.82", "9%",
		"Whether tax is payable on reverse charge basis: No", "Indian Rupees One Hundred Seventy-Five and Eighty-Two Paise Only",
		"Asha &lt;b&gt;"} {
		if !strings.Contains(s, want) {
			t.Errorf("rendered invoice lacks %q", want)
		}
	}
	if strings.Contains(s, "Postbook") || strings.Contains(s, "Returns") {
		t.Error("the service document carries commerce wording")
	}
}

func TestServiceCreditNoteNamesTheOriginal(t *testing.T) {
	d := premiumLikeDoc()
	d.Kind, d.Number = KindCreditNote, "MPC/26-27/000001"
	if _, err := RenderServiceHTML(d); !errors.Is(err, ErrServiceDocumentInvalid) {
		t.Fatalf("a credit note without its original: %v", err)
	}
	d.Original = &ServiceReference{Number: "MP/26-27/000001", Date: "2026-11-13"}
	out, err := RenderServiceHTML(d)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(out); !strings.Contains(s, "Credit note") || !strings.Contains(s, "Against tax invoice MP/26-27/000001 dated 13 Nov 2026") ||
		!strings.Contains(s, "Amount credited") {
		t.Fatalf("credit note: %s", s)
	}
}

func TestServiceDocumentRefusesWhatDoesNotAddUp(t *testing.T) {
	for name, mut := range map[string]func(*ServiceDocument){
		"line total":   func(d *ServiceDocument) { d.Lines[0].TotalPaise++ },
		"header total": func(d *ServiceDocument) { d.TotalPaise++ },
		// Line and header agree with each other but not with taxable + GST.
		"line identity": func(d *ServiceDocument) { d.Lines[0].TotalPaise++; d.TotalPaise++ },
		"igst and cgst": func(d *ServiceDocument) {
			d.Lines[0].IGSTPaise = 1
			d.Lines[0].TotalPaise++
			d.IGSTPaise = 1
			d.TotalPaise++
		},
		"negative":       func(d *ServiceDocument) { d.Lines[0].CGSTPaise = -1 },
		"long number":    func(d *ServiceDocument) { d.Number = "MPX/2026-27/000001" },
		"no number":      func(d *ServiceDocument) { d.Number = " " },
		"bad date":       func(d *ServiceDocument) { d.Date = "13/11/2026" },
		"no lines":       func(d *ServiceDocument) { d.Lines = nil },
		"unknown kind":   func(d *ServiceDocument) { d.Kind = "receipt" },
		"invoice w/ ref": func(d *ServiceDocument) { d.Original = &ServiceReference{Number: "X", Date: "2026-01-01"} },
	} {
		d := premiumLikeDoc()
		d.Lines = append([]ServiceLine(nil), d.Lines...)
		mut(&d)
		if err := d.Validate(); !errors.Is(err, ErrServiceDocumentInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := premiumLikeDoc().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestShortFinancialYearTurnsOnFirstApril(t *testing.T) {
	ist := time.FixedZone("IST", 19800)
	for _, c := range []struct {
		t    time.Time
		want string
	}{
		{time.Date(2027, 3, 31, 23, 59, 59, 0, ist), "26-27"},
		{time.Date(2027, 4, 1, 0, 0, 0, 0, ist), "27-28"},
		{time.Date(2099, 4, 1, 0, 0, 0, 0, ist), "99-00"},
		{time.Date(2026, 11, 13, 0, 0, 0, 0, ist), "26-27"},
	} {
		if got := ShortFinancialYear(c.t); got != c.want {
			t.Errorf("%v: %s, want %s", c.t, got, c.want)
		}
	}
}

func TestRupeesAndWords(t *testing.T) {
	for p, want := range map[int64]string{0: "₹0.00", 5: "₹0.05", 17582: "₹175.82", 176882: "₹1,768.82", 14990000: "₹1,49,900.00",
		1234567890: "₹1,23,45,678.90", -17582: "-₹175.82"} {
		if got := Rupees(p); got != want {
			t.Errorf("Rupees(%d) = %s, want %s", p, got, want)
		}
	}
	for p, want := range map[int64]string{
		17582:      "Indian Rupees One Hundred Seventy-Five and Eighty-Two Paise Only",
		176882:     "Indian Rupees One Thousand Seven Hundred Sixty-Eight and Eighty-Two Paise Only",
		10000000:   "Indian Rupees One Lakh Only",
		1234567890: "Indian Rupees One Crore Twenty-Three Lakh Forty-Five Thousand Six Hundred Seventy-Eight and Ninety Paise Only",
		-1500:      "Indian Rupees Fifteen Only",
		0:          "Indian Rupees Zero Only",
	} {
		if got := AmountInWords(p); got != want {
			t.Errorf("AmountInWords(%d) = %q, want %q", p, got, want)
		}
	}
	if RatePercent(1800) != "18" || RatePercent(900) != "9" || RatePercent(250) != "2.5" || RatePercent(0) != "0" {
		t.Error("RatePercent")
	}
	if ServiceFilename("MP/26-27/000001", ".html") != "MP_26-27_000001.html" {
		t.Error("ServiceFilename")
	}
}
